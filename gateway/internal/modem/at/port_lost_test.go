package at

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestRunReportsRemovedSerialDevice(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	// A closed writer makes every read return 0 bytes, which is how a
	// removed USB TTY looks to the non-blocking idle poll.
	_ = writer.Close()
	client := NewClient(reader)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Run(ctx); !errors.Is(err, ErrPortLost) {
		t.Fatalf("Run error = %v, want ErrPortLost", err)
	}
}

func TestRunIgnoresIdleFakePort(t *testing.T) {
	client := NewClient(&scriptedPort{})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := client.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run error = %v, want context deadline", err)
	}
}
