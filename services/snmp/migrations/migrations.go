// Package migrations embebe las migraciones goose del esquema PostgreSQL
// `snmp` (docs/database.md §3): ajustes por ISP del rol snmp. El histórico de
// métricas vive en ClickHouse (subpaquete clickhouse).
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
