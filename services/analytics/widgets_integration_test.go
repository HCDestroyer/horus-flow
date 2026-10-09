//go:build integration

package analytics_test

import (
	"context"
	"encoding/json"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/chmigrate"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	tw "github.com/hcdestroyer/horus-flow/services/analytics/api/trafficwidgets"
)

func provider(t *testing.T, e *env) tw.Provider {
	t.Helper()
	p, ok := module.Lookup[tw.Provider](e.services, tw.ServiceWidgetData)
	if !ok {
		t.Fatal("widget data provider not registered")
	}
	return p
}

func asJSON(t *testing.T, d *tw.WidgetData) map[string]any {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// TestWidgetData resuelve los widgets de tráfico con las formas de C9 y de
// apps/frontend/app/widgets/shapes.ts; sin datos personales las IPs llegan
// enmascaradas (criterio 7) y diez kioscos ⇒ una sola consulta (criterio 6).
func TestWidgetData(t *testing.T) {
	e := setup(t, flowinv.Data{})
	now := time.Now().UTC()
	rows, _, _ := trafficRows(e, now)
	for m := 1; m <= 50; m++ {
		rows = append(rows, row{ts: now.Add(-time.Duration(m)*time.Minute + time.Second), realm: e.realm, status: "attributed",
			direction: "download", client: netip.MustParseAddr("10.20.0.1"), bytes: 75_000_000, packets: 10})
	}
	e.insert(rows)
	p := provider(t, e)
	ctx := context.Background()
	if len(p.Types()) != 8 {
		t.Fatalf("types = %v", p.Types())
	}

	// traffic_now
	d, err := p.Resolve(ctx, tw.Request{TenantID: e.tenant, Type: "traffic_now", Config: json.RawMessage(`{"compare":"yesterday"}`), ShowPersonalData: true})
	if err != nil {
		t.Fatal(err)
	}
	m := asJSON(t, d)
	vals := m["data"].(map[string]any)["values"].(map[string]any)
	for _, k := range []string{"down_bps", "up_bps", "down_bps_yesterday", "up_bps_yesterday", "flows_per_second", "sparkline"} {
		if _, ok := vals[k]; !ok {
			t.Fatalf("traffic_now without %s: %v", k, vals)
		}
	}
	if vals["down_bps"] != 10_000_000.0 { // 75 MB/min ⇒ 10 Mb/s
		t.Fatalf("down_bps = %v", vals["down_bps"])
	}
	sp := vals["sparkline"].(map[string]any)
	if sp["step_seconds"] != 60.0 || len(sp["down_bps"].([]any)) != 60 {
		t.Fatalf("sparkline %v", sp)
	}
	if m["meta"].(map[string]any)["data_endpoint_kind"] != "state" {
		t.Fatalf("meta %v", m["meta"])
	}

	// customers_active
	d, err = p.Resolve(ctx, tw.Request{TenantID: e.tenant, Type: "customers_active"})
	if err != nil {
		t.Fatal(err)
	}
	vals = asJSON(t, d)["data"].(map[string]any)["values"].(map[string]any)
	if vals["active"] != 1.0 || vals["total"] != 15.0 || vals["new_today"] != 15.0 {
		t.Fatalf("customers_active %v", vals)
	}

	// exporters_status: sin estado del collector ⇒ pending_configuration.
	d, err = p.Resolve(ctx, tw.Request{TenantID: e.tenant, Type: "exporters_status"})
	if err != nil {
		t.Fatal(err)
	}
	tbl := asJSON(t, d)["data"].(map[string]any)
	if r := tbl["rows"].([]any); len(r) != 1 || r[0].(map[string]any)["state"] != "pending_configuration" || r[0].(map[string]any)["router"] != "rt-centro" {
		t.Fatalf("exporters_status %v", tbl)
	}

	// traffic_timeseries
	d, err = p.Resolve(ctx, tw.Request{TenantID: e.tenant, Type: "traffic_timeseries", Config: json.RawMessage(`{"range":"6h"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if s := asJSON(t, d)["data"].(map[string]any)["series"].([]any); len(s) != 2 {
		t.Fatalf("series %v", s)
	}

	// top_services / top_categories / top_organizations
	for _, typ := range []string{"top_services", "top_categories", "top_organizations"} {
		d, err = p.Resolve(ctx, tw.Request{TenantID: e.tenant, Type: typ, Config: json.RawMessage(`{"range":"1h","n":3}`)})
		if err != nil {
			t.Fatal(err)
		}
		tbl := asJSON(t, d)["data"].(map[string]any)
		r := tbl["rows"].([]any)
		if len(r) == 0 || len(r) > 3 || tbl["others"].(map[string]any)["label"] != "Otros" {
			t.Fatalf("%s %v", typ, tbl)
		}
		if _, ok := r[0].(map[string]any)["down_bytes"].(float64); !ok {
			t.Fatalf("%s row %v", typ, r[0])
		}
	}

	// top_customers sin datos personales: nunca la IP completa.
	d, err = p.Resolve(ctx, tw.Request{TenantID: e.tenant, Type: "top_customers", Config: json.RawMessage(`{"range":"1h"}`), ShowPersonalData: false})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(d)
	if strings.Contains(string(b), "10.20.0.1\"") || strings.Contains(string(b), "10.20.0.15\"") || !d.Meta.MaskedPersonalData {
		t.Fatalf("personal data leaked: %s", b)
	}
	if ip := d.Data.Rows[0]["customer_ip"]; ip != "10.20.0.•••" {
		t.Fatalf("masked ip = %v", ip)
	}
	d, err = p.Resolve(ctx, tw.Request{TenantID: e.tenant, Type: "top_customers", Config: json.RawMessage(`{"range":"1h"}`), ShowPersonalData: true})
	if err != nil || d.Data.Rows[0]["customer_ip"] != "10.20.0.1" {
		t.Fatalf("unmasked top customer %v %v", err, d.Data.Rows)
	}

	// Diez kioscos con el mismo widget en la misma ventana ⇒ una consulta.
	db, err := chmigrate.Open(e.srv.DSN, e.srv.Password)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	queries := func() uint64 {
		if _, err := db.ExecContext(ctx, "SYSTEM FLUSH LOGS"); err != nil {
			t.Fatal(err)
		}
		var n uint64
		if err := db.QueryRowContext(ctx, `SELECT count() FROM system.query_log WHERE type = 'QueryFinish'
			AND query LIKE '%flows.site_5m%' AND query LIKE ? AND query NOT LIKE '%query_log%'`, "%"+e.tenant.String()+"%").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := queries()
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Resolve(ctx, tw.Request{TenantID: e.tenant, Type: "top_services", Config: json.RawMessage(`{"range":"1h","n":4}`)}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := queries() - before; n != 1 {
		t.Fatalf("ClickHouse queries for 10 kiosks = %d, want 1 (cache)", n)
	}

	// Tipo ajeno a FLOW.
	if _, err := p.Resolve(ctx, tw.Request{TenantID: e.tenant, Type: "findings_feed"}); err == nil {
		t.Fatal("unsupported type resolved")
	}
}
