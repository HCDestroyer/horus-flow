//go:build integration

package clickhouse_test

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/chmigrate"
	"github.com/hcdestroyer/horus-flow/packages/go/chtest"
	chadapter "github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/clickhouse"
	chschema "github.com/hcdestroyer/horus-flow/services/snmp/migrations/clickhouse"
)

// TestSNMPSchema aplica la migración real contra ClickHouse (chtest), la
// revierte y la vuelve a aplicar, escribe un lote con el escritor y lo lee
// por tenant (crudo y agregados de 5 min / 1 h) con la política p_tenant.
func TestSNMPSchema(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	srv := chtest.Start(t, nil)
	admin, err := chmigrate.Open(srv.DSN, srv.Password)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close() }()
	opts := chmigrate.Options{VersionTable: chschema.VersionTable, BootstrapDatabase: chschema.BootstrapDatabase}

	// Base limpia.
	if _, err := chmigrate.DownTo(ctx, admin, chschema.Migrations(), 0, opts); err != nil {
		t.Logf("down inicial: %v", err)
	}
	applied, err := chmigrate.Up(ctx, admin, chschema.Migrations(), opts)
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("aplicadas %d migraciones, esperaba 1", len(applied))
	}
	if again, err := chmigrate.Up(ctx, admin, chschema.Migrations(), opts); err != nil || len(again) != 0 {
		t.Fatalf("segunda ejecución: %d aplicadas, err %v", len(again), err)
	}
	if _, err := chmigrate.DownTo(ctx, admin, chschema.Migrations(), 0, opts); err != nil {
		t.Fatalf("down: %v", err)
	}
	if n := count(ctx, t, admin, "SELECT count() FROM system.tables WHERE database = 'snmp' AND name != 'goose_db_version'"); n != 0 {
		t.Fatalf("tras down quedan %d tablas", n)
	}
	if _, err := chmigrate.Up(ctx, admin, chschema.Migrations(), opts); err != nil {
		t.Fatalf("re-up: %v", err)
	}
	for _, tbl := range []string{"device_metrics", "interface_metrics", "device_5m", "device_1h", "interface_5m", "interface_1h"} {
		if n := count(ctx, t, admin, "SELECT count() FROM system.tables WHERE database = 'snmp' AND name = '"+tbl+"'"); n != 1 {
			t.Fatalf("falta snmp.%s", tbl)
		}
	}
	// Escritor horus_snmp (rol horus_snmp_role) y lector por tenant.
	if err := chmigrate.Provision(ctx, admin, []chmigrate.User{{Name: chadapter.UserSNMP, Password: "snmp-writer-password-1", Roles: []string{"horus_snmp_role"}},
		{Name: "horus_analytics", Password: "analytics-reader-pass-1", Roles: []string{"horus_analytics_role"}}}); err != nil {
		t.Fatalf("provision: %v", err)
	}
	w, err := chadapter.Open(srv.DSN, chadapter.UserSNMP, "snmp-writer-password-1", 4, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close(ctx) }()
	tenantA, tenantB := uuid.New(), uuid.New()
	routerA, routerB, site := uuid.New(), uuid.New(), uuid.New()
	ts := time.Now().UTC().Truncate(time.Minute)
	cpu := float32(42.5)
	rx := 1500.0
	up := uint64(3600)
	b := chadapter.Batch{ID: "test:" + uuid.NewString()}
	for i := range 3 {
		at := ts.Add(time.Duration(i) * time.Minute)
		b.Devices = append(b.Devices,
			chadapter.DeviceRow{TenantID: tenantA, SiteID: site, RouterID: routerA, TS: at, PollDurationMs: 120, ICMPOK: true, SNMPOK: true, CPUPercent: &cpu, UptimeSeconds: &up},
			chadapter.DeviceRow{TenantID: tenantB, SiteID: site, RouterID: routerB, TS: at, PollDurationMs: 90, ICMPOK: i != 0, SNMPOK: false})
		b.Interfaces = append(b.Interfaces, chadapter.InterfaceRow{TenantID: tenantA, SiteID: site, RouterID: routerA, InterfaceID: uuid.New(),
			IfIndex: 7, Name: "sfp-sfpplus1", TS: at, OperUp: true, RxBps: &rx, TxBps: &rx})
	}
	if err := w.Insert(ctx, b); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rd := openAs(t, srv.DSN, "horus_analytics", "analytics-reader-pass-1")
	defer func() { _ = rd.Close() }()
	q := func(sqlText string, tenant uuid.UUID) uint64 {
		var n uint64
		if err := rd.QueryRowContext(ctx, sqlText+" SETTINGS SQL_horus_tenant = '"+tenant.String()+"'").Scan(&n); err != nil {
			t.Fatalf("%s: %v", sqlText, err)
		}
		return n
	}
	if n := q("SELECT count() FROM snmp.device_metrics", tenantA); n != 3 {
		t.Fatalf("tenant A ve %d filas de device_metrics, esperaba 3", n)
	}
	if n := q("SELECT count() FROM snmp.device_metrics", tenantB); n != 3 {
		t.Fatalf("tenant B ve %d filas de device_metrics, esperaba 3", n)
	}
	if n := q("SELECT count() FROM snmp.interface_metrics", tenantB); n != 0 {
		t.Fatalf("tenant B ve %d filas de interface_metrics de A", n)
	}
	// Agregados (lectura como analytics: sum/count/max, bucket).
	var cnt, okc uint64
	var sum float64
	if err := rd.QueryRowContext(ctx, `SELECT sum(cpu_percent_sum), sum(cpu_percent_count), sum(icmp_ok_count) FROM snmp.device_5m
		WHERE router_id = ? GROUP BY bucket ORDER BY bucket LIMIT 1 SETTINGS SQL_horus_tenant = '`+tenantA.String()+`'`, routerA).Scan(&sum, &cnt, &okc); err != nil {
		t.Fatalf("device_5m: %v", err)
	}
	if cnt == 0 || sum/float64(cnt) < 42 || sum/float64(cnt) > 43 || okc == 0 {
		t.Fatalf("device_5m avg cpu = %v/%v, icmp_ok=%v", sum, cnt, okc)
	}
	if n := q("SELECT count() FROM snmp.device_1h", tenantA); n == 0 {
		t.Fatal("device_1h vacío")
	}
	if n := q("SELECT sum(samples) FROM snmp.interface_1h", tenantA); n != 3 {
		t.Fatalf("interface_1h samples = %d", n)
	}
	// Sin el ajuste de tenant la lectura falla (fail-closed).
	if err := rd.QueryRowContext(ctx, "SELECT count() FROM snmp.device_metrics").Scan(&cnt); err == nil {
		t.Fatal("lectura sin SQL_horus_tenant debería fallar")
	}
}

func openAs(t *testing.T, dsn, user, password string) *sql.DB {
	t.Helper()
	db, err := chmigrate.Open(replaceUser(dsn, user), password)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func replaceUser(dsn, user string) string {
	// clickhouse://horus@host:port/db → clickhouse://user@host:port/db
	const scheme = "clickhouse://"
	rest := dsn[len(scheme):]
	for i := range rest {
		if rest[i] == '@' {
			return scheme + user + rest[i:]
		}
	}
	return dsn
}

func count(ctx context.Context, t *testing.T, db *sql.DB, q string) uint64 {
	t.Helper()
	var n uint64
	if err := db.QueryRowContext(ctx, q).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}
