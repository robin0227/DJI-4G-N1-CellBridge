package sip

import (
	"fmt"
	"strings"
	"testing"
)

func testDigest(a *Auth, user, password, method, uri string, legacy bool) string {
	ha1 := digestHash(user + ":" + a.Realm() + ":" + password)
	ha2 := digestHash(method + ":" + uri)
	response := digestHash(ha1 + ":" + a.Nonce() + ":00000001:test-client:auth:" + ha2)
	extra := `, qop=auth, nc=00000001, cnonce="test-client"`
	if legacy {
		response = digestHash(ha1 + ":" + a.Nonce() + ":" + ha2)
		extra = ""
	}
	return fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s", algorithm=MD5%s`, user, a.Realm(), a.Nonce(), uri, response, extra)
}

func TestDigestKnownVector(t *testing.T) {
	a := NewAuth("testrealm@host.com")
	a.nonce = "dcd98b7102dd2f0e8b11d0f600bfb0c093"
	a.AddUser("Mufasa", "Circle Of Life")
	msg := "GET /dir/index.html SIP/2.0\r\nAuthorization: Digest username=\"Mufasa\", realm=\"testrealm@host.com\", nonce=\"dcd98b7102dd2f0e8b11d0f600bfb0c093\", uri=\"/dir/index.html\", qop=auth, nc=00000001, cnonce=\"0a4f113b\", response=\"6629fae49393a05397450978507c4ef1\"\r\n\r\n"
	if !a.Verify(msg, "Mufasa") {
		t.Fatal("RFC Digest test vector rejected")
	}
}

func TestRegisterDigestVerification(t *testing.T) {
	a := NewAuth("cellbridge")
	a.AddUser("iphone", "test-password")
	base := "REGISTER sip:gateway SIP/2.0\r\nTo: <sip:iphone@gateway>\r\n"
	for _, legacy := range []bool{false, true} {
		msg := base + "Authorization: " + testDigest(a, "iphone", "test-password", "REGISTER", "sip:gateway", legacy) + "\r\n\r\n"
		if a.NeedsAuth(msg) {
			t.Fatalf("valid digest rejected, legacy=%v", legacy)
		}
	}
	good := testDigest(a, "iphone", "test-password", "REGISTER", "sip:gateway", false)
	for name, header := range map[string]string{
		"fake header":           "anything",
		"wrong password":        testDigest(a, "iphone", "wrong", "REGISTER", "sip:gateway", false),
		"unknown account":       testDigest(a, "unknown", "test-password", "REGISTER", "sip:gateway", false),
		"wrong URI":             testDigest(a, "iphone", "test-password", "REGISTER", "sip:other", false),
		"wrong method":          testDigest(a, "iphone", "test-password", "INVITE", "sip:gateway", false),
		"stale nonce":           strings.Replace(good, a.Nonce(), "old-nonce", 1),
		"wrong realm":           strings.Replace(good, `realm="cellbridge"`, `realm="other"`, 1),
		"duplicate field":       good + `, username="iphone"`,
		"zero nonce count":      strings.Replace(good, "nc=00000001", "nc=00000000", 1),
		"unsupported algorithm": strings.Replace(good, "algorithm=MD5", "algorithm=SHA-512", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if a.Verify(base+"Authorization: "+header+"\r\n\r\n", "iphone") {
				t.Fatal("invalid digest accepted")
			}
		})
	}
	if a.Verify(base+"\r\nAuthorization: "+good, "iphone") {
		t.Fatal("body spoofing accepted")
	}
}

func TestDigestQuotedDirectives(t *testing.T) {
	fields, ok := digestFields(`Digest username="comma,name", cnonce="escaped\"quote", qop=auth`)
	if !ok || fields["username"] != "comma,name" || fields["cnonce"] != `escaped"quote` {
		t.Fatalf("fields: %v, %v", fields, ok)
	}
	for _, bad := range []string{`Basic abc`, `Digest username="unterminated`, `Digest username="a",`, `Digest username="a"oops`, `Digest a=b, a=c`} {
		if _, ok := digestFields(bad); ok {
			t.Errorf("malformed Digest accepted: %s", bad)
		}
	}
}
