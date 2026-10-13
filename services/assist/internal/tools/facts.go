package tools

import (
	"fmt"
	"sort"
	"strings"
)

// Hechos redactados por herramienta (docs/assist.md §Anclaje): frases cortas
// con los valores exactos y sus unidades, en el orden en que un operador los
// leería. El modelo los cita como [F1], [F2]…; la verificación de la respuesta
// comprueba que los números que escriba salgan de aquí o del JSON.

func findingFacts(data any) []string {
	m := asMap(data)
	f := asMap(m["finding"])
	if f == nil {
		return nil
	}
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	add("Hallazgo %v: tipo %v, severidad %v, estado %v, confianza %s.", f["id"], f["kind"], f["severity"], f["state"], pct(f["confidence"]))
	if c := asMap(f["customer"]); c != nil {
		add("Cliente afectado: %v (tipo %v, estado de seguridad %v).", firstNonEmpty(c["address"], f["customer_id"]), c["kind"], c["security_state"])
	} else if f["customer_id"] != nil {
		add("Cliente afectado: customer_id %v.", f["customer_id"])
	}
	if s := asMap(f["summary"]); s != nil && s["text"] != nil {
		add("Resumen de Horus: %v", s["text"])
	} else if t, ok := f["summary"].(string); ok && t != "" {
		add("Resumen de Horus: %s", t)
	}
	if f["first_seen_at"] != nil || f["last_seen_at"] != nil {
		add("Primera señal %v; última %v; ocurrencias %v.", orDash(f["first_seen_at"]), orDash(f["last_seen_at"]), orDash(f["occurrences"]))
	}
	if t := asMap(f["primary_target"]); t != nil {
		add("Objetivo principal: %v %v.", t["type"], t["value"])
	}
	if rs := asList(f["reasons"]); len(rs) > 0 {
		var parts []string
		for i, r := range rs {
			if i >= 6 {
				break
			}
			switch x := r.(type) {
			case string:
				parts = append(parts, x)
			case map[string]any:
				parts = append(parts, fmt.Sprint(firstNonEmpty(x["text"], x["kind"], x["code"])))
			}
		}
		add("Razones de la detección: %s.", strings.Join(parts, "; "))
	}
	if sig := asMap(f["signals"]); len(sig) > 0 {
		keys := sortedKeys(sig)
		var parts []string
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%v", k, sig[k]))
		}
		add("Señales: %s.", strings.Join(parts, ", "))
	}
	switch ev := m["evidence_flows"].(type) {
	case string:
		add("Evidencia de flujos: %s.", ev)
	case map[string]any:
		add("Evidencia: %v flujos muestreados en %v grupos destino/puerto.", orDash(ev["flows_sampled"]), orDash(ev["groups"]))
		for i, raw := range asList(ev["top"]) {
			if i >= 8 {
				break
			}
			g := asMap(raw)
			if g == nil {
				continue
			}
			line := fmt.Sprintf("Destino %v puerto %v/%v (%v): %v flujos, %s, de %v a %v", g["remote"], g["remote_port"], g["protocol"],
				g["direction"], g["flows"], human(g["bytes"]), orDash(g["first_ts"]), orDash(g["last_ts"]))
			if g["remote_asn"] != nil {
				line += fmt.Sprintf(", AS%v", g["remote_asn"])
			}
			if g["reputation_category"] != nil {
				line += fmt.Sprintf(", reputación %v", g["reputation_category"])
			}
			add("%s.", line)
		}
	}
	return out
}

func routerFacts(data any) []string {
	m := asMap(data)
	r := asMap(m["router"])
	if r == nil {
		return nil
	}
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	add("Router %v (%v): estado %v, alta (onboarding) %v, RouterOS %v, modelo %v.", firstNonEmpty(r["name"], r["id"]), r["id"],
		orDash(r["state"]), orDash(firstNonEmpty(r["onboarding_state"], r["onboarding"])), orDash(r["routeros_version"]), orDash(r["model"]))
	if r["site_id"] != nil || r["site"] != nil {
		add("Nodo: %v.", firstNonEmpty(r["site"], r["site_id"]))
	}
	if r["last_seen_at"] != nil {
		add("Última vez visto: %v.", r["last_seen_at"])
	}
	if ws := asList(r["warnings"]); len(ws) > 0 {
		var parts []string
		for _, w := range ws {
			switch x := w.(type) {
			case string:
				parts = append(parts, x)
			case map[string]any:
				parts = append(parts, fmt.Sprint(firstNonEmpty(x["message"], x["code"])))
			}
		}
		add("Avisos del router: %s.", strings.Join(parts, "; "))
	} else {
		add("Avisos del router: ninguno.")
	}
	if c := asMap(r["credentials"]); c != nil {
		add("Credenciales configuradas: %v.", strings.Join(trueKeys(c), ", "))
	}
	switch p := m["wireguard_peer"].(type) {
	case nil:
		add("Túnel WireGuard: sin peer registrado para este router.")
	case string:
		add("Túnel WireGuard: %s.", p)
	case map[string]any:
		add("Túnel WireGuard: estado %v, último handshake %v (hace %v s), endpoint %v, keepalive %v.", orDash(p["state"]),
			orDash(firstNonEmpty(p["last_handshake_at"], p["latest_handshake_at"])), orDash(firstNonEmpty(p["handshake_age_seconds"], p["last_handshake_age_seconds"])),
			orDash(p["endpoint"]), orDash(p["persistent_keepalive"]))
	}
	switch e := m["flow_exporter"].(type) {
	case string:
		add("Exportador de flujos: %s.", e)
	case map[string]any:
		out = append(out, exporterLine(e))
	}
	return out
}

func exporterLine(e map[string]any) string {
	line := fmt.Sprintf("Exportador %v: estado %v desde %v; último flujo %v; %v flujos/s; pérdida %s",
		firstNonEmpty(e["router"], e["router_name"], e["router_id"]), orDash(e["state"]), orDash(e["state_since"]), orDash(e["last_flow_at"]),
		orDash(e["flows_per_second"]), pct(e["loss_ratio"]))
	if e["clock_skew_seconds"] != nil {
		line += fmt.Sprintf("; desfase de reloj %v s", e["clock_skew_seconds"])
	}
	if e["sampling_rate"] != nil {
		line += fmt.Sprintf("; muestreo 1:%v", e["sampling_rate"])
	}
	if e["protocol"] != nil {
		line += fmt.Sprintf("; protocolo %v", e["protocol"])
	}
	return line + "."
}

func exporterFacts(data any) []string {
	m := asMap(data)
	if e := asMap(m["exporter"]); e != nil {
		return []string{exporterLine(e)}
	}
	var out []string
	if by := asMap(m["by_state"]); len(by) > 0 {
		var parts []string
		for _, k := range sortedKeys(by) {
			parts = append(parts, fmt.Sprintf("%s=%v", k, by[k]))
		}
		out = append(out, "Exportadores por estado: "+strings.Join(parts, ", ")+".")
	}
	list := asList(m["exporters"])
	if len(list) == 0 {
		out = append(out, "No hay exportadores de flujos registrados en este ISP.")
	}
	for i, raw := range list {
		if i >= 12 {
			out = append(out, fmt.Sprintf("Hay %d exportadores en total; se listan 12.", len(list)))
			break
		}
		if e := asMap(raw); e != nil {
			out = append(out, exporterLine(e))
		}
	}
	return out
}

func findingsListFacts(data any) []string {
	m := asMap(data)
	list := asList(m["findings"])
	if len(list) == 0 {
		return []string{"No hay hallazgos abiertos ni reconocidos con ese filtro."}
	}
	out := []string{fmt.Sprintf("%d hallazgos abiertos o reconocidos (ordenados por severidad).", len(list))}
	for i, raw := range list {
		if i >= 15 {
			break
		}
		f := asMap(raw)
		cust := fmt.Sprint(f["customer_id"])
		if c := asMap(f["customer"]); c != nil && c["address"] != nil {
			cust = fmt.Sprint(c["address"])
		}
		out = append(out, fmt.Sprintf("%v: %v, severidad %v, confianza %s, cliente %s, última señal %v. %v", f["id"], f["kind"],
			f["severity"], pct(f["confidence"]), cust, orDash(f["last_seen_at"]), orEmpty(f["summary"])))
	}
	return out
}

func trafficTopFacts(data any) []string {
	m := asMap(data)
	top := asMap(m["top"])
	rows := asList(top["rows"])
	if len(rows) == 0 {
		return []string{"Sin tráfico en ese rango (ninguna fila)."}
	}
	out := []string{fmt.Sprintf("Top %d filas (bytes totales del rango consultado).", len(rows))}
	for i, raw := range rows {
		if i >= 10 {
			break
		}
		r := asMap(raw)
		label := fmt.Sprint(firstNonEmpty(r["label"], r["name"], r["key"]))
		out = append(out, fmt.Sprintf("%d. %s: bajada %s, subida %s%s.", i+1, label, human(r["down_bytes"]), human(r["up_bytes"]), shareText(r["share"])))
	}
	return out
}

func customerFacts(data any) []string {
	m := asMap(data)
	c := asMap(m["customer"])
	if c == nil {
		return nil
	}
	out := []string{fmt.Sprintf("Cliente %v: tipo %v, estado %v, estado de seguridad %v, nodo %v.", firstNonEmpty(c["address"], c["id"]),
		orDash(c["kind"]), orDash(c["state"]), orDash(c["security_state"]), orDash(firstNonEmpty(c["site"], c["site_id"])))}
	switch fs := m["recent_findings"].(type) {
	case string:
		out = append(out, "Hallazgos recientes: "+fs+".")
	case []any:
		if len(fs) == 0 {
			out = append(out, "Hallazgos recientes: ninguno.")
		}
		for i, raw := range fs {
			if i >= 8 {
				break
			}
			f := asMap(raw)
			out = append(out, fmt.Sprintf("Hallazgo %v: %v, severidad %v, estado %v, última señal %v.", f["id"], f["kind"], f["severity"], f["state"], orDash(f["last_seen_at"])))
		}
	}
	return out
}

func reputationFacts(data any) []string {
	m := asMap(data)
	q := asMap(m["query"])
	var out []string
	what := fmt.Sprintf("AS%v", q["asn"])
	if p, _ := q["prefix"].(string); p != "" {
		what = p
	}
	if in, _ := m["in_isp_allowlist"].(bool); in {
		out = append(out, what+" está en la allowlist del ISP (tráfico esperado).")
	} else {
		out = append(out, what+" no está en la allowlist del ISP.")
	}
	rel := asList(m["open_findings_with_this_target"])
	out = append(out, fmt.Sprintf("Hallazgos abiertos con %s como objetivo: %d.", what, len(rel)))
	if feeds := asList(m["reputation_feeds"]); len(feeds) > 0 {
		var names []string
		for _, f := range feeds {
			names = append(names, fmt.Sprint(asMap(f)["name"]))
		}
		out = append(out, "Feeds de reputación activos: "+strings.Join(names, ", ")+".")
	}
	out = append(out, "Horus no consulta los feeds indicador por indicador: la coincidencia aparece como reputation_category en la evidencia.")
	return out
}

// --- utilidades ---------------------------------------------------------------------------

func pct(v any) string {
	f, ok := v.(float64)
	if !ok {
		return "—"
	}
	if f <= 1 {
		return fmt.Sprintf("%.0f %%", f*100)
	}
	return fmt.Sprintf("%.0f %%", f)
}

func human(v any) string {
	n := parseUint(v)
	if n == 0 {
		if v == nil {
			return "—"
		}
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	f := float64(n)
	i := 0
	for f >= 1000 && i < len(units)-1 {
		f /= 1000
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s (%d bytes)", f, units[i], n)
}

func shareText(v any) string {
	f, ok := v.(float64)
	if !ok {
		return ""
	}
	return fmt.Sprintf(", cuota %s", pct(f))
}

func orDash(v any) any {
	if v == nil || v == "" {
		return "—"
	}
	return v
}

func orEmpty(v any) any {
	if v == nil {
		return ""
	}
	return v
}

func firstNonEmpty(vs ...any) any {
	for _, v := range vs {
		if v != nil && v != "" {
			return v
		}
	}
	return nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func trueKeys(m map[string]any) []string {
	var out []string
	for _, k := range sortedKeys(m) {
		if b, ok := m[k].(bool); ok && b {
			out = append(out, k)
		}
	}
	if len(out) == 0 {
		return []string{"ninguna"}
	}
	return out
}
