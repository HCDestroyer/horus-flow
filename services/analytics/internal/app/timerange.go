// Package app son las consultas de tráfico de analytics sobre los agregados
// de ClickHouse (contrato C3): tops, series, atribución, tráfico de un
// cliente, propuestas del modo descubrimiento y datos de widgets (C9).
package app

import (
	"errors"
	"fmt"
	"time"
)

// ErrRange es un rango inválido (422).
var ErrRange = errors.New("invalid time range")

// ErrRangeTooLarge es un rango por encima de lo que permiten los datos (422 TIME_RANGE_TOO_LARGE).
var ErrRangeTooLarge = errors.New("time range too large")

var relative = map[string]time.Duration{
	"15m": 15 * time.Minute, "1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour,
	"7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour, "90d": 90 * 24 * time.Hour,
}

// Range es un intervalo semiabierto [From, To).
type Range struct {
	From, To time.Time
}

// ParseRange resuelve range | from/to (from excluyente con range). def se
// usa si no llega nada.
func ParseRange(rng, from, to string, def string, now time.Time) (Range, error) {
	end := now.UTC()
	if to != "" {
		t, err := time.Parse(time.RFC3339, to)
		if err != nil {
			return Range{}, fmt.Errorf("%w: to", ErrRange)
		}
		end = t.UTC()
	}
	switch {
	case rng != "" && from != "":
		return Range{}, fmt.Errorf("%w: range and from are exclusive", ErrRange)
	case from != "":
		f, err := time.Parse(time.RFC3339, from)
		if err != nil || !f.Before(end) {
			return Range{}, fmt.Errorf("%w: from", ErrRange)
		}
		if end.Sub(f) > 400*24*time.Hour {
			return Range{}, ErrRangeTooLarge
		}
		return Range{From: f.UTC(), To: end}, nil
	}
	if rng == "" {
		rng = def
	}
	d, ok := relative[rng]
	if !ok {
		return Range{}, fmt.Errorf("%w: range %q", ErrRange, rng)
	}
	return Range{From: end.Add(-d), To: end}, nil
}

// Granularity elige la tabla de agregados y el paso según el rango
// (I1-08 criterio 2: 24 h ⇒ 5 min).
type Granularity struct {
	Step   time.Duration
	Suffix string // 5m | 1h | 1d
}

// Pick devuelve la granularidad para un rango y un paso pedido (0 = automático, ≤ ~500 puntos).
func Pick(r Range, step time.Duration) Granularity {
	span := r.To.Sub(r.From)
	g := Granularity{Step: 5 * time.Minute, Suffix: "5m"}
	switch {
	case span > 30*24*time.Hour:
		g = Granularity{Step: 24 * time.Hour, Suffix: "1d"}
	case span > 2*24*time.Hour:
		g = Granularity{Step: time.Hour, Suffix: "1h"}
	}
	if step > g.Step {
		// múltiplo del paso de la tabla
		g.Step = step.Truncate(g.Step)
	}
	for span/g.Step > 500 {
		g.Step *= 2
	}
	return g
}
