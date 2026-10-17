// Package config es la configuración del módulo devices (HORUS_DEVICES_* y
// las comunes de PostgreSQL; docs/conventions.md §2.3).
package config

import (
	"log/slog"
	"net/netip"
	"strings"
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
	// Modo directo (Huawei VRP, sin túnel; docs/vendors/huawei.md §5): IP del
	// colector y del sondeador de Horus tal y como las ve el router por el
	// camino que asegura el operador, y servidor NTP para la configuración
	// VRP generada. Vacías = marcadores que el operador completa.
	DirectCollectorAddress string `env:"HORUS_DEVICES_DIRECT_COLLECTOR_ADDRESS"`
	DirectCollectorPort    int    `env:"HORUS_DEVICES_DIRECT_COLLECTOR_PORT" envDefault:"2055"`
	DirectPollerAddress    string `env:"HORUS_DEVICES_DIRECT_POLLER_ADDRESS"`
	DirectNTPServer        string `env:"HORUS_DEVICES_DIRECT_NTP_SERVER"`
	// Ciclo de vida de clientes (docs/database.md §2.3.4): días sin tráfico
	// hasta inactive, meses hasta la purga y periodo del job.
	CustomerInactivityDays  int           `env:"HORUS_DEVICES_CUSTOMER_INACTIVITY_DAYS" envDefault:"30"`
	CustomerRetentionMonths int           `env:"HORUS_DEVICES_CUSTOMER_RETENTION_MONTHS" envDefault:"25"`
	CustomerLifecycleEvery  time.Duration `env:"HORUS_DEVICES_CUSTOMER_LIFECYCLE_EVERY" envDefault:"24h"`
	// Conector ArgosISP (ADR-0030): barrido de identidades (> 6 h) y modo
	// laboratorio que permite URLs http y destinos privados o loopback.
	ArgosSweepEvery          time.Duration `env:"HORUS_INTEGRATIONS_SWEEP_EVERY" envDefault:"10m"`
	IntegrationsAllowPrivate bool          `env:"HORUS_INTEGRATIONS_ALLOW_PRIVATE" envDefault:"false"`
	ArgosRequestsPerSecond   int           `env:"HORUS_INTEGRATIONS_ARGOS_RPS" envDefault:"10"`
}

// DirectSettings interpreta la configuración del modo directo (una IP mal
// escrita se ignora: la configuración VRP lleva el marcador).
func (c Config) DirectSettings() (collector, poller, ntp netip.Addr, port int) {
	parse := func(s string) netip.Addr {
		a, err := netip.ParseAddr(strings.TrimSpace(s))
		if err != nil {
			return netip.Addr{}
		}
		return a.Unmap()
	}
	return parse(c.DirectCollectorAddress), parse(c.DirectPollerAddress), parse(c.DirectNTPServer), c.DirectCollectorPort
}

// LogValue implementa slog.LogValuer (sin secretos).
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(slog.Bool("postgres", c.PostgresDSN != ""), slog.Bool("migrate", c.Migrate))
}
