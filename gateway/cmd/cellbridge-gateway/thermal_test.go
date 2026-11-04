package main

import (
	"testing"
	"time"
)

func TestThermalLogThrottle(t *testing.T) {
	var throttle thermalLogThrottle
	start := time.Now()
	steps := []struct {
		after time.Duration
		c     float64
		want  bool
	}{
		{0, 30, true},
		{30 * time.Second, 31, false},
		{time.Minute, 32, false},
		{90 * time.Second, 33, true},
		{2 * time.Minute, 31, false},
		{2*time.Minute + thermalLogEvery, 31, true},
	}
	for _, step := range steps {
		if got := throttle.shouldLog(step.c, start.Add(step.after)); got != step.want {
			t.Fatalf("after %s at %.0f°C: shouldLog = %v, want %v", step.after, step.c, got, step.want)
		}
	}
}
