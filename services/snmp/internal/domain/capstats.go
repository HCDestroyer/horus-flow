package domain

import (
	"math"
	"slices"
	"time"
)

// Bucket5m es un intervalo de 5 min de una interfaz (snmp.interface_5m):
// medias de bits/s y paquetes/s por sentido (nil = sin muestras) y sumas de
// errores y descartes. rx = entrante al router (bajada de Internet en un
// uplink), tx = saliente.
type Bucket5m struct {
	At     time.Time
	RxBps  *float64
	TxBps  *float64
	RxPps  *float64
	TxPps  *float64
	Errors uint64
	Drops  uint64
}

// BucketSeconds es la duración de un intervalo de 5 min.
const BucketSeconds = 300

// DailyStats son las estadísticas diarias de un enlace (snmp.uplink_daily):
// el «pico diario» es el p95 de las medias de 5 min, que descarta ráfagas
// sueltas y es la base del pronóstico.
type DailyStats struct {
	Day                time.Time `json:"day"`
	Samples            int       `json:"samples"`
	RxP95Bps           float64   `json:"rx_p95_bps"`
	TxP95Bps           float64   `json:"tx_p95_bps"`
	RxMaxBps           float64   `json:"rx_max_bps"`
	TxMaxBps           float64   `json:"tx_max_bps"`
	CapacityBps        int64     `json:"capacity_bps"`
	HoursAboveWarning  float64   `json:"hours_above_warning"`
	HoursAboveCritical float64   `json:"hours_above_critical"`
	Errors             uint64    `json:"errors"`
	Drops              uint64    `json:"drops"`
	Packets            float64   `json:"packets"`
}

// PeakP95Bps es el mayor p95 de los dos sentidos.
func (d DailyStats) PeakP95Bps() float64 { return math.Max(d.RxP95Bps, d.TxP95Bps) }

// UtilP95 es el pico diario sobre la capacidad (nil si no se conoce).
func (d DailyStats) UtilP95() *float64 {
	if d.CapacityBps <= 0 {
		return nil
	}
	v := d.PeakP95Bps() / float64(d.CapacityBps)
	return &v
}

// Percentile devuelve el percentil p (0..1) con interpolación lineal entre
// rangos (método 7 de Hyndman y Fan, el de R y NumPy). values no se modifica.
// Sin valores devuelve NaN.
func Percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return math.NaN()
	}
	s := slices.Clone(values)
	slices.Sort(s)
	if p <= 0 {
		return s[0]
	}
	if p >= 1 {
		return s[len(s)-1]
	}
	h := p * float64(len(s)-1)
	lo := math.Floor(h)
	i := int(lo)
	if i+1 >= len(s) {
		return s[i]
	}
	return s[i] + (h-lo)*(s[i+1]-s[i])
}

// ComputeDaily resume los intervalos de un día. capacity 0 = sin capacidad
// conocida (no se cuentan horas sobre umbral). Devuelve ok=false si ningún
// intervalo tiene tasa.
func ComputeDaily(day time.Time, buckets []Bucket5m, capacity int64, warn, crit float64) (DailyStats, bool) {
	out := DailyStats{Day: day.UTC().Truncate(24 * time.Hour), CapacityBps: capacity}
	var rx, tx []float64
	for _, b := range buckets {
		out.Errors += b.Errors
		out.Drops += b.Drops
		var pps float64
		if b.RxPps != nil {
			pps += *b.RxPps
		}
		if b.TxPps != nil {
			pps += *b.TxPps
		}
		out.Packets += pps * BucketSeconds
		if b.RxBps == nil && b.TxBps == nil {
			continue
		}
		out.Samples++
		var r, t float64
		if b.RxBps != nil {
			r = *b.RxBps
			rx = append(rx, r)
			out.RxMaxBps = math.Max(out.RxMaxBps, r)
		}
		if b.TxBps != nil {
			t = *b.TxBps
			tx = append(tx, t)
			out.TxMaxBps = math.Max(out.TxMaxBps, t)
		}
		if capacity > 0 {
			u := math.Max(r, t) / float64(capacity)
			if u >= warn {
				out.HoursAboveWarning += BucketSeconds / 3600.0
			}
			if u >= crit {
				out.HoursAboveCritical += BucketSeconds / 3600.0
			}
		}
	}
	if out.Samples == 0 {
		return out, false
	}
	if len(rx) > 0 {
		out.RxP95Bps = Percentile(rx, 0.95)
	}
	if len(tx) > 0 {
		out.TxP95Bps = Percentile(tx, 0.95)
	}
	return out, true
}

// Diagnósticos de un enlace (UplinkDiagnosis del contrato).
const (
	DiagnosisOK          = "ok"
	DiagnosisBusy        = "busy"            // p95 sobre el aviso: cerca de su capacidad
	DiagnosisFull        = "full"            // saturado: lleno, sin errores físicos
	DiagnosisFaulty      = "faulty"          // errores o descartes con el enlace holgado
	DiagnosisFullFaulty  = "full_and_faulty" // saturado y además con errores físicos
	DiagnosisNoData      = "no_data"
	DiagnosisNoCapacity  = "no_capacity" // sin capacidad contratada ni velocidad de puerto
	errorRatioFaulty     = 1e-4          // 0,01 % de paquetes con error: problema físico
	dropRatioIdleFaulty  = 1e-3          // 0,1 % de descartes con el enlace holgado
	minPacketsToJudge    = 1e5           // con menos paquetes no se juzgan las proporciones
	criticalHoursForFull = 1.0           // 1 h/día sobre el umbral crítico = saturado
)

// Diagnosis es la clasificación explicable de un enlace en una ventana.
type Diagnosis struct {
	Kind       string  `json:"kind"`
	ErrorRatio float64 `json:"error_ratio"`
	DropRatio  float64 `json:"drop_ratio"`
}

// Diagnose distingue «lleno» de «averiado» con la utilización y los
// contadores: los descartes con el enlace saturado son la cola llena
// (esperables); los errores de entrada/salida (CRC, alineación) o los
// descartes con el enlace holgado apuntan a un problema físico, de
// negociación o de política, no de capacidad.
func Diagnose(s DailyStats, warn, crit float64) Diagnosis {
	return DiagnoseWithUtil(s, s.UtilP95(), warn, crit)
}

// DiagnoseWithUtil es Diagnose con la utilización p95 ya calculada (p. ej.
// cada día contra la capacidad vigente ese día: WindowUtilP95).
func DiagnoseWithUtil(s DailyStats, u *float64, warn, crit float64) Diagnosis {
	d := Diagnosis{Kind: DiagnosisOK}
	if s.Samples == 0 {
		d.Kind = DiagnosisNoData
		return d
	}
	if s.Packets >= minPacketsToJudge {
		d.ErrorRatio = float64(s.Errors) / s.Packets
		d.DropRatio = float64(s.Drops) / s.Packets
	}
	if u == nil {
		if d.ErrorRatio >= errorRatioFaulty {
			d.Kind = DiagnosisFaulty
		} else {
			d.Kind = DiagnosisNoCapacity
		}
		return d
	}
	saturated := *u >= crit || s.HoursAboveCritical >= criticalHoursForFull
	busy := *u >= warn
	physical := d.ErrorRatio >= errorRatioFaulty
	idleDrops := !busy && d.DropRatio >= dropRatioIdleFaulty
	switch {
	case saturated && physical:
		d.Kind = DiagnosisFullFaulty
	case saturated:
		d.Kind = DiagnosisFull
	case physical || idleDrops:
		d.Kind = DiagnosisFaulty
	case busy:
		d.Kind = DiagnosisBusy
	}
	return d
}

// MergeDaily suma varias ventanas diarias (p. ej. los últimos 7 días) en una:
// los p95 son el máximo de los p95 diarios (el peor día), las horas y los
// contadores se suman y la capacidad es la última conocida.
func MergeDaily(days []DailyStats) DailyStats {
	var out DailyStats
	for i, d := range days {
		if i == 0 || d.Day.After(out.Day) {
			out.Day = d.Day
			if d.CapacityBps > 0 {
				out.CapacityBps = d.CapacityBps
			}
		}
		out.Samples += d.Samples
		out.RxP95Bps = math.Max(out.RxP95Bps, d.RxP95Bps)
		out.TxP95Bps = math.Max(out.TxP95Bps, d.TxP95Bps)
		out.RxMaxBps = math.Max(out.RxMaxBps, d.RxMaxBps)
		out.TxMaxBps = math.Max(out.TxMaxBps, d.TxMaxBps)
		out.HoursAboveWarning += d.HoursAboveWarning
		out.HoursAboveCritical += d.HoursAboveCritical
		out.Errors += d.Errors
		out.Drops += d.Drops
		out.Packets += d.Packets
		if out.CapacityBps == 0 && d.CapacityBps > 0 {
			out.CapacityBps = d.CapacityBps
		}
	}
	return out
}

// SumBuckets suma, intervalo a intervalo, los de varios enlaces (salida
// internacional agregada): el p95 diario de la suma es el pico real del
// conjunto (los picos de enlaces distintos no siempre coinciden). Un
// intervalo sin tasa en un enlace no suma nada de ese enlace.
func SumBuckets(lists [][]Bucket5m) []Bucket5m {
	byAt := map[time.Time]*Bucket5m{}
	add := func(dst **float64, v *float64) {
		if v == nil {
			return
		}
		if *dst == nil {
			z := 0.0
			*dst = &z
		}
		**dst += *v
	}
	for _, l := range lists {
		for _, b := range l {
			x := byAt[b.At]
			if x == nil {
				x = &Bucket5m{At: b.At}
				byAt[b.At] = x
			}
			add(&x.RxBps, b.RxBps)
			add(&x.TxBps, b.TxBps)
			add(&x.RxPps, b.RxPps)
			add(&x.TxPps, b.TxPps)
			x.Errors += b.Errors
			x.Drops += b.Drops
		}
	}
	out := make([]Bucket5m, 0, len(byAt))
	for _, b := range byAt {
		out = append(out, *b)
	}
	slices.SortFunc(out, func(a, b Bucket5m) int { return a.At.Compare(b.At) })
	return out
}
