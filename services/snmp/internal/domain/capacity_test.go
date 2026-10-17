package domain

import (
	"math"
	"math/rand/v2"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/snmp/api"
)

func fp(v float64) *float64 { return &v }
func bp(v bool) *bool       { return &v }

func near(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %v, quiero %v ± %v", name, got, want, tol)
	}
}

func TestPercentile(t *testing.T) {
	v := []float64{10, 1, 9, 2, 8, 3, 7, 4, 6, 5}
	near(t, "p50", Percentile(v, 0.5), 5.5, 1e-9)
	near(t, "p95", Percentile(v, 0.95), 9.55, 1e-9)
	near(t, "p0", Percentile(v, 0), 1, 0)
	near(t, "p100", Percentile(v, 1), 10, 0)
	if v[0] != 10 {
		t.Fatal("Percentile modificó la entrada")
	}
	if !math.IsNaN(Percentile(nil, 0.95)) {
		t.Fatal("sin valores debe ser NaN")
	}
	near(t, "uno", Percentile([]float64{42}, 0.95), 42, 0)
}

// Un día de 288 intervalos: 24 a 900 Mb/s (2 h) y el resto a 300 Mb/s en
// un enlace de 1 Gb/s → 2 h sobre el 85 % y el 70 %, p95 en la meseta alta.
func TestComputeDailyHoursAndP95(t *testing.T) {
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var bs []Bucket5m
	for i := 0; i < 288; i++ {
		rx := 300e6
		if i >= 240 && i < 264 {
			rx = 900e6
		}
		bs = append(bs, Bucket5m{At: day.Add(time.Duration(i) * 5 * time.Minute), RxBps: fp(rx), TxBps: fp(rx / 10),
			RxPps: fp(1000), TxPps: fp(100), Errors: 0, Drops: 1})
	}
	// Un intervalo sin tasa no cuenta como muestra.
	bs = append(bs, Bucket5m{At: day.Add(24*time.Hour - time.Minute)})
	s, ok := ComputeDaily(day, bs, 1_000_000_000, 0.70, 0.85)
	if !ok || s.Samples != 288 {
		t.Fatalf("ok=%v samples=%d", ok, s.Samples)
	}
	near(t, "horas >85", s.HoursAboveCritical, 2, 1e-9)
	near(t, "horas >70", s.HoursAboveWarning, 2, 1e-9)
	near(t, "rx p95", s.RxP95Bps, 900e6, 1)
	near(t, "util p95", *s.UtilP95(), 0.9, 1e-9)
	near(t, "paquetes", s.Packets, 288*1100*300, 1)
	if s.Drops != 288 || s.RxMaxBps != 900e6 {
		t.Fatalf("drops=%d max=%v", s.Drops, s.RxMaxBps)
	}
	if _, ok := ComputeDaily(day, []Bucket5m{{At: day}}, 1e9, 0.7, 0.85); ok {
		t.Fatal("sin tasas debe dar ok=false")
	}
	// Sin capacidad conocida no hay horas sobre umbral ni utilización.
	s2, _ := ComputeDaily(day, bs, 0, 0.70, 0.85)
	if s2.HoursAboveWarning != 0 || s2.UtilP95() != nil {
		t.Fatalf("sin capacidad: %+v", s2)
	}
}

func TestDiagnoseFullVersusFaulty(t *testing.T) {
	base := DailyStats{Samples: 288, CapacityBps: 1_000_000_000, Packets: 1e8}
	cases := []struct {
		name string
		mod  func(*DailyStats)
		want string
	}{
		{"sano", func(d *DailyStats) { d.RxP95Bps = 400e6 }, DiagnosisOK},
		{"cerca", func(d *DailyStats) { d.RxP95Bps = 750e6 }, DiagnosisBusy},
		{"lleno con descartes de cola", func(d *DailyStats) { d.RxP95Bps = 950e6; d.Drops = 500_000 }, DiagnosisFull},
		{"lleno por horas críticas", func(d *DailyStats) { d.RxP95Bps = 600e6; d.HoursAboveCritical = 1.5 }, DiagnosisFull},
		{"averiado: errores con enlace holgado", func(d *DailyStats) { d.RxP95Bps = 200e6; d.Errors = 50_000 }, DiagnosisFaulty},
		{"averiado: descartes con enlace holgado", func(d *DailyStats) { d.RxP95Bps = 200e6; d.Drops = 200_000 }, DiagnosisFaulty},
		{"lleno y averiado", func(d *DailyStats) { d.TxP95Bps = 900e6; d.Errors = 20_000 }, DiagnosisFullFaulty},
		{"pocos paquetes: no se juzga", func(d *DailyStats) { d.RxP95Bps = 100e6; d.Errors = 50; d.Packets = 1000 }, DiagnosisOK},
		{"sin capacidad", func(d *DailyStats) { d.RxP95Bps = 100e6; d.CapacityBps = 0 }, DiagnosisNoCapacity},
		{"sin datos", func(d *DailyStats) { d.Samples = 0 }, DiagnosisNoData},
	}
	for _, c := range cases {
		d := base
		c.mod(&d)
		if got := Diagnose(d, 0.70, 0.85).Kind; got != c.want {
			t.Errorf("%s: %s, quiero %s", c.name, got, c.want)
		}
	}
}

func TestSumBucketsAndMerge(t *testing.T) {
	at := time.Date(2026, 10, 1, 20, 0, 0, 0, time.UTC)
	got := SumBuckets([][]Bucket5m{
		{{At: at, RxBps: fp(100), Drops: 1}, {At: at.Add(5 * time.Minute), RxBps: fp(50)}},
		{{At: at, RxBps: fp(200), TxBps: fp(10)}, {At: at.Add(5 * time.Minute)}},
	})
	if len(got) != 2 || *got[0].RxBps != 300 || *got[0].TxBps != 10 || got[0].Drops != 1 || *got[1].RxBps != 50 || got[1].TxBps != nil {
		t.Fatalf("SumBuckets: %+v", got)
	}
	m := MergeDaily([]DailyStats{{Day: at, RxP95Bps: 10, HoursAboveWarning: 1, CapacityBps: 100},
		{Day: at.AddDate(0, 0, 1), RxP95Bps: 30, HoursAboveWarning: 2, CapacityBps: 200}})
	if m.RxP95Bps != 30 || m.HoursAboveWarning != 3 || m.CapacityBps != 200 {
		t.Fatalf("MergeDaily: %+v", m)
	}
}

// synthetic genera n días de picos con crecimiento mensual g, fin de semana
// un 15 % más cargado y ruido multiplicativo ±noise.
func synthetic(n int, start float64, g, noise float64, seed uint64) ([]DailyPeak, time.Time) {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	end := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	out := make([]DailyPeak, 0, n)
	daily := math.Log1p(g) / daysPerMonth
	for i := 0; i < n; i++ {
		d := end.AddDate(0, 0, i-n+1)
		v := start * math.Exp(daily*float64(i))
		if wd := d.Weekday(); wd == time.Saturday || wd == time.Sunday {
			v *= 1.15
		}
		v *= 1 + noise*(2*r.Float64()-1)
		out = append(out, DailyPeak{Day: d, Bps: v})
	}
	return out, end
}

func TestForecastRecoversGrowthWithSeasonalityAndNoise(t *testing.T) {
	for _, g := range []float64{0.03, 0.08, -0.02} {
		series, end := synthetic(140, 400e6, g, 0.06, 7)
		// Dos días atípicos (corte y evento) no mueven la tendencia robusta.
		series[50].Bps *= 0.2
		series[90].Bps *= 2.5
		f := RunForecast(ForecastInput{Series: series, CapacityBps: 1e9, Planning: 0.8, Urgent: 0.9, LeadDays: 90, Now: end})
		if f.MonthlyGrowth == nil || !f.Seasonality || f.HistoryDays != 140 {
			t.Fatalf("g=%v: %+v", g, f)
		}
		near(t, "crecimiento", *f.MonthlyGrowth, g, 0.008)
		if *f.MonthlyGrowthLow > g || *f.MonthlyGrowthHigh < g {
			t.Errorf("g=%v fuera del intervalo [%v, %v]", g, *f.MonthlyGrowthLow, *f.MonthlyGrowthHigh)
		}
		// Nivel actual: el día más cargado (fin de semana) de la tendencia.
		wantCur := 400e6 * math.Exp(math.Log1p(g)/daysPerMonth*139) * 1.15
		near(t, "pico actual", *f.CurrentPeakBps/wantCur, 1, 0.05)
		if len(f.Projection) != 53 || f.Projection[10].LowBps > f.Projection[10].Bps || f.Projection[10].HighBps < f.Projection[10].Bps {
			t.Errorf("proyección: %d puntos, %+v", len(f.Projection), f.Projection[10])
		}
	}
}

func TestForecastCrossingDate(t *testing.T) {
	// Sin ruido ni estacionalidad: 500 Mb/s hoy, +5 % mensual, enlace de 1 Gb/s.
	end := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	daily := math.Log1p(0.05) / daysPerMonth
	var series []DailyPeak
	for i := 0; i < 120; i++ {
		series = append(series, DailyPeak{Day: end.AddDate(0, 0, i-119), Bps: 500e6 * math.Exp(daily*float64(i-119))})
	}
	f := RunForecast(ForecastInput{Series: series, CapacityBps: 1e9, Planning: 0.8, Urgent: 0.9, LeadDays: 90, Now: end})
	wantDays := math.Log(1.6) / daily
	if f.Planning.Date == nil || f.Planning.DaysUntil == nil {
		t.Fatalf("sin fecha de cruce: %+v", f.Planning)
	}
	near(t, "días hasta el 80 %", float64(*f.Planning.DaysUntil), wantDays, 1.5)
	if f.Level != ForecastOK || f.Confidence != ConfidenceHigh {
		t.Fatalf("nivel %s confianza %s", f.Level, f.Confidence)
	}
	near(t, "6 meses", *f.Growth6m, math.Exp(daily*sixMonthsDays)-1, 1e-6)
	// Con 300 días de antelación pasa a planificación; el urgente (90 %) llega después.
	f = RunForecast(ForecastInput{Series: series, CapacityBps: 1e9, Planning: 0.8, Urgent: 0.9, LeadDays: 300, Now: end})
	if f.Level != ForecastPlanning || *f.Urgent.DaysUntil <= *f.Planning.DaysUntil {
		t.Fatalf("antelación 300: %s %+v %+v", f.Level, f.Planning, f.Urgent)
	}
	rec := Recommendation(f, "de salida internacional")
	if !strings.HasPrefix(rec, "Planificar upgrade de salida internacional: pico 500 Mb/s, en 6 meses ~") ||
		!strings.Contains(rec, "cruza el 80 % hacia "+FormatMonth(*f.Planning.Date)) {
		t.Fatalf("recomendación: %q", rec)
	}
	// Ya por encima del urgente.
	f = RunForecast(ForecastInput{Series: series, CapacityBps: 520e6, Planning: 0.8, Urgent: 0.9, LeadDays: 90, Now: end})
	if f.Level != ForecastUrgent || !f.Urgent.Already || *f.Urgent.DaysUntil != 0 {
		t.Fatalf("ya superado: %s %+v", f.Level, f.Urgent)
	}
	if rec := Recommendation(f, "del enlace «A»"); !strings.Contains(rec, "Upgrade urgente del enlace «A»") || !strings.Contains(rec, "ya supera el 90 %") {
		t.Fatalf("recomendación urgente: %q", rec)
	}
	// Sin crecimiento: no cruza.
	flat := make([]DailyPeak, 60)
	for i := range flat {
		flat[i] = DailyPeak{Day: end.AddDate(0, 0, i-59), Bps: 300e6}
	}
	f = RunForecast(ForecastInput{Series: flat, CapacityBps: 1e9, Planning: 0.8, Urgent: 0.9, LeadDays: 90, Now: end})
	if f.Level != ForecastOK || f.Planning.Date != nil || !strings.Contains(Recommendation(f, "del enlace"), "sin crecimiento") {
		t.Fatalf("plano: %+v %q", f, Recommendation(f, "del enlace"))
	}
}

func TestForecastInsufficientData(t *testing.T) {
	series, end := synthetic(20, 300e6, 0.05, 0.02, 3)
	f := RunForecast(ForecastInput{Series: series, CapacityBps: 1e9, Planning: 0.8, Urgent: 0.9, LeadDays: 90, Now: end})
	if f.Level != ForecastInsufficient || f.MonthlyGrowth == nil || f.Planning.Date != nil || f.Confidence != ConfidenceLow {
		t.Fatalf("20 días: %+v", f)
	}
	if rec := Recommendation(f, "del enlace"); !strings.HasPrefix(rec, "Datos insuficientes para fecha estimada: tendencia +") {
		t.Fatalf("recomendación: %q", rec)
	}
	f = RunForecast(ForecastInput{Series: series[:10], CapacityBps: 1e9, Planning: 0.8, Urgent: 0.9, LeadDays: 90, Now: end})
	if f.MonthlyGrowth != nil || f.HistoryDays != 10 || !strings.HasPrefix(Recommendation(f, "x"), "Datos insuficientes para pronosticar") {
		t.Fatalf("10 días: %+v", f)
	}
	// Días sin dato (0) no cuentan.
	series[3].Bps, series[4].Bps = 0, math.NaN()
	if f := RunForecast(ForecastInput{Series: series, Now: end}); f.HistoryDays != 18 {
		t.Fatalf("días válidos = %d", f.HistoryDays)
	}
	if f := RunForecast(ForecastInput{Series: append(series, synthetic2(40, end)...), Now: end}); f.Level != ForecastNoCapacity {
		t.Fatalf("sin capacidad: %s", f.Level)
	}
}

func synthetic2(n int, end time.Time) []DailyPeak {
	out := make([]DailyPeak, n)
	for i := range out {
		out[i] = DailyPeak{Day: end.AddDate(0, 0, -100+i), Bps: 200e6}
	}
	return out
}

func TestFormatters(t *testing.T) {
	for v, want := range map[float64]string{780e6: "780 Mb/s", 1.04e9: "1,0 Gb/s", 8.5e6: "8,5 Mb/s", 950: "950 b/s", 12.4e9: "12 Gb/s"} {
		if got := FormatBps(v); got != want {
			t.Errorf("FormatBps(%v) = %q, quiero %q", v, got, want)
		}
	}
	if FormatSignedPct(0.28) != "+28 %" || FormatSignedPct(0.042) != "+4,2 %" || FormatSignedPct(-0.03) != "−3,0 %" {
		t.Errorf("FormatSignedPct: %q %q %q", FormatSignedPct(0.28), FormatSignedPct(0.042), FormatSignedPct(-0.03))
	}
	if FormatMonth(time.Date(2027, 3, 9, 0, 0, 0, 0, time.UTC)) != "03/2027" {
		t.Error("FormatMonth")
	}
}

func TestSiteHealthComposition(t *testing.T) {
	s := DefaultCapacitySettings()
	h := ComposeSiteHealth(SiteHealthInput{Settings: s,
		Uplinks: []UplinkHealthInput{{Label: "Proveedor A", OperUp: bp(true), UtilP95: fp(0.92), LossPct: fp(2), LatencyMs: fp(40)},
			{Label: "Proveedor B", OperUp: bp(true), UtilP95: fp(0.3)}},
		Routers: []RouterHealthInput{{Name: "rt-1", Status: api.StatusOnline}}})
	if h.Status != api.StatusCritical || h.Summary != "Crítico: enlace Proveedor A al 92 % en hora pico y 2,0 % de pérdida" {
		t.Fatalf("salud: %+v", h)
	}
	if h.Reasons[0].Code != "uplink_saturated" {
		t.Fatalf("código: %+v", h.Reasons)
	}
	h = ComposeSiteHealth(SiteHealthInput{Settings: s,
		Uplinks: []UplinkHealthInput{{Label: "A", OperUp: bp(true), UtilP95: fp(0.5), LossPct: fp(2)}}})
	if h.Status != api.StatusDegraded || h.Summary != "Degradado: enlace A 2,0 % de pérdida" || h.Reasons[0].Code != "uplink_quality" {
		t.Fatalf("pérdida: %+v", h)
	}
	h = ComposeSiteHealth(SiteHealthInput{Settings: s,
		Uplinks:  []UplinkHealthInput{{Label: "A", OperUp: bp(false)}, {Label: "B", OperUp: bp(false)}},
		Services: []ServiceHealthInput{{Name: "IPTV", State: "down"}}})
	if h.Status != api.StatusOffline || h.Reasons[0].Code != "all_uplinks_down" || !strings.Contains(h.Summary, "servicio IPTV caído") {
		t.Fatalf("caído: %+v", h)
	}
	h = ComposeSiteHealth(SiteHealthInput{Settings: s,
		Uplinks: []UplinkHealthInput{{Label: "A", OperUp: bp(false)}, {Label: "B", OperUp: bp(true), UtilP95: fp(0.75)}},
		Routers: []RouterHealthInput{{Name: "rt-2", Status: api.StatusWarning, Reason: "cpu_high"}}})
	if h.Status != api.StatusDegraded || len(h.Reasons) != 3 || h.Reasons[0].Text != "enlace A caído" ||
		!strings.Contains(h.Summary, "router rt-2 aviso (cpu high)") {
		t.Fatalf("mixto: %+v", h)
	}
	if h := ComposeSiteHealth(SiteHealthInput{Settings: s, Uplinks: []UplinkHealthInput{{Label: "A", OperUp: bp(true), UtilP95: fp(0.2)}}}); h.Status != api.StatusOnline ||
		!strings.HasPrefix(h.Summary, "Sano") {
		t.Fatalf("sano: %+v", h)
	}
	if h := ComposeSiteHealth(SiteHealthInput{Settings: s}); h.Status != api.StatusUnknown {
		t.Fatalf("sin datos: %+v", h)
	}
	// El monitor sostenido manda aunque el p95 del día sea bajo.
	if h := ComposeSiteHealth(SiteHealthInput{Settings: s, Uplinks: []UplinkHealthInput{{Label: "A", OperUp: bp(true), UtilP95: fp(0.4),
		UtilNow: fp(0.88), SaturationState: LevelCritical}}}); h.Status != api.StatusCritical || !strings.Contains(h.Summary, "al 88 %") {
		t.Fatalf("sostenido: %+v", h)
	}
}

func TestClassifyTraffic(t *testing.T) {
	local := []uint32{2906, 15169}
	cases := []struct {
		asn     uint32
		country string
		want    string
		reason  string
	}{
		{0, "US", OriginUnknown, ""}, {15169, "US", OriginLocal, LocalByList}, {14754, "GT", OriginLocal, LocalByCountry},
		{13335, "US", OriginInternational, ""}, {64512, "", OriginUnknown, ""}, {64513, "ZZ", OriginUnknown, ""},
	}
	for _, c := range cases {
		o, r := ClassifyASN(c.asn, c.country, "gt", local)
		if o != c.want || r != c.reason {
			t.Errorf("AS%d %s: %s/%s, quiero %s/%s", c.asn, c.country, o, r, c.want, c.reason)
		}
	}
	var s TrafficSplit
	s.Finish()
	if s.InternationalShare != nil {
		t.Fatal("sin tráfico: proporción nil")
	}
	for _, v := range []ASNVolume{{Country: "US", BytesDown: 600}, {Country: "US", Local: true, BytesDown: 300},
		{Country: "GT", BytesUp: 100}, {NoASN: true, BytesDown: 1000}, {Country: "", BytesDown: 0}} {
		s.Add(v, "GT")
	}
	s.Finish()
	if s.InternationalBytes != 600 || s.LocalBytes != 400 || s.LocalByList != 300 || s.LocalByCountry != 100 || s.UnknownBytes != 1000 {
		t.Fatalf("split: %+v", s)
	}
	near(t, "internacional", *s.InternationalShare, 0.6, 1e-9)
}

func TestNetwatchCommentsAndSnippet(t *testing.T) {
	for c, want := range map[string]ProbeRef{
		"horus-uplink;if=sfp-sfpplus1;ref=gw": {Interface: "sfp-sfpplus1", Ref: ProbeRefGateway},
		"horus-uplink;ref=intl-1.1.1.1":       {Ref: ProbeRefInternational},
		"horus-uplink;if=ether1":              {Interface: "ether1", Ref: ProbeRefGateway},
	} {
		got, ok := ParseNetwatchComment(c)
		if !ok || got != want {
			t.Errorf("%q → %+v %v", c, got, ok)
		}
	}
	if _, ok := ParseNetwatchComment("monitoreo del ISP"); ok {
		t.Error("comentario ajeno aceptado")
	}
	sn := BuildNetwatchSnippet(`rt "core"`, []SnippetUplink{{Name: "sfp-sfpplus1", Label: "Proveedor A"}, {Name: "ether 2", Label: "IX"}},
		DefaultInternationalRefs)
	if len(sn.Placeholders) != 2 || sn.Placeholders[0].Key != "{GATEWAY_1}" ||
		!strings.Contains(sn.Script, `host={GATEWAY_2} interval=30s`) || !strings.Contains(sn.Script, `comment="horus-uplink;if=ether_2;ref=gw"`) ||
		!strings.Contains(sn.Script, `comment="horus-uplink;ref=intl-8.8.8.8"`) || strings.Contains(sn.Script, `"core"`) ||
		strings.Contains(sn.Script, "script=") {
		t.Fatalf("script:\n%s", sn.Script)
	}
	// Lo que el script escribe es lo que el lector entiende.
	if r, _ := ParseNetwatchComment("horus-uplink;if=" + SafeIfName("ether 2") + ";ref=gw"); r.Interface != SafeIfName("ether 2") {
		t.Fatal("ida y vuelta del nombre")
	}
	if ProbeID(uuid.Nil, "1.1.1.1", "intl", "") == ProbeID(uuid.Nil, "8.8.8.8", "intl", "") {
		t.Fatal("ProbeID colisiona")
	}
	if w := BuildNetwatchSnippet("rt", nil, nil).Warnings; len(w) != 1 {
		t.Fatalf("avisos: %v", w)
	}
}

func TestConditionHysteresis(t *testing.T) {
	if LevelFor(0.86, 0.7, 0.85, 0.05, LevelOK) != LevelCritical || LevelFor(0.82, 0.7, 0.85, 0.05, LevelCritical) != LevelCritical ||
		LevelFor(0.79, 0.7, 0.85, 0.05, LevelCritical) != LevelWarning || LevelFor(0.67, 0.7, 0.85, 0.05, LevelWarning) != LevelWarning ||
		LevelFor(0.64, 0.7, 0.85, 0.05, LevelWarning) != LevelOK || LevelFor(0.69, 0.7, 0.85, 0.05, LevelOK) != LevelOK {
		t.Fatal("LevelFor")
	}
	var c ConditionState
	t0 := time.Date(2026, 10, 16, 20, 0, 0, 0, time.UTC)
	step := func(i int, level string) (string, bool) {
		return c.Step(level, 0.9, t0.Add(time.Duration(i)*time.Minute), 15*time.Minute, 10*time.Minute, 5*time.Minute)
	}
	for i := 0; i < 15; i++ {
		if _, ch := step(i, LevelCritical); ch {
			t.Fatalf("adoptado antes de 15 min (i=%d)", i)
		}
	}
	if prev, ch := step(15, LevelCritical); !ch || prev != LevelOK || c.Level != LevelCritical || !c.Since.Equal(t0) {
		t.Fatalf("no adoptado a los 15 min: %+v", c)
	}
	// Un minuto sano no recupera; un hueco de datos reinicia el candidato.
	if _, ch := step(16, LevelOK); ch {
		t.Fatal("recuperación inmediata")
	}
	if _, ch := step(30, LevelOK); ch || c.PendingSince != t0.Add(30*time.Minute) {
		t.Fatalf("hueco no reinicia: %+v", c)
	}
	for i := 31; i < 40; i++ {
		step(i, LevelOK)
	}
	if prev, ch := step(40, LevelOK); !ch || prev != LevelCritical || c.Level != LevelOK {
		t.Fatalf("recuperación: %+v", c)
	}
	// Repetir el mismo nivel no vuelve a cambiar (sin duplicados).
	if _, ch := step(41, LevelOK); ch {
		t.Fatal("cambio duplicado")
	}
}

func TestCapacityValidation(t *testing.T) {
	in := UplinkConfigInput{RouterID: uuid.New(), Label: "  Proveedor A  ", Kind: UplinkKindInternational, ContractedBps: ptr64(500_000_000)}
	if errs := in.Validate(); len(errs) != 0 || in.Label != "Proveedor A" || in.InternationalEgress == nil || !*in.InternationalEgress {
		t.Fatalf("válido: %v %+v", errs, in)
	}
	bad := UplinkConfigInput{Kind: "fibra", ContractedBps: ptr64(10)}
	if errs := bad.Validate(); len(errs) != 4 {
		t.Fatalf("inválido: %v", errs)
	}
	if CapacityBps(nil, ptr64(1e9)) != 1e9 || CapacityBps(ptr64(5e8), ptr64(1e9)) != 5e8 || CapacityBps(nil, nil) != 0 {
		t.Fatal("CapacityBps")
	}
	s := DefaultCapacitySettings()
	gt := " gt "
	s.Country = &gt
	s.LocalASNs = []uint32{15169, 2906, 15169}
	s.ProbeTargets = []ProbeTarget{{Label: "Gateway", Address: netip.MustParseAddr("203.0.113.1")}}
	if errs := s.Validate(nil); len(errs) != 0 || *s.Country != "GT" || len(s.LocalASNs) != 2 {
		t.Fatalf("ajustes: %v %+v", errs, s)
	}
	s.PlanningThreshold, s.UrgentThreshold = 0.95, 0.9
	s.ProbeTargets = []ProbeTarget{{Label: "", Address: netip.MustParseAddr("10.255.0.5")}, {Label: "v6", Address: netip.MustParseAddr("2001:db8::1")}}
	errs := s.Validate([]netip.Prefix{netip.MustParsePrefix("10.255.0.0/24")})
	if len(errs) != 4 {
		t.Fatalf("ajustes inválidos: %v", errs)
	}
}

func ptr64(v int64) *int64 { return &v }
