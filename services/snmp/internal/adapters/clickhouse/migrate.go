// Package clickhouse es el adaptador ClickHouse del rol snmp: aplica el
// esquema snmp.* al arrancar (tabla de control propia) y escribe las muestras
// por lotes con un escritor asíncrono acotado. Un ClickHouse caído nunca
// detiene el sondeo (docs/architecture.md §10.1).
package clickhouse

import (
	"context"
	"log/slog"

	"github.com/hcdestroyer/horus-flow/packages/go/chmigrate"
	chschema "github.com/hcdestroyer/horus-flow/services/snmp/migrations/clickhouse"
)

// UserSNMP es el usuario escritor de snmp.* (rol horus_snmp_role).
const UserSNMP = "horus_snmp"

// Options de Migrate.
func options(log *slog.Logger) chmigrate.Options {
	return chmigrate.Options{VersionTable: chschema.VersionTable, BootstrapDatabase: chschema.BootstrapDatabase, Logger: log}
}

// Migrate aplica las migraciones pendientes de snmp.* y, si hay contraseña,
// crea o rota el usuario horus_snmp. Idempotente.
func Migrate(ctx context.Context, dsn, password, snmpPassword string, log *slog.Logger) error {
	db, err := chmigrate.Open(dsn, password)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	applied, err := chmigrate.Up(ctx, db, chschema.Migrations(), options(log))
	if err != nil {
		return err
	}
	if snmpPassword != "" {
		if err := chmigrate.Provision(ctx, db, []chmigrate.User{{Name: UserSNMP, Password: snmpPassword, Roles: []string{"horus_snmp_role"}}}); err != nil {
			return err
		}
	} else if log != nil {
		log.Warn("snmp: HORUS_CLICKHOUSE_SNMP_PASSWORD not set: user horus_snmp not provisioned (writing with the DSN user)")
	}
	if log != nil {
		log.Info("snmp: clickhouse schema ready", "applied", len(applied))
	}
	return nil
}

// DownTo revierte las migraciones (solo tests).
func DownTo(ctx context.Context, dsn, password string, version int64) error {
	db, err := chmigrate.Open(dsn, password)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	_, err = chmigrate.DownTo(ctx, db, chschema.Migrations(), version, options(nil))
	return err
}
