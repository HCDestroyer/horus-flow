// Package config define la configuración propia del módulo ingester.
package config

import (
	"errors"
	"time"
)

// Config del módulo. Las variables comunes están en packages/go/config.Common.
// Toda variable admite la variante _FILE (secretos de Docker/Kubernetes).
type Config struct {
	// ClickHouseDSN es la conexión del migrador (clickhouse://user@host:9000/db).
	// Vacío: el rol arranca sin tocar ClickHouse (stub de I0-04).
	ClickHouseDSN string `env:"HORUS_CLICKHOUSE_DSN"`
	// ClickHousePassword sustituye a la contraseña del DSN.
	ClickHousePassword string `env:"HORUS_CLICKHOUSE_PASSWORD"`
	// Migrate aplica las migraciones ClickHouse al arrancar el rol
	// (database.md §3: cada servicio migra su esquema al arrancar).
	Migrate bool `env:"HORUS_INGESTER_CH_MIGRATE" envDefault:"true"`

	// Contraseñas de los usuarios ClickHouse por módulo. Un usuario sin
	// contraseña configurada no se crea (se loguea y se sigue).
	IngesterPassword  string `env:"HORUS_CLICKHOUSE_INGESTER_PASSWORD"`
	AnalyticsPassword string `env:"HORUS_CLICKHOUSE_ANALYTICS_PASSWORD"`
	DetectionPassword string `env:"HORUS_CLICKHOUSE_DETECTION_PASSWORD"`
	AlertsPassword    string `env:"HORUS_CLICKHOUSE_ALERTS_PASSWORD"`
	JobsPassword      string `env:"HORUS_CLICKHOUSE_JOBS_PASSWORD"`

	// Ingesta de flujos (I1-04). Sin HORUS_NATS_URL el rol solo migra.
	NATSURL string `env:"HORUS_NATS_URL"`
	// EnsureStreams crea TLM_FLOWS/FLOWS_EVENTS si faltan (dev y tests).
	EnsureStreams bool  `env:"HORUS_NATS_ENSURE_STREAMS" envDefault:"false"`
	TLMMaxBytes   int64 `env:"HORUS_TLM_FLOWS_MAX_BYTES" envDefault:"53687091200"`
	// InventoryFile: exportadores, realms y prefijos de clientes (packages/go/flowinv).
	InventoryFile string `env:"HORUS_FLOWS_INVENTORY_FILE"`
	// Workers son los lotes que se procesan e insertan en paralelo.
	Workers int `env:"HORUS_INGESTER_WORKERS" envDefault:"4"`
	// Descubrimiento de clientes (I1-05).
	FirstSeenInterval  time.Duration `env:"HORUS_INGESTER_FIRST_SEEN_INTERVAL" envDefault:"10s"`
	FirstSeenTTL       time.Duration `env:"HORUS_INGESTER_FIRST_SEEN_TTL" envDefault:"1h"`
	DiscoveryPerMinute int           `env:"HORUS_INGESTER_DISCOVERY_PER_MINUTE" envDefault:"2000"`
	DiscoveryRealmMax  int           `env:"HORUS_INGESTER_DISCOVERY_REALM_MAX" envDefault:"1048576"`
	ActivityInterval   time.Duration `env:"HORUS_INGESTER_ACTIVITY_INTERVAL" envDefault:"1h"`
	SummaryInterval    time.Duration `env:"HORUS_INGESTER_SUMMARY_INTERVAL" envDefault:"10s"`
	// API de estado de exportadores (I1-09): validación del access JWT si el
	// rol auth no corre en el mismo proceso.
	PublicKeys string `env:"HORUS_JWT_PUBLIC_KEYS"`
	Issuer     string `env:"HORUS_AUTH_ISSUER" envDefault:"horus-auth"`
	// Snapshots versionados (datasets.SnapshotDir) que se recargan en caliente (I1-07, §11).
	ASNSnapshotDir        string `env:"HORUS_ASN_SNAPSHOT_DIR"`
	CatalogSnapshotDir    string `env:"HORUS_CATALOG_SNAPSHOT_DIR"`
	ReputationSnapshotDir string `env:"HORUS_REPUTATION_SNAPSHOT_DIR"`
}

// Validate implementa config.Validator.
func (c *Config) Validate() error {
	if c.Migrate && c.ClickHouseDSN == "" && c.ClickHousePassword != "" {
		return errors.New("HORUS_CLICKHOUSE_PASSWORD set without HORUS_CLICKHOUSE_DSN")
	}
	return nil
}
