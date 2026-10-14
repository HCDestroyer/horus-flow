package api

import (
	"io/fs"

	snmpschema "github.com/hcdestroyer/horus-flow/services/snmp/migrations/clickhouse"
)

// Esquema ClickHouse del rol snmp para las pruebas de integración de otros
// módulos (p. ej. analytics, que lee sus agregados): solo se importa el
// contrato services/snmp/api, nunca las migraciones directamente.
const (
	ClickHouseVersionTable      = snmpschema.VersionTable
	ClickHouseBootstrapDatabase = snmpschema.BootstrapDatabase
)

// ClickHouseMigrations son las migraciones ClickHouse del rol snmp.
func ClickHouseMigrations() fs.FS { return snmpschema.Migrations() }
