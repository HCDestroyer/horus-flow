// Package migrations embebe las migraciones goose del esquema `devices`
// (docs/database.md §3). Nombres con marca de tiempo UTC.
package migrations

import (
	"embed"
	"io/fs"
)

//go:embed postgres/*.sql
var files embed.FS

// Schema es el esquema PostgreSQL del módulo.
const Schema = "devices"

// Postgres devuelve las migraciones PostgreSQL (archivos en la raíz).
func Postgres() fs.FS {
	sub, err := fs.Sub(files, "postgres")
	if err != nil {
		panic(err) //nolint:forbidigo // imposible: el directorio está embebido
	}
	return sub
}
