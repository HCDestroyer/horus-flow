// Package config define la configuración propia del módulo ingester.
package config

import "errors"

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
}

// Validate implementa config.Validator.
func (c *Config) Validate() error {
	if c.Migrate && c.ClickHouseDSN == "" && c.ClickHousePassword != "" {
		return errors.New("HORUS_CLICKHOUSE_PASSWORD set without HORUS_CLICKHOUSE_DSN")
	}
	return nil
}
