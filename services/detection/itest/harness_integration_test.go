//go:build integration

package itest

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
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"

	"github.com/hcdestroyer/horus-flow/packages/go/chmigrate"
	"github.com/hcdestroyer/horus-flow/packages/go/chtest"
	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowbus/flowbustest"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/pcapread"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
	"github.com/hcdestroyer/horus-flow/services/collector"
	"github.com/hcdestroyer/horus-flow/services/detection/api/reputation"
	chreader "github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/clickhouse"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/postgres"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/repsnap"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/app"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/engine"
	"github.com/hcdestroyer/horus-flow/services/detection/migrations"
	"github.com/hcdestroyer/horus-flow/services/ingester"
	"github.com/hcdestroyer/horus-flow/services/ingester/api/chschema"
)

// detectionCHPassword es la contraseña del usuario horus_detection en el
// ClickHouse de test (lector por tenant con row policies).
const detectionCHPassword = "detection-test-password-0123"

func repo(parts ...string) string {
	return filepath.Join(append([]string{"..", "..", ".."}, parts...)...)
}

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
// ClickHouse, PostgreSQL y el motor de detección.
type world struct {
	t      *testing.T
	tenant uuid.UUID
	site   uuid.UUID
	router uuid.UUID
	ch     *sql.DB
	pg     *pgdb.DB
	reader *chreader.Reader
	svc    *app.Service
	engine *engine.Engine
	env    []string
}

func migrateCH(ctx context.Context, dsn, pw string) error {
	return chschema.MigrateClickHouse(ctx, []string{"HORUS_CLICKHOUSE_DSN=" + dsn, "HORUS_CLICKHOUSE_PASSWORD=" + pw,
		"HORUS_CLICKHOUSE_DETECTION_PASSWORD=" + detectionCHPassword}, slog.New(slog.DiscardHandler))
}

// newWorld prepara inventario, reputación y servicios: ingestSnap es el
// snapshot con el que el ingester marca la reputación en ingesta y
// engineSnap el que ve el motor (barrido retroactivo).
func newWorld(t *testing.T, d flowinv.Data, tenant, site, router uuid.UUID, ingestSnap, engineSnap *reputation.Snapshot) *world {
	t.Helper()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	ch := chtest.Start(t, migrateCH)
	natsURL := flowbustest.URL(t)
	b, _ := json.Marshal(d)
	inv := filepath.Join(t.TempDir(), "inventory.json")
	if err := os.WriteFile(inv, b, 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"HORUS_NATS_URL=" + natsURL, "HORUS_NATS_ENSURE_STREAMS=true", "HORUS_TLM_FLOWS_MAX_BYTES=268435456",
		"HORUS_FLOWS_INVENTORY_FILE=" + inv}
	ingEnv := append([]string{"HORUS_CLICKHOUSE_DSN=" + ch.DSN, "HORUS_CLICKHOUSE_PASSWORD=" + ch.Password, "HORUS_INGESTER_CH_MIGRATE=false"}, env...)
	var rep engine.Reputation
	if ingestSnap != nil {
		dir := t.TempDir()
		if _, _, err := ingestSnap.Publish(datasets.SnapshotDir{Root: dir}); err != nil {
			t.Fatal(err)
		}
		ingEnv = append(ingEnv, "HORUS_REPUTATION_SNAPSHOT_DIR="+dir)
	}
	if engineSnap != nil {
		dir := t.TempDir()
		if _, _, err := engineSnap.Publish(datasets.SnapshotDir{Root: dir}); err != nil {
			t.Fatal(err)
		}
		rep = repsnap.New(dir, time.Minute, log)
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
	reader, err := chreader.Open(ch.DSN, chreader.UserDetection, detectionCHPassword, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	pg := openPG(t)
	svc := app.NewService(pg, app.Options{Flows: reader})
	return &world{t: t, tenant: tenant, site: site, router: router, ch: admin, pg: pg, reader: reader, svc: svc,
		engine: engine.New(reader, svc, rep, engine.Options{Lag: 2 * time.Minute, Logger: log}), env: env}
}

// openPG crea una base vacía con el esquema detection migrado.
func openPG(t *testing.T) *pgdb.DB {
	t.Helper()
	dsn := pgtest.New(t)
	ctx := context.Background()
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: dsn, AppRole: "detection_app", PlatformRole: "detection_platform"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := pgdb.Migrate(ctx, db, migrations.Schema, migrations.Postgres(), nil); err != nil {
		t.Fatal(err)
	}
	return db
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
		m.Data = fb.Marshal()
		return m
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
func (w *world) evaluate(now time.Time) *engine.Report {
	w.t.Helper()
	rep, err := w.engine.Evaluate(context.Background(), w.tenant, now, true)
	if err != nil {
		w.t.Fatalf("evaluate: %v", err)
	}
	return rep
}

// findings devuelve los hallazgos del tenant (directamente de PostgreSQL).
func (w *world) findings() []domain.Finding {
	w.t.Helper()
	return findingsOf(w.t, w.pg, w.tenant)
}

func findingsOf(t *testing.T, db *pgdb.DB, tenant uuid.UUID) []domain.Finding {
	t.Helper()
	var out []domain.Finding
	err := db.TenantTx(context.Background(), pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(), `SELECT id FROM detection.finding ORDER BY opened_at, id`)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		for _, id := range ids {
			f, err := postgres.Get(context.Background(), tx, id, false)
			if err != nil {
				return err
			}
			out = append(out, *f)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
