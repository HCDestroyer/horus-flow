package pgdb

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

var schemaRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// Migrator aplica las migraciones goose de un módulo sobre su esquema.
type Migrator struct {
	provider *goose.Provider
	db       *sql.DB
}

// NewMigrator prepara las migraciones de fsys (archivos *.sql en la raíz de
// fsys) para el esquema schema. Crea el esquema si no existe (la tabla de
// control `<esquema>.goose_db_version` vive dentro). La conexión debe ser la
// del rol `<svc>_migrator` (o administrador en desarrollo).
func NewMigrator(ctx context.Context, d *DB, schema string, fsys fs.FS, logger *slog.Logger) (*Migrator, error) {
	if !schemaRe.MatchString(schema) {
		return nil, fmt.Errorf("pgdb: invalid schema %q", schema)
	}
	if _, err := d.Pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+pgx.Identifier{schema}.Sanitize()); err != nil {
		return nil, fmt.Errorf("pgdb: create schema %s: %w", schema, err)
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("pgdb: locker: %w", err)
	}
	db := stdlib.OpenDBFromPool(d.Pool)
	opts := []goose.ProviderOption{
		goose.WithTableName(schema + ".goose_db_version"),
		goose.WithSessionLocker(locker),
		goose.WithDisableGlobalRegistry(true),
	}
	if logger != nil {
		opts = append(opts, goose.WithSlog(logger))
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, fsys, opts...)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pgdb: goose provider: %w", err)
	}
	return &Migrator{provider: p, db: db}, nil
}

// Up aplica las migraciones pendientes y devuelve cuántas aplicó.
func (m *Migrator) Up(ctx context.Context) (int, error) {
	res, err := m.provider.Up(ctx)
	if err != nil {
		return len(res), fmt.Errorf("pgdb: migrate up: %w", err)
	}
	return len(res), nil
}

// DownTo revierte hasta version (0 = todas).
func (m *Migrator) DownTo(ctx context.Context, version int64) (int, error) {
	res, err := m.provider.DownTo(ctx, version)
	if err != nil {
		return len(res), fmt.Errorf("pgdb: migrate down: %w", err)
	}
	return len(res), nil
}

// Version devuelve la versión aplicada.
func (m *Migrator) Version(ctx context.Context) (int64, error) {
	v, err := m.provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("pgdb: version: %w", err)
	}
	return v, nil
}

// Close libera la conexión database/sql (no cierra el pool).
func (m *Migrator) Close() error {
	if err := m.db.Close(); err != nil {
		return fmt.Errorf("pgdb: close migrator: %w", err)
	}
	return nil
}

// Migrate es atajo de NewMigrator + Up + Close.
func Migrate(ctx context.Context, d *DB, schema string, fsys fs.FS, logger *slog.Logger) (int, error) {
	m, err := NewMigrator(ctx, d, schema, fsys, logger)
	if err != nil {
		return 0, err
	}
	defer func() { _ = m.Close() }()
	return m.Up(ctx)
}
