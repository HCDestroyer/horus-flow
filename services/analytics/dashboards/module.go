package dashboards

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/packages/go/observability"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	dashapi "github.com/hcdestroyer/horus-flow/services/analytics/api/dashboards"
	tw "github.com/hcdestroyer/horus-flow/services/analytics/api/trafficwidgets"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
)

//go:embed migrations/postgres/*.sql
var migrationFiles embed.FS

// Migrations devuelve las migraciones del esquema analytics.
func Migrations() fs.FS {
	sub, err := fs.Sub(migrationFiles, "migrations/postgres")
	if err != nil {
		panic(err) //nolint:forbidigo // imposible: el directorio está embebido
	}
	return sub
}

// Config del submódulo (HORUS_ANALYTICS_*; PostgreSQL común).
type Config struct {
	PostgresDSN      string               `env:"HORUS_POSTGRES_DSN"`
	PostgresPassword observability.Secret `env:"HORUS_POSTGRES_PASSWORD"`
	Migrate          bool                 `env:"HORUS_ANALYTICS_MIGRATE" envDefault:"true"`
	AppRole          string               `env:"HORUS_ANALYTICS_DB_APP_ROLE" envDefault:"analytics_app"`
	PlatformRole     string               `env:"HORUS_ANALYTICS_DB_PLATFORM_ROLE" envDefault:"analytics_platform"`
	CursorKey        observability.Secret `env:"HORUS_ANALYTICS_CURSOR_KEY"`
	PublicKeys       string               `env:"HORUS_JWT_PUBLIC_KEYS"`
	Issuer           string               `env:"HORUS_AUTH_ISSUER" envDefault:"horus-auth"`
	PublicBaseURL    string               `env:"HORUS_PUBLIC_BASE_URL"`
}

// Module es el submódulo en marcha (lo compone el módulo analytics).
type Module struct {
	db      *pgdb.DB
	svc     *Service
	migrate bool
	logger  *slog.Logger
	inner   module.Module
}

// New construye el submódulo: rutas de dashboards, playlists, catálogo y
// kiosco, y el contrato dashboardsapi.Access. Sin HORUS_POSTGRES_DSN en dev
// devuelve nil (inactivo).
func New(ctx context.Context, deps module.Deps) (*Module, error) {
	cfg, err := config.Load[Config](deps.Environ)
	if err != nil {
		return nil, fmt.Errorf("dashboards config: %w", err)
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	dev := deps.Common.Env == "" || deps.Common.Env == config.EnvDev
	if cfg.PostgresDSN == "" {
		if !dev {
			return nil, errors.New("dashboards: HORUS_POSTGRES_DSN is required")
		}
		logger.WarnContext(ctx, "dashboards inactive: HORUS_POSTGRES_DSN not set (dev)")
		return nil, nil
	}
	catalog, err := LoadCatalog()
	if err != nil {
		return nil, err
	}
	verifier, ok := module.Lookup[*authz.Verifier](deps.Services, authapi.ServiceVerifier)
	if !ok {
		if cfg.PublicKeys == "" {
			return nil, errors.New("dashboards: no token verifier (auth role not local and HORUS_JWT_PUBLIC_KEYS_FILE unset)")
		}
		keys, err := authz.ParsePublicKeysPEM([]byte(cfg.PublicKeys))
		if err != nil {
			return nil, fmt.Errorf("dashboards: HORUS_JWT_PUBLIC_KEYS_FILE: %w", err)
		}
		verifier = authz.NewVerifier(keys, cfg.Issuer, nil)
	}
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: cfg.PostgresDSN, Password: cfg.PostgresPassword.Reveal(),
		AppRole: cfg.AppRole, PlatformRole: cfg.PlatformRole, MaxConns: 10})
	if err != nil {
		return nil, err
	}
	svc := &Service{
		st: &store{db: db}, catalog: catalog, cursor: pagination.NewCodec([]byte(cfg.CursorKey.Reveal())), now: time.Now, logger: logger,
		kiosks: func() (authapi.KioskChecker, bool) {
			return module.Lookup[authapi.KioskChecker](deps.Services, authapi.ServiceKiosks)
		},
		widgets: func() (tw.Provider, bool) { return module.Lookup[tw.Provider](deps.Services, tw.ServiceWidgetData) },
	}
	if deps.Services != nil {
		if err := deps.Services.Provide(dashapi.ServiceAccess, dashapi.Access(svc)); err != nil {
			db.Close()
			return nil, err
		}
	}
	if deps.Health != nil {
		deps.Health.AddCheck(health.Check{Name: "postgres", Critical: true, Probe: db.Ping})
	}
	if deps.Routes != nil {
		(&handler{svc: svc, guard: authz.NewGuard(verifier), baseURL: cfg.PublicBaseURL, logger: logger}).mount(deps.Routes)
	}
	m := &Module{db: db, svc: svc, migrate: cfg.Migrate, logger: logger}
	inner, err := natsx.WithRelay(ctx, deps, module.Idle(), db, Schema)
	if err != nil {
		db.Close()
		return nil, err
	}
	m.inner = inner
	return m, nil
}

// Templates devuelve las plantillas de sistema embebidas.
func Templates() ([]Dashboard, error) {
	var out []Dashboard
	for _, name := range []string{"noc-isp.json", "security.json"} {
		b, err := contractFS.ReadFile("contract/templates/" + name)
		if err != nil {
			return nil, fmt.Errorf("dashboards: template %s: %w", name, err)
		}
		var doc struct {
			ID              uuid.UUID `json:"id"`
			Name            string    `json:"name"`
			TemplateKey     *string   `json:"template_key"`
			TemplateVersion *int      `json:"template_version"`
			Layout          Layout    `json:"layout"`
			DefaultRange    string    `json:"default_range"`
			RefreshSeconds  int       `json:"refresh_seconds"`
			Variables       Variables `json:"variables"`
			Widgets         []Widget  `json:"widgets"`
			Version         int       `json:"version"`
			CreatedAt       time.Time `json:"created_at"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			return nil, fmt.Errorf("dashboards: template %s: %w", name, err)
		}
		out = append(out, Dashboard{ID: doc.ID, Name: doc.Name, Visibility: VisSystem, TemplateKey: doc.TemplateKey,
			TemplateVersion: doc.TemplateVersion, Layout: doc.Layout, DefaultRange: doc.DefaultRange, RefreshSeconds: doc.RefreshSeconds,
			Variables: doc.Variables, Widgets: doc.Widgets, Version: doc.Version, CreatedAt: doc.CreatedAt, UpdatedAt: doc.CreatedAt})
	}
	return out, nil
}

// Start migra el esquema y siembra las plantillas (cada config valida contra
// el config_schema de su tipo; I1-15 criterio 1).
func (m *Module) Start(ctx context.Context) error {
	if err := m.db.Ping(ctx); err != nil {
		return err
	}
	if m.migrate {
		n, err := pgdb.Migrate(ctx, m.db, Schema, Migrations(), m.logger)
		if err != nil {
			return err
		}
		m.logger.InfoContext(ctx, "analytics migrations applied", slog.Int("count", n))
	}
	tpls, err := Templates()
	if err != nil {
		return err
	}
	for i := range tpls {
		if err := validateDocument(m.svc.catalog, &tpls[i]); err != nil {
			return fmt.Errorf("dashboards: template %s: %w", tpls[i].Name, err)
		}
	}
	return m.svc.st.seedTemplates(ctx, tpls)
}

// Run publica el outbox mientras corre el proceso.
func (m *Module) Run(ctx context.Context) error { return m.inner.Run(ctx) }

// Stop cierra la base.
func (m *Module) Stop(context.Context) error {
	m.db.Close()
	return nil
}
