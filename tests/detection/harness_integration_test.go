//go:build integration

package detection_test

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/chmigrate"
	"github.com/hcdestroyer/horus-flow/packages/go/chtest"
	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/pcapread"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
	"github.com/hcdestroyer/horus-flow/services/collector"
	"github.com/hcdestroyer/horus-flow/services/detection"
	"github.com/hcdestroyer/horus-flow/services/detection/api/reputation"
	"github.com/hcdestroyer/horus-flow/services/ingester"
	"github.com/hcdestroyer/horus-flow/services/ingester/api/chschema"
)

// detectionCHPassword es la contraseña del usuario horus_detection en el
// ClickHouse de test (lector por tenant con row policies).
const detectionCHPassword = "detection-test-password-0123"

func repo(parts ...string) string { return filepath.Join(append([]string{"..", ".."}, parts...)...) }

// simExpected es lo que usa la prueba del expected.json del simulador.
type simExpected struct {
	Scenario   string    `json:"scenario"`
	Start      time.Time `json:"start"`
	Duration   int       `json:"duration_seconds"`
	Indicators []struct {
		IP     string `json:"ip"`
		Kind   string `json:"kind"`
		Source string `json:"source"`
	} `json:"indicators"`
	Findings []struct {
		Client   string `json:"client"`
		Kind     string `json:"kind"`
		Severity string `json:"severity"`
	} `json:"findings"`
	Exporters []struct {
		ExporterIP    string `json:"exporter_ip"`
		IPv6ClientLen int    `json:"ipv6_client_len"`
		Prefixes      struct {
			Customers      []string `json:"customers"`
			Infrastructure []string `json:"infrastructure"`
			Excluded       []string `json:"excluded"`
		} `json:"prefixes"`
		Totals struct {
			DataRecords int `json:"data_records"`
		} `json:"totals"`
		ByStatus map[string]int `json:"by_status"`
	} `json:"exporters"`
}

// world es un ISP con su pipeline de flujos (collector + ingester reales),
// ClickHouse, PostgreSQL y el módulo detection.
type world struct {
	t      *testing.T
	tenant uuid.UUID
	site   uuid.UUID
	router uuid.UUID
	ch     *sql.DB       // administrador de ClickHouse (preparar y comprobar)
	chDet  *sql.DB       // usuario horus_detection
	pg     *pgxpool.Pool // administrador de PostgreSQL
	det    module.Module
	env    []string
}

func migrateCH(ctx context.Context, dsn, pw string) error {
	return chschema.MigrateClickHouse(ctx, []string{"HORUS_CLICKHOUSE_DSN=" + dsn, "HORUS_CLICKHOUSE_PASSWORD=" + pw,
		"HORUS_CLICKHOUSE_DETECTION_PASSWORD=" + detectionCHPassword}, slog.New(slog.DiscardHandler))
}

func publish(t *testing.T, s *reputation.Snapshot) string {
	t.Helper()
	dir := t.TempDir()
	if _, _, err := s.Publish(datasets.SnapshotDir{Root: dir}); err != nil {
		t.Fatal(err)
	}
	return dir
}

// newWorld prepara inventario, reputación y servicios: ingestSnap es el
// snapshot con el que el ingester marca la reputación en ingesta y
// engineSnap el que ve el motor (barrido retroactivo).
func newWorld(t *testing.T, d flowinv.Data, tenant, site, router uuid.UUID, ingestSnap, engineSnap *reputation.Snapshot) *world {
	t.Helper()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	ch := chtest.Start(t, migrateCH)
	natsURL := natsURL(t)
	b, _ := json.Marshal(d)
	inv := filepath.Join(t.TempDir(), "inventory.json")
	if err := os.WriteFile(inv, b, 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"HORUS_NATS_URL=" + natsURL, "HORUS_NATS_ENSURE_STREAMS=true", "HORUS_TLM_FLOWS_MAX_BYTES=67108864",
		"HORUS_FLOWS_INVENTORY_FILE=" + inv}
	ingEnv := append([]string{"HORUS_CLICKHOUSE_DSN=" + ch.DSN, "HORUS_CLICKHOUSE_PASSWORD=" + ch.Password, "HORUS_INGESTER_CH_MIGRATE=false"}, env...)
	if ingestSnap != nil {
		ingEnv = append(ingEnv, "HORUS_REPUTATION_SNAPSHOT_DIR="+publish(t, ingestSnap))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	t.Cleanup(cancel)
	ing, err := ingester.Register(ctx, module.Deps{Logger: log, Common: config.Common{Env: "dev"}, Environ: ingEnv})
	if err != nil {
		t.Fatal(err)
	}
	if err := ing.(module.Starter).Start(ctx); err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- ing.Run(runCtx) }()
	t.Cleanup(func() {
		stop()
		<-done
		_ = ing.(module.Stopper).Stop(context.Background())
	})
	admin, err := chmigrate.Open(ch.DSN, ch.Password)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	host := strings.SplitN(strings.TrimPrefix(ch.DSN, "clickhouse://"), "@", 2)[1]
	chDet, err := chmigrate.Open("clickhouse://horus_detection@"+host, detectionCHPassword)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = chDet.Close() })

	// Módulo detection real (Register + Start = migraciones), sin planificador.
	pgDSN := pgtest.New(t)
	key, err := authz.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub, err := authz.MarshalPublicKeyPEM(key.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	detEnv := []string{"HORUS_POSTGRES_DSN=" + pgDSN, "HORUS_CLICKHOUSE_DSN=" + ch.DSN, "HORUS_CLICKHOUSE_DETECTION_PASSWORD=" + detectionCHPassword,
		"HORUS_JWT_PUBLIC_KEYS=" + string(pub), "HORUS_DETECTION_INTERVAL=1h"}
	if engineSnap != nil {
		detEnv = append(detEnv, "HORUS_REPUTATION_SNAPSHOT_DIR="+publish(t, engineSnap))
	}
	det, err := detection.Register(ctx, module.Deps{Logger: log, Common: config.Common{Env: "dev"}, Environ: detEnv})
	if err != nil {
		t.Fatal(err)
	}
	if err := det.(module.Starter).Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = det.(module.Stopper).Stop(context.Background()) })
	pg, err := pgxpool.New(context.Background(), pgDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pg.Close)
	return &world{t: t, tenant: tenant, site: site, router: router, ch: admin, chDet: chDet, pg: pg, det: det, env: env}
}

// simInventory construye el inventario de un escenario del simulador.
func simInventory(exp simExpected) (flowinv.Data, uuid.UUID, uuid.UUID, uuid.UUID) {
	ex := exp.Exporters[0]
	tenant, site, router, realmPriv, realmPub := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	d := flowinv.Data{
		Exporters: []flowinv.Exporter{{TenantID: tenant, RouterID: router, SiteID: site, TunnelIP: netip.MustParseAddr(ex.ExporterIP)}},
		Realms: []flowinv.Realm{{ID: realmPriv, TenantID: tenant, Kind: flowinv.RealmNodePrivate, SiteID: site},
			{ID: realmPub, TenantID: tenant, Kind: flowinv.RealmPublic}},
	}
	add := func(list []string, role string) {
		for _, s := range list {
			pf := netip.MustParsePrefix(s)
			realm := realmPub
			if pf.Addr().IsPrivate() {
				realm = realmPriv
			}
			d.Prefixes = append(d.Prefixes, flowinv.ClientPrefix{ID: uuid.New(), TenantID: tenant, SiteID: site,
				RealmID: realm, Prefix: pf, Role: role, IPv6ClientLen: ex.IPv6ClientLen})
		}
	}
	add(ex.Prefixes.Customers, flowinv.RoleCustomers)
	add(ex.Prefixes.Infrastructure, flowinv.RoleInfrastructure)
	add(ex.Prefixes.Excluded, flowinv.RoleExcluded)
	return d, tenant, site, router
}

func loadExpected(t *testing.T, path string) simExpected {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var exp simExpected
	if err := json.Unmarshal(b, &exp); err != nil {
		t.Fatal(err)
	}
	return exp
}

// scenarioSnapshot es el feed de prueba del escenario (indicators).
func scenarioSnapshot(t *testing.T, exp simExpected, listed time.Time) *reputation.Snapshot {
	t.Helper()
	if len(exp.Indicators) == 0 {
		return nil
	}
	var entries []reputation.Entry
	for _, i := range exp.Indicators {
		a := netip.MustParseAddr(i.IP)
		entries = append(entries, reputation.Entry{Prefix: netip.PrefixFrom(a, a.BitLen()), Indicator: reputation.Indicator{
			Source: i.Source, Category: reputation.Category(i.Kind), Confidence: 90, FirstSeen: listed, Threat: "TestBot"}})
	}
	s, err := reputation.NewSnapshot(datasets.SnapshotMeta{CreatedAt: listed, Sources: []datasets.SourceRef{{ID: exp.Indicators[0].Source}}}, entries)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// shift traslada los tiempos de los lotes (los fixtures son de enero de 2026
// y flows_raw tiene TTL de 7 días).
//
// Debe ser idempotente: el publisher del collector reintenta con el MISMO
// *nats.Msg cuando JetStream tarda o falla (backoff), y collector.Replay
// vuelve a pasar el mensaje por Transform. Si se modificara m.Data en su
// sitio, un reintento trasladaría el lote dos veces (~9 meses de más) y sus
// flujos saldrían de la ventana: era la causa de los fallos intermitentes de
// beacon y sustained_out (dependía de la latencia de JetStream, no de la
// hora). Por eso se devuelve un mensaje nuevo y el original no se toca.
func shift(d time.Duration) func(*nats.Msg) *nats.Msg {
	return func(m *nats.Msg) *nats.Msg {
		if !strings.HasPrefix(m.Subject, flowbus.SubjectBatchPrefix) {
			return m
		}
		fb, err := flowpb.UnmarshalFlowBatch(m.Data)
		if err != nil {
			return m
		}
		for i := range fb.Records {
			fb.Records[i].TS = fb.Records[i].TS.Add(d)
			fb.Records[i].FlowStart = fb.Records[i].FlowStart.Add(d)
		}
		out := nats.NewMsg(m.Subject)
		out.Header = m.Header
		out.Data = fb.Marshal()
		return out
	}
}

// replay pasa una captura por el collector real y espera a que el ingester
// escriba want filas.
func (w *world) replay(path string, d time.Duration, want int) {
	w.t.Helper()
	ds, err := pcapread.ReadFile(path)
	if err != nil {
		w.t.Fatal(err)
	}
	var tr func(*nats.Msg) *nats.Msg
	if d != 0 {
		tr = shift(d)
	}
	if _, err := collector.Replay(context.Background(), w.env, ds, collector.ReplayOptions{Transform: tr}); err != nil {
		w.t.Fatal(err)
	}
	var n uint64
	for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		if err := w.ch.QueryRow("SELECT count() FROM flows.flows_raw WHERE tenant_id = ?", w.tenant).Scan(&n); err != nil {
			w.t.Fatal(err)
		}
		if int(n) >= want {
			break
		}
	}
	if int(n) != want {
		w.t.Fatalf("flows_raw rows = %d, want %d", n, want)
	}
}

// evaluate corre el motor (todos los detectores) en now.
func (w *world) evaluate(now time.Time) detection.EvaluateReport {
	w.t.Helper()
	rep, err := detection.Evaluate(context.Background(), w.det, w.tenant, now)
	if err != nil {
		w.t.Fatalf("evaluate: %v", err)
	}
	w.t.Logf("evaluate(%s): %+v", now.Format(time.RFC3339), rep)
	return rep
}

// reason es una razón del hallazgo.
type reason struct {
	Code   string         `json:"code"`
	Detail string         `json:"detail"`
	Data   map[string]any `json:"data"`
}

// finding es la vista de prueba de detection.finding.
type finding struct {
	Client, Kind, Severity  string
	Confidence              float64
	TargetType, TargetValue string
	Signals                 []string
	Reasons                 []reason
	Evidence                map[string]any
	Summary                 string
	Site, Router, Customer  uuid.UUID
	WindowFrom, LastSeen    time.Time
}

func (f finding) key() string { return fmt.Sprintf("%s|%s|%s", f.Client, f.Kind, f.Severity) }

// findings devuelve los hallazgos del tenant (administrador de PostgreSQL).
func (w *world) findings() []finding {
	w.t.Helper()
	rows, err := w.pg.Query(context.Background(), `SELECT CASE WHEN family(address) = 4 THEN host(address) ELSE text(address) END,
			kind, severity, confidence, target_type, target_value, signals, reasons::text, evidence::text, summary->>'text',
			site_id, router_id, customer_id, window_from, last_seen_at
		FROM detection.finding WHERE tenant_id = $1 ORDER BY opened_at, id`, w.tenant)
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	var out []finding
	for rows.Next() {
		var f finding
		var reasons, evidence string
		if err := rows.Scan(&f.Client, &f.Kind, &f.Severity, &f.Confidence, &f.TargetType, &f.TargetValue, &f.Signals, &reasons, &evidence,
			&f.Summary, &f.Site, &f.Router, &f.Customer, &f.WindowFrom, &f.LastSeen); err != nil {
			w.t.Fatal(err)
		}
		_ = json.Unmarshal([]byte(reasons), &f.Reasons)
		_ = json.Unmarshal([]byte(evidence), &f.Evidence)
		out = append(out, f)
	}
	return out
}

func detectionEvaluate(w *world, tenant uuid.UUID, now time.Time) (detection.EvaluateReport, error) {
	return detection.Evaluate(context.Background(), w.det, tenant, now)
}

// natsURL arranca un NATS con JetStream en proceso con un límite de
// almacenamiento fijo: así la reserva de los streams (FLOWS_EVENTS reserva
// 1 GiB) no depende del disco libre de la máquina en ese momento.
func natsURL(t *testing.T) string {
	t.Helper()
	s, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(),
		JetStreamMaxStore: 8 << 30, JetStreamMaxMemory: 256 << 20, NoLog: true, NoSigs: true, MaxPayload: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	go s.Start()
	if !s.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(func() {
		s.Shutdown()
		s.WaitForShutdown()
	})
	return s.ClientURL()
}
