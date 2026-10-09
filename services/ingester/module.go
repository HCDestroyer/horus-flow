// Package ingester es el módulo del rol ingester de `horus` (ADR-0025,
// docs/services.md).
//
// Al arrancar aplica el esquema ClickHouse (I0-13) si hay
// HORUS_CLICKHOUSE_DSN y HORUS_INGESTER_CH_MIGRATE no es false. Con
// HORUS_NATS_URL consume los lotes del collector (TLM_FLOWS, durable
// flows-ingester), atribuye cada flujo a su cliente con la regla de
// traffic-model.md §4.4, enriquece, descubre clientes (first_seen /
// activity_summary, sin escribir en PostgreSQL) e inserta en flows.flows_raw
// con insert_deduplication_token = batch_id (I1-04, I1-05).
package ingester

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	chadapter "github.com/hcdestroyer/horus-flow/services/ingester/internal/adapters/clickhouse"
	"github.com/hcdestroyer/horus-flow/services/ingester/internal/app"
	modcfg "github.com/hcdestroyer/horus-flow/services/ingester/internal/config"
)

// Role es el nombre del rol en HORUS_ROLES: consumo de lotes de flujos y métricas SNMP hacia ClickHouse.
const Role = "ingester"

// Register construye el módulo del rol ingester (firma module.Factory).
func Register(_ context.Context, deps module.Deps) (module.Module, error) {
	cfg, err := config.Load[modcfg.Config](deps.Environ)
	if err != nil {
		return nil, fmt.Errorf("%s config: %w", Role, err)
	}
	log := deps.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	m := &ingester{Module: module.Idle(), cfg: cfg, log: log, deps: deps, inv: flowinv.NewStore(nil)}
	if cfg.NATSURL != "" {
		m.metrics = app.NewMetrics(deps.Metrics)
		if cfg.InventoryFile != "" {
			s, err := flowinv.LoadFile(cfg.InventoryFile)
			if err != nil {
				return nil, fmt.Errorf("%s: inventory: %w", Role, err)
			}
			m.inv.Swap(s)
		}
	}
	return m, nil
}

type ingester struct {
	module.Module
	cfg     modcfg.Config
	log     *slog.Logger
	deps    module.Deps
	inv     *flowinv.Store
	metrics *app.Metrics

	nc       *nats.Conn
	js       jetstream.JetStream
	writer   *chadapter.Writer
	cons     jetstream.Consumer
	consumer *app.Consumer
	loops    []func(ctx context.Context)
}

// Start implementa module.Starter: migra ClickHouse y prepara la ingesta
// antes de marcar el rol listo.
func (m *ingester) Start(ctx context.Context) error {
	if m.cfg.Migrate {
		if m.cfg.ClickHouseDSN == "" {
			m.log.Warn("HORUS_CLICKHOUSE_DSN not set: ClickHouse schema not migrated")
		} else if err := chadapter.Migrate(ctx, m.cfg, m.log); err != nil {
			return fmt.Errorf("%s: migrate clickhouse: %w", Role, err)
		}
	}
	if m.cfg.NATSURL == "" {
		return nil
	}
	if m.cfg.ClickHouseDSN == "" {
		return errors.New("ingester: HORUS_CLICKHOUSE_DSN is required to ingest flows")
	}
	user, password := "", m.cfg.ClickHousePassword
	if m.cfg.IngesterPassword != "" {
		user, password = chadapter.UserIngester, m.cfg.IngesterPassword
	}
	w, err := chadapter.OpenWriter(m.cfg.ClickHouseDSN, user, password)
	if err != nil {
		return err
	}
	m.writer = w
	nc, js, err := flowbus.Connect(m.cfg.NATSURL, "horus-ingester")
	if err != nil {
		return err
	}
	m.nc, m.js = nc, js
	if m.cfg.EnsureStreams {
		if err := flowbus.EnsureStreams(ctx, js, m.cfg.TLMMaxBytes); err != nil {
			return err
		}
	}
	cons, err := js.CreateOrUpdateConsumer(ctx, flowbus.StreamTelemetry, flowbus.IngesterConsumerConfig())
	if err != nil {
		return fmt.Errorf("ingester: consumer %s: %w", flowbus.ConsumerIngester, err)
	}
	m.cons = cons
	proc := &app.Processor{Inv: m.inv}
	m.wire(ctx, proc)
	m.consumer = &app.Consumer{Proc: proc, Ins: w, Workers: m.cfg.Workers, M: m.metrics, Log: m.log,
		DLQ: func(ctx context.Context, msg *nats.Msg) error { _, err := js.PublishMsg(ctx, msg); return err }}
	if h := m.deps.Health; h != nil {
		h.AddCheck(health.Check{Name: "clickhouse", Critical: false, Probe: w.Ping})
		h.AddCheck(health.Check{Name: "nats", Critical: false, Probe: func(context.Context) error {
			if !nc.IsConnected() {
				return errors.New("nats disconnected")
			}
			return nil
		}})
	}
	m.log.InfoContext(ctx, "ingester consuming flows", "stream", flowbus.StreamTelemetry, "durable", flowbus.ConsumerIngester)
	return nil
}

// wire añade enriquecimiento, descubrimiento y resúmenes al procesador.
func (m *ingester) wire(_ context.Context, _ *app.Processor) {}

// Run consume lotes hasta el apagado.
func (m *ingester) Run(ctx context.Context) error {
	if m.consumer == nil {
		<-ctx.Done()
		return nil
	}
	if m.cfg.InventoryFile != "" {
		go flowinv.WatchFile(ctx, m.cfg.InventoryFile, m.inv, 5*time.Second, m.log)
	}
	for _, l := range m.loops {
		go l(ctx)
	}
	return m.consumer.Run(ctx, m.cons)
}

// Stop cierra NATS y ClickHouse.
func (m *ingester) Stop(context.Context) error {
	if m.nc != nil {
		_ = m.nc.Drain()
	}
	if m.writer != nil {
		_ = m.writer.Close()
	}
	return nil
}

// MigrateClickHouse aplica el esquema ClickHouse con la configuración del
// entorno (HORUS_CLICKHOUSE_DSN, HORUS_CLICKHOUSE_PASSWORD[_FILE] y las
// contraseñas de usuarios por módulo). Lo usan `make migrate-ch` y, cuando
// exista, `horus migrate --module=ingester`.
func MigrateClickHouse(ctx context.Context, environ []string, log *slog.Logger) error {
	cfg, err := config.Load[modcfg.Config](environ)
	if err != nil {
		return fmt.Errorf("%s config: %w", Role, err)
	}
	if err := chadapter.MustHaveDSN(cfg); err != nil {
		return err
	}
	return chadapter.Migrate(ctx, cfg, log)
}
