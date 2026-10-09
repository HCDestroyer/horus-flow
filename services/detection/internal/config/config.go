// Package config contiene la configuración de `mod:detection`: la del rol
// (Config) y la declaración de feeds de reputación (feeds.yaml, embebida).
package config

import (
	_ "embed"
	"log/slog"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/observability"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
)

//go:embed feeds.yaml
var defaultFeeds []byte

// DefaultFeeds devuelve la declaración de feeds embebida.
func DefaultFeeds() (*datasets.Config, error) { return datasets.ParseConfig(defaultFeeds) }

// LoadFeeds lee la declaración de feeds de path, o la embebida si path es "".
func LoadFeeds(path string) (*datasets.Config, error) {
	if path == "" {
		return DefaultFeeds()
	}
	return datasets.LoadConfig(path)
}

// Config es la configuración del rol detection (HORUS_DETECTION_* y las
// comunes de PostgreSQL/ClickHouse; docs/conventions.md §2.3).
type Config struct {
	PostgresDSN      string               `env:"HORUS_POSTGRES_DSN"`
	PostgresPassword observability.Secret `env:"HORUS_POSTGRES_PASSWORD"`
	Migrate          bool                 `env:"HORUS_DETECTION_MIGRATE" envDefault:"true"`
	AppRole          string               `env:"HORUS_DETECTION_DB_APP_ROLE" envDefault:"detection_app"`
	PlatformRole     string               `env:"HORUS_DETECTION_DB_PLATFORM_ROLE" envDefault:"detection_platform"`
	// CursorKey firma los cursores de paginación (vacío = aleatoria por proceso).
	CursorKey observability.Secret `env:"HORUS_DETECTION_CURSOR_KEY"`
	// Claves públicas de los access tokens si auth no es local.
	PublicKeys    string `env:"HORUS_JWT_PUBLIC_KEYS"`
	Issuer        string `env:"HORUS_AUTH_ISSUER" envDefault:"horus-auth"`
	PublicBaseURL string `env:"HORUS_PUBLIC_BASE_URL"`

	// ClickHouse: lector por tenant (usuario horus_detection, row policies).
	ClickHouseDSN      string               `env:"HORUS_CLICKHOUSE_DSN"`
	ClickHouseUser     string               `env:"HORUS_DETECTION_CH_USER" envDefault:"horus_detection"`
	ClickHousePassword observability.Secret `env:"HORUS_CLICKHOUSE_DETECTION_PASSWORD"`
	ClickHouseTimeout  time.Duration        `env:"HORUS_DETECTION_CH_TIMEOUT" envDefault:"30s"`
	// Snapshot de reputación (el que publica feedsync; barrido retroactivo y razones).
	ReputationSnapshotDir string `env:"HORUS_REPUTATION_SNAPSHOT_DIR"`
	// Motor: activo, cadencia, margen para flujos tardíos y tenants fijos.
	Engine   bool          `env:"HORUS_DETECTION_ENGINE" envDefault:"true"`
	Interval time.Duration `env:"HORUS_DETECTION_INTERVAL" envDefault:"1m"`
	Lag      time.Duration `env:"HORUS_DETECTION_LAG" envDefault:"2m"`
	Tenants  []string      `env:"HORUS_DETECTION_TENANTS" envSeparator:","`

	// Feeds de reputación (I0-17, D20) dentro del rol: almacén, declaración,
	// listas personalizadas y sincronización periódica (descarga de Internet:
	// desactivada por defecto; HORUS_FEEDS_FIXTURES lee de un directorio).
	DataDir         string        `env:"HORUS_DATA_DIR" envDefault:"/var/lib/horus/store"`
	FeedsConfig     string        `env:"HORUS_FEEDS_CONFIG"`
	FeedsCustom     string        `env:"HORUS_FEEDS_CUSTOM"`
	FeedsFixtures   string        `env:"HORUS_FEEDS_FIXTURES"`
	FeedsSync       bool          `env:"HORUS_DETECTION_FEEDS_SYNC" envDefault:"false"`
	FeedsInterval   time.Duration `env:"HORUS_DETECTION_FEEDS_INTERVAL" envDefault:"15m"`
	AllowUnverified bool          `env:"HORUS_FEEDS_ALLOW_UNVERIFIED" envDefault:"false"`
}

// LogValue implementa slog.LogValuer (sin secretos).
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(slog.Bool("postgres", c.PostgresDSN != ""), slog.Bool("clickhouse", c.ClickHouseDSN != ""),
		slog.Bool("engine", c.Engine), slog.Duration("interval", c.Interval))
}
