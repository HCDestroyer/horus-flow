// Package chschema es el contrato público del esquema ClickHouse de flows
// (C3) que aplica el rol ingester: otros módulos y los tests de integración
// lo usan para preparar un ClickHouse sin importar el módulo ingester.
package chschema

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hcdestroyer/horus-flow/packages/go/config"
	chadapter "github.com/hcdestroyer/horus-flow/services/ingester/internal/adapters/clickhouse"
	modcfg "github.com/hcdestroyer/horus-flow/services/ingester/internal/config"
)

// MigrateClickHouse aplica las migraciones y crea los usuarios por módulo
// con la configuración de environ (HORUS_CLICKHOUSE_DSN, contraseñas…).
func MigrateClickHouse(ctx context.Context, environ []string, log *slog.Logger) error {
	cfg, err := config.Load[modcfg.Config](environ)
	if err != nil {
		return fmt.Errorf("ingester config: %w", err)
	}
	if err := chadapter.MustHaveDSN(cfg); err != nil {
		return err
	}
	return chadapter.Migrate(ctx, cfg, log)
}
