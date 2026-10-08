// Package ingester es el módulo de el rol ingester de `horus`
// (ADR-0025, docs/services.md).
//
// Estado: al arrancar aplica el esquema ClickHouse v0 (I0-13) si hay
// HORUS_CLICKHOUSE_DSN y HORUS_INGESTER_CH_MIGRATE no es false; la ingesta
// de flujos llega en historias posteriores.
package ingester

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	chadapter "github.com/hcdestroyer/horus-flow/services/ingester/internal/adapters/clickhouse"
	modcfg "github.com/hcdestroyer/horus-flow/services/ingester/internal/config"
)

// Role es el nombre del rol en HORUS_ROLES: consumo de lotes de flujos y métricas SNMP hacia ClickHouse.
const Role = "ingester"

// Register construye el módulo del rol ingester (firma module.Factory).
func Register(_ context.Context, deps module.Deps) (module.Module, error) {
	cfg, err := config.Load[modcfg.Config](deps.Environ)
	if err != nil {
		return nil, fmt.Errorf("%s config: %w", Role, err)
	}
	return &ingester{Module: module.Idle(), cfg: cfg, log: deps.Logger}, nil
}

type ingester struct {
	module.Module
	cfg modcfg.Config
	log *slog.Logger
}

// Start implementa module.Starter: migra ClickHouse antes de marcar el rol listo.
func (m *ingester) Start(ctx context.Context) error {
	if !m.cfg.Migrate {
		return nil
	}
	if m.cfg.ClickHouseDSN == "" {
		m.log.Warn("HORUS_CLICKHOUSE_DSN not set: ClickHouse schema not migrated")
		return nil
	}
	if err := chadapter.Migrate(ctx, m.cfg, m.log); err != nil {
		return fmt.Errorf("%s: migrate clickhouse: %w", Role, err)
	}
	return nil
}

// MigrateClickHouse aplica el esquema ClickHouse con la configuración del
// entorno (HORUS_CLICKHOUSE_DSN, HORUS_CLICKHOUSE_PASSWORD[_FILE] y las
// contraseñas de usuarios por módulo). Lo usan `make migrate-ch` y, cuando
// exista, `horus migrate --module=ingester`.
func MigrateClickHouse(ctx context.Context, environ []string, log *slog.Logger) error {
	cfg, err := config.Load[modcfg.Config](environ)
	if err != nil {
		return fmt.Errorf("%s config: %w", Role, err)
	}
	if err := chadapter.MustHaveDSN(cfg); err != nil {
		return err
	}
	return chadapter.Migrate(ctx, cfg, log)
}
