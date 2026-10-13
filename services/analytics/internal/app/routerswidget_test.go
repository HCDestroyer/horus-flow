package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	tw "github.com/hcdestroyer/horus-flow/services/analytics/api/trafficwidgets"
	snmpapi "github.com/hcdestroyer/horus-flow/services/snmp/api"
)

func f64(v float64) *float64 { return &v }

func statusRows(now time.Time) []snmpapi.RouterStatus {
	reason := snmpapi.ReasonCPUHigh
	exp := "online"
	since := now.Add(-time.Hour)
	up := 3
	return []snmpapi.RouterStatus{
		{RouterID: uuid.New(), SiteID: uuid.New(), Name: "rt-sur", SiteName: "Sur", Status: snmpapi.StatusWarning, Reason: &reason,
			Since: &since, LastObservedAt: &now, Signals: snmpapi.Signals{WireGuard: "ok", Exporter: &exp},
			Device: snmpapi.DeviceSample{CPUPercent: f64(91), MemoryPercent: f64(40)},
			Uplink: &snmpapi.Uplink{Name: "sfp1", RxBps: f64(100), TxBps: f64(50)}, InterfacesUp: &up, InterfacesTotal: &up},
		{RouterID: uuid.New(), SiteID: uuid.New(), Name: "rt-centro", SiteName: "Centro", Status: snmpapi.StatusOnline,
			Device: snmpapi.DeviceSample{CPUPercent: f64(12)}, Uplink: &snmpapi.Uplink{Name: "ether1", RxBps: f64(900), TxBps: nil}},
		{RouterID: uuid.New(), SiteID: uuid.New(), Name: "rt-norte", SiteName: "Norte", Status: snmpapi.StatusOffline},
	}
}

func names(d *tw.WidgetData) []string {
	out := make([]string, len(d.Data.Rows))
	for i, r := range d.Data.Rows {
		out[i] = r["router"].(string)
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// routers_status_grid: columnas exactas del contrato con el frontend, orden
// de List por defecto, filtro show, orden por name/cpu/traffic con los
// routers sin muestra al final, freshness desde el sondeador.
func TestRoutersStatusGrid(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	last := now.Add(-45 * time.Second)
	rd := &fakeReader{list: statusRows(now), health: snmpapi.PollerHealth{Healthy: true, LastCycleAt: &last}}
	svc := &Service{Q: &routerQ{}, Now: func() time.Time { return now }, Routers: func() snmpapi.RouterStatusReader { return rd }}
	w := NewWidgets(svc, time.Second)
	ctx := context.Background()
	resolve := func(cfg string) *tw.WidgetData {
		t.Helper()
		d, err := w.Resolve(ctx, tw.Request{TenantID: uuid.New(), Type: WidgetRoutersStatusGrid, Config: json.RawMessage(cfg)})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	d := resolve(`{}`)
	if d.Meta.DataEndpointKind != "table" || d.Data.Kind != "table" || d.Meta.MaskedPersonalData {
		t.Fatalf("meta %+v", d.Meta)
	}
	want := []string{"router", "site", "router_id", "status", "reason", "since", "last_observed_at", "cpu_percent", "memory_percent",
		"temperature_celsius", "uptime_seconds", "rx_bps", "tx_bps", "uplink", "exporter_state", "tunnel_state", "interfaces_up", "interfaces_total"}
	cols := make([]string, len(d.Data.Columns))
	for i, c := range d.Data.Columns {
		cols[i] = c.Key
		if c.PersonalData {
			t.Fatalf("%s marked personal", c.Key)
		}
	}
	if !eq(cols, want) {
		t.Fatalf("columns %v", cols)
	}
	for _, c := range d.Data.Columns {
		typ := map[string]string{"status": "state", "since": "timestamp", "last_observed_at": "timestamp"}[c.Key]
		if typ == "" {
			typ = "number"
			for _, s := range []string{"router", "site", "router_id", "reason", "uplink", "exporter_state", "tunnel_state"} {
				if s == c.Key {
					typ = "string"
				}
			}
		}
		if c.Type != typ {
			t.Fatalf("column %s type %s, want %s", c.Key, c.Type, typ)
		}
	}
	if !eq(names(d), []string{"rt-sur", "rt-centro", "rt-norte"}) {
		t.Fatalf("default order %v", names(d))
	}
	row := d.Data.Rows[0]
	for _, k := range want {
		if _, ok := row[k]; !ok {
			t.Fatalf("row without %s: %v", k, row)
		}
	}
	if row["status"] != "warning" || *row["reason"].(*string) != "cpu_high" || row["uplink"] != "sfp1" ||
		row["since"] != "2026-10-13T11:00:00Z" || *row["rx_bps"].(*float64) != 100 || *row["exporter_state"].(*string) != "online" ||
		row["tunnel_state"] != "ok" || *row["interfaces_up"].(*int) != 3 {
		t.Fatalf("row %v", row)
	}
	if d.Data.Rows[2]["uplink"] != nil || d.Data.Rows[2]["since"] != nil || d.Data.Rows[2]["rx_bps"] != nil {
		t.Fatalf("offline row %v", d.Data.Rows[2])
	}
	if d.Meta.FreshnessSeconds == nil || *d.Meta.FreshnessSeconds != 45 || d.Meta.Partial {
		t.Fatalf("freshness %v partial %v", d.Meta.FreshnessSeconds, d.Meta.Partial)
	}
	// La fila serializa null (no "null" ni cero) en lo que no hay.
	b, _ := json.Marshal(d)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	rows := out["data"].(map[string]any)["rows"].([]any)
	if r := rows[2].(map[string]any); r["cpu_percent"] != nil || r["reason"] != nil || r["interfaces_up"] != nil {
		t.Fatalf("json row %v", r)
	}

	if got := names(resolve(`{"show":["offline","warning"]}`)); !eq(got, []string{"rt-sur", "rt-norte"}) {
		t.Fatalf("show %v", got)
	}
	if got := names(resolve(`{"sort":"name"}`)); !eq(got, []string{"rt-centro", "rt-norte", "rt-sur"}) {
		t.Fatalf("name %v", got)
	}
	if got := names(resolve(`{"sort":"cpu"}`)); !eq(got, []string{"rt-sur", "rt-centro", "rt-norte"}) {
		t.Fatalf("cpu %v", got)
	}
	if got := names(resolve(`{"sort":"traffic"}`)); !eq(got, []string{"rt-centro", "rt-sur", "rt-norte"}) {
		t.Fatalf("traffic %v", got)
	}
	if got := names(resolve(`{"sort":"status","show":[]}`)); !eq(got, []string{"rt-sur", "rt-centro", "rt-norte"}) {
		t.Fatalf("status %v", got)
	}

	// Sondeador caído: partial, sin freshness.
	rd.health = snmpapi.PollerHealth{}
	d = resolve(`{"show":["online"]}`)
	if !d.Meta.Partial || d.Meta.FreshnessSeconds != nil {
		t.Fatalf("unhealthy poller: %+v", d.Meta)
	}
}

// Sin el rol snmp en el proceso (o con su estado caído) el widget es "no
// disponible" (ErrUnavailable), nunca una lista vacía.
func TestRoutersStatusGridUnavailable(t *testing.T) {
	svc := &Service{Q: &routerQ{}}
	w := NewWidgets(svc, time.Second)
	req := tw.Request{TenantID: uuid.New(), Type: WidgetRoutersStatusGrid}
	if _, err := w.Resolve(context.Background(), req); !errors.Is(err, tw.ErrUnavailable) {
		t.Fatalf("without snmp role: %v", err)
	}
	svc.Routers = func() snmpapi.RouterStatusReader { return nil }
	if _, err := w.Resolve(context.Background(), req); !errors.Is(err, tw.ErrUnavailable) {
		t.Fatalf("nil reader: %v", err)
	}
	svc.Routers = func() snmpapi.RouterStatusReader { return &fakeReader{err: errors.New("nats down")} }
	req.Config = json.RawMessage(`{"sort":"name"}`)
	if _, err := w.Resolve(context.Background(), req); !errors.Is(err, tw.ErrUnavailable) {
		t.Fatalf("reader error: %v", err)
	}
}
