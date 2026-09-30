package sip

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func testUDPPair(t *testing.T) (*Server, *net.UDPConn, *net.UDPAddr) {
	t.Helper()
	listen := func() *net.UDPConn {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		return conn
	}
	client, gateway := listen(), listen()
	return &Server{conn: gateway, registrar: NewRegistrar(), ctx: context.Background()}, client, client.LocalAddr().(*net.UDPAddr)
}

func readSIPTest(t *testing.T, conn *net.UDPConn) string {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 8192)
	n, _, err := conn.ReadFromUDP(buf)
	if err != nil {
		t.Fatal(err)
	}
	return string(buf[:n])
}

func TestRegistrationExpiry(t *testing.T) {
	for _, tc := range []struct {
		contact, header string
		want            int
		bad             bool
	}{
		{"<sip:iphone@host>;expires=300", "3600", 300, false},
		{"<sip:iphone@host>;Expires=0", "3600", 0, false},
		{"<sip:iphone@host>;expires=0300", "", 300, false},
		{"<sip:iphone@host;expires=10>", "600", 600, false},
		{"<sip:iphone@host>", "", 3600, false},
		{"<sip:iphone@host>", "9999999", 86400, false},
		{"<sip:iphone@host>;expires=-1", "", 0, true},
		{"<sip:iphone@host>;expires=oops", "", 0, true},
		{"<sip:iphone@host>;expires=", "", 0, true},
		{"<sip:iphone@host>", "oops", 0, true},
	} {
		got, err := registrationExpiry(tc.contact, tc.header)
		if (err != nil) != tc.bad || got != tc.want {
			t.Errorf("%q/%q = %d, %v", tc.contact, tc.header, got, err)
		}
	}
}

func TestRegistrarKeepsNewBindingAndPrunesExpired(t *testing.T) {
	r := NewRegistrar()
	r.Register("iphone", "<sip:iphone@host:6000>", "UDP", 300)
	r.Register("iphone", "<sip:iphone@host:5000>", "UDP", 0)
	if _, ok := r.Get("iphone"); !ok {
		t.Fatal("old-port deregistration erased new binding")
	}
	r.Register("iphone", "*", "UDP", 0)
	if _, ok := r.Get("iphone"); ok {
		t.Fatal("wildcard deregistration failed")
	}
	r.Register("expired", "<sip:expired@host>", "UDP", 1)
	r.regs["expired"].Expires = time.Now().Add(-time.Second)
	if len(r.All()) != 0 || len(r.regs) != 0 {
		t.Fatal("expired registration retained")
	}
}

func TestHandleRegisterDigestAndReceivedSource(t *testing.T) {
	s, client, addr := testUDPPair(t)
	s.auth = NewAuth("cellbridge")
	s.auth.AddUser("iphone", "test-password")
	base := "REGISTER sip:gateway SIP/2.0\r\nVia: SIP/2.0/UDP host:5060;branch=z9hG4bKtest;rport\r\nFrom: <sip:iphone@gateway>;tag=client\r\nTo: <sip:iphone@gateway>\r\nCall-ID: test-register\r\nCSeq: 1 REGISTER\r\nContact: <sip:iphone@192.0.2.1:6000>;expires=300\r\nExpires: 3600\r\n"
	s.handleRegister(base+"Authorization: bogus\r\n\r\n", addr)
	if !strings.HasPrefix(readSIPTest(t, client), "SIP/2.0 401") {
		t.Fatal("invalid authentication not rejected")
	}
	if _, ok := s.registrar.Get("iphone"); ok {
		t.Fatal("unauthenticated registration created")
	}
	s.handleRegister(base+"Authorization: "+testDigest(s.auth, "iphone", "test-password", "REGISTER", "sip:gateway", false)+"\r\n\r\n", addr)
	response := readSIPTest(t, client)
	if !strings.HasPrefix(response, "SIP/2.0 200") || !strings.Contains(response, "Expires: 300") {
		t.Fatal(response)
	}
	reg, ok := s.registrar.Get("iphone")
	if !ok || !sameUDPAddr(registrationAddr(reg), addr) || time.Until(reg.Expires) > 301*time.Second {
		t.Fatalf("incorrect received binding: %+v", reg)
	}
	request := "INVITE sip:10010@gateway SIP/2.0\r\nFrom: <sip:iphone@gateway>\r\n\r\n"
	if !s.authorizedRegisteredClient(request, addr) {
		t.Fatal("registered source rejected")
	}
	other := &net.UDPAddr{IP: addr.IP, Port: addr.Port + 1}
	if s.authorizedRegisteredClient(request, other) {
		t.Fatal("username spoof from other source accepted")
	}
}

func TestUnauthenticatedMessageCannotSendSMS(t *testing.T) {
	s, client, addr := testUDPPair(t)
	s.sendSMS = func(context.Context, string, string) error { t.Error("unauthorized paid SMS attempted"); return nil }
	s.handleMessageRequest(fmt.Sprintf("MESSAGE sip:10010@gateway SIP/2.0\r\nFrom: <sip:iphone@gateway>\r\nCall-ID: sms-test\r\nCSeq: 1 MESSAGE\r\nContent-Length: 5\r\n\r\nhello"), addr)
	if !strings.HasPrefix(readSIPTest(t, client), "SIP/2.0 403") {
		t.Fatal("unauthorized MESSAGE not rejected")
	}
}
