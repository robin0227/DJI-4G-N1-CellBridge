package sip

import (
	"net"
	"strings"
	"testing"
	"time"
)

func TestFailedInviteResponseACKSurvivesDeletedSession(t *testing.T) {
	s, client, addr := testUDPPair(t)
	s.rememberInviteTransaction(clientInvite, addr)
	response := buildResponse(clientInvite, 487, "Request Terminated", "", "", "client-tag")
	for i := 0; i < 2; i++ {
		s.handleResponse(response, addr)
		ack := readSIPTest(t, client)
		if !strings.HasPrefix(ack, "ACK "+requestURIOf(clientInvite)+" SIP/2.0") || viaBranchOf(ack) != viaBranchOf(clientInvite) || parseHeader(ack, "To") != parseHeader(response, "To") || parseHeader(ack, "CSeq") != "1 ACK" {
			t.Fatal(ack)
		}
	}
}

func TestDuplicateSuccessfulAnswerAlwaysACKed(t *testing.T) {
	s, client, addr := testUDPPair(t)
	sess := newTestSession(parseHeader(clientInvite, "Call-ID"), "inbound", "active")
	sess.RememberInvite(inviteAttempt{addr: addr, req: clientInvite})
	s.sessions.Store(sess.ID, sess)
	response := buildResponse(clientInvite, 200, "OK", "Contact: <sip:iphone@127.0.0.1:6000>\r\n", "", "client-tag")
	for i := 0; i < 2; i++ {
		s.handleResponse(response, addr)
		ack := readSIPTest(t, client)
		if !strings.HasPrefix(ack, "ACK sip:iphone@127.0.0.1:6000 SIP/2.0") {
			t.Fatal(ack)
		}
		if sess.State() != "active" {
			t.Fatal("retransmission changed active state")
		}
	}
}

func TestUnrelatedResponseCannotHangUpCall(t *testing.T) {
	s, client, addr := testUDPPair(t)
	sess := newTestSession(parseHeader(clientInvite, "Call-ID"), "inbound", "active")
	sess.RememberInvite(inviteAttempt{addr: addr, req: clientInvite})
	s.sessions.Store(sess.ID, sess)
	response := buildResponse(clientInvite, 500, "Server Error", "", "", "client-tag")
	for _, invalid := range []string{strings.Replace(response, "1 INVITE", "2 BYE", 1), strings.Replace(response, viaBranchOf(clientInvite), "z9hG4bKspoof", 1)} {
		s.handleResponse(invalid, addr)
	}
	s.handleResponse(response, &net.UDPAddr{IP: addr.IP, Port: addr.Port + 1})
	if sess.State() != "active" {
		t.Fatal("unrelated response hung up active call")
	}
	_ = client.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if _, _, err := client.ReadFromUDP(make([]byte, 4096)); err == nil {
		t.Fatal("unrelated response got ACK")
	}
}

func TestInviteTransactionCacheExpires(t *testing.T) {
	s, _, addr := testUDPPair(t)
	s.inviteTransactions.Store(transactionKey("old", addr), inviteTransaction{clientInvite, time.Now().Add(-time.Second)})
	s.pruneInviteTransactions()
	if _, ok := s.inviteTransactions.Load(transactionKey("old", addr)); ok {
		t.Fatal("expired transaction retained")
	}
}

func TestLateAnswerACKThenBYE(t *testing.T) {
	s, client, addr := testUDPPair(t)
	s.rememberInviteTransaction(clientInvite, addr)
	response := buildResponse(clientInvite, 200, "OK", "Contact: <sip:iphone@127.0.0.1:6000>\r\n", "", "client-tag")
	s.handleResponse(response, addr)
	if !strings.HasPrefix(readSIPTest(t, client), "ACK ") {
		t.Fatal("late answer not ACKed")
	}
	if !strings.HasPrefix(readSIPTest(t, client), "BYE ") {
		t.Fatal("late answer left silent dialog open")
	}
}
