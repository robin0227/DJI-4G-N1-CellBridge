package sip

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

func sameUDPAddr(a, b *net.UDPAddr) bool {
	return a != nil && b != nil && a.IP.Equal(b.IP) && a.Port == b.Port && a.Zone == b.Zone
}

// INVITE and MESSAGE must come from the UDP binding that proved possession of
// the account password. Merely knowing a registered username is insufficient.
func (s *Server) authorizedRegisteredClient(msg string, remote *net.UDPAddr) bool {
	if s.registrar == nil {
		return false
	}
	reg, ok := s.registrar.Get(extractSIPUser(parseHeader(msg, "From")))
	return ok && sameUDPAddr(registrationAddr(reg), remote)
}

func registrationExpiry(contact, header string) (int, error) {
	value := strings.TrimSpace(header)
	params := contact
	if end := strings.LastIndexByte(contact, '>'); end >= 0 {
		params = contact[end+1:]
	}
	for _, param := range strings.Split(params, ";")[1:] {
		pair := strings.SplitN(strings.TrimSpace(param), "=", 2)
		if strings.EqualFold(pair[0], "expires") {
			if len(pair) != 2 || strings.TrimSpace(pair[1]) == "" {
				return 0, fmt.Errorf("missing expires value")
			}
			value = strings.TrimSpace(pair[1])
			break
		}
	}
	if value == "" {
		return 3600, nil
	}
	expires, err := strconv.Atoi(value)
	if err != nil || expires < 0 {
		return 0, fmt.Errorf("invalid expires value")
	}
	if expires > 86400 {
		expires = 86400
	}
	return expires, nil
}

func registerContact(contact string, expires int) string {
	if contact == "*" {
		return "*"
	}
	base, params := contact, ""
	if end := strings.LastIndexByte(contact, '>'); end >= 0 {
		base, params = contact[:end+1], contact[end+1:]
	} else if i := strings.IndexByte(contact, ';'); i >= 0 {
		base, params = contact[:i], contact[i:]
	}
	for _, param := range strings.Split(params, ";") {
		if param == "" || strings.EqualFold(strings.TrimSpace(strings.SplitN(param, "=", 2)[0]), "expires") {
			continue
		}
		base += ";" + param
	}
	return fmt.Sprintf("%s;expires=%d", base, expires)
}
