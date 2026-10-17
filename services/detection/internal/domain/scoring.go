package domain

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Scoring residencial/comercial explicable (roadmap I2, docs/events.md §8.8,
// docs/database.md "Propuesta del scoring"). Heurística ponderada: cada
// característica se normaliza a 0..1 y aporta su peso; el resultado es la
// probabilidad comercial 0..100. Las razones llevan un código estable y un
// detalle humano con los números reales.

// Nombres y versión del modelo.
const (
	// DetScoring es la clave del scoring en detection.detector_config.
	DetScoring = "customer_scoring"
	// ScoringModel es la versión del modelo de scoring.
	ScoringModel = "commercial-v1"
)

// Tipos de cliente que sugiere el scoring.
const (
	KindResidential = "residential"
	KindCommercial  = "commercial"
)

// Niveles de la sugerencia: residencial, residencial con indicios comerciales
// (commercial_use_suspected) y comercial.
const (
	LevelResidential = "residential"
	LevelSuspected   = "suspected"
	LevelCommercial  = "commercial"
)

// Códigos de característica (y de razón) del modelo commercial-v1.
const (
	FeatInboundServices = "inbound_services"
	FeatUploadRatio     = "upload_ratio_high"
	FeatUploadVsMedian  = "upload_vs_residential_median"
	FeatBusinessHours   = "business_hours_pattern"
	FeatAlwaysOn        = "always_on"
	FeatRemoteASNs      = "many_remote_asns"
	// ReasonDynamicPool baja la confianza comercial en pools dinámicos.
	ReasonDynamicPool = "dynamic_pool"
)

// ScoringFeatures en orden de presentación.
var ScoringFeatures = []string{FeatInboundServices, FeatUploadRatio, FeatUploadVsMedian, FeatBusinessHours, FeatAlwaysOn, FeatRemoteASNs}

// ScoringParams son los parámetros del scoring por ISP (clave
// customer_scoring de detection.detector_config).
type ScoringParams struct {
	Enabled bool `json:"enabled"`
	// Window es la ventana de características (7 días).
	Window Duration `json:"window"`
	// MinActiveHours: sin este mínimo de horas con actividad no hay sugerencia.
	MinActiveHours int `json:"min_active_hours"`
	// ActiveHourBytes es el suelo de bytes (subida + bajada) de una hora activa.
	ActiveHourBytes uint64 `json:"active_hour_bytes"`
	// CommercialThreshold (0–100): desde aquí se sugiere comercial.
	CommercialThreshold int `json:"commercial_threshold"`
	// SuspectedThreshold (0–100): residencial con indicios comerciales.
	SuspectedThreshold int `json:"suspected_threshold"`
	// Hysteresis: tiempo que una sugerencia nueva debe sostenerse antes de publicarse.
	Hysteresis Duration `json:"hysteresis"`
	// DynamicPoolFactor multiplica la probabilidad comercial en prefijos dinámicos.
	DynamicPoolFactor float64 `json:"dynamic_pool_factor"`
	// Horario laboral local del ISP [desde, hasta) y días (1 = lunes … 7 = domingo).
	BusinessFromHour int   `json:"business_from_hour"`
	BusinessToHour   int   `json:"business_to_hour"`
	BusinessDays     []int `json:"business_days"`
	// Normalización de cada característica (valor que da 0 → valor que da 1).
	InboundPortsFull       int     `json:"inbound_ports_full"`
	InboundPeersFull       int     `json:"inbound_peers_full"`
	ResidentialUploadRatio float64 `json:"residential_upload_ratio"`
	CommercialUploadRatio  float64 `json:"commercial_upload_ratio"`
	UploadMedianFull       float64 `json:"upload_median_full"`
	// MinResidentialPeers: mínimo de residenciales para calcular la mediana.
	MinResidentialPeers int     `json:"min_residential_peers"`
	BusinessShareLow    float64 `json:"business_share_low"`
	BusinessShareHigh   float64 `json:"business_share_high"`
	AlwaysOnLow         float64 `json:"always_on_low"`
	AlwaysOnHigh        float64 `json:"always_on_high"`
	RemoteASNsLow       int     `json:"remote_asns_low"`
	RemoteASNsHigh      int     `json:"remote_asns_high"`
	// ScoreChangeDelta: puntos de cambio que publican score.changed.
	ScoreChangeDelta int `json:"score_change_delta"`
	// Weights: peso de cada característica (se normalizan sobre las disponibles).
	Weights map[string]float64 `json:"weights"`
}

// DefaultScoringParams son los valores iniciales (umbral 80, sospecha 50,
// histéresis 7 días, ventana 7 días).
func DefaultScoringParams() ScoringParams {
	return ScoringParams{
		Enabled: true, Window: Duration(7 * 24 * time.Hour), MinActiveHours: 48, ActiveHourBytes: 1 << 20,
		CommercialThreshold: 80, SuspectedThreshold: 50, Hysteresis: Duration(7 * 24 * time.Hour), DynamicPoolFactor: 0.6,
		BusinessFromHour: 8, BusinessToHour: 19, BusinessDays: []int{1, 2, 3, 4, 5, 6},
		InboundPortsFull: 3, InboundPeersFull: 100, ResidentialUploadRatio: 0.15, CommercialUploadRatio: 0.6,
		UploadMedianFull: 5, MinResidentialPeers: 10, BusinessShareLow: 0.45, BusinessShareHigh: 0.75,
		AlwaysOnLow: 0.6, AlwaysOnHigh: 0.95, RemoteASNsLow: 50, RemoteASNsHigh: 300, ScoreChangeDelta: 5,
		Weights: map[string]float64{
			FeatInboundServices: 0.30, FeatUploadRatio: 0.20, FeatUploadVsMedian: 0.20,
			FeatBusinessHours: 0.15, FeatAlwaysOn: 0.10, FeatRemoteASNs: 0.05,
		},
	}
}

// Validate comprueba que los parámetros tienen sentido (un override inválido
// se ignora y quedan los valores por defecto).
func (p ScoringParams) Validate() error {
	switch {
	case p.Window.D() < 24*time.Hour:
		return fmt.Errorf("window < 24h")
	case p.CommercialThreshold < 1 || p.CommercialThreshold > 100:
		return fmt.Errorf("commercial_threshold fuera de 1..100")
	case p.SuspectedThreshold < 1 || p.SuspectedThreshold > p.CommercialThreshold:
		return fmt.Errorf("suspected_threshold fuera de 1..commercial_threshold")
	case p.DynamicPoolFactor < 0 || p.DynamicPoolFactor > 1:
		return fmt.Errorf("dynamic_pool_factor fuera de 0..1")
	case p.BusinessFromHour < 0 || p.BusinessToHour > 24 || p.BusinessFromHour >= p.BusinessToHour:
		return fmt.Errorf("horario laboral inválido")
	case p.Hysteresis < 0:
		return fmt.Errorf("hysteresis negativa")
	}
	for k, w := range p.Weights {
		if !slices.Contains(ScoringFeatures, k) || w < 0 || w > 1 {
			return fmt.Errorf("peso inválido %q", k)
		}
	}
	return nil
}

// IsBusinessHour indica si la hora local cae en el horario laboral.
func (p ScoringParams) IsBusinessHour(t time.Time) bool {
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7
	}
	return slices.Contains(p.BusinessDays, wd) && t.Hour() >= p.BusinessFromHour && t.Hour() < p.BusinessToHour
}

// ScoringInput son las características brutas de un cliente en la ventana.
type ScoringInput struct {
	UpBytes, DownBytes uint64
	// ActiveHours de WindowHours tuvieron al menos ActiveHourBytes.
	ActiveHours, WindowHours int
	// BusinessBytes es el tráfico (subida + bajada) en horario laboral local.
	BusinessBytes uint64
	// InboundPorts: puertos de servicio distintos con conexiones entrantes;
	// InboundPeers: IPs remotas distintas que los usan.
	InboundPorts, InboundPeers uint64
	RemoteASNs                 uint64
	// ResidentialMedianUp es la mediana de subida de los residenciales del
	// ISP en la ventana (0 = no disponible: pocos residenciales).
	ResidentialMedianUp float64
	// DynamicPool: la IP está en un prefijo assignment_mode = dynamic.
	DynamicPool bool
}

// Enough indica si hay datos suficientes para sugerir.
func (p ScoringParams) Enough(in ScoringInput) bool { return in.ActiveHours >= p.MinActiveHours }

// Feature es una característica explicada.
type Feature struct {
	Code string `json:"code"`
	// Value es el valor bruto (unidad en Unit).
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
	// Normalized es el valor normalizado 0..1.
	Normalized float64 `json:"normalized"`
	// Weight es el peso configurado; Contribution, los puntos (0..1) que
	// aporta a la probabilidad comercial.
	Weight       float64 `json:"weight"`
	Contribution float64 `json:"contribution"`
	Available    bool    `json:"available"`
	Detail       string  `json:"detail"`
}

// Score es el resultado del scoring de un cliente.
type Score struct {
	Model string
	// Raw es la probabilidad comercial 0..1 antes del ajuste por pool dinámico.
	Raw float64
	// Commercial es la probabilidad comercial 0..1 final.
	Commercial float64
	// CommercialScore es Commercial en 0..100 (lo que se compara con los umbrales).
	CommercialScore int
	Level           string
	Kind            string
	// Confidence es la probabilidad de la clase sugerida (0..1).
	Confidence float64
	Features   []Feature
	Reasons    []Reason
}

// Scores devuelve las probabilidades por clase (payload del evento).
func (s Score) Scores() map[string]float64 {
	return map[string]float64{KindResidential: Round2(1 - s.Commercial), KindCommercial: Round2(s.Commercial)}
}

// ReasonCodes devuelve los códigos de las razones.
func (s Score) ReasonCodes() []string {
	out := make([]string, 0, len(s.Reasons))
	for _, r := range s.Reasons {
		out = append(out, r.Code)
	}
	return out
}

// ramp normaliza v linealmente: lo ≤ v ≤ hi → 0..1.
func ramp(v, lo, hi float64) float64 {
	if hi <= lo {
		if v >= hi {
			return 1
		}
		return 0
	}
	return math.Max(0, math.Min(1, (v-lo)/(hi-lo)))
}

// Score calcula la probabilidad comercial explicada de un cliente.
func (p ScoringParams) Score(in ScoringInput) Score {
	total := float64(in.UpBytes + in.DownBytes)
	var feats []Feature
	add := func(code string, value float64, unit string, norm float64, available bool, detail string) {
		feats = append(feats, Feature{Code: code, Value: Round2(value), Unit: unit, Normalized: Round2(norm), Weight: p.Weights[code],
			Available: available, Detail: detail})
	}

	// 1. Servicios entrantes: puertos de servicio y pares remotos distintos.
	ports, peers := float64(in.InboundPorts), float64(in.InboundPeers)
	inb := 0.0
	if in.InboundPorts > 0 {
		inb = 0.5*ramp(ports, 0, float64(p.InboundPortsFull)) + 0.5*ramp(peers, 0, float64(p.InboundPeersFull))
	}
	add(FeatInboundServices, ports, "ports", inb, true,
		fmt.Sprintf("%s con conexiones entrantes de %s distintas", plural(in.InboundPorts, "puerto de servicio", "puertos de servicio"),
			plural(in.InboundPeers, "IP", "IPs")))

	// 2. Ratio subida/bajada frente al residencial típico.
	ratio := 0.0
	if in.DownBytes > 0 {
		ratio = float64(in.UpBytes) / float64(in.DownBytes)
	} else if in.UpBytes > 0 {
		ratio = p.CommercialUploadRatio
	}
	add(FeatUploadRatio, ratio, "ratio", ramp(ratio, p.ResidentialUploadRatio, p.CommercialUploadRatio), total > 0,
		fmt.Sprintf("Ratio subida/bajada %s (residencial típico < %s)", es(ratio, 2), es(p.ResidentialUploadRatio, 2)))

	// 3. Subida frente a la mediana residencial del ISP.
	mult := 0.0
	if in.ResidentialMedianUp > 0 {
		mult = float64(in.UpBytes) / in.ResidentialMedianUp
	}
	add(FeatUploadVsMedian, mult, "x", ramp(mult, 1, p.UploadMedianFull), in.ResidentialMedianUp > 0,
		fmt.Sprintf("Subida %s× la mediana residencial (%s en la ventana)", es(mult, 1), HumanBytes(in.UpBytes)))

	// 4. Patrón de horario laboral (L–S 08:00–19:00 local por defecto).
	share := 0.0
	if total > 0 {
		share = float64(in.BusinessBytes) / total
	}
	add(FeatBusinessHours, share, "share", ramp(share, p.BusinessShareLow, p.BusinessShareHigh), total > 0,
		fmt.Sprintf("%s %% del tráfico en horario laboral (%s %02d:00–%02d:00, hora local)", es(share*100, 0), p.daysLabel(),
			p.BusinessFromHour, p.BusinessToHour))

	// 5. Actividad 24/7.
	on := 0.0
	if in.WindowHours > 0 {
		on = float64(in.ActiveHours) / float64(in.WindowHours)
	}
	add(FeatAlwaysOn, on, "share", ramp(on, p.AlwaysOnLow, p.AlwaysOnHigh), in.WindowHours > 0,
		fmt.Sprintf("Actividad en el %s %% de las horas de la ventana (%d de %d h)", es(on*100, 0), in.ActiveHours, in.WindowHours))

	// 6. Muchos ASN remotos (peso bajo).
	asns := float64(in.RemoteASNs)
	add(FeatRemoteASNs, asns, "asns", ramp(asns, float64(p.RemoteASNsLow), float64(p.RemoteASNsHigh)), true,
		fmt.Sprintf("Tráfico saliente hacia %s distintos", plural(in.RemoteASNs, "ASN", "ASN")))

	sumW := 0.0
	for _, f := range feats {
		if f.Available {
			sumW += f.Weight
		}
	}
	raw := 0.0
	for i := range feats {
		f := &feats[i]
		if !f.Available || sumW == 0 {
			continue
		}
		f.Contribution = Round2(f.Weight * f.Normalized / sumW)
		raw += f.Weight * f.Normalized / sumW
	}
	raw = math.Max(0, math.Min(1, raw))
	commercial := raw
	s := Score{Model: ScoringModel, Raw: raw, Features: feats}
	ranked := slices.Clone(feats)
	slices.SortStableFunc(ranked, func(a, b Feature) int { return cmp.Compare(b.Contribution, a.Contribution) })
	for _, f := range ranked {
		if f.Contribution < 0.02 || len(s.Reasons) >= 5 {
			continue
		}
		s.Reasons = append(s.Reasons, Reason{Code: f.Code, Detail: f.Detail, Weight: W(f.Contribution),
			Data: map[string]any{"value": f.Value, "normalized": f.Normalized}})
	}
	if in.DynamicPool && p.DynamicPoolFactor < 1 {
		commercial = raw * p.DynamicPoolFactor
		s.Reasons = append(s.Reasons, Reason{Code: ReasonDynamicPool, Detail: fmt.Sprintf(
			"La IP es de un pool dinámico (puede cambiar de cliente): confianza comercial ×%s", es(p.DynamicPoolFactor, 2)),
			Data: map[string]any{"factor": p.DynamicPoolFactor}})
	}
	if s.Reasons == nil {
		s.Reasons = []Reason{}
	}
	s.Commercial = commercial
	s.CommercialScore = int(math.Round(commercial * 100))
	s.Level = p.LevelOf(s.CommercialScore)
	s.Kind = KindOf(s.Level)
	s.Confidence = Round2(1 - commercial)
	if s.Kind == KindCommercial {
		s.Confidence = Round2(commercial)
	}
	return s
}

// LevelOf clasifica una puntuación 0..100 con los umbrales del ISP.
func (p ScoringParams) LevelOf(score int) string {
	switch {
	case score >= p.CommercialThreshold:
		return LevelCommercial
	case score >= p.SuspectedThreshold:
		return LevelSuspected
	}
	return LevelResidential
}

// KindOf es el tipo de cliente de un nivel (suspected sigue siendo residencial).
func KindOf(level string) string {
	if level == LevelCommercial {
		return KindCommercial
	}
	return KindResidential
}

// SuggestionState es el estado de histéresis de un cliente: el nivel
// publicado (último kind_suggested) y el candidato pendiente.
type SuggestionState struct {
	Published    string
	Pending      string
	PendingSince time.Time
}

// Advance aplica la histéresis: un nivel distinto del publicado se publica
// solo si se sostiene durante Hysteresis (si cambia entre medias, el plazo
// empieza de nuevo). Un cliente nunca publicado parte de residencial (el tipo
// por defecto) sin evento. Devuelve el estado nuevo y si hay que publicar.
func (p ScoringParams) Advance(st SuggestionState, level string, now time.Time) (SuggestionState, bool) {
	if st.Published == "" {
		st.Published = LevelResidential
	}
	if level == st.Published {
		st.Pending, st.PendingSince = "", time.Time{}
		return st, false
	}
	if level != st.Pending {
		st.Pending, st.PendingSince = level, now
	}
	if now.Sub(st.PendingSince) >= p.Hysteresis.D() {
		return SuggestionState{Published: level}, true
	}
	return st, false
}

// MedianUp devuelve la mediana de vs (0 si hay menos de min valores).
func MedianUp(vs []uint64, minPeers int) float64 {
	if len(vs) == 0 || len(vs) < minPeers {
		return 0
	}
	s := slices.Clone(vs)
	slices.Sort(s)
	n := len(s)
	if n%2 == 1 {
		return float64(s[n/2])
	}
	return (float64(s[n/2-1]) + float64(s[n/2])) / 2
}

func (p ScoringParams) daysLabel() string {
	names := map[int]string{1: "L", 2: "M", 3: "X", 4: "J", 5: "V", 6: "S", 7: "D"}
	d := slices.Clone(p.BusinessDays)
	slices.Sort(d)
	if len(d) == 0 {
		return "—"
	}
	contiguous := true
	for i := 1; i < len(d); i++ {
		if d[i] != d[i-1]+1 {
			contiguous = false
		}
	}
	if contiguous && len(d) > 2 {
		return names[d[0]] + "–" + names[d[len(d)-1]]
	}
	out := make([]string, 0, len(d))
	for _, x := range d {
		out = append(out, names[x])
	}
	return strings.Join(out, ",")
}

// es formatea un número con coma decimal (español).
func es(v float64, dec int) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', dec, 64), ".", ",", 1)
}

func plural(n uint64, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.FormatUint(n, 10) + " " + many
}

// HumanBytes formatea bytes en unidades decimales con coma ("12,4 GB").
func HumanBytes(b uint64) string {
	units := []string{"B", "kB", "MB", "GB", "TB", "PB"}
	v := float64(b)
	i := 0
	for v >= 1000 && i < len(units)-1 {
		v /= 1000
		i++
	}
	if i == 0 {
		return strconv.FormatUint(b, 10) + " B"
	}
	return es(v, 1) + " " + units[i]
}
