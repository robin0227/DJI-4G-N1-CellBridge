package sip

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
)

type Auth struct {
	mu    sync.RWMutex
	users map[string]string
	realm string
	nonce string
}

func NewAuth(realm string) *Auth {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return &Auth{users: make(map[string]string), realm: realm, nonce: hex.EncodeToString(b)}
}

func (a *Auth) AddUser(username, password string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.users[username] = password
}

func (a *Auth) Check(username, password string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	p, ok := a.users[username]
	return ok && subtle.ConstantTimeCompare([]byte(p), []byte(password)) == 1
}

func (a *Auth) Realm() string { return a.realm }
func (a *Auth) Nonce() string { return a.nonce }

func (a *Auth) NeedsAuth(msg string) bool {
	return !a.Verify(msg, extractSIPUser(parseHeader(msg, "To")))
}

// Verify checks the actual Digest response, not just the presence of a header.
// MD5 is retained for existing SIP handset compatibility; this is not TLS.
func (a *Auth) Verify(msg, username string) bool {
	d, ok := digestFields(parseHeader(msg, "Authorization"))
	if !ok || username == "" || d["username"] != username || d["realm"] != a.realm || d["nonce"] != a.nonce {
		return false
	}
	a.mu.RLock()
	password, exists := a.users[username]
	a.mu.RUnlock()
	if !exists {
		return false
	}
	request := strings.Fields(strings.SplitN(msg, "\r\n", 2)[0])
	if len(request) != 3 || request[2] != "SIP/2.0" || d["uri"] != request[1] {
		return false
	}
	ha1 := digestHash(username + ":" + a.realm + ":" + password)
	switch strings.ToLower(d["algorithm"]) {
	case "", "md5":
	case "md5-sess":
		if d["cnonce"] == "" {
			return false
		}
		ha1 = digestHash(ha1 + ":" + a.nonce + ":" + d["cnonce"])
	default:
		return false
	}
	ha2 := digestHash(request[0] + ":" + request[1])
	response := ""
	switch d["qop"] {
	case "":
		response = digestHash(ha1 + ":" + a.nonce + ":" + ha2)
	case "auth":
		nc, err := strconv.ParseUint(d["nc"], 16, 32)
		if err != nil || len(d["nc"]) != 8 || nc == 0 || d["cnonce"] == "" {
			return false
		}
		response = digestHash(ha1 + ":" + a.nonce + ":" + d["nc"] + ":" + d["cnonce"] + ":auth:" + ha2)
	default:
		return false
	}
	return subtle.ConstantTimeCompare([]byte(response), []byte(strings.ToLower(d["response"]))) == 1
}

func digestHash(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

// Quoted directive values may contain commas; reject ambiguous or broken input.
func digestFields(header string) (map[string]string, bool) {
	parts := strings.SplitN(strings.TrimSpace(header), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Digest") {
		return nil, false
	}
	s := strings.TrimSpace(parts[1])
	out := make(map[string]string)
	for s != "" {
		i := strings.IndexByte(s, '=')
		if i < 1 {
			return nil, false
		}
		key := strings.ToLower(strings.TrimSpace(s[:i]))
		if strings.ContainsAny(key, " ,\t\r\n") {
			return nil, false
		}
		if _, exists := out[key]; exists {
			return nil, false
		}
		s = strings.TrimSpace(s[i+1:])
		value := ""
		if strings.HasPrefix(s, "\"") {
			s = s[1:]
			var b strings.Builder
			closed := false
			for len(s) > 0 {
				c := s[0]
				s = s[1:]
				if c == '"' {
					closed = true
					break
				}
				if c == '\\' {
					if s == "" {
						return nil, false
					}
					c, s = s[0], s[1:]
				}
				b.WriteByte(c)
			}
			if !closed {
				return nil, false
			}
			value = b.String()
		} else {
			i = strings.IndexByte(s, ',')
			if i < 0 {
				i = len(s)
			}
			value, s = strings.TrimSpace(s[:i]), s[i:]
		}
		out[key] = value
		s = strings.TrimSpace(s)
		if s == "" {
			break
		}
		if s[0] != ',' {
			return nil, false
		}
		s = strings.TrimSpace(s[1:])
		if s == "" {
			return nil, false
		}
	}
	return out, len(out) > 0
}

func WWWAuthHeader(realm, nonce string) string {
	return `Digest realm="` + realm + `", nonce="` + nonce + `", algorithm=MD5, qop="auth"`
}
