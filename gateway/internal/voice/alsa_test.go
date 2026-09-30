package voice

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenALSARequiresBothDevices(t *testing.T) {
	if _, err := OpenALSA("", "hw:0,0"); err == nil {
		t.Fatal("empty capture device was accepted")
	}
	if _, err := OpenALSA("hw:0,0", ""); err == nil {
		t.Fatal("empty playback device was accepted")
	}
}

func TestALSAProbeRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	audio, err := OpenALSA("hw:0,0", "hw:0,0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := audio.Probe(ctx); err == nil {
		t.Fatal("canceled probe succeeded")
	}
}

func TestALSAPrepareRouteRunsConfiguredHelper(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "called")
	helper := filepath.Join(dir, "route-helper")
	script := "#!/bin/sh\nprintf '%s' \"$1\" > \"$ROUTE_MARKER\"\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CELLBRIDGE_ROUTE_HELPER", helper)
	t.Setenv("ROUTE_MARKER", marker)

	audio, err := OpenALSA("hw:0,0", "hw:0,0")
	if err != nil {
		t.Fatal(err)
	}
	if err := audio.PrepareRoute(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "start" {
		t.Fatalf("helper argument = %q, want start", got)
	}
}
