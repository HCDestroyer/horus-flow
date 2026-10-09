// Package config es la configuración del módulo devices (HORUS_DEVICES_* y
// las comunes de PostgreSQL; docs/conventions.md §2.3).
package config

import (
	"log/slog"

	"github.com/hcdestroyer/horus-flow/packages/go/observability"
)

// Config del módulo.
type Config struct {
	PostgresDSN      string               `env:"HORUS_POSTGRES_DSN"`
	PostgresPassword observability.Secret `env:"HORUS_POSTGRES_PASSWORD"`
	Migrate          bool                 `env:"HORUS_DEVICES_MIGRATE" envDefault:"true"`
	AppRole          string               `env:"HORUS_DEVICES_DB_APP_ROLE" envDefault:"devices_app"`
	PlatformRole     string               `env:"HORUS_DEVICES_DB_PLATFORM_ROLE" envDefault:"devices_platform"`
	// CursorKey firma los cursores de paginación (vacío = aleatoria por proceso).
	CursorKey observability.Secret `env:"HORUS_DEVICES_CURSOR_KEY"`
	// PublicKeys: claves públicas de los access tokens si auth no es local
	// (HORUS_JWT_PUBLIC_KEYS_FILE).
	PublicKeys    string `env:"HORUS_JWT_PUBLIC_KEYS"`
	Issuer        string `env:"HORUS_AUTH_ISSUER" envDefault:"horus-auth"`
	PublicBaseURL string `env:"HORUS_PUBLIC_BASE_URL"`
}

// LogValue implementa slog.LogValuer (sin secretos).
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(slog.Bool("postgres", c.PostgresDSN != ""), slog.Bool("migrate", c.Migrate))
}
