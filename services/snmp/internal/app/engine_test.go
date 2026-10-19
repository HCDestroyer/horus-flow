package app

import (
	"context"
	"errors"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/icmp"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/snmpclient"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/config"
)

// --- dobles ---

type fakeTransport struct {
	mu   sync.Mutex
	vars map[string]any
	err  error
}

func (f *fakeTransport) Get(_ context.Context, oids []string) ([]snmpclient.Var, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := make([]snmpclient.Var, 0, len(oids))
	for _, o := range oids {
		out = append(out, snmpclient.Var{OID: o, Value: f.vars[o]})
	}
	return out, nil
}

func (f *fakeTransport) Walk(_ context.Context, root string) ([]snmpclient.Var, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	var keys []string
	for k := range f.vars {
		if strings.HasPrefix(k, root+".") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]snmpclient.Var, 0, len(keys))
	for _, k := range keys {
		out = append(out, snmpclient.Var{OID: k, Value: f.vars[k]})
	}
	return out, nil
}

func (f *fakeTransport) Close() error { return nil }

func (f *fakeTransport) set(k string, v any) {
	f.mu.Lock()
	f.vars[k] = v
	f.mu.Unlock()
}

type fakeDialer struct {
	mu   sync.Mutex
	by   map[netip.Addr]*fakeTransport
	errs map[netip.Addr]error
	seen []snmpclient.Credential
}

func (d *fakeDialer) Dial(_ context.Context, target netip.Addr, cred snmpclient.Credential) (snmpclient.Transport, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seen = append(d.seen, cred)
	if err := d.errs[target]; err != nil {
		return nil, err
	}
	t := d.by[target]
	if t == nil {
		return nil, errors.New("request timeout")
	}
	return t, nil
}

type fakePinger struct {
	mu   sync.Mutex
	down map[netip.Addr]bool
	seen map[netip.Addr]int
}

func (p *fakePinger) count(a netip.Addr) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.seen[a]
}

func (p *fakePinger) Ping(_ context.Context, addr netip.Addr, probes int, _ time.Duration) (icmp.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.seen == nil {
		p.seen = map[netip.Addr]int{}
	}
	p.seen[addr]++
	if p.down[addr] {
		return icmp.Result{Sent: probes}, nil
	}
	rtt := 8 * time.Millisecond
	return icmp.Result{Sent: probes, Received: probes, AvgRTT: &rtt}, nil
}

type fakeDevices struct {
	mu      sync.Mutex
	routers []devapi.RouterInfo
	creds   map[uuid.UUID]devapi.SNMPCredential
	reveals int
}

func (d *fakeDevices) GetRouter(context.Context, uuid.UUID, uuid.UUID) (devapi.RouterInfo, error) {
	return devapi.RouterInfo{}, devapi.ErrRouterNotFound
}
func (d *fakeDevices) ListRouters(context.Context) ([]devapi.RouterInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]devapi.RouterInfo(nil), d.routers...), nil
}
func (d *fakeDevices) SetTunnel(context.Context, uuid.UUID, uuid.UUID, devapi.TunnelUpdate) error {
	return nil
}
func (d *fakeDevices) IssueCredentials(context.Context, uuid.UUID, uuid.UUID) (devapi.OnboardingCredentials, error) {
	return devapi.OnboardingCredentials{}, nil
}
func (d *fakeDevices) RevealSNMPCredential(_ context.Context, _ uuid.UUID, id uuid.UUID) (devapi.SNMPCredential, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reveals++
	c, ok := d.creds[id]
	if !ok {
		return devapi.SNMPCredential{}, devapi.ErrNoCredential
	}
	return c, nil
}

type memStore struct {
	mu   sync.Mutex
	data map[uuid.UUID][]byte
}

func (s *memStore) Load(context.Context) (map[uuid.UUID][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[uuid.UUID][]byte{}
	for k, v := range s.data {
		out[k] = v
	}
	return out, nil
}
func (s *memStore) Save(_ context.Context, id uuid.UUID, b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		s.data = map[uuid.UUID][]byte{}
	}
	s.data[id] = b
	return nil
}
func (s *memStore) Delete(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, id)
	return nil
}

type memEvents struct {
	mu  sync.Mutex
	evs []Event
}

func (m *memEvents) Publish(_ context.Context, ev Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evs = append(m.evs, ev)
	return nil
}

func (m *memEvents) types() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, e := range m.evs {
		out = append(out, e.Type)
	}
	return out
}

func (m *memEvents) last(typ string) *Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.evs) - 1; i >= 0; i-- {
		if m.evs[i].Type == typ {
			e := m.evs[i]
			return &e
		}
	}
	return nil
}

type memTelemetry struct {
	mu   sync.Mutex
	subs []string
}

func (m *memTelemetry) Publish(_ context.Context, s Sample) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs = append(m.subs, s.Subject)
	return nil
}

func routerOS(uptime uint64, cpu int64) *fakeTransport {
	v := map[string]any{
		snmpclient.OIDSysDescr: "RouterOS CCR2116-12G-4S+", snmpclient.OIDSysUpTime: uptime,
		snmpclient.OIDHrStorageTable + ".2.65536": snmpclient.OIDHrStorageRAM, snmpclient.OIDHrStorageTable + ".4.65536": uint64(1024),
		snmpclient.OIDHrStorageTable + ".5.65536": uint64(2097152), snmpclient.OIDHrStorageTable + ".6.65536": uint64(1258291),
		snmpclient.OIDMtxrHlTemperature: int64(470), snmpclient.OIDMtxrLicVersion: "7.16.2",
		snmpclient.OIDIfName + ".7": "sfp-sfpplus1", snmpclient.OIDIfType + ".7": int64(6), snmpclient.OIDIfHighSpeed + ".7": uint64(10000),
		snmpclient.OIDIfAdminStatus + ".7": int64(1), snmpclient.OIDIfOperStatus + ".7": int64(1), snmpclient.OIDIfAlias + ".7": "UPLINK",
		snmpclient.OIDIfName + ".3001": "<pppoe-juan>", snmpclient.OIDIfType + ".3001": int64(23),
		snmpclient.OIDIfHCInOctets + ".7": uint64(1000), snmpclient.OIDIfHCOutOctets + ".7": uint64(500),
		snmpclient.OIDIfHCInUcast + ".7": uint64(10), snmpclient.OIDIfHCOutUcast + ".7": uint64(5),
		snmpclient.OIDIfInErrors + ".7": uint64(0), snmpclient.OIDIfOutErrors + ".7": uint64(0), snmpclient.OIDIfInDiscards + ".7": uint64(0), snmpclient.OIDIfOutDiscards + ".7": uint64(0),
	}
	for i := 1; i <= 4; i++ {
		v[snmpclient.OIDHrProcessorLoad+"."+strconv.Itoa(i)] = cpu
	}
	return &fakeTransport{vars: v}
}

type harness struct {
	eng    *Engine
	dev    *fakeDevices
	dial   *fakeDialer
	ping   *fakePinger
	store  *memStore
	events *memEvents
	tele   *memTelemetry
	now    time.Time
	mu     sync.Mutex
}

func (h *harness) clock() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now
}

func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	h.now = h.now.Add(d)
	h.mu.Unlock()
}

var (
	tenantA = uuid.MustParse("0192e000-0000-7000-8000-000000000001")
	tenantB = uuid.MustParse("0192e000-0000-7000-8000-000000000002")
	siteA   = uuid.MustParse("0192e111-0000-7000-8000-000000000011")
	siteA2  = uuid.MustParse("0192e111-0000-7000-8000-000000000012")
	siteB   = uuid.MustParse("0192e111-0000-7000-8000-000000000021")
	rtA1    = uuid.MustParse("0192e333-0000-7000-8000-000000000031")
	rtA2    = uuid.MustParse("0192e333-0000-7000-8000-000000000032")
	rtB1    = uuid.MustParse("0192e333-0000-7000-8000-000000000033")
	ipA1    = netip.MustParseAddr("10.255.0.1")
	ipA2    = netip.MustParseAddr("10.255.0.2")
	ipB1    = netip.MustParseAddr("10.255.0.3")
)

func newHarness(t *testing.T, store *memStore) *harness {
	t.Helper()
	addr := func(a netip.Addr) *netip.Addr { return &a }
	h := &harness{now: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC), store: store, events: &memEvents{}, tele: &memTelemetry{},
		ping: &fakePinger{down: map[netip.Addr]bool{}}}
	h.dev = &fakeDevices{routers: []devapi.RouterInfo{
		{ID: rtA1, TenantID: tenantA, SiteID: siteA, Name: "core-1", OnboardingState: devapi.OnboardingExporting, TunnelAddress: addr(ipA1)},
		{ID: rtA2, TenantID: tenantA, SiteID: siteA2, Name: "agg-2", OnboardingState: devapi.OnboardingTunnelUp, TunnelAddress: addr(ipA2)},
		{ID: rtB1, TenantID: tenantB, SiteID: siteB, Name: "otro-isp", OnboardingState: devapi.OnboardingExporting, TunnelAddress: addr(ipB1)},
		{ID: uuid.New(), TenantID: tenantA, SiteID: siteA, Name: "pendiente", OnboardingState: devapi.OnboardingPending},
	}, creds: map[uuid.UUID]devapi.SNMPCredential{
		rtA1: {Username: "horus-1", AuthProtocol: "sha1", AuthPassword: "a", PrivProtocol: "aes128", PrivPassword: "p"},
		rtB1: {Username: "horus-3", AuthProtocol: "sha1", AuthPassword: "a", PrivProtocol: "aes128", PrivPassword: "p"},
	}}
	h.dial = &fakeDialer{by: map[netip.Addr]*fakeTransport{ipA1: routerOS(1000, 10), ipB1: routerOS(1000, 10)}, errs: map[netip.Addr]error{}}
	cfg := config.Config{Enabled: true, Interval: time.Minute, HealthInterval: 5 * time.Minute, ICMPInterval: 30 * time.Second,
		DiscoveryInterval: time.Hour, Timeout: time.Second, Retries: 0, Concurrency: 4, CPUWarning: 80, CPUCritical: 95,
		MemoryWarning: 85, MemoryCritical: 95, TemperatureWarning: 70, TemperatureCritical: 85, InstanceID: "test-1"}
	h.eng = New(Options{Config: cfg, Dialer: h.dial, Pinger: h.ping, Events: h.events, Telemetry: h.tele, Store: store, Now: h.clock,
		Devices:     func() (devapi.Onboarding, bool) { return h.dev, true },
		Credentials: func() (devapi.SNMPCredentialReader, bool) { return h.dev, true }})
	if err := h.eng.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) cycle() {
	ctx := context.Background()
	h.eng.runICMP(ctx)
	h.eng.runCycle(ctx, h.clock())
	h.advance(time.Minute)
}

func TestEngineEndToEnd(t *testing.T) {
	h := newHarness(t, &memStore{})
	ctx := context.Background()
	// Objetivos: solo routers con túnel y alta avanzada.
	if n := len(h.eng.snapshot()); n != 3 {
		t.Fatalf("objetivos = %d, quiero 3", n)
	}
	h.cycle()
	rows, health, err := h.eng.List(ctx, tenantA, nil)
	if err != nil || len(rows) != 2 || !health.Healthy || health.Targets != 2 || health.IntervalSeconds != 60 {
		t.Fatalf("List A: %d filas, %+v, %v", len(rows), health, err)
	}
	byName := map[string]api.RouterStatus{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	a1 := byName["core-1"]
	if a1.Status != api.StatusOnline || *a1.Reason != api.ReasonOK || a1.Signals.SNMP != "ok" || a1.Signals.ICMP != "ok" || a1.Signals.WireGuard != "unknown" {
		t.Fatalf("core-1 tras el primer ciclo: %+v", a1)
	}
	if a1.Device.CPUPercent == nil || *a1.Device.CPUPercent != 10 || *a1.Device.CPUCores != 4 || *a1.Device.Model != "CCR2116-12G-4S+" ||
		*a1.Device.TemperatureCelsius != 47 || *a1.Device.Firmware != "7.16.2" || *a1.Device.UptimeSeconds != 10 || *a1.Device.ICMPRTTMs != 8 {
		t.Fatalf("muestra core-1: %+v", a1.Device)
	}
	if a1.Uplink == nil || a1.Uplink.Name != "sfp-sfpplus1" || a1.Uplink.RxBps != nil || *a1.InterfacesTotal != 1 || *a1.InterfacesUp != 1 {
		t.Fatalf("uplink/interfaces primer ciclo (tasas null): %+v", a1.Uplink)
	}
	// agg-2 sin credenciales: degraded/no_snmp_credentials (ICMP responde), el orden lo pone primero.
	if rows[0].Name != "agg-2" || rows[0].Status != api.StatusDegraded || *rows[0].Reason != api.ReasonNoCredentials {
		t.Fatalf("agg-2: %+v", rows[0])
	}
	// Segundo ciclo: contadores avanzan → tasas; alcance por nodo.
	h.dial.by[ipA1].set(snmpclient.OIDIfHCInOctets+".7", uint64(1000+60*125_000_000)) // 1 Gbit/s
	h.dial.by[ipA1].set(snmpclient.OIDIfHCOutOctets+".7", uint64(500+60*12_500_000))
	h.cycle()
	live, found, _ := h.eng.Live(ctx, tenantA, rtA1)
	if !found || len(live.Interfaces) != 1 || live.Interfaces[0].RxBps == nil || *live.Interfaces[0].RxBps != 1e9 || !live.Interfaces[0].Uplink ||
		*live.Interfaces[0].UtilizationRx != 0.1 || live.Interfaces[0].CounterDiscontinuity {
		t.Fatalf("live tras 2º ciclo: %+v", live.Interfaces)
	}
	if _, found, _ := h.eng.Live(ctx, tenantB, rtA1); found {
		t.Fatal("router de A visible desde B")
	}
	if rows, _, _ := h.eng.List(ctx, tenantA, []uuid.UUID{siteA2}); len(rows) != 1 || rows[0].Name != "agg-2" {
		t.Fatalf("filtro por nodo: %+v", rows)
	}
	ifs, found, _ := h.eng.Interfaces(ctx, tenantA, rtA1)
	if !found || len(ifs.Data) != 2 || ifs.Data[1].Name != "<pppoe-juan>" || ifs.Data[1].Monitored || !ifs.Data[0].Uplink || ifs.Data[0].Type != "ethernetCsmacd" ||
		ifs.Data[0].ID != api.InterfaceID(rtA1, "sfp-sfpplus1") {
		t.Fatalf("interfaces: %+v", ifs)
	}
	// Reinicio: uptime menor → rebooted y tasas null (discontinuidad).
	h.dial.by[ipA1].set(snmpclient.OIDSysUpTime, uint64(50))
	h.cycle()
	if ev := h.events.last(TypeRebooted); ev == nil {
		t.Fatalf("falta rebooted: %v", h.events.types())
	}
	live, _, _ = h.eng.Live(ctx, tenantA, rtA1)
	if !live.Interfaces[0].CounterDiscontinuity || live.Interfaces[0].RxBps != nil {
		t.Fatalf("tras reinicio: %+v", live.Interfaces[0])
	}
	// CPU alta sostenida: 3 ciclos para warning, con histéresis de 2 observaciones del estado.
	h.dial.by[ipA1].set(snmpclient.OIDSysUpTime, uint64(50_000))
	for i := 1; i <= 4; i++ {
		h.dial.by[ipA1].set(snmpclient.OIDHrProcessorLoad+"."+strconv.Itoa(i), int64(90))
	}
	for range 3 {
		h.cycle()
	}
	live, _, _ = h.eng.Live(ctx, tenantA, rtA1)
	if live.Status.Status != api.StatusOnline || len(live.Status.Thresholds) != 2 {
		t.Fatalf("3 ciclos de cpu alta: umbral sí, estado aún online por histéresis: %+v", live.Status)
	}
	h.cycle()
	live, _, _ = h.eng.Live(ctx, tenantA, rtA1)
	if live.Status.Status != api.StatusWarning || *live.Status.Reason != api.ReasonCPUHigh {
		t.Fatalf("4º ciclo: warning cpu_high: %+v", live.Status)
	}
	ev := h.events.last(TypeStateChanged)
	data := ev.Data.(map[string]any)
	if data["state"] != api.StatusWarning || data["reason"] != api.ReasonCPUHigh || *data["previous_state"].(*string) != api.StatusOnline || ev.TenantID == nil || *ev.TenantID != tenantA {
		t.Fatalf("state_changed: %+v", data)
	}
	// Túnel caído: ICMP ✗ (3 ciclos de ping), SNMP ✗, WG stale → offline/tunnel_down.
	h.ping.down[ipA1] = true
	h.dial.errs[ipA1] = errors.New("request timeout")
	h.eng.SetWireGuard(tenantA, rtA1, api.SignalStale, h.clock())
	for range 2 {
		h.eng.runICMP(ctx)
	}
	for range 3 {
		h.cycle()
	}
	live, _, _ = h.eng.Live(ctx, tenantA, rtA1)
	if live.Status.Status != api.StatusOffline || *live.Status.Reason != api.ReasonTunnelDown || live.Status.Signals.ICMP != "fail" || live.Status.Signals.WireGuard != "stale" {
		t.Fatalf("túnel caído: %+v", live.Status)
	}
	// Credencial rechazada: auth_failed, se recarga la credencial.
	h.ping.down[ipA1] = false
	before := h.dev.reveals
	h.dial.errs[ipA1] = snmpclient.ErrAuth
	for range 3 {
		h.cycle()
	}
	live, _, _ = h.eng.Live(ctx, tenantA, rtA1)
	if live.Status.Status != api.StatusDegraded || *live.Status.Reason != api.ReasonSNMPAuthFailed || h.dev.reveals <= before {
		t.Fatalf("auth: %+v reveals %d→%d", live.Status, before, h.dev.reveals)
	}
	// Telemetría y eventos publicados.
	if h.events.last(TypeInterfacesDiscovered) == nil || len(h.tele.subs) == 0 || !strings.HasPrefix(h.tele.subs[0], "horus.telemetry.snmp.device.") {
		t.Fatalf("telemetría/eventos: %v %v", h.events.types(), h.tele.subs[:1])
	}
	// Baja de un router: desaparece y se borra del KV.
	h.dev.mu.Lock()
	h.dev.routers = h.dev.routers[:2]
	h.dev.mu.Unlock()
	h.eng.refreshTargets(ctx)
	if _, found, _ := h.eng.Live(ctx, tenantB, rtB1); found {
		t.Fatal("router dado de baja sigue")
	}
	if _, ok := h.store.data[rtB1]; ok {
		t.Fatal("estado del router de baja no borrado del KV")
	}
}

func TestEngineRestartKeepsSinceAndEffectiveStatus(t *testing.T) {
	store := &memStore{}
	h := newHarness(t, store)
	ctx := context.Background()
	h.cycle()
	rows, _, _ := h.eng.List(ctx, tenantA, nil)
	var since time.Time
	for _, r := range rows {
		if r.RouterID == rtA1 {
			since = *r.Since
		}
	}
	// Reinicio del rol: nuevo engine con el mismo KV y reloj avanzado 2 minutos.
	h2 := newHarness(t, store)
	h2.now = h.clock().Add(2 * time.Minute)
	rows, health, _ := h2.eng.List(ctx, tenantA, nil)
	if health.Healthy {
		t.Fatal("sin ciclo todavía el poller no late")
	}
	for _, r := range rows {
		if r.RouterID != rtA1 {
			continue
		}
		if r.Status != api.StatusStale || r.ObservedState == nil || *r.ObservedState != api.StatusOnline {
			t.Fatalf("sin latido = stale conservando el observado: %+v", r)
		}
	}
	h2.cycle()
	rows, _, _ = h2.eng.List(ctx, tenantA, nil)
	for _, r := range rows {
		if r.RouterID == rtA1 && (r.Status != api.StatusOnline || !r.Since.Equal(since)) {
			t.Fatalf("since debe conservarse entre reinicios: %v vs %v (%s)", r.Since, since, r.Status)
		}
	}
	// Observación vieja (> 3 intervalos) = stale aunque el poller lata.
	h2.advance(5 * time.Minute)
	h2.eng.mu.Lock()
	n := h2.clock()
	h2.eng.lastCycle = &n
	h2.eng.mu.Unlock()
	rows, _, _ = h2.eng.List(ctx, tenantA, nil)
	if rows[0].Status != api.StatusStale || rows[1].Status != api.StatusStale {
		t.Fatalf("observación vieja = stale: %s %s", rows[0].Status, rows[1].Status)
	}
	// Nunca observado (router nuevo) = unknown.
	addr := ipA2
	h2.dev.mu.Lock()
	h2.dev.routers = append(h2.dev.routers, devapi.RouterInfo{ID: uuid.New(), TenantID: tenantA, SiteID: siteA, Name: "nuevo",
		OnboardingState: devapi.OnboardingTunnelUp, TunnelAddress: &addr})
	h2.dev.mu.Unlock()
	h2.eng.refreshTargets(ctx)
	rows, _, _ = h2.eng.List(ctx, tenantA, nil)
	found := false
	for _, r := range rows {
		if r.Name == "nuevo" {
			found = r.Status == api.StatusUnknown && r.ObservedState == nil && r.Since == nil
		}
	}
	if !found {
		t.Fatalf("router nuevo = unknown: %+v", rows)
	}
}

func TestPollNow(t *testing.T) {
	h := newHarness(t, &memStore{})
	ctx := context.Background()
	if found, _, _, _ := h.eng.PollNow(ctx, tenantB, rtA1); found {
		t.Fatal("router de otro tenant encontrado")
	}
	found, queued, reason, err := h.eng.PollNow(ctx, tenantA, rtA2)
	if !found || queued || reason == nil || *reason != api.ReasonNoCredentials || err != nil {
		t.Fatalf("sin credenciales: %v %v %v %v", found, queued, reason, err)
	}
	found, queued, reason, err = h.eng.PollNow(ctx, tenantA, rtA1)
	if !found || !queued || reason != nil || err != nil {
		t.Fatalf("encolado: %v %v %v %v", found, queued, reason, err)
	}
	if _, _, _, err = h.eng.PollNow(ctx, tenantA, rtA1); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("segunda petición en < 10 s: %v", err)
	}
	h.advance(11 * time.Second)
	if _, queued, _, err = h.eng.PollNow(ctx, tenantA, rtA1); err != nil || !queued {
		t.Fatalf("tras 10 s: %v %v", queued, err)
	}
	if got := FilterStatus([]api.RouterStatus{{Status: "online"}, {Status: "offline"}, {Status: "critical"}}, "offline, critical"); len(got) != 2 {
		t.Fatalf("FilterStatus: %+v", got)
	}
}
