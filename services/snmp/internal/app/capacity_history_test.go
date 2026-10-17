package app

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// Capacidad con vigencia: los días anteriores a un upgrade se miden contra la
// capacidad de entonces (horas sobre umbral, p95 de la ventana); un cambio
// fechado en el pasado recalcula los días afectados; el pronóstico usa la
// capacidad vigente hoy.
func TestCapacityHistoryUtilization(t *testing.T) {
	f := newCapFixture(t)
	series := newFakeSeries(func(uuid.UUID, time.Time) float64 { return 400e6 })
	ctx := context.Background()
	gig, half := int64(1_000_000_000), int64(500_000_000)
	egress := false
	if _, err := f.repo.PutUplinkConfig(ctx, f.tenant, f.iface, domain.UplinkConfigInput{RouterID: f.router, Label: "Proveedor A",
		Kind: domain.UplinkKindInternational, ContractedBps: &gig, InternationalEgress: &egress}, "", 0); err != nil {
		t.Fatal(err)
	}
	*f.clock = time.Date(2026, 10, 16, 7, 0, 0, 0, time.UTC) // 01:00 en Guatemala
	upgrade := time.Date(2026, 9, 26, 6, 0, 0, 0, time.UTC)  // 26/09 a las 00:00 en Guatemala
	f.repo.history = map[uuid.UUID]map[uuid.UUID][]domain.CapacityPeriod{f.tenant: {f.iface: {
		{ValidFrom: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), ValidTo: &upgrade, ContractedBps: &half},
		{ValidFrom: upgrade, ContractedBps: &gig},
	}}}
	c := f.capacity(series, nil)
	if err := c.Load(ctx); err != nil {
		t.Fatal(err)
	}
	c.RunDaily(ctx, false)
	before := series.daily[f.iface][time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)].Stats
	after := series.daily[f.iface][time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)].Stats
	if before.CapacityBps != half || math.Abs(before.HoursAboveWarning-24) > 0.01 || *before.UtilP95() < 0.79 {
		t.Fatalf("antes del upgrade (500 Mb/s): %+v", before)
	}
	if after.CapacityBps != gig || after.HoursAboveWarning != 0 {
		t.Fatalf("después del upgrade (1 Gb/s): %+v", after)
	}
	// Ventana de 30 días: el peor día contra su capacidad (80 %), no 400 Mb/s / 1 Gb/s.
	ov, err := c.Overview(ctx, f.tenant, 30)
	if err != nil {
		t.Fatal(err)
	}
	w := ov.Data[0].Uplinks[0].Window
	if w == nil || w.UtilP95 == nil || math.Abs(*w.UtilP95-0.8) > 0.01 {
		t.Fatalf("utilización de la ventana = %+v", w)
	}
	if ov.Data[0].Uplinks[0].Diagnosis.Kind != domain.DiagnosisBusy {
		t.Fatalf("diagnóstico con la capacidad de cada día = %+v", ov.Data[0].Uplinks[0].Diagnosis)
	}
	// Pronóstico: umbrales con la capacidad vigente hoy.
	if fc := f.repo.forecasts[f.iface]; fc.Forecast.CapacityBps != gig {
		t.Fatalf("capacidad del pronóstico = %d", fc.Forecast.CapacityBps)
	}
	det, err := c.UplinkDetail(ctx, f.tenant, nil, f.iface, 30)
	if err != nil || len(det.CapacityHistory) != 2 || *det.CapacityHistory[0].ContractedBps != gig {
		t.Fatalf("historia en el detalle (la más reciente primero) = %+v %v", det.CapacityHistory, err)
	}
	// El administrador fecha el upgrade antes (16/09): se recalculan solo los días afectados.
	earlier := time.Date(2026, 9, 16, 6, 0, 0, 0, time.UTC)
	f.repo.history[f.tenant][f.iface][0].ValidTo = &earlier
	f.repo.history[f.tenant][f.iface][1].ValidFrom = earlier
	c.reloadTenant(ctx, f.tenant)
	q := series.queried
	c.RunDaily(ctx, true)
	fixed := series.daily[f.iface][time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)].Stats
	if fixed.CapacityBps != gig || fixed.HoursAboveWarning != 0 {
		t.Fatalf("día recalculado = %+v", fixed)
	}
	if old := series.daily[f.iface][time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)].Stats; old.CapacityBps != half {
		t.Fatalf("día anterior al cambio = %+v", old)
	}
	if n := series.queried - q; n != 10 {
		t.Fatalf("días recalculados = %d, quiero 10 (16/09–25/09)", n)
	}
}

func TestContractedAtAndCapacityFunc(t *testing.T) {
	a, b := int64(100e6), int64(200e6)
	t1 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	t2 := t1.AddDate(0, 0, 10)
	t3 := t2.AddDate(0, 0, 10)
	h := []domain.CapacityPeriod{{ValidFrom: t1, ValidTo: &t2, ContractedBps: &a}, {ValidFrom: t2, ValidTo: &t3, ContractedBps: &b}}
	cases := []struct {
		at   time.Time
		want *int64
		ok   bool
	}{{t1.AddDate(0, 0, -5), &a, true}, {t1, &a, true}, {t2.Add(-time.Second), &a, true}, {t2, &b, true}, {t3, nil, false}}
	for _, c := range cases {
		got, ok := domain.ContractedAt(h, c.at)
		if ok != c.ok || (c.want == nil) != (got == nil) || (got != nil && *got != *c.want) {
			t.Errorf("ContractedAt(%s) = %v %v", c.at, got, ok)
		}
	}
	speed := int64(1e9)
	f := domain.CapacityFuncFor(h, &b, &speed)
	if f(t1) != a || f(t3) != speed {
		t.Fatalf("con historia: %d %d", f(t1), f(t3))
	}
	if g := domain.CapacityFuncFor(nil, &b, &speed); g(t1) != b {
		t.Fatalf("sin historia, la contratada actual: %d", g(t1))
	}
	if s := domain.SumCapacity([]domain.CapacityFunc{f, f})(t2); s != 2*b {
		t.Fatalf("suma = %d", s)
	}
}
