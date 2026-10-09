// Package detection es el módulo del rol detection de `horus` (ADR-0025,
// docs/services.md, ADR-0024): reputación, motor de detección de botnets
// sobre ClickHouse, hallazgos con ciclo de vida y su API.
//
// Al arrancar aplica las migraciones del esquema `detection`. Las rutas
// revalidan el access JWT con el validador de auth (module.Services) o con
// HORUS_JWT_PUBLIC_KEYS. Con HORUS_CLICKHOUSE_DSN el motor evalúa cada
// HORUS_DETECTION_INTERVAL a los tenants registrados (y HORUS_DETECTION_TENANTS).
// Sin HORUS_POSTGRES_DSN, en desarrollo el rol queda inactivo con un aviso.
package detection

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/detection/api/securitywidgets"
	chreader "github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/clickhouse"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/feeds"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/httpapi"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/postgres"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/repsnap"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/app"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/app/feedsync"
	modcfg "github.com/hcdestroyer/horus-flow/services/detection/internal/config"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/engine"
	"github.com/hcdestroyer/horus-flow/services/detection/migrations"
)

// Role es el nombre del rol en HORUS_ROLES: reputación, correlación, hallazgos y scoring.
const Role = "detection"

type mod struct {
	feeds   *feedsync.Scheduler
	cfg     modcfg.Config
	db      *pgdb.DB
	ch      *chreader.Reader
	store   *postgres.Store
	engine  *engine.Engine
	tenants []uuid.UUID
	logger  *slog.Logger
}

// Register construye el módulo del rol detection (firma module.Factory).
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
			return nil, errors.New("detection: HORUS_POSTGRES_DSN is required")
		}
		logger.WarnContext(ctx, "detection inactive: HORUS_POSTGRES_DSN not set (dev)")
		return module.Idle(), nil
	}
	verifier, ok := module.Lookup[*authz.Verifier](deps.Services, authapi.ServiceVerifier)
	if !ok {
		if cfg.PublicKeys == "" {
			return nil, errors.New("detection: no token verifier (auth role not local and HORUS_JWT_PUBLIC_KEYS unset)")
		}
		keys, err := authz.ParsePublicKeysPEM([]byte(cfg.PublicKeys))
		if err != nil {
			return nil, fmt.Errorf("detection: HORUS_JWT_PUBLIC_KEYS: %w", err)
		}
		verifier = authz.NewVerifier(keys, cfg.Issuer, nil)
	}
	var tenants []uuid.UUID
	for _, s := range cfg.Tenants {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("detection: HORUS_DETECTION_TENANTS: %w", err)
		}
		tenants = append(tenants, id)
	}
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: cfg.PostgresDSN, Password: cfg.PostgresPassword.Reveal(),
		AppRole: cfg.AppRole, PlatformRole: cfg.PlatformRole, MaxConns: 10})
	if err != nil {
		return nil, err
	}
	m := &mod{cfg: cfg, db: db, store: postgres.New(db), tenants: tenants, logger: logger}
	opts := app.Options{Cursor: pagination.NewCodec([]byte(cfg.CursorKey.Reveal())), Registry: m.store, Logger: logger}
	if audit, ok := module.Lookup[authapi.AuditRecorder](deps.Services, authapi.ServiceAudit); ok {
		opts.Audit = audit
	}
	opts.Kiosks = func() (authapi.KioskChecker, bool) {
		return module.Lookup[authapi.KioskChecker](deps.Services, authapi.ServiceKiosks)
	}
	if cfg.ClickHouseDSN != "" {
		m.ch, err = chreader.Open(cfg.ClickHouseDSN, cfg.ClickHouseUser, cfg.ClickHousePassword.Reveal(), cfg.ClickHouseTimeout)
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("detection: clickhouse: %w", err)
		}
		opts.Flows = m.ch
	}
	svc := app.NewService(db, opts)
	m.feeds = newFeeds(cfg, logger)
	svc.SetSources(m.feeds)
	snapDir := cfg.ReputationSnapshotDir
	if snapDir == "" {
		snapDir = m.feeds.SnapshotDir()
	}
	if m.ch != nil && cfg.Engine {
		m.engine = engine.New(m.ch, svc, repsnap.New(snapDir, time.Minute, logger), engine.Options{Lag: cfg.Lag, Logger: logger})
	}
	if deps.Health != nil {
		deps.Health.AddCheck(health.Check{Name: "postgres", Critical: true, Probe: db.Ping})
		if m.ch != nil {
			deps.Health.AddCheck(health.Check{Name: "clickhouse", Critical: false, Probe: m.ch.Ping})
		}
	}
	if deps.Services != nil {
		// Datos de los widgets de seguridad para analytics/dashboards (C9).
		if err := deps.Services.Provide(securitywidgets.ServiceWidgetData, securitywidgets.Provider(svc.Widgets())); err != nil {
			db.Close()
			return nil, err
		}
	}
	if deps.Routes != nil {
		httpapi.New(svc, authz.NewGuard(verifier), cfg.PublicBaseURL, logger).Mount(deps.Routes)
	}
	return natsx.WithRelay(ctx, deps, m, db, migrations.Schema)
}

func (m *mod) Start(ctx context.Context) error {
	if err := m.db.Ping(ctx); err != nil {
		return err
	}
	if !m.cfg.Migrate {
		return nil
	}
	n, err := pgdb.Migrate(ctx, m.db, migrations.Schema, migrations.Postgres(), m.logger)
	if err != nil {
		return err
	}
	m.logger.InfoContext(ctx, "detection migrations applied", slog.Int("count", n))
	return nil
}

// newFeeds construye la sincronización de feeds del rol (catálogo + listas
// personalizadas → snapshot en $HORUS_DATA_DIR/catalog/reputation).
func newFeeds(cfg modcfg.Config, log *slog.Logger) *feedsync.Scheduler {
	hf := datasets.NewHTTPFetcher("horus-flow (+reputation feeds)", 5*time.Minute)
	hf.Guard = &datasets.EgressGuard{Deny: datasets.InstallationPrefixes()}
	var f datasets.Fetcher = hf
	if cfg.FeedsFixtures != "" {
		f = datasets.DirFetcher{Dir: cfg.FeedsFixtures}
	}
	return &feedsync.Scheduler{ConfigPath: cfg.FeedsConfig, CustomPath: cfg.FeedsCustom, DataDir: cfg.DataDir, Log: log,
		Service: &feedsync.Service{Store: &datasets.Store{Root: datasets.DatasetsDir(cfg.DataDir), Tool: "horus/detection"},
			Fetcher: f, Parse: feeds.EntriesFromFile, AllowUnverified: cfg.AllowUnverified}}
}

// Run sincroniza los feeds (si está activado) y evalúa periódicamente a los
// tenants (planificador del motor).
func (m *mod) Run(ctx context.Context) error {
	if m.cfg.FeedsSync {
		go m.syncFeeds(ctx)
	}
	if m.engine == nil {
		<-ctx.Done()
		return nil
	}
	t := time.NewTicker(m.cfg.Interval)
	defer t.Stop()
	for {
		m.tick(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func (m *mod) syncFeeds(ctx context.Context) {
	t := time.NewTicker(m.cfg.FeedsInterval)
	defer t.Stop()
	for {
		if _, err := m.feeds.Sync(ctx, time.Now()); err != nil && ctx.Err() == nil {
			m.logger.WarnContext(ctx, "detection: reputation feeds sync", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (m *mod) tick(ctx context.Context) {
	tenants, err := m.store.Tenants(ctx)
	if err != nil {
		m.logger.WarnContext(ctx, "detection: list tenants", "err", err)
	}
	for _, id := range m.tenants {
		if !slices.Contains(tenants, id) {
			tenants = append(tenants, id)
		}
	}
	for _, id := range tenants {
		if ctx.Err() != nil {
			return
		}
		rep, err := m.engine.Evaluate(ctx, id, time.Now(), false)
		if err != nil {
			m.logger.WarnContext(ctx, "detection: evaluate", "tenant", id.String(), "err", err)
		}
		if rep != nil && (rep.Applied.Opened > 0 || rep.Applied.Updated > 0 || rep.Expired > 0) {
			m.logger.InfoContext(ctx, "detection: findings", "tenant", id.String(), "opened", rep.Applied.Opened,
				"updated", rep.Applied.Updated, "expired", rep.Expired, "silenced", rep.Applied.Silenced)
		}
	}
}

func (m *mod) Stop(context.Context) error {
	if m.ch != nil {
		_ = m.ch.Close()
	}
	m.db.Close()
	return nil
}

// EvaluateReport resume una evaluación del motor.
type EvaluateReport struct {
	Candidates                           int
	Opened, Updated, Unchanged, Silenced int
	Expired                              int
	Skipped                              map[string]int
}

// ErrNoEngine indica un módulo sin motor (sin ClickHouse o con
// HORUS_DETECTION_ENGINE=false).
var ErrNoEngine = errors.New("detection: engine not configured")

// Evaluate ejecuta el motor del módulo m (de Register) para tenant en now con
// todos los detectores, sin mirar su cadencia. Lo usan las pruebas de
// integración y de aceptación (escenarios del simulador) para evaluar en un
// instante controlado.
func Evaluate(ctx context.Context, m module.Module, tenant uuid.UUID, now time.Time) (EvaluateReport, error) {
	md, ok := m.(*mod)
	if !ok || md.engine == nil {
		return EvaluateReport{}, ErrNoEngine
	}
	rep, err := md.engine.Evaluate(ctx, tenant, now, true)
	if rep == nil {
		return EvaluateReport{}, err
	}
	return EvaluateReport{Candidates: len(rep.Candidates), Opened: rep.Applied.Opened, Updated: rep.Applied.Updated,
		Unchanged: rep.Applied.Unchanged, Silenced: rep.Applied.Silenced, Expired: rep.Expired, Skipped: rep.Skipped}, err
}
