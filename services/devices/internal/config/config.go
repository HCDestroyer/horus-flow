// Package config es la configuración del módulo devices (HORUS_DEVICES_* y
// las comunes de PostgreSQL; docs/conventions.md §2.3).
package config

import (
	"log/slog"
	"time"

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
	// KEK de 32 B (hex o base64; HORUS_DEVICES_KEK_FILE) para cifrar las
	// credenciales de routers (docs/security.md S9). Vacía en dev = efímera.
	KEK observability.Secret `env:"HORUS_DEVICES_KEK"`
	// TunnelCIDRs: rangos de túneles de Horus (los mismos que wireguard); la
	// importación de prefijos nunca los propone.
	TunnelCIDRs []string `env:"HORUS_WG_TUNNEL_CIDRS" envSeparator:"," envDefault:"10.255.0.0/16"`
	// RouterOSBaseURL: plantilla de la URL REST del router ({ip} = IP de túnel).
	RouterOSBaseURL string        `env:"HORUS_DEVICES_ROUTEROS_BASE_URL" envDefault:"https://{ip}"`
	RouterOSTimeout time.Duration `env:"HORUS_DEVICES_ROUTEROS_TIMEOUT" envDefault:"20s"`
	// Ciclo de vida de clientes (docs/database.md §2.3.4): días sin tráfico
	// hasta inactive, meses hasta la purga y periodo del job.
	CustomerInactivityDays  int           `env:"HORUS_DEVICES_CUSTOMER_INACTIVITY_DAYS" envDefault:"30"`
	CustomerRetentionMonths int           `env:"HORUS_DEVICES_CUSTOMER_RETENTION_MONTHS" envDefault:"25"`
	CustomerLifecycleEvery  time.Duration `env:"HORUS_DEVICES_CUSTOMER_LIFECYCLE_EVERY" envDefault:"24h"`
}

// LogValue implementa slog.LogValuer (sin secretos).
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(slog.Bool("postgres", c.PostgresDSN != ""), slog.Bool("migrate", c.Migrate))
}
