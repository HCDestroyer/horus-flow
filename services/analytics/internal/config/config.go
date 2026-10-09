// Package config define la configuración del rol analytics (consultas de tráfico).
package config

import "time"

// Config del módulo analytics (consultas sobre ClickHouse, I1-08/I1-29).
type Config struct {
	// ClickHouseDSN: servidor ClickHouse (clickhouse://user@host:9000/db).
	// Vacío = sin consultas de tráfico (los endpoints responden 503).
	ClickHouseDSN      string `env:"HORUS_CLICKHOUSE_DSN"`
	ClickHousePassword string `env:"HORUS_CLICKHOUSE_PASSWORD"`
	// AnalyticsPassword: si está, se consulta como horus_analytics (lector
	// con la política por tenant de ClickHouse) en lugar del usuario del DSN.
	AnalyticsPassword string `env:"HORUS_CLICKHOUSE_ANALYTICS_PASSWORD"`
	// QueryTimeout acota cada consulta (api.md §2.11: 10 s por widget).
	QueryTimeout time.Duration `env:"HORUS_ANALYTICS_QUERY_TIMEOUT" envDefault:"10s"`
	// CacheTTL de los datos de widgets (caché en proceso; Valkey pendiente).
	CacheTTL time.Duration `env:"HORUS_ANALYTICS_CACHE_TTL" envDefault:"10s"`
	// InventoryFile: exportadores y nodos (nombres para widgets, exporters_status).
	InventoryFile string `env:"HORUS_FLOWS_INVENTORY_FILE"`
	// NATSURL para leer el estado de los exportadores (bucket KV).
	NATSURL string `env:"HORUS_NATS_URL"`
	// Validación del access JWT si auth no es local.
	PublicKeys string `env:"HORUS_JWT_PUBLIC_KEYS"`
	Issuer     string `env:"HORUS_AUTH_ISSUER" envDefault:"horus-auth"`
	// CatalogSnapshotDir / ASNSnapshotDir: nombres de servicios, categorías y
	// organizaciones (y escritura de dim.* en ClickHouse, escritor único).
	CatalogSnapshotDir string `env:"HORUS_CATALOG_SNAPSHOT_DIR"`
	ASNSnapshotDir     string `env:"HORUS_ASN_SNAPSHOT_DIR"`
}
