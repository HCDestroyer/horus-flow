//go:build integration

package chschema_test

import (
	"bufio"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/chmigrate"
)

// Conexión de los tests de integración. Por orden de preferencia:
//
//   - HORUS_CH_TEST_DSN (+ HORUS_CH_TEST_PASSWORD_FILE): un ClickHouse cualquiera.
//   - deployments/compose/.env + secrets/clickhouse_password.txt: el compose de `make up`.
//
// AVISO: los tests borran y recrean las bases flows y dim, y los usuarios y roles horus_* del
// ClickHouse de destino. Úsalos solo contra el compose de desarrollo o un contenedor efímero.
type target struct {
	dsn      string
	password string
	host     string // host:puerto nativo, para abrir conexiones con otros usuarios
	db       string
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", "..", "..", ".."))
}

func discover(t *testing.T) target {
	t.Helper()
	if dsn := os.Getenv("HORUS_CH_TEST_DSN"); dsn != "" {
		var pw string
		if f := os.Getenv("HORUS_CH_TEST_PASSWORD_FILE"); f != "" {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read HORUS_CH_TEST_PASSWORD_FILE: %v", err)
			}
			pw = strings.TrimSpace(string(b))
		}
		u := strings.TrimPrefix(dsn, "clickhouse://")
		host, db, _ := strings.Cut(u[strings.LastIndex(u, "@")+1:], "/")
		return target{dsn: dsn, password: pw, host: host, db: strings.Split(db, "?")[0]}
	}
	root := repoRoot(t)
	env, err := readEnv(filepath.Join(root, "deployments", "compose", ".env"))
	if err != nil {
		t.Skipf("sin HORUS_CH_TEST_DSN ni deployments/compose/.env (ejecuta `make up`): %v", err)
	}
	pw, err := os.ReadFile(filepath.Join(root, "deployments", "compose", "secrets", "clickhouse_password.txt"))
	if err != nil {
		t.Skipf("sin secreto de ClickHouse del compose: %v", err)
	}
	addr := env["HORUS_BIND_ADDR"]
	if addr == "" || addr == "0.0.0.0" {
		addr = "127.0.0.1"
	}
	port := env["HORUS_CH_NATIVE_PORT"]
	if port == "" {
		port = "9000"
	}
	host := net.JoinHostPort(addr, port)
	return target{
		dsn:      fmt.Sprintf("clickhouse://%s@%s/%s", env["HORUS_CH_USER"], host, env["HORUS_CH_DB"]),
		password: strings.TrimSpace(string(pw)),
		host:     host,
		db:       env["HORUS_CH_DB"],
	}
}

func readEnv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if ok {
			out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return out, sc.Err()
}

func openAdmin(t *testing.T, tg target) *sql.DB {
	t.Helper()
	db, err := chmigrate.Open(tg.dsn, tg.password)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ClickHouse no responde en %s (¿`make up`?): %v", tg.host, err)
	}
	return db
}

func openAs(t *testing.T, tg target, user, password string) *sql.DB {
	t.Helper()
	db, err := chmigrate.Open(fmt.Sprintf("clickhouse://%s@%s/%s", user, tg.host, tg.db), password)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// resetSchema deja el servidor como recién instalado para el esquema de Horus.
func resetSchema(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.QueryContext(ctx,
		"SELECT short_name, database, table FROM system.row_policies WHERE database IN ('flows', 'dim')")
	if err != nil {
		t.Fatal(err)
	}
	var drops []string
	for rows.Next() {
		var name, d, tbl string
		if err := rows.Scan(&name, &d, &tbl); err != nil {
			t.Fatal(err)
		}
		drops = append(drops, fmt.Sprintf("DROP ROW POLICY IF EXISTS %s ON %s.%s", name, d, tbl))
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	drops = append(drops,
		"DROP DATABASE IF EXISTS flows SYNC",
		"DROP DATABASE IF EXISTS dim SYNC",
	)
	for _, q := range []string{"user", "role"} {
		names := queryStrings(ctx, t, db, fmt.Sprintf("SELECT name FROM system.%ss WHERE name LIKE 'horus\\_%%'", q))
		for _, n := range names {
			drops = append(drops, fmt.Sprintf("DROP %s IF EXISTS %s", strings.ToUpper(q), n))
		}
	}
	for _, q := range drops {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func queryStrings(ctx context.Context, t *testing.T, db *sql.DB, q string, args ...any) []string {
	t.Helper()
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func queryUint(ctx context.Context, t *testing.T, db *sql.DB, q string, args ...any) uint64 {
	t.Helper()
	var v uint64
	if err := db.QueryRowContext(ctx, q, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return v
}

func randomPassword(t *testing.T) string {
	t.Helper()
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

type sqlDB = *sql.DB

// ---------------------------------------------------------------- filas de flows_raw

type flowRow struct {
	tenant, site, router, realm, service, batch uuid.UUID
	ts                                          time.Time
	status, direction, reputation               string
	clientIP, remoteIP                          netip.Addr
	clientPort, remotePort                      uint16
	protocol, tcpFlags                          uint8
	bytes, packets                              uint64
	samplingRate                                uint32
	merged                                      uint16
	remoteASN                                   uint32
}

func ip16(a netip.Addr) net.IP {
	b := a.As16()
	return net.IP(b[:])
}

func insertFlows(ctx context.Context, t *testing.T, db *sql.DB, rows []flowRow) {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO flows.flows_raw (
		tenant_id, ts, flow_start, received_at, site_id, router_id, realm_id, attribution_status, direction,
		client_ip, client_port, remote_ip, remote_port, protocol, tcp_flags, bytes, packets, sampling_rate,
		merged_flows, flow_source, remote_asn, service_id, reputation_category, reputation_source_id,
		reputation_confidence, reputation_version, batch_id)`)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		var src uint16
		var conf uint8
		var ver uint32
		if r.reputation != "none" {
			src, conf, ver = 7, 90, 3
		}
		if _, err := stmt.ExecContext(ctx,
			r.tenant, r.ts, r.ts.Add(-10*time.Second), r.ts.Add(time.Second), r.site, r.router, r.realm, r.status, r.direction,
			ip16(r.clientIP), r.clientPort, ip16(r.remoteIP), r.remotePort, r.protocol, r.tcpFlags, r.bytes, r.packets, r.samplingRate,
			r.merged, "ipfix", r.remoteASN, r.service, r.reputation, src, conf, ver, r.batch,
		); err != nil {
			t.Fatalf("append row: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("insert flows_raw: %v", err)
	}
}

// fixtureClient es el resumen por cliente del expected.json del simulador (tools/flowsim, I0-10).
type fixtureClient struct {
	Key         string `json:"key"`
	Family      int    `json:"family"`
	Records     int    `json:"records"`
	BytesUp     uint64 `json:"bytes_up"`
	BytesDown   uint64 `json:"bytes_down"`
	PacketsUp   uint64 `json:"packets_up"`
	PacketsDown uint64 `json:"packets_down"`
}

type fixture struct {
	Scenario        string `json:"scenario"`
	DurationSeconds int    `json:"duration_seconds"`
	Exporters       []struct {
		Name    string          `json:"name"`
		Clients []fixtureClient `json:"clients"`
	} `json:"exporters"`
}

func loadFixture(t *testing.T, scenario string) fixture {
	t.Helper()
	p := filepath.Join(repoRoot(t), "tools", "flowsim", "fixtures", "sim", scenario, "ipfix.expected.json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("fixture del simulador: %v", err)
	}
	var f fixture
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func clientAddr(t *testing.T, key string) netip.Addr {
	t.Helper()
	if p, err := netip.ParsePrefix(key); err == nil {
		return p.Masked().Addr()
	}
	a, err := netip.ParseAddr(key)
	if err != nil {
		t.Fatalf("clave de cliente %q: %v", key, err)
	}
	return a
}

// split reparte total en n partes que suman exactamente total.
func split(total uint64, n int) []uint64 {
	out := make([]uint64, n)
	for i := range out {
		out[i] = total / uint64(n)
	}
	out[0] += total % uint64(n)
	return out
}

// rowsFromFixture convierte los totales por cliente del simulador en filas de flows_raw ya
// enriquecidas (lo que haría el ingester): mismo número de registros y mismos bytes/paquetes por
// sentido, repartidos en la duración del escenario a partir de base.
func rowsFromFixture(t *testing.T, f fixture, tenant, site, realm uuid.UUID, base time.Time) ([]flowRow, map[string]uint64) {
	t.Helper()
	router, service, batch := uuid.New(), uuid.New(), uuid.New()
	totals := map[string]uint64{}
	var rows []flowRow
	for _, c := range f.Exporters[0].Clients {
		ip := clientAddr(t, c.Key)
		up := (c.Records + 1) / 2
		down := c.Records - up
		if down == 0 && c.BytesDown > 0 {
			down = 1
		}
		if up == 0 {
			up = 1
		}
		mk := func(dir string, n int, bytes, packets uint64) {
			bs, ps := split(bytes, n), split(packets, n)
			for i := range n {
				remote := netip.AddrFrom4([4]byte{198, 51, 100, byte(i % 250)})
				if ip.Is6() && !ip.Is4In6() {
					remote = netip.MustParseAddr(fmt.Sprintf("2001:db8:ffff::%x", i+1))
				}
				rows = append(rows, flowRow{
					tenant: tenant, site: site, router: router, realm: realm, service: service, batch: batch,
					ts:         base.Add(time.Duration(i*f.DurationSeconds/n) * time.Second),
					status:     "attributed", direction: dir, reputation: "none",
					clientIP:   ip, remoteIP: remote, clientPort: uint16(40000 + i), remotePort: 443,
					protocol:   6, tcpFlags: 0x1b, bytes: bs[i], packets: ps[i], samplingRate: 1, merged: 1,
					remoteASN:  15169,
				})
			}
		}
		mk("upload", up, c.BytesUp, c.PacketsUp)
		mk("download", down, c.BytesDown, c.PacketsDown)
		totals["bytes_up"] += c.BytesUp
		totals["bytes_down"] += c.BytesDown
		totals["packets_up"] += c.PacketsUp
		totals["packets_down"] += c.PacketsDown
		totals["flows"] += uint64(up + down)
		totals["clients"]++
	}
	return rows, totals
}
