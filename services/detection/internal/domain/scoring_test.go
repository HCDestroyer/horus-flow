package domain

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

const gb = 1_000_000_000

// residential es un hogar típico: baja mucho, sube poco, picos nocturnos.
func residential() ScoringInput {
	return ScoringInput{UpBytes: 4 * gb, DownBytes: 60 * gb, ActiveHours: 110, WindowHours: 168, BusinessBytes: 20 * gb,
		InboundPorts: 0, InboundPeers: 0, RemoteASNs: 40, ResidentialMedianUp: 4 * gb}
}

// office es una oficina con servidor: servicios entrantes, subida alta,
// horario laboral y 24/7.
func office() ScoringInput {
	return ScoringInput{UpBytes: 40 * gb, DownBytes: 50 * gb, ActiveHours: 166, WindowHours: 168, BusinessBytes: 70 * gb,
		InboundPorts: 4, InboundPeers: 240, RemoteASNs: 320, ResidentialMedianUp: 4 * gb}
}

func feature(t *testing.T, s Score, code string) Feature {
	t.Helper()
	for _, f := range s.Features {
		if f.Code == code {
			return f
		}
	}
	t.Fatalf("falta la característica %s", code)
	return Feature{}
}

func TestRamp(t *testing.T) {
	cases := []struct{ v, lo, hi, want float64 }{
		{0, 1, 5, 0}, {1, 1, 5, 0}, {3, 1, 5, 0.5}, {5, 1, 5, 1}, {50, 1, 5, 1}, {2, 3, 3, 0}, {3, 3, 3, 1},
	}
	for _, c := range cases {
		if got := ramp(c.v, c.lo, c.hi); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("ramp(%v, %v, %v) = %v; want %v", c.v, c.lo, c.hi, got, c.want)
		}
	}
}

func TestScoreFeatureNormalization(t *testing.T) {
	p := DefaultScoringParams()
	s := p.Score(office())
	if f := feature(t, s, FeatInboundServices); f.Normalized != 1 || f.Value != 4 {
		t.Errorf("inbound_services = %+v; want normalizado 1 con 4 puertos", f)
	}
	// ratio 40/50 = 0,8 ≥ 0,6 → 1.
	if f := feature(t, s, FeatUploadRatio); f.Normalized != 1 || f.Value != 0.8 {
		t.Errorf("upload_ratio_high = %+v", f)
	}
	// 40 GB / 4 GB = 10× ≥ 5× → 1.
	if f := feature(t, s, FeatUploadVsMedian); f.Normalized != 1 || f.Value != 10 {
		t.Errorf("upload_vs_residential_median = %+v", f)
	}
	// 70/90 = 0,78 ≥ 0,75 → 1.
	if f := feature(t, s, FeatBusinessHours); f.Normalized != 1 {
		t.Errorf("business_hours_pattern = %+v", f)
	}
	// 166/168 = 0,988 ≥ 0,95 → 1.
	if f := feature(t, s, FeatAlwaysOn); f.Normalized != 1 {
		t.Errorf("always_on = %+v", f)
	}
	if f := feature(t, s, FeatRemoteASNs); f.Normalized != 1 {
		t.Errorf("many_remote_asns = %+v", f)
	}
	in := office()
	in.InboundPorts, in.InboundPeers = 1, 50 // 0,5·(1/3) + 0,5·(50/100)
	if f := feature(t, p.Score(in), FeatInboundServices); math.Abs(f.Normalized-0.42) > 0.005 {
		t.Errorf("inbound parcial = %v; want 0,42", f.Normalized)
	}
	in.InboundPorts, in.InboundPeers = 0, 500 // sin puertos de servicio no cuenta
	if f := feature(t, p.Score(in), FeatInboundServices); f.Normalized != 0 {
		t.Errorf("inbound sin puertos = %v; want 0", f.Normalized)
	}
}

func TestScoreWeightsAndThresholds(t *testing.T) {
	p := DefaultScoringParams()
	off := p.Score(office())
	if off.CommercialScore != 100 || off.Level != LevelCommercial || off.Kind != KindCommercial || off.Confidence != 1 {
		t.Fatalf("oficina = %d %s %s %v; want 100 commercial", off.CommercialScore, off.Level, off.Kind, off.Confidence)
	}
	sum := 0.0
	for _, f := range off.Features {
		sum += f.Contribution
	}
	if math.Abs(sum-1) > 0.02 {
		t.Errorf("suma de contribuciones = %v; want 1", sum)
	}
	if len(off.Reasons) == 0 || off.Reasons[0].Code != FeatInboundServices {
		t.Errorf("la razón principal debe ser inbound_services (mayor peso): %+v", off.Reasons)
	}
	if !strings.Contains(off.Reasons[0].Detail, "240 IPs") {
		t.Errorf("el detalle lleva los números reales: %q", off.Reasons[0].Detail)
	}
	res := p.Score(residential())
	if res.Level != LevelResidential || res.Kind != KindResidential || res.CommercialScore >= p.SuspectedThreshold {
		t.Fatalf("hogar = %d %s; want residential", res.CommercialScore, res.Level)
	}
	if res.Confidence < 0.8 {
		t.Errorf("confianza residencial = %v", res.Confidence)
	}
	sc := res.Scores()
	if math.Abs(sc[KindResidential]+sc[KindCommercial]-1) > 0.011 {
		t.Errorf("scores no suman 1: %v", sc)
	}
	// Suspected: solo subida alta (0,20 + 0,20 + algo) sin servicios.
	mid := residential()
	mid.UpBytes, mid.DownBytes, mid.BusinessBytes = 30*gb, 40*gb, 50*gb
	ms := p.Score(mid)
	if ms.Level != LevelSuspected || ms.Kind != KindResidential {
		t.Fatalf("uso mixto = %d %s; want suspected", ms.CommercialScore, ms.Level)
	}
	// Umbrales del ISP: con 40 el mismo cliente ya es comercial.
	p.CommercialThreshold, p.SuspectedThreshold = 40, 20
	if got := p.Score(mid); got.Level != LevelCommercial {
		t.Errorf("con umbral 40: %d %s", got.CommercialScore, got.Level)
	}
	// Pesos: sin peso para los servicios entrantes la oficina pierde su razón principal.
	q := DefaultScoringParams()
	q.Weights[FeatInboundServices] = 0
	if got := q.Score(office()); got.Reasons[0].Code == FeatInboundServices {
		t.Errorf("peso 0 no debe dar razón: %+v", got.Reasons)
	}
}

func TestScoreUnavailableMedian(t *testing.T) {
	p := DefaultScoringParams()
	in := office()
	in.ResidentialMedianUp = 0
	s := p.Score(in)
	f := feature(t, s, FeatUploadVsMedian)
	if f.Available || f.Contribution != 0 {
		t.Errorf("sin mediana la característica no cuenta: %+v", f)
	}
	if s.CommercialScore != 100 {
		t.Errorf("los pesos se renormalizan sobre las disponibles: %d", s.CommercialScore)
	}
}

func TestScoreDynamicPool(t *testing.T) {
	p := DefaultScoringParams()
	in := office()
	in.DynamicPool = true
	s := p.Score(in)
	if s.Raw != 1 || s.CommercialScore != 60 || s.Level != LevelSuspected {
		t.Fatalf("pool dinámico = raw %v score %d %s; want 60 suspected", s.Raw, s.CommercialScore, s.Level)
	}
	last := s.Reasons[len(s.Reasons)-1]
	if last.Code != ReasonDynamicPool || last.Weight != nil {
		t.Errorf("razón dynamic_pool = %+v", last)
	}
}

func TestEnough(t *testing.T) {
	p := DefaultScoringParams()
	in := residential()
	in.ActiveHours = 47
	if p.Enough(in) {
		t.Error("47 h activas < 48: sin sugerencia")
	}
	in.ActiveHours = 48
	if !p.Enough(in) {
		t.Error("48 h activas bastan")
	}
}

func TestHysteresis(t *testing.T) {
	p := DefaultScoringParams()
	day := func(n int) time.Time { return time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC).AddDate(0, 0, n) }
	var st SuggestionState
	st, pub := p.Advance(st, LevelResidential, day(0))
	if pub || st.Published != LevelResidential {
		t.Fatalf("residencial inicial no se publica: %+v %v", st, pub)
	}
	st, pub = p.Advance(st, LevelCommercial, day(1))
	if pub || st.Pending != LevelCommercial {
		t.Fatalf("comercial nuevo queda pendiente: %+v %v", st, pub)
	}
	// Oscila un día: el plazo se reinicia.
	st, _ = p.Advance(st, LevelSuspected, day(3))
	if st.Pending != LevelSuspected || !st.PendingSince.Equal(day(3)) {
		t.Fatalf("cambio de candidato reinicia el plazo: %+v", st)
	}
	st, _ = p.Advance(st, LevelCommercial, day(4))
	for d := 5; d < 11; d++ {
		if st, pub = p.Advance(st, LevelCommercial, day(d)); pub {
			t.Fatalf("publicado antes de 7 días (día %d)", d)
		}
	}
	st, pub = p.Advance(st, LevelCommercial, day(11))
	if !pub || st.Published != LevelCommercial || st.Pending != "" {
		t.Fatalf("7 días sostenido → publica: %+v %v", st, pub)
	}
	// Vuelve al publicado: el pendiente se borra sin publicar.
	st, _ = p.Advance(st, LevelResidential, day(12))
	st, pub = p.Advance(st, LevelCommercial, day(13))
	if pub || st.Pending != "" {
		t.Fatalf("volver al nivel publicado no publica: %+v %v", st, pub)
	}
	// Sin histéresis se publica en el acto.
	p.Hysteresis = 0
	if _, pub = p.Advance(SuggestionState{}, LevelSuspected, day(20)); !pub {
		t.Error("histéresis 0 publica de inmediato")
	}
}

func TestMedianUp(t *testing.T) {
	if MedianUp([]uint64{5, 1, 3}, 1) != 3 || MedianUp([]uint64{4, 1, 3, 2}, 1) != 2.5 {
		t.Error("mediana")
	}
	if MedianUp([]uint64{1, 2}, 10) != 0 {
		t.Error("pocos residenciales: sin mediana")
	}
}

func TestScoringParamsMerge(t *testing.T) {
	p := DefaultParams()
	if err := p.Merge(DetScoring, json.RawMessage(`{"commercial_threshold": 70, "weights": {"always_on": 0.3}}`)); err != nil {
		t.Fatal(err)
	}
	if p.Scoring.CommercialThreshold != 70 || p.Scoring.Weights[FeatAlwaysOn] != 0.3 || p.Scoring.Weights[FeatInboundServices] != 0.30 {
		t.Errorf("merge = %+v", p.Scoring)
	}
	// Inválido: se rechaza entero y quedan los valores previos.
	if err := p.Merge(DetScoring, json.RawMessage(`{"commercial_threshold": 40, "suspected_threshold": 60}`)); err == nil {
		t.Error("suspected > commercial debe fallar")
	}
	if err := p.Merge(DetScoring, json.RawMessage(`{"weights": {"desconocido": 0.3}}`)); err == nil {
		t.Error("peso desconocido debe fallar")
	}
	if p.Scoring.CommercialThreshold != 70 {
		t.Errorf("un override inválido no cambia nada: %d", p.Scoring.CommercialThreshold)
	}
	if DefaultParams().Scoring.Weights[FeatAlwaysOn] != 0.10 {
		t.Error("los valores por defecto no se comparten entre llamadas")
	}
	p.SetEnabled(DetScoring, false)
	if p.Scoring.Enabled {
		t.Error("SetEnabled")
	}
}

func TestIsBusinessHour(t *testing.T) {
	p := DefaultScoringParams()
	mon10 := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC) // lunes
	if !p.IsBusinessHour(mon10) || p.IsBusinessHour(mon10.Add(9*time.Hour)) || p.IsBusinessHour(mon10.AddDate(0, 0, 6)) {
		t.Error("horario laboral L–S 08–19")
	}
	if p.daysLabel() != "L–S" {
		t.Errorf("daysLabel = %q", p.daysLabel())
	}
	if HumanBytes(12_400_000_000) != "12,4 GB" || HumanBytes(512) != "512 B" {
		t.Errorf("HumanBytes = %q", HumanBytes(12_400_000_000))
	}
}
