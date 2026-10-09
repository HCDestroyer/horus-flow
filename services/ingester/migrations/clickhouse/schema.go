// Package chschema embebe las migraciones ClickHouse v0 de Horus (historia
// I0-13), derivadas del contrato C3
// (packages/schemas/datastore/v0/clickhouse-contract-v0.sql).
//
// Ubicación según docs/database.md §3
// (services/<servicio>/migrations/clickhouse/): el binario único solo puede
// embeber archivos bajo el árbol del módulo que los usa. Las aplica
// packages/go/chmigrate: el rol ingester al arrancar y `make migrate-ch` en
// desarrollo. Ver README.md.
package chschema

import (
	"embed"
	"io/fs"
)

//go:embed *.sql
var migrations embed.FS

// Migrations devuelve las migraciones (<marca-de-tiempo>_<desc>.sql en la
// raíz), como las espera goose.
func Migrations() fs.FS {
	return migrations
}
