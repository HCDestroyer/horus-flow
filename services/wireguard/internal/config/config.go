// Package config es la configuración del módulo wireguard (HORUS_WG_*,
// HORUS_WIREGUARD_* y las comunes de PostgreSQL, acceso y mTLS;
// docs/conventions.md §2.3).
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
	Migrate          bool                 `env:"HORUS_WIREGUARD_MIGRATE" envDefault:"true"`
	AppRole          string               `env:"HORUS_WIREGUARD_DB_APP_ROLE" envDefault:"wireguard_app"`
	PlatformRole     string               `env:"HORUS_WIREGUARD_DB_PLATFORM_ROLE" envDefault:"wireguard_platform"`
	CursorKey        observability.Secret `env:"HORUS_WIREGUARD_CURSOR_KEY"`
	PublicKeys       string               `env:"HORUS_JWT_PUBLIC_KEYS"`
	Issuer           string               `env:"HORUS_AUTH_ISSUER" envDefault:"horus-auth"`

	// Hub y despliegue (ADR-0022 §1.2, docs/vendors/mikrotik.md §5 y §7).
	HubID       string   `env:"HORUS_WG_HUB_ID"`
	HubName     string   `env:"HORUS_WG_HUB_NAME" envDefault:"hub-1"`
	Endpoint    string   `env:"HORUS_WG_ENDPOINT" envDefault:"horus.localhost"`
	Port        int      `env:"HORUS_WG_PORT" envDefault:"51820"`
	HubPubKey   string   `env:"HORUS_WG_HUB_PUBLIC_KEY"`
	TunnelCIDRs []string `env:"HORUS_WG_TUNNEL_CIDRS" envSeparator:"," envDefault:"10.255.0.0/16"`
	Services    string   `env:"HORUS_WG_SERVICES_CIDR" envDefault:"10.255.0.0/24"`
	// CollectorIP vacío = IP del hub (primera de la red de servicios).
	CollectorIP  string `env:"HORUS_COLLECTOR_IP"`
	CacheEntries string `env:"HORUS_WG_TRAFFIC_FLOW_CACHE_ENTRIES" envDefault:"256k"`
	NTPServer    string `env:"HORUS_WG_NTP_SERVER" envDefault:"pool.ntp.org"`

	// Acceso público (D19): destino del /tool fetch y certificado a importar.
	PublicBaseURL string `env:"HORUS_PUBLIC_BASE_URL"`
	AccessMode    string `env:"HORUS_ACCESS_MODE"`
	TLSMode       string `env:"HORUS_TLS_MODE"`
	// PublicCertPEM: certificado público servido (HORUS_PUBLIC_TLS_CERT_FILE);
	// se incluye en el script con TLS self_signed o provided.
	PublicCertPEM string `env:"HORUS_PUBLIC_TLS_CERT"`

	// gRPC interno con wg-agent.
	GRPCAddr       string        `env:"HORUS_WIREGUARD_GRPC_ADDR" envDefault:":9094"`
	AgentAddr      string        `env:"HORUS_WGAGENT_ADDR"`
	GRPCTLSMode    string        `env:"GRPC_TLS_MODE" envDefault:"mtls"`
	TLSCAFile      string        `env:"HORUS_TLS_CA_FILE"`
	TLSCertFile    string        `env:"HORUS_TLS_CERT_FILE"`
	TLSKeyFile     string        `env:"HORUS_TLS_KEY_FILE"`
	ReconcileEvery time.Duration `env:"HORUS_WIREGUARD_RECONCILE_EVERY" envDefault:"5s"`
}

// LogValue implementa slog.LogValuer (sin secretos).
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(slog.Bool("postgres", c.PostgresDSN != ""), slog.String("endpoint", c.Endpoint), slog.Int("port", c.Port),
		slog.Any("tunnel_cidrs", c.TunnelCIDRs), slog.String("services_cidr", c.Services), slog.String("access_mode", c.AccessMode),
		slog.String("grpc_tls", c.GRPCTLSMode))
}
