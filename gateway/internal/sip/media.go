package sip

import (
	"crypto/rand"
	"encoding/binary"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/cellbridge/cellbridge/gateway/internal/voice"
	"github.com/pion/rtp"
)

const dtmfPayloadType uint8 = 101

// MediaSession carries one call's RTP (PCMU/8000 + RFC 4733 DTMF). Sequence
// numbers and timestamps must advance per packet or strict RTP stacks
// (baresip / YakPhone) discard every packet as a duplicate.
type MediaSession struct {
	conn        *net.UDPConn
	remoteAddr  *net.UDPAddr
	ssrc        uint32
	payloadType uint8
	mu          sync.Mutex
	bridge      *voice.Bridge
	closed      bool
	seq         uint16
	ts          uint32
	rx          int64
	tx          int64
	onDTMF      func(rune)
	lastDTMFTS  uint32
	hasDTMFTS   bool
}

func randomUint32() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return uint32(time.Now().UnixNano())
	}
	return binary.BigEndian.Uint32(b[:])
}

func NewMediaSession(localAddr string) (*MediaSession, error) {
	// Keep the RTP socket in the same address family as the SIP clients used by
	// CellBridge. On macOS, ListenUDP("udp", 0.0.0.0:0) can create an IPv6
	// wildcard socket ([::]); writes from that socket to an IPv4 handset may
	// then fail with EHOSTUNREACH even though the handset is reachable on LAN.
	udpAddr, err := net.ResolveUDPAddr("udp4", localAddr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp4", udpAddr)
	if err != nil {
		return nil, err
	}
	ssrc := randomUint32()
	if ssrc == 0 {
		ssrc = 0x12345678
	}
	return &MediaSession{
		conn:        conn,
		ssrc:        ssrc,
		seq:         uint16(randomUint32()),
		ts:          randomUint32(),
		payloadType: 0,
	}, nil
}

func (m *MediaSession) LocalAddr() *net.UDPAddr { return m.conn.LocalAddr().(*net.UDPAddr) }

func (m *MediaSession) RemoteAddr() *net.UDPAddr {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.remoteAddr
}

func (m *MediaSession) SetRemote(addr string) error {
	ra, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.remoteAddr = ra
	m.mu.Unlock()
	return nil
}

// OnDTMF registers a callback for RFC 4733 telephone-event digits (PT=101).
func (m *MediaSession) OnDTMF(fn func(rune)) {
	m.mu.Lock()
	m.onDTMF = fn
	m.mu.Unlock()
}

func (m *MediaSession) WritePCMU(frame []byte) error {
	m.mu.Lock()
	ra := m.remoteAddr
	m.seq++
	m.ts += uint32(len(frame))
	seq, ts, pt, ssrc := m.seq, m.ts, m.payloadType, m.ssrc
	m.mu.Unlock()
	if ra == nil {
		return nil
	}
	pkt := &rtp.Packet{
		Header:  rtp.Header{Version: 2, PayloadType: pt, SequenceNumber: seq, Timestamp: ts, SSRC: ssrc},
		Payload: frame,
	}
	buf, err := pkt.Marshal()
	if err != nil {
		return err
	}
	_, err = m.conn.WriteToUDP(buf, ra)
	if err == nil {
		m.mu.Lock()
		m.tx++
		m.mu.Unlock()
	}
	return err
}

func (m *MediaSession) OnPCMUFrame(fn func([]byte)) {
	m.OnSequencedPCMUFrame(func(_ uint16, frame []byte) { fn(frame) })
}

// OnSequencedPCMUFrame delivers each accepted PCMU payload with its RTP
// sequence number so the voice bridge can reorder and de-duplicate.
//
// RTP that queued in the socket before the reader started is discarded
// first. The phone starts sending the moment it answers, but the bridge only
// starts after the modem has picked up the cellular leg; stale frames would
// otherwise hit the jitter buffer in one burst and add delay at call start.
func (m *MediaSession) OnSequencedPCMUFrame(fn func(seq uint16, frame []byte)) {
	if stale := m.discardQueued(); stale > 0 {
		slog.Info("rtp stale packets discarded", "local", m.LocalAddr().String(), "packets", stale)
	}
	go func() {
		buf := make([]byte, 2048)
		for {
			n, remote, err := m.conn.ReadFromUDP(buf)
			if err != nil {
				m.mu.Lock()
				closed := m.closed
				m.mu.Unlock()
				if closed {
					return
				}
				continue
			}
			pkt := &rtp.Packet{}
			if err := pkt.Unmarshal(buf[:n]); err != nil {
				continue
			}
			// RFC 4733 DTMF event: 4-byte payload on PT=101. Never forwarded as PCMU audio.
			if pkt.PayloadType == dtmfPayloadType && len(pkt.Payload) == 4 {
				if digit, ok := decodeRFC4733Digit(pkt.Payload[0]); ok {
					m.mu.Lock()
					allowed := !m.closed && (m.remoteAddr == nil || remote.IP.Equal(m.remoteAddr.IP))
					var cb func(rune)
					if allowed && (!m.hasDTMFTS || pkt.Timestamp != m.lastDTMFTS) {
						m.hasDTMFTS = true
						m.lastDTMFTS = pkt.Timestamp
						cb = m.onDTMF
					}
					m.mu.Unlock()
					if cb != nil {
						cb(digit)
					}
				}
				continue
			}
			if pkt.PayloadType != 0 || len(pkt.Payload) != 160 {
				continue
			}
			m.mu.Lock()
			if m.closed || (m.remoteAddr != nil && !remote.IP.Equal(m.remoteAddr.IP)) {
				m.mu.Unlock()
				continue
			}
			// Symmetric RTP (RFC 4961): if the authenticated peer sends media
			// from a NAT-mapped port different from its SDP port, latch onto it.
			if m.remoteAddr != nil && remote.Port != 0 && m.remoteAddr.Port != remote.Port {
				m.remoteAddr = &net.UDPAddr{IP: append(net.IP(nil), remote.IP...), Port: remote.Port, Zone: remote.Zone}
				slog.Info("rtp symmetric port latched", "remote", m.remoteAddr.String())
			}
			m.rx++
			m.mu.Unlock()
			fn(pkt.SequenceNumber, pkt.Payload)
		}
	}()
}

var rfc4733Digits = [...]rune{'0', '1', '2', '3', '4', '5', '6', '7', '8', '9', '*', '#', 'A', 'B', 'C', 'D'}

func decodeRFC4733Digit(code byte) (rune, bool) {
	if int(code) >= len(rfc4733Digits) {
		return 0, false
	}
	return rfc4733Digits[code], true
}

// discardQueued drops every datagram already waiting on the socket.
func (m *MediaSession) discardQueued() int {
	buf := make([]byte, 2048)
	count := 0
	for {
		// A deadline already in the past fails before reading anything, so
		// give each read a 1 ms window: queued datagrams return at once.
		if err := m.conn.SetReadDeadline(time.Now().Add(time.Millisecond)); err != nil {
			return count
		}
		if _, _, err := m.conn.ReadFromUDP(buf); err != nil {
			break
		}
		count++
	}
	_ = m.conn.SetReadDeadline(time.Time{})
	return count
}

// Stats reports RTP counters for §36/§37 audio diagnostics.
func (m *MediaSession) Stats() (rx, tx int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rx, m.tx
}

func (m *MediaSession) Close() error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	return m.conn.Close()
}
