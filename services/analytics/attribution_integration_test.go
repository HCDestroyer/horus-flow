//go:build integration

package analytics_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/chmigrate"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
)

// TestAttributionFromAggregate: GET /traffic/attribution da las proporciones
// por estado de atribución del rango pedido, solo del ISP del token y del nodo
// filtrado, y las lee de flows.attribution_5m sin tocar flows_raw (en el
// nocturno, la consulta sobre flows_raw leía todo el día del ISP: p95 > 500 ms
// a 5 000 flujos/s, tests/load/REPORT.md).
func TestAttributionFromAggregate(t *testing.T) {
	e := setup(t, flowinv.Data{})
	now := time.Now().UTC()
	in := now.Add(-20 * time.Minute)
	other := uuid.New() // otro nodo del mismo ISP
	e.insert([]row{
		{ts: in, realm: e.realm, status: "attributed", direction: "download", bytes: 6000, packets: 1},
		{ts: in, realm: e.realm, status: "internal", direction: "internal", bytes: 1000, packets: 1},
		{ts: in, status: "infrastructure", direction: "download", bytes: 2000, packets: 1},
		{ts: in, status: "unknown", direction: "download", bytes: 500, packets: 1},
		{ts: in, status: "transit", direction: "download", bytes: 500, packets: 1},
		// Fuera de range=1h (dentro de 24 h).
		{ts: now.Add(-3 * time.Hour), status: "unknown", direction: "download", bytes: 90000, packets: 1},
		// Otro nodo: cuenta para el ISP, no para site_id=e.site.
		{ts: in, site: other, status: "unknown", direction: "download", bytes: 10000, packets: 1},
	})
	read := map[string][]string{"traffic.read": all}
	ratios := func(path, tok string) (a, i, u float64) {
		t.Helper()
		code, body := e.get(path, tok)
		if code != 200 {
			t.Fatalf("%s = %d %v", path, code, body)
		}
		d := body["data"].(map[string]any)
		return d["attributed_ratio"].(float64), d["infrastructure_ratio"].(float64), d["unattributed_ratio"].(float64)
	}
	near := func(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

	tok := e.token(read, "tenant")
	if a, i, u := ratios("/api/v1/analytics/traffic/attribution?range=1h&site_id="+e.site.String(), tok); !near(a, 0.7) || !near(i, 0.2) || !near(u, 0.1) {
		t.Fatalf("1h nodo: atribuido %v, infraestructura %v, fuera %v; quería 0.7/0.2/0.1", a, i, u)
	}
	// Todo el ISP en 1 h: 20 000 bytes (7 000 atribuidos, 2 000 infra, 11 000 fuera).
	if a, i, u := ratios("/api/v1/analytics/traffic/attribution?range=1h", tok); !near(a, 0.35) || !near(i, 0.1) || !near(u, 0.55) {
		t.Fatalf("1h ISP: %v/%v/%v; quería 0.35/0.1/0.55", a, i, u)
	}
	// 24 h (defecto): suma la fila de hace 3 h: 110 000 bytes.
	if a, _, u := ratios("/api/v1/analytics/traffic/attribution", tok); !near(a, 7000.0/110000) || !near(u, 101000.0/110000) {
		t.Fatalf("24h: atribuido %v, fuera %v", a, u)
	}
	// Otro ISP no ve nada.
	if a, i, u := ratios("/api/v1/analytics/traffic/attribution?range=24h", setupTenantToken(e, uuid.New(), read)); a != 0 || i != 0 || u != 0 {
		t.Fatalf("otro ISP ve %v/%v/%v", a, i, u)
	}

	// Ninguna de esas consultas leyó flows_raw.
	ctx := context.Background()
	db, err := chmigrate.Open(e.srv.DSN, e.srv.Password)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, "SYSTEM FLUSH LOGS"); err != nil {
		t.Fatal(err)
	}
	var raw, agg uint64
	if err := db.QueryRowContext(ctx, `SELECT countIf(query LIKE '%flows.flows_raw%'), countIf(query LIKE '%flows.attribution_5m%')
		FROM system.query_log WHERE type = 'QueryFinish' AND query_kind = 'Select' AND query LIKE '%attribution_status%'
		AND query NOT LIKE '%query_log%'`).Scan(&raw, &agg); err != nil {
		t.Fatal(err)
	}
	if raw != 0 || agg < 4 {
		t.Fatalf("consultas de atribución: %d sobre flows_raw, %d sobre attribution_5m", raw, agg)
	}
}
