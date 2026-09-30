package sip

import (
	"net"
	"sync"

	"github.com/cellbridge/cellbridge/gateway/internal/voice"
	"github.com/pion/rtp"
)

// MediaSession carries one call's RTP (PCMU/8000). Sequence numbers and
// timestamps must advance per packet or strict RTP stacks (baresip /
// YakPhone) discard every packet as a duplicate — the earlier constant-0
// bug produced silent calls.
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
	return &MediaSession{conn: conn, ssrc: 0x12345678, payloadType: 0}, nil
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
			if pkt.PayloadType != 0 || len(pkt.Payload) != 160 {
				continue
			}
			m.mu.Lock()
			if m.closed || (m.remoteAddr != nil && !remote.IP.Equal(m.remoteAddr.IP)) {
				m.mu.Unlock()
				continue
			}
			m.rx++
			m.mu.Unlock()
			fn(pkt.Payload)
		}
	}()
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
