package domain

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/hcdestroyer/horus-flow/services/snmp/api"
)

// Salud de un nodo: un único estado explicable que combina sus enlaces de
// subida (caída, pérdida, latencia, saturación), sus routers (estado del
// sondeador) y sus servicios de infraestructura. Vocabulario del estado de
// equipos (api.Status*): online, warning, degraded, critical, offline,
// unknown; siempre con razones legibles, la más grave primero.

// UplinkHealthInput es lo que la salud necesita de un enlace.
type UplinkHealthInput struct {
	Label string
	// OperUp: nil = desconocido.
	OperUp *bool
	// UtilNow es la utilización de la última muestra (0..1).
	UtilNow *float64
	// UtilP95 es el pico (p95 de 5 min) del último día completo o de hoy.
	UtilP95 *float64
	// SaturationState es el estado sostenido del monitor (ok | warning | critical).
	SaturationState string
	LossPct         *float64
	LatencyMs       *float64
	Diagnosis       string
}

// RouterHealthInput es el estado efectivo de un router del nodo.
type RouterHealthInput struct {
	Name   string
	Status string
	Reason string
}

// ServiceHealthInput es el estado de un servicio de infraestructura.
type ServiceHealthInput struct {
	Name  string
	State string // up | down | degraded | unknown
}

// SiteHealthInput reúne las señales de un nodo.
type SiteHealthInput struct {
	Uplinks  []UplinkHealthInput
	Routers  []RouterHealthInput
	Services []ServiceHealthInput
	Settings CapacitySettings
}

// HealthReason es una razón del estado del nodo.
type HealthReason struct {
	Status string `json:"status"`
	Code   string `json:"code"`
	Text   string `json:"text"`
}

// SiteHealth es el resultado.
type SiteHealth struct {
	Status  string         `json:"status"`
	Summary string         `json:"summary"`
	Reasons []HealthReason `json:"reasons"`
}

// rank ordena por gravedad (unknown solo gana si no hay nada más).
var healthRank = map[string]int{api.StatusUnknown: 0, api.StatusOnline: 1, api.StatusWarning: 2, api.StatusDegraded: 3,
	api.StatusCritical: 4, api.StatusOffline: 5}

var healthLabel = map[string]string{api.StatusOnline: "Sano", api.StatusWarning: "Aviso", api.StatusDegraded: "Degradado",
	api.StatusCritical: "Crítico", api.StatusOffline: "Fuera de servicio", api.StatusUnknown: "Sin datos"}

// HealthLabel devuelve el nombre en español de un estado de salud.
func HealthLabel(status string) string { return healthLabel[status] }

func pct1(v float64) string {
	if v < 10 {
		return strings.Replace(strconv.FormatFloat(v, 'f', 1, 64), ".", ",", 1) + " %"
	}
	return strconv.FormatFloat(v, 'f', 0, 64) + " %"
}

func ms(v float64) string { return strconv.FormatFloat(v, 'f', 0, 64) + " ms" }

// ComposeSiteHealth calcula la salud de un nodo.
func ComposeSiteHealth(in SiteHealthInput) SiteHealth {
	s := in.Settings
	var reasons []HealthReason
	add := func(status, code, text string) {
		reasons = append(reasons, HealthReason{Status: status, Code: code, Text: text})
	}
	known := false
	upCount, downCount := 0, 0
	for _, u := range in.Uplinks {
		label := "enlace " + u.Label
		if u.OperUp != nil {
			known = true
			if !*u.OperUp {
				downCount++
				add(api.StatusDegraded, "uplink_down", label+" caído")
				continue
			}
			upCount++
		}
		// Saturación: el monitor sostenido manda; si no, el p95 del día.
		var parts []string
		status, saturated := "", false
		util := u.UtilP95
		if u.UtilNow != nil && (util == nil || *u.UtilNow > *util) && u.SaturationState != "" && u.SaturationState != "ok" {
			util = u.UtilNow
		}
		if util != nil {
			known = true
			switch {
			case u.SaturationState == "critical" || *util >= s.SaturationCritical:
				status, saturated = api.StatusCritical, true
				parts = append(parts, "al "+FormatPct(*util)+" en hora pico")
			case u.SaturationState == "warning" || *util >= s.SaturationWarning:
				status, saturated = api.StatusWarning, true
				parts = append(parts, "al "+FormatPct(*util)+" en hora pico")
			}
		}
		if u.LossPct != nil {
			known = true
			switch {
			case *u.LossPct >= s.LossCriticalPct:
				status = worse(status, api.StatusCritical)
				parts = append(parts, pct1(*u.LossPct)+" de pérdida")
			case *u.LossPct >= s.LossWarningPct:
				status = worse(status, api.StatusDegraded)
				parts = append(parts, pct1(*u.LossPct)+" de pérdida")
			}
		}
		if u.LatencyMs != nil {
			known = true
			switch {
			case *u.LatencyMs >= s.LatencyCriticalMs:
				status = worse(status, api.StatusDegraded)
				parts = append(parts, "latencia de "+ms(*u.LatencyMs))
			case *u.LatencyMs >= s.LatencyWarningMs:
				status = worse(status, api.StatusWarning)
				parts = append(parts, "latencia de "+ms(*u.LatencyMs))
			}
		}
		if u.Diagnosis == DiagnosisFaulty || u.Diagnosis == DiagnosisFullFaulty {
			status = worse(status, api.StatusDegraded)
			parts = append(parts, "con errores o descartes de interfaz")
		}
		if status != "" {
			code := "uplink_quality"
			if saturated {
				code = "uplink_saturated"
			}
			add(status, code, label+" "+joinES(parts))
		}
	}
	if len(in.Uplinks) > 0 && downCount > 0 && upCount == 0 {
		reasons = append([]HealthReason{{Status: api.StatusOffline, Code: "all_uplinks_down", Text: "todos los enlaces de subida caídos"}}, reasons...)
	}
	for _, r := range in.Routers {
		switch r.Status {
		case api.StatusOnline:
			known = true
		case api.StatusOffline, api.StatusCritical, api.StatusDegraded, api.StatusWarning:
			known = true
			st := r.Status
			if st == api.StatusOffline {
				// Un router caído degrada el nodo; el nodo solo está fuera si no le queda ningún enlace.
				st = api.StatusCritical
			}
			text := "router " + r.Name + " " + strings.ToLower(healthLabel[r.Status])
			if r.Reason != "" && r.Reason != api.ReasonOK {
				text += " (" + strings.ReplaceAll(r.Reason, "_", " ") + ")"
			}
			add(st, "router_"+r.Status, text)
		}
	}
	for _, sv := range in.Services {
		switch sv.State {
		case "up":
			known = true
		case "down":
			known = true
			add(api.StatusCritical, "service_down", "servicio "+sv.Name+" caído")
		case "degraded":
			known = true
			add(api.StatusDegraded, "service_degraded", "servicio "+sv.Name+" degradado")
		}
	}
	out := SiteHealth{Status: api.StatusUnknown, Reasons: []HealthReason{}}
	if known {
		out.Status = api.StatusOnline
	}
	slices.SortStableFunc(reasons, func(a, b HealthReason) int { return healthRank[b.Status] - healthRank[a.Status] })
	for _, r := range reasons {
		out.Status = worse(out.Status, r.Status)
	}
	out.Reasons = append(out.Reasons, reasons...)
	switch {
	case len(reasons) == 0 && known:
		out.Summary = "Sano: enlaces, routers y servicios dentro de los umbrales"
	case len(reasons) == 0:
		out.Summary = "Sin datos: el nodo no tiene enlaces, routers ni servicios observados"
	default:
		texts := make([]string, 0, 3)
		for i, r := range reasons {
			if i == 3 {
				break
			}
			texts = append(texts, r.Text)
		}
		more := ""
		if len(reasons) > 3 {
			more = fmt.Sprintf(" (y %d más)", len(reasons)-3)
		}
		out.Summary = healthLabel[out.Status] + ": " + strings.Join(texts, "; ") + more
	}
	return out
}

func worse(a, b string) string {
	if healthRank[b] > healthRank[a] {
		return b
	}
	return a
}

// joinES une con comas e «y» final.
func joinES(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " y " + parts[len(parts)-1]
}
