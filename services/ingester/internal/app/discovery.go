package app

import (
	"context"
	"log/slog"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
)

// Publisher publica un mensaje NATS con confirmación.
type Publisher interface {
	PublishMsg(ctx context.Context, m *nats.Msg) error
}

// DiscoveryOptions configura el descubrimiento de clientes (I1-05).
type DiscoveryOptions struct {
	// TTL evita repetir first_seen de una clave aún no confirmada por devices.
	TTL time.Duration
	// PerMinute limita las claves nuevas por realm y minuto (anti-avalancha).
	PerMinute int
	// RealmMax es el tope de claves conocidas + pendientes por realm.
	RealmMax int
	// FirstSeenChunk es el máximo de clientes por evento (FLOWS_EVENTS: 64 KiB).
	FirstSeenChunk int
	// ActivityChunk es el máximo de clientes por parte del resumen (< 512 KiB).
	ActivityChunk int
}

type pendingClient struct {
	prefix uuid.UUID
	first  time.Time
}

type activity struct {
	last  time.Time
	bytes uint64
	flows uint32
}

type realmState struct {
	tenant   uuid.UUID
	router   uuid.UUID
	known    map[netip.Addr]struct{}
	emitted  map[netip.Addr]time.Time
	pending  map[netip.Addr]pendingClient
	window   time.Time // inicio de la ventana de first_seen
	minute   time.Time
	newInMin int
	active   map[netip.Addr]*activity
}

// Discovery mantiene el conjunto de clientes conocidos por realm y publica
// horus.flows.client.first_seen.<realm_id> (lotes deduplicados cada 10 s,
// FLOWS_EVENTS, JSON) y horus.flows.client.activity_summary
// (horus.telemetry.flows.client_activity.<realm_id>, Protobuf, cada hora).
// Nunca escribe en PostgreSQL: devices crea el cliente (ADR-0018).
type Discovery struct {
	opts      DiscoveryOptions
	pub       Publisher
	log       *slog.Logger
	throttled prometheus.Counter
	firstSeen prometheus.Counter

	mu     sync.Mutex
	realms map[uuid.UUID]*realmState
	hour   time.Time
}

// NewDiscovery crea el descubridor; reg puede ser nil.
func NewDiscovery(o DiscoveryOptions, pub Publisher, reg prometheus.Registerer, log *slog.Logger) *Discovery {
	if o.TTL <= 0 {
		o.TTL = time.Hour
	}
	if o.PerMinute <= 0 {
		o.PerMinute = 2000
	}
	if o.RealmMax <= 0 {
		o.RealmMax = 1 << 20
	}
	if o.FirstSeenChunk <= 0 {
		o.FirstSeenChunk = 400
	}
	if o.ActivityChunk <= 0 {
		o.ActivityChunk = 6000
	}
	d := &Discovery{opts: o, pub: pub, log: log, realms: map[uuid.UUID]*realmState{},
		throttled: prometheus.NewCounter(prometheus.CounterOpts{Name: "horus_customers_discovery_throttled_total",
			Help: "Claves de cliente nuevas descartadas por el límite anti-avalancha."}),
		firstSeen: prometheus.NewCounter(prometheus.CounterOpts{Name: "horus_ingester_first_seen_total",
			Help: "Claves de cliente publicadas en first_seen."}),
	}
	if reg != nil {
		_ = reg.Register(d.throttled)
		_ = reg.Register(d.firstSeen)
	}
	return d
}

func (d *Discovery) realm(id, tenant uuid.UUID) *realmState {
	r := d.realms[id]
	if r == nil {
		r = &realmState{tenant: tenant, known: map[netip.Addr]struct{}{}, emitted: map[netip.Addr]time.Time{},
			pending: map[netip.Addr]pendingClient{}, active: map[netip.Addr]*activity{}}
		d.realms[id] = r
	}
	return r
}

// LoadKnown añade claves conocidas (snapshot de devices o del inventario).
func (d *Discovery) LoadKnown(keys []flowinv.CustomerKey) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, k := range keys {
		r := d.realm(k.RealmID, k.TenantID)
		r.known[k.Address.Unmap()] = struct{}{}
		delete(r.pending, k.Address.Unmap())
	}
}

// Forget quita una clave (customer.purged: si reaparece es un cliente nuevo).
func (d *Discovery) Forget(realm uuid.UUID, addr netip.Addr) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if r := d.realms[realm]; r != nil {
		delete(r.known, addr.Unmap())
		delete(r.emitted, addr.Unmap())
	}
}

// Observe implementa Observer.
func (d *Discovery) Observe(e Exporter, row *Row) {
	if row.AttributionStatus != StatusAttributed && row.AttributionStatus != StatusInternal {
		return
	}
	addr := row.ClientIP
	ts := row.TS
	d.mu.Lock()
	defer d.mu.Unlock()
	r := d.realm(row.RealmID, e.TenantID)
	r.router = e.RouterID
	a := r.active[addr]
	if a == nil {
		a = &activity{}
		r.active[addr] = a
	}
	if ts.After(a.last) {
		a.last = ts
	}
	a.bytes += row.Bytes
	a.flows++
	if _, ok := r.known[addr]; ok {
		return
	}
	if _, ok := r.pending[addr]; ok {
		return
	}
	if at, ok := r.emitted[addr]; ok && time.Since(at) < d.opts.TTL {
		return
	}
	now := time.Now()
	if m := now.Truncate(time.Minute); !m.Equal(r.minute) {
		r.minute, r.newInMin = m, 0
	}
	if r.newInMin >= d.opts.PerMinute || len(r.known)+len(r.pending) >= d.opts.RealmMax {
		d.throttled.Inc()
		return
	}
	r.newInMin++
	if len(r.pending) == 0 {
		r.window = now
	}
	r.pending[addr] = pendingClient{prefix: row.ClientPrefixID, first: ts}
}

// FirstSeenClient es un cliente del evento first_seen (JSON del contrato).
type FirstSeenClient struct {
	Address        string `json:"address"`
	ClientPrefixID string `json:"client_prefix_id,omitempty"`
	FirstSeen      string `json:"first_seen"`
}

// ClientFirstSeen es el payload de horus.flows.client.first_seen.
type ClientFirstSeen struct {
	BatchID    string            `json:"batch_id"`
	RealmID    string            `json:"realm_id"`
	RouterID   string            `json:"router_id"`
	WindowFrom string            `json:"window_from"`
	WindowTo   string            `json:"window_to"`
	Clients    []FirstSeenClient `json:"clients"`
}

type firstSeenBatch struct {
	tenant  uuid.UUID
	realm   uuid.UUID
	payload ClientFirstSeen
	addrs   []netip.Addr
}

// FlushFirstSeen publica las claves pendientes de cada realm.
func (d *Discovery) FlushFirstSeen(ctx context.Context) {
	now := time.Now()
	var batches []firstSeenBatch
	d.mu.Lock()
	for id, r := range d.realms {
		if len(r.pending) == 0 {
			continue
		}
		addrs := make([]netip.Addr, 0, len(r.pending))
		for a := range r.pending {
			addrs = append(addrs, a)
		}
		sort.Slice(addrs, func(i, j int) bool { return addrs[i].Less(addrs[j]) })
		for start := 0; start < len(addrs); start += d.opts.FirstSeenChunk {
			chunk := addrs[start:min(start+d.opts.FirstSeenChunk, len(addrs))]
			b := firstSeenBatch{tenant: r.tenant, realm: id, addrs: chunk, payload: ClientFirstSeen{
				BatchID: newID(), RealmID: id.String(), RouterID: r.router.String(),
				WindowFrom: r.window.UTC().Format(time.RFC3339), WindowTo: now.UTC().Format(time.RFC3339)}}
			for _, a := range chunk {
				p := r.pending[a]
				c := FirstSeenClient{Address: a.String(), FirstSeen: p.first.UTC().Format(time.RFC3339)}
				if p.prefix != uuid.Nil {
					c.ClientPrefixID = p.prefix.String()
				}
				b.payload.Clients = append(b.payload.Clients, c)
			}
			batches = append(batches, b)
		}
		r.pending = map[netip.Addr]pendingClient{}
		for a, at := range r.emitted {
			if now.Sub(at) >= d.opts.TTL {
				delete(r.emitted, a)
			}
		}
	}
	d.mu.Unlock()
	for _, b := range batches {
		tenant := b.tenant
		bid, _ := uuid.Parse(b.payload.BatchID)
		m, err := flowbus.Event{ID: bid, Type: flowbus.TypeClientFirstSeen, Source: "horus/flows/ingester", Entity: b.realm.String(),
			TenantID: &tenant, AggregateType: "realm", AggregateVersion: 1, Time: now, Data: b.payload}.Msg()
		if err == nil {
			err = d.pub.PublishMsg(ctx, m)
		}
		d.mu.Lock()
		r := d.realms[b.realm]
		for _, a := range b.addrs {
			if err == nil {
				r.emitted[a] = now
			}
		}
		d.mu.Unlock()
		if err != nil {
			// Sin spool (events.md §8.5): el siguiente flujo de la clave la repite.
			d.log.Warn("first_seen not published", "realm_id", b.realm, "clients", len(b.addrs), "error", err)
			continue
		}
		d.firstSeen.Add(float64(len(b.addrs)))
	}
}

// FlushActivity publica el resumen de actividad acumulado (cada hora) por realm.
func (d *Discovery) FlushActivity(ctx context.Context) {
	now := time.Now()
	type part struct {
		tenant, realm uuid.UUID
		msg           flowpb.ClientActivitySummary
	}
	var parts []part
	d.mu.Lock()
	hour := d.hour
	if hour.IsZero() {
		hour = now.Add(-time.Minute).Truncate(time.Hour)
	}
	for id, r := range d.realms {
		if len(r.active) == 0 {
			continue
		}
		addrs := make([]netip.Addr, 0, len(r.active))
		for a := range r.active {
			addrs = append(addrs, a)
		}
		sort.Slice(addrs, func(i, j int) bool { return addrs[i].Less(addrs[j]) })
		total := (len(addrs) + d.opts.ActivityChunk - 1) / d.opts.ActivityChunk
		for i := 0; i < total; i++ {
			chunk := addrs[i*d.opts.ActivityChunk : min((i+1)*d.opts.ActivityChunk, len(addrs))]
			s := flowpb.ClientActivitySummary{BatchID: newID(), RealmID: id.String(), Hour: hour.UTC(),
				Part: int32(i + 1), Parts: int32(total)} //nolint:gosec // partes acotadas
			for _, a := range chunk {
				ac := r.active[a]
				s.Clients = append(s.Clients, flowpb.ActiveClient{Address: a.String(), LastSeen: ac.last.UTC(), BytesEst: ac.bytes, Flows: ac.flows})
			}
			parts = append(parts, part{tenant: r.tenant, realm: id, msg: s})
		}
		r.active = map[netip.Addr]*activity{}
	}
	d.hour = now.Truncate(time.Hour)
	d.mu.Unlock()
	for _, p := range parts {
		m := flowbus.Telemetry{Subject: flowbus.SubjectActivityPrefix + p.realm.String(), Type: flowbus.TypeClientActivity,
			Source: "horus/flows/ingester", TenantID: p.tenant.String(), MsgID: p.msg.BatchID,
			ContentType: flowpb.ContentTypeActivitySummary, Time: now, Body: p.msg.Marshal()}.Msg()
		if err := d.pub.PublishMsg(ctx, m); err != nil {
			d.log.Warn("activity_summary not published", "realm_id", p.realm, "error", err)
		}
	}
}

// Run publica first_seen cada firstEvery y activity_summary cada activityEvery.
func (d *Discovery) Run(ctx context.Context, firstEvery, activityEvery time.Duration) {
	ft := time.NewTicker(firstEvery)
	defer ft.Stop()
	at := time.NewTicker(activityEvery)
	defer at.Stop()
	for {
		select {
		case <-ctx.Done():
			d.FlushFirstSeen(context.WithoutCancel(ctx))
			return
		case <-ft.C:
			d.FlushFirstSeen(ctx)
		case <-at.C:
			d.FlushActivity(ctx)
		}
	}
}

func newID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}
