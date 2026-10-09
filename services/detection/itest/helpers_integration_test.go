//go:build integration

package itest

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/postgres"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/detection/migrations"
)

// openPG crea una base vacía con el esquema detection migrado.
func openPG(t *testing.T) *pgdb.DB {
	t.Helper()
	dsn := pgtest.New(t)
	ctx := context.Background()
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: dsn, AppRole: "detection_app", PlatformRole: "detection_platform"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := pgdb.Migrate(ctx, db, migrations.Schema, migrations.Postgres(), nil); err != nil {
		t.Fatal(err)
	}
	return db
}

func findingsOf(t *testing.T, db *pgdb.DB, tenant uuid.UUID) []domain.Finding {
	t.Helper()
	var out []domain.Finding
	err := db.TenantTx(context.Background(), pgdb.TenantID(tenant), func(tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(), `SELECT id FROM detection.finding ORDER BY opened_at, id`)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		for _, id := range ids {
			f, err := postgres.Get(context.Background(), tx, id, false)
			if err != nil {
				return err
			}
			out = append(out, *f)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
