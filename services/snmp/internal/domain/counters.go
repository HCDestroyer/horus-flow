package domain

import "time"

// Counters son los contadores crudos de una interfaz en un sondeo
// (IF-MIB: HC de 64 bits para octetos y paquetes; errores y descartes de 32).
type Counters struct {
	At          time.Time `json:"at"`
	Uptime      uint64    `json:"uptime"` // sysUpTime en centésimas
	InOctets    uint64    `json:"in_octets"`
	OutOctets   uint64    `json:"out_octets"`
	InUcast     uint64    `json:"in_ucast"`
	OutUcast    uint64    `json:"out_ucast"`
	InErrors    uint64    `json:"in_errors"`
	OutErrors   uint64    `json:"out_errors"`
	InDiscards  uint64    `json:"in_discards"`
	OutDiscards uint64    `json:"out_discards"`
}

// Rates son las tasas y deltas de un intervalo. Discontinuity = true
// (primer sondeo, reinicio o contador que retrocede) deja todo en nil.
type Rates struct {
	Discontinuity bool
	Seconds       float64
	RxBps, TxBps  *float64
	RxPps, TxPps  *float64
	RxBytesDelta  *uint64
	TxBytesDelta  *uint64
	RxErrors      *int64
	TxErrors      *int64
	RxDrops       *int64
	TxDrops       *int64
	UtilRx        *float64
	UtilTx        *float64
}

// Delta calcula las tasas entre prev y cur. speedBps = 0 → sin utilización.
// Un uptime menor que el anterior (reinicio) o cualquier contador que
// retrocede (reinicio del contador, wrap de Counter32) es una
// discontinuidad: no se inventan tasas (docs/events.md §8.4).
func Delta(prev *Counters, cur Counters, speedBps int64) Rates {
	if prev == nil {
		return Rates{Discontinuity: true}
	}
	secs := cur.At.Sub(prev.At).Seconds()
	if secs <= 0 || cur.Uptime < prev.Uptime {
		return Rates{Discontinuity: true, Seconds: secs}
	}
	for _, p := range [][2]uint64{{prev.InOctets, cur.InOctets}, {prev.OutOctets, cur.OutOctets}, {prev.InUcast, cur.InUcast},
		{prev.OutUcast, cur.OutUcast}, {prev.InErrors, cur.InErrors}, {prev.OutErrors, cur.OutErrors},
		{prev.InDiscards, cur.InDiscards}, {prev.OutDiscards, cur.OutDiscards}} {
		if p[1] < p[0] {
			return Rates{Discontinuity: true, Seconds: secs}
		}
	}
	r := Rates{Seconds: secs}
	inB, outB := cur.InOctets-prev.InOctets, cur.OutOctets-prev.OutOctets
	r.RxBytesDelta, r.TxBytesDelta = &inB, &outB
	r.RxBps = f(float64(inB) * 8 / secs)
	r.TxBps = f(float64(outB) * 8 / secs)
	r.RxPps = f(float64(cur.InUcast-prev.InUcast) / secs)
	r.TxPps = f(float64(cur.OutUcast-prev.OutUcast) / secs)
	r.RxErrors = i64(cur.InErrors - prev.InErrors)
	r.TxErrors = i64(cur.OutErrors - prev.OutErrors)
	r.RxDrops = i64(cur.InDiscards - prev.InDiscards)
	r.TxDrops = i64(cur.OutDiscards - prev.OutDiscards)
	if speedBps > 0 {
		r.UtilRx = f(clamp01(*r.RxBps / float64(speedBps)))
		r.UtilTx = f(clamp01(*r.TxBps / float64(speedBps)))
	}
	return r
}

func f(v float64) *float64 { return &v }

func i64(v uint64) *int64 {
	n := int64(v) //nolint:gosec // delta acotado por el intervalo
	return &n
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
