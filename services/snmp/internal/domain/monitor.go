package domain

import "time"

// Condiciones vigiladas de un enlace (UplinkCondition del contrato).
const (
	ConditionSaturation = "saturation"
	ConditionLoss       = "loss"
	ConditionLatency    = "latency"
)

// Niveles de una condición.
const (
	LevelOK       = "ok"
	LevelWarning  = "warning"
	LevelCritical = "critical"
)

var levelRank = map[string]int{LevelOK: 0, LevelWarning: 1, LevelCritical: 2}

// LevelFor devuelve el nivel instantáneo de un valor con margen de
// histéresis: para bajar de un nivel el valor debe quedar por debajo de su
// umbral menos margin (evita alertas que suben y bajan en el borde).
func LevelFor(value, warn, crit, margin float64, current string) string {
	switch {
	case value >= crit:
		return LevelCritical
	case current == LevelCritical && value >= crit-margin:
		return LevelCritical
	case value >= warn:
		return LevelWarning
	case current != LevelOK && current != "" && value >= warn-margin:
		return LevelWarning
	}
	return LevelOK
}

// ConditionState es el estado sostenido de una condición (tabla
// snmp.uplink_condition): Level es el adoptado; Pending el candidato que aún
// no cumplió el tiempo sostenido.
type ConditionState struct {
	Level        string    `json:"level"`
	Since        time.Time `json:"since"`
	Value        float64   `json:"value"`
	Pending      string    `json:"pending"`
	PendingSince time.Time `json:"pending_since"`
	LastAt       time.Time `json:"last_at"`
}

// Step aplica una observación: un nivel distinto del adoptado se adopta
// cuando se mantiene sustained (los empeoramientos) o recover (las mejoras y
// la recuperación). Un hueco de datos mayor que gap reinicia el candidato.
// Devuelve el nivel anterior si cambió.
func (c *ConditionState) Step(level string, value float64, at time.Time, sustained, recover, gap time.Duration) (prev string, changed bool) {
	if c.Level == "" {
		c.Level, c.Since = LevelOK, at
	}
	if !c.LastAt.IsZero() && gap > 0 && at.Sub(c.LastAt) > gap {
		c.Pending = ""
	}
	c.LastAt = at
	if level == c.Level {
		c.Pending = ""
		if level != LevelOK {
			c.Value = max(c.Value, value)
		} else {
			c.Value = value
		}
		return "", false
	}
	if level != c.Pending {
		c.Pending, c.PendingSince = level, at
	}
	need := recover
	if levelRank[level] > levelRank[c.Level] {
		need = sustained
	}
	if at.Sub(c.PendingSince) < need {
		return "", false
	}
	prev = c.Level
	c.Level, c.Since, c.Value, c.Pending = level, c.PendingSince, value, ""
	return prev, true
}
