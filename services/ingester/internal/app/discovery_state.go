package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/flowstate"
)

// StateSnapshotName es el nombre de la instantánea del descubrimiento en
// flows_state.
const StateSnapshotName = "ingester-discovery"

// discoverySnap es la parte del descubrimiento que no sale de DEVICES_EVENTS:
// claves pendientes de first_seen, las ya emitidas (TTL) y la actividad de la
// hora en curso. Se guarda en flows_state cada pocos segundos y al parar
// (D23): un reinicio no pierde el resumen de actividad acumulado ni repite
// first_seen ya emitidos.
type discoverySnap struct {
	Hour   time.Time                   `json:"hour"`
	Realms map[uuid.UUID]realmSnapshot `json:"realms"`
}

type realmSnapshot struct {
	Tenant  uuid.UUID                   `json:"tenant"`
	Router  uuid.UUID                   `json:"router"`
	Window  time.Time                   `json:"window"`
	Emitted map[netip.Addr]time.Time    `json:"emitted,omitempty"`
	Pending map[netip.Addr]pendingSnap  `json:"pending,omitempty"`
	Active  map[netip.Addr]activitySnap `json:"active,omitempty"`
}

type pendingSnap struct {
	Prefix uuid.UUID `json:"prefix"`
	First  time.Time `json:"first"`
}

type activitySnap struct {
	Last  time.Time `json:"last"`
	Bytes uint64    `json:"bytes"`
	Flows uint32    `json:"flows"`
}

// StateSnapshot serializa pendientes, emitidas y actividad.
func (d *Discovery) StateSnapshot() (json.RawMessage, error) {
	d.mu.Lock()
	s := discoverySnap{Hour: d.hour, Realms: make(map[uuid.UUID]realmSnapshot, len(d.realms))}
	for id, r := range d.realms {
		if len(r.emitted)+len(r.pending)+len(r.active) == 0 {
			continue
		}
		rs := realmSnapshot{Tenant: r.tenant, Router: r.router, Window: r.window,
			Emitted: make(map[netip.Addr]time.Time, len(r.emitted)), Pending: make(map[netip.Addr]pendingSnap, len(r.pending)),
			Active: make(map[netip.Addr]activitySnap, len(r.active))}
		for a, t := range r.emitted {
			rs.Emitted[a] = t
		}
		for a, p := range r.pending {
			rs.Pending[a] = pendingSnap{Prefix: p.prefix, First: p.first}
		}
		for a, ac := range r.active {
			rs.Active[a] = activitySnap{Last: ac.last, Bytes: ac.bytes, Flows: ac.flows}
		}
		s.Realms[id] = rs
	}
	d.mu.Unlock()
	return json.Marshal(s)
}

// RestoreState fusiona una instantánea de StateSnapshot con el estado
// actual (los lotes reentregados tras el reinicio se vuelven a observar:
// last_seen es un máximo y bytes/flujos son estimaciones).
func (d *Discovery) RestoreState(b json.RawMessage) error {
	var s discoverySnap
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.hour.IsZero() {
		d.hour = s.Hour
	}
	for id, rs := range s.Realms {
		r := d.realm(id, rs.Tenant)
		if r.router == uuid.Nil {
			r.router = rs.Router
		}
		if r.window.IsZero() {
			r.window = rs.Window
		}
		for a, t := range rs.Emitted {
			if t.After(r.emitted[a]) {
				r.emitted[a] = t
			}
		}
		for a, p := range rs.Pending {
			if _, ok := r.known[a]; ok {
				continue
			}
			if _, ok := r.pending[a]; !ok {
				r.pending[a] = pendingClient{prefix: p.Prefix, first: p.First}
			}
		}
		for a, as := range rs.Active {
			ac := r.active[a]
			if ac == nil {
				ac = &activity{}
				r.active[a] = ac
			}
			if as.Last.After(ac.last) {
				ac.last = as.Last
			}
			ac.bytes += as.Bytes
			ac.flows += as.Flows
		}
	}
	return nil
}

// LoadState restaura la instantánea del descubrimiento (si existe).
func (d *Discovery) LoadState(ctx context.Context, store flowstate.Store) error {
	if store == nil {
		return nil
	}
	b, err := store.Load(ctx, StateSnapshotName)
	if err != nil || len(b) == 0 {
		return err
	}
	return d.RestoreState(b)
}

// KeepState guarda la instantánea del descubrimiento cada every y al
// cancelar ctx.
func (d *Discovery) KeepState(ctx context.Context, store flowstate.Store, every time.Duration, log *slog.Logger) {
	if store == nil {
		<-ctx.Done()
		return
	}
	save := func(ctx context.Context) {
		b, err := d.StateSnapshot()
		if err == nil {
			err = store.Save(ctx, StateSnapshotName, b)
		}
		if err != nil && log != nil {
			log.Warn("discovery state not saved", "error", err)
		}
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			save(context.WithoutCancel(ctx))
			return
		case <-t.C:
			save(ctx)
		}
	}
}
