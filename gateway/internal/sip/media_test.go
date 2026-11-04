package sip

import (
	"net"
	"testing"
	"time"

	"github.com/pion/rtp"
)

func TestMediaRejectsWrongPayloadTypeAndClosesCleanly(t *testing.T) {
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
	frames := make(chan []byte, 3)
	m.OnPCMUFrame(func(frame []byte) { frames <- frame })
	for _, pt := range []uint8{101, 0} {
		packet := &rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: pt}, Payload: make([]byte, 160)}
		data, err := packet.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.WriteToUDP(data, m.LocalAddr()); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-frames:
	case <-time.After(time.Second):
		t.Fatal("PCMU not delivered")
	}
	select {
	case <-frames:
		t.Fatal("non-PCMU frame delivered as audio")
	case <-time.After(20 * time.Millisecond):
	}
	if rx, _ := m.Stats(); rx != 1 {
		t.Fatalf("rx=%d, want 1", rx)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMediaSessionUsesIPv4Socket(t *testing.T) {
	session, err := NewMediaSession("0.0.0.0:0")
	if err != nil {
		t.Fatalf("NewMediaSession: %v", err)
	}
	defer session.Close()

	if got := session.LocalAddr().IP; got.To4() == nil {
		t.Fatalf("media socket is not IPv4: %v", got)
	}
	if got := session.LocalAddr().Network(); got != "udp" {
		t.Fatalf("unexpected media network: %q", got)
	}
}

func TestMediaSessionRejectsIPv6Remote(t *testing.T) {
	session, err := NewMediaSession("0.0.0.0:0")
	if err != nil {
		t.Fatalf("NewMediaSession: %v", err)
	}
	defer session.Close()

	if err := session.SetRemote("[::1]:9000"); err == nil {
		t.Fatal("SetRemote accepted an IPv6 address for an IPv4 media socket")
	}
}

// Packets the phone sent before the bridge started are stale and must not
// reach the jitter buffer as one burst.
func TestMediaDiscardsRTPQueuedBeforeReaderStarts(t *testing.T) {
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
	send := func(seq uint16) {
		packet := &rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 0, SequenceNumber: seq}, Payload: make([]byte, 160)}
		data, err := packet.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.WriteToUDP(data, m.LocalAddr()); err != nil {
			t.Fatal(err)
		}
	}
	for seq := uint16(1); seq <= 20; seq++ {
		send(seq)
	}
	time.Sleep(20 * time.Millisecond)
	got := make(chan uint16, 32)
	m.OnSequencedPCMUFrame(func(seq uint16, _ []byte) { got <- seq })
	send(21)
	select {
	case seq := <-got:
		if seq != 21 {
			t.Fatalf("first delivered seq = %d, want 21 (stale backlog leaked)", seq)
		}
	case <-time.After(time.Second):
		t.Fatal("fresh packet not delivered after discarding backlog")
	}
}
