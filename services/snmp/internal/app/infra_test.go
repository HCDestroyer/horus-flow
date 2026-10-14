package app

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/probe"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

func ptr[T any](v T) *T { return &v }

// infraMemStore es un InfraStore en memoria para el planificador.
type infraMemStore struct {
	mu       sync.Mutex
	services map[uuid.UUID]domain.Service
	events   []outbox.Event
	failSave bool
}

func newInfraMemStore(ss ...domain.Service) *infraMemStore {
	m := &infraMemStore{services: map[uuid.UUID]domain.Service{}}
	for _, s := range ss {
		m.services[s.ID] = s
	}
	return m
}

func (m *infraMemStore) List(_ context.Context, t pgdb.TenantID) ([]domain.Service, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Service
	for _, s := range m.services {
		if s.TenantID == t.UUID() {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *infraMemStore) Get(_ context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Service, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.services[id]
	if !ok || s.TenantID != t.UUID() {
		return nil, ErrInfraNotFound
	}
	return &s, nil
}

func (m *infraMemStore) Count(ctx context.Context, t pgdb.TenantID) (int, error) {
	l, _ := m.List(ctx, t)
	return len(l), nil
}

func (m *infraMemStore) Insert(_ context.Context, _ pgdb.TenantID, s *domain.Service) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range m.services {
		if o.TenantID == s.TenantID && o.Name == s.Name {
			return ErrInfraDuplicate
		}
	}
	m.services[s.ID] = *s
	return nil
}

func (m *infraMemStore) Update(_ context.Context, _ pgdb.TenantID, s *domain.Service, expect int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.services[s.ID]
	if !ok {
		return ErrInfraNotFound
	}
	if cur.Version != expect {
		return ErrInfraVersion
	}
	c := *s
	c.Version = expect + 1
	m.services[s.ID] = c
	return nil
}

func (m *infraMemStore) Delete(_ context.Context, _ pgdb.TenantID, id uuid.UUID, expect int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.services[id]
	if !ok {
		return ErrInfraNotFound
	}
	if cur.Version != expect {
		return ErrInfraVersion
	}
	delete(m.services, id)
	return nil
}

func (m *infraMemStore) LoadAll(context.Context) ([]domain.Service, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Service
	for _, s := range m.services {
		out = append(out, s)
	}
	return out, nil
}

func (m *infraMemStore) SaveState(_ context.Context, _ pgdb.TenantID, id uuid.UUID, st domain.ServiceStatus, checks []domain.CheckStatus, ev *outbox.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failSave {
		return errors.New("postgres caído")
	}
	s := m.services[id]
	s.Status, s.CheckStatus = st, checks
	m.services[id] = s
	if ev != nil {
		m.events = append(m.events, *ev)
	}
	return nil
}

type fakeProber struct {
	mu  sync.Mutex
	obs map[string]domain.CheckObservation
	req []probe.Request
}

func (f *fakeProber) set(typ string, o domain.CheckObservation) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.obs[typ] = o
}

func (f *fakeProber) Run(_ context.Context, r probe.Request) domain.CheckObservation {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.req = append(f.req, r)
	return f.obs[r.Check.Type]
}

type fakeFlows struct {
	data map[netip.Addr][]domain.MinuteTraffic
	err  error
}

func (f *fakeFlows) TargetTraffic(_ context.Context, _ uuid.UUID, _ []netip.Addr, _, _ time.Time) (map[netip.Addr][]domain.MinuteTraffic, error) {
	return f.data, f.err
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func TestSchedulerStateChangesAndEvent(t *testing.T) {
	tenant, site := uuid.New(), uuid.New()
	target := netip.MustParseAddr("192.0.2.20")
	svc := domain.Service{ID: uuid.New(), TenantID: tenant, SiteID: &site, Name: "IPTV cabecera", Target: target, Kind: "iptv",
		Checks:          []domain.Check{{ID: uuid.New(), Type: domain.CheckICMP}, {ID: uuid.New(), Type: domain.CheckHTTP, Port: ptr(8080)}},
		IntervalSeconds: 30, FailuresBeforeDown: 2, RecoveriesBeforeUp: 1, Enabled: true, Version: 3,
		Status: domain.ServiceStatus{State: domain.StateUnknown}}
	store := newInfraMemStore(svc)
	pr := &fakeProber{obs: map[string]domain.CheckObservation{
		domain.CheckICMP: {State: domain.StateUp, Reason: domain.ReasonOK, LatencyMs: ptr(3.0)},
		domain.CheckHTTP: {State: domain.StateUp, Reason: domain.ReasonOK, LatencyMs: ptr(20.0)},
	}}
	clk := &clock{t: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)}
	s := NewInfraScheduler(SchedulerOptions{Store: store, Prober: pr, Now: clk.now})
	ctx := context.Background()
	s.load(ctx)
	step := func() {
		clk.t = clk.t.Add(time.Minute) // > intervalo + jitter
		s.Tick(ctx)
	}
	step()
	if st := store.services[svc.ID].Status; st.State != domain.StateUp {
		t.Fatalf("tras el primer ciclo: %+v", st)
	}
	if len(store.events) != 1 || store.events[0].Data.(map[string]any)["previous_state"] != "unknown" {
		t.Fatalf("evento unknown→up: %+v", store.events)
	}
	if pr.req[0].Port != 8080 && pr.req[1].Port != 8080 {
		t.Fatalf("puerto http: %+v", pr.req)
	}
	pr.set(domain.CheckHTTP, domain.CheckObservation{State: domain.StateDown, Reason: domain.ReasonTimeout})
	step()
	if store.services[svc.ID].Status.State != domain.StateUp || len(store.events) != 1 {
		t.Fatal("un fallo no debe cambiar el estado (failures_before_down=2)")
	}
	step()
	st := store.services[svc.ID].Status
	if st.State != domain.StateDown || st.CheckType == nil || *st.CheckType != domain.CheckHTTP || *st.Reason != domain.ReasonTimeout {
		t.Fatalf("down por http: %+v", st)
	}
	if len(store.events) != 2 {
		t.Fatalf("eventos: %d", len(store.events))
	}
	ev := store.events[1]
	d := ev.Data.(map[string]any)
	want := map[string]any{"tenant_id": tenant.String(), "service_id": svc.ID.String(), "site_id": site.String(), "name": "IPTV cabecera",
		"check_type": "http", "target": "192.0.2.20", "previous_state": "up", "state": "down", "reason": "timeout"}
	for k, v := range want {
		if d[k] != v {
			t.Errorf("data.%s = %v, esperaba %v", k, d[k], v)
		}
	}
	if d["observed_at"] != clk.t.UTC().Format(outbox.TimeFormat) {
		t.Errorf("observed_at = %v", d["observed_at"])
	}
	if len(d) != 10 {
		t.Errorf("el payload tiene %d campos, el contrato 10: %v", len(d), d)
	}
	if ev.Type != TypeServiceStateChanged || ev.AggregateID != svc.ID || *ev.TenantID != tenant {
		t.Fatalf("sobre: %+v", ev)
	}
	// Sigue caído: no se repite el evento.
	step()
	if len(store.events) != 2 {
		t.Fatal("evento repetido sin cambio de estado")
	}
	// Recupera (recoveries_before_up = 1).
	pr.set(domain.CheckHTTP, domain.CheckObservation{State: domain.StateUp, Reason: domain.ReasonOK})
	step()
	if len(store.events) != 3 || store.events[2].Data.(map[string]any)["state"] != "up" ||
		store.events[2].Data.(map[string]any)["check_type"] != "http" {
		t.Fatalf("recuperación: %+v", store.events)
	}
	// Si no se puede guardar, el estado en memoria no avanza y el evento se reintenta.
	pr.set(domain.CheckICMP, domain.CheckObservation{State: domain.StateDown, Reason: domain.ReasonTimeout})
	store.failSave = true
	step()
	step()
	store.failSave = false
	step()
	step()
	if store.services[svc.ID].Status.State != domain.StateDown || len(store.events) != 4 {
		t.Fatalf("tras reintentar: %+v eventos=%d", store.services[svc.ID].Status, len(store.events))
	}
}

func TestPassiveChecks(t *testing.T) {
	tenant := uuid.New()
	target := netip.MustParseAddr("192.0.2.30")
	noTraffic := domain.Check{ID: uuid.New(), Type: domain.CheckNoTraffic, WindowMinutes: ptr(5), MinBytes: ptr(int64(1000))}
	ddos := domain.Check{ID: uuid.New(), Type: domain.CheckDDoS, PPSThreshold: ptr(1000.0)}
	svc := domain.Service{ID: uuid.New(), TenantID: tenant, Name: "DNS", Target: target, Kind: "dns", Checks: []domain.Check{noTraffic, ddos},
		IntervalSeconds: 60, FailuresBeforeDown: 1, RecoveriesBeforeUp: 1, Enabled: true, Version: 1}
	store := newInfraMemStore(svc)
	clk := &clock{t: time.Date(2026, 10, 14, 12, 0, 30, 0, time.UTC)}
	flows := &fakeFlows{data: map[netip.Addr][]domain.MinuteTraffic{}}
	s := NewInfraScheduler(SchedulerOptions{Store: store, Prober: &fakeProber{obs: map[string]domain.CheckObservation{}}, Flows: flows,
		Now: clk.now, FlowLag: 2 * time.Minute})
	ctx := context.Background()
	s.load(ctx)
	traffic := func(perMinBytes, perMinInPackets uint64) {
		to := clk.t.Add(-2 * time.Minute).Truncate(time.Minute)
		var rows []domain.MinuteTraffic
		for i := 1; i <= 5; i++ {
			rows = append(rows, domain.MinuteTraffic{Minute: to.Add(-time.Duration(i) * time.Minute), Bytes: perMinBytes, InBytes: perMinBytes / 2, InPackets: perMinInPackets})
		}
		flows.data[target] = rows
	}
	step := func() { clk.t = clk.t.Add(2 * time.Minute); s.Tick(ctx) }
	statusOf := func(c domain.Check) domain.CheckStatus {
		sv := store.services[svc.ID]
		return sv.StatusOf(c)
	}
	// Host ocioso desde siempre: sin alerta (unknown, sin línea base).
	step()
	if st := statusOf(noTraffic); st.State != domain.StateUnknown || *st.Reason != domain.ReasonNoBaseline {
		t.Fatalf("sin tráfico previo: %+v", st)
	}
	if store.services[svc.ID].Status.State == domain.StateDown {
		t.Fatal("no debe alertar sin tráfico previo")
	}
	traffic(10_000, 100)
	step()
	if st := statusOf(noTraffic); st.State != domain.StateUp || !st.HadTraffic {
		t.Fatalf("con tráfico: %+v", st)
	}
	// Se queda sin tráfico: down no_traffic.
	delete(flows.data, target)
	step()
	if st := statusOf(noTraffic); st.State != domain.StateDown || *st.Reason != domain.ReasonNoTraffic {
		t.Fatalf("sin tráfico tras tenerlo: %+v", st)
	}
	// Ataque: 600 000 paquetes/min entrantes = 10 000 pps > 1000 → degraded (el servicio sigue down por no_traffic? no: hay tráfico).
	traffic(50_000_000, 600_000)
	step()
	if st := statusOf(ddos); st.State != domain.StateDegraded || *st.Reason != domain.ReasonDDoSInbound {
		t.Fatalf("ddos: %+v", st)
	}
	sv := store.services[svc.ID].Status
	if sv.State != domain.StateDegraded || *sv.CheckType != domain.CheckDDoS {
		t.Fatalf("servicio degradado por ddos: %+v", sv)
	}
	last := store.events[len(store.events)-1].Data.(map[string]any)
	if last["check_type"] != "ddos_inbound" || last["state"] != "degraded" || last["reason"] != "ddos_inbound" || last["site_id"] != nil {
		t.Fatalf("evento ddos: %v", last)
	}
	// ClickHouse caído: un ciclo no cambia nada.
	flows.err = errors.New("clickhouse caído")
	step()
	if statusOf(ddos).State != domain.StateDegraded {
		t.Fatal("un ciclo sin datos no debe cambiar el estado")
	}
}

func TestViaRouterICMPNotForwardable(t *testing.T) {
	rid := uuid.New()
	svc := domain.Service{ID: uuid.New(), TenantID: uuid.New(), Name: "x", Target: netip.MustParseAddr("10.0.0.5"), Kind: "http",
		Checks:           []domain.Check{{ID: uuid.New(), Type: domain.CheckICMP}, {ID: uuid.New(), Type: domain.CheckTCP, Port: ptr(80), ForwardPort: ptr(18080)}},
		ProbeViaRouterID: &rid, IntervalSeconds: 30, FailuresBeforeDown: 1, RecoveriesBeforeUp: 1, Enabled: true, Version: 1}
	store := newInfraMemStore(svc)
	pr := &fakeProber{obs: map[string]domain.CheckObservation{}}
	clk := &clock{t: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)}
	s := NewInfraScheduler(SchedulerOptions{Store: store, Prober: pr, Now: clk.now})
	s.load(context.Background())
	clk.t = clk.t.Add(time.Minute)
	s.Tick(context.Background())
	cs := store.services[svc.ID].CheckStatus
	if cs[0].State != domain.StateUnknown || *cs[0].Reason != domain.ReasonNotForwardable {
		t.Fatalf("icmp por router: %+v", cs[0])
	}
	// Sin devices en el proceso el router no se resuelve: unknown, sin sondear.
	if cs[1].State != domain.StateUnknown || *cs[1].Reason != domain.ReasonRouterOff || len(pr.req) != 0 {
		t.Fatalf("tcp por router sin devices: %+v (%d sondeos)", cs[1], len(pr.req))
	}
}
