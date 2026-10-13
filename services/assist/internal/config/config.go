// Package config contiene la configuración del rol assist (HORUS_ASSIST_* y las
// comunes de PostgreSQL; docs/conventions.md §2.3, docs/assist.md).
package config

import (
	"errors"
	"log/slog"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/observability"
)

// Config del rol assist.
type Config struct {
	PostgresDSN      string               `env:"HORUS_POSTGRES_DSN"`
	PostgresPassword observability.Secret `env:"HORUS_POSTGRES_PASSWORD"`
	Migrate          bool                 `env:"HORUS_ASSIST_MIGRATE" envDefault:"true"`
	AppRole          string               `env:"HORUS_ASSIST_DB_APP_ROLE" envDefault:"assist_app"`
	PlatformRole     string               `env:"HORUS_ASSIST_DB_PLATFORM_ROLE" envDefault:"assist_platform"`
	// Claves públicas de los access tokens si auth no es local.
	PublicKeys string `env:"HORUS_JWT_PUBLIC_KEYS"`
	Issuer     string `env:"HORUS_AUTH_ISSUER" envDefault:"horus-auth"`
	// PublicBaseURL forma la URL del servidor MCP que se muestra en la página de administración.
	PublicBaseURL string `env:"HORUS_PUBLIC_BASE_URL"`
	// KEK de 32 B (hex o base64; HORUS_ASSIST_KEK_FILE) que cifra las claves de API de los ISP.
	// Sin ella (fuera de dev) no se pueden guardar claves por ISP (sí usar la de plataforma).
	KEK observability.Secret `env:"HORUS_ASSIST_KEK"`

	// Valor por defecto de plataforma: lo que usa un ISP que no configuró su proveedor.
	// DefaultEnabled activa el asistente en los ISP sin configuración propia.
	DefaultEnabled  bool                 `env:"HORUS_ASSIST_DEFAULT_ENABLED" envDefault:"false"`
	DefaultProvider string               `env:"HORUS_ASSIST_PROVIDER" envDefault:"anthropic"`
	DefaultModel    string               `env:"HORUS_ASSIST_MODEL"`
	DefaultBaseURL  string               `env:"HORUS_ASSIST_BASE_URL"`
	DefaultAPIKey   observability.Secret `env:"HORUS_ASSIST_API_KEY"`
	DefaultEffort   string               `env:"HORUS_ASSIST_EFFORT" envDefault:"medium"`
	DefaultLimit    int                  `env:"HORUS_ASSIST_DAILY_LIMIT" envDefault:"100"`

	// Límites de cada análisis: iteraciones del bucle de herramientas, tiempo total y tokens de salida.
	MaxIterations   int           `env:"HORUS_ASSIST_MAX_ITERATIONS" envDefault:"8"`
	RequestTimeout  time.Duration `env:"HORUS_ASSIST_REQUEST_TIMEOUT" envDefault:"120s"`
	MaxOutputTokens int64         `env:"HORUS_ASSIST_MAX_OUTPUT_TOKENS" envDefault:"16000"`
	// AllowLoopback permite base_url en 127.0.0.0/8 o ::1 (servidor local en la misma máquina, sin
	// contenedores). Por defecto se rechaza: dentro de un contenedor apunta al propio Horus.
	AllowLoopback bool `env:"HORUS_ASSIST_ALLOW_LOOPBACK" envDefault:"false"`
	// MCP habilita el servidor MCP (POST/GET/DELETE /api/v1/assist/mcp).
	MCP bool `env:"HORUS_ASSIST_MCP" envDefault:"true"`
	// MCPTokenTTL es la vigencia máxima de un token MCP.
	MCPTokenTTL time.Duration `env:"HORUS_ASSIST_MCP_TOKEN_TTL" envDefault:"2160h"`
}

// Validate implementa la validación de config.Load.
func (c *Config) Validate() error {
	var errs []error
	if c.MaxIterations < 1 || c.MaxIterations > 20 {
		errs = append(errs, errors.New("HORUS_ASSIST_MAX_ITERATIONS must be in [1, 20]"))
	}
	if c.RequestTimeout < 10*time.Second || c.RequestTimeout > 10*time.Minute {
		errs = append(errs, errors.New("HORUS_ASSIST_REQUEST_TIMEOUT must be in [10s, 10m]"))
	}
	if c.MaxOutputTokens < 1024 || c.MaxOutputTokens > 64000 {
		errs = append(errs, errors.New("HORUS_ASSIST_MAX_OUTPUT_TOKENS must be in [1024, 64000]"))
	}
	if c.MCPTokenTTL < time.Hour {
		errs = append(errs, errors.New("HORUS_ASSIST_MCP_TOKEN_TTL must be >= 1h"))
	}
	return errors.Join(errs...)
}

// LogValue implementa slog.LogValuer (sin secretos).
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(slog.Bool("postgres", c.PostgresDSN != ""), slog.String("default_provider", c.DefaultProvider),
		slog.Bool("default_enabled", c.DefaultEnabled), slog.Bool("platform_key", !c.DefaultAPIKey.IsZero()), slog.Bool("mcp", c.MCP))
}
