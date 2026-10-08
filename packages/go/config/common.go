package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Entornos válidos de HORUS_ENV.
const (
	EnvDev     = "dev"
	EnvStaging = "staging"
	EnvProd    = "prod"
)

// Common es la configuración común a todos los procesos `horus`
// (docs/conventions.md §2.3, docs/observability.md §1 y §7). Cada módulo
// define además su propia estructura con sus variables HORUS_<MÓDULO>_*.
type Common struct {
	// Env es el entorno de despliegue: dev, staging o prod.
	Env string `env:"HORUS_ENV" envDefault:"dev"`
	// Roles es la lista de roles del proceso (HORUS_ROLES, separada por comas).
	// Vacío o "all" significa todos los roles públicos.
	Roles []string `env:"HORUS_ROLES" envSeparator:","`
	// Process es el nombre del contenedor/proceso (horus-app, horus-collector…)
	// que se usa en logs y métricas.
	Process string `env:"HORUS_PROCESS" envDefault:"horus"`

	LogLevel  string `env:"HORUS_LOG_LEVEL" envDefault:"info"`
	LogFormat string `env:"HORUS_LOG_FORMAT" envDefault:"json"`

	// AdminAddr sirve /healthz, /readyz, /metrics y (opcional) pprof.
	AdminAddr string `env:"HORUS_ADMIN_ADDR" envDefault:":8081"`
	// HTTPAddr sirve la API REST de los roles locales.
	HTTPAddr     string `env:"HORUS_HTTP_ADDR" envDefault:":8080"`
	PprofEnabled bool   `env:"HORUS_PPROF_ENABLED" envDefault:"false"`

	// StartTimeout acota la inicialización de cada rol.
	StartTimeout time.Duration `env:"HORUS_START_TIMEOUT" envDefault:"60s"`
	// ShutdownDelay es la espera entre poner /readyz en 503 y dejar de aceptar
	// tráfico (para que el balanceador lo saque).
	ShutdownDelay time.Duration `env:"HORUS_SHUTDOWN_DELAY" envDefault:"5s"`
	// ShutdownTimeout es el plazo máximo para drenar y cerrar todo.
	ShutdownTimeout time.Duration `env:"HORUS_SHUTDOWN_TIMEOUT" envDefault:"25s"`
}

// Validate implementa [Validator].
func (c *Common) Validate() error {
	var errs []error
	switch c.Env {
	case EnvDev, EnvStaging, EnvProd:
	default:
		errs = append(errs, fmt.Errorf("HORUS_ENV must be one of dev|staging|prod, got %q", c.Env))
	}
	switch strings.ToLower(c.LogFormat) {
	case "json", "text":
	default:
		errs = append(errs, fmt.Errorf("HORUS_LOG_FORMAT must be json|text, got %q", c.LogFormat))
	}
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(c.LogLevel)); err != nil {
		errs = append(errs, fmt.Errorf("HORUS_LOG_LEVEL invalid: %q", c.LogLevel))
	}
	if c.AdminAddr == "" {
		errs = append(errs, errors.New("HORUS_ADMIN_ADDR must not be empty"))
	}
	if c.Process == "" {
		errs = append(errs, errors.New("HORUS_PROCESS must not be empty"))
	}
	for name, d := range map[string]time.Duration{
		"HORUS_START_TIMEOUT":    c.StartTimeout,
		"HORUS_SHUTDOWN_DELAY":   c.ShutdownDelay,
		"HORUS_SHUTDOWN_TIMEOUT": c.ShutdownTimeout,
	} {
		if d < 0 {
			errs = append(errs, fmt.Errorf("%s must not be negative", name))
		}
	}
	if c.StartTimeout == 0 {
		errs = append(errs, errors.New("HORUS_START_TIMEOUT must be > 0"))
	}
	if c.ShutdownTimeout == 0 {
		errs = append(errs, errors.New("HORUS_SHUTDOWN_TIMEOUT must be > 0"))
	}
	return errors.Join(errs...)
}

// LogValue implementa slog.LogValuer: la configuración efectiva que se
// registra al arrancar. Common no contiene secretos; las configuraciones de
// módulo deben usar observability.Secret para los suyos.
func (c Common) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("env", c.Env),
		slog.String("roles", strings.Join(c.Roles, ",")),
		slog.String("process", c.Process),
		slog.String("log_level", c.LogLevel),
		slog.String("log_format", c.LogFormat),
		slog.String("admin_addr", c.AdminAddr),
		slog.String("http_addr", c.HTTPAddr),
		slog.Bool("pprof_enabled", c.PprofEnabled),
		slog.String("start_timeout", c.StartTimeout.String()),
		slog.String("shutdown_delay", c.ShutdownDelay.String()),
		slog.String("shutdown_timeout", c.ShutdownTimeout.String()),
	)
}
