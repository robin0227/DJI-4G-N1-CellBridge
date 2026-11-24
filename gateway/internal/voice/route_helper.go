package voice

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const routeFreshWindow = 30 * time.Second

type inFlightRoute struct {
	done chan struct{}
	err  error
}

// externalRouteCache deduplicates and coalesces external route-helper runs
// for a single audio adapter. Pre-warming the route while an inbound call is
// ringing and then answering seconds later reuses the same fresh rotation
// instead of spawning a second helper; once Start consumes the route, the
// next call always rotates fresh.
type externalRouteCache struct {
	mu         sync.Mutex
	inFlight   *inFlightRoute
	preparedAt time.Time
	consumed   bool
	now        func() time.Time
}

func (c *externalRouteCache) currentTime() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// MarkConsumed records that the audio device has been opened since the last
// route rotation, so the next call must rotate fresh.
func (c *externalRouteCache) MarkConsumed() {
	c.mu.Lock()
	c.consumed = true
	c.mu.Unlock()
}

func (c *externalRouteCache) Prepare(ctx context.Context) error {
	helper := strings.TrimSpace(os.Getenv("CELLBRIDGE_ROUTE_HELPER"))
	if helper == "" {
		return nil
	}
	if !filepath.IsAbs(helper) {
		return fmt.Errorf("CELLBRIDGE_ROUTE_HELPER must be an absolute path")
	}

	c.mu.Lock()
	if !c.consumed && !c.preparedAt.IsZero() && c.currentTime().Sub(c.preparedAt) < routeFreshWindow {
		c.mu.Unlock()
		return nil
	}
	if flight := c.inFlight; flight != nil {
		c.mu.Unlock()
		select {
		case <-flight.done:
			return flight.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	flight := &inFlightRoute{done: make(chan struct{})}
	c.inFlight = flight
	c.mu.Unlock()

	err := runExternalRouteHelper(ctx, helper)

	c.mu.Lock()
	flight.err = err
	if err == nil {
		c.preparedAt = c.currentTime()
		c.consumed = false
	}
	if c.inFlight == flight {
		c.inFlight = nil
	}
	close(flight.done)
	c.mu.Unlock()
	return err
}

// prepareExternalRoute lets platform integrations refresh the modem-side
// voice route immediately before dialing. The helper is optional, but when it
// is configured it must be an absolute path so a service cannot accidentally
// execute a binary selected through PATH.
func prepareExternalRoute(ctx context.Context) error {
	helper := strings.TrimSpace(os.Getenv("CELLBRIDGE_ROUTE_HELPER"))
	if helper == "" {
		return nil
	}
	if !filepath.IsAbs(helper) {
		return fmt.Errorf("CELLBRIDGE_ROUTE_HELPER must be an absolute path")
	}
	return runExternalRouteHelper(ctx, helper)
}

func runExternalRouteHelper(ctx context.Context, helper string) error {
	output, err := exec.CommandContext(ctx, helper, "start").CombinedOutput()
	if err != nil {
		return fmt.Errorf("prepare voice route: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
