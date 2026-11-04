package sip

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/cellbridge/cellbridge/gateway/internal/modem"
)

// End-to-end check of the inbound path: a registered client must actually
// receive an INVITE when the modem reports an incoming call. Before the
// contactAddr fix this test fails with "no INVITE received".
func TestRingClientsSendsInviteToRegisteredClient(t *testing.T) {
	clientConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()
	clientAddr := clientConn.LocalAddr().(*net.UDPAddr)

	gwConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer gwConn.Close()

	reg := NewRegistrar()
	reg.Register("iphone", fmt.Sprintf("<sip:iphone@%s>;expires=300", clientAddr.String()), "UDP", 300)

	s := &Server{conn: gwConn, registrar: reg, ctx: context.Background()}
	s.ringClients(modem.ModemEvent{Kind: "incoming", CallID: modem.CallID("test-call-0001"), Peer: "13800138000"})

	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, _, err := clientConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("no INVITE received: %v", err)
	}
	msg := string(buf[:n])
	requestLine := strings.SplitN(msg, "\r\n", 2)[0]
	if want := fmt.Sprintf("INVITE sip:iphone@%s SIP/2.0", clientAddr.String()); requestLine != want {
		t.Errorf("request line = %q, want %q", requestLine, want)
	}
	if !strings.Contains(msg, "Call-ID: in-test-call-0001") {
		t.Errorf("missing Call-ID in %q", msg)
	}
	if !strings.Contains(msg, "m=audio") {
		t.Errorf("missing SDP body in %q", msg)
	}
	if !strings.Contains(msg, "13800138000") {
		t.Errorf("caller number missing from INVITE in %q", msg)
	}
}

// A single cellular call rings several times; only the first RING may open
// a session, otherwise the client gets a burst of parallel INVITEs.
func TestRingClientsDeduplicatesRepeatRings(t *testing.T) {
	clientConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()
	clientAddr := clientConn.LocalAddr().(*net.UDPAddr)

	gwConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer gwConn.Close()

	reg := NewRegistrar()
	reg.Register("iphone", fmt.Sprintf("<sip:iphone@%s>;expires=300", clientAddr.String()), "UDP", 300)
	s := &Server{conn: gwConn, registrar: reg, ctx: context.Background()}

	event := modem.ModemEvent{Kind: "incoming", CallID: modem.CallID("dup-call-0001"), Peer: "10010"}
	s.ringClients(event)
	s.ringClients(event)
	s.ringClients(event)

	_ = clientConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 4096)
	count := 0
	for {
		if _, _, err := clientConn.ReadFromUDP(buf); err != nil {
			break
		}
		count++
	}
	if count != 1 {
		t.Errorf("got %d INVITEs for one incoming call, want 1", count)
	}
}

// YakPhone registers a per-instance contact user and rejects an INVITE whose
// Request-URI names only the account (404 → the caller hears "busy").
func TestRingClientsAddressesRegisteredContact(t *testing.T) {
	clientConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()
	clientAddr := clientConn.LocalAddr().(*net.UDPAddr)
	gwConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer gwConn.Close()

	reg := NewRegistrar()
	// The phone advertises its private address; the packet arrives via NAT.
	contact := "<sip:iphone-2YbUkOF3XmCJSoS@192.168.10.137:55186>;expires=300"
	reg.RegisterFrom("iphone", contact, "UDP", 300, clientAddr)
	s := &Server{conn: gwConn, registrar: reg, ctx: context.Background()}
	s.ringClients(modem.ModemEvent{Kind: "incoming", CallID: modem.CallID("contact-call-01"), Peer: "10086"})

	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, _, err := clientConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("no INVITE received at the NAT source address: %v", err)
	}
	msg := string(buf[:n])
	if requestLine := strings.SplitN(msg, "\r\n", 2)[0]; requestLine != "INVITE sip:iphone-2YbUkOF3XmCJSoS@192.168.10.137:55186 SIP/2.0" {
		t.Errorf("request line = %q, want the registered contact", requestLine)
	}
	if !strings.Contains(msg, "\r\nTo: <sip:iphone@") {
		t.Errorf("To header must stay the account address of record in %q", msg)
	}
}
