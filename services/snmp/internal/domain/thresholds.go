package domain

import (
	"strconv"

	"github.com/hcdestroyer/horus-flow/services/snmp/api"
)

// Thresholds son los umbrales configurados (HORUS_SNMP_*_WARNING/_CRITICAL).
type Thresholds struct {
	CPUWarning, CPUCritical                 float64
	MemoryWarning, MemoryCritical           float64
	TemperatureWarning, TemperatureCritical float64
	// SustainedCycles: ciclos seguidos sobre el umbral para CPU y memoria (3).
	SustainedCycles int
}

// DefaultThresholds son los valores por defecto del rol.
var DefaultThresholds = Thresholds{CPUWarning: 80, CPUCritical: 95, MemoryWarning: 85, MemoryCritical: 95,
	TemperatureWarning: 70, TemperatureCritical: 85, SustainedCycles: 3}

// Streaks cuenta, por métrica, los ciclos seguidos por encima de cada nivel.
// Se persiste con el estado del router.
type Streaks struct {
	Warning  map[string]int `json:"warning,omitempty"`
	Critical map[string]int `json:"critical,omitempty"`
}

// metricRule asocia una métrica con sus umbrales y si es sostenida.
type metricRule struct {
	metric    string
	value     *float64
	warn      float64
	crit      float64
	sustained bool
}

// Evaluate actualiza las rachas con la muestra y devuelve los umbrales
// superados (nivel máximo por métrica). CPU (media y núcleo más cargado) y
// memoria necesitan SustainedCycles ciclos seguidos; la temperatura, uno.
// Una métrica ausente (nil) reinicia su racha.
func (s *Streaks) Evaluate(t Thresholds, d api.DeviceSample) []api.ThresholdHit {
	if s.Warning == nil {
		s.Warning = map[string]int{}
	}
	if s.Critical == nil {
		s.Critical = map[string]int{}
	}
	n := t.SustainedCycles
	if n <= 0 {
		n = 1
	}
	var out []api.ThresholdHit
	for _, r := range []metricRule{
		{"cpu_percent", d.CPUPercent, t.CPUWarning, t.CPUCritical, true},
		{"cpu_max_core_percent", d.CPUMaxCorePercent, t.CPUWarning, t.CPUCritical, true},
		{"memory_percent", d.MemoryPercent, t.MemoryWarning, t.MemoryCritical, true},
		{"temperature_celsius", d.TemperatureCelsius, t.TemperatureWarning, t.TemperatureCritical, false},
		{"cpu_temperature_celsius", d.CPUTemperatureCelsius, t.TemperatureWarning, t.TemperatureCritical, false},
	} {
		if r.value == nil {
			delete(s.Warning, r.metric)
			delete(s.Critical, r.metric)
			continue
		}
		v := *r.value
		if v >= r.warn {
			s.Warning[r.metric]++
		} else {
			delete(s.Warning, r.metric)
		}
		if v >= r.crit {
			s.Critical[r.metric]++
		} else {
			delete(s.Critical, r.metric)
		}
		need := 1
		if r.sustained {
			need = n
		}
		switch {
		case s.Critical[r.metric] >= need:
			out = append(out, api.ThresholdHit{Metric: r.metric, Level: "critical", Value: v, Threshold: r.crit})
		case s.Warning[r.metric] >= need:
			out = append(out, api.ThresholdHit{Metric: r.metric, Level: "warning", Value: v, Threshold: r.warn})
		}
	}
	return out
}

// ThresholdValues son los umbrales editables por ISP (%, °C). Los de la
// instalación (HORUS_SNMP_*) son el valor por defecto.
type ThresholdValues struct {
	CPUWarning          float64 `json:"cpu_warning"`
	CPUCritical         float64 `json:"cpu_critical"`
	MemoryWarning       float64 `json:"memory_warning"`
	MemoryCritical      float64 `json:"memory_critical"`
	TemperatureWarning  float64 `json:"temperature_warning"`
	TemperatureCritical float64 `json:"temperature_critical"`
}

// Values devuelve la parte editable de unos umbrales.
func (t Thresholds) Values() ThresholdValues {
	return ThresholdValues{CPUWarning: t.CPUWarning, CPUCritical: t.CPUCritical, MemoryWarning: t.MemoryWarning,
		MemoryCritical: t.MemoryCritical, TemperatureWarning: t.TemperatureWarning, TemperatureCritical: t.TemperatureCritical}
}

// With aplica unos valores editables conservando el resto (ciclos sostenidos).
func (t Thresholds) With(v ThresholdValues) Thresholds {
	t.CPUWarning, t.CPUCritical, t.MemoryWarning, t.MemoryCritical = v.CPUWarning, v.CPUCritical, v.MemoryWarning, v.MemoryCritical
	t.TemperatureWarning, t.TemperatureCritical = v.TemperatureWarning, v.TemperatureCritical
	return t
}

// ThresholdError es un campo no válido.
type ThresholdError struct{ Field, Message string }

// Validate comprueba rangos (CPU y memoria 1–100 %, temperatura 20–150 °C) y
// que el aviso no supere al crítico.
func (v ThresholdValues) Validate() []ThresholdError {
	var out []ThresholdError
	check := func(name string, warn, crit, lo, hi float64, unit string) {
		rng := "Entre " + fmtNum(lo) + " y " + fmtNum(hi) + unit + "."
		if warn < lo || warn > hi {
			out = append(out, ThresholdError{name + "_warning", rng})
		}
		if crit < lo || crit > hi {
			out = append(out, ThresholdError{name + "_critical", rng})
		}
		if warn > crit {
			out = append(out, ThresholdError{name + "_warning", "El aviso no puede superar al crítico."})
		}
	}
	check("cpu", v.CPUWarning, v.CPUCritical, 1, 100, " %")
	check("memory", v.MemoryWarning, v.MemoryCritical, 1, 100, " %")
	check("temperature", v.TemperatureWarning, v.TemperatureCritical, 20, 150, " °C")
	return out
}

func fmtNum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
