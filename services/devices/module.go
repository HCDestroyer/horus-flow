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

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/adapters/httpapi"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/adapters/postgres"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	modcfg "github.com/hcdestroyer/horus-flow/services/devices/internal/config"
	"github.com/hcdestroyer/horus-flow/services/devices/migrations"
)

// Role es el nombre del rol en HORUS_ROLES: nodos, routers, realms, credenciales y clientes.
const Role = "devices"

type mod struct {
	db      *pgdb.DB
	logger  *slog.Logger
	migrate bool
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
	svc := app.NewService(postgres.New(db), pagination.NewCodec([]byte(cfg.CursorKey.Reveal())), nil)
	if deps.Health != nil {
		deps.Health.AddCheck(health.Check{Name: "postgres", Critical: true, Probe: db.Ping})
	}
	if deps.Routes != nil {
		httpapi.New(svc, authz.NewGuard(verifier), cfg.PublicBaseURL, logger).Mount(deps.Routes)
	}
	return &mod{db: db, logger: logger, migrate: cfg.Migrate}, nil
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

func (m *mod) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (m *mod) Stop(context.Context) error {
	m.db.Close()
	return nil
}
