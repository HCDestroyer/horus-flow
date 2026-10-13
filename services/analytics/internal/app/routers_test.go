package app

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/services/analytics/internal/adapters/chread"
	snmpapi "github.com/hcdestroyer/horus-flow/services/snmp/api"
)

// anyRows son filas genéricas: cada celda se copia a su destino por tipo
// (time.Time, uuid.UUID, string, float64 y *float64 → **float64).
type anyRows struct {
	chread.Rows
	rows [][]any
	i    int
}

func (f *anyRows) Next() bool { f.i++; return f.i <= len(f.rows) }
func (f *anyRows) Scan(dest ...any) error {
	r := f.rows[f.i-1]
	for i, d := range dest {
		switch p := d.(type) {
		case *time.Time:
			*p = r[i].(time.Time)
		case *uuid.UUID:
			*p = r[i].(uuid.UUID)
		case *string:
			*p = r[i].(string)
		case *float64:
			*p = r[i].(float64)
		case **float64:
			if r[i] == nil {
				*p = nil
			} else {
				v := r[i].(float64)
				*p = &v
			}
		default:
			return errors.New("anyRows: unsupported dest")
		}
	}
	return nil
}
func (f *anyRows) Close() error { return nil }
func (f *anyRows) Err() error   { return nil }

// routerQ responde por tabla y recuerda el último SQL.
type routerQ struct {
	byTable map[string][][]any
	sqls    []string
	err     error
}

func (q *routerQ) Query(_ context.Context, _ uuid.UUID, sql string, _ ...any) (chread.Rows, context.CancelFunc, error) {
	q.sqls = append(q.sqls, sql)
	if q.err != nil {
		return nil, nil, q.err
	}
	for tbl, rows := range q.byTable {
		if strings.Contains(sql, tbl) {
			return &anyRows{rows: rows}, func() {}, nil
		}
	}
	return &anyRows{}, func() {}, nil
}

// fakeReader es un RouterStatusReader de prueba.
type fakeReader struct {
	list   []snmpapi.RouterStatus
	health snmpapi.PollerHealth
	live   map[uuid.UUID]*snmpapi.RouterLiveMetrics
	err    error
}

func (f *fakeReader) List(context.Context, uuid.UUID, []uuid.UUID) ([]snmpapi.RouterStatus, snmpapi.PollerHealth, error) {
	return f.list, f.health, f.err
}

func (f *fakeReader) Live(_ context.Context, _, id uuid.UUID) (*snmpapi.RouterLiveMetrics, bool, error) {
	l, ok := f.live[id]
	return l, ok, f.err
}

func TestParseInterfaceIDs(t *testing.T) {
	ids, err := ParseInterfaceIDs("")
	if err != nil || ids != nil {
		t.Fatalf("empty: %v %v", ids, err)
	}
	a, b := uuid.New(), uuid.New()
	ids, err = ParseInterfaceIDs(a.String() + ", " + b.String())
	if err != nil || len(ids) != 2 || ids[0] != a || ids[1] != b {
		t.Fatalf("two: %v %v", ids, err)
	}
	if _, err := ParseInterfaceIDs("ether1"); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("invalid: %v", err)
	}
	var many []string
	for range MaxRouterInterfaces + 1 {
		many = append(many, uuid.NewString())
	}
	if _, err := ParseInterfaceIDs(strings.Join(many, ",")); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("too many: %v", err)
	}
}

func TestParseMetrics(t *testing.T) {
	m, err := parseMetrics(nil, DefaultDeviceMetrics, deviceMetrics)
	if err != nil || strings.Join(m, ",") != "cpu_percent,memory_percent,temperature_celsius" {
		t.Fatalf("default: %v %v", m, err)
	}
	m, err = parseMetrics([]string{"availability_ratio", " uptime_seconds"}, DefaultDeviceMetrics, deviceMetrics)
	if err != nil || len(m) != 2 || m[1] != "uptime_seconds" {
		t.Fatalf("list: %v %v", m, err)
	}
	if _, err := parseMetrics([]string{"rx_bps"}, DefaultDeviceMetrics, deviceMetrics); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("device with interface metric: %v", err)
	}
	if _, err := parseMetrics([]string{"cpu_percent"}, DefaultInterfaceMetrics, interfaceMetrics); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("interface with device metric: %v", err)
	}
}

// pickSNMP: crudo hasta 24 h con paso ≥ 1 min y ≤ ~500 puntos; agregados
// de 5 min hasta 2 d y de 1 h después, con media = sum/count (NULL sin
// muestras); con SNMPAggregates en false todo lee el crudo.
func TestPickSNMP(t *testing.T) {
	now := time.Now()
	g := pickSNMP(Range{From: now.Add(-time.Hour), To: now}, 0)
	if g.Step != time.Minute || g.table("device") != "snmp.device_metrics" || g.ts() != "ts" {
		t.Fatalf("1h: %+v", g)
	}
	g = pickSNMP(Range{From: now.Add(-24 * time.Hour), To: now}, 0)
	if g.Step != 4*time.Minute || g.Suffix != "" {
		t.Fatalf("24h: %+v", g)
	}
	g = pickSNMP(Range{From: now.Add(-24 * time.Hour), To: now}, 10*time.Minute)
	if g.Step != 10*time.Minute {
		t.Fatalf("24h step 600: %+v", g)
	}
	g = pickSNMP(Range{From: now.Add(-36 * time.Hour), To: now}, 0)
	if g.Suffix != "5m" || g.Step != 5*time.Minute || g.table("device") != "snmp.device_5m" || g.ts() != "bucket" {
		t.Fatalf("36h: %+v", g)
	}
	g = pickSNMP(Range{From: now.Add(-7 * 24 * time.Hour), To: now}, 0)
	if g.Suffix != "1h" || g.table("interface") != "snmp.interface_1h" {
		t.Fatalf("7d: %+v", g)
	}
	if e := g.expr(deviceMetrics["cpu_percent"]); !strings.Contains(e, "sum(cpu_percent_sum) / nullIf(sum(cpu_percent_count), 0)") {
		t.Fatalf("avg expr %s", e)
	}
	if e := g.expr(deviceMetrics["availability_ratio"]); !strings.Contains(e, "icmp_ok_count") || !strings.Contains(e, "icmp_total") {
		t.Fatalf("availability expr %s", e)
	}
	SNMPAggregates = false
	defer func() { SNMPAggregates = true }()
	g = pickSNMP(Range{From: now.Add(-7 * 24 * time.Hour), To: now}, 0)
	if g.Suffix != "" || g.Step < time.Minute {
		t.Fatalf("7d sin agregados: %+v", g)
	}
}

// Router: inventario → estado del rol snmp → dim.router; otro tenant es 404.
func TestRouterLookup(t *testing.T) {
	tenant, other := uuid.New(), uuid.New()
	rtInv, rtLive, rtDim, site := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	inv, err := flowinv.New(flowinv.Data{Exporters: []flowinv.Exporter{{TenantID: tenant, RouterID: rtInv, SiteID: site,
		TunnelIP: netip.MustParseAddr("10.255.0.1")}}})
	if err != nil {
		t.Fatal(err)
	}
	q := &routerQ{byTable: map[string][][]any{"dim.router": {{site}}}}
	s := &Service{Q: q, Inv: flowinv.NewStore(inv), Routers: func() snmpapi.RouterStatusReader {
		return &fakeReader{live: map[uuid.UUID]*snmpapi.RouterLiveMetrics{rtLive: {Status: snmpapi.RouterStatus{SiteID: site}}}}
	}}
	ctx := context.Background()
	if got, err := s.Router(ctx, tenant, rtInv); err != nil || got != site {
		t.Fatalf("inventory: %v %v", got, err)
	}
	if _, err := s.Router(ctx, other, rtInv); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other tenant: %v", err)
	}
	if got, err := s.Router(ctx, tenant, rtLive); err != nil || got != site {
		t.Fatalf("live: %v %v", got, err)
	}
	if got, err := s.Router(ctx, tenant, rtDim); err != nil || got != site {
		t.Fatalf("dim.router: %v %v", got, err)
	}
	q.byTable = nil
	if _, err := s.Router(ctx, tenant, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

// RouterMetrics: el bucket sin filas es un hueco (null, nunca 0), una métrica
// NULL dentro de un bucket con filas también; coverage y partial lo reflejan.
func TestRouterMetricsGaps(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	r := Range{From: now.Add(-5 * time.Minute), To: now}
	t0 := r.From
	q := &routerQ{byTable: map[string][][]any{"snmp.device_metrics": {
		{t0, 1.0, 40.0},
		{t0.Add(1 * time.Minute), 0.5, nil},
		{t0.Add(3 * time.Minute), 0.0, 90.0},
		{t0.Add(4 * time.Minute), 1.0, 41.0},
	}}}
	s := &Service{Q: q, Now: func() time.Time { return now }}
	res, err := s.RouterMetrics(context.Background(), uuid.New(), uuid.New(), []string{"availability_ratio", "cpu_percent"}, r, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Series) != 2 || res.Series[0].Metric != "availability_ratio" || res.Series[0].Unit != "ratio" ||
		res.Series[1].Unit != "percent" || res.Step != time.Minute {
		t.Fatalf("series %+v", res)
	}
	av, cpu := res.Series[0].Points, res.Series[1].Points
	if len(av) != 5 || len(cpu) != 5 {
		t.Fatalf("points %d %d", len(av), len(cpu))
	}
	if av[2].Value != nil || cpu[2].Value != nil {
		t.Fatalf("gap must be null: %v %v", av[2].Value, cpu[2].Value)
	}
	if av[3].Value == nil || *av[3].Value != 0 {
		t.Fatalf("availability 0 must stay 0: %v", av[3].Value)
	}
	if av[1].Value == nil || *av[1].Value != 0.5 || cpu[1].Value != nil {
		t.Fatalf("partial bucket: %v %v", av[1].Value, cpu[1].Value)
	}
	if res.Coverage != 0.8 || !res.Partial {
		t.Fatalf("coverage %v partial %v", res.Coverage, res.Partial)
	}
	sql := q.sqls[len(q.sqls)-1]
	if !strings.Contains(sql, "tenant_id = ?") || !strings.Contains(sql, "router_id = ?") || !strings.Contains(sql, "sum(icmp_ok) / count()") {
		t.Fatalf("sql %s", sql)
	}
	if _, err := s.RouterMetrics(context.Background(), uuid.New(), uuid.New(), []string{"nope"}, r, 0); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("bad metric: %v", err)
	}
	q.err = chread.ErrUnavailable
	if _, err := s.RouterMetrics(context.Background(), uuid.New(), uuid.New(), nil, r, 0); !errors.Is(err, chread.ErrUnavailable) {
		t.Fatalf("unavailable: %v", err)
	}
}

// RouterInterfaceMetrics: sin interface_id usa los uplinks del estado vivo;
// sin uplinks, las interfaces con más tráfico; group = nombre; una serie por
// métrica e interfaz; más de 8 es inválido.
func TestRouterInterfaceMetrics(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	r := Range{From: now.Add(-3 * time.Minute), To: now}
	tenant, router := uuid.New(), uuid.New()
	up, lan := uuid.New(), uuid.New()
	q := &routerQ{byTable: map[string][][]any{
		"snmp.interface_metrics WHERE tenant_id = ? AND router_id = ? AND interface_id IN ?": {
			{r.From, up, "ether1", 1000.0, 500.0},
			{r.From.Add(time.Minute), up, "ether1", nil, 700.0},
		},
		"ORDER BY traffic DESC": {{lan, "bridge", 9.0}, {up, "ether1", 5.0}},
	}}
	s := &Service{Q: q, Now: func() time.Time { return now }}
	ctx := context.Background()

	// Con uplink en el estado vivo.
	s.Routers = func() snmpapi.RouterStatusReader {
		return &fakeReader{live: map[uuid.UUID]*snmpapi.RouterLiveMetrics{router: {Interfaces: []snmpapi.InterfaceSample{
			{InterfaceID: lan, Name: "bridge"}, {InterfaceID: up, Name: "ether1", Uplink: true}}}}}
	}
	res, err := s.RouterInterfaceMetrics(ctx, tenant, router, nil, nil, r, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Series) != 2 || res.Series[0].Metric != "rx_bps" || res.Series[1].Metric != "tx_bps" ||
		res.Series[0].Group == nil || *res.Series[0].Group != "ether1" || res.Series[0].Unit != "bps" {
		t.Fatalf("series %+v", res.Series)
	}
	rx, tx := res.Series[0].Points, res.Series[1].Points
	if len(rx) != 3 || rx[0].Value == nil || *rx[0].Value != 1000 || rx[1].Value != nil || rx[2].Value != nil {
		t.Fatalf("rx %+v", rx)
	}
	if tx[1].Value == nil || *tx[1].Value != 700 {
		t.Fatalf("tx %+v", tx)
	}
	if res.Coverage < 0.66 || res.Coverage > 0.67 || !res.Partial {
		t.Fatalf("coverage %v", res.Coverage)
	}

	// Sin uplink ni estado vivo: las de más tráfico, con nombre de la consulta.
	s.Routers = nil
	res, err = s.RouterInterfaceMetrics(ctx, tenant, router, nil, []string{"rx_errors"}, r, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Series) != 2 || *res.Series[0].Group != "bridge" || *res.Series[1].Group != "ether1" || res.Series[0].Unit != "count" {
		t.Fatalf("busiest %+v", res.Series)
	}

	// Explícitas: una serie por métrica e interfaz; desconocida ⇒ group = id.
	other := uuid.New()
	res, err = s.RouterInterfaceMetrics(ctx, tenant, router, []uuid.UUID{up, other}, []string{"rx_bps", "utilization_rx"}, r, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Series) != 4 || *res.Series[2].Group != other.String() || res.Series[3].Unit != "ratio" {
		t.Fatalf("explicit %+v", res.Series)
	}
	var many []uuid.UUID
	for range MaxRouterInterfaces + 1 {
		many = append(many, uuid.New())
	}
	if _, err := s.RouterInterfaceMetrics(ctx, tenant, router, many, nil, r, 0); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("too many: %v", err)
	}
}
