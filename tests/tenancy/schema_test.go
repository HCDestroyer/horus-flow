//go:build integration

package tenancy

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
	"github.com/hcdestroyer/horus-flow/packages/go/tenanttest"
	authmig "github.com/hcdestroyer/horus-flow/services/auth/migrations"
	devmig "github.com/hcdestroyer/horus-flow/services/devices/migrations"
)

// Tablas sin RLS por diseño (docs/database.md §1.2): las outbox solo las lee
// el relay del módulo con su rol de plataforma.
var (
	rlsExempt  = []string{"auth.outbox", "devices.outbox"}
	nullableOK = []string{"auth.outbox", "devices.outbox", "auth.audit_log", "auth.role"}
)

func migrateAll(t *testing.T, db *pgdb.DB) {
	t.Helper()
	ctx := context.Background()
	if _, err := pgdb.Migrate(ctx, db, authmig.Schema, authmig.Postgres(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := pgdb.Migrate(ctx, db, devmig.Schema, devmig.Postgres(), nil); err != nil {
		t.Fatal(err)
	}
}

// I0-06 criterios 1, 2 y 5: las migraciones crean el modelo C2 con tenant_id
// NOT NULL y RLS fail-closed, y son reversibles (aplicar, revertir, reaplicar).
func TestMigrationsApplyRevertReapply(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: pgtest.New(t), AppRole: "devices_app", PlatformRole: "devices_platform"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migrateAll(t, db)

	tables := tenanttest.AssertRLS(t, db.Pool, []string{"auth", "devices"}, rlsExempt, nullableOK)
	want := map[string]bool{
		"auth.tenant_membership": true, "auth.role_assignment": true, "auth.kiosk": true,
		"auth.kiosk_enrollment_code": true, "devices.site": true, "devices.router": true,
		"devices.interface": true, "devices.credential": true, "devices.ip_realm": true,
		"devices.client_prefix": true, "devices.customer": true, "devices.customer_kind_change": true,
	}
	for _, tb := range tables {
		delete(want, tb.Table)
	}
	if len(want) > 0 {
		t.Fatalf("faltan tablas del contrato C2: %v", want)
	}

	// Revertir todo y reaplicar.
	for _, sch := range []string{devmig.Schema, authmig.Schema} {
		fsys := devmig.Postgres()
		if sch == authmig.Schema {
			fsys = authmig.Postgres()
		}
		mg, err := pgdb.NewMigrator(ctx, db, sch, fsys, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := mg.DownTo(ctx, 0); err != nil {
			t.Fatalf("down %s: %v", sch, err)
		}
		if v, err := mg.Version(ctx); err != nil || v != 0 {
			t.Fatalf("version tras down %s = %d, %v", sch, v, err)
		}
		_ = mg.Close()
	}
	var n int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname IN ('auth','devices') AND tablename <> 'goose_db_version'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("tablas tras down = %d, %v", n, err)
	}
	migrateAll(t, db)
	tenanttest.AssertRLS(t, db.Pool, []string{"auth", "devices"}, rlsExempt, nullableOK)
}

// I0-06 criterios 2 y 3: sin contexto de tenant el rol de la aplicación no ve
// filas; los prefijos no se solapan en un realm pero sí en el realm privado
// de otro nodo.
func TestRLSFailClosedAndPrefixOverlap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: pgtest.New(t), AppRole: "devices_app", PlatformRole: "devices_platform"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migrateAll(t, db)

	tenantA, tenantB := pgdb.TenantID(uuid.Must(uuid.NewV7())), pgdb.TenantID(uuid.Must(uuid.NewV7()))
	type node struct{ site, realm uuid.UUID }
	newNode := func(tenant pgdb.TenantID, name string) node {
		n := node{site: uuid.Must(uuid.NewV7()), realm: uuid.Must(uuid.NewV7())}
		err := db.TenantTx(ctx, tenant, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO devices.site (id, tenant_id, name) VALUES ($1, $2, $3)`, n.site, tenant.UUID(), name); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO devices.ip_realm (id, tenant_id, kind, site_id, name) VALUES ($1, $2, 'node_private', $3, $4)`,
				n.realm, tenant.UUID(), n.site, name+"-private")
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	addPrefix := func(tenant pgdb.TenantID, n node, prefix string) error {
		return db.TenantTx(ctx, tenant, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO devices.client_prefix (id, tenant_id, site_id, realm_id, prefix, role) VALUES ($1, $2, $3, $4, $5, 'customers')`,
				uuid.Must(uuid.NewV7()), tenant.UUID(), n.site, n.realm, prefix)
			return err
		})
	}
	n1 := newNode(tenantA, "centro")
	n2 := newNode(tenantA, "norte")
	newNode(tenantB, "otro-isp")
	if err := addPrefix(tenantA, n1, "10.20.0.0/24"); err != nil {
		t.Fatal(err)
	}
	if err := addPrefix(tenantA, n1, "10.20.0.128/25"); !pgdb.IsCode(err, pgdb.SQLStateExclusionViolation) {
		t.Fatalf("solape en el mismo realm: err = %v, quiero exclusion_violation", err)
	}
	if err := addPrefix(tenantA, n2, "10.20.0.128/25"); err != nil {
		t.Fatalf("mismo rango en el realm privado de otro nodo debe aceptarse: %v", err)
	}

	count := func(run func(context.Context, func(pgx.Tx) error) error) int {
		var n int
		if err := run(ctx, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM devices.site`).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(db.AppTx); n != 0 {
		t.Fatalf("rol de la aplicación sin tenant ve %d filas, quiero 0", n)
	}
	if n := count(func(ctx context.Context, fn func(pgx.Tx) error) error { return db.TenantTx(ctx, tenantA, fn) }); n != 2 {
		t.Fatalf("tenant A ve %d nodos, quiero 2", n)
	}
	if n := count(func(ctx context.Context, fn func(pgx.Tx) error) error { return db.TenantTx(ctx, tenantB, fn) }); n != 1 {
		t.Fatalf("tenant B ve %d nodos, quiero 1", n)
	}
	// Un INSERT con tenant_id distinto del contexto lo rechaza la política.
	err = db.TenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO devices.site (id, tenant_id, name) VALUES ($1, $2, 'x')`, uuid.Must(uuid.NewV7()), tenantB.UUID())
		return err
	})
	if err == nil {
		t.Fatal("INSERT de otro tenant aceptado")
	}
	if n := count(db.PlatformTx); n != 3 {
		t.Fatalf("rol de plataforma ve %d nodos, quiero 3", n)
	}
}
