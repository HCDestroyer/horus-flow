package domain

import (
	"math"
	"slices"
	"time"
)

// CapacityPeriod es la capacidad contratada de un enlace en una vigencia
// [ValidFrom, ValidTo) (tabla snmp.uplink_capacity_history). ContractedBps
// nil = sin capacidad contratada (se usa la velocidad del puerto); ValidTo
// nil = vigente.
type CapacityPeriod struct {
	ValidFrom     time.Time  `json:"valid_from"`
	ValidTo       *time.Time `json:"valid_to"`
	ContractedBps *int64     `json:"contracted_bps"`
	ChangedBy     *string    `json:"-"`
}

// MaxCapacityBackdate es lo más atrás que se puede fechar un cambio de
// capacidad (lo que guarda snmp.interface_5m para recalcular los picos).
const MaxCapacityBackdate = 90 * 24 * time.Hour

// SortPeriods ordena por inicio de vigencia.
func SortPeriods(h []CapacityPeriod) {
	slices.SortFunc(h, func(a, b CapacityPeriod) int { return a.ValidFrom.Compare(b.ValidFrom) })
}

// ContractedAt devuelve la capacidad contratada vigente en t según la
// historia (ordenada). Antes de la primera vigencia rige la primera (la
// capacidad que se configuró ya existía antes de configurarla). ok=false si
// no hay historia o t cae después de una vigencia cerrada sin otra abierta
// (enlace sin configuración en ese momento).
func ContractedAt(h []CapacityPeriod, t time.Time) (*int64, bool) {
	if len(h) == 0 {
		return nil, false
	}
	if t.Before(h[0].ValidFrom) {
		return h[0].ContractedBps, true
	}
	for i := len(h) - 1; i >= 0; i-- {
		p := h[i]
		if !t.Before(p.ValidFrom) {
			if p.ValidTo != nil && !t.Before(*p.ValidTo) {
				return nil, false
			}
			return p.ContractedBps, true
		}
	}
	return nil, false
}

// CapacityFunc da la capacidad (bits/s) con la que se mide la utilización
// en cada instante: la contratada vigente entonces o, si no hay, la
// velocidad del puerto (0 = desconocida).
type CapacityFunc func(at time.Time) int64

// CapacityFuncFor construye la función de capacidad de un enlace: la
// historia manda; sin historia, la contratada actual; y la velocidad del
// puerto cuando no hay contratada.
func CapacityFuncFor(h []CapacityPeriod, current *int64, speed *int64) CapacityFunc {
	return func(at time.Time) int64 {
		if c, ok := ContractedAt(h, at); ok {
			return CapacityBps(c, speed)
		}
		if len(h) == 0 {
			return CapacityBps(current, speed)
		}
		return CapacityBps(nil, speed)
	}
}

// SumCapacity suma las capacidades de varios enlaces en cada instante
// (salida internacional agregada).
func SumCapacity(fs []CapacityFunc) CapacityFunc {
	return func(at time.Time) int64 {
		var n int64
		for _, f := range fs {
			n += f(at)
		}
		return n
	}
}

// ComputeDailyAt es ComputeDaily con la capacidad vigente en cada intervalo
// de 5 min (un cambio de capacidad a media jornada cuenta bien las horas
// sobre umbral). La capacidad del día es la vigente al final del día.
func ComputeDailyAt(day time.Time, buckets []Bucket5m, capAt CapacityFunc, dayEnd time.Time, warn, crit float64) (DailyStats, bool) {
	out, ok := ComputeDaily(day, buckets, 0, warn, crit)
	out.CapacityBps = capAt(dayEnd.Add(-time.Second))
	for _, b := range buckets {
		if b.RxBps == nil && b.TxBps == nil {
			continue
		}
		c := capAt(b.At)
		if c <= 0 {
			continue
		}
		var r, t float64
		if b.RxBps != nil {
			r = *b.RxBps
		}
		if b.TxBps != nil {
			t = *b.TxBps
		}
		u := math.Max(r, t) / float64(c)
		if u >= warn {
			out.HoursAboveWarning += BucketSeconds / 3600.0
		}
		if u >= crit {
			out.HoursAboveCritical += BucketSeconds / 3600.0
		}
	}
	return out, ok
}

// WindowUtilP95 es la utilización p95 de una ventana de días: el peor día
// medido contra la capacidad vigente ese día (fallback si el día no la
// guardó). nil sin capacidad conocida en ningún día.
func WindowUtilP95(days []DailyStats, fallback int64) *float64 {
	var best *float64
	for _, d := range days {
		c := d.CapacityBps
		if c <= 0 {
			c = fallback
		}
		if c <= 0 || d.Samples == 0 {
			continue
		}
		u := d.PeakP95Bps() / float64(c)
		if best == nil || u > *best {
			v := u
			best = &v
		}
	}
	return best
}
