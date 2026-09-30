package sip

import (
	"sync"
	"testing"
)

func TestRingTimeoutNeverEndsAnsweredOrAnsweringCall(t *testing.T) {
	for _, state := range []string{"answering", "active", "ended"} {
		s := &Server{}
		sess := newTestSession("in-long-call", "inbound", state)
		s.sessions.Store(sess.ID, sess)
		s.expireInboundRinging(sess)
		if sess.State() != state {
			t.Fatalf("timeout changed %s call to %s", state, sess.State())
		}
		if _, ok := s.sessions.Load(sess.ID); !ok {
			t.Fatalf("timeout removed %s call", state)
		}
	}
}

func TestRingTimeoutClaimsOnlyUnansweredInboundCall(t *testing.T) {
	sess := newTestSession("ringing", "inbound", "init")
	if !sess.claimRingTimeout() {
		t.Fatal("unanswered call did not expire")
	}
	if sess.claimRingTimeout() || sess.beginAnswer() {
		t.Fatal("expired call claimed again or answered")
	}
	if !sess.RingExpired() || sess.State() != "init" {
		t.Fatal("timeout did not preserve CANCEL state")
	}
	out := newTestSession("dialing", "outbound", "init")
	if out.claimRingTimeout() {
		t.Fatal("inbound timer claimed outbound call")
	}
}

func TestPickupAndRingTimeoutCannotBothWin(t *testing.T) {
	for i := 0; i < 1000; i++ {
		sess := newTestSession("race", "inbound", "init")
		var answer, expired bool
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); answer = sess.beginAnswer() }()
		go func() { defer wg.Done(); expired = sess.claimRingTimeout() }()
		wg.Wait()
		if answer == expired {
			t.Fatalf("answer=%v, expired=%v", answer, expired)
		}
		sess.cancel()
	}
}
