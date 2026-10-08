//go:build integration

// Tests de integración del esquema ClickHouse v0 (I0-13) contra el ClickHouse del compose:
//
//	make up && go test -tags integration -count=1 ./infrastructure/clickhouse/
//
// Ver harness_integration_test.go para elegir otro servidor. Los tests BORRAN las bases flows y
// dim y los usuarios/roles horus_* del servidor de destino.
package chschema_test

import (
	"context"
	"io/fs"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	chschema "github.com/hcdestroyer/horus-flow/infrastructure/clickhouse"
	"github.com/hcdestroyer/horus-flow/packages/go/chmigrate"
)

func TestSchemaV0(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	tg := discover(t)
	admin := openAdmin(t, tg)
	migrations := chschema.Migrations()
	opts := chmigrate.Options{}

	resetSchema(ctx, t, admin)
	files := migrationFiles(t)

	t.Run("migra una base vacía", func(t *testing.T) {
		applied, err := chmigrate.Up(ctx, admin, migrations, opts)
		if err != nil {
			t.Fatal(err)
		}
		if len(applied) != len(files) {
			t.Fatalf("aplicadas %d migraciones, hay %d archivos", len(applied), len(files))
		}
		cur, target, err := chmigrate.Version(ctx, admin, migrations, opts)
		if err != nil || cur != target {
			t.Fatalf("versión %d, esperada %d (err %v)", cur, target, err)
		}
	})

	t.Run("es idempotente", func(t *testing.T) {
		applied, err := chmigrate.Up(ctx, admin, migrations, opts)
		if err != nil {
			t.Fatal(err)
		}
		if len(applied) != 0 {
			t.Fatalf("segunda ejecución aplicó %d migraciones", len(applied))
		}
	})

	t.Run("up, down y up de nuevo", func(t *testing.T) {
		if _, err := chmigrate.DownTo(ctx, admin, migrations, 0, opts); err != nil {
			t.Fatal(err)
		}
		if n := queryUint(ctx, t, admin, "SELECT count() FROM system.tables WHERE database IN ('flows', 'dim') AND name != 'goose_db_version'"); n != 0 {
			t.Fatalf("tras down quedan %d tablas", n)
		}
		applied, err := chmigrate.Up(ctx, admin, migrations, opts)
		if err != nil {
			t.Fatal(err)
		}
		if len(applied) != len(files) {
			t.Fatalf("re-up aplicó %d de %d", len(applied), len(files))
		}
	})

	t.Run("tenant_id primero en el ORDER BY y TTL", func(t *testing.T) {
		checkLayout(ctx, t, admin)
	})

	t.Run("toda tabla con tenant_id tiene row policy de tenant", func(t *testing.T) {
		missing := queryStrings(ctx, t, admin, `
			SELECT database || '.' || table FROM system.columns
			WHERE database IN ('flows', 'dim') AND name = 'tenant_id'
			  AND (database, table) IN (SELECT database, name FROM system.tables WHERE engine LIKE '%MergeTree')
			  AND (database, table) NOT IN (SELECT database, table FROM system.row_policies WHERE short_name = 'p_tenant')
			ORDER BY 1`)
		if len(missing) > 0 {
			t.Fatalf("tablas con tenant_id sin p_tenant: %v", missing)
		}
	})

	// Usuarios por módulo con contraseñas aleatorias (en despliegue salen de secretos _FILE).
	pw := map[string]string{}
	var users []chmigrate.User
	for _, m := range []string{"ingester", "analytics", "detection", "alerts", "jobs"} {
		name := "horus_" + m
		pw[name] = randomPassword(t)
		users = append(users, chmigrate.User{Name: name, Password: pw[name], Roles: []string{name + "_role"}})
	}
	t.Run("crea usuarios por módulo (idempotente)", func(t *testing.T) {
		for range 2 {
			if err := chmigrate.Provision(ctx, admin, users); err != nil {
				t.Fatal(err)
			}
		}
	})

	tenantA, tenantB := uuid.New(), uuid.New()
	siteA, siteB, realm := uuid.New(), uuid.New(), uuid.New()
	base := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour + 5*time.Minute)

	// Resolver DNS del tenant A antes de insertar flujos (lo lee la MV de seguridad).
	mustExec(ctx, t, admin, "INSERT INTO dim.tenant_resolver (tenant_id, resolver_ip, label, version) VALUES (?, toIPv6('10.255.255.53'), 'isp-dns', 1)", tenantA)
	mustExec(ctx, t, admin, "INSERT INTO dim.site (tenant_id, site_id, name, parent_id, timezone, version) VALUES (?, ?, 'Nodo A', ?, 'UTC', 1)", tenantA, siteA, uuid.Nil)

	fixA := loadFixture(t, "normal")
	rowsA, totA := rowsFromFixture(t, fixA, tenantA, siteA, realm, base)
	fixB := loadFixture(t, "commercial")
	rowsB, totB := rowsFromFixture(t, fixB, tenantB, siteB, realm, base)

	t.Run("insertar un lote de flujos alimenta los agregados", func(t *testing.T) {
		insertFlows(ctx, t, admin, rowsA)
		insertFlows(ctx, t, admin, rowsB)
		checkConsumption(ctx, t, admin, tenantA, totA)
		checkConsumption(ctx, t, admin, tenantB, totB)
	})

	t.Run("señales de seguridad, reputación y no atribuidos", func(t *testing.T) {
		insertFlows(ctx, t, admin, signalRows(tenantA, siteA, realm, base))
		checkSecurity(ctx, t, admin, tenantA)
	})

	t.Run("diccionarios cargan con el usuario interno", func(t *testing.T) {
		mustExec(ctx, t, admin, "SYSTEM RELOAD DICTIONARY dim.site_dict")
		var name string
		if err := admin.QueryRowContext(ctx, "SELECT dictGet('dim.site_dict', 'name', (?, ?))", tenantA, siteA).Scan(&name); err != nil {
			t.Fatal(err)
		}
		if name != "Nodo A" {
			t.Fatalf("dictGet site_dict = %q", name)
		}
		if n := queryUint(ctx, t, admin, "SELECT toUInt64(dictHas('dim.watch_port_dict', (toUInt8(6), toUInt16(23))))"); n != 1 {
			t.Fatal("watch_port_dict sin el puerto 23 por defecto")
		}
	})

	t.Run("row policies aíslan tenants", func(t *testing.T) {
		checkIsolation(ctx, t, tg, admin, pw, tenantA, tenantB, totA, totB)
	})

	t.Run("escritor único y permisos mínimos", func(t *testing.T) {
		checkWriters(ctx, t, tg, admin, pw, siteA, realm)
	})

	t.Run("TTL borra lo caducado al forzar OPTIMIZE FINAL", func(t *testing.T) {
		checkTTL(ctx, t, admin, realm)
	})
}

func migrationFiles(t *testing.T) []string {
	t.Helper()
	entries, err := fs.ReadDir(chschema.Migrations(), ".")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func mustExec(ctx context.Context, t *testing.T, db sqlDB, q string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(ctx, q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func checkLayout(ctx context.Context, t *testing.T, db sqlDB) {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
		SELECT database, name, engine, sorting_key, partition_key, create_table_query
		FROM system.tables
		WHERE database IN ('flows', 'dim') AND engine LIKE '%MergeTree' AND name != 'goose_db_version'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]bool{}
	for rows.Next() {
		var d, name, engine, sortKey, partKey, ddl string
		if err := rows.Scan(&d, &name, &engine, &sortKey, &partKey, &ddl); err != nil {
			t.Fatal(err)
		}
		full := d + "." + name
		seen[full] = true
		hasTenant := strings.Contains(ddl, "`tenant_id` UUID")
		if hasTenant && !strings.HasPrefix(sortKey, "tenant_id") {
			t.Errorf("%s: ORDER BY %q no empieza por tenant_id", full, sortKey)
		}
		if d == "flows" && !strings.Contains(ddl, " TTL ") {
			t.Errorf("%s: sin TTL", full)
		}
		if d == "flows" && partKey == "" {
			t.Errorf("%s: sin PARTITION BY", full)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"flows.flows_raw", "flows.customer_5m", "flows.customer_1h", "flows.customer_1d",
		"flows.site_5m", "flows.site_1h", "flows.site_1d", "flows.unattributed_1h",
		"flows.client_security_1m", "flows.client_security_1h", "flows.client_port_1m",
		"flows.reputation_hit", "flows.flows_coverage",
		"dim.tenant", "dim.site", "dim.router", "dim.interface", "dim.customer", "dim.service",
		"dim.category", "dim.organization", "dim.asn", "dim.watch_port", "dim.tenant_resolver",
	} {
		if !seen[want] {
			t.Errorf("falta la tabla %s", want)
		}
	}
	var partKey string
	if err := db.QueryRowContext(ctx, "SELECT partition_key FROM system.tables WHERE database = 'flows' AND name = 'flows_raw'").Scan(&partKey); err != nil {
		t.Fatal(err)
	}
	if partKey != "toYYYYMMDD(ts)" {
		t.Errorf("flows_raw particiona por %q, se esperaba diaria", partKey)
	}
	mvs := queryStrings(ctx, t, db, "SELECT name FROM system.tables WHERE database = 'flows' AND engine = 'MaterializedView' ORDER BY name")
	want := []string{
		"mv_client_port_1m", "mv_client_security_1h", "mv_client_security_1m", "mv_customer_1d", "mv_customer_1h",
		"mv_customer_5m", "mv_reputation_hit", "mv_site_1d", "mv_site_1h", "mv_site_5m", "mv_unattributed_1h",
	}
	if !slices.Equal(mvs, want) {
		t.Errorf("vistas materializadas = %v, se esperaba %v", mvs, want)
	}
}

func checkConsumption(ctx context.Context, t *testing.T, db sqlDB, tenant uuid.UUID, tot map[string]uint64) {
	t.Helper()
	up, down := tot["bytes_up"], tot["bytes_down"]
	for _, c := range []struct {
		q    string
		want uint64
	}{
		{"SELECT sum(bytes) FROM flows.flows_raw WHERE tenant_id = ? AND direction = 'upload'", up},
		{"SELECT sum(bytes) FROM flows.flows_raw WHERE tenant_id = ? AND direction = 'download'", down},
		{"SELECT sum(bytes) FROM flows.customer_5m WHERE tenant_id = ? AND direction = 'upload'", up},
		{"SELECT sum(bytes) FROM flows.customer_5m WHERE tenant_id = ? AND direction = 'download'", down},
		{"SELECT sum(packets) FROM flows.customer_5m WHERE tenant_id = ? AND direction = 'upload'", tot["packets_up"]},
		{"SELECT sum(flows) FROM flows.customer_5m WHERE tenant_id = ?", tot["flows"]},
		{"SELECT sum(bytes) FROM flows.customer_1h WHERE tenant_id = ? AND direction = 'upload'", up},
		{"SELECT sum(packets) FROM flows.customer_1h WHERE tenant_id = ? AND direction = 'download'", tot["packets_down"]},
		{"SELECT sum(bytes) FROM flows.customer_1d WHERE tenant_id = ?", up + down},
		{"SELECT uniqExact(client_ip) FROM flows.customer_1d WHERE tenant_id = ?", tot["clients"]},
		{"SELECT sum(bytes) FROM flows.site_5m WHERE tenant_id = ?", up + down},
		{"SELECT sum(bytes) FROM flows.site_1h WHERE tenant_id = ?", up + down},
		{"SELECT sum(bytes) FROM flows.site_1d WHERE tenant_id = ?", up + down},
		{"SELECT uniqMerge(clients) FROM flows.site_1d WHERE tenant_id = ?", tot["clients"]},
		{"SELECT sum(up_bytes) FROM flows.client_security_1m WHERE tenant_id = ?", up},
		{"SELECT sum(down_bytes) FROM flows.client_security_1m WHERE tenant_id = ?", down},
		{"SELECT sum(up_bytes) FROM flows.client_security_1h WHERE tenant_id = ?", up},
		{"SELECT sum(flows) FROM flows.client_port_1m WHERE tenant_id = ?", tot["flows"]},
	} {
		if got := queryUint(ctx, t, db, c.q, tenant); got != c.want {
			t.Errorf("%s = %d, se esperaba %d", c.q, got, c.want)
		}
	}
}

// signalRows: un escaneo tipo Mirai (200 SYN a 23/tcp), DNS al resolver del ISP y a terceros, un
// servicio entrante, un contacto con C2 marcado por reputación e IPs fuera de prefijo.
func signalRows(tenant, site, realm uuid.UUID, base time.Time) []flowRow {
	bot := netip.MustParseAddr("10.21.0.5")
	row := func(remote string, rport uint16, proto, flags uint8) flowRow {
		return flowRow{
			tenant: tenant, site: site, router: uuid.Nil, realm: realm, service: uuid.Nil, batch: uuid.Nil,
			ts: base.Add(30 * time.Second), status: "attributed", direction: "upload", reputation: "none",
			clientIP: bot, remoteIP: netip.MustParseAddr(remote), clientPort: 50000, remotePort: rport,
			protocol: proto, tcpFlags: flags, bytes: 60, packets: 1, samplingRate: 1, merged: 1, remoteASN: 64500,
		}
	}
	var rows []flowRow
	for i := range 200 {
		r := row(netip.AddrFrom4([4]byte{100, 64, byte(i / 100), byte(i % 100)}).String(), 23, 6, 0x02)
		rows = append(rows, r)
	}
	for range 3 {
		rows = append(rows, row("10.255.255.53", 53, 17, 0))
	}
	rows = append(rows, row("8.8.8.8", 53, 17, 0), row("1.1.1.1", 853, 6, 0x1b))
	in := row("192.0.2.200", 51000, 6, 0x12)
	in.clientPort = 8080
	rows = append(rows, in)
	c2 := row("192.0.2.66", 6667, 6, 0x1b)
	c2.reputation, c2.packets, c2.bytes = "botnet_cc", 10, 900
	rows = append(rows, c2)
	for range 2 {
		u := row("203.0.113.9", 443, 6, 0x1b)
		u.status, u.realm, u.clientIP, u.bytes = "unknown", uuid.Nil, netip.MustParseAddr("100.100.1.1"), 1000
		rows = append(rows, u)
	}
	return rows
}

func checkSecurity(ctx context.Context, t *testing.T, db sqlDB, tenant uuid.UUID) {
	t.Helper()
	const bot = "client_ip = toIPv6('10.21.0.5')"
	for _, c := range []struct {
		q    string
		want uint64
	}{
		{"SELECT sum(flows_out) FROM flows.client_security_1m WHERE tenant_id = ? AND " + bot, 207},
		{"SELECT sum(syn_only_out) FROM flows.client_security_1m WHERE tenant_id = ? AND " + bot, 200},
		{"SELECT sum(small_flows_out) FROM flows.client_security_1m WHERE tenant_id = ? AND " + bot, 206},
		{"SELECT uniqMerge(remote_ips_out) FROM flows.client_security_1m WHERE tenant_id = ? AND " + bot, 205},
		{"SELECT uniqMerge(remote_nets24_out) FROM flows.client_security_1m WHERE tenant_id = ? AND " + bot, 6},
		{"SELECT uniqMerge(remote_ports_out) FROM flows.client_security_1m WHERE tenant_id = ? AND " + bot, 5},
		{"SELECT sumMap(watch_port_flows)[23] FROM flows.client_security_1m WHERE tenant_id = ? AND " + bot, 200},
		{"SELECT length(mapKeys(sumMap(watch_port_flows))) FROM flows.client_security_1m WHERE tenant_id = ? AND " + bot, 2},
		{"SELECT uniqMerge(inbound_service_ports) FROM flows.client_security_1m WHERE tenant_id = ? AND " + bot, 1},
		{"SELECT sum(dns_flows_isp) FROM flows.client_security_1m WHERE tenant_id = ? AND " + bot, 3},
		{"SELECT sum(dns_flows_other) FROM flows.client_security_1m WHERE tenant_id = ? AND " + bot, 2},
		{"SELECT uniqMerge(dns_resolvers) FROM flows.client_security_1m WHERE tenant_id = ? AND " + bot, 3},
		// La capa de 1 h combina los estados del minuto.
		{"SELECT uniqMerge(remote_ips_out) FROM flows.client_security_1h WHERE tenant_id = ? AND " + bot, 205},
		{"SELECT sumMap(watch_port_flows)[23] FROM flows.client_security_1h WHERE tenant_id = ? AND " + bot, 200},
		{"SELECT sum(syn_only_out) FROM flows.client_security_1h WHERE tenant_id = ? AND " + bot, 200},
		// client_port_1m: puertos vigilados y de amplificación con su número; el resto en 0.
		{"SELECT sum(flows) FROM flows.client_port_1m WHERE tenant_id = ? AND " + bot + " AND remote_port = 23", 200},
		{"SELECT sum(syn_only) FROM flows.client_port_1m WHERE tenant_id = ? AND " + bot + " AND remote_port = 23", 200},
		{"SELECT sum(flows) FROM flows.client_port_1m WHERE tenant_id = ? AND " + bot + " AND remote_port = 53", 4},
		{"SELECT sum(flows) FROM flows.client_port_1m WHERE tenant_id = ? AND " + bot + " AND remote_port = 0", 2},
		{"SELECT sum(flows) FROM flows.client_port_1m WHERE tenant_id = ? AND " + bot + " AND remote_port = 6667", 1},
		{"SELECT sum(flows) FROM flows.reputation_hit WHERE tenant_id = ? AND " + bot + " AND remote_ip = toIPv6('192.0.2.66') AND reputation_category = 'botnet_cc'", 1},
		{"SELECT sum(bytes) FROM flows.reputation_hit WHERE tenant_id = ?", 900},
		{"SELECT sum(bytes) FROM flows.unattributed_1h WHERE tenant_id = ? AND client_ip = toIPv6('100.100.1.1')", 2000},
		// Lo no atribuido no entra en los agregados de cliente pero sí en los del nodo.
		{"SELECT count() FROM flows.customer_5m WHERE tenant_id = ? AND client_ip = toIPv6('100.100.1.1')", 0},
		{"SELECT sum(bytes) FROM flows.site_5m WHERE tenant_id = ? AND remote_asn = 64500 AND direction = 'upload'", 200*60 + 6*60 + 900 + 2000},
	} {
		if got := queryUint(ctx, t, db, c.q, tenant); got != c.want {
			t.Errorf("%s = %d, se esperaba %d", c.q, got, c.want)
		}
	}
}

func checkIsolation(ctx context.Context, t *testing.T, tg target, admin sqlDB, pw map[string]string,
	tenantA, tenantB uuid.UUID, totA, totB map[string]uint64,
) {
	t.Helper()
	settings := func(tenant uuid.UUID) string { return " SETTINGS SQL_horus_tenant = '" + tenant.String() + "'" }
	totalA := totA["bytes_up"] + totA["bytes_down"]
	totalB := totB["bytes_up"] + totB["bytes_down"]
	for _, user := range []string{"horus_analytics", "horus_detection", "horus_alerts"} {
		db := openAs(t, tg, user, pw[user])
		// Sin tenant fijado la consulta se rechaza (fail-closed), aunque no filtre por tenant_id.
		var n uint64
		err := db.QueryRowContext(ctx, "SELECT count() FROM flows.customer_5m").Scan(&n)
		if err == nil || !strings.Contains(err.Error(), "SQL_horus_tenant") {
			t.Errorf("%s: consulta sin SQL_horus_tenant = (%d, %v), se esperaba error", user, n, err)
		}
		// Con tenant fijado solo ve ese tenant aunque no filtre por tenant_id.
		for tenant, want := range map[uuid.UUID]uint64{tenantA: totalA, tenantB: totalB} {
			got := queryUint(ctx, t, db, "SELECT sum(bytes) FROM flows.site_1h"+settings(tenant))
			if tenant == tenantA {
				want += 200*60 + 6*60 + 900 + 2000 // señales del tenant A
			}
			if got != want {
				t.Errorf("%s con tenant %s: sum(bytes) = %d, se esperaba %d", user, tenant, got, want)
			}
			others := queryUint(ctx, t, db, "SELECT count() FROM flows.flows_raw WHERE tenant_id != ?"+settings(tenant), tenant)
			if others != 0 {
				t.Errorf("%s ve %d filas crudas de otros tenants", user, others)
			}
		}
		// Un tenant inexistente no ve nada.
		if got := queryUint(ctx, t, db, "SELECT count() FROM flows.customer_1h"+settings(uuid.New())); got != 0 {
			t.Errorf("%s con tenant desconocido ve %d filas", user, got)
		}
	}
	// jobs (plataforma) ve todos los tenants sin fijar ninguno.
	jobs := openAs(t, tg, "horus_jobs", pw["horus_jobs"])
	all := queryUint(ctx, t, admin, "SELECT count() FROM flows.flows_raw")
	if got := queryUint(ctx, t, jobs, "SELECT count() FROM flows.flows_raw"); got != all || all == 0 {
		t.Errorf("horus_jobs ve %d filas crudas de %d", got, all)
	}
}

func checkWriters(ctx context.Context, t *testing.T, tg target, admin sqlDB, pw map[string]string, site, realm uuid.UUID) {
	t.Helper()
	ing := openAs(t, tg, "horus_ingester", pw["horus_ingester"])
	tenant := uuid.New()
	row := flowRow{
		tenant: tenant, site: site, router: uuid.Nil, realm: realm, service: uuid.Nil, batch: uuid.New(),
		ts: time.Now().UTC(), status: "attributed", direction: "upload", reputation: "none",
		clientIP: netip.MustParseAddr("10.20.9.9"), remoteIP: netip.MustParseAddr("198.51.100.1"),
		clientPort: 40000, remotePort: 443, protocol: 6, tcpFlags: 0x1b, bytes: 1234, packets: 3,
		samplingRate: 1, merged: 1,
	}
	// El ingester inserta en el crudo y las MV (definer horus_mv) pueblan los agregados.
	insertFlows(ctx, t, ing, []flowRow{row})
	if got := queryUint(ctx, t, admin, "SELECT sum(bytes) FROM flows.customer_5m WHERE tenant_id = ?", tenant); got != 1234 {
		t.Errorf("insert del ingester: customer_5m = %d, se esperaba 1234", got)
	}
	denied := func(db sqlDB, who, q string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, q, args...)
		if err == nil || !strings.Contains(err.Error(), "ACCESS_DENIED") && !strings.Contains(err.Error(), "Not enough privileges") {
			t.Errorf("%s: %q debería estar denegado, err = %v", who, q, err)
		}
	}
	denied(ing, "horus_ingester", "SELECT count() FROM flows.flows_raw")
	denied(ing, "horus_ingester", "INSERT INTO dim.site (tenant_id, site_id, name, parent_id, timezone, version) VALUES (?, ?, 'x', ?, 'UTC', 1)", tenant, site, uuid.Nil)
	denied(ing, "horus_ingester", "INSERT INTO flows.customer_5m (tenant_id) VALUES (?)", tenant)
	an := openAs(t, tg, "horus_analytics", pw["horus_analytics"])
	mustExec(ctx, t, an, "INSERT INTO dim.site (tenant_id, site_id, name, parent_id, timezone, version) VALUES (?, ?, 'Nodo X', ?, 'UTC', 1)", tenant, uuid.New(), uuid.Nil)
	denied(an, "horus_analytics", "INSERT INTO flows.flows_coverage (tenant_id, flow_bytes) VALUES (?, 1)", tenant)
	jobs := openAs(t, tg, "horus_jobs", pw["horus_jobs"])
	mustExec(ctx, t, jobs, "INSERT INTO flows.flows_coverage (tenant_id, site_id, router_id, bucket, flow_bytes) VALUES (?, ?, ?, now(), 1)", tenant, site, uuid.Nil)
	det := openAs(t, tg, "horus_detection", pw["horus_detection"])
	denied(det, "horus_detection", "INSERT INTO dim.watch_port (protocol, port, label, version) VALUES (6, 9999, 'x', 9)")
}

func checkTTL(ctx context.Context, t *testing.T, db sqlDB, realm uuid.UUID) {
	t.Helper()
	tenant := uuid.New()
	old := flowRow{
		tenant: tenant, site: uuid.New(), router: uuid.Nil, realm: realm, service: uuid.Nil, batch: uuid.New(),
		ts: time.Now().UTC().Add(-8 * 24 * time.Hour), status: "attributed", direction: "download", reputation: "none",
		clientIP: netip.MustParseAddr("10.20.7.7"), remoteIP: netip.MustParseAddr("198.51.100.7"),
		clientPort: 40000, remotePort: 443, protocol: 6, tcpFlags: 0x1b, bytes: 777, packets: 2, samplingRate: 1, merged: 1,
	}
	insertFlows(ctx, t, db, []flowRow{old})
	mustExec(ctx, t, db, "OPTIMIZE TABLE flows.flows_raw FINAL")
	if got := queryUint(ctx, t, db, "SELECT count() FROM flows.flows_raw WHERE tenant_id = ?", tenant); got != 0 {
		t.Errorf("flows_raw conserva %d filas de hace 8 días (TTL 7 d)", got)
	}
	// El agregado de 5 min (TTL 90 d) sí la conserva.
	if got := queryUint(ctx, t, db, "SELECT sum(bytes) FROM flows.customer_5m WHERE tenant_id = ?", tenant); got != 777 {
		t.Errorf("customer_5m = %d, se esperaba 777", got)
	}
	mustExec(ctx, t, db, "INSERT INTO flows.customer_5m (tenant_id, client_ip, bucket, bytes) VALUES (?, toIPv6('10.20.7.7'), now() - INTERVAL 91 DAY, 5)", tenant)
	mustExec(ctx, t, db, "OPTIMIZE TABLE flows.customer_5m FINAL")
	if got := queryUint(ctx, t, db, "SELECT sum(bytes) FROM flows.customer_5m WHERE tenant_id = ?", tenant); got != 777 {
		t.Errorf("customer_5m tras OPTIMIZE = %d, se esperaba 777 (fila de hace 91 d borrada)", got)
	}
}
