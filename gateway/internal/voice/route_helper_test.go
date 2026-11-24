package voice

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestExternalRouteCacheReusesFreshRotationAndResetsAfterConsume(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "count")
	helper := filepath.Join(dir, "route-helper")
	script := "#!/bin/sh\nprintf 'x' >> \"$ROUTE_COUNTER\"\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CELLBRIDGE_ROUTE_HELPER", helper)
	t.Setenv("ROUTE_COUNTER", counter)

	now := time.Unix(1000, 0)
	cache := &externalRouteCache{now: func() time.Time { return now }}

	if err := cache.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Second call within 30s before consumption must hit the fresh cache.
	now = now.Add(5 * time.Second)
	if err := cache.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(counter)
	if len(got) != 1 {
		t.Fatalf("helper invocations before consume = %d, want 1", len(got))
	}

	// Once audio Start consumes the route, the next call must rotate fresh.
	cache.MarkConsumed()
	if err := cache.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(counter)
	if len(got) != 2 {
		t.Fatalf("helper invocations after consume = %d, want 2", len(got))
	}

	// After the 30s freshness window expires, rotate fresh again.
	now = now.Add(31 * time.Second)
	if err := cache.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(counter)
	if len(got) != 3 {
		t.Fatalf("helper invocations after expiry = %d, want 3", len(got))
	}
}

func TestExternalRouteCacheCoalescesConcurrentCalls(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "count")
	helper := filepath.Join(dir, "route-helper")
	script := "#!/bin/sh\nsleep 0.1\nprintf 'x' >> \"$ROUTE_COUNTER\"\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CELLBRIDGE_ROUTE_HELPER", helper)
	t.Setenv("ROUTE_COUNTER", counter)

	cache := &externalRouteCache{}
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range errs {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = cache.Prepare(context.Background())
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d error: %v", i, err)
		}
	}
	got, _ := os.ReadFile(counter)
	if len(got) != 1 {
		t.Fatalf("concurrent helper invocations = %d, want 1", len(got))
	}
}

func TestALSACaptureSizingDefaultsAndOverrides(t *testing.T) {
	t.Setenv("CELLBRIDGE_ALSA_CAPTURE_PERIOD", "")
	t.Setenv("CELLBRIDGE_ALSA_CAPTURE_BUFFER", "")
	p, b := alsaCaptureSizing()
	if p != defaultCapturePeriod || b != defaultCaptureBuffer {
		t.Fatalf("defaults = (%d,%d), want (%d,%d)", p, b, defaultCapturePeriod, defaultCaptureBuffer)
	}
	t.Setenv("CELLBRIDGE_ALSA_CAPTURE_PERIOD", "160")
	t.Setenv("CELLBRIDGE_ALSA_CAPTURE_BUFFER", "1280")
	p, b = alsaCaptureSizing()
	if p != 160 || b != 1280 {
		t.Fatalf("overrides = (%d,%d), want (160,1280)", p, b)
	}
}
