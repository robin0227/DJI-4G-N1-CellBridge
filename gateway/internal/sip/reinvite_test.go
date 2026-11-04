package sip

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func reinviteFixture(t *testing.T) (*Server, *net.UDPConn, *MediaSession) {
	t.Helper()
	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	gw, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { gw.Close() })
	media, err := NewMediaSession("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { media.Close() })
	if err := media.SetRemote("127.0.0.1:1320"); err != nil {
		t.Fatal(err)
	}
	s := &Server{conn: gw, registrar: NewRegistrar(), ctx: context.Background()}
	s.sessions.Store("in-call-1", &SIPCallSession{ID: "in-call-1", media: media, state: "active"})
	return s, client, media
}

func readSIP(t *testing.T, conn *net.UDPConn) string {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 4096)
	n, _, err := conn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("no response: %v", err)
	}
	return string(buf[:n])
}

// baresip re-INVITEs right after an inbound call connects; the gateway must
// answer 200 OK with SDP, never 180 Ringing.
func TestReInviteInsideCallGets200WithSDP(t *testing.T) {
	s, client, media := reinviteFixture(t)
	sdp := "v=0\r\no=- 1 2 IN IP4 127.0.0.1\r\ns=-\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 1400 RTP/AVP 0\r\na=rtpmap:0 PCMU/8000\r\na=sendrecv\r\n"
	msg := fmt.Sprintf("INVITE sip:cellbridge@127.0.0.1:5060 SIP/2.0\r\nVia: SIP/2.0/UDP 127.0.0.1;branch=z9hG4bKre1\r\nFrom: <sip:iphone@127.0.0.1>;tag=phone\r\nTo: <sip:10086@127.0.0.1>;tag=cbcall1\r\nCall-ID: in-call-1\r\nCSeq: 52941 INVITE\r\nContent-Type: application/sdp\r\nContent-Length: %d\r\n\r\n%s", len(sdp), sdp)
	s.handleMessage(msg, client.LocalAddr().(*net.UDPAddr))
	resp := readSIP(t, client)
	if !strings.HasPrefix(resp, "SIP/2.0 200 OK") {
		t.Fatalf("re-INVITE answered %q, want 200 OK", strings.SplitN(resp, "\r\n", 2)[0])
	}
	if !strings.Contains(resp, "CSeq: 52941 INVITE") || !strings.Contains(resp, "m=audio") || !strings.Contains(resp, "a=sendrecv") {
		t.Fatalf("200 OK missing CSeq or SDP answer:\n%s", resp)
	}
	if strings.Count(resp, "tag=cbcall1") != 1 {
		t.Fatalf("To tag must be kept exactly once:\n%s", resp)
	}
	if got := media.RemoteAddr().Port; got != 1400 {
		t.Fatalf("RTP target port = %d, want the re-INVITE's 1400", got)
	}
}

func TestReInviteHoldIsMirrored(t *testing.T) {
	s, client, _ := reinviteFixture(t)
	sdp := "v=0\r\nc=IN IP4 127.0.0.1\r\nm=audio 1320 RTP/AVP 0\r\na=sendonly\r\n"
	msg := fmt.Sprintf("UPDATE sip:cellbridge@127.0.0.1 SIP/2.0\r\nVia: SIP/2.0/UDP 127.0.0.1;branch=z9hG4bKup1\r\nFrom: <sip:iphone@127.0.0.1>;tag=phone\r\nTo: <sip:10086@127.0.0.1>;tag=cbcall1\r\nCall-ID: in-call-1\r\nCSeq: 3 UPDATE\r\nContent-Length: %d\r\n\r\n%s", len(sdp), sdp)
	s.handleMessage(msg, client.LocalAddr().(*net.UDPAddr))
	if resp := readSIP(t, client); !strings.HasPrefix(resp, "SIP/2.0 200 OK") || !strings.Contains(resp, "a=recvonly") {
		t.Fatalf("hold UPDATE answer:\n%s", resp)
	}
}

func TestUnknownRequestGets501(t *testing.T) {
	s, client, _ := reinviteFixture(t)
	msg := "SUBSCRIBE sip:cellbridge@127.0.0.1 SIP/2.0\r\nVia: SIP/2.0/UDP 127.0.0.1;branch=z9hG4bKsub\r\nFrom: <sip:iphone@127.0.0.1>;tag=a\r\nTo: <sip:cellbridge@127.0.0.1>\r\nCall-ID: sub-1\r\nCSeq: 1 SUBSCRIBE\r\nContent-Length: 0\r\n\r\n"
	s.handleMessage(msg, client.LocalAddr().(*net.UDPAddr))
	if resp := readSIP(t, client); !strings.HasPrefix(resp, "SIP/2.0 501") {
		t.Fatalf("SUBSCRIBE answered %q", strings.SplitN(resp, "\r\n", 2)[0])
	}
}
