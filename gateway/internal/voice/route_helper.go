package voice

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

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
	output, err := exec.CommandContext(ctx, helper, "start").CombinedOutput()
	if err != nil {
		return fmt.Errorf("prepare voice route: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
