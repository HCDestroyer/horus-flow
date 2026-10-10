package flowinv

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/flowstate"
)

// Projector construye el inventario a partir de los eventos de dominio de
// devices (DEVICES_EVENTS: router.*, site.*, realm.*, client_prefix.*,
// customer.*; docs/events.md §4.5 `flows-inventory`) sobre una base
// (fichero de inventario, opcional) y lo publica en un Store. Aplica
// last-writer-wins por aggregate_version y es idempotente.
type Projector struct {
	store *Store
	base  Data
	log   *slog.Logger

	mu        sync.Mutex
	routers   map[uuid.UUID]routerState
	siteNames map[uuid.UUID]string
	realms    map[uuid.UUID]versioned[Realm]
	prefixes  map[uuid.UUID]versioned[ClientPrefix]
	versions  map[uuid.UUID]int
}

type versioned[T any] struct {
	v       T
	deleted bool
}

type routerState struct {
	e       Exporter
	deleted bool
}

// NewProjector crea el proyector sobre base.
func NewProjector(store *Store, base Data, log *slog.Logger) *Projector {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Projector{store: store, base: base, log: log, routers: map[uuid.UUID]routerState{},
		siteNames: map[uuid.UUID]string{}, realms: map[uuid.UUID]versioned[Realm]{},
		prefixes: map[uuid.UUID]versioned[ClientPrefix]{}, versions: map[uuid.UUID]int{}}
}

type envelope struct {
	Type             string          `json:"type"`
	TenantID         *string         `json:"tenant_id"`
	AggregateVersion int             `json:"aggregate_version"`
	Data             json.RawMessage `json:"data"`
}

type routerData struct {
	ID            uuid.UUID `json:"id"`
	SiteID        uuid.UUID `json:"site_id"`
	Name          string    `json:"name"`
	AdminState    string    `json:"admin_state"`
	TunnelAddress *string   `json:"tunnel_address"`
	DeletedAt     *string   `json:"deleted_at"`
}

type prefixData struct {
	ID            uuid.UUID `json:"id"`
	SiteID        uuid.UUID `json:"site_id"`
	RealmID       uuid.UUID `json:"realm_id"`
	Prefix        string    `json:"prefix"`
	Role          string    `json:"role"`
	DefaultKind   string    `json:"default_kind"`
	IPv6ClientLen *int      `json:"ipv6_client_len"`
	DeletedAt     *string   `json:"deleted_at"`
}

type realmData struct {
	ID     uuid.UUID `json:"id"`
	Kind   string    `json:"kind"`
	SiteID uuid.UUID `json:"site_id"`
	Name   string    `json:"name"`
}

type siteData struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// Apply aplica un evento (cuerpo JSON con el sobre). Devuelve si cambió algo.
func (p *Projector) Apply(body []byte) (bool, error) {
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return false, err
	}
	if env.TenantID == nil {
		return false, errors.New("flowinv: event without tenant")
	}
	tenant, err := uuid.Parse(*env.TenantID)
	if err != nil {
		return false, err
	}
	parts := strings.Split(env.Type, ".")
	if len(parts) != 4 {
		return false, nil
	}
	entity, verb := parts[2], parts[3]
	var id struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(env.Data, &id); err != nil || id.ID == uuid.Nil {
		return false, errors.New("flowinv: event without data.id")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := p.versions[id.ID]; ok && env.AggregateVersion > 0 && env.AggregateVersion <= v && verb != "deleted" {
		return false, nil // versión ya aplicada (reentrega o desorden)
	}
	p.versions[id.ID] = env.AggregateVersion
	deleted := verb == "deleted" || verb == "purged"
	switch entity {
	case "router":
		var d routerData
		if err := json.Unmarshal(env.Data, &d); err != nil {
			return false, err
		}
		rs := routerState{deleted: deleted || d.DeletedAt != nil || d.AdminState == "decommissioned"}
		rs.e = Exporter{TenantID: tenant, RouterID: d.ID, SiteID: d.SiteID, Name: d.Name, AdminState: d.AdminState,
			SiteName: p.siteNames[d.SiteID]}
		if old, ok := p.routers[d.ID]; ok {
			rs.e.Interfaces = old.e.Interfaces
		}
		if d.TunnelAddress != nil {
			if a, err := netip.ParseAddr(strings.Split(*d.TunnelAddress, "/")[0]); err == nil {
				rs.e.TunnelIP = a
			}
		}
		p.routers[d.ID] = rs
	case "site":
		var d siteData
		if err := json.Unmarshal(env.Data, &d); err != nil {
			return false, err
		}
		p.siteNames[d.ID] = d.Name
	case "realm":
		var d realmData
		if err := json.Unmarshal(env.Data, &d); err != nil {
			return false, err
		}
		p.realms[d.ID] = versioned[Realm]{v: Realm{ID: d.ID, TenantID: tenant, Kind: d.Kind, SiteID: d.SiteID, Name: d.Name}, deleted: deleted}
	case "client_prefix":
		var d prefixData
		if err := json.Unmarshal(env.Data, &d); err != nil {
			return false, err
		}
		pf, err := netip.ParsePrefix(d.Prefix)
		if err != nil {
			return false, err
		}
		cp := ClientPrefix{ID: d.ID, TenantID: tenant, SiteID: d.SiteID, RealmID: d.RealmID, Prefix: pf, Role: d.Role,
			DefaultKind: d.DefaultKind}
		if d.IPv6ClientLen != nil {
			cp.IPv6ClientLen = *d.IPv6ClientLen
		}
		p.prefixes[d.ID] = versioned[ClientPrefix]{v: cp, deleted: deleted || d.DeletedAt != nil}
	default:
		return false, nil
	}
	return true, nil
}

// Data devuelve base + proyección (orden determinista).
func (p *Projector) Data() Data {
	p.mu.Lock()
	defer p.mu.Unlock()
	d := Data{Tenants: p.base.Tenants, Customers: p.base.Customers}
	seenRouter := map[uuid.UUID]bool{}
	for _, rs := range p.routers {
		seenRouter[rs.e.RouterID] = true
		if rs.deleted || !rs.e.TunnelIP.IsValid() {
			continue
		}
		e := rs.e
		if e.SiteName == "" {
			e.SiteName = p.siteNames[e.SiteID]
		}
		d.Exporters = append(d.Exporters, e)
	}
	for _, e := range p.base.Exporters {
		if !seenRouter[e.RouterID] {
			d.Exporters = append(d.Exporters, e)
		}
	}
	seenRealm := map[uuid.UUID]bool{}
	for id, r := range p.realms {
		seenRealm[id] = true
		if !r.deleted {
			d.Realms = append(d.Realms, r.v)
		}
	}
	for _, r := range p.base.Realms {
		if !seenRealm[r.ID] {
			d.Realms = append(d.Realms, r)
		}
	}
	seenPrefix := map[uuid.UUID]bool{}
	for id, cp := range p.prefixes {
		seenPrefix[id] = true
		if !cp.deleted {
			d.Prefixes = append(d.Prefixes, cp.v)
		}
	}
	for _, cp := range p.base.Prefixes {
		if !seenPrefix[cp.ID] {
			d.Prefixes = append(d.Prefixes, cp)
		}
	}
	sort.Slice(d.Exporters, func(i, j int) bool { return d.Exporters[i].RouterID.String() < d.Exporters[j].RouterID.String() })
	sort.Slice(d.Realms, func(i, j int) bool { return d.Realms[i].ID.String() < d.Realms[j].ID.String() })
	sort.Slice(d.Prefixes, func(i, j int) bool { return d.Prefixes[i].ID.String() < d.Prefixes[j].ID.String() })
	return d
}

// publish reconstruye el snapshot; uno inválido se descarta y sigue el anterior.
func (p *Projector) publish() {
	s, err := New(p.Data())
	if err != nil {
		p.log.Warn("flows inventory projection rejected", "error", err)
		return
	}
	p.store.Swap(s)
}

// projectionState es la forma serializada de la proyección (instantánea).
type projectionState struct {
	Routers   []routerSnap              `json:"routers"`
	SiteNames map[uuid.UUID]string      `json:"site_names"`
	Realms    []realmSnap               `json:"realms"`
	Prefixes  []prefixSnap              `json:"prefixes"`
	Versions  map[uuid.UUID]int         `json:"versions"`
}

type routerSnap struct {
	E       Exporter `json:"e"`
	Deleted bool     `json:"deleted"`
}

type realmSnap struct {
	V       Realm `json:"v"`
	Deleted bool  `json:"deleted"`
}

type prefixSnap struct {
	V       ClientPrefix `json:"v"`
	Deleted bool         `json:"deleted"`
}

// Snapshot serializa la proyección (sin la base del fichero).
func (p *Projector) Snapshot() (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := projectionState{SiteNames: p.siteNames, Versions: p.versions}
	for _, r := range p.routers {
		st.Routers = append(st.Routers, routerSnap{E: r.e, Deleted: r.deleted})
	}
	for _, r := range p.realms {
		st.Realms = append(st.Realms, realmSnap{V: r.v, Deleted: r.deleted})
	}
	for _, c := range p.prefixes {
		st.Prefixes = append(st.Prefixes, prefixSnap{V: c.v, Deleted: c.deleted})
	}
	return json.Marshal(st)
}

// Restore sustituye la proyección por una instantánea.
func (p *Projector) Restore(b json.RawMessage) error {
	var st projectionState
	if err := json.Unmarshal(b, &st); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.routers, p.realms = map[uuid.UUID]routerState{}, map[uuid.UUID]versioned[Realm]{}
	p.prefixes, p.siteNames, p.versions = map[uuid.UUID]versioned[ClientPrefix]{}, map[uuid.UUID]string{}, map[uuid.UUID]int{}
	for _, r := range st.Routers {
		p.routers[r.E.RouterID] = routerState{e: r.E, deleted: r.Deleted}
	}
	for _, r := range st.Realms {
		p.realms[r.V.ID] = versioned[Realm]{v: r.V, deleted: r.Deleted}
	}
	for _, c := range st.Prefixes {
		p.prefixes[c.V.ID] = versioned[ClientPrefix]{v: c.V, deleted: c.Deleted}
	}
	for k, v := range st.SiteNames {
		p.siteNames[k] = v
	}
	for k, v := range st.Versions {
		p.versions[k] = v
	}
	return nil
}

// Subjects son los eventos de DEVICES_EVENTS que proyecta el inventario.
var Subjects = []string{"horus.devices.router.>", "horus.devices.site.>", "horus.devices.realm.>", "horus.devices.client_prefix.>"}

// Run mantiene la proyección de DEVICES_EVENTS hasta que ctx se cancela.
// ready se cierra al alcanzar el final del stream por primera vez. Si el
// stream no existe (devices no desplegado) reintenta cada 30 s.
//
// La proyección vive en memoria y se reconstruye en cada arranque
// (packages/go/flowstate): instantánea en el Object Store flows_state (si
// states no es nil) y lectura del stream con un consumidor efímero sin acks
// desde la secuencia siguiente. Así sobrevive a la retención de 30 días de
// DEVICES_EVENTS. Antes era un durable por rol, que tras un reinicio solo
// entregaba lo no confirmado y dejaba el inventario sin los routers y
// prefijos dados de alta por la API (todos los flujos `unknown`). name es el
// nombre de la instantánea y el de aquel durable, que se borra si existe.
func (p *Projector) Run(ctx context.Context, js jetstream.JetStream, name string, ready chan<- struct{}) {
	p.RunWithStore(ctx, js, nil, name, ready)
}

// RunWithStore es Run con almacén de instantáneas.
func (p *Projector) RunWithStore(ctx context.Context, js jetstream.JetStream, states flowstate.Store, name string, ready chan<- struct{}) {
	r := &flowstate.Replay{JS: js, Stream: "DEVICES_EVENTS", Filters: Subjects, Name: name, Legacy: name, Log: p.log,
		Apply:    func(_ string, data []byte) (bool, error) { return p.Apply(data) },
		Snapshot: p.Snapshot, Restore: p.Restore, Changed: p.publish}
	if states != nil {
		r.Store = states
	}
	r.Run(ctx, ready)
}

// Keep mantiene vivo el inventario de un rol: con NATS, proyección de
// DEVICES_EVENTS (instantánea `name` en flows_state + stream) sobre el
// contenido actual del Store (fichero, si lo hay); sin NATS, recarga del
// fichero. Bloquea hasta que ctx se cancela.
func Keep(ctx context.Context, store *Store, file string, js jetstream.JetStream, name string, log *slog.Logger) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if js != nil {
		var states flowstate.Store
		if os, err := flowstate.Open(ctx, js); err == nil {
			states = os
		} else {
			log.Warn("flows inventory snapshot store unavailable: replaying DEVICES_EVENTS only", "error", err)
		}
		NewProjector(store, store.Load().Data(), log).RunWithStore(ctx, js, states, name, nil)
		return
	}
	if file != "" {
		WatchFile(ctx, file, store, 5*time.Second, log)
	}
}
