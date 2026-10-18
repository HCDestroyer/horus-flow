// Package memdev implementa en memoria las interfaces de túnel y el demonio
// IKE del agente (tests y desarrollo sin CAP_NET_ADMIN ni strongSwan).
package memdev

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/app"
	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/domain"
)

// Host son las interfaces, rutas y conjuntos del cortafuegos simulados.
type Host struct {
	mu sync.Mutex
	// Links por nombre de interfaz.
	Links map[string]domain.Tunnel
	// Stats por nombre de interfaz.
	Stats    map[string]app.LinkState
	GREPeers []netip.Addr
	Bindings []app.Binding
	// Ensures cuenta las llamadas que cambiaron algo (para probar que no hay cortes).
	Ensures int
	Fail    bool
}

// NewHost crea el host vacío.
func NewHost() *Host {
	return &Host{Links: map[string]domain.Tunnel{}, Stats: map[string]app.LinkState{}}
}

var _ app.Links = (*Host)(nil)

var errDown = errors.New("memdev: host unavailable")

// List implementa app.Links.
func (h *Host) List(context.Context) ([]app.LinkState, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.Fail {
		return nil, errDown
	}
	out := make([]app.LinkState, 0, len(h.Links))
	for name, t := range h.Links {
		st := h.Stats[name]
		st.Transport, st.Index, st.Up = t.Transport, t.Index, true
		out = append(out, st)
	}
	slices.SortFunc(out, func(a, b app.LinkState) int { return int(a.Index) - int(b.Index) })
	return out, nil
}

// Ensure implementa app.Links.
func (h *Host) Ensure(_ context.Context, t domain.Tunnel) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.Fail {
		return errDown
	}
	cur, ok := h.Links[t.Interface()]
	t.PSK = "" // la interfaz no guarda secretos
	t.Routed = slices.Clone(t.Routed)
	if ok && cur.Equal(t) {
		return nil
	}
	h.Links[t.Interface()] = t
	h.Ensures++
	return nil
}

// Remove implementa app.Links.
func (h *Host) Remove(_ context.Context, transport string, index uint32) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.Fail {
		return errDown
	}
	delete(h.Links, domain.InterfaceName(transport, index))
	delete(h.Stats, domain.InterfaceName(transport, index))
	return nil
}

// SyncFilters implementa app.Links.
func (h *Host) SyncFilters(_ context.Context, peers []netip.Addr, b []app.Binding) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.Fail {
		return errDown
	}
	h.GREPeers, h.Bindings = slices.Clone(peers), slices.Clone(b)
	return nil
}

// Count simula tráfico recibido por una interfaz.
func (h *Host) Count(name string, packets, bytes uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.Stats[name]
	st.RxPackets += packets
	st.RxBytes += bytes
	h.Stats[name] = st
}

// IKE es un demonio IKE simulado.
type IKE struct {
	mu     sync.Mutex
	Loaded map[uint32]domain.Tunnel
	SAMap  map[uint32]app.SA
	Down   bool
}

// NewIKE crea el demonio vacío.
func NewIKE() *IKE { return &IKE{Loaded: map[uint32]domain.Tunnel{}, SAMap: map[uint32]app.SA{}} }

var _ app.IKE = (*IKE)(nil)

var errIKEDown = errors.New("memdev: ike daemon down")

// Load implementa app.IKE.
func (k *IKE) Load(_ context.Context, t domain.Tunnel) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.Down {
		return errIKEDown
	}
	k.Loaded[t.Index] = t
	return nil
}

// Unload implementa app.IKE.
func (k *IKE) Unload(_ context.Context, index uint32) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.Down {
		return errIKEDown
	}
	delete(k.Loaded, index)
	delete(k.SAMap, index)
	return nil
}

// Conns implementa app.IKE.
func (k *IKE) Conns(context.Context) ([]uint32, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.Down {
		return nil, errIKEDown
	}
	var out []uint32
	for idx := range k.Loaded {
		out = append(out, idx)
	}
	slices.Sort(out)
	return out, nil
}

// SAs implementa app.IKE.
func (k *IKE) SAs(context.Context) (map[uint32]app.SA, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.Down {
		return nil, errIKEDown
	}
	out := make(map[uint32]app.SA, len(k.SAMap))
	for idx, sa := range k.SAMap {
		if _, ok := k.Loaded[idx]; ok {
			out[idx] = sa
		}
	}
	return out, nil
}

// Establish simula una IKE SA establecida con su CHILD SA.
func (k *IKE) Establish(index uint32, at time.Time, endpoint string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.SAMap[index] = app.SA{State: "established", ChildInstalled: true, EstablishedAt: at, LastRekeyAt: at, Endpoint: endpoint}
}

// Restart simula un reinicio del demonio (pierde conexiones y SA).
func (k *IKE) Restart() {
	k.mu.Lock()
	defer k.mu.Unlock()
	clear(k.Loaded)
	clear(k.SAMap)
}
