package sip

import (
	"net"
	"sync"
	"time"
)

type Registration struct {
	Username  string
	Contact   string
	Remote    *net.UDPAddr
	Expires   time.Time
	Transport string
	LastSeen  time.Time
}

type Registrar struct {
	mu   sync.Mutex
	regs map[string]*Registration
}

func NewRegistrar() *Registrar { return &Registrar{regs: make(map[string]*Registration)} }

func (r *Registrar) Register(username, contact, transport string, expiresSec int) {
	r.RegisterFrom(username, contact, transport, expiresSec, nil)
}

func (r *Registrar) RegisterFrom(username, contact, transport string, expiresSec int, remote *net.UDPAddr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if expiresSec <= 0 {
		// A delayed unregister from an old UDP port must not erase the new binding.
		if old := r.regs[username]; old != nil && contact != "*" && contactURI(old.Contact, nil, username) != contactURI(contact, nil, username) {
			return
		}
		delete(r.regs, username)
		return
	}
	if remote != nil {
		remote = &net.UDPAddr{IP: append(net.IP(nil), remote.IP...), Port: remote.Port, Zone: remote.Zone}
	}
	r.regs[username] = &Registration{
		Username:  username,
		Contact:   contact,
		Remote:    remote,
		Transport: transport,
		LastSeen:  time.Now(),
		Expires:   time.Now().Add(time.Duration(expiresSec) * time.Second),
	}
}

func (r *Registrar) Get(username string) (*Registration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	reg, ok := r.regs[username]
	if !ok {
		return nil, false
	}
	if time.Now().After(reg.Expires) {
		delete(r.regs, username)
		return nil, false
	}
	return reg, true
}

func (r *Registrar) All() []*Registration {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*Registration
	now := time.Now()
	for key, v := range r.regs {
		if now.Before(v.Expires) {
			out = append(out, v)
		} else {
			delete(r.regs, key)
		}
	}
	return out
}

func registrationAddr(reg *Registration) *net.UDPAddr {
	if reg.Remote != nil {
		return reg.Remote
	}
	return contactAddr(reg.Contact)
}
