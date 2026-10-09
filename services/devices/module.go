// Package devices es el módulo del rol devices de `horus` (ADR-0025,
// docs/services.md): nodos, routers, realms, prefijos de clientes y
// (I1) clientes = IPs.
//
// Al arrancar aplica las migraciones del esquema `devices`. Las rutas
// revalidan el access JWT con el validador de auth (module.Services) o, si
// auth no es local, con HORUS_JWT_PUBLIC_KEYS_FILE. Sin HORUS_POSTGRES_DSN, en
// desarrollo el rol queda inactivo con un aviso.
package devices

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/adapters/httpapi"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/adapters/postgres"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/adapters/routeros"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	modcfg "github.com/hcdestroyer/horus-flow/services/devices/internal/config"
	"github.com/hcdestroyer/horus-flow/services/devices/migrations"
)

// Role es el nombre del rol en HORUS_ROLES: nodos, routers, realms, credenciales y clientes.
const Role = "devices"

type mod struct {
	db        *pgdb.DB
	logger    *slog.Logger
	migrate   bool
	customers *app.Customers
	bus       *natsx.Bus
	every     time.Duration
}

// Register construye el módulo del rol devices (firma module.Factory).
func Register(ctx context.Context, deps module.Deps) (module.Module, error) {
	cfg, err := config.Load[modcfg.Config](deps.Environ)
	if err != nil {
		return nil, fmt.Errorf("%s config: %w", Role, err)
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	dev := deps.Common.Env == "" || deps.Common.Env == config.EnvDev
	if cfg.PostgresDSN == "" {
		if !dev {
			return nil, errors.New("devices: HORUS_POSTGRES_DSN is required")
		}
		logger.WarnContext(ctx, "devices inactive: HORUS_POSTGRES_DSN not set (dev)")
		return module.Idle(), nil
	}
	verifier, ok := module.Lookup[*authz.Verifier](deps.Services, authapi.ServiceVerifier)
	if !ok {
		if cfg.PublicKeys == "" {
			return nil, errors.New("devices: no token verifier (auth role not local and HORUS_JWT_PUBLIC_KEYS_FILE unset)")
		}
		keys, err := authz.ParsePublicKeysPEM([]byte(cfg.PublicKeys))
		if err != nil {
			return nil, fmt.Errorf("devices: HORUS_JWT_PUBLIC_KEYS_FILE: %w", err)
		}
		verifier = authz.NewVerifier(keys, cfg.Issuer, nil)
	}
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: cfg.PostgresDSN, Password: cfg.PostgresPassword.Reveal(),
		AppRole: cfg.AppRole, PlatformRole: cfg.PlatformRole, MaxConns: 10})
	if err != nil {
		return nil, err
	}
	store := postgres.New(db)
	svc := app.NewService(store, pagination.NewCodec([]byte(cfg.CursorKey.Reveal())), nil)
	sealer, err := loadSealer(cfg, dev, logger)
	if err != nil {
		db.Close()
		return nil, err
	}
	onboarding := app.NewOnboarding(store, sealer, nil)
	if deps.Services != nil {
		if err := deps.Services.Provide(devapi.ServiceOnboarding, onboarding); err != nil {
			db.Close()
			return nil, err
		}
	}
	if deps.Health != nil {
		deps.Health.AddCheck(health.Check{Name: "postgres", Critical: true, Probe: db.Ping})
	}
	var exclude []netip.Prefix
	for _, c := range cfg.TunnelCIDRs {
		if p, err := netip.ParsePrefix(strings.TrimSpace(c)); err == nil {
			exclude = append(exclude, p)
		}
	}
	reader := routeros.Reader{Timeout: cfg.RouterOSTimeout, BaseURL: func(ip string) string {
		return strings.ReplaceAll(cfg.RouterOSBaseURL, "{ip}", ip)
	}}
	importer := app.NewImporter(store, onboarding, reader, exclude,
		func() (authapi.AuditRecorder, bool) {
			return module.Lookup[authapi.AuditRecorder](deps.Services, authapi.ServiceAudit)
		}, nil, logger)
	customers := app.NewCustomers(store, pagination.NewCodec([]byte(cfg.CursorKey.Reveal())),
		func() (authapi.AuditRecorder, bool) {
			return module.Lookup[authapi.AuditRecorder](deps.Services, authapi.ServiceAudit)
		}, nil, logger)
	customers.InactivityDays, customers.RetentionMonths = cfg.CustomerInactivityDays, cfg.CustomerRetentionMonths
	if deps.Routes != nil {
		guard := authz.NewGuard(verifier)
		httpapi.New(svc, guard, cfg.PublicBaseURL, logger).Mount(deps.Routes)
		httpapi.NewImport(importer, guard, logger).Mount(deps.Routes)
		httpapi.NewCustomers(customers, guard, logger).Mount(deps.Routes)
	}
	bus, err := natsx.Shared(ctx, deps.Services, deps.Environ, logger)
	if err != nil {
		db.Close()
		return nil, err
	}
	m := &mod{db: db, logger: logger, migrate: cfg.Migrate, customers: customers, bus: bus, every: cfg.CustomerLifecycleEvery}
	return natsx.WithRelay(ctx, deps, m, db, migrations.Schema)
}

// loadSealer carga la KEK de credenciales (efímera solo en dev).
func loadSealer(cfg modcfg.Config, dev bool, logger *slog.Logger) (*envelope.Sealer, error) {
	if cfg.KEK.IsZero() {
		if !dev {
			return nil, errors.New("devices: HORUS_DEVICES_KEK_FILE is required outside dev")
		}
		logger.Warn("devices: ephemeral KEK (dev): router credentials become unreadable on restart; set HORUS_DEVICES_KEK_FILE")
		return envelope.Ephemeral(), nil
	}
	kek, err := envelope.ParseKEK(cfg.KEK.Reveal())
	if err != nil {
		return nil, fmt.Errorf("devices: HORUS_DEVICES_KEK_FILE: %w", err)
	}
	return envelope.New(kek)
}

func (m *mod) Start(ctx context.Context) error {
	if err := m.db.Ping(ctx); err != nil {
		return err
	}
	if !m.migrate {
		return nil
	}
	n, err := pgdb.Migrate(ctx, m.db, migrations.Schema, migrations.Postgres(), m.logger)
	if err != nil {
		return err
	}
	m.logger.InfoContext(ctx, "devices migrations applied", slog.Int("count", n))
	return nil
}

// Run consume los lotes de descubrimiento y actividad de clientes (si hay
// NATS) y ejecuta el ciclo de vida diario (inactivación y purga).
func (m *mod) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); m.customers.RunLifecycleDaily(ctx, m.every) }()
	if m.bus != nil {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = natsx.Consume(ctx, m.bus.JS, natsx.ConsumerConfig{Service: Role, Stream: "FLOWS_EVENTS", Durable: m.bus.Durable(app.DurableFirstSeen),
				FilterSubjects: []string{"horus.flows.client.first_seen.>"}, Logger: m.logger}, m.customers.HandleFirstSeen)
		}()
		go func() {
			defer wg.Done()
			_ = natsx.Consume(ctx, m.bus.JS, natsx.ConsumerConfig{Service: Role, Stream: "TLM_FLOWS", Durable: m.bus.Durable(app.DurableActivity),
				FilterSubjects: []string{"horus.telemetry.flows.client_activity.>"}, Encoding: natsx.EncodingTelemetry,
				MaxDeliver: 5, BackOff: []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 5 * time.Minute},
				AckWait: time.Minute, MaxAckPending: 1000, Logger: m.logger}, m.customers.HandleActivity)
		}()
	} else {
		m.logger.WarnContext(ctx, "customer discovery consumers inactive: HORUS_NATS_URL not set")
	}
	<-ctx.Done()
	wg.Wait()
	return nil
}

func (m *mod) Stop(context.Context) error {
	m.db.Close()
	return nil
}
