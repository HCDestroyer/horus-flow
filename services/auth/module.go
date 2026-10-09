// Package auth es el módulo del rol auth de `horus` (ADR-0025,
// docs/services.md): identidad, tenants (ISP), membresías, roles, sesiones,
// tokens y auditoría.
//
// Al arrancar aplica las migraciones del esquema `auth`, sincroniza el
// catálogo de permisos C7 y crea el superadministrador semilla si se
// configuró (HORUS_SEED_ADMIN_EMAIL + HORUS_SEED_ADMIN_PASSWORD_FILE).
// Registra en module.Services el validador de tokens, la comprobación de
// sesiones y la auditoría para el gateway y los demás módulos locales.
//
// Sin HORUS_POSTGRES_DSN, en desarrollo el rol queda inactivo con un aviso;
// en staging/prod es un error de configuración.
package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/adapters/httpapi"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/adapters/postgres"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/app"
	modcfg "github.com/hcdestroyer/horus-flow/services/auth/internal/config"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/auth/migrations"
)

// Role es el nombre del rol en HORUS_ROLES: identidad, tenants, membresías, roles, sesiones y auditoría.
const Role = "auth"

type mod struct {
	db      *pgdb.DB
	svc     *app.Service
	cfg     modcfg.Config
	logger  *slog.Logger
	migrate bool
}

// Register construye el módulo del rol auth (firma module.Factory).
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
			return nil, errors.New("auth: HORUS_POSTGRES_DSN is required")
		}
		logger.WarnContext(ctx, "auth inactive: HORUS_POSTGRES_DSN not set (dev)")
		return module.Idle(), nil
	}
	signKey, err := loadSigningKey(cfg, dev, logger)
	if err != nil {
		return nil, err
	}
	kek, err := loadKEK(cfg, dev, logger)
	if err != nil {
		return nil, err
	}
	sealer, err := domain.NewSealer(kek)
	if err != nil {
		return nil, err
	}
	catalog, err := domain.LoadCatalog()
	if err != nil {
		return nil, err
	}
	keys := authz.KeySet{}
	keys.Add(signKey.Public().(ed25519.PublicKey)) //nolint:forcetypeassert // Ed25519 siempre
	if cfg.PreviousPublicKeys != "" {
		prev, err := authz.ParsePublicKeysPEM([]byte(cfg.PreviousPublicKeys))
		if err != nil {
			return nil, fmt.Errorf("auth: HORUS_AUTH_PREVIOUS_PUBLIC_KEYS: %w", err)
		}
		for kid, k := range prev {
			keys[kid] = k
		}
	}
	verifier := authz.NewVerifier(keys, cfg.Issuer, nil)
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: cfg.PostgresDSN, Password: cfg.PostgresPassword.Reveal(),
		AppRole: cfg.AppRole, PlatformRole: cfg.PlatformRole, MaxConns: 10})
	if err != nil {
		return nil, err
	}
	svc, err := app.NewService(postgres.New(db), app.Options{
		Signer: authz.NewSigner(signKey, cfg.Issuer, cfg.AccessTTL, nil), Sealer: sealer, Catalog: catalog,
		Argon:       domain.Argon2Params{Memory: cfg.Argon2MemoryKiB, Time: cfg.Argon2Time, Threads: 1, SaltLen: 16, KeyLen: 32},
		RefreshIdle: cfg.RefreshIdle, SessionMax: cfg.SessionMaxAge, Logger: logger,
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	for name, v := range map[string]any{api.ServiceSessions: svc, api.ServiceVerifier: verifier, api.ServiceAudit: svc} {
		if deps.Services == nil {
			break
		}
		if err := deps.Services.Provide(name, v); err != nil {
			db.Close()
			return nil, err
		}
	}
	if deps.Health != nil {
		deps.Health.AddCheck(health.Check{Name: "postgres", Critical: true, Probe: db.Ping})
	}
	if deps.Routes != nil {
		httpapi.New(svc, authz.NewGuard(verifier), httpapi.Options{
			Origins: authz.ParseOrigins(cfg.AllowedOrigins, cfg.PublicBaseURL), Cursor: pagination.NewCodec(sealer.MAC("cursor", nil)),
			PublicBaseURL: cfg.PublicBaseURL, Logger: logger,
		}).Mount(deps.Routes)
	}
	logger.InfoContext(ctx, "auth configured", slog.Any("auth", cfg), slog.String("postgres", pgdb.RedactDSN(cfg.PostgresDSN)))
	return natsx.WithRelay(ctx, deps, &mod{db: db, svc: svc, cfg: cfg, logger: logger, migrate: cfg.Migrate}, db, migrations.Schema)
}

func (m *mod) Start(ctx context.Context) error {
	if err := m.db.Ping(ctx); err != nil {
		return err
	}
	if m.migrate {
		n, err := pgdb.Migrate(ctx, m.db, migrations.Schema, migrations.Postgres(), m.logger)
		if err != nil {
			return err
		}
		m.logger.InfoContext(ctx, "auth migrations applied", slog.Int("count", n))
	}
	return m.svc.Bootstrap(ctx, m.cfg.SeedAdminEmail, m.cfg.SeedAdminPassword.Reveal(), m.cfg.SeedAdminName)
}

func (m *mod) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (m *mod) Stop(context.Context) error {
	m.db.Close()
	return nil
}

func loadSigningKey(cfg modcfg.Config, dev bool, logger *slog.Logger) (ed25519.PrivateKey, error) {
	if !cfg.SigningKey.IsZero() {
		k, err := authz.ParsePrivateKeyPEM([]byte(cfg.SigningKey.Reveal()))
		if err != nil {
			return nil, fmt.Errorf("auth: HORUS_AUTH_SIGNING_KEY_FILE: %w", err)
		}
		return k, nil
	}
	if !dev {
		return nil, errors.New("auth: HORUS_AUTH_SIGNING_KEY_FILE is required outside dev")
	}
	logger.Warn("auth: ephemeral signing key (dev): tokens die on restart; set HORUS_AUTH_SIGNING_KEY_FILE")
	return authz.GenerateKey()
}

func loadKEK(cfg modcfg.Config, dev bool, logger *slog.Logger) ([]byte, error) {
	if !cfg.KEK.IsZero() {
		v := strings.TrimSpace(cfg.KEK.Reveal())
		if b, err := hex.DecodeString(v); err == nil && len(b) == 32 {
			return b, nil
		}
		if b, err := base64.StdEncoding.DecodeString(v); err == nil && len(b) == 32 {
			return b, nil
		}
		return nil, errors.New("auth: HORUS_AUTH_KEK_FILE must hold 32 bytes (hex or base64)")
	}
	if !dev {
		return nil, errors.New("auth: HORUS_AUTH_KEK_FILE is required outside dev")
	}
	logger.Warn("auth: ephemeral KEK (dev): TOTP secrets become unreadable on restart; set HORUS_AUTH_KEK_FILE")
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return b, err
}
