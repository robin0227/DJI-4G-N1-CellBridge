package sip

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cellbridge/cellbridge/gateway/internal/modem"
	"github.com/pion/rtp"
)

func TestForwardSMSAddressesRegisteredContact(t *testing.T) {
	clientConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()
	clientAddr := clientConn.LocalAddr().(*net.UDPAddr)

	gwConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer gwConn.Close()

	reg := NewRegistrar()
	reg.RegisterFrom("iphone", "<sip:iphone-rand123@192.168.10.50:55186>;expires=300", "UDP", 300, clientAddr)
	s := &Server{conn: gwConn, registrar: reg, ctx: context.Background()}
	s.ForwardSMS("10086", "Your code is 8888")

	_ = clientConn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 4096)
	n, _, err := clientConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("no forwarded MESSAGE received: %v", err)
	}
	msg := string(buf[:n])
	firstLine := strings.SplitN(msg, "\r\n", 2)[0]
	if firstLine != "MESSAGE sip:iphone-rand123@192.168.10.50:55186 SIP/2.0" {
		t.Fatalf("request line = %q, want registered Contact URI", firstLine)
	}
	if !strings.Contains(msg, "Your code is 8888") {
		t.Fatalf("SMS body missing in %q", msg)
	}
}

func TestRTPDTMFDecodesAndDeduplicatesRFC4733(t *testing.T) {
	m, err := NewMediaSession("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := m.SetRemote(client.LocalAddr().String()); err != nil {
		t.Fatal(err)
	}

	digits := make(chan rune, 8)
	m.OnDTMF(func(d rune) { digits <- d })
	m.OnPCMUFrame(func([]byte) { t.Error("RFC 4733 event leaked to PCMU callback") })

	sendEvent := func(code byte, ts uint32, end bool) {
		flags := byte(10)
		if end {
			flags |= 0x80
		}
		pkt := &rtp.Packet{
			Header:  rtp.Header{Version: 2, PayloadType: 101, Timestamp: ts},
			Payload: []byte{code, flags, 0x00, 0xA0},
		}
		data, err := pkt.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.WriteToUDP(data, m.LocalAddr()); err != nil {
			t.Fatal(err)
		}
	}
	// Three retransmissions of digit '5' with identical RTP timestamp -> 1 callback.
	sendEvent(5, 1600, false)
	sendEvent(5, 1600, true)
	sendEvent(5, 1600, true)
	// Next digit '#' (code 11) with new timestamp -> 2nd callback.
	sendEvent(11, 3200, true)

	got := make([]rune, 0, 2)
	deadline := time.After(time.Second)
	for len(got) < 2 {
		select {
		case d := <-digits:
			got = append(got, d)
		case <-deadline:
			t.Fatalf("got digits %q, want \"5#\"", string(got))
		}
	}
	if string(got) != "5#" {
		t.Fatalf("got digits %q, want \"5#\"", string(got))
	}
	if rx, _ := m.Stats(); rx != 0 {
		t.Fatalf("PCMU rx = %d, want 0 for DTMF packets", rx)
	}
}

func TestSymmetricRTPLatchesNATPort(t *testing.T) {
	m, err := NewMediaSession("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	// SDP advertised port 19999, but real RTP arrives from client's ephemeral port.
	if err := m.SetRemote("127.0.0.1:19999"); err != nil {
		t.Fatal(err)
	}
	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	got := make(chan struct{}, 1)
	m.OnPCMUFrame(func([]byte) { got <- struct{}{} })
	pkt := &rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 0, SequenceNumber: 1}, Payload: make([]byte, 160)}
	data, _ := pkt.Marshal()
	_, _ = client.WriteToUDP(data, m.LocalAddr())

	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatal("PCMU frame not received")
	}
	wantPort := client.LocalAddr().(*net.UDPAddr).Port
	if m.RemoteAddr().Port != wantPort {
		t.Fatalf("latched port = %d, want %d", m.RemoteAddr().Port, wantPort)
	}
}

func TestParseInfoDTMF(t *testing.T) {
	relay := "INFO sip:cellbridge@127.0.0.1 SIP/2.0\r\nContent-Type: application/dtmf-relay\r\n\r\nSignal=9\r\nDuration=160\r\n"
	if d, ok := parseInfoDTMF(relay); !ok || d != '9' {
		t.Fatalf("parseInfoDTMF(relay) = (%q, %v), want ('9', true)", d, ok)
	}
	plain := "INFO sip:cellbridge@127.0.0.1 SIP/2.0\r\nContent-Type: application/dtmf\r\n\r\n*"
	if d, ok := parseInfoDTMF(plain); !ok || d != '*' {
		t.Fatalf("parseInfoDTMF(plain) = (%q, %v), want ('*', true)", d, ok)
	}
}

func TestInviteFromUnregisteredKnownUserGets401Challenge(t *testing.T) {
	s, client, addr := testUDPPair(t)
	s.auth = NewAuth("cellbridge")
	s.auth.AddUser("iphone", "secret")
	invite := "INVITE sip:10086@127.0.0.1 SIP/2.0\r\nVia: SIP/2.0/UDP 127.0.0.1;branch=z9hG4bK1\r\nFrom: <sip:iphone@127.0.0.1>;tag=a\r\nTo: <sip:10086@127.0.0.1>\r\nCall-ID: inv-unreg\r\nCSeq: 1 INVITE\r\nContent-Length: 0\r\n\r\n"
	s.handleInvite(invite, addr)
	resp := readSIPTest(t, client)
	if !strings.HasPrefix(resp, "SIP/2.0 401") || !strings.Contains(resp, "WWW-Authenticate:") {
		t.Fatalf("expected 401 Digest challenge for unregistered known user, got:\n%s", resp)
	}

	unknown := strings.ReplaceAll(invite, "sip:iphone@", "sip:intruder@")
	s.handleInvite(unknown, addr)
	resp = readSIPTest(t, client)
	if !strings.HasPrefix(resp, "SIP/2.0 403") {
		t.Fatalf("expected 403 Forbidden for unknown user, got:\n%s", resp)
	}
}

type prewarmAudio struct {
	modem.VoiceAudio
	called atomic.Int32
}

func (p *prewarmAudio) PrepareRoute(context.Context) error {
	p.called.Add(1)
	return nil
}

func TestRingClientsPrewarmsAudioRoute(t *testing.T) {
	s, client, addr := testUDPPair(t)
	s.registrar.RegisterFrom("iphone", "<sip:iphone@"+addr.String()+">;expires=300", "UDP", 300, addr)
	audio := &prewarmAudio{}
	s.audio = audio
	s.ringClients(modem.ModemEvent{Kind: "incoming", CallID: modem.CallID("prewarm-1"), Peer: "10086"})
	_ = readSIPTest(t, client)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if audio.called.Load() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("ringClients did not pre-warm the voice route")
}
