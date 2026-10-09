//go:build integration

package flows_test

import (
	"context"
	"database/sql"
	"encoding/json"
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
	"github.com/hcdestroyer/horus-flow/services/collector"
	"github.com/hcdestroyer/horus-flow/services/ingester"
	"github.com/hcdestroyer/horus-flow/services/ingester/api/chschema"
)

func repo(parts ...string) string { return filepath.Join(append([]string{"..", ".."}, parts...)...) }

// pipeline es un ClickHouse + NATS + ingester listos para recibir lotes.
type pipeline struct {
	t       *testing.T
	db      *sql.DB
	js      jetstream.JetStream
	env     []string
	tenant  uuid.UUID
}

// startPipeline arranca el ingester real con el inventario d.
func startPipeline(t *testing.T, d flowinv.Data, tenant uuid.UUID, extra ...string) *pipeline {
	t.Helper()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	ch := chtest.Start(t, func(ctx context.Context, dsn, pw string) error {
		return chschema.MigrateClickHouse(ctx, []string{"HORUS_CLICKHOUSE_DSN=" + dsn, "HORUS_CLICKHOUSE_PASSWORD=" + pw}, slog.New(slog.DiscardHandler))
	})
	natsURL := flowbustest.URL(t)
	if _, err := flowinv.New(d); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(d)
	inv := filepath.Join(t.TempDir(), "inventory.json")
	if err := os.WriteFile(inv, b, 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"HORUS_NATS_URL=" + natsURL, "HORUS_NATS_ENSURE_STREAMS=true", "HORUS_TLM_FLOWS_MAX_BYTES=268435456",
		"HORUS_FLOWS_INVENTORY_FILE=" + inv}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	ing, err := ingester.Register(ctx, module.Deps{Logger: log, Common: config.Common{Env: "dev"}, Environ: append(append([]string{
		"HORUS_CLICKHOUSE_DSN=" + ch.DSN, "HORUS_CLICKHOUSE_PASSWORD=" + ch.Password, "HORUS_INGESTER_CH_MIGRATE=false",
	}, env...), extra...)})
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
	nc, js, err := flowbus.Connect(natsURL, "flows-e2e")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	db, err := chmigrate.Open(ch.DSN, ch.Password)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &pipeline{t: t, db: db, js: js, env: env, tenant: tenant}
}

// replay pasa una captura por el collector real (collector.Replay).
func (p *pipeline) replay(capPath string, tr func(*nats.Msg) *nats.Msg) collector.ReplayStats {
	p.t.Helper()
	ds, err := pcapread.ReadFile(capPath)
	if err != nil {
		p.t.Fatal(err)
	}
	st, err := collector.Replay(context.Background(), p.env, ds, collector.ReplayOptions{Transform: tr})
	if err != nil {
		p.t.Fatal(err)
	}
	return st
}

func (p *pipeline) count() int {
	p.t.Helper()
	var n uint64
	if err := p.db.QueryRow("SELECT count() FROM flows.flows_raw WHERE tenant_id = ?", p.tenant).Scan(&n); err != nil {
		p.t.Fatal(err)
	}
	return int(n)
}

func (p *pipeline) waitCount(want int) {
	p.t.Helper()
	var n int
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		if n = p.count(); n >= want {
			break
		}
	}
	if n != want {
		p.t.Fatalf("flows_raw rows = %d, want %d", n, want)
	}
}

// counts devuelve SELECT k, count() agrupado.
func (p *pipeline) counts(sqlText string) map[string]int {
	p.t.Helper()
	rows, err := p.db.Query(sqlText, p.tenant)
	if err != nil {
		p.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n uint64
		if err := rows.Scan(&k, &n); err != nil {
			p.t.Fatal(err)
		}
		out[k] = int(n)
	}
	return out
}
