package tenanttest

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TableRLS es el estado RLS de una tabla con columna tenant_id.
type TableRLS struct {
	Table          string // esquema.tabla
	TenantNullable bool
	Enabled        bool
	Forced         bool
	Policies       []string
}

// InspectRLS devuelve, para las tablas de schemas con columna tenant_id, su
// estado de RLS (pg_catalog). Ignora particiones hijas (heredan la política
// del padre) y las tablas de control de goose.
func InspectRLS(ctx context.Context, pool *pgxpool.Pool, schemas []string) ([]TableRLS, error) {
	rows, err := pool.Query(ctx, `
		SELECT n.nspname || '.' || c.relname,
		       NOT a.attnotnull,
		       c.relrowsecurity,
		       c.relforcerowsecurity,
		       coalesce((SELECT array_agg(p.polname::text ORDER BY p.polname) FROM pg_policy p WHERE p.polrelid = c.oid), '{}')
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'tenant_id' AND NOT a.attisdropped
		WHERE c.relkind IN ('r', 'p') AND n.nspname = ANY($1) AND NOT c.relispartition
		ORDER BY 1`, schemas)
	if err != nil {
		return nil, fmt.Errorf("inspect rls: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (TableRLS, error) {
		var t TableRLS
		err := r.Scan(&t.Table, &t.TenantNullable, &t.Enabled, &t.Forced, &t.Policies)
		return t, err //nolint:wrapcheck // se envuelve abajo
	})
	if err != nil {
		return nil, fmt.Errorf("inspect rls: %w", err)
	}
	return out, nil
}

// AssertRLS falla el test si alguna tabla con tenant_id de schemas no tiene
// RLS activado y forzado con la política p_tenant (docs/database.md §1.2 y
// §3 regla 6), salvo las de exempt (p. ej. las outbox, que solo lee el relay
// del módulo). Además exige `tenant_id NOT NULL` salvo en nullableOK
// (auditoría y outbox admiten NULL = plataforma).
func AssertRLS(t testing.TB, pool *pgxpool.Pool, schemas, exempt, nullableOK []string) []TableRLS {
	t.Helper()
	tables, err := InspectRLS(context.Background(), pool, schemas)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) == 0 {
		t.Fatalf("no hay tablas con tenant_id en %v", schemas)
	}
	var problems []string
	for _, tb := range tables {
		if tb.TenantNullable && !slices.Contains(nullableOK, tb.Table) {
			problems = append(problems, tb.Table+": tenant_id admite NULL")
		}
		if slices.Contains(exempt, tb.Table) {
			continue
		}
		if !tb.Enabled || !tb.Forced {
			problems = append(problems, fmt.Sprintf("%s: RLS enabled=%v forced=%v", tb.Table, tb.Enabled, tb.Forced))
		}
		if !slices.Contains(tb.Policies, "p_tenant") {
			problems = append(problems, tb.Table+": sin política p_tenant")
		}
	}
	if len(problems) > 0 {
		t.Fatalf("tablas con tenant_id que incumplen database.md §1.2:\n  %s", strings.Join(problems, "\n  "))
	}
	return tables
}
