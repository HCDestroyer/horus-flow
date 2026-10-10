// Package alerts es el módulo del rol alerts de `horus` (ADR-0025,
// docs/services.md; dueño CORE por D21).
//
// I1 (D13/D17): canal mínimo de notificaciones por ISP — email (SMTP de la
// instalación, HORUS_SMTP_*), Telegram (bot de la instalación o propio) y
// LibreNMS por su API — con secretos write-only cifrados con la KEK de
// alerts (HORUS_ALERTS_KEK_FILE), prueba de conexión, registro de entregas y
// el durable alerts-notify que avisa de hallazgos abiertos, exportadores
// silenciosos y túneles caídos sin la IP del cliente. Reglas, silencios y
// escalado llegan con el incremento de alertas completo.
package alerts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/packages/go/observability"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/alerts/internal/notify"
	"github.com/hcdestroyer/horus-flow/services/alerts/migrations"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
)

// Role es el nombre del rol en HORUS_ROLES: reglas, alertas y notificaciones.
const Role = "alerts"

// Config del módulo.
type Config struct {
	PostgresDSN      string               `env:"HORUS_POSTGRES_DSN"`
	PostgresPassword observability.Secret `env:"HORUS_POSTGRES_PASSWORD"`
	Migrate          bool                 `env:"HORUS_ALERTS_MIGRATE" envDefault:"true"`
	AppRole          string               `env:"HORUS_ALERTS_DB_APP_ROLE" envDefault:"alerts_app"`
	PlatformRole     string               `env:"HORUS_ALERTS_DB_PLATFORM_ROLE" envDefault:"alerts_platform"`
	CursorKey        observability.Secret `env:"HORUS_ALERTS_CURSOR_KEY"`
	KEK              observability.Secret `env:"HORUS_ALERTS_KEK"`
	PublicKeys       string               `env:"HORUS_JWT_PUBLIC_KEYS"`
	Issuer           string               `env:"HORUS_AUTH_ISSUER" envDefault:"horus-auth"`
	PublicBaseURL    string               `env:"HORUS_PUBLIC_BASE_URL"`
	SMTPHost         string               `env:"HORUS_SMTP_HOST"`
	SMTPPort         int                  `env:"HORUS_SMTP_PORT" envDefault:"587"`
	SMTPUsername     string               `env:"HORUS_SMTP_USERNAME"`
	SMTPPassword     observability.Secret `env:"HORUS_SMTP_PASSWORD"`
	SMTPFrom         string               `env:"HORUS_SMTP_FROM"`
	SMTPTLS          string               `env:"HORUS_SMTP_TLS" envDefault:"starttls"`
	TelegramAPI      string               `env:"HORUS_TELEGRAM_API_URL" envDefault:"https://api.telegram.org"`
	TelegramToken    observability.Secret `env:"HORUS_TELEGRAM_BOT_TOKEN"`
	// Cola de entregas (D23): periodo del despachador, intentos y backoff.
	DispatchEvery time.Duration   `env:"HORUS_ALERTS_DISPATCH_INTERVAL" envDefault:"5s"`
	MaxAttempts   int             `env:"HORUS_ALERTS_MAX_ATTEMPTS" envDefault:"8"`
	RetryBackoff  []time.Duration `env:"HORUS_ALERTS_RETRY_BACKOFF" envSeparator:","`
}

type mod struct {
	db      *pgdb.DB
	svc     *notify.Service
	bus     *natsx.Bus
	migrate bool
	logger  *slog.Logger
	relay   module.Module
	// dispatchEvery es el periodo del despachador de la cola de entregas.
	dispatchEvery time.Duration
}

// Register construye el módulo del rol alerts (firma module.Factory).
func Register(ctx context.Context, deps module.Deps) (module.Module, error) {
	cfg, err := config.Load[Config](deps.Environ)
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
			return nil, errors.New("alerts: HORUS_POSTGRES_DSN is required")
		}
		logger.WarnContext(ctx, "alerts inactive: HORUS_POSTGRES_DSN not set (dev)")
		return module.Idle(), nil
	}
	var sealer *envelope.Sealer
	if cfg.KEK.IsZero() {
		if !dev {
			return nil, errors.New("alerts: HORUS_ALERTS_KEK_FILE is required outside dev")
		}
		logger.Warn("alerts: ephemeral KEK (dev): channel credentials become unreadable on restart; set HORUS_ALERTS_KEK_FILE")
		sealer = envelope.Ephemeral()
	} else {
		kek, err := envelope.ParseKEK(cfg.KEK.Reveal())
		if err != nil {
			return nil, fmt.Errorf("alerts: HORUS_ALERTS_KEK_FILE: %w", err)
		}
		if sealer, err = envelope.New(kek); err != nil {
			return nil, err
		}
	}
	verifier, ok := module.Lookup[*authz.Verifier](deps.Services, authapi.ServiceVerifier)
	if !ok {
		if cfg.PublicKeys == "" {
			return nil, errors.New("alerts: no token verifier (auth role not local and HORUS_JWT_PUBLIC_KEYS_FILE unset)")
		}
		keys, err := authz.ParsePublicKeysPEM([]byte(cfg.PublicKeys))
		if err != nil {
			return nil, fmt.Errorf("alerts: HORUS_JWT_PUBLIC_KEYS_FILE: %w", err)
		}
		verifier = authz.NewVerifier(keys, cfg.Issuer, nil)
	}
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: cfg.PostgresDSN, Password: cfg.PostgresPassword.Reveal(),
		AppRole: cfg.AppRole, PlatformRole: cfg.PlatformRole, MaxConns: 10})
	if err != nil {
		return nil, err
	}
	senders := &notify.Senders{
		SMTP: notify.SMTPConfig{Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword.Reveal(),
			From: cfg.SMTPFrom, TLS: cfg.SMTPTLS},
		TelegramAPI: cfg.TelegramAPI, TelegramToken: cfg.TelegramToken.Reveal(), DefaultTimeout: 10 * time.Second,
	}
	svc := notify.NewService(notify.NewStore(db), sealer, senders, pagination.NewCodec([]byte(cfg.CursorKey.Reveal())),
		func() (authapi.AuditRecorder, bool) {
			return module.Lookup[authapi.AuditRecorder](deps.Services, authapi.ServiceAudit)
		},
		cfg.PublicBaseURL, logger)
	svc.SetRetry(cfg.MaxAttempts, cfg.RetryBackoff, 0)
	if deps.Health != nil {
		deps.Health.AddCheck(health.Check{Name: "postgres", Critical: true, Probe: db.Ping})
	}
	if deps.Routes != nil {
		notify.NewHandler(svc, authz.NewGuard(verifier), cfg.PublicBaseURL, logger).Mount(deps.Routes)
	}
	bus, err := natsx.Shared(ctx, deps.Services, deps.Environ, logger)
	if err != nil {
		db.Close()
		return nil, err
	}
	relay, err := natsx.WithRelay(ctx, deps, module.Idle(), db, migrations.Schema)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &mod{db: db, svc: svc, bus: bus, migrate: cfg.Migrate, logger: logger, relay: relay, dispatchEvery: cfg.DispatchEvery}, nil
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
	m.logger.InfoContext(ctx, "alerts migrations applied", slog.Int("count", n))
	return nil
}

// Run publica el outbox y consume alerts-notify (deliver new: solo avisa de
// lo que ocurre a partir del alta del durable, events.md §4.2).
func (m *mod) Run(ctx context.Context) error {
	if m.bus != nil {
		go func() {
			_ = natsx.Consume(ctx, m.bus.JS, natsx.ConsumerConfig{Service: Role, Stream: "DETECTION_EVENTS",
				Durable: m.bus.Durable(notify.DurableNotify + "-detection"), FilterSubjects: notify.NotifySubjects[:2], DeliverNew: true,
				Logger: m.logger}, m.svc.HandleEvent)
		}()
		go func() {
			_ = natsx.Consume(ctx, m.bus.JS, natsx.ConsumerConfig{Service: Role, Stream: "FLOWS_EVENTS",
				Durable: m.bus.Durable(notify.DurableNotify + "-flows"), FilterSubjects: notify.NotifySubjects[2:4], DeliverNew: true,
				Logger: m.logger}, m.svc.HandleEvent)
		}()
		go func() {
			_ = natsx.Consume(ctx, m.bus.JS, natsx.ConsumerConfig{Service: Role, Stream: "WIREGUARD_EVENTS",
				Durable: m.bus.Durable(notify.DurableNotify + "-wireguard"), FilterSubjects: notify.NotifySubjects[4:], DeliverNew: true,
				Logger: m.logger}, m.svc.HandleEvent)
		}()
	} else {
		m.logger.WarnContext(ctx, "alerts-notify inactive: HORUS_NATS_URL not set")
	}
	// Cola persistente de entregas (D23): retoma tras un reinicio lo que
	// quedó pendiente o a medio enviar.
	go m.svc.RunDispatcher(ctx, m.dispatchEvery)
	return m.relay.Run(ctx)
}

func (m *mod) Stop(context.Context) error {
	m.db.Close()
	return nil
}
