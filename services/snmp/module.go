// Package snmp es el módulo del rol snmp de `horus` (ADR-0025, EP-08,
// docs/services.md ficha snmp): sondea cada router por el túnel WireGuard
// (ICMP cada 30 s, SNMPv3 cada 60 s, salud cada 300 s), mantiene el estado
// observado con razón e histéresis correlacionando ICMP, SNMP, handshake WG y
// exportador de flujos (docs/architecture.md §10.4), lo persiste en NATS KV
// `snmp_router_state`, publica eventos (SNMP_EVENTS) y telemetría (TLM_SNMP),
// escribe el histórico en ClickHouse (snmp.*) y sirve GET /routers/status,
// /routers/{id}/metrics/live, /routers/{id}/interfaces y POST
// /routers/{id}/poll. Publica api.RouterStatusReader en module.Services para
// analytics y el gateway.
//
// Servicios de infraestructura del ISP (I2): inventario en PostgreSQL
// (esquema snmp, RLS), chequeos activos (ICMP, TCP, HTTP) y pasivos desde
// flows.flows_raw (no_traffic, ddos_inbound) con histéresis, y
// horus.snmp.service.state_changed por el outbox snmp.outbox.
//
// Degradación: sin NATS funciona solo en memoria (sin eventos ni señal del
// túnel); sin ClickHouse sin histórico; sin socket ICMP la señal ICMP queda
// unknown y el estado se calcula con SNMP y WireGuard.
package snmp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	alertsapi "github.com/hcdestroyer/horus-flow/services/alerts/api"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	collectorapi "github.com/hcdestroyer/horus-flow/services/collector/api"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/api"
	chadapter "github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/clickhouse"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/httpapi"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/icmp"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/natsbus"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/pgstore"
	pgadapter "github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/postgres"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/probe"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/app"
	modcfg "github.com/hcdestroyer/horus-flow/services/snmp/internal/config"
	"github.com/hcdestroyer/horus-flow/services/snmp/migrations"
)

// Role es el nombre del rol en HORUS_ROLES: pollers SNMP/ICMP y estado observado.
const Role = "snmp"

type mod struct {
	cfg    modcfg.Config
	log    *slog.Logger
	deps   module.Deps
	bus    *natsx.Bus
	inv    *flowinv.Store
	engine *app.Engine
	opts   app.Options
	writer *chadapter.Writer
	pinger *icmp.Socket
	// Servicios de infraestructura (PostgreSQL, esquema snmp).
	db    *pgdb.DB
	infra *app.Infra
	sched *app.InfraScheduler
	flows *chadapter.FlowReader
}

// Register construye el módulo del rol snmp (firma module.Factory).
func Register(ctx context.Context, deps module.Deps) (module.Module, error) {
	cfg, err := config.Load[modcfg.Config](deps.Environ)
	if err != nil {
		return nil, fmt.Errorf("%s config: %w", Role, err)
	}
	log := deps.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	m := &mod{cfg: cfg, log: log, deps: deps, inv: flowinv.NewStore(nil)}
	if cfg.InventoryFile != "" {
		if s, err := flowinv.LoadFile(cfg.InventoryFile); err == nil {
			m.inv.Swap(s)
		} else {
			log.WarnContext(ctx, "snmp: inventory file not loaded (names from devices only)", "error", err)
		}
	}
	if m.bus, err = natsx.Shared(ctx, deps.Services, deps.Environ, log); err != nil {
		log.WarnContext(ctx, "snmp: NATS unavailable: in-memory only (no events, no tunnel signal, no persisted state)", "error", err)
		m.bus = nil
	}
	if m.bus == nil {
		log.WarnContext(ctx, "snmp: HORUS_NATS_URL not set: in-memory only (no events, no tunnel signal, no persisted state)")
	}
	m.opts = app.Options{Config: cfg, Inv: m.inv, Metrics: deps.Metrics, Logger: log,
		Devices: func() (devapi.Onboarding, bool) {
			return module.Lookup[devapi.Onboarding](deps.Services, devapi.ServiceOnboarding)
		},
		Credentials: func() (devapi.SNMPCredentialReader, bool) {
			return module.Lookup[devapi.SNMPCredentialReader](deps.Services, devapi.ServiceSNMPCredentials)
		},
	}
	if cfg.Enabled {
		sock, err := icmp.Open()
		if err != nil {
			log.WarnContext(ctx, "snmp: ICMP socket unavailable (udp4 needs net.ipv4.ping_group_range; raw needs CAP_NET_RAW): ICMP signal stays unknown", "error", err)
		} else {
			m.pinger = sock
			m.opts.Pinger = sock
			log.InfoContext(ctx, "snmp: ICMP socket ready", "mode", sock.Mode())
		}
	}
	var thStore app.ThresholdStore
	if cfg.PostgresDSN != "" {
		db, err := pgdb.Open(ctx, pgdb.Config{DSN: cfg.PostgresDSN, Password: cfg.PostgresPassword.Reveal(), AppRole: cfg.AppRole,
			PlatformRole: cfg.PlatformRole, MaxConns: 8})
		if err != nil {
			return nil, fmt.Errorf("%s: postgres: %w", Role, err)
		}
		m.db = db
		st := pgstore.New(db)
		thStore, m.opts.Thresholds = st, st
	} else {
		log.WarnContext(ctx, "snmp: HORUS_POSTGRES_DSN not set: installation thresholds for every ISP (not editable from the console)")
	}
	m.engine = app.New(m.opts)
	if err := m.setupInfra(ctx); err != nil {
		return nil, err
	}
	if deps.Services != nil {
		if err := deps.Services.Provide(api.ServiceRouterStatus, m.engine); err != nil {
			return nil, err
		}
	}
	if deps.Routes != nil {
		if v := verifier(cfg, deps); v != nil {
			h := httpapi.New(m.engine, authz.NewGuard(v), func() (authapi.AuditRecorder, bool) {
				return module.Lookup[authapi.AuditRecorder](deps.Services, authapi.ServiceAudit)
			}, log)
			h.Mount(deps.Routes)
			h.MountThresholds(deps.Routes, app.NewThresholds(thStore, m.engine))
			httpapi.NewInfra(m.infra, authz.NewGuard(v), func() (authapi.AuditRecorder, bool) {
				return module.Lookup[authapi.AuditRecorder](deps.Services, authapi.ServiceAudit)
			}, cfg.PublicBaseURL, servicesCIDR(cfg), log).Mount(deps.Routes)
		} else {
			log.WarnContext(ctx, "snmp: no token verifier (auth not local, HORUS_JWT_PUBLIC_KEYS unset): routers API not served")
		}
	}
	if deps.Health != nil {
		if m.bus != nil {
			deps.Health.AddCheck(health.Check{Name: "nats", Critical: false, Probe: func(context.Context) error {
				if !m.bus.NC.IsConnected() {
					return errors.New("nats disconnected")
				}
				return nil
			}})
		}
		if cfg.Enabled {
			deps.Health.AddCheck(health.Check{Name: "poller", Critical: false, Probe: func(context.Context) error {
				h := m.engine.Health(uuid.Nil, time.Now())
				if h.LastCycleAt == nil || !h.Healthy {
					return errors.New("poller has not completed a cycle in 3 intervals")
				}
				return nil
			}})
		}
	}
	log.InfoContext(ctx, "snmp: registered", "enabled", cfg.Enabled, "interval", cfg.Interval.String(),
		"icmp_interval", cfg.ICMPInterval.String(), "health_interval", cfg.HealthInterval.String(), "concurrency", cfg.Concurrency,
		"clickhouse", cfg.ClickHouseDSN != "", "nats", m.bus != nil, "infra_services", m.db != nil)
	if m.db != nil && m.bus != nil {
		// horus.snmp.service.state_changed sale por el outbox snmp.outbox.
		return natsx.WithRelay(ctx, deps, m, m.db, migrations.Schema)
	}
	return m, nil
}

// setupInfra prepara el inventario y el planificador de servicios de
// infraestructura. Sin HORUS_POSTGRES_DSN la API responde 503.
func (m *mod) setupInfra(ctx context.Context) error {
	devices := func() (devapi.Onboarding, bool) {
		return module.Lookup[devapi.Onboarding](m.deps.Services, devapi.ServiceOnboarding)
	}
	// La conexión la abre Register (compartida con los umbrales por ISP).
	db := m.db
	if db == nil {
		m.log.WarnContext(ctx, "snmp: HORUS_POSTGRES_DSN not set: infrastructure services disabled (API 503)")
		m.infra = app.NewInfra(nil, devices, nil)
		return nil
	}
	var pinger icmp.Pinger
	if m.pinger != nil {
		pinger = m.pinger
	}
	m.sched = app.NewInfraScheduler(app.SchedulerOptions{Store: pgadapter.New(db), Prober: &probe.Prober{Pinger: pinger},
		Devices: devices, Concurrency: m.cfg.InfraConcurrency, Timeout: m.cfg.InfraTimeout, Metrics: m.deps.Metrics, Logger: m.log})
	if !m.cfg.Enabled {
		m.sched.Disable()
	}
	m.infra = app.NewInfra(pgadapter.New(db), devices, m.sched)
	m.infra.DenyTargets(deniedTargets(m.cfg))
	// LibreNMS (alerts): grupo «Horus · Infraestructura» tras cada alta, edición o baja, y la lista
	// para la sincronización completa desde la consola.
	m.infra.SetLibreNMS(func() (alertsapi.LibreNMSInfrastructure, bool) {
		return module.Lookup[alertsapi.LibreNMSInfrastructure](m.deps.Services, alertsapi.ServiceLibreNMSInfrastructure)
	}, m.log)
	if m.deps.Services != nil {
		if err := m.deps.Services.Provide(api.ServiceInfraTargets, api.InfraTargets(app.InfraTargets{Infra: m.infra})); err != nil {
			return err
		}
	}
	return nil
}

// deniedTargets son las redes internas de la instalación que un ISP no puede
// sondear: contenedores, red de servicios del túnel y las que se configuren.
func deniedTargets(cfg modcfg.Config) []netip.Prefix {
	var out []netip.Prefix
	for _, c := range append([]string{cfg.DockerSubnet, cfg.ServicesCIDR}, cfg.InfraDenyCIDRs...) {
		if p, err := netip.ParsePrefix(strings.TrimSpace(c)); err == nil {
			out = append(out, p.Masked())
		}
	}
	return out
}

// servicesCIDR es la red de servicios de Horus (origen de los sondeos).
func servicesCIDR(cfg modcfg.Config) netip.Prefix {
	p, err := netip.ParsePrefix(cfg.ServicesCIDR)
	if err != nil {
		return netip.Prefix{}
	}
	return p.Masked()
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

// Start migra PostgreSQL, abre ClickHouse (migración + escritor), el KV y el estado inicial.
func (m *mod) Start(ctx context.Context) error {
	if m.db != nil {
		if err := m.db.Ping(ctx); err != nil {
			return fmt.Errorf("%s: postgres: %w", Role, err)
		}
		if m.cfg.PGMigrate {
			n, err := pgdb.Migrate(ctx, m.db, migrations.Schema, migrations.Postgres(), m.log)
			if err != nil {
				return fmt.Errorf("%s: migrate postgres: %w", Role, err)
			}
			m.log.InfoContext(ctx, "snmp migrations applied", "count", n)
		}
		if m.deps.Health != nil {
			m.deps.Health.AddCheck(health.Check{Name: "postgres", Critical: false, Probe: m.db.Ping})
		}
	}
	if m.cfg.ClickHouseDSN == "" {
		m.log.WarnContext(ctx, "snmp: HORUS_CLICKHOUSE_DSN not set: no metric history (live state only)")
	} else {
		if m.cfg.Migrate {
			if err := chadapter.Migrate(ctx, m.cfg.ClickHouseDSN, m.cfg.ClickHousePassword, m.cfg.SNMPPassword, m.log); err != nil {
				return fmt.Errorf("%s: migrate clickhouse: %w", Role, err)
			}
		}
		user, pw := "", m.cfg.ClickHousePassword
		if m.cfg.SNMPPassword != "" {
			user, pw = chadapter.UserSNMP, m.cfg.SNMPPassword
		}
		w, err := chadapter.Open(m.cfg.ClickHouseDSN, user, pw, m.cfg.WriterQueue, m.deps.Metrics, m.log)
		if err != nil {
			return err
		}
		m.writer = w
		m.opts.Sink = w
		if m.sched != nil {
			if fr, err := chadapter.OpenFlowReader(m.cfg.ClickHouseDSN, user, pw, 0); err != nil {
				m.log.WarnContext(ctx, "snmp: flows reader unavailable: passive infra checks stay unknown", "error", err)
			} else {
				m.flows = fr
				m.sched.SetFlows(fr)
			}
		}
		if m.deps.Health != nil {
			m.deps.Health.AddCheck(health.Check{Name: "clickhouse", Critical: false, Probe: w.Ping})
		}
	}
	if m.bus != nil {
		js := m.bus.JS
		if st, err := natsbus.OpenState(ctx, js); err != nil {
			m.log.WarnContext(ctx, "snmp: state bucket unavailable: router state starts empty after a restart", "error", err)
		} else {
			m.opts.Store = st
		}
		m.opts.Events = natsbus.Events{JS: js}
		m.opts.Telemetry = natsbus.Telemetry{JS: js}
		if kv, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: flowbus.ExporterStateBucket,
			Description: "Estado de los exportadores de flujos (I1-09)", History: 1, Storage: jetstream.FileStorage}); err == nil {
			m.opts.Exporters = collectorapi.KVStates{KV: kv}
		}
	}
	// El engine se construyó en Register (Services); se le pasan los adaptadores abiertos aquí.
	m.engine.Attach(m.opts)
	return m.engine.Start(ctx)
}

// Run sondea hasta el apagado.
func (m *mod) Run(ctx context.Context) error {
	var js jetstream.JetStream
	if m.bus != nil {
		js = m.bus.JS
		go func() { _ = natsbus.RunWireGuard(ctx, m.bus, m.engine, m.log) }()
		go func() { _ = natsbus.RunDevices(ctx, m.bus, m.engine, m.log) }()
	}
	go flowinv.Keep(ctx, m.inv, m.cfg.InventoryFile, js, "flows-inventory-"+Role, m.log)
	if m.sched != nil {
		go func() { _ = m.sched.Run(ctx) }()
	}
	return m.engine.Run(ctx)
}

// Stop vacía el escritor y cierra el socket ICMP.
func (m *mod) Stop(ctx context.Context) error {
	var errs []error
	if m.writer != nil {
		errs = append(errs, m.writer.Close(ctx))
	}
	if m.pinger != nil {
		errs = append(errs, m.pinger.Close())
	}
	if m.flows != nil {
		errs = append(errs, m.flows.Close())
	}
	if m.db != nil {
		m.db.Close()
	}
	return errors.Join(errs...)
}
