// Package clickhouse es el adaptador ClickHouse del ingester. Por ahora solo
// aplica el esquema v0 (I0-13) y crea los usuarios por módulo.
package clickhouse

import (
	"context"
	"fmt"
	"log/slog"

	chschema "github.com/hcdestroyer/horus-flow/infrastructure/clickhouse"
	"github.com/hcdestroyer/horus-flow/packages/go/chmigrate"
	"github.com/hcdestroyer/horus-flow/services/ingester/internal/config"
)

// Usuarios ClickHouse por módulo y su rol (los roles los crea la migración
// 20261008120500_roles_and_row_policies.sql).
const (
	UserIngester  = "horus_ingester"
	UserAnalytics = "horus_analytics"
	UserDetection = "horus_detection"
	UserAlerts    = "horus_alerts"
	UserJobs      = "horus_jobs"
)

// Users devuelve los usuarios que tienen contraseña configurada y los nombres
// de los que se omiten.
func Users(cfg config.Config) (users []chmigrate.User, skipped []string) {
	for _, u := range []struct {
		name, password, role string
	}{
		{UserIngester, cfg.IngesterPassword, "horus_ingester_role"},
		{UserAnalytics, cfg.AnalyticsPassword, "horus_analytics_role"},
		{UserDetection, cfg.DetectionPassword, "horus_detection_role"},
		{UserAlerts, cfg.AlertsPassword, "horus_alerts_role"},
		{UserJobs, cfg.JobsPassword, "horus_jobs_role"},
	} {
		if u.password == "" {
			skipped = append(skipped, u.name)
			continue
		}
		users = append(users, chmigrate.User{Name: u.name, Password: u.password, Roles: []string{u.role}})
	}
	return users, skipped
}

// Migrate aplica las migraciones pendientes y crea/actualiza los usuarios por
// módulo. Es idempotente.
func Migrate(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	db, err := chmigrate.Open(cfg.ClickHouseDSN, cfg.ClickHousePassword)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	applied, err := chmigrate.Up(ctx, db, chschema.Migrations(), chmigrate.Options{Logger: log})
	if err != nil {
		return err
	}
	current, _, err := chmigrate.Version(ctx, db, chschema.Migrations(), chmigrate.Options{})
	if err != nil {
		return err
	}
	users, skipped := Users(cfg)
	if err := chmigrate.Provision(ctx, db, users); err != nil {
		return err
	}
	if len(skipped) > 0 {
		log.Warn("clickhouse users without password not provisioned", "users", skipped)
	}
	log.Info("clickhouse schema ready", "version", current, "applied", len(applied), "users", len(users))
	return nil
}

// MustHaveDSN valida que haya DSN para migrar.
func MustHaveDSN(cfg config.Config) error {
	if cfg.ClickHouseDSN == "" {
		return fmt.Errorf("HORUS_CLICKHOUSE_DSN is required to migrate ClickHouse")
	}
	return nil
}
