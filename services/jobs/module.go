// Package jobs es el módulo del rol jobs de `horus` (ADR-0025,
// docs/services.md): tareas singleton de plataforma.
//
// I1-22: aviso de versión nueva. Cada HORUS_UPDATE_CHECK_INTERVAL (6 h)
// consulta la fuente de versiones de la instalación (HORUS_UPDATE_SOURCE: API
// de releases de GitHub o latest.json) para el canal HORUS_UPDATE_CHANNEL
// (stable | beta) y lo sirve en GET /api/v1/platform/updates (solo
// platform.updates.read, el superadministrador). Sin salida a Internet falla
// en silencio: estado "unchecked". Métricas horus_update_available y
// horus_update_check_success; un aviso en el log por cada versión nueva.
//
// Copia remota de los backups (ADR-0019 con la nota de 2026-10-12): destinos
// SFTP de plataforma (/api/v1/platform/remote-destinations,
// platform.storage.manage) en el esquema PostgreSQL `jobs`, credenciales
// cifradas con la KEK de jobs (HORUS_JOBS_KEK_FILE) y un motor que cada
// HORUS_REMOTE_SYNC_INTERVAL (10 min) sube cifrado con age
// (HORUS_BACKUP_AGE_RECIPIENT_FILE) lo nuevo de backups/postgres y
// backups/clickhouse. Nada depende del destino: un fallo solo cambia su
// estado y las métricas horus_remote_sync_*.
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/observability"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/jobs/internal/updatecheck"
)

// Role es el nombre del rol en HORUS_ROLES: tareas singleton: archivado, sincronización remota y mantenimiento.
const Role = "jobs"

// PermUpdatesRead es el permiso de GET /platform/updates.
const PermUpdatesRead = "platform.updates.read"

// Config del rol jobs.
type Config struct {
	Version       string        `env:"HORUS_VERSION" envDefault:"0.0.0-dev"`
	Channel       string        `env:"HORUS_UPDATE_CHANNEL" envDefault:"stable"`
	Source        string        `env:"HORUS_UPDATE_SOURCE"`
	Check         bool          `env:"HORUS_UPDATE_CHECK" envDefault:"true"`
	Interval      time.Duration `env:"HORUS_UPDATE_CHECK_INTERVAL" envDefault:"6h"`
	Repo          string        `env:"HORUS_REPO" envDefault:"hcdestroyer/horus-flow"`
	PublicKeys    string        `env:"HORUS_JWT_PUBLIC_KEYS"`
	Issuer        string        `env:"HORUS_AUTH_ISSUER" envDefault:"horus-auth"`
	FirstDelayMax time.Duration `env:"HORUS_UPDATE_CHECK_FIRST_DELAY" envDefault:"2m"`

	// Copia remota (esquema PostgreSQL jobs).
	PostgresDSN      string               `env:"HORUS_POSTGRES_DSN"`
	PostgresPassword observability.Secret `env:"HORUS_POSTGRES_PASSWORD"`
	Migrate          bool                 `env:"HORUS_JOBS_MIGRATE" envDefault:"true"`
	AppRole          string               `env:"HORUS_JOBS_DB_APP_ROLE" envDefault:"jobs_app"`
	PlatformRole     string               `env:"HORUS_JOBS_DB_PLATFORM_ROLE" envDefault:"jobs_platform"`
	KEK              observability.Secret `env:"HORUS_JOBS_KEK"`
	// Clave pública age (age1…) con la que se cifran las copias; la identidad
	// (privada) vive en el paquete de secretos offline. HORUS_BACKUP_AGE_IDENTITY
	// se admite en desarrollo y tests (se deriva la pública).
	AgeRecipient  string               `env:"HORUS_BACKUP_AGE_RECIPIENT"`
	AgeIdentity   observability.Secret `env:"HORUS_BACKUP_AGE_IDENTITY"`
	StoreDir      string               `env:"HORUS_DATA_DIR" envDefault:"/var/lib/horus/store"`
	SyncInterval  time.Duration        `env:"HORUS_REMOTE_SYNC_INTERVAL" envDefault:"10m"`
	SyncSettle    time.Duration        `env:"HORUS_REMOTE_SYNC_SETTLE" envDefault:"2m"`
	SyncLagAfter  time.Duration        `env:"HORUS_REMOTE_SYNC_LAG_THRESHOLD" envDefault:"26h"`
	PublicBaseURL string               `env:"HORUS_PUBLIC_BASE_URL"`
}

// Validate implementa config.Validator.
func (c *Config) Validate() error {
	if c.Channel != "stable" && c.Channel != "beta" {
		return fmt.Errorf("HORUS_UPDATE_CHANNEL=%q: stable | beta", c.Channel)
	}
	if c.Interval < time.Minute {
		return fmt.Errorf("HORUS_UPDATE_CHECK_INTERVAL=%s: mínimo 1m", c.Interval)
	}
	if c.SyncInterval < time.Minute {
		return fmt.Errorf("HORUS_REMOTE_SYNC_INTERVAL=%s: mínimo 1m", c.SyncInterval)
	}
	if c.SyncSettle < 0 || c.SyncLagAfter < time.Hour {
		return fmt.Errorf("HORUS_REMOTE_SYNC_SETTLE ≥ 0 y HORUS_REMOTE_SYNC_LAG_THRESHOLD ≥ 1h")
	}
	if c.Source == "" {
		c.Source = "https://api.github.com/repos/" + c.Repo + "/releases?per_page=50"
	}
	return nil
}

// fallback: con la fuente por defecto (API de GitHub), el latest.json de la última release, que
// se descarga de github.com sin pasar por el límite de la API. Una fuente propia no tiene respaldo.
func (c *Config) fallback() string {
	if c.Source != "https://api.github.com/repos/"+c.Repo+"/releases?per_page=50" {
		return ""
	}
	return "https://github.com/" + c.Repo + "/releases/latest/download/latest.json"
}

// Register construye el módulo del rol jobs (firma module.Factory).
func Register(ctx context.Context, deps module.Deps) (module.Module, error) {
	cfg, err := config.Load[Config](deps.Environ)
	if err != nil {
		return nil, fmt.Errorf("%s config: %w", Role, err)
	}
	log := deps.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	j := &jobs{cfg: cfg, log: log, checker: updatecheck.New(updatecheck.Options{
		Current: cfg.Version, Channel: cfg.Channel, Source: cfg.Source, Fallback: cfg.fallback(), Repo: cfg.Repo,
	})}
	if deps.Metrics != nil {
		j.available = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "horus_update_available",
			Help: "1 si hay una versión nueva de Horus en el canal de la instalación."}, []string{"channel"})
		j.success = prometheus.NewGauge(prometheus.GaugeOpts{Name: "horus_update_check_success",
			Help: "1 si la última consulta de la fuente de versiones funcionó (0: no comprobado)."})
		for _, c := range []prometheus.Collector{j.available, j.success} {
			if err := deps.Metrics.Register(c); err != nil {
				return nil, fmt.Errorf("%s metrics: %w", Role, err)
			}
		}
	}
	if !cfg.Check {
		j.checker.Disable("comprobación de versiones desactivada en la instalación (--no-update-check)")
	}
	v := verifier(cfg, deps)
	if deps.Routes != nil {
		if v != nil {
			g := authz.NewGuard(v)
			deps.Routes.Handle("GET /api/v1/platform/updates", g.Platform(PermUpdatesRead, j.getUpdates))
		} else {
			log.Warn("jobs: sin verificador de tokens (auth no es local y falta HORUS_JWT_PUBLIC_KEYS): /platform/updates no se sirve")
		}
	}
	if j.remote, err = newRemote(ctx, cfg, deps, v, log); err != nil {
		return nil, err
	}
	return j, nil
}

func verifier(cfg Config, deps module.Deps) *authz.Verifier {
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

type jobs struct {
	cfg       Config
	log       *slog.Logger
	checker   *updatecheck.Checker
	available *prometheus.GaugeVec
	success   prometheus.Gauge
	notified  string
	remote    *remoteCopy
}

func (j *jobs) getUpdates(w http.ResponseWriter, _ *http.Request) {
	jsonapi.Write(w, http.StatusOK, j.checker.Status())
}

// Start aplica las migraciones del esquema jobs (si hay copia remota).
func (j *jobs) Start(ctx context.Context) error {
	if j.remote == nil {
		return nil
	}
	return j.remote.start(ctx)
}

// Stop cierra la conexión a PostgreSQL.
func (j *jobs) Stop(context.Context) error {
	if j.remote != nil {
		j.remote.db.Close()
	}
	return nil
}

// Run arranca el motor de copia remota y el aviso de versiones.
func (j *jobs) Run(ctx context.Context) error {
	if j.remote != nil {
		go j.remote.svc.Run(ctx)
	}
	return j.runUpdates(ctx)
}

// runUpdates comprueba al poco de arrancar (con espera aleatoria para no
// coincidir todas las instalaciones) y después cada Interval.
func (j *jobs) runUpdates(ctx context.Context) error {
	if !j.cfg.Check {
		<-ctx.Done()
		return nil
	}
	delay := time.Duration(0)
	if j.cfg.FirstDelayMax > 0 {
		delay = time.Duration(rand.Int64N(int64(j.cfg.FirstDelayMax))) //nolint:gosec // espera aleatoria, no criptográfica
	}
	t := time.NewTimer(delay)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			j.checkOnce(ctx)
			t.Reset(j.cfg.Interval)
		}
	}
}

func (j *jobs) checkOnce(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	err := j.checker.Check(cctx)
	st := j.checker.Status()
	if j.success != nil {
		if err != nil {
			j.success.Set(0)
		} else {
			j.success.Set(1)
		}
		v := 0.0
		if st.UpdateAvailable {
			v = 1
		}
		j.available.WithLabelValues(st.Channel).Set(v)
	}
	if err != nil {
		j.log.Info("jobs: versiones no comprobadas", slog.String("reason", err.Error()))
		return
	}
	if st.Latest != nil && st.Latest.Version != j.notified {
		j.notified = st.Latest.Version
		j.log.Warn("jobs: hay una versión nueva de Horus Flow",
			slog.String("event", "horus.platform.update_available"), slog.String("current", st.CurrentVersion),
			slog.String("latest", st.Latest.Version), slog.String("channel", st.Channel),
			slog.String("notes_url", st.Latest.NotesURL), slog.String("upgrade_command", st.Latest.UpgradeCommand))
	}
}
