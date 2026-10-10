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
}

// Validate implementa config.Validator.
func (c *Config) Validate() error {
	if c.Channel != "stable" && c.Channel != "beta" {
		return fmt.Errorf("HORUS_UPDATE_CHANNEL=%q: stable | beta", c.Channel)
	}
	if c.Interval < time.Minute {
		return fmt.Errorf("HORUS_UPDATE_CHECK_INTERVAL=%s: mínimo 1m", c.Interval)
	}
	if c.Source == "" {
		c.Source = "https://api.github.com/repos/" + c.Repo + "/releases?per_page=50"
	}
	return nil
}

// Register construye el módulo del rol jobs (firma module.Factory).
func Register(_ context.Context, deps module.Deps) (module.Module, error) {
	cfg, err := config.Load[Config](deps.Environ)
	if err != nil {
		return nil, fmt.Errorf("%s config: %w", Role, err)
	}
	log := deps.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	j := &jobs{cfg: cfg, log: log, checker: updatecheck.New(updatecheck.Options{
		Current: cfg.Version, Channel: cfg.Channel, Source: cfg.Source, Repo: cfg.Repo,
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
	if deps.Routes != nil {
		if v := verifier(cfg, deps); v != nil {
			g := authz.NewGuard(v)
			deps.Routes.Handle("GET /api/v1/platform/updates", g.Platform(PermUpdatesRead, j.getUpdates))
		} else {
			log.Warn("jobs: sin verificador de tokens (auth no es local y falta HORUS_JWT_PUBLIC_KEYS): /platform/updates no se sirve")
		}
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
}

func (j *jobs) getUpdates(w http.ResponseWriter, _ *http.Request) {
	jsonapi.Write(w, http.StatusOK, j.checker.Status())
}

// Run comprueba al poco de arrancar (con espera aleatoria para no coincidir
// todas las instalaciones) y después cada Interval.
func (j *jobs) Run(ctx context.Context) error {
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
