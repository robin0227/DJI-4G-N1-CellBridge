package sms

import (
	"context"
	"encoding/hex"
	"sync"
	"testing"
	"time"

	"github.com/cellbridge/cellbridge/gateway/internal/db"
	"github.com/cellbridge/cellbridge/gateway/internal/modem"
)

type notifyingInboxModem struct {
	mu      sync.Mutex
	items   []modem.RawSMS
	deleted []string
	notify  chan struct{}
}

func (m *notifyingInboxModem) SendSMS(context.Context, string, modem.SMSPayload) (modem.SMSID, error) {
	return "", nil
}

func (m *notifyingInboxModem) SMSNotify() <-chan struct{} { return m.notify }

func (m *notifyingInboxModem) ListSMS(_ context.Context, cursor modem.SMSCursor) ([]modem.RawSMS, modem.SMSCursor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]modem.RawSMS(nil), m.items...)
	return out, cursor, nil
}

func (m *notifyingInboxModem) DeleteSMS(_ context.Context, idx string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted = append(m.deleted, idx)
	m.items = nil
	return nil
}

func TestEngineRunWakesImmediatelyOnSMSNotify(t *testing.T) {
	database := openTestDB(t)
	segments, err := EncodeSubmitSegments("10086", "Code 123456", 1)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := hex.DecodeString(segments[0].PDU)
	if err != nil {
		t.Fatal(err)
	}
	inbox := &notifyingInboxModem{notify: make(chan struct{}, 1)}
	engine := NewEngine(database, inbox)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	received := make(chan *db.Message, 1)
	go func() {
		_ = engine.Run(ctx, time.Hour, func(msg *db.Message) { received <- msg })
	}()
	// Wait for initial empty poll, then inject SMS and trigger +CMTI notify.
	time.Sleep(50 * time.Millisecond)
	inbox.mu.Lock()
	inbox.items = []modem.RawSMS{{ModemStorage: "MT", ModemIndex: 4, RawPDU: raw}}
	inbox.mu.Unlock()
	inbox.notify <- struct{}{}

	select {
	case msg := <-received:
		if msg.Body != "Code 123456" {
			t.Fatalf("body = %q, want Code 123456", msg.Body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("engine did not wake up on SMSNotify within 2s (1h fallback interval)")
	}
}
