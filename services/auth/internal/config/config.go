// Package config es la configuración del módulo auth (HORUS_AUTH_* y las
// comunes de PostgreSQL y URL pública; docs/conventions.md §2.3). Toda
// variable admite la variante _FILE.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/observability"
)

// Config del módulo.
type Config struct {
	// PostgresDSN vacío en desarrollo deja el rol inactivo (con aviso).
	PostgresDSN      string               `env:"HORUS_POSTGRES_DSN"`
	PostgresPassword observability.Secret `env:"HORUS_POSTGRES_PASSWORD"`
	// Migrate aplica las migraciones del esquema auth al arrancar.
	Migrate      bool   `env:"HORUS_AUTH_MIGRATE" envDefault:"true"`
	AppRole      string `env:"HORUS_AUTH_DB_APP_ROLE" envDefault:"auth_app"`
	PlatformRole string `env:"HORUS_AUTH_DB_PLATFORM_ROLE" envDefault:"auth_platform"`

	// SigningKey es la clave Ed25519 PKCS#8 PEM de los access tokens
	// (HORUS_AUTH_SIGNING_KEY_FILE). Vacía en dev = clave efímera.
	SigningKey observability.Secret `env:"HORUS_AUTH_SIGNING_KEY"`
	// PreviousPublicKeys: claves públicas PEM aún aceptadas tras una rotación.
	PreviousPublicKeys string `env:"HORUS_AUTH_PREVIOUS_PUBLIC_KEYS"`
	// KEK de 32 B en hexadecimal o base64 (HORUS_AUTH_KEK_FILE) para cifrar
	// los secretos TOTP. Vacía en dev = efímera.
	KEK observability.Secret `env:"HORUS_AUTH_KEK"`
	// Issuer es el `iss` de los tokens.
	Issuer string `env:"HORUS_AUTH_ISSUER" envDefault:"horus-auth"`

	AccessTTL     time.Duration `env:"HORUS_AUTH_ACCESS_TTL" envDefault:"10m"`
	RefreshIdle   time.Duration `env:"HORUS_AUTH_REFRESH_IDLE_TTL" envDefault:"12h"`
	SessionMaxAge time.Duration `env:"HORUS_AUTH_SESSION_MAX_AGE" envDefault:"168h"`

	// Argon2 (security.md S2); bajarlos solo en tests.
	Argon2MemoryKiB uint32 `env:"HORUS_AUTH_ARGON2_MEMORY_KIB" envDefault:"65536"`
	Argon2Time      uint32 `env:"HORUS_AUTH_ARGON2_TIME" envDefault:"3"`

	// Superadministrador semilla (I0-06): se crea al arrancar si no existe;
	// debe cambiar la contraseña en el primer login y activar TOTP.
	SeedAdminEmail    string               `env:"HORUS_SEED_ADMIN_EMAIL"`
	SeedAdminPassword observability.Secret `env:"HORUS_SEED_ADMIN_PASSWORD"`
	SeedAdminName     string               `env:"HORUS_SEED_ADMIN_NAME" envDefault:"Superadministrador"`

	// PublicBaseURL y AllowedOrigins (D14/D19, api.md §1.11).
	PublicBaseURL  string   `env:"HORUS_PUBLIC_BASE_URL"`
	AllowedOrigins []string `env:"HORUS_ALLOWED_ORIGINS" envSeparator:","`
}

// Validate implementa config.Validator.
func (c *Config) Validate() error {
	var errs []error
	if c.AccessTTL <= 0 || c.AccessTTL > time.Hour {
		errs = append(errs, errors.New("HORUS_AUTH_ACCESS_TTL must be in (0, 1h]"))
	}
	if c.RefreshIdle <= 0 || c.SessionMaxAge <= 0 {
		errs = append(errs, errors.New("HORUS_AUTH_REFRESH_IDLE_TTL and HORUS_AUTH_SESSION_MAX_AGE must be > 0"))
	}
	if c.Argon2MemoryKiB < 8 || c.Argon2Time < 1 {
		errs = append(errs, errors.New("argon2 parameters too low"))
	}
	if c.PublicBaseURL != "" {
		if u, err := url.Parse(c.PublicBaseURL); err != nil || u.Scheme == "" || u.Host == "" {
			errs = append(errs, fmt.Errorf("HORUS_PUBLIC_BASE_URL invalid: %q", c.PublicBaseURL))
		}
	}
	if (c.SeedAdminEmail == "") != (c.SeedAdminPassword.IsZero()) {
		errs = append(errs, errors.New("HORUS_SEED_ADMIN_EMAIL and HORUS_SEED_ADMIN_PASSWORD_FILE go together"))
	}
	return errors.Join(errs...)
}

// LogValue implementa slog.LogValuer (sin secretos).
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Bool("postgres", c.PostgresDSN != ""),
		slog.Bool("migrate", c.Migrate),
		slog.Bool("signing_key", !c.SigningKey.IsZero()),
		slog.Bool("kek", !c.KEK.IsZero()),
		slog.String("access_ttl", c.AccessTTL.String()),
		slog.Bool("seed_admin", c.SeedAdminEmail != ""),
		slog.String("public_base_url", c.PublicBaseURL),
	)
}
