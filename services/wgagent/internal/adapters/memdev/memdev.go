// Package memdev es una interfaz WireGuard en memoria (tests y desarrollo sin
// CAP_NET_ADMIN): guarda los peers y permite simular handshakes.
package memdev

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/hcdestroyer/horus-flow/services/wgagent/internal/app"
	"github.com/hcdestroyer/horus-flow/services/wgagent/internal/domain"
)

// Device es la interfaz en memoria.
type Device struct {
	mu         sync.Mutex
	peers      map[string]domain.Peer
	PrivateKey string
	ListenPort int
	// Fail hace fallar todas las operaciones (interfaz caída).
	Fail    bool
	Applies int
}

var _ app.Device = (*Device)(nil)

// New crea una interfaz vacía.
func New() *Device { return &Device{peers: map[string]domain.Peer{}} }

var errDown = errors.New("memdev: interface down")

// Configure implementa app.Device.
func (d *Device) Configure(_ context.Context, privateKey string, listenPort int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.Fail {
		return errDown
	}
	d.PrivateKey, d.ListenPort = privateKey, listenPort
	return nil
}

// Peers implementa app.Device.
func (d *Device) Peers(context.Context) ([]domain.Peer, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.Fail {
		return nil, errDown
	}
	out := make([]domain.Peer, 0, len(d.peers))
	for _, p := range d.peers {
		p.AllowedIPs = slices.Clone(p.AllowedIPs)
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b domain.Peer) int {
		if a.PublicKey < b.PublicKey {
			return -1
		}
		return 1
	})
	return out, nil
}

// Apply implementa app.Device.
func (d *Device) Apply(_ context.Context, upsert []domain.Peer, remove []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.Fail {
		return errDown
	}
	d.Applies++
	for _, p := range upsert {
		cur := d.peers[p.PublicKey]
		cur.PublicKey, cur.AllowedIPs, cur.Keepalive = p.PublicKey, slices.Clone(p.AllowedIPs), p.Keepalive
		d.peers[p.PublicKey] = cur
	}
	for _, k := range remove {
		delete(d.peers, k)
	}
	return nil
}

// Handshake simula un handshake del peer con clave pub desde endpoint.
func (d *Device) Handshake(pub, endpoint string, at time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.peers[pub]
	if !ok {
		return false
	}
	p.Endpoint, p.LastHandshake = endpoint, at
	p.RxBytes += 148
	p.TxBytes += 92
	d.peers[pub] = p
	return true
}
