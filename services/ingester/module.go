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
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	collectorapi "github.com/hcdestroyer/horus-flow/services/collector/api"
	chadapter "github.com/hcdestroyer/horus-flow/services/ingester/internal/adapters/clickhouse"
	"github.com/hcdestroyer/horus-flow/services/ingester/internal/adapters/httpapi"
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
	m := &ingester{Module: module.Idle(), cfg: cfg, log: log, deps: deps, inv: flowinv.NewStore(nil), states: &lazyStates{}}
	if cfg.NATSURL != "" {
		m.metrics = app.NewMetrics(deps.Metrics)
		if cfg.InventoryFile != "" {
			s, err := flowinv.LoadFile(cfg.InventoryFile)
			if err != nil {
				return nil, fmt.Errorf("%s: inventory: %w", Role, err)
			}
			m.inv.Swap(s)
		}
		if deps.Routes != nil {
			if v := verifier(cfg, deps); v != nil {
				httpapi.New(authz.NewGuard(v), m.inv, m.states).Mount(deps.Routes)
			} else {
				log.Warn("ingester: no token verifier (auth not local, HORUS_JWT_PUBLIC_KEYS unset): /flow-exporters not served")
			}
		}
	}
	return m, nil
}

func verifier(cfg modcfg.Config, deps module.Deps) *authz.Verifier {
	if v, ok := module.Lookup[*authz.Verifier](deps.Services, authapi.ServiceVerifier); ok {
		return v
	}
	if cfg.PublicKeys == "" {
		return nil
	}
	keys, err := authz.ParsePublicKeysPEM([]byte(cfg.PublicKeys))
	if err != nil {
		return nil
	}
	return authz.NewVerifier(keys, cfg.Issuer, nil)
}

// lazyStates da acceso al bucket KV del estado de exportadores una vez conectado.
type lazyStates struct{ kv atomic.Pointer[httpapi.KVStates] }

func (l *lazyStates) Get(ctx context.Context, id uuid.UUID) (*collectorapi.FlowExporter, error) {
	kv := l.kv.Load()
	if kv == nil {
		return nil, nil
	}
	return kv.Get(ctx, id)
}

type ingester struct {
	module.Module
	cfg     modcfg.Config
	log     *slog.Logger
	deps    module.Deps
	inv     *flowinv.Store
	metrics *app.Metrics
	states  *lazyStates

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
	if kv, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: flowbus.ExporterStateBucket,
		Description: "Estado de los exportadores de flujos (I1-09)", History: 1, Storage: jetstream.FileStorage}); err == nil {
		m.states.kv.Store(&httpapi.KVStates{KV: kv})
	} else {
		m.log.WarnContext(ctx, "exporter state bucket unavailable", "error", err)
	}
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

type jsPub struct{ js jetstream.JetStream }

func (p jsPub) PublishMsg(ctx context.Context, msg *nats.Msg) error {
	_, err := p.js.PublishMsg(ctx, msg)
	return err
}

// wire añade enriquecimiento, descubrimiento y resúmenes al procesador.
func (m *ingester) wire(_ context.Context, proc *app.Processor) {
	pub := jsPub{js: m.js}
	disc := app.NewDiscovery(app.DiscoveryOptions{TTL: m.cfg.FirstSeenTTL, PerMinute: m.cfg.DiscoveryPerMinute,
		RealmMax: m.cfg.DiscoveryRealmMax}, pub, m.deps.Metrics, m.log)
	disc.LoadKnown(m.inv.Load().Data().Customers)
	proc.Observers = append(proc.Observers, disc)
	m.loops = append(m.loops,
		func(ctx context.Context) { disc.Run(ctx, m.cfg.FirstSeenInterval, m.cfg.ActivityInterval) },
		func(ctx context.Context) { disc.RunKnownClients(ctx, m.js, m.log) },
	)
}

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
