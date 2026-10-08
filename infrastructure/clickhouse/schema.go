// Package chschema embebe las migraciones ClickHouse v0 de Horus (historia
// I0-13), derivadas del contrato C3
// (packages/schemas/datastore/v0/clickhouse-contract-v0.sql).
//
// Las aplica packages/go/chmigrate: el rol ingester al arrancar y
// `make migrate-ch` en desarrollo. Ver README.md.
package chschema

import (
	"embed"
	"io/fs"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrations devuelve las migraciones con los archivos en la raíz, como las
// espera goose.
func Migrations() fs.FS {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		// fs.Sub solo falla con una ruta inválida, que es una constante.
		panic(err)
	}
	return sub
}
