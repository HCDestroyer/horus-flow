package chschema_test

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	chschema "github.com/hcdestroyer/horus-flow/services/ingester/migrations/clickhouse"
)

// Reglas verificables sin servidor (docs/conventions.md §6: migraciones con marca de tiempo UTC;
// docs/database.md §3 y §5). La verificación contra ClickHouse real está en
// schema_integration_test.go (build tag integration).

var (
	nameRe   = regexp.MustCompile(`^(\d{14})_[a-z0-9_]+\.sql$`)
	createRe = regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS (\w+\.\w+)(.*?)ENGINE = .*?ORDER BY \(?([\w, ]+)\)?`)
)

func readMigrations(t *testing.T) map[string]string {
	t.Helper()
	fsys := chschema.Migrations()
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		b, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(b)
	}
	if len(out) == 0 {
		t.Fatal("no hay migraciones embebidas")
	}
	return out
}

func TestMigrationFiles(t *testing.T) {
	t.Parallel()
	for name, sql := range readMigrations(t) {
		if !nameRe.MatchString(name) {
			t.Errorf("%s: el nombre debe ser <AAAAMMDDhhmmss>_<desc>.sql", name)
		}
		for _, ann := range []string{"-- +goose NO TRANSACTION", "-- +goose Up", "-- +goose Down"} {
			if !strings.Contains(sql, ann) {
				t.Errorf("%s: falta la anotación %q", name, ann)
			}
		}
		up, _, _ := strings.Cut(sql, "-- +goose Down")
		for _, kw := range []string{"CREATE TABLE ", "CREATE MATERIALIZED VIEW ", "CREATE DICTIONARY ", "CREATE ROLE ", "CREATE USER ", "CREATE ROW POLICY "} {
			for _, line := range strings.Split(up, "\n") {
				if strings.HasPrefix(line, kw) && !strings.HasPrefix(line, kw+"IF NOT EXISTS") {
					t.Errorf("%s: %q no es idempotente (falta IF NOT EXISTS)", name, line)
				}
			}
		}
	}
}

func TestTenantFirstAndTTL(t *testing.T) {
	t.Parallel()
	tables := 0
	for name, sql := range readMigrations(t) {
		up, _, _ := strings.Cut(sql, "-- +goose Down")
		for _, stmt := range strings.Split(up, ";") {
			m := createRe.FindStringSubmatch(stmt)
			if m == nil {
				continue
			}
			tables++
			table, cols, orderBy := m[1], m[2], strings.TrimSpace(m[3])
			tenantScoped := strings.Contains(cols, "tenant_id") || strings.Contains(stmt, " AS flows.")
			if tenantScoped && !strings.HasPrefix(orderBy, "tenant_id") {
				t.Errorf("%s: %s ORDER BY (%s) no empieza por tenant_id", name, table, orderBy)
			}
			if strings.HasPrefix(table, "flows.") && !strings.Contains(stmt, "\nTTL ") {
				t.Errorf("%s: %s sin TTL", name, table)
			}
		}
	}
	if tables < 20 {
		t.Fatalf("solo se reconocieron %d CREATE TABLE; ¿cambió el formato?", tables)
	}
}
