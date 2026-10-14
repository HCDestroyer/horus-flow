// Package migrations embebe las migraciones goose del esquema PostgreSQL
// `snmp` (docs/database.md §3): ajustes por ISP del rol snmp (umbrales de
// routers), inventario de servicios de infraestructura, estado de sus chequeos
// y outbox. Las de ClickHouse (base `snmp`) viven en migrations/clickhouse.
// Nombres con marca de tiempo UTC.
package migrations

import (
	"embed"
	"io/fs"
)

//go:embed postgres/*.sql
var files embed.FS

// Schema es el esquema PostgreSQL del módulo.
const Schema = "snmp"

// Postgres devuelve las migraciones PostgreSQL (archivos en la raíz).
func Postgres() fs.FS {
	sub, err := fs.Sub(files, "postgres")
	if err != nil {
		panic(err) //nolint:forbidigo // imposible: el directorio está embebido
	}
	return sub
}
