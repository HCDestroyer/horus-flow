//go:build integration

package collector_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/chmigrate"
	"github.com/hcdestroyer/horus-flow/packages/go/chtest"
	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowbus/flowbustest"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/pcapread"
	"github.com/hcdestroyer/horus-flow/services/collector/internal/app"
	"github.com/hcdestroyer/horus-flow/services/ingester"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/catalog"
)

// shiftSink traslada los tiempos de los lotes al presente: los fixtures del
// simulador son de enero de 2026 y flows_raw tiene TTL de 7 días.
type shiftSink struct {
	js    jetstream.JetStream
	shift time.Duration
}

func (s shiftSink) PublishMsg(ctx context.Context, m *nats.Msg) error {
	if strings.HasPrefix(m.Subject, flowbus.SubjectBatchPrefix) {
		fb, err := flowpb.UnmarshalFlowBatch(m.Data)
		if err != nil {
			return err
		}
		for i := range fb.Records {
			fb.Records[i].TS = fb.Records[i].TS.Add(s.shift)
			fb.Records[i].FlowStart = fb.Records[i].FlowStart.Add(s.shift)
		}
		m.Data = fb.Marshal()
	}
	_, err := s.js.PublishMsg(ctx, m)
	return err
}

type simExpected struct {
	Start     time.Time `json:"start"`
	Exporters []struct {
		ExporterIP    string `json:"exporter_ip"`
		IPv6ClientLen int    `json:"ipv6_client_len"`
		Prefixes      struct {
			Customers      []string `json:"customers"`
			Infrastructure []string `json:"infrastructure"`
			Excluded       []string `json:"excluded"`
		} `json:"prefixes"`
		Totals   struct{ DataRecords int `json:"data_records"` } `json:"totals"`
		ByStatus map[string]int                                  `json:"by_status"`
		Clients  []struct {
			Key       string `json:"key"`
			BytesUp   uint64 `json:"bytes_up"`
			BytesDown uint64 `json:"bytes_down"`
		} `json:"clients"`
	} `json:"exporters"`
}

// runScenario pasa un fixture por collector + ingester reales y devuelve la
// conexión a ClickHouse y el tenant de la ejecución.
func runScenario(t *testing.T, capPath, expPath string) (*sql.DB, uuid.UUID, simExpected) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	var exp simExpected
	b, err := os.ReadFile(expPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &exp); err != nil {
		t.Fatal(err)
	}
	ch := chtest.Start(t, func(ctx context.Context, dsn, pw string) error {
		return ingester.MigrateClickHouse(ctx, []string{"HORUS_CLICKHOUSE_DSN=" + dsn, "HORUS_CLICKHOUSE_PASSWORD=" + pw}, log)
	})
	natsURL := flowbustest.URL(t)
	tenant := uuid.New()
	var d flowinv.Data
	for _, ex := range exp.Exporters {
		site, router, realmPriv, realmPub := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		e := flowinv.Exporter{TenantID: tenant, RouterID: router, SiteID: site, TunnelIP: netip.MustParseAddr(ex.ExporterIP)}
		d.Exporters = append(d.Exporters, e)
		d.Realms = append(d.Realms, flowinv.Realm{ID: realmPriv, TenantID: tenant, Kind: flowinv.RealmNodePrivate, SiteID: site},
			flowinv.Realm{ID: realmPub, TenantID: tenant, Kind: flowinv.RealmPublic})
		add := func(list []string, role string) {
			for _, s := range list {
				p := netip.MustParsePrefix(s)
				realm := realmPub
				if p.Addr().IsPrivate() || strings.HasPrefix(s, "100.") {
					realm = realmPriv
				}
				d.Prefixes = append(d.Prefixes, flowinv.ClientPrefix{ID: uuid.New(), TenantID: tenant, SiteID: site,
					RealmID: realm, Prefix: p, Role: role, IPv6ClientLen: ex.IPv6ClientLen})
			}
		}
		add(ex.Prefixes.Customers, flowinv.RoleCustomers)
		add(ex.Prefixes.Infrastructure, flowinv.RoleInfrastructure)
		add(ex.Prefixes.Excluded, flowinv.RoleExcluded)
	}
	invBytes, _ := json.Marshal(d)
	invFile := filepath.Join(t.TempDir(), "inventory.json")
	if err := os.WriteFile(invFile, invBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := flowinv.New(d)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)
	ing, err := ingester.Register(ctx, module.Deps{Logger: log, Common: config.Common{Env: "dev"}, Environ: []string{
		"HORUS_NATS_URL=" + natsURL, "HORUS_NATS_ENSURE_STREAMS=true", "HORUS_TLM_FLOWS_MAX_BYTES=268435456",
		"HORUS_CLICKHOUSE_DSN=" + ch.DSN, "HORUS_CLICKHOUSE_PASSWORD=" + ch.Password, "HORUS_INGESTER_CH_MIGRATE=false",
		"HORUS_FLOWS_INVENTORY_FILE=" + invFile,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ing.(module.Starter).Start(ctx); err != nil {
		t.Fatal(err)
	}
	ingCtx, stopIng := context.WithCancel(ctx)
	ingDone := make(chan error, 1)
	go func() { ingDone <- ing.Run(ingCtx) }()
	t.Cleanup(func() {
		stopIng()
		<-ingDone
		_ = ing.(module.Stopper).Stop(context.Background())
	})
	nc, js, err := flowbus.Connect(natsURL, "scenario-collector")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	eng := app.NewEngine(app.EngineOptions{Workers: 2, QueueDatagrams: 1 << 15, BatchMaxRecords: 500, BatchMaxAge: time.Second,
		BufferBytes: 256 << 20, PendingTTL: 30 * time.Second, CollectorID: "scenario",
		State: app.StateOptions{SilentAfter: 2 * time.Minute, LossThreshold: 0.01, LossWindow: 5 * time.Minute,
			ClockSkew: 30 * time.Second, Interval: time.Hour}},
		flowinv.NewStore(snap), shiftSink{js: js, shift: time.Since(exp.Start).Truncate(time.Hour)}, nil, app.NewMetrics(nil), log)
	colCtx, stopCol := context.WithCancel(ctx)
	colDone := make(chan error, 1)
	go func() { colDone <- eng.Run(colCtx) }()
	ds, err := pcapread.ReadFile(capPath)
	if err != nil {
		t.Fatal(err)
	}
	for i, dg := range ds {
		eng.Submit(app.Datagram{Src: dg.Src, Payload: dg.Payload, At: time.Now()})
		if i%500 == 499 {
			time.Sleep(5 * time.Millisecond)
		}
	}
	time.Sleep(300 * time.Millisecond)
	stopCol()
	<-colDone
	if st, err := js.Stream(ctx, flowbus.StreamTelemetry); err == nil {
		info, _ := st.Info(ctx)
		t.Logf("TLM_FLOWS: %d mensajes; datagramas %d", info.State.Msgs, len(ds))
	}
	db, err := chmigrate.Open(ch.DSN, ch.Password)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	want := 0
	for _, ex := range exp.Exporters {
		want += ex.Totals.DataRecords - ex.ByStatus["tunnel"] - ex.ByStatus["excluded"]
	}
	waitCount(ctx, t, db, tenant, want)
	return db, tenant, exp
}

// TestScenarioNormalEnrichment: el escenario `normal` del simulador (IPFIX,
// IPv4 + IPv6 con prefijo delegado) atribuye como el verificador y ≥ 95 % de
// los bytes remotos tienen ASN; los destinos de servicios conocidos quedan
// con su servicio y categoría (I1-07 criterio 1).
func TestScenarioNormalEnrichment(t *testing.T) {
	base := filepath.Join("..", "..", "tools", "flowsim", "fixtures", "sim", "normal")
	db, tenant, exp := runScenario(t, filepath.Join(base, "ipfix.hfsim.gz"), filepath.Join(base, "ipfix.expected.json"))
	ctx := context.Background()
	ex := exp.Exporters[0]

	// Atribución por estado y bytes por cliente (IPv4 /32 e IPv6 truncada).
	rows, err := db.QueryContext(ctx, `SELECT toString(attribution_status), count() FROM flows.flows_raw WHERE tenant_id = ? GROUP BY attribution_status`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for rows.Next() {
		var k string
		var n uint64
		_ = rows.Scan(&k, &n)
		got[k] = int(n)
	}
	_ = rows.Close()
	for k, v := range ex.ByStatus {
		if k == "tunnel" || k == "excluded" {
			continue
		}
		if got[k] != v {
			t.Errorf("attribution_status %s = %d, want %d", k, got[k], v)
		}
	}
	clientRows, err := db.QueryContext(ctx, `SELECT IPv6NumToString(client_ip), sumIf(bytes, direction IN ('upload','internal')), sumIf(bytes, direction = 'download')
		FROM flows.flows_raw WHERE tenant_id = ? AND attribution_status IN ('attributed','internal') GROUP BY client_ip`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	type ud struct{ up, down uint64 }
	clients := map[string]ud{}
	for clientRows.Next() {
		var ip string
		var up, down uint64
		if err := clientRows.Scan(&ip, &up, &down); err != nil {
			t.Fatal(err)
		}
		a := netip.MustParseAddr(ip).Unmap()
		key := a.String()
		if a.Is6() {
			key = netip.PrefixFrom(a, ex.IPv6ClientLen).String()
		}
		clients[key] = ud{up, down}
	}
	_ = clientRows.Close()
	if len(clients) != len(ex.Clients) {
		t.Errorf("clients = %d, want %d", len(clients), len(ex.Clients))
	}
	for _, c := range ex.Clients {
		if g := clients[c.Key]; g.up != c.BytesUp || g.down != c.BytesDown {
			t.Errorf("client %s: got %+v want up=%d down=%d", c.Key, g, c.BytesUp, c.BytesDown)
		}
	}

	// Cobertura ASN de los bytes con IP remota pública.
	rr, err := db.QueryContext(ctx, `SELECT IPv6NumToString(remote_ip), remote_asn, service_id, category_id, sum(bytes) FROM flows.flows_raw
		WHERE tenant_id = ? GROUP BY remote_ip, remote_asn, service_id, category_id`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	var total, withASN uint64
	svc := map[string]uuid.UUID{}
	for rr.Next() {
		var ip string
		var asn uint32
		var s, c uuid.UUID
		var b uint64
		if err := rr.Scan(&ip, &asn, &s, &c, &b); err != nil {
			t.Fatal(err)
		}
		a := netip.MustParseAddr(ip).Unmap()
		if !a.IsGlobalUnicast() || a.IsPrivate() || netip.MustParsePrefix("100.64.0.0/10").Contains(a) ||
			netip.MustParsePrefix("2001:db8::/32").Contains(a) || netip.MustParsePrefix("203.0.113.0/24").Contains(a) {
			continue
		}
		total += b
		if asn != 0 {
			withASN += b
		}
		svc[a.String()] = s
	}
	_ = rr.Close()
	if total == 0 || float64(withASN)/float64(total) < 0.95 {
		t.Fatalf("ASN coverage = %d/%d bytes (%.1f %%), want ≥ 95 %%", withASN, total, 100*float64(withASN)/float64(max(total, 1)))
	}
	t.Logf("cobertura ASN: %.2f %% de %d bytes remotos", 100*float64(withASN)/float64(total), total)
	// Servicios conocidos del escenario.
	checked := 0
	for ip, s := range svc {
		a := netip.MustParseAddr(ip)
		var want string
		switch {
		case netip.MustParsePrefix("45.57.0.0/17").Contains(a), netip.MustParsePrefix("198.38.96.0/19").Contains(a):
			want = "netflix"
		case netip.MustParsePrefix("162.159.200.0/24").Contains(a):
			want = "ntp"
		case netip.MustParsePrefix("1.1.1.0/24").Contains(a):
			want = "cloudflare_dns"
		default:
			continue
		}
		checked++
		if s != catalog.ID("service", want) {
			t.Errorf("remote %s: service %s, want %s", ip, s, want)
		}
	}
	if checked == 0 {
		t.Fatal("no known-service destinations in the scenario")
	}
}
