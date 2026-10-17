package domain

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Pronóstico de capacidad (docs/capacity-forecast.md). Método, en Go puro:
//
//  1. Serie: pico diario = p95 de las medias de 5 min del día (DailyStats),
//     últimos 8–26 semanas (ForecastWindowDays); se descartan días sin dato.
//  2. y = ln(pico): el crecimiento del tráfico es multiplicativo, así la
//     pendiente es una tasa de crecimiento constante.
//  3. Tendencia robusta de Theil–Sen (mediana de las pendientes entre todos
//     los pares de días): ignora días atípicos (cortes, eventos) hasta ~29 %.
//  4. Estacionalidad semanal (con ≥ 21 días): mediana de los residuos por día
//     de la semana, centrada; se resta y se recalcula Theil–Sen sobre la serie
//     desestacionalizada. Para proyectar se usa el día de la semana más cargado
//     (es el que cruza primero el umbral).
//  5. Intervalo de la pendiente: el de Sen (rangos de las pendientes por
//     pares, z = 1,96); ruido residual: 1,4826·MAD. Banda de proyección =
//     pendientes extremas ± 1,645·σ (≈ 90 %).
//  6. Crecimiento mensual = e^(30,44·b) − 1. Fecha de cruce de θ·capacidad:
//     t = (ln(θ·C) − a − s_max) / b, con a el nivel actual desestacionalizado.
//
// Con menos de MinTrendDays días no hay tendencia; con menos de MinDateDays
// solo se informa la tendencia ("datos insuficientes para fecha estimada").

// Parámetros del pronóstico.
const (
	ForecastWindowDays = 182 // 26 semanas
	MinTrendDays       = 14
	MinDateDays        = 28
	SeasonalityDays    = 21
	daysPerMonth       = 30.4375
	horizonDays        = 3 * 365 // más allá: "no cruza en 36 meses"
	projectionDays     = 365
	projectionStep     = 7
	sixMonthsDays      = 182
	zSen               = 1.96
	zBand              = 1.645
	madToSigma         = 1.4826
	highConfidenceDays = 84 // 12 semanas
	medConfidenceDays  = 56 // 8 semanas
	minBps             = 1.0
)

// Niveles del pronóstico (CapacityForecastLevel del contrato).
const (
	ForecastOK           = "ok"
	ForecastPlanning     = "planning"
	ForecastUrgent       = "urgent"
	ForecastInsufficient = "insufficient_data"
	ForecastNoCapacity   = "no_capacity"
)

// Confianza del pronóstico.
const (
	ConfidenceLow    = "low"
	ConfidenceMedium = "medium"
	ConfidenceHigh   = "high"
)

// DailyPeak es un punto de la serie de picos diarios (bits/s).
type DailyPeak struct {
	Day time.Time `json:"day"`
	Bps float64   `json:"bps"`
}

// ForecastInput son los datos de un pronóstico.
type ForecastInput struct {
	Series      []DailyPeak
	CapacityBps int64
	Planning    float64
	Urgent      float64
	LeadDays    int
	Now         time.Time
}

// Crossing es el cruce estimado de un umbral.
type Crossing struct {
	Threshold float64    `json:"threshold"`
	Date      *time.Time `json:"date"`
	DaysUntil *int       `json:"days_until"`
	// Already: el pico actual ya supera el umbral.
	Already bool `json:"already"`
}

// ForecastPoint es un punto de la proyección (bits/s).
type ForecastPoint struct {
	Day     time.Time `json:"day"`
	Bps     float64   `json:"bps"`
	LowBps  float64   `json:"low_bps"`
	HighBps float64   `json:"high_bps"`
}

// Forecast es el resultado.
type Forecast struct {
	Level             string          `json:"level"`
	Method            string          `json:"method"`
	HistoryDays       int             `json:"history_days"`
	FirstDay          *time.Time      `json:"first_day"`
	LastDay           *time.Time      `json:"last_day"`
	Seasonality       bool            `json:"seasonality"`
	Confidence        string          `json:"confidence"`
	CapacityBps       int64           `json:"capacity_bps"`
	ObservedPeakBps   *float64        `json:"observed_peak_bps"`
	CurrentPeakBps    *float64        `json:"current_peak_bps"`
	MonthlyGrowth     *float64        `json:"monthly_growth"`
	MonthlyGrowthLow  *float64        `json:"monthly_growth_low"`
	MonthlyGrowthHigh *float64        `json:"monthly_growth_high"`
	Peak6mBps         *float64        `json:"peak_6m_bps"`
	Growth6m          *float64        `json:"growth_6m"`
	Planning          Crossing        `json:"planning"`
	Urgent            Crossing        `json:"urgent"`
	LeadDays          int             `json:"lead_days"`
	Projection        []ForecastPoint `json:"projection"`
}

// MethodName identifica el método en la respuesta.
const MethodName = "theil_sen_log_weekly"

func median(v []float64) float64 { return Percentile(v, 0.5) }

// theilSen devuelve la pendiente (mediana de pendientes por pares), el
// intervalo de Sen al 95 % y la ordenada (mediana de y − b·x).
func theilSen(x, y []float64) (slope, low, high, intercept float64) {
	n := len(x)
	slopes := make([]float64, 0, n*(n-1)/2)
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if dx := x[j] - x[i]; dx != 0 {
				slopes = append(slopes, (y[j]-y[i])/dx)
			}
		}
	}
	slices.Sort(slopes)
	slope = median(slopes)
	nn := float64(n)
	c := zSen * math.Sqrt(nn*(nn-1)*(2*nn+5)/18)
	total := float64(len(slopes))
	lo := int(math.Round((total-c)/2)) - 1
	hi := int(math.Round((total + c) / 2))
	lo = max(0, min(lo, len(slopes)-1))
	hi = max(0, min(hi, len(slopes)-1))
	low, high = slopes[lo], slopes[hi]
	res := make([]float64, n)
	for i := range x {
		res[i] = y[i] - slope*x[i]
	}
	intercept = median(res)
	return slope, low, high, intercept
}

// RunForecast calcula el pronóstico de un enlace o de un agregado.
func RunForecast(in ForecastInput) Forecast {
	out := Forecast{Level: ForecastInsufficient, Method: MethodName, Confidence: ConfidenceLow, CapacityBps: in.CapacityBps,
		LeadDays: in.LeadDays, Projection: []ForecastPoint{},
		Planning: Crossing{Threshold: in.Planning}, Urgent: Crossing{Threshold: in.Urgent}}
	pts := make([]DailyPeak, 0, len(in.Series))
	for _, p := range in.Series {
		if p.Bps >= minBps && !math.IsNaN(p.Bps) && !math.IsInf(p.Bps, 0) {
			pts = append(pts, DailyPeak{Day: p.Day.UTC().Truncate(24 * time.Hour), Bps: p.Bps})
		}
	}
	slices.SortFunc(pts, func(a, b DailyPeak) int { return a.Day.Compare(b.Day) })
	pts = slices.CompactFunc(pts, func(a, b DailyPeak) bool { return a.Day.Equal(b.Day) })
	if len(pts) > 0 {
		cut := pts[len(pts)-1].Day.AddDate(0, 0, -ForecastWindowDays+1)
		pts = slices.DeleteFunc(pts, func(p DailyPeak) bool { return p.Day.Before(cut) })
	}
	out.HistoryDays = len(pts)
	if len(pts) == 0 {
		return out
	}
	first, last := pts[0].Day, pts[len(pts)-1].Day
	out.FirstDay, out.LastDay = &first, &last
	// Pico observado: el mayor de la última semana.
	obs := 0.0
	for _, p := range pts {
		if !p.Day.Before(last.AddDate(0, 0, -6)) {
			obs = math.Max(obs, p.Bps)
		}
	}
	out.ObservedPeakBps = &obs
	if len(pts) < MinTrendDays {
		return out
	}
	n := len(pts)
	x, y := make([]float64, n), make([]float64, n)
	for i, p := range pts {
		x[i] = p.Day.Sub(last).Hours() / 24 // 0 = último día
		y[i] = math.Log(p.Bps)
	}
	b, bLow, bHigh, a := theilSen(x, y)
	var season [7]float64
	if n >= SeasonalityDays {
		out.Seasonality = true
		var byDow [7][]float64
		for i, p := range pts {
			byDow[p.Day.Weekday()] = append(byDow[p.Day.Weekday()], y[i]-(a+b*x[i]))
		}
		var mean float64
		for d := range byDow {
			if len(byDow[d]) > 0 {
				season[d] = median(byDow[d])
			}
			mean += season[d]
		}
		mean /= 7
		ys := make([]float64, n)
		for d := range season {
			season[d] -= mean
		}
		for i, p := range pts {
			ys[i] = y[i] - season[p.Day.Weekday()]
		}
		b, bLow, bHigh, a = theilSen(x, ys)
		y = ys
	}
	sMax := slices.Max(season[:])
	res := make([]float64, n)
	for i := range x {
		res[i] = math.Abs(y[i] - (a + b*x[i]))
	}
	sigma := madToSigma * median(res)
	growth := func(slope float64) *float64 { g := math.Expm1(slope * daysPerMonth); return &g }
	out.MonthlyGrowth, out.MonthlyGrowthLow, out.MonthlyGrowthHigh = growth(b), growth(bLow), growth(bHigh)
	cur := math.Exp(a + sMax)
	out.CurrentPeakBps = &cur
	p6 := math.Exp(a + b*sixMonthsDays + sMax)
	g6 := p6/cur - 1
	out.Peak6mBps, out.Growth6m = &p6, &g6
	for h := 0; h <= projectionDays; h += projectionStep {
		hf := float64(h)
		out.Projection = append(out.Projection, ForecastPoint{Day: last.AddDate(0, 0, h),
			Bps:     math.Exp(a + b*hf + sMax),
			LowBps:  math.Exp(a + bLow*hf + sMax - zBand*sigma),
			HighBps: math.Exp(a + bHigh*hf + sMax + zBand*sigma)})
	}
	switch {
	case n >= highConfidenceDays && bLow > 0:
		out.Confidence = ConfidenceHigh
	case n >= medConfidenceDays:
		out.Confidence = ConfidenceMedium
	}
	if n < MinDateDays {
		out.Confidence = ConfidenceLow
		return out
	}
	if in.CapacityBps <= 0 {
		out.Level = ForecastNoCapacity
		return out
	}
	cross := func(th float64) Crossing {
		c := Crossing{Threshold: th}
		target := th * float64(in.CapacityBps)
		if cur >= target {
			c.Already = true
			d := in.Now.UTC().Truncate(24 * time.Hour)
			c.Date = &d
			zero := 0
			c.DaysUntil = &zero
			return c
		}
		if b <= 0 {
			return c
		}
		h := (math.Log(target) - a - sMax) / b
		if h > horizonDays {
			return c
		}
		d := last.Add(time.Duration(h * 24 * float64(time.Hour))).Truncate(24 * time.Hour)
		c.Date = &d
		days := max(0, int(math.Ceil(d.Sub(in.Now.UTC()).Hours()/24)))
		c.DaysUntil = &days
		return c
	}
	out.Planning, out.Urgent = cross(in.Planning), cross(in.Urgent)
	within := func(c Crossing) bool { return c.DaysUntil != nil && *c.DaysUntil <= in.LeadDays }
	switch {
	case within(out.Urgent):
		out.Level = ForecastUrgent
	case within(out.Planning):
		out.Level = ForecastPlanning
	default:
		out.Level = ForecastOK
	}
	return out
}

// FormatBps escribe una tasa en bits/s con prefijo SI decimal y coma
// decimal ("780 Mb/s", "1,0 Gb/s").
func FormatBps(v float64) string {
	units := []struct {
		div float64
		sym string
	}{{1e12, "Tb/s"}, {1e9, "Gb/s"}, {1e6, "Mb/s"}, {1e3, "kb/s"}}
	for _, u := range units {
		if v >= u.div {
			q := v / u.div
			if q < 10 {
				return strings.Replace(strconv.FormatFloat(q, 'f', 1, 64), ".", ",", 1) + " " + u.sym
			}
			return strconv.FormatFloat(math.Round(q), 'f', 0, 64) + " " + u.sym
		}
	}
	return strconv.FormatFloat(math.Round(v), 'f', 0, 64) + " b/s"
}

// FormatPct escribe una proporción como porcentaje entero ("92 %").
func FormatPct(r float64) string { return strconv.FormatFloat(math.Round(r*100), 'f', 0, 64) + " %" }

// FormatSignedPct escribe un crecimiento con signo ("+28 %", "−3 %"); con
// menos de un 10 % lleva un decimal ("+4,2 %").
func FormatSignedPct(r float64) string {
	p := r * 100
	sign := "+"
	if p < 0 {
		sign, p = "−", -p
	}
	if p < 10 {
		return sign + strings.Replace(strconv.FormatFloat(p, 'f', 1, 64), ".", ",", 1) + " %"
	}
	return sign + strconv.FormatFloat(math.Round(p), 'f', 0, 64) + " %"
}

// FormatMonth escribe mm/aaaa.
func FormatMonth(t time.Time) string { return t.UTC().Format("01/2006") }

// Recommendation redacta la recomendación del pronóstico. subject es el
// sujeto en la frase («del enlace «Proveedor A»», «de salida internacional»).
func Recommendation(f Forecast, subject string) string {
	switch {
	case f.HistoryDays < MinTrendDays:
		return fmt.Sprintf("Datos insuficientes para pronosticar: %d días de historia (mínimo %d).", f.HistoryDays, MinTrendDays)
	case f.HistoryDays < MinDateDays:
		return fmt.Sprintf("Datos insuficientes para fecha estimada: tendencia %s mensual con %d días de historia (fecha a partir de %d días).",
			FormatSignedPct(deref(f.MonthlyGrowth)), f.HistoryDays, MinDateDays)
	case f.Level == ForecastNoCapacity:
		return fmt.Sprintf("Configure la capacidad contratada para estimar fechas: pico %s, tendencia %s mensual.",
			FormatBps(deref(f.CurrentPeakBps)), FormatSignedPct(deref(f.MonthlyGrowth)))
	}
	peak, growth := deref(f.CurrentPeakBps), deref(f.MonthlyGrowth)
	sixMonths := fmt.Sprintf("en 6 meses ~%s (%s)", FormatBps(deref(f.Peak6mBps)), FormatSignedPct(deref(f.Growth6m)))
	when := func(c Crossing) string {
		if c.Already {
			return "ya supera el " + FormatPct(c.Threshold)
		}
		if c.Date == nil {
			return "no cruza el " + FormatPct(c.Threshold) + " en 36 meses"
		}
		return "cruza el " + FormatPct(c.Threshold) + " hacia " + FormatMonth(*c.Date)
	}
	switch f.Level {
	case ForecastUrgent:
		return fmt.Sprintf("Upgrade urgente %s: pico %s, %s, %s.", subject, FormatBps(peak), sixMonths, when(f.Urgent))
	case ForecastPlanning:
		return fmt.Sprintf("Planificar upgrade %s: pico %s, %s, %s.", subject, FormatBps(peak), sixMonths, when(f.Planning))
	}
	if growth <= 0 {
		return fmt.Sprintf("Sin acción %s: pico %s (%s de la capacidad), sin crecimiento (%s mensual).", subject, FormatBps(peak),
			FormatPct(peak/float64(f.CapacityBps)), FormatSignedPct(growth))
	}
	return fmt.Sprintf("Sin acción %s por ahora: pico %s (%s de la capacidad), %s mensual, %s.", subject, FormatBps(peak),
		FormatPct(peak/float64(f.CapacityBps)), FormatSignedPct(growth), when(f.Planning))
}

func deref(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}
