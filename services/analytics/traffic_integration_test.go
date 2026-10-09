//go:build integration

package analytics_test

import (
	"context"
	"log/slog"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/config"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/services/analytics"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/catalog"
)

func num(t *testing.T, v any) uint64 {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		t.Fatalf("not a Uint64String: %v", v)
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// trafficRows: 12 servicios con bytes decrecientes y 15 clientes en la
// última hora, más 2 h de tráfico continuo con un hueco de 20 min.
func trafficRows(e *env, now time.Time) (rows []row, totalDown, totalUp uint64) {
	svcs := []string{"netflix", "youtube", "meta_generic", "whatsapp", "zoom", "spotify", "tiktok", "steam",
		"twitch", "discord", "telegram", "web_https"}
	for i, s := range svcs {
		for c := range 15 {
			down, up := uint64((12-i)*1000+c), uint64((12-i)*100)
			ip := netip.AddrFrom4([4]byte{10, 20, 0, byte(c + 1)})
			rows = append(rows,
				row{ts: now.Add(-30 * time.Minute), realm: e.realm, status: "attributed", direction: "download", client: ip,
					bytes: down, packets: 1, service: catalog.ID("service", s), asn: 2906},
				row{ts: now.Add(-30 * time.Minute), realm: e.realm, status: "attributed", direction: "upload", client: ip,
					bytes: up, packets: 1, service: catalog.ID("service", s), asn: 2906})
			totalDown += down
			totalUp += up
		}
	}
	return rows, totalDown, totalUp
}

// TestTrafficAPI cubre I1-08: top N + Otros coherente con los totales, top de
// clientes con permiso de datos personales, series de 24 h con agregados de
// 5 min y huecos como null (partial), aislamiento por ISP y 503 sin ClickHouse.
func TestTrafficAPI(t *testing.T) {
	e := setup(t, flowinv.Data{})
	now := time.Now().UTC()
	rows, totalDown, totalUp := trafficRows(e, now)
	// Serie: cada 5 min durante 2 h salvo un hueco de 20 min (exportador silencioso).
	for m := 120; m >= 10; m -= 5 {
		if m <= 70 && m > 50 {
			continue
		}
		rows = append(rows, row{ts: now.Add(-time.Duration(m) * time.Minute), status: "unknown", direction: "download", bytes: 600, packets: 1})
	}
	e.insert(rows)
	read := map[string][]string{"traffic.read": all}

	code, body := e.get("/api/v1/analytics/traffic/top?dimension=services&n=5&range=1h", e.token(read, "tenant"))
	if code != 200 {
		t.Fatalf("top = %d %v", code, body)
	}
	data := body["data"].(map[string]any)
	rs := data["rows"].([]any)
	if len(rs) != 5 {
		t.Fatalf("rows = %d", len(rs))
	}
	first := rs[0].(map[string]any)
	if first["label"] != "Netflix" || first["key"] != catalog.ID("service", "netflix").String() {
		t.Fatalf("first row %v", first)
	}
	var sumDown, sumUp uint64
	for _, r := range rs {
		m := r.(map[string]any)
		sumDown += num(t, m["down_bytes"])
		sumUp += num(t, m["up_bytes"])
	}
	others := data["others"].(map[string]any)
	totals := data["totals"].(map[string]any)
	sumDown += num(t, others["down_bytes"])
	sumUp += num(t, others["up_bytes"])
	// totals incluye además el tráfico "unknown" de la serie (cuenta para el nodo).
	if sumDown != num(t, totals["down_bytes"]) || sumUp != num(t, totals["up_bytes"]) || num(t, totals["up_bytes"]) != totalUp {
		t.Fatalf("rows+others (%d/%d) != totals %v (escenario %d/%d)", sumDown, sumUp, totals, totalDown, totalUp)
	}

	// Categorías, organizaciones y ASN.
	for _, dim := range []string{"categories", "organizations", "asns"} {
		code, body := e.get("/api/v1/analytics/traffic/top?dimension="+dim+"&range=1h", e.token(read, "tenant"))
		if code != 200 || len(body["data"].(map[string]any)["rows"].([]any)) == 0 {
			t.Fatalf("top %s = %d %v", dim, code, body)
		}
	}

	// Top de clientes: exige customers.read.
	if code, _ := e.get("/api/v1/analytics/traffic/top?dimension=customers&range=1h", e.token(read, "tenant")); code != 403 {
		t.Fatalf("customers without customers.read = %d", code)
	}
	code, body = e.get("/api/v1/analytics/traffic/top?dimension=customers&n=3&range=1h",
		e.token(map[string][]string{"traffic.read": all, "customers.read": all}, "tenant"))
	if code != 200 {
		t.Fatalf("top customers = %d %v", code, body)
	}
	c0 := body["data"].(map[string]any)["rows"].([]any)[0].(map[string]any)
	if c0["customer"].(map[string]any)["address"] != "10.20.0.15" {
		t.Fatalf("top customer %v", c0)
	}

	// Serie de 24 h: paso de 5 min y el hueco como null (no cero), partial.
	code, body = e.get("/api/v1/analytics/traffic/timeseries?range=24h", e.token(read, "tenant"))
	if code != 200 {
		t.Fatalf("timeseries = %d %v", code, body)
	}
	meta := body["meta"].(map[string]any)
	if meta["step"] != 300.0 || meta["partial"] != true {
		t.Fatalf("meta %v", meta)
	}
	pts := body["data"].(map[string]any)["series"].([]any)[0].(map[string]any)["points"].([]any)
	var nulls, values int
	for _, p := range pts {
		if p.([]any)[1] == nil {
			nulls++
		} else {
			values++
			if p.([]any)[1].(float64) <= 0 {
				t.Fatal("zero instead of gap")
			}
		}
	}
	if values < 15 || nulls < 4 {
		t.Fatalf("points: %d values, %d nulls", values, nulls)
	}

	// Atribución.
	code, body = e.get("/api/v1/analytics/traffic/attribution?range=24h", e.token(read, "tenant"))
	if code != 200 || body["data"].(map[string]any)["attributed_ratio"].(float64) <= 0.5 {
		t.Fatalf("attribution = %d %v", code, body)
	}

	// Otro ISP: su token no ve nada de este, y un nodo ajeno es 404.
	other := setupTenantToken(e, uuid.New(), read)
	code, body = e.get("/api/v1/analytics/traffic/top?dimension=services&range=1h", other)
	if code != 200 || len(body["data"].(map[string]any)["rows"].([]any)) != 0 {
		t.Fatalf("other tenant sees data: %d %v", code, body)
	}
	if code, _ := e.get("/api/v1/analytics/traffic/top?dimension=services&site_id="+e.site.String(), other); code != 404 {
		t.Fatalf("other tenant site = %d", code)
	}
}

func setupTenantToken(e *env, tenant uuid.UUID, perms map[string][]string) string {
	saved := e.tenant
	e.tenant = tenant
	defer func() { e.tenant = saved }()
	return e.token(perms, "tenant")
}

// TestAnalyticsUnavailable: con ClickHouse caído, 503 ANALYTICS_UNAVAILABLE (I1-08 criterio 4).
func TestAnalyticsUnavailable(t *testing.T) {
	e := setup(t, flowinv.Data{})
	mux := httpx.NewMux(nil, slog.New(slog.DiscardHandler))
	mod, err := analytics.Register(context.Background(), module.Deps{Logger: slog.New(slog.DiscardHandler), Common: config.Common{Env: "dev"},
		Routes: mux.ForService("analytics"), Services: e.services, Environ: []string{
			"HORUS_CLICKHOUSE_DSN=clickhouse://horus@127.0.0.1:1/horus", "HORUS_ANALYTICS_QUERY_TIMEOUT=2s"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := mod.(module.Starter).Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.handler = mux.Handler()
	code, body := e.get("/api/v1/analytics/traffic/top?dimension=services", e.token(map[string][]string{"traffic.read": all}, "tenant"))
	if code != 503 || body["code"] != "ANALYTICS_UNAVAILABLE" {
		t.Fatalf("down = %d %v", code, body)
	}
}
