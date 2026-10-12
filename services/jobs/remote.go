package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"filippo.io/age"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/jobs/internal/remote"
	"github.com/hcdestroyer/horus-flow/services/jobs/migrations"
)

// remoteCopy es la copia remota de los backups (destinos SFTP y motor).
type remoteCopy struct {
	db      *pgdb.DB
	svc     *remote.Service
	migrate bool
	log     *slog.Logger
}

// newRemote prepara la copia remota. Sin HORUS_POSTGRES_DSN en desarrollo no
// hay copia remota (nil); fuera de desarrollo la DSN y la KEK son
// obligatorias. Sin clave age las copias no se hacen (los destinos se pueden
// configurar y probar) y se avisa en el log.
func newRemote(ctx context.Context, cfg Config, deps module.Deps, v *authz.Verifier, log *slog.Logger) (*remoteCopy, error) {
	dev := deps.Common.Env == "" || deps.Common.Env == config.EnvDev
	if cfg.PostgresDSN == "" {
		if !dev {
			return nil, errors.New("jobs: HORUS_POSTGRES_DSN is required")
		}
		log.WarnContext(ctx, "jobs: remote copy inactive: HORUS_POSTGRES_DSN not set (dev)")
		return nil, nil
	}
	var sealer *envelope.Sealer
	if cfg.KEK.IsZero() {
		if !dev {
			return nil, errors.New("jobs: HORUS_JOBS_KEK_FILE is required outside dev")
		}
		log.Warn("jobs: ephemeral KEK (dev): remote destination credentials become unreadable on restart; set HORUS_JOBS_KEK_FILE")
		sealer = envelope.Ephemeral()
	} else {
		kek, err := envelope.ParseKEK(cfg.KEK.Reveal())
		if err != nil {
			return nil, fmt.Errorf("jobs: HORUS_JOBS_KEK_FILE: %w", err)
		}
		if sealer, err = envelope.New(kek); err != nil {
			return nil, err
		}
	}
	var recipient age.Recipient
	missing := ""
	switch {
	case cfg.AgeRecipient != "":
		r, pub, err := remote.ParseAgeRecipient(cfg.AgeRecipient)
		if err != nil {
			return nil, fmt.Errorf("jobs: HORUS_BACKUP_AGE_RECIPIENT_FILE: %w", err)
		}
		recipient = r
		log.Info("jobs: remote copies encrypted with age", slog.String("recipient", pub))
	case !cfg.AgeIdentity.IsZero():
		r, pub, err := remote.ParseAgeRecipient(cfg.AgeIdentity.Reveal())
		if err != nil {
			return nil, fmt.Errorf("jobs: HORUS_BACKUP_AGE_IDENTITY_FILE: %w", err)
		}
		recipient = r
		log.Info("jobs: remote copies encrypted with age", slog.String("recipient", pub))
	case dev:
		id, err := age.GenerateX25519Identity()
		if err != nil {
			return nil, err
		}
		recipient = id.Recipient()
		log.Warn("jobs: ephemeral age key (dev): remote copies cannot be decrypted after restart; set HORUS_BACKUP_AGE_RECIPIENT_FILE")
	default:
		missing = "falta HORUS_BACKUP_AGE_RECIPIENT_FILE (install.sh genera backup_age_identity y backup_age_recipient)"
		log.Warn("jobs: remote copy disabled: HORUS_BACKUP_AGE_RECIPIENT_FILE not set")
	}
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: cfg.PostgresDSN, Password: cfg.PostgresPassword.Reveal(),
		AppRole: cfg.AppRole, PlatformRole: cfg.PlatformRole, MaxConns: 4})
	if err != nil {
		return nil, err
	}
	svc, err := remote.NewService(remote.Options{
		Repo: remote.NewStore(db), Sealer: sealer, Recipient: recipient, RecipientMissing: missing, Root: cfg.StoreDir,
		Interval: cfg.SyncInterval, Settle: cfg.SyncSettle, LagAfter: cfg.SyncLagAfter, Logger: log, Metrics: deps.Metrics,
		Audit: func() (authapi.AuditRecorder, bool) {
			return module.Lookup[authapi.AuditRecorder](deps.Services, authapi.ServiceAudit)
		},
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("%s remote: %w", Role, err)
	}
	if deps.Health != nil {
		deps.Health.AddCheck(health.Check{Name: "postgres", Critical: true, Probe: db.Ping})
	}
	if deps.Routes != nil {
		if v == nil {
			log.Warn("jobs: sin verificador de tokens: /platform/remote-destinations no se sirve")
		} else {
			remote.NewHandler(svc, authz.NewGuard(v), cfg.PublicBaseURL, log).Mount(deps.Routes)
		}
	}
	return &remoteCopy{db: db, svc: svc, migrate: cfg.Migrate, log: log}, nil
}

func (r *remoteCopy) start(ctx context.Context) error {
	if err := r.db.Ping(ctx); err != nil {
		return err
	}
	if !r.migrate {
		return nil
	}
	n, err := pgdb.Migrate(ctx, r.db, migrations.Schema, migrations.Postgres(), r.log)
	if err != nil {
		return err
	}
	r.log.InfoContext(ctx, "jobs migrations applied", slog.Int("count", n))
	return nil
}
