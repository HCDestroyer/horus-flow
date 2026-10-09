//go:build integration

package collector_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/pcapread"
	"github.com/hcdestroyer/horus-flow/services/collector/internal/app"
	"github.com/hcdestroyer/horus-flow/services/ingester"
)

type sinkJS struct{ js jetstream.JetStream }

func (s sinkJS) PublishMsg(ctx context.Context, m *nats.Msg) error {
	_, err := s.js.PublishMsg(ctx, m)
	return err
}

type goldenExpected struct {
	Exporters []struct {
		ExporterIP string `json:"exporter_ip"`
		Prefixes   struct {
			Customers      []string `json:"customers"`
			Infrastructure []string `json:"infrastructure"`
		} `json:"prefixes"`
		Totals struct {
			DataRecords int `json:"data_records"`
		} `json:"totals"`
		ByStatus map[string]int `json:"by_status"`
		ByRule   map[string]int `json:"by_rule"`
		Clients  []struct {
			Key       string `json:"key"`
			Records   int    `json:"records"`
			BytesUp   uint64 `json:"bytes_up"`
			BytesDown uint64 `json:"bytes_down"`
		} `json:"clients"`
		SequenceGaps int `json:"sequence_gaps"`
		LostRecords  int `json:"lost_records"`
	} `json:"exporters"`
}

// TestGoldenRealMikroTik es el test dorado de I1-03/I1-04: reproduce la
// captura real anonimizada de un MikroTik con NAT en el router principal por
// el collector (NATS JetStream real) y el ingester (ClickHouse real) y
// comprueba en flows.flows_raw los recuentos de subida / bajada / internos /
// desconocidos y los bytes por cliente de ipfix-nat-20s.expected.json
// (regla de docs/traffic-model.md §4.4, con IE 226 para la bajada).
func TestGoldenRealMikroTik(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	fixture := filepath.Join("..", "..", "tests", "fixtures", "mikrotik-real")
	var exp goldenExpected
	b, err := os.ReadFile(filepath.Join(fixture, "ipfix-nat-20s.expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &exp); err != nil {
		t.Fatal(err)
	}
	ex := exp.Exporters[0]

	ch := chtest.Start(t, func(ctx context.Context, dsn, pw string) error {
		return ingester.MigrateClickHouse(ctx, []string{"HORUS_CLICKHOUSE_DSN=" + dsn, "HORUS_CLICKHOUSE_PASSWORD=" + pw}, log)
	})
	natsURL := flowbustest.URL(t)

	// Inventario: tenant nuevo por ejecución (el ClickHouse puede ser compartido).
	tenant, site, router, realm := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	d := flowinv.Data{
		Exporters: []flowinv.Exporter{{TenantID: tenant, RouterID: router, SiteID: site, Name: "po-router"}},
		Realms:    []flowinv.Realm{{ID: realm, TenantID: tenant, Kind: flowinv.RealmNodePrivate, SiteID: site}},
	}
	d.Exporters[0].TunnelIP = mustAddr(t, ex.ExporterIP)
	for _, p := range ex.Prefixes.Customers {
		d.Prefixes = append(d.Prefixes, flowinv.ClientPrefix{ID: uuid.New(), TenantID: tenant, SiteID: site, RealmID: realm,
			Prefix: mustPrefix(t, p), Role: flowinv.RoleCustomers})
	}
	for _, p := range ex.Prefixes.Infrastructure {
		d.Prefixes = append(d.Prefixes, flowinv.ClientPrefix{ID: uuid.New(), TenantID: tenant, SiteID: site, RealmID: realm,
			Prefix: mustPrefix(t, p), Role: flowinv.RoleInfrastructure})
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

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Ingester real (módulo completo).
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
	defer func() {
		stopIng()
		<-ingDone
		_ = ing.(module.Stopper).Stop(context.Background())
	}()

	// Collector: el motor real con la IP de origen de la captura (un socket
	// UDP de test no puede enviar desde 10.255.3.17).
	nc, js, err := flowbus.Connect(natsURL, "golden-collector")
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	eng := app.NewEngine(app.EngineOptions{Workers: 2, QueueDatagrams: 4096, BatchMaxRecords: 500, BatchMaxAge: time.Second,
		BufferBytes: 64 << 20, PendingTTL: 30 * time.Second, CollectorID: "golden",
		State: app.StateOptions{SilentAfter: 2 * time.Minute, LossThreshold: 0.01, LossWindow: 5 * time.Minute,
			ClockSkew: 30 * time.Second, Interval: time.Hour}},
		flowinv.NewStore(snap), sinkJS{js: js}, nil, app.NewMetrics(nil), log)
	colCtx, stopCol := context.WithCancel(ctx)
	colDone := make(chan error, 1)
	go func() { colDone <- eng.Run(colCtx) }()
	ds, err := pcapread.ReadFile(filepath.Join(fixture, "ipfix-nat-20s.pcapng"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dg := range ds {
		eng.Submit(app.Datagram{Src: dg.Src, Payload: dg.Payload, At: time.Now()})
	}
	time.Sleep(200 * time.Millisecond)
	stopCol() // vacía lotes abiertos y publica
	<-colDone

	db, err := chmigrate.Open(ch.DSN, ch.Password)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	want := ex.Totals.DataRecords - (ex.ByStatus["tunnel"])
	waitCount(ctx, t, db, tenant, want)

	// Recuentos por dirección y estado de atribución.
	q := func(sqlText string) map[string]int {
		rows, err := db.QueryContext(ctx, sqlText, tenant)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		out := map[string]int{}
		for rows.Next() {
			var k string
			var n uint64
			if err := rows.Scan(&k, &n); err != nil {
				t.Fatal(err)
			}
			out[k] = int(n)
		}
		return out
	}
	byDir := q("SELECT toString(direction), count() FROM flows.flows_raw WHERE tenant_id = ? GROUP BY direction")
	byStatus := q("SELECT toString(attribution_status), count() FROM flows.flows_raw WHERE tenant_id = ? GROUP BY attribution_status")
	wantDir := map[string]int{
		"upload":   ex.ByRule["upload_src"],
		"download": ex.ByRule["download_post_nat_dst"] + ex.ByRule["download_dst"],
		"internal": ex.ByRule["internal"],
		"unknown":  ex.ByStatus["unknown"] + ex.ByStatus["infrastructure"] + ex.ByStatus["transit"],
	}
	wantStatus := map[string]int{"attributed": ex.ByStatus["attributed"], "internal": ex.ByStatus["internal"], "unknown": ex.ByStatus["unknown"]}
	t.Logf("flows_raw por dirección: %v (esperado %v)", byDir, wantDir)
	t.Logf("flows_raw por estado: %v (esperado %v)", byStatus, wantStatus)
	for k, v := range wantDir {
		if byDir[k] != v {
			t.Errorf("direction %s = %d, want %d", k, byDir[k], v)
		}
	}
	for k, v := range wantStatus {
		if byStatus[k] != v {
			t.Errorf("attribution_status %s = %d, want %d", k, byStatus[k], v)
		}
	}

	// Bytes y registros por cliente (IP privada pre-NAT, D12).
	rows, err := db.QueryContext(ctx, `SELECT IPv6NumToString(client_ip), count(),
		sumIf(bytes, direction IN ('upload', 'internal')), sumIf(bytes, direction = 'download')
		FROM flows.flows_raw WHERE tenant_id = ? AND attribution_status IN ('attributed', 'internal')
		GROUP BY client_ip`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	type cl struct {
		n        int
		up, down uint64
	}
	got := map[string]cl{}
	for rows.Next() {
		var ip string
		var n, up, down uint64
		if err := rows.Scan(&ip, &n, &up, &down); err != nil {
			t.Fatal(err)
		}
		if len(ip) > 7 && ip[:7] == "::ffff:" {
			ip = ip[7:]
		}
		got[ip] = cl{int(n), up, down}
	}
	_ = rows.Close()
	if len(got) != len(ex.Clients) {
		t.Errorf("clients = %d, want %d", len(got), len(ex.Clients))
	}
	for _, c := range ex.Clients {
		g := got[c.Key]
		if g.n != c.Records || g.up != c.BytesUp || g.down != c.BytesDown {
			t.Errorf("client %s: got %+v want records=%d up=%d down=%d", c.Key, g, c.Records, c.BytesUp, c.BytesDown)
		}
	}
	t.Logf("clientes IPv4: %d (esperado %d)", len(got), len(ex.Clients))

	// Reentrega fuera de la ventana de deduplicación de JetStream: el mismo
	// lote con otro Nats-Msg-Id no duplica filas (insert_deduplication_token).
	st, err := js.Stream(ctx, flowbus.StreamTelemetry)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := st.GetMsg(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	m := nats.NewMsg(raw.Subject)
	m.Data, m.Header = raw.Data, raw.Header
	m.Header.Set(flowbus.HeaderMsgID, "redelivery-"+uuid.NewString())
	if _, err := js.PublishMsg(ctx, m); err != nil {
		t.Fatal(err)
	}
	cons, err := js.Consumer(ctx, flowbus.StreamTelemetry, flowbus.ConsumerIngester)
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		info, err := cons.Info(ctx)
		if err == nil && info.NumPending == 0 && info.NumAckPending == 0 {
			break
		}
	}
	if n := count(ctx, t, db, tenant); n != want {
		t.Fatalf("after redelivery flows_raw = %d, want %d (duplicated batch)", n, want)
	}
}

func count(ctx context.Context, t *testing.T, db *sql.DB, tenant uuid.UUID) int {
	t.Helper()
	var n uint64
	if err := db.QueryRowContext(ctx, "SELECT count() FROM flows.flows_raw WHERE tenant_id = ?", tenant).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return int(n)
}

func waitCount(ctx context.Context, t *testing.T, db *sql.DB, tenant uuid.UUID, want int) {
	t.Helper()
	var n int
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		if n = count(ctx, t, db, tenant); n >= want {
			break
		}
	}
	if n != want {
		t.Fatalf("flows_raw rows = %d, want %d", n, want)
	}
}

func mustAddr(t *testing.T, s string) (a netipAddr) {
	t.Helper()
	a, err := parseAddr(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func mustPrefix(t *testing.T, s string) netipPrefix {
	t.Helper()
	p, err := parsePrefix(s)
	if err != nil {
		t.Fatal(fmt.Errorf("prefix %q: %w", s, err))
	}
	return p
}
