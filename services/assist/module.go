// Package assist es el módulo del rol assist de `horus` (docs/assist.md): el
// asistente de IA de Horus. Analiza un hallazgo o un router con un modelo
// (Claude por la API de Anthropic, o un servidor compatible con OpenAI para un
// modelo local) que usa herramientas de solo lectura sobre datos enmascarados,
// y publica las mismas herramientas como servidor MCP.
//
// Las herramientas leen la API REST del propio proceso como el usuario que
// pide el análisis (adaptador loopback): necesita el rol gateway y los módulos
// de datos (detection, devices, wireguard, flows, analytics) en el mismo
// proceso, como en el perfil mínimo. Al arrancar aplica las migraciones del
// esquema `assist`. Sin HORUS_POSTGRES_DSN, en desarrollo queda inactivo.
package assist

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/adapters/httpapi"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/adapters/loopback"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/adapters/mcpserver"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/adapters/postgres"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/app"
	modcfg "github.com/hcdestroyer/horus-flow/services/assist/internal/config"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/provider"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/provider/anthropic"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/provider/openaicompat"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/tools"
	"github.com/hcdestroyer/horus-flow/services/assist/migrations"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
)

// Role es el nombre del rol en HORUS_ROLES: asistente de IA (análisis con herramientas y servidor MCP).
const Role = "assist"

type mod struct {
	cfg    modcfg.Config
	db     *pgdb.DB
	logger *slog.Logger
}

// Register construye el módulo del rol assist (firma module.Factory).
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
			return nil, errors.New("assist: HORUS_POSTGRES_DSN is required")
		}
		logger.WarnContext(ctx, "assist inactive: HORUS_POSTGRES_DSN not set (dev)")
		return module.Idle(), nil
	}
	verifier, ok := module.Lookup[*authz.Verifier](deps.Services, authapi.ServiceVerifier)
	if !ok {
		if cfg.PublicKeys == "" {
			return nil, errors.New("assist: no token verifier (auth role not local and HORUS_JWT_PUBLIC_KEYS unset)")
		}
		keys, err := authz.ParsePublicKeysPEM([]byte(cfg.PublicKeys))
		if err != nil {
			return nil, fmt.Errorf("assist: HORUS_JWT_PUBLIC_KEYS: %w", err)
		}
		verifier = authz.NewVerifier(keys, cfg.Issuer, nil)
	}
	sealer, err := loadSealer(cfg, dev, logger)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(domain.Providers, cfg.DefaultProvider) {
		return nil, fmt.Errorf("assist: HORUS_ASSIST_PROVIDER %q not supported", cfg.DefaultProvider)
	}
	registry, err := tools.NewRegistry(tools.Horus()...)
	if err != nil {
		return nil, err
	}
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: cfg.PostgresDSN, Password: cfg.PostgresPassword.Reveal(),
		AppRole: cfg.AppRole, PlatformRole: cfg.PlatformRole, MaxConns: 5})
	if err != nil {
		return nil, err
	}
	m := &mod{cfg: cfg, db: db, logger: logger}
	opts := app.Options{
		Store: postgres.New(db), Registry: registry, Providers: newProviderFactory(cfg), Logger: logger,
		Defaults: app.Defaults{Enabled: cfg.DefaultEnabled, Provider: cfg.DefaultProvider, Model: cfg.DefaultModel,
			BaseURL: cfg.DefaultBaseURL, APIKey: cfg.DefaultAPIKey.Reveal(), Effort: cfg.DefaultEffort, Limit: cfg.DefaultLimit},
		MaxIterations: cfg.MaxIterations, RequestTimeout: cfg.RequestTimeout, MaxOutputTokens: cfg.MaxOutputTokens,
		MCPTokenTTL: cfg.MCPTokenTTL, HistoryRetention: cfg.HistoryRetention,
	}
	if sealer != nil {
		opts.Sealer = sealer
	}
	if audit, ok := module.Lookup[authapi.AuditRecorder](deps.Services, authapi.ServiceAudit); ok {
		opts.Audit = audit
	}
	if cfg.MCP {
		if d, ok := module.Lookup[authapi.Delegator](deps.Services, authapi.ServiceDelegation); ok {
			opts.Delegation = d
		} else {
			logger.WarnContext(ctx, "assist: MCP server unavailable (auth role not in this process)")
		}
	}
	if deps.Routes != nil {
		loop := deps.Routes.Loopback()
		opts.Readers = func(p *authz.Principal) tools.Reader { return loopback.New(loop, p.Raw) }
	}
	svc := app.New(opts)
	if deps.Health != nil {
		deps.Health.AddCheck(health.Check{Name: "postgres", Critical: true, Probe: db.Ping})
	}
	if deps.Routes != nil {
		httpapi.New(svc, authz.NewGuard(verifier), logger).Mount(deps.Routes)
		mcpserver.New(svc, logger).Mount(deps.Routes)
	}
	logger.InfoContext(ctx, "assist configured", slog.Any("config", cfg))
	return m, nil
}

// newProviderFactory construye los adaptadores según la configuración efectiva de cada ISP.
func newProviderFactory(cfg modcfg.Config) app.ProviderFactory {
	deny := datasets.InstallationPrefixes()
	return func(c app.ProviderConfig) (provider.Provider, error) {
		switch c.Provider {
		case domain.ProviderAnthropic:
			return anthropic.New(anthropic.Options{APIKey: c.APIKey, Model: c.Model, Effort: c.Effort, Timeout: cfg.RequestTimeout}), nil
		case domain.ProviderOpenAICompatible:
			if err := domain.ValidateBaseURL(c.BaseURL); err != nil {
				return nil, fmt.Errorf("assist: base_url: %w", err)
			}
			return openaicompat.New(openaicompat.Options{BaseURL: c.BaseURL, APIKey: c.APIKey, Model: c.Model,
				Timeout: cfg.RequestTimeout, Deny: deny, AllowLoopback: cfg.AllowLoopback}), nil
		default:
			return nil, fmt.Errorf("assist: provider %q not supported", c.Provider)
		}
	}
}

// loadSealer carga la KEK de las claves de API (efímera solo en dev).
func loadSealer(cfg modcfg.Config, dev bool, logger *slog.Logger) (*envelope.Sealer, error) {
	if cfg.KEK.IsZero() {
		if dev {
			logger.Warn("assist: ephemeral KEK (dev): stored API keys become unreadable on restart; set HORUS_ASSIST_KEK_FILE")
			return envelope.Ephemeral(), nil
		}
		logger.Warn("assist: HORUS_ASSIST_KEK_FILE not set: per-ISP API keys cannot be stored (the platform key still works)")
		return nil, nil
	}
	kek, err := envelope.ParseKEK(cfg.KEK.Reveal())
	if err != nil {
		return nil, fmt.Errorf("assist: HORUS_ASSIST_KEK_FILE: %w", err)
	}
	return envelope.New(kek)
}

// Start aplica las migraciones del esquema assist.
func (m *mod) Start(ctx context.Context) error {
	if err := m.db.Ping(ctx); err != nil {
		return err
	}
	if m.cfg.Migrate {
		n, err := pgdb.Migrate(ctx, m.db, migrations.Schema, migrations.Postgres(), m.logger)
		if err != nil {
			return err
		}
		m.logger.InfoContext(ctx, "assist migrations applied", slog.Int("count", n))
	}
	return nil
}

// Run espera al apagado (el módulo solo sirve rutas HTTP).
func (m *mod) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// Stop cierra el pool.
func (m *mod) Stop(context.Context) error {
	m.db.Close()
	return nil
}
