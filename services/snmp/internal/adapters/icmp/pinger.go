// Package icmp hace ping por el túnel (docs/architecture.md §10.4). Primero
// prueba un socket `udp4` sin privilegios (requiere net.ipv4.ping_group_range,
// que el compose fija con sysctls); si no, un socket raw (CAP_NET_RAW); si
// tampoco, el rol sigue sin señal ICMP (unknown) y lo avisa una sola vez.
package icmp

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

const protocolICMP = 1

// Result es el resultado de un ping (varias sondas).
type Result struct {
	Sent     int
	Received int
	// AvgRTT es la media de las respuestas (nil si ninguna).
	AvgRTT *time.Duration
}

// Loss devuelve la proporción perdida (0..1).
func (r Result) Loss() float64 {
	if r.Sent == 0 {
		return 1
	}
	return float64(r.Sent-r.Received) / float64(r.Sent)
}

// Pinger envía sondas ICMP echo.
type Pinger interface {
	Ping(ctx context.Context, addr netip.Addr, probes int, timeout time.Duration) (Result, error)
}

// Socket es un Pinger sobre un único socket compartido.
type Socket struct {
	conn    *icmp.PacketConn
	mode    string // udp4 | raw
	seq     atomic.Uint32
	mu      sync.Mutex
	pending map[pendingKey]chan time.Time
	closed  chan struct{}
}

type pendingKey struct {
	ip  netip.Addr
	seq int
}

// Open abre el socket: udp4 sin privilegios y, si falla, raw. Devuelve el
// modo usado.
func Open() (*Socket, error) {
	conn, err := icmp.ListenPacket("udp4", "0.0.0.0")
	mode := "udp4"
	if err != nil {
		var rawErr error
		conn, rawErr = icmp.ListenPacket("ip4:icmp", "0.0.0.0")
		mode = "raw"
		if rawErr != nil {
			return nil, errors.Join(err, rawErr)
		}
	}
	s := &Socket{conn: conn, mode: mode, pending: map[pendingKey]chan time.Time{}, closed: make(chan struct{})}
	go s.read()
	return s, nil
}

// Mode devuelve udp4 o raw.
func (s *Socket) Mode() string { return s.mode }

// Close cierra el socket.
func (s *Socket) Close() error {
	select {
	case <-s.closed:
		return nil
	default:
		close(s.closed)
	}
	return s.conn.Close()
}

func (s *Socket) read() {
	buf := make([]byte, 1500)
	for {
		n, peer, err := s.conn.ReadFrom(buf)
		if err != nil {
			select {
			case <-s.closed:
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		msg, err := icmp.ParseMessage(protocolICMP, buf[:n])
		if err != nil || msg.Type != ipv4.ICMPTypeEchoReply {
			continue
		}
		echo, ok := msg.Body.(*icmp.Echo)
		if !ok {
			continue
		}
		ip := peerAddr(peer)
		s.mu.Lock()
		ch := s.pending[pendingKey{ip: ip, seq: echo.Seq}]
		s.mu.Unlock()
		if ch != nil {
			select {
			case ch <- time.Now():
			default:
			}
		}
	}
}

func peerAddr(a net.Addr) netip.Addr {
	switch v := a.(type) {
	case *net.UDPAddr:
		ip, _ := netip.AddrFromSlice(v.IP)
		return ip.Unmap()
	case *net.IPAddr:
		ip, _ := netip.AddrFromSlice(v.IP)
		return ip.Unmap()
	}
	return netip.Addr{}
}

// Ping envía probes sondas (espaciadas 100 ms) y espera cada respuesta hasta
// timeout. Solo IPv4.
func (s *Socket) Ping(ctx context.Context, addr netip.Addr, probes int, timeout time.Duration) (Result, error) {
	addr = addr.Unmap()
	if !addr.Is4() {
		return Result{}, errors.New("icmp: only IPv4 targets")
	}
	if probes <= 0 {
		probes = 1
	}
	var dst net.Addr = &net.UDPAddr{IP: addr.AsSlice()}
	if s.mode == "raw" {
		dst = &net.IPAddr{IP: addr.AsSlice()}
	}
	res := Result{}
	type probe struct {
		key  pendingKey
		ch   chan time.Time
		sent time.Time
	}
	var ps []probe
	id := os.Getpid() & 0xffff
	for i := 0; i < probes; i++ {
		seq := int(s.seq.Add(1) & 0xffff)
		key := pendingKey{ip: addr, seq: seq}
		ch := make(chan time.Time, 1)
		s.mu.Lock()
		s.pending[key] = ch
		s.mu.Unlock()
		msg := icmp.Message{Type: ipv4.ICMPTypeEcho, Code: 0, Body: &icmp.Echo{ID: id, Seq: seq, Data: []byte("horus-snmp")}}
		b, err := msg.Marshal(nil)
		if err != nil {
			s.forget(key)
			return res, err
		}
		sent := time.Now()
		if _, err := s.conn.WriteTo(b, dst); err != nil {
			s.forget(key)
			continue
		}
		res.Sent++
		ps = append(ps, probe{key: key, ch: ch, sent: sent})
		if i < probes-1 {
			select {
			case <-ctx.Done():
				i = probes
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	var total time.Duration
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for _, p := range ps {
		select {
		case at := <-p.ch:
			res.Received++
			total += at.Sub(p.sent)
		case <-deadline.C:
		case <-ctx.Done():
		}
		s.forget(p.key)
	}
	if res.Received > 0 {
		avg := total / time.Duration(res.Received)
		res.AvgRTT = &avg
	}
	if res.Sent == 0 {
		return res, errors.New("icmp: no probe sent")
	}
	return res, nil
}

func (s *Socket) forget(k pendingKey) {
	s.mu.Lock()
	delete(s.pending, k)
	s.mu.Unlock()
}
