package app

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/clickhouse"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/pgstore"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/config"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// --- dobles del análisis de capacidad ---

type fakeCapRepo struct {
	mu        sync.Mutex
	configs   map[uuid.UUID][]domain.UplinkConfig
	settings  map[uuid.UUID]domain.SettingsRow
	conds     map[condKey]domain.ConditionRow
	forecasts map[uuid.UUID]domain.ForecastRow
	events    []outbox.Event
	failSave  bool
	history   map[uuid.UUID]map[uuid.UUID][]domain.CapacityPeriod
}

func newFakeCapRepo() *fakeCapRepo {
	return &fakeCapRepo{configs: map[uuid.UUID][]domain.UplinkConfig{}, settings: map[uuid.UUID]domain.SettingsRow{},
		conds: map[condKey]domain.ConditionRow{}, forecasts: map[uuid.UUID]domain.ForecastRow{}}
}

func (f *fakeCapRepo) ListUplinkConfigs(_ context.Context, t uuid.UUID) ([]domain.UplinkConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.UplinkConfig{}, f.configs[t]...), nil
}
func (f *fakeCapRepo) GetUplinkConfig(_ context.Context, t, iface uuid.UUID) (*domain.UplinkConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.configs[t] {
		if c.InterfaceID == iface {
			return &c, nil
		}
	}
	return nil, nil
}
func (f *fakeCapRepo) PutUplinkConfig(_ context.Context, t, iface uuid.UUID, in domain.UplinkConfigInput, _ string, expect int) (*domain.UplinkConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := domain.UplinkConfig{InterfaceID: iface, RouterID: in.RouterID, Label: in.Label, Provider: in.Provider, Kind: in.Kind,
		ContractedBps: in.ContractedBps, InternationalEgress: *in.InternationalEgress, Version: 1}
	for i, x := range f.configs[t] {
		if x.InterfaceID == iface {
			if expect > 0 && expect != x.Version {
				return nil, pgstore.ErrVersion
			}
			c.Version = x.Version + 1
			f.configs[t][i] = c
			return &c, nil
		}
	}
	f.configs[t] = append(f.configs[t], c)
	return &c, nil
}
func (f *fakeCapRepo) DeleteUplinkConfig(_ context.Context, t, iface uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, x := range f.configs[t] {
		if x.InterfaceID == iface {
			f.configs[t] = append(f.configs[t][:i], f.configs[t][i+1:]...)
			return true, nil
		}
	}
	return false, nil
}
func (f *fakeCapRepo) GetCapacitySettings(_ context.Context, t uuid.UUID) (*domain.SettingsRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.settings[t]; ok {
		return &r, nil
	}
	return nil, nil
}
func (f *fakeCapRepo) PutCapacitySettings(_ context.Context, t uuid.UUID, v domain.CapacitySettings, _ string, _ int) (*domain.SettingsRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := domain.SettingsRow{Settings: v, Version: f.settings[t].Version + 1}
	f.settings[t] = r
	return &r, nil
}
func (f *fakeCapRepo) DeleteCapacitySettings(_ context.Context, t uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.settings, t)
	return nil
}
func (f *fakeCapRepo) LoadCapacity(context.Context) (*pgstore.CapacityState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := &pgstore.CapacityState{Configs: map[uuid.UUID][]domain.UplinkConfig{}, Settings: map[uuid.UUID]domain.CapacitySettings{}}
	for t, c := range f.configs {
		st.Configs[t] = append([]domain.UplinkConfig{}, c...)
	}
	for t, s := range f.settings {
		st.Settings[t] = s.Settings
	}
	for _, c := range f.conds {
		st.Conditions = append(st.Conditions, c)
	}
	st.History = f.history
	return st, nil
}

func (f *fakeCapRepo) ListCapacityHistory(_ context.Context, t uuid.UUID) (map[uuid.UUID][]domain.CapacityPeriod, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.history[t], nil
}
func (f *fakeCapRepo) SaveCondition(_ context.Context, c domain.ConditionRow, ev *outbox.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSave {
		return errors.New("postgres caído")
	}
	f.conds[condKey{c.TenantID, c.InterfaceID, c.Condition}] = c
	if ev != nil {
		f.events = append(f.events, *ev)
	}
	return nil
}
func (f *fakeCapRepo) ListConditions(_ context.Context, t uuid.UUID) ([]domain.ConditionRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.ConditionRow
	for _, c := range f.conds {
		if c.TenantID == t && c.Level != domain.LevelOK {
			out = append(out, c)
		}
	}
	return out, nil
}
func (f *fakeCapRepo) SaveForecast(_ context.Context, r domain.ForecastRow, ev *outbox.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forecasts[r.SubjectID] = r
	if ev != nil {
		f.events = append(f.events, *ev)
	}
	return nil
}
func (f *fakeCapRepo) ListForecasts(_ context.Context, t uuid.UUID) ([]domain.ForecastRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.ForecastRow
	for _, r := range f.forecasts {
		if r.TenantID == t {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeCapRepo) eventsOf(typ string) []outbox.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []outbox.Event
	for _, e := range f.events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// fakeSeries guarda lo escrito y genera intervalos de 5 min con un pico
// diario que crece un 4 % mensual.
type fakeSeries struct {
	mu      sync.Mutex
	daily   map[uuid.UUID]map[time.Time]clickhouse.DailyRow
	probes  []domain.ProbeSample
	peak    func(iface uuid.UUID, at time.Time) float64
	traffic []domain.ASNVolume
	queried int
}

func newFakeSeries(peak func(uuid.UUID, time.Time) float64) *fakeSeries {
	return &fakeSeries{daily: map[uuid.UUID]map[time.Time]clickhouse.DailyRow{}, peak: peak}
}

func (f *fakeSeries) InsertProbes(_ context.Context, s []domain.ProbeSample) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probes = append(f.probes, s...)
	return nil
}
func (f *fakeSeries) InsertDaily(_ context.Context, _ uuid.UUID, rows []clickhouse.DailyRow, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range rows {
		if f.daily[r.SubjectID] == nil {
			f.daily[r.SubjectID] = map[time.Time]clickhouse.DailyRow{}
		}
		f.daily[r.SubjectID][r.Stats.Day] = r
	}
	return nil
}
func (f *fakeSeries) Buckets(_ context.Context, _ uuid.UUID, ifaces []uuid.UUID, from, to time.Time) (map[uuid.UUID][]domain.Bucket5m, map[uuid.UUID]clickhouse.IfaceMeta, error) {
	f.mu.Lock()
	f.queried++
	f.mu.Unlock()
	out := map[uuid.UUID][]domain.Bucket5m{}
	for _, id := range ifaces {
		for at := from; at.Before(to); at = at.Add(5 * time.Minute) {
			v := f.peak(id, at)
			if v <= 0 {
				continue
			}
			tx := v / 10
			out[id] = append(out[id], domain.Bucket5m{At: at, RxBps: &v, TxBps: &tx})
		}
	}
	return out, map[uuid.UUID]clickhouse.IfaceMeta{}, nil
}
func (f *fakeSeries) LastDaily(context.Context, uuid.UUID) (map[uuid.UUID]time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[uuid.UUID]time.Time{}
	for id, m := range f.daily {
		for d := range m {
			if d.After(out[id]) {
				out[id] = d
			}
		}
	}
	return out, nil
}
func (f *fakeSeries) DailySeries(_ context.Context, _ uuid.UUID, subjects []uuid.UUID, from, to time.Time) (map[uuid.UUID][]domain.DailyStats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[uuid.UUID][]domain.DailyStats{}
	for _, id := range subjects {
		for d, r := range f.daily[id] {
			if !d.Before(from) && !d.After(to) {
				out[id] = append(out[id], r.Stats)
			}
		}
	}
	for id := range out {
		s := out[id]
		for i := 1; i < len(s); i++ {
			for j := i; j > 0 && s[j].Day.Before(s[j-1].Day); j-- {
				s[j], s[j-1] = s[j-1], s[j]
			}
		}
	}
	return out, nil
}
func (f *fakeSeries) ProbeSummaries(context.Context, uuid.UUID, time.Time) ([]clickhouse.ProbeAgg, error) {
	return nil, nil
}
func (f *fakeSeries) ProbeSeries(context.Context, uuid.UUID, []uuid.UUID, time.Time, time.Time) (map[uuid.UUID][]clickhouse.ProbePoint, error) {
	return map[uuid.UUID][]clickhouse.ProbePoint{}, nil
}
func (f *fakeSeries) TrafficByCountry(_ context.Context, _ uuid.UUID, sites []uuid.UUID, _ []uint32, _, _ time.Time) ([]domain.ASNVolume, error) {
	var out []domain.ASNVolume
	for _, v := range f.traffic {
		if len(sites) == 0 || contains(sites, v.SiteID) {
			out = append(out, v)
		}
	}
	return out, nil
}

func contains(ids []uuid.UUID, id uuid.UUID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

type fakeProbeReader struct {
	probes map[uuid.UUID][]devapi.NetwatchProbe
	err    map[uuid.UUID]error
	calls  int
}

func (f *fakeProbeReader) ReadNetwatch(_ context.Context, _ uuid.UUID, router uuid.UUID) ([]devapi.NetwatchProbe, error) {
	f.calls++
	if err := f.err[router]; err != nil {
		return nil, err
	}
	return f.probes[router], nil
}

// capFixture es un ISP con un router y su enlace de subida sfp1 (1 Gb/s).
type capFixture struct {
	tenant, site, router, iface uuid.UUID
	clock                       *time.Time
	eng                         *Engine
	r                           *router
	repo                        *fakeCapRepo
}

func newCapFixture(t *testing.T) *capFixture {
	t.Helper()
	now := time.Date(2026, 10, 16, 20, 0, 0, 0, time.UTC)
	f := &capFixture{tenant: uuid.New(), site: uuid.New(), router: uuid.New(), clock: &now, repo: newFakeCapRepo()}
	f.iface = api.InterfaceID(f.router, "sfp1")
	f.eng = New(Options{Config: config.Config{Enabled: true, Interval: time.Minute}, Now: func() time.Time { return *f.clock }})
	speed := int64(1_000_000_000)
	up := api.OperUp
	since := now.Add(-time.Hour)
	f.r = &router{Target: Target{RouterID: f.router, TenantID: f.tenant, SiteID: f.site, Name: "rt-core", SiteName: "Nodo Centro"},
		st: persisted{Machine: domain.Machine{State: api.StatusOnline, Reason: api.ReasonOK, Since: &since}, LastObservedAt: &now,
			UplinkIfIndex: 1, DiscoveredAt: &now, Interfaces: []ifState{{InterfaceID: f.iface, IfIndex: 1, Name: "sfp1", SpeedBps: &speed,
				OperStatus: &up, Monitored: true}}}}
	f.eng.routers[f.router] = f.r
	f.setTraffic(300e6)
	return f
}

func (f *capFixture) setTraffic(rx float64) {
	f.r.mu.Lock()
	defer f.r.mu.Unlock()
	tx := rx / 10
	x := &f.r.st.Interfaces[0]
	x.Sample = &ifSample{InterfaceSample: api.InterfaceSample{InterfaceID: f.iface, Name: "sfp1", RxBps: &rx, TxBps: &tx}, SampledAt: *f.clock}
	f.r.st.LastObservedAt = f.clock
}

func (f *capFixture) capacity(series CapacitySeries, probes devapi.RouterProbeReader) *Capacity {
	c := NewCapacity(CapacityOptions{Repo: f.repo, Series: series, Engine: f.eng, Now: func() time.Time { return *f.clock },
		Probes: func() (devapi.RouterProbeReader, bool) { return probes, probes != nil },
		Tenants: func(context.Context) (map[uuid.UUID]TenantInfo, bool) {
			return map[uuid.UUID]TenantInfo{f.tenant: {ID: f.tenant, Timezone: "America/Guatemala", Country: "GT", Active: true}}, true
		}})
	return c
}

func (f *capFixture) advance(d time.Duration) {
	n := f.clock.Add(d)
	*f.clock = n
}

func TestNetwatchSamplesMapping(t *testing.T) {
	f := newCapFixture(t)
	snap := f.eng.Snapshot(*f.clock)
	loss, rtt, stdev := 2.0, 12.5, 1.5
	probes := []devapi.NetwatchProbe{
		{Host: "198.51.100.1", Type: "icmp", Status: "up", Comment: "horus-uplink;if=sfp1;ref=gw", LossPercent: &loss, RTTAvgMs: &rtt, RTTStdevMs: &stdev},
		{Host: "1.1.1.1", Type: "icmp", Status: "down", Comment: "horus-uplink;ref=intl-1.1.1.1"},
		{Host: "192.0.2.9", Type: "icmp", Status: "up", Comment: "horus-uplink;if=ether9;ref=gw"},
		{Host: "192.0.2.1", Type: "icmp", Status: "up", Comment: "monitoreo propio"},
		{Host: "192.0.2.2", Type: "icmp", Status: "up", Comment: "horus-uplink;if=sfp1", Disabled: true},
		{Host: "192.0.2.3", Type: "tcp-conn", Status: "up", Comment: "horus-uplink;if=sfp1"},
	}
	got := NetwatchSamples(snap[0], probes, *f.clock)
	if len(got) != 3 {
		t.Fatalf("lecturas = %d: %+v", len(got), got)
	}
	gw, intl, orphan := got[0], got[1], got[2]
	if gw.InterfaceID != f.iface || gw.Ref != domain.ProbeRefGateway || *gw.LossPct != 2 || *gw.JitterMs != 1.5 || !*gw.Up ||
		gw.Label != "Puerta de enlace por sfp1" || gw.Source != domain.ProbeSourceRouter || gw.TenantID != f.tenant {
		t.Fatalf("puerta de enlace: %+v", gw)
	}
	if intl.InterfaceID != uuid.Nil || intl.Ref != domain.ProbeRefInternational || *intl.LossPct != 100 || *intl.Up {
		t.Fatalf("internacional caída: %+v", intl)
	}
	if orphan.InterfaceID != uuid.Nil || !strings.Contains(orphan.Label, "interfaz no encontrada") {
		t.Fatalf("interfaz inexistente: %+v", orphan)
	}
	if gw.ProbeID == intl.ProbeID || gw.ProbeID != domain.ProbeID(f.router, "198.51.100.1", "gw", "sfp1") {
		t.Fatal("probe_id inestable")
	}
}

func TestCollectProbesBacksOffOnRouterAPIErrors(t *testing.T) {
	f := newCapFixture(t)
	series := newFakeSeries(func(uuid.UUID, time.Time) float64 { return 0 })
	rd := &fakeProbeReader{err: map[uuid.UUID]error{f.router: errors.New("devices: router API unavailable: TLS fingerprint not pinned yet")}}
	c := f.capacity(series, rd)
	c.collectProbes(context.Background())
	c.collectProbes(context.Background())
	if rd.calls != 1 {
		t.Fatalf("reintento sin espera: %d lecturas", rd.calls)
	}
	sn, err := c.NetwatchSnippet(context.Background(), f.tenant, nil, f.router)
	if err != nil || sn.ProbesError == nil || *sn.ProbesError != "tls_fingerprint_not_pinned" || len(sn.Placeholders) != 1 {
		t.Fatalf("script: %v %+v", err, sn)
	}
	if _, err := c.NetwatchSnippet(context.Background(), uuid.New(), nil, f.router); !errors.Is(err, ErrUplinkNotFound) {
		t.Fatalf("otro ISP: %v", err)
	}
	if _, err := c.NetwatchSnippet(context.Background(), f.tenant, []uuid.UUID{uuid.New()}, f.router); !errors.Is(err, ErrUplinkNotFound) {
		t.Fatalf("fuera de alcance: %v", err)
	}
	// Pasados 10 min se reintenta; con lectura correcta se escribe en ClickHouse.
	loss := 0.0
	rd.err = nil
	rd.probes = map[uuid.UUID][]devapi.NetwatchProbe{f.router: {{Host: "198.51.100.1", Type: "icmp", Status: "up",
		Comment: "horus-uplink;if=sfp1;ref=gw", LossPercent: &loss}}}
	f.advance(11 * time.Minute)
	f.setTraffic(300e6)
	c.collectProbes(context.Background())
	if rd.calls != 2 || len(series.probes) != 1 {
		t.Fatalf("lecturas %d, filas %d", rd.calls, len(series.probes))
	}
}

// La saturación sostenida emite un único evento al cruzar (tras 15 min) y
// uno de recuperación; repetir el nivel no duplica. Si no se puede guardar,
// el nivel no se adopta y se reintenta.
func TestCapacityMonitorDedupeAndRecovery(t *testing.T) {
	f := newCapFixture(t)
	c := f.capacity(nil, nil)
	ctx := context.Background()
	for i := 0; i <= 16; i++ {
		f.setTraffic(950e6)
		c.monitor(ctx)
		f.advance(time.Minute)
	}
	ev := f.repo.eventsOf(TypeUplinkConditionChanged)
	if len(ev) != 1 {
		t.Fatalf("eventos tras 16 min: %d", len(ev))
	}
	d := ev[0].Data.(map[string]any)
	if d["condition"] != "saturation" || d["level"] != "critical" || d["previous_level"] != "ok" || d["threshold"] != 0.85 ||
		d["interface_name"] != "sfp1" || d["router_name"] != "rt-core" || d["site_id"] != f.site.String() || ev[0].AggregateID != f.iface {
		t.Fatalf("evento: %+v", d)
	}
	for i := 0; i < 10; i++ {
		f.setTraffic(960e6)
		c.monitor(ctx)
		f.advance(time.Minute)
	}
	if n := len(f.repo.eventsOf(TypeUplinkConditionChanged)); n != 1 {
		t.Fatalf("duplicados: %d", n)
	}
	// Bajada a 30 %: recupera tras max(5 min, 15/2 min).
	f.repo.failSave = true
	for i := 0; i <= 8; i++ {
		f.setTraffic(300e6)
		c.monitor(ctx)
		f.advance(time.Minute)
	}
	if n := len(f.repo.eventsOf(TypeUplinkConditionChanged)); n != 1 {
		t.Fatalf("evento guardado con PostgreSQL caído: %d", n)
	}
	f.repo.failSave = false
	f.setTraffic(300e6)
	c.monitor(ctx)
	ev = f.repo.eventsOf(TypeUplinkConditionChanged)
	if len(ev) != 2 || ev[1].Data.(map[string]any)["level"] != "ok" || ev[1].Data.(map[string]any)["previous_level"] != "critical" {
		t.Fatalf("recuperación: %+v", ev)
	}
	// Tras reiniciar, el estado guardado evita repetir la alarma.
	c2 := f.capacity(nil, nil)
	if err := c2.Load(ctx); err != nil {
		t.Fatal(err)
	}
	f.setTraffic(300e6)
	c2.monitor(ctx)
	if n := len(f.repo.eventsOf(TypeUplinkConditionChanged)); n != 2 {
		t.Fatalf("evento tras reinicio: %d", n)
	}
}

func TestCapacityLossConditionFromGatewayProbe(t *testing.T) {
	f := newCapFixture(t)
	loss := 8.0
	rd := &fakeProbeReader{probes: map[uuid.UUID][]devapi.NetwatchProbe{f.router: {{Host: "198.51.100.1", Type: "icmp", Status: "up",
		Comment: "horus-uplink;if=sfp1;ref=gw", LossPercent: &loss}}}}
	c := f.capacity(nil, rd)
	for i := 0; i <= 16; i++ {
		f.setTraffic(200e6)
		c.collectProbes(context.Background())
		c.monitor(context.Background())
		f.advance(time.Minute)
	}
	ev := f.repo.eventsOf(TypeUplinkConditionChanged)
	if len(ev) != 1 || ev[0].Data.(map[string]any)["condition"] != "loss" || ev[0].Data.(map[string]any)["unit"] != "percent" {
		t.Fatalf("pérdida: %+v", ev)
	}
	ov, err := c.Overview(context.Background(), f.tenant, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(ov.Data) != 1 || ov.Data[0].Health.Status != api.StatusCritical || !strings.Contains(ov.Data[0].Health.Summary, "8,0 % de pérdida") {
		t.Fatalf("salud: %+v", ov.Data[0].Health)
	}
}

// El trabajo diario rellena 89 días de picos (enlace y salida
// internacional), pronostica y emite forecast_changed solo al cambiar de
// nivel.
func TestCapacityDailyJobAndForecastEvents(t *testing.T) {
	f := newCapFixture(t)
	end := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	daily := math.Log1p(0.04) / 30.4375
	series := newFakeSeries(func(_ uuid.UUID, at time.Time) float64 {
		days := at.Sub(end).Hours() / 24
		return 600e6 * math.Exp(daily*days)
	})
	contracted := int64(811_000_000)
	egress := true
	if _, err := f.repo.PutUplinkConfig(context.Background(), f.tenant, f.iface, domain.UplinkConfigInput{RouterID: f.router,
		Label: "Proveedor A", Kind: domain.UplinkKindInternational, ContractedBps: &contracted, InternationalEgress: &egress}, "", 0); err != nil {
		t.Fatal(err)
	}
	c := f.capacity(series, nil)
	if err := c.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	*f.clock = time.Date(2026, 10, 16, 7, 0, 0, 0, time.UTC) // 01:00 en Guatemala
	c.RunDaily(context.Background(), false)
	egressID := domain.EgressSubjectID(f.tenant)
	if n := len(series.daily[f.iface]); n != backfillDays {
		t.Fatalf("días del enlace = %d", n)
	}
	if n := len(series.daily[egressID]); n != backfillDays {
		t.Fatalf("días de la salida = %d", n)
	}
	fc, ok := f.repo.forecasts[f.iface]
	if !ok || fc.Level != domain.ForecastPlanning || fc.Forecast.MonthlyGrowth == nil || math.Abs(*fc.Forecast.MonthlyGrowth-0.04) > 0.002 {
		t.Fatalf("pronóstico: %+v", fc)
	}
	if !strings.HasPrefix(fc.Recommendation, "Planificar upgrade del enlace «Proveedor A»") {
		t.Fatalf("recomendación: %q", fc.Recommendation)
	}
	if eg := f.repo.forecasts[egressID]; eg.Level != domain.ForecastPlanning || !strings.Contains(eg.Recommendation, "de salida internacional") {
		t.Fatalf("salida internacional: %+v", eg)
	}
	ev := f.repo.eventsOf(TypeForecastChanged)
	if len(ev) != 2 {
		t.Fatalf("eventos de pronóstico: %d", len(ev))
	}
	d := ev[0].Data.(map[string]any)
	if d["level"] != "planning" || d["previous_level"] != "" || d["planning_date"] == nil {
		t.Fatalf("evento: %+v", d)
	}
	// Mismo día: no se repite; forzado: mismo nivel, sin eventos nuevos ni días repetidos.
	q := series.queried
	c.RunDaily(context.Background(), false)
	c.RunDaily(context.Background(), true)
	if n := len(f.repo.eventsOf(TypeForecastChanged)); n != 2 {
		t.Fatalf("eventos repetidos: %d", n)
	}
	if series.queried != q {
		t.Fatalf("recalculó días ya escritos: %d consultas", series.queried-q)
	}
	// Al día siguiente solo se calcula ese día.
	*f.clock = f.clock.Add(24 * time.Hour)
	c.RunDaily(context.Background(), false)
	if series.queried != q+1 || len(series.daily[f.iface]) != backfillDays+1 {
		t.Fatalf("día nuevo: %d consultas, %d días", series.queried-q, len(series.daily[f.iface]))
	}
	// Detalle: serie diaria y pronóstico guardado.
	det, err := c.UplinkDetail(context.Background(), f.tenant, nil, f.iface, 30)
	if err != nil || len(det.Daily) != 30 || det.Forecast == nil || det.Recommendation == nil || det.Uplink.CapacitySource != "contracted" {
		t.Fatalf("detalle: %v %+v", err, det)
	}
	if _, err := c.UplinkDetail(context.Background(), f.tenant, []uuid.UUID{uuid.New()}, f.iface, 30); !errors.Is(err, ErrUplinkNotFound) {
		t.Fatalf("detalle fuera de alcance: %v", err)
	}
	if eg, err := c.EgressDetail(context.Background(), f.tenant, nil, 90); err != nil || eg.Egress == nil || len(eg.Daily) == 0 {
		t.Fatalf("detalle de la salida: %v %+v", err, eg)
	}
	if _, err := c.EgressDetail(context.Background(), f.tenant, []uuid.UUID{f.site}, 90); !errors.Is(err, ErrUplinkNotFound) {
		t.Fatalf("salida con alcance por nodo: %v", err)
	}
}

func TestCapacityConfigScopeAndOverview(t *testing.T) {
	f := newCapFixture(t)
	c := f.capacity(newFakeSeries(func(uuid.UUID, time.Time) float64 { return 400e6 }), nil)
	ctx := context.Background()
	egress := true
	in := domain.UplinkConfigInput{RouterID: f.router, Label: "Proveedor A", Kind: domain.UplinkKindInternational, InternationalEgress: &egress}
	if _, _, err := c.PutUplink(ctx, f.tenant, []uuid.UUID{uuid.New()}, f.iface, in, "u", 0); !errors.Is(err, ErrUplinkNotFound) {
		t.Fatalf("fuera de alcance: %v", err)
	}
	if _, _, err := c.PutUplink(ctx, uuid.New(), nil, f.iface, in, "u", 0); !errors.Is(err, ErrUplinkNotFound) {
		t.Fatalf("otro ISP: %v", err)
	}
	other := in
	other.RouterID = uuid.New()
	if _, _, err := c.PutUplink(ctx, f.tenant, nil, f.iface, other, "u", 0); !errors.Is(err, ErrUplinkNotFound) {
		t.Fatalf("router distinto: %v", err)
	}
	after, before, err := c.PutUplink(ctx, f.tenant, []uuid.UUID{f.site}, f.iface, in, "u", 0)
	if err != nil || before != nil || after.Version != 1 {
		t.Fatalf("alta: %v %+v", err, after)
	}
	if _, _, err := c.PutUplink(ctx, f.tenant, nil, f.iface, in, "u", 7); !errors.Is(err, ErrCapacityVersion) {
		t.Fatalf("If-Match: %v", err)
	}
	ov, err := c.Overview(ctx, f.tenant, 7)
	if err != nil {
		t.Fatal(err)
	}
	u := ov.Data[0].Uplinks[0]
	if !u.Configured || !u.AutoDetected || u.Label != "Proveedor A" || u.CapacitySource != "port_speed" || *u.CapacityBps != 1e9 ||
		math.Abs(*u.Utilization-0.3) > 1e-9 || u.Window == nil || ov.Egress == nil || ov.Egress.CapacityBps != 1e9 {
		t.Fatalf("vista: %+v egress=%+v", u, ov.Egress)
	}
	if ov.Data[0].Health.Status != api.StatusOnline || ov.Data[0].SiteName != "Nodo Centro" {
		t.Fatalf("salud: %+v", ov.Data[0].Health)
	}
	scoped := FilterSites(ov, []uuid.UUID{uuid.New()}, nil)
	if len(scoped.Data) != 0 || scoped.Egress != nil {
		t.Fatalf("alcance por nodo: %+v", scoped)
	}
	hs, err := c.SiteHealth(ctx, f.tenant, nil)
	if err != nil || len(hs) != 1 || hs[0].Uplinks != 1 || hs[0].Status != api.StatusOnline || hs[0].WorstUtilP95 == nil {
		t.Fatalf("widget: %v %+v", err, hs)
	}
	if _, err := c.DeleteUplink(ctx, f.tenant, nil, f.iface); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeleteUplink(ctx, f.tenant, nil, f.iface); !errors.Is(err, ErrUplinkNotFound) {
		t.Fatalf("baja repetida: %v", err)
	}
	// Ajustes: el país efectivo sale del ISP salvo que se fije.
	v, err := c.GetSettings(ctx, f.tenant)
	if err != nil || v.Customized || *v.EffectiveCountry != "GT" {
		t.Fatalf("ajustes: %v %+v", err, v)
	}
	st := domain.DefaultCapacitySettings()
	sv := "SV"
	st.Country = &sv
	if v, err = c.PutSettings(ctx, f.tenant, st, "u", 0); err != nil || *v.EffectiveCountry != "SV" || *v.TenantCountry != "GT" {
		t.Fatalf("ajustes guardados: %v %+v", err, v)
	}
}

func TestTrafficSplitView(t *testing.T) {
	f := newCapFixture(t)
	series := newFakeSeries(func(uuid.UUID, time.Time) float64 { return 0 })
	other := uuid.New()
	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	series.traffic = []domain.ASNVolume{
		{SiteID: f.site, Day: day, Country: "US", BytesDown: 700},
		{SiteID: f.site, Day: day, Country: "GT", BytesDown: 200},
		{SiteID: f.site, Day: day, Country: "US", Local: true, BytesDown: 100},
		{SiteID: other, Day: day.AddDate(0, 0, 1), NoASN: true, BytesDown: 50},
		{SiteID: f.site, Day: day.AddDate(0, 0, -60), Country: "US", BytesDown: 999},
	}
	c := f.capacity(series, nil)
	v, err := c.TrafficSplit(context.Background(), f.tenant, nil, 30)
	if err != nil {
		t.Fatal(err)
	}
	if *v.Country != "GT" || v.CountrySource != "tenant" || v.Total.InternationalBytes != 700 || v.Total.LocalBytes != 300 ||
		v.Total.UnknownBytes != 50 || math.Abs(*v.Total.InternationalShare-0.7) > 1e-9 || len(v.Sites) != 2 || len(v.Daily) != 2 {
		t.Fatalf("reparto: %+v", v)
	}
	if v.Sites[0].SiteID != f.site || v.Sites[0].SiteName != "Nodo Centro" || v.Sites[0].LocalByList != 100 {
		t.Fatalf("por nodo: %+v", v.Sites[0])
	}
	scoped, err := c.TrafficSplit(context.Background(), f.tenant, []uuid.UUID{other}, 30)
	if err != nil || scoped.Total.LocalBytes != 0 || scoped.Total.UnknownBytes != 50 || scoped.Total.InternationalShare != nil {
		t.Fatalf("filtrado: %v %+v", err, scoped)
	}
}
