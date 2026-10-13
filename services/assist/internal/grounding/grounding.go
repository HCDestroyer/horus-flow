// Package grounding verifica que una respuesta del asistente se apoye en los
// datos que recibió (docs/assist.md §Anclaje): cada IP, ASN, UUID, puerto,
// porcentaje y cifra con unidad que aparezca en la respuesta debe existir en
// alguna evidencia (resultados de herramientas, datos precargados, prompt), y
// cada comando RouterOS de escritura debe venir del catálogo verificado. Es
// independiente del modelo: cuanto más pequeño el modelo, más hace falta.
package grounding

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/hcdestroyer/horus-flow/services/assist/internal/tools"
)

// Status del anclaje de una respuesta.
type Status string

// Estados.
const (
	// Verified: todos los datos y comandos están respaldados.
	Verified Status = "verified"
	// Partial: hay datos o comandos sin respaldo; se listan.
	Partial Status = "partial"
	// Unverified: la respuesta no usó ninguna evidencia (ni herramientas ni datos precargados).
	Unverified Status = "unverified"
)

// Report es el resultado de la verificación.
type Report struct {
	Status Status `json:"status"`
	// Unsupported son los datos de la respuesta que no aparecen en la evidencia.
	Unsupported []string `json:"unsupported"`
	// UnverifiedCommands son comandos RouterOS fuera del catálogo (o de escritura sin deshacer).
	UnverifiedCommands []string `json:"unverified_commands"`
	// MissingSections son las secciones obligatorias que faltan.
	MissingSections []string `json:"missing_sections"`
	// Repaired indica que hubo un turno de corrección.
	Repaired bool `json:"repaired"`
}

// OK indica que no hay nada que corregir.
func (r Report) OK() bool {
	return r.Status == Verified && len(r.MissingSections) == 0
}

// Sections obligatorias de la respuesta (prompt.System).
var Sections = []string{"## Qué está pasando", "## Posibles causas", "## Qué hacer", "## Para deshacer", "## Fuentes"}

var (
	reIPv4   = regexp.MustCompile(`\b(?:\d{1,3}|x)\.(?:\d{1,3}|x)\.(?:\d{1,3}|x)\.(?:\d{1,3}|x)(?:/\d{1,2})?\b`)
	reASN    = regexp.MustCompile(`(?i)\bAS\s?(\d{2,10})\b`)
	reUUID   = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	rePort   = regexp.MustCompile(`(?i)\bpuertos?\s+(?:TCP|UDP)?\s*(\d{1,5})\b|\b(?:TCP|UDP)/(\d{1,5})\b|\bdst-port=(\d{1,5})\b`)
	rePct    = regexp.MustCompile(`\b(\d{1,3}(?:[.,]\d+)?)\s?%`)
	reUnit   = regexp.MustCompile(`(?i)\b(\d[\d.,]*)\s?(Tbit/s|Gbit/s|Mbit/s|kbit/s|bit/s|Tbps|Gbps|Mbps|kbps|bps|TB|GB|MB|KB|kB|bytes|B|flujos/s|flujos|paquetes|conexiones|destinos|clientes|hallazgos|segundos|s)\b`)
	reFence  = regexp.MustCompile("(?s)```[a-zA-Z0-9_-]*\n(.*?)```")
	reDigits = regexp.MustCompile(`\d+`)
)

// Check verifica answer contra la evidencia (textos: resultados de
// herramientas, datos precargados y el prompt; se concatenan).
func Check(answer string, evidence []string) Report {
	rep := Report{Unsupported: []string{}, UnverifiedCommands: []string{}, MissingSections: []string{}}
	corpus := strings.ToLower(strings.Join(evidence, "\n"))
	numbers := numberSet(corpus)
	hasEvidence := strings.TrimSpace(corpus) != ""

	for _, s := range Sections {
		if !strings.Contains(answer, s) {
			rep.MissingSections = append(rep.MissingSections, strings.TrimPrefix(s, "## "))
		}
	}

	// Comandos: líneas de los bloques de código que empiezan por "/".
	for _, m := range reFence.FindAllStringSubmatch(answer, -1) {
		for _, line := range strings.Split(m[1], "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "/") {
				continue
			}
			if !tools.CommandAllowed(line) {
				rep.UnverifiedCommands = appendUnique(rep.UnverifiedCommands, line)
			}
		}
	}

	// Datos: lo que hay en el texto (fuera de los bloques de código, que ya se
	// comprobaron como comandos) debe estar en la evidencia.
	prose := reFence.ReplaceAllString(answer, " ")
	lower := strings.ToLower(prose)
	for _, ip := range reIPv4.FindAllString(prose, -1) {
		if !strings.Contains(corpus, strings.ToLower(ip)) {
			rep.Unsupported = appendUnique(rep.Unsupported, ip)
		}
	}
	for _, m := range reASN.FindAllStringSubmatch(prose, -1) {
		if !strings.Contains(corpus, "as"+m[1]) && !containsNumberField(corpus, m[1]) {
			rep.Unsupported = appendUnique(rep.Unsupported, "AS"+m[1])
		}
	}
	for _, id := range reUUID.FindAllString(prose, -1) {
		if !strings.Contains(corpus, strings.ToLower(id)) {
			rep.Unsupported = appendUnique(rep.Unsupported, id)
		}
	}
	for _, m := range rePort.FindAllStringSubmatch(prose, -1) {
		port := firstGroup(m[1:])
		if port != "" && !numbers[port] {
			rep.Unsupported = appendUnique(rep.Unsupported, "puerto "+port)
		}
	}
	for _, m := range rePct.FindAllStringSubmatch(lower, -1) {
		if !numberSupported(numbers, m[1], true) {
			rep.Unsupported = appendUnique(rep.Unsupported, m[1]+" %")
		}
	}
	for _, m := range reUnit.FindAllStringSubmatch(prose, -1) {
		if !numberSupported(numbers, m[1], false) {
			rep.Unsupported = appendUnique(rep.Unsupported, m[1]+" "+m[2])
		}
	}
	sort.Strings(rep.Unsupported)
	switch {
	case !hasEvidence:
		rep.Status = Unverified
	case len(rep.Unsupported) > 0 || len(rep.UnverifiedCommands) > 0:
		rep.Status = Partial
	default:
		rep.Status = Verified
	}
	return rep
}

// RepairMessage redacta el turno de corrección para el modelo.
func RepairMessage(r Report) string {
	var b strings.Builder
	b.WriteString("Verificación automática de tu respuesta contra los datos de las herramientas. Corrige y vuelve a escribir la respuesta completa (mismas secciones):\n")
	if len(r.Unsupported) > 0 {
		b.WriteString("- Estos datos NO aparecen en ninguna herramienta ni en los datos facilitados: " + strings.Join(r.Unsupported, ", ") +
			". Quítalos, sustitúyelos por el valor exacto que devolvió una herramienta, o escribe \"sin dato\". No los deduzcas ni los redondees.\n")
	}
	if len(r.UnverifiedCommands) > 0 {
		b.WriteString("- Estos comandos RouterOS no están en el catálogo verificado (routeros_commands): " + strings.Join(r.UnverifiedCommands, " | ") +
			". Sustitúyelos por una plantilla del catálogo (consulta routeros_commands si hace falta) o quítalos; solo se admiten además consultas de solo lectura (/… print).\n")
	}
	if len(r.MissingSections) > 0 {
		b.WriteString("- Faltan las secciones: " + strings.Join(r.MissingSections, ", ") + ". Incluye las cinco secciones en orden.\n")
	}
	b.WriteString("En \"Fuentes\" lista las herramientas y los hechos [F#] que usaste. Si algo no se puede determinar con los datos, dilo explícitamente.")
	return b.String()
}

// Warning es el aviso que se añade a una respuesta que no se pudo corregir.
func Warning(r Report) string {
	var parts []string
	if len(r.Unsupported) > 0 {
		parts = append(parts, "datos que no aparecen en Horus: "+strings.Join(r.Unsupported, ", "))
	}
	if len(r.UnverifiedCommands) > 0 {
		parts = append(parts, fmt.Sprintf("%d comando(s) RouterOS fuera del catálogo verificado", len(r.UnverifiedCommands)))
	}
	if r.Status == Unverified {
		parts = append(parts, "la respuesta no se apoya en datos de Horus")
	}
	if len(parts) == 0 {
		return ""
	}
	return "Verificación: " + strings.Join(parts, "; ") + ". Compruébalo antes de actuar."
}

// numberSet extrae todos los números de la evidencia en varias formas
// (tal cual, sin separadores de miles, con coma o punto decimal, y los
// números enteros derivados de redondear decimales).
func numberSet(corpus string) map[string]bool {
	set := map[string]bool{}
	for _, m := range regexp.MustCompile(`\d[\d.,]*`).FindAllString(corpus, -1) {
		for _, v := range variants(m) {
			set[v] = true
		}
	}
	return set
}

// variants devuelve las formas equivalentes de un número. Para la evidencia
// (loose) incluye la parte entera ("12.4 MB" respalda "12 MB"); para una
// afirmación no, porque "9,8 GB" no queda respaldado por un "9" cualquiera.
func variants(n string) []string { return variantsOf(n, true) }

func variantsOf(n string, loose bool) []string {
	n = strings.Trim(n, ".,")
	out := []string{n}
	plain := strings.NewReplacer(",", "", ".", "").Replace(n)
	out = append(out, plain)
	if i := strings.LastIndexAny(n, ".,"); i > 0 {
		intPart := strings.NewReplacer(",", "", ".", "").Replace(n[:i])
		frac := n[i+1:]
		out = append(out, intPart+"."+frac, intPart+","+frac)
		if loose {
			out = append(out, intPart)
		}
		// Redondeo a 1 decimal y al entero más próximo.
		if len(frac) >= 2 {
			if v, err := parseFloat(intPart + "." + frac); err == nil {
				out = append(out, fmt.Sprintf("%.1f", v), fmt.Sprintf("%.0f", v), strings.ReplaceAll(fmt.Sprintf("%.1f", v), ".", ","))
			}
		}
	}
	return out
}

func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%g", &f)
	return f, err
}

// numberSupported admite el número tal cual o alguna de sus variantes. Los
// porcentajes además admiten su forma 0–1 (confianza 0.92 → 92 %).
func numberSupported(set map[string]bool, n string, percent bool) bool {
	for _, v := range variantsOf(n, false) {
		if set[v] {
			return true
		}
	}
	if percent {
		if v, err := parseFloat(strings.ReplaceAll(n, ",", ".")); err == nil {
			for _, cand := range []string{fmt.Sprintf("%.2f", v/100), fmt.Sprintf("%.3f", v/100), fmt.Sprintf("%g", v/100)} {
				if set[cand] || set[strings.TrimPrefix(cand, "0")] {
					return true
				}
			}
		}
	}
	// Enteros pequeños (≤ 12) suelen ser conteos de la propia redacción ("3 pasos").
	if !strings.ContainsAny(n, ".,") {
		if v, err := parseFloat(n); err == nil && v <= 12 {
			return true
		}
	}
	return false
}

// containsNumberField busca un ASN como campo numérico ("remote_asn":15169).
func containsNumberField(corpus, n string) bool {
	return strings.Contains(corpus, `asn":`+n) || strings.Contains(corpus, "asn = "+n) || strings.Contains(corpus, "asn "+n)
}

func firstGroup(gs []string) string {
	for _, g := range gs {
		if g != "" {
			return g
		}
	}
	return ""
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// Digits devuelve los dígitos de un texto (ayuda para tests).
func Digits(s string) []string { return reDigits.FindAllString(s, -1) }
