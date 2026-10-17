package app

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// Sección de capacidad del reporte semanal: enlaces por nodo con el p95 del
// periodo, horas sobre umbral, pronóstico y recomendación, salida
// internacional y reparto local / internacional del periodo.
func TestCapacityReport(t *testing.T) {
	f := newCapFixture(t)
	end := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	daily := math.Log1p(0.04) / 30.4375
	series := newFakeSeries(func(_ uuid.UUID, at time.Time) float64 {
		return 600e6 * math.Exp(daily*at.Sub(end).Hours()/24)
	})
	series.traffic = []domain.ASNVolume{
		{SiteID: f.site, Day: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC), Country: "US", BytesDown: 600},
		{SiteID: f.site, Day: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC), Country: "GT", BytesDown: 400},
		// Fuera del periodo: no cuenta.
		{SiteID: f.site, Day: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Country: "US", BytesDown: 999},
	}
	contracted := int64(811_000_000)
	egress := true
	ctx := context.Background()
	if _, err := f.repo.PutUplinkConfig(ctx, f.tenant, f.iface, domain.UplinkConfigInput{RouterID: f.router, Label: "Proveedor A",
		Kind: domain.UplinkKindInternational, ContractedBps: &contracted, InternationalEgress: &egress}, "", 0); err != nil {
		t.Fatal(err)
	}
	c := f.capacity(series, nil)
	if err := c.Load(ctx); err != nil {
		t.Fatal(err)
	}
	*f.clock = time.Date(2026, 10, 16, 13, 0, 0, 0, time.UTC) // lunes 07:00 en Guatemala
	c.RunDaily(ctx, false)
	from := time.Date(2026, 10, 9, 6, 0, 0, 0, time.UTC) // 09/10 00:00 en Guatemala
	to := time.Date(2026, 10, 16, 6, 0, 0, 0, time.UTC)
	r, err := c.CapacityReport(ctx, f.tenant, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sites) != 1 || len(r.Sites[0].Uplinks) != 1 || r.SaturationWarning != 0.7 || !r.History {
		t.Fatalf("reporte = %+v", r)
	}
	u := r.Sites[0].Uplinks[0]
	if u.Label != "Proveedor A" || u.Days != 7 || u.UtilP95 == nil || *u.UtilP95 < 0.7 || *u.UtilP95 > 0.8 ||
		u.HoursAboveWarning < 24*6 || u.ForecastLevel != domain.ForecastPlanning || u.Recommendation == "" || u.PlanningDate == nil ||
		u.CapacityBps != contracted || u.RouterName != "rt-core" {
		t.Fatalf("enlace = %+v", u)
	}
	if r.Egress == nil || r.Egress.ForecastLevel != domain.ForecastPlanning || r.Egress.Days != 7 {
		t.Fatalf("salida internacional = %+v", r.Egress)
	}
	if r.Traffic == nil || r.Traffic.InternationalBytes != 600 || r.Traffic.LocalBytes != 400 || *r.Traffic.InternationalShare != 0.6 {
		t.Fatalf("reparto = %+v", r.Traffic)
	}
}
