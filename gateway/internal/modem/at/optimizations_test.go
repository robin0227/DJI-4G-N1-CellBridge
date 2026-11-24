package at

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHandleURCSignalsSMSNotifyOnCMTI(t *testing.T) {
	adapter := NewAdapter(nil)
	adapter.HandleURC(`+CMTI: "MT",3`)
	select {
	case <-adapter.SMSNotify():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected SMSNotify signal on +CMTI URC")
	}
}

type failingATHPort struct {
	mu     sync.Mutex
	read   bytes.Buffer
	writes []string
}

func (p *failingATHPort) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cmd := strings.TrimSpace(string(data))
	p.writes = append(p.writes, cmd)
	switch {
	case cmd == "ATH":
		p.read.WriteString("\r\nERROR\r\n")
	default:
		p.read.WriteString("\r\nOK\r\n")
	}
	return len(data), nil
}

func (p *failingATHPort) Read(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.read.Len() == 0 {
		return 0, io.EOF
	}
	return p.read.Read(data)
}

func (p *failingATHPort) Close() error { return nil }

func TestHangupFallsBackToCHUPAndClearsStateOnError(t *testing.T) {
	port := &failingATHPort{}
	adapter := NewAdapter(NewClient(port))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	callID, err := adapter.Dial(ctx, "10086")
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Hangup(ctx, callID); err != nil {
		t.Fatalf("expected AT+CHUP fallback to succeed, got %v", err)
	}
	foundCHUP := false
	for _, w := range port.writes {
		if w == "AT+CHUP" {
			foundCHUP = true
		}
	}
	if !foundCHUP {
		t.Fatalf("expected AT+CHUP fallback in writes: %v", port.writes)
	}
	// Active call state must be cleared so a subsequent dial succeeds.
	if _, err := adapter.Dial(ctx, "10010"); err != nil {
		t.Fatalf("second dial failed after hangup fallback: %v", err)
	}
}

func TestNormalizeRegistrationHandlesExtendedCEREG(t *testing.T) {
	cases := map[string]string{
		`+CEREG: 0,1`:                         "registered",
		`+CEREG: 0,5`:                         "registered",
		`+CEREG: 2,1,"1A2B","01234567",7`:     "registered",
		`+CEREG: 2,2`:                         "searching",
		`+CEREG: 0,3`:                         "denied",
		`+CEREG: 0,0`:                         "unknown",
	}
	for input, want := range cases {
		if got := normalizeRegistration(input); got != want {
			t.Errorf("normalizeRegistration(%q) = %q, want %q", input, got, want)
		}
	}
}
