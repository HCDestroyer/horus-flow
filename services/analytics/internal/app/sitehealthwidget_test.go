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

type fakeCapacity struct {
	rows  []snmpapi.SiteHealthSummary
	err   error
	sites []uuid.UUID
}

func (f *fakeCapacity) SiteHealth(_ context.Context, _ uuid.UUID, sites []uuid.UUID) ([]snmpapi.SiteHealthSummary, error) {
	f.sites = sites
	return f.rows, f.err
}

// site_health: columnas del contrato con el frontend, peor salud primero,
// filtro show, orden por nombre, alcance por nodo y "no disponible" sin el
// rol snmp.
func TestSiteHealthWidget(t *testing.T) {
	next := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	site := uuid.New()
	fc := &fakeCapacity{rows: []snmpapi.SiteHealthSummary{
		{SiteID: uuid.New(), SiteName: "Centro", Status: "online", Summary: "Sano", Uplinks: 2},
		{SiteID: site, SiteName: "Sur", Status: "critical", Summary: "Crítico: enlace Proveedor A al 92 % en hora pico", Uplinks: 1,
			WorstUtilP95: f64(0.92), WorstLossPct: f64(2), ForecastLevel: "planning", NextCrossing: &next},
		{SiteID: uuid.New(), SiteName: "Norte", Status: "degraded", Summary: "Degradado: enlace B caído", Uplinks: 2, UplinksDown: 1},
	}}
	svc := &Service{Q: &routerQ{}, Capacity: func() snmpapi.CapacityReader { return fc }}
	w := NewWidgets(svc, time.Millisecond)
	resolve := func(cfg string) *tw.WidgetData {
		t.Helper()
		d, err := w.Resolve(context.Background(), tw.Request{TenantID: uuid.New(), Type: WidgetSiteHealth, Config: json.RawMessage(cfg)})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	d := resolve(`{}`)
	want := []string{"site", "site_id", "status", "summary", "uplinks", "uplinks_down", "worst_util_p95", "worst_loss_pct", "forecast_level", "next_crossing"}
	if len(d.Data.Columns) != len(want) || d.Data.Kind != "table" {
		t.Fatalf("columnas: %+v", d.Data.Columns)
	}
	for i, c := range d.Data.Columns {
		if c.Key != want[i] {
			t.Fatalf("columna %d = %s, quiero %s", i, c.Key, want[i])
		}
	}
	order := func(d *tw.WidgetData) []string {
		out := make([]string, len(d.Data.Rows))
		for i, r := range d.Data.Rows {
			out[i] = r["site"].(string)
		}
		return out
	}
	if got := order(d); !eq(got, []string{"Sur", "Norte", "Centro"}) {
		t.Fatalf("orden por estado: %v", got)
	}
	sur := d.Data.Rows[0]
	if sur["forecast_level"] != "planning" || sur["next_crossing"] != "2027-03-01T00:00:00Z" || *(sur["worst_util_p95"].(*float64)) != 0.92 {
		t.Fatalf("fila: %+v", sur)
	}
	if d.Data.Rows[2]["forecast_level"] != nil {
		t.Fatal("sin pronóstico debe ser null")
	}
	time.Sleep(2 * time.Millisecond)
	if got := order(resolve(`{"sort":"name"}`)); !eq(got, []string{"Centro", "Norte", "Sur"}) {
		t.Fatalf("orden por nombre: %v", got)
	}
	time.Sleep(2 * time.Millisecond)
	if got := order(resolve(`{"show":["critical","degraded"],"site_ids":["` + site.String() + `"]}`)); !eq(got, []string{"Sur", "Norte"}) ||
		len(fc.sites) != 1 || fc.sites[0] != site {
		t.Fatalf("filtro: %v sites=%v", got, fc.sites)
	}
	// Sin el rol snmp, o con error: no disponible.
	svc.Capacity = func() snmpapi.CapacityReader { return nil }
	time.Sleep(2 * time.Millisecond)
	if _, err := w.Resolve(context.Background(), tw.Request{TenantID: uuid.New(), Type: WidgetSiteHealth}); !errors.Is(err, tw.ErrUnavailable) {
		t.Fatalf("sin rol: %v", err)
	}
	svc.Capacity = func() snmpapi.CapacityReader { return &fakeCapacity{err: errors.New("postgres caído")} }
	if _, err := w.Resolve(context.Background(), tw.Request{TenantID: uuid.New(), Type: WidgetSiteHealth, Config: json.RawMessage(`{"sort":"name"}`)}); !errors.Is(err, tw.ErrUnavailable) {
		t.Fatalf("error del lector: %v", err)
	}
}
