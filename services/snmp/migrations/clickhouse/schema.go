// Package chschema embebe las migraciones ClickHouse del rol snmp
// (docs/database.md §6.3 y §9): base `snmp` con las series crudas de
// dispositivo e interfaz (30 d) y sus agregados de 5 min (90 d) y 1 h (13
// meses), escritas por el usuario horus_snmp y leídas por analytics con la
// política por tenant de flows. Tabla de control propia: snmp.goose_db_version.
package chschema

import (
	"embed"
	"io/fs"
)

//go:embed *.sql
var migrations embed.FS

// Migrations devuelve las migraciones (<marca-de-tiempo>_<desc>.sql).
func Migrations() fs.FS { return migrations }

// Nombres de la tabla de control y la base de arranque (chmigrate.Options).
const (
	VersionTable      = "snmp.goose_db_version"
	BootstrapDatabase = "snmp"
)
