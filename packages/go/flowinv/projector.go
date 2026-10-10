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

// Run consume DEVICES_EVENTS desde el principio hasta que ctx se cancela.
// ready se cierra al alcanzar el final del stream por primera vez. Si el
// stream no existe (devices no desplegado) reintenta cada 30 s.
//
// La proyección vive en memoria, así que cada arranque del proceso debe
// releer el stream entero: se usa un consumidor efímero sin acks con
// DeliverAll (si se pierde, se recrea y se relee todo: Apply es idempotente
// por versión). Antes era un durable por rol, que tras un reinicio solo
// entregaba lo no confirmado y dejaba el inventario sin los routers y
// prefijos dados de alta por la API (todos los flujos `unknown`). legacy es
// el nombre de aquel durable: se borra si existe.
func (p *Projector) Run(ctx context.Context, js jetstream.JetStream, legacy string, ready chan<- struct{}) {
	var once sync.Once
	markReady := func() {
		if ready != nil {
			once.Do(func() { close(ready) })
		}
	}
	cleaned := false
	for ctx.Err() == nil {
		if !cleaned && legacy != "" {
			if err := js.DeleteConsumer(ctx, "DEVICES_EVENTS", legacy); err == nil || errors.Is(err, jetstream.ErrConsumerNotFound) {
				cleaned = true
			}
		}
		cons, err := js.CreateConsumer(ctx, "DEVICES_EVENTS", jetstream.ConsumerConfig{
			DeliverPolicy: jetstream.DeliverAllPolicy, AckPolicy: jetstream.AckNonePolicy,
			FilterSubjects:    []string{"horus.devices.router.>", "horus.devices.site.>", "horus.devices.realm.>", "horus.devices.client_prefix.>"},
			InactiveThreshold: 5 * time.Minute,
		})
		if err != nil {
			markReady()
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
				continue
			}
		}
		for ctx.Err() == nil {
			batch, err := cons.FetchNoWait(256)
			if err != nil {
				break
			}
			changed, n := false, 0
			for m := range batch.Messages() {
				n++
				if c, err := p.Apply(m.Data()); err != nil {
					p.log.Warn("devices event ignored by flows inventory", "subject", m.Subject(), "error", err)
				} else if c {
					changed = true
				}
			}
			if changed {
				p.publish()
			}
			if n == 0 {
				markReady()
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
				}
			}
		}
	}
}

// Keep mantiene vivo el inventario de un rol: con NATS, proyección de
// DEVICES_EVENTS (durable propio del rol) sobre el contenido actual del
// Store (fichero, si lo hay); sin NATS, recarga del fichero. Bloquea hasta
// que ctx se cancela.
func Keep(ctx context.Context, store *Store, file string, js jetstream.JetStream, durable string, log *slog.Logger) {
	if js != nil {
		NewProjector(store, store.Load().Data(), log).Run(ctx, js, durable, nil)
		return
	}
	if file != "" {
		WatchFile(ctx, file, store, 5*time.Second, log)
	}
}
