// Package reporting es el rol reporting de `horus` (módulo analytics, ADR-0025;
// docs/services.md §analytics): reporte semanal de seguridad por ISP en PDF y
// CSV (I3), programado por ISP y generable a petición.
//
//   - API: /api/v1/reports (listado, generación a petición → 202 + Location,
//     detalle, descarga autenticada y auditada) y /api/v1/report-definitions
//     (programación semanal). Solo se descarga por la API: sin URLs firmadas.
//   - Datos: hallazgos y estado de seguridad (detection, contrato en proceso
//     securityreport), disponibilidad de routers y tráfico (agregados de
//     ClickHouse que ya lee analytics) y avisos enviados (alerts, contrato
//     NotificationStats). Una fuente ausente deja su sección con una nota.
//   - Cola: la verdad es analytics.report_run (FOR UPDATE SKIP LOCKED); el
//     planificador encola una ejecución por definición y periodo (unicidad
//     parcial en PostgreSQL: seguro con varias réplicas).
//   - Archivos en $HORUS_DATA_DIR/reports/<tenant_id>/; retención
//     HORUS_REPORTS_RETENTION_DAYS (365).
//   - Eventos horus.reporting.report.completed|failed por el outbox de analytics.
package reporting

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/mailer"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/packages/go/observability"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	alertsapi "github.com/hcdestroyer/horus-flow/services/alerts/api"
	"github.com/hcdestroyer/horus-flow/services/analytics/dashboards"
	"github.com/hcdestroyer/horus-flow/services/analytics/internal/adapters/chread"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	secapi "github.com/hcdestroyer/horus-flow/services/detection/api/securityreport"
)

// Config del rol reporting (PostgreSQL y ClickHouse comunes con analytics).
type Config struct {
	PostgresDSN        string               `env:"HORUS_POSTGRES_DSN"`
	PostgresPassword   observability.Secret `env:"HORUS_POSTGRES_PASSWORD"`
	Migrate            bool                 `env:"HORUS_ANALYTICS_MIGRATE" envDefault:"true"`
	AppRole            string               `env:"HORUS_ANALYTICS_DB_APP_ROLE" envDefault:"analytics_app"`
	PlatformRole       string               `env:"HORUS_ANALYTICS_DB_PLATFORM_ROLE" envDefault:"analytics_platform"`
	CursorKey          observability.Secret `env:"HORUS_ANALYTICS_CURSOR_KEY"`
	PublicKeys         string               `env:"HORUS_JWT_PUBLIC_KEYS"`
	Issuer             string               `env:"HORUS_AUTH_ISSUER" envDefault:"horus-auth"`
	PublicBaseURL      string               `env:"HORUS_PUBLIC_BASE_URL"`
	DataDir            string               `env:"HORUS_DATA_DIR" envDefault:"/var/lib/horus/store"`
	RetentionDays      int                  `env:"HORUS_REPORTS_RETENTION_DAYS" envDefault:"365"`
	Worker             bool                 `env:"HORUS_REPORTS_WORKER" envDefault:"true"`
	ClickHouseDSN      string               `env:"HORUS_CLICKHOUSE_DSN"`
	ClickHousePassword observability.Secret `env:"HORUS_CLICKHOUSE_PASSWORD"`
	AnalyticsPassword  observability.Secret `env:"HORUS_CLICKHOUSE_ANALYTICS_PASSWORD"`
	QueryTimeout       time.Duration        `env:"HORUS_REPORTS_QUERY_TIMEOUT" envDefault:"60s"`
	SMTPHost           string               `env:"HORUS_SMTP_HOST"`
	SMTPPort           int                  `env:"HORUS_SMTP_PORT" envDefault:"587"`
	SMTPUsername       string               `env:"HORUS_SMTP_USERNAME"`
	SMTPPassword       observability.Secret `env:"HORUS_SMTP_PASSWORD"`
	SMTPFrom           string               `env:"HORUS_SMTP_FROM"`
	SMTPTLS            string               `env:"HORUS_SMTP_TLS" envDefault:"starttls"`
}

// Module es el rol en marcha.
type Module struct {
	cfg    Config
	db     *pgdb.DB
	ch     atomic.Pointer[chread.Reader]
	worker *Worker
	logger *slog.Logger
}

// New construye el rol reporting. Sin HORUS_POSTGRES_DSN en desarrollo
// queda inactivo con un aviso.
func New(ctx context.Context, deps module.Deps) (module.Module, error) {
	cfg, err := config.Load[Config](deps.Environ)
	if err != nil {
		return nil, fmt.Errorf("reporting config: %w", err)
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	dev := deps.Common.Env == "" || deps.Common.Env == config.EnvDev
	if cfg.PostgresDSN == "" {
		if !dev {
			return nil, errors.New("reporting: HORUS_POSTGRES_DSN is required")
		}
		logger.WarnContext(ctx, "reporting inactive: HORUS_POSTGRES_DSN not set (dev)")
		return module.Idle(), nil
	}
	if cfg.RetentionDays < 1 || cfg.RetentionDays > 3650 {
		return nil, fmt.Errorf("reporting: HORUS_REPORTS_RETENTION_DAYS must be between 1 and 3650: %d", cfg.RetentionDays)
	}
	verifier, ok := module.Lookup[*authz.Verifier](deps.Services, authapi.ServiceVerifier)
	if !ok {
		if cfg.PublicKeys == "" {
			return nil, errors.New("reporting: no token verifier (auth role not local and HORUS_JWT_PUBLIC_KEYS_FILE unset)")
		}
		keys, err := authz.ParsePublicKeysPEM([]byte(cfg.PublicKeys))
		if err != nil {
			return nil, fmt.Errorf("reporting: HORUS_JWT_PUBLIC_KEYS_FILE: %w", err)
		}
		verifier = authz.NewVerifier(keys, cfg.Issuer, nil)
	}
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: cfg.PostgresDSN, Password: cfg.PostgresPassword.Reveal(),
		AppRole: cfg.AppRole, PlatformRole: cfg.PlatformRole, MaxConns: 5})
	if err != nil {
		return nil, err
	}
	m := &Module{cfg: cfg, db: db, logger: logger}
	st := &Store{DB: db}
	files := Files{Root: cfg.DataDir}
	retention := time.Duration(cfg.RetentionDays) * 24 * time.Hour
	directory := func() (authapi.TenantDirectory, bool) {
		return module.Lookup[authapi.TenantDirectory](deps.Services, authapi.ServiceTenants)
	}
	smtp := mailer.Config{Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword.Reveal(),
		From: cfg.SMTPFrom, TLS: cfg.SMTPTLS}
	// Las fuentes se buscan en cada reporte: detection y alerts pueden
	// registrarse después de reporting (orden de HORUS_ROLES).
	collector := &Collector{
		Security: func() (secapi.Reader, bool) {
			return module.Lookup[secapi.Reader](deps.Services, secapi.ServiceSecurityReport)
		},
		Alerts: func() (alertsapi.NotificationStats, bool) {
			return module.Lookup[alertsapi.NotificationStats](deps.Services, alertsapi.ServiceNotificationStats)
		},
		Metrics: func() Metrics {
			if r := m.ch.Load(); r != nil {
				return CHMetrics{Q: r}
			}
			return nil
		},
		Now: time.Now, Logger: logger,
	}
	m.worker = &Worker{St: st, Files: files, Collector: collector, Directory: directory, Now: time.Now, Logger: logger,
		Retention: retention, SMTP: smtp, PublicBaseURL: cfg.PublicBaseURL}
	svc := &Service{St: st, Files: files, Cursor: pagination.NewCodec([]byte(cfg.CursorKey.Reveal())), Now: time.Now, Logger: logger,
		Audit: func() (authapi.AuditRecorder, bool) {
			return module.Lookup[authapi.AuditRecorder](deps.Services, authapi.ServiceAudit)
		},
		Directory: directory, Retention: retention, Wake: m.worker.Wake, EmailAvailable: smtp.Enabled()}
	if deps.Health != nil {
		deps.Health.AddCheck(health.Check{Name: "postgres", Critical: true, Probe: db.Ping})
	}
	if deps.Routes != nil {
		(&Handler{Svc: svc, Guard: authz.NewGuard(verifier), BaseURL: cfg.PublicBaseURL, Logger: logger}).Mount(deps.Routes)
	}
	inner, err := natsx.WithRelay(ctx, deps, m, db, Schema)
	if err != nil {
		db.Close()
		return nil, err
	}
	return inner, nil
}

// Start migra el esquema analytics (las mismas migraciones que dashboards;
// goose serializa con un lock de sesión si ambos roles arrancan a la vez) y
// abre ClickHouse si está configurado (caído: secciones con nota).
func (m *Module) Start(ctx context.Context) error {
	if err := m.db.Ping(ctx); err != nil {
		return err
	}
	if m.cfg.Migrate {
		n, err := pgdb.Migrate(ctx, m.db, Schema, dashboards.Migrations(), m.logger)
		if err != nil {
			return err
		}
		m.logger.InfoContext(ctx, "analytics migrations applied (reporting)", slog.Int("count", n))
	}
	if m.cfg.ClickHouseDSN == "" {
		m.logger.WarnContext(ctx, "reporting: HORUS_CLICKHOUSE_DSN not set: availability and traffic sections unavailable")
		return nil
	}
	user, pw := "", m.cfg.ClickHousePassword.Reveal()
	if m.cfg.AnalyticsPassword.Reveal() != "" {
		user, pw = chread.UserAnalytics, m.cfg.AnalyticsPassword.Reveal()
	}
	r, err := chread.Open(m.cfg.ClickHouseDSN, user, pw, m.cfg.QueryTimeout)
	if err != nil {
		return fmt.Errorf("reporting: clickhouse: %w", err)
	}
	m.ch.Store(r)
	return nil
}

// Run ejecuta el planificador, el generador y la retención.
func (m *Module) Run(ctx context.Context) error {
	if m.cfg.Worker {
		go m.worker.Run(ctx)
	} else {
		m.logger.InfoContext(ctx, "reporting: worker disabled (HORUS_REPORTS_WORKER=false): API only")
	}
	<-ctx.Done()
	return nil
}

// Stop cierra las conexiones.
func (m *Module) Stop(context.Context) error {
	if r := m.ch.Load(); r != nil {
		_ = r.Close()
	}
	m.db.Close()
	return nil
}
