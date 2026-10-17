package reporting

import (
	"fmt"
	"time"

	capreport "github.com/hcdestroyer/horus-flow/services/snmp/api/capacityreport"
)

// Etiquetas de la sección de capacidad.
var (
	forecastLabels = map[string]string{"ok": "Sin acción", "planning": "Planificar", "urgent": "Urgente",
		"insufficient_data": "Datos insuficientes", "no_capacity": "Sin capacidad"}
	healthLabels = map[string]string{"online": "Sano", "warning": "Aviso", "degraded": "Degradado", "critical": "Crítico",
		"offline": "Fuera de servicio", "unknown": "Sin datos"}
)

// ForecastLabel traduce el nivel de pronóstico de capacidad.
func ForecastLabel(s string) string { return label(forecastLabels, s) }

// HealthLabel traduce la salud de un nodo.
func HealthLabel(s string) string { return label(healthLabels, s) }

func forecastTone(s string) rgb {
	switch s {
	case "urgent":
		return colError
	case "planning":
		return colWarning
	case "ok":
		return colSuccess
	}
	return colMuted
}

func healthTone(s string) rgb {
	switch s {
	case "critical", "offline":
		return colError
	case "warning", "degraded":
		return colWarning
	case "online":
		return colSuccess
	}
	return colMuted
}

// fmtHours escribe horas con un decimal ("12,5 h").
func fmtHours(h float64) string { return decimal(h, 1) + " h" }

// fmtOpt escribe un valor opcional con su unidad.
func fmtOpt(v *float64, digits int, unit string) string {
	if v == nil {
		return "—"
	}
	return decimal(*v, digits) + unit
}

func utilCell(u capreport.Uplink, r *capreport.Report) cellv {
	s := FmtPercent(u.UtilP95, 1)
	switch {
	case u.UtilP95 == nil:
	case *u.UtilP95 >= r.SaturationCritical:
		return toned(s, colError)
	case *u.UtilP95 >= r.SaturationWarning:
		return toned(s, colWarning)
	}
	return plain(s)
}

func capBps(v int64) string {
	if v <= 0 {
		return "—"
	}
	return FmtBitsPerSecond(float64(v))
}

// capacitySection es la sección «Capacidad de enlaces» del PDF.
func capacitySection(p *pdfDoc, d *Data) {
	cp := d.Capacity
	if cp == nil {
		p.notice(d.CapacityNote)
		return
	}
	warn, crit := cp.SaturationWarning, cp.SaturationCritical
	p.paragraph(fmt.Sprintf("Por nodo, sus enlaces de subida en el periodo: p95 = el peor día (p95 de las medias de 5 min) contra la "+
		"capacidad vigente ese día; horas sobre el %s y el %s de la capacidad; pérdida y latencia medias de la sonda de su puerta "+
		"de enlace; nivel de pronóstico y recomendación (capacidad vigente hoy).", FmtPercent(&warn, 0), FmtPercent(&crit, 0)))
	if !cp.History {
		p.notice("Sin ClickHouse: no hay picos diarios ni pronósticos; solo la configuración de los enlaces.")
	}
	cols := []col{{"Enlace", 0.17, "L", false}, {"Capacidad", 0.1, "R", false}, {"p95", 0.1, "R", false},
		{"h > " + FmtPercent(&warn, 0), 0.1, "R", false}, {"h > " + FmtPercent(&crit, 0), 0.1, "R", false},
		{"Pérdida", 0.08, "R", false}, {"Latencia", 0.09, "R", false}, {"Pronóstico", 0.16, "L", false}, {"Cruce", 0.1, "L", false}}
	row := func(u capreport.Uplink) []cellv {
		cross := "—"
		if u.PlanningDate != nil {
			cross = u.PlanningDate.UTC().Format("01/2006")
		}
		return []cellv{plain(u.Label), plain(capBps(u.CapacityBps)), utilCell(u, cp), plain(fmtHours(u.HoursAboveWarning)),
			plain(fmtHours(u.HoursAboveCritical)), plain(fmtOpt(u.LossPct, 2, " %")), plain(fmtOpt(u.LatencyMs, 1, " ms")),
			toned(ForecastLabel(u.ForecastLevel), forecastTone(u.ForecastLevel)), plain(cross)}
	}
	if len(cp.Sites) == 0 {
		p.paragraph("Sin enlaces de subida detectados ni configurados.")
	}
	for _, s := range cp.Sites {
		p.ensure(24)
		p.font("B", 10)
		p.color(healthTone(s.Status))
		p.cell(contentW, 6, p.fit(fmt.Sprintf("%s · %s", s.SiteName, HealthLabel(s.Status)), contentW), "", 1, "L", false)
		if s.Summary != "" {
			p.paragraph(s.Summary)
		}
		var rows [][]cellv
		for _, u := range s.Uplinks {
			rows = append(rows, row(u))
		}
		p.table(cols, rows, "Sin enlaces en este nodo.")
		for _, u := range s.Uplinks {
			if u.Recommendation != "" {
				p.paragraph("• " + u.Recommendation)
			}
		}
	}
	if e := cp.Egress; e != nil {
		p.ensure(24)
		p.font("B", 10)
		p.color(colText)
		p.cell(contentW, 6, "Salida internacional agregada", "", 1, "L", false)
		p.table(cols, [][]cellv{row(*e)}, "")
		if e.Recommendation != "" {
			p.paragraph("• " + e.Recommendation)
		}
	}
	if t := cp.Traffic; t != nil {
		p.paragraph(fmt.Sprintf("Tráfico remoto del periodo: %s internacional y %s local (%s internacional, %s local, de ellos %s "+
			"por la lista local de prefijos; %s sin clasificar fuera de las proporciones).", FmtPercent(t.InternationalShare, 1),
			FmtPercent(t.LocalShare, 1), FmtBytes(t.InternationalBytes), FmtBytes(t.LocalBytes), FmtBytes(t.LocalByPrefixBytes),
			FmtBytes(t.UnknownBytes)))
	}
}

// capacityCSV es capacidad_enlaces.csv: una fila por enlace y la salida
// internacional, y al final el reparto local / internacional.
func capacityCSV(d *Data, _ *time.Location) [][]string {
	cp := d.Capacity
	if cp == nil {
		return note(d.CapacityNote)
	}
	rows := [][]string{{"nodo", "salud_nodo", "enlace", "router", "proveedor", "capacidad_bps", "util_p95_pct", "pico_p95_bps",
		"horas_sobre_aviso", "horas_sobre_critico", "dias_con_datos", "perdida_pct", "latencia_ms", "pronostico", "clave_pronostico",
		"cruce_planificacion", "recomendacion"}}
	line := func(site, health string, u capreport.Uplink) []string {
		cross := ""
		if u.PlanningDate != nil {
			cross = u.PlanningDate.UTC().Format("2006-01-02")
		}
		opt := func(v *float64, digits int) string {
			if v == nil {
				return ""
			}
			return decimal(*v, digits)
		}
		capS := ""
		if u.CapacityBps > 0 {
			capS = fmt.Sprintf("%d", u.CapacityBps)
		}
		return []string{site, health, u.Label, u.RouterName, u.Provider, capS, pct(u.UtilP95), decimal(u.PeakP95Bps, 0),
			decimal(u.HoursAboveWarning, 2), decimal(u.HoursAboveCritical, 2), itoa(u.Days), opt(u.LossPct, 3), opt(u.LatencyMs, 2),
			ForecastLabel(u.ForecastLevel), u.ForecastLevel, cross, u.Recommendation}
	}
	for _, s := range cp.Sites {
		for _, u := range s.Uplinks {
			rows = append(rows, line(s.SiteName, HealthLabel(s.Status), u))
		}
	}
	if e := cp.Egress; e != nil {
		rows = append(rows, line("", "", *e))
	}
	if t := cp.Traffic; t != nil {
		rows = append(rows, []string{}, []string{"trafico", "bytes", "proporcion_pct"},
			[]string{"internacional", utoa(t.InternationalBytes), pct(t.InternationalShare)},
			[]string{"local", utoa(t.LocalBytes), pct(t.LocalShare)},
			[]string{"local_por_prefijo", utoa(t.LocalByPrefixBytes), ""},
			[]string{"sin_clasificar", utoa(t.UnknownBytes), ""})
	}
	return rows
}
