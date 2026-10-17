package reporting

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Formato de datos de los reportes (horus-identity, marca-y-escritura):
// fechas dd/mm/aaaa HH:mm en la zona del ISP, volumen en bytes SI decimal,
// porcentajes con coma decimal.

// FmtDateTime es dd/mm/aaaa HH:mm en loc.
func FmtDateTime(t time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	return t.In(loc).Format("02/01/2006 15:04")
}

// FmtDate es dd/mm/aaaa en loc.
func FmtDate(t time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	return t.In(loc).Format("02/01/2006")
}

// FmtPeriod describe el periodo con el último instante incluido
// ("06/10/2026 00:00 – 12/10/2026 23:59").
func FmtPeriod(p Period) string {
	return FmtDateTime(p.From, p.Loc) + " – " + FmtDateTime(p.To.Add(-time.Minute), p.Loc)
}

// FmtBytes es un volumen en bytes con prefijos SI decimales (1 kB = 1000 B).
func FmtBytes(b uint64) string {
	units := []string{"B", "kB", "MB", "GB", "TB", "PB", "EB"}
	v := float64(b)
	i := 0
	for v >= 1000 && i < len(units)-1 {
		v /= 1000
		i++
	}
	if i == 0 {
		return strconv.FormatUint(b, 10) + " B"
	}
	return decimal(v, 2) + " " + units[i]
}

// FmtBitsPerSecond es una tasa en bits/s con prefijos SI decimales.
func FmtBitsPerSecond(bps float64) string {
	units := []string{"b/s", "kb/s", "Mb/s", "Gb/s", "Tb/s"}
	i := 0
	for bps >= 1000 && i < len(units)-1 {
		bps /= 1000
		i++
	}
	return decimal(bps, 2) + " " + units[i]
}

// FmtPercent es una proporción [0, 1] como porcentaje ("99,95 %"); nil → "—".
func FmtPercent(r *float64, digits int) string {
	if r == nil {
		return "—"
	}
	return decimal(*r*100, digits) + " %"
}

// FmtDuration es una duración legible en español ("2 d 3 h", "4 h 12 min", "35 min").
func FmtDuration(seconds float64) string {
	d := time.Duration(seconds * float64(time.Second)).Round(time.Minute)
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	hours := int(d / time.Hour)
	mins := int((d - time.Duration(hours)*time.Hour) / time.Minute)
	switch {
	case days > 0:
		return fmt.Sprintf("%d d %d h", days, hours)
	case hours > 0:
		return fmt.Sprintf("%d h %d min", hours, mins)
	}
	return fmt.Sprintf("%d min", mins)
}

// FmtChange es la variación relativa de cur frente a prev ("+12,3 %"); sin base → "—".
func FmtChange(cur, prev uint64) string {
	if prev == 0 {
		return "—"
	}
	c := (float64(cur) - float64(prev)) / float64(prev) * 100
	sign := "+"
	if c < 0 {
		sign = "-" // U+2212 no existe en cp1252 (fuentes estándar del PDF)
		c = -c
	}
	return sign + decimal(c, 1) + " %"
}

// FmtInt es un entero con separador de miles (espacio fino no separable no
// existe en cp1252: se usa el punto, como es habitual en español).
func FmtInt(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func decimal(v float64, digits int) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "—"
	}
	return strings.Replace(strconv.FormatFloat(v, 'f', digits, 64), ".", ",", 1)
}

// Etiquetas en español de los valores de dominio (los desconocidos se muestran tal cual).
var (
	severityLabels = map[string]string{"critical": "Crítica", "high": "Alta", "medium": "Media", "low": "Baja", "info": "Info"}
	stateLabels    = map[string]string{"open": "Abierto", "acknowledged": "Reconocido", "resolved": "Resuelto", "false_positive": "Falso positivo"}
	securityLabels = map[string]string{"clean": "Limpio", "suspected": "Sospechoso", "infected": "Infectado", "mitigated": "Mitigado"}
	kindLabels     = map[string]string{
		"botnet_c2_communication": "Comunicación con C2 de botnet",
		"ddos_participation":      "Participación en DDoS",
		"outbound_scanning":       "Escaneo saliente",
		"outbound_fanout":         "Abanico de conexiones salientes",
		"spam_smtp_outbound":      "Spam SMTP saliente",
		"open_proxy_abuse":        "Abuso de proxy abierto",
		"cryptomining":            "Minería de criptomonedas",
		"beaconing":               "Beaconing",
		"reputation_hit":          "Contacto con destino en lista de reputación",
	}
	channelLabels = map[string]string{"email": "Correo", "telegram": "Telegram", "librenms": "LibreNMS"}
	eventLabels   = map[string]string{
		"finding_opened": "Hallazgo abierto", "finding_reopened": "Hallazgo reabierto",
		"exporter_silent": "Exportador silencioso", "exporter_recovered": "Exportador recuperado",
		"tunnel_down": "Túnel caído", "tunnel_recovered": "Túnel recuperado",
		"router_offline": "Router sin conexión", "router_recovered": "Router recuperado",
		"router_cpu_high": "CPU alta en router", "router_memory_high": "Memoria alta en router",
		"router_temperature_high": "Temperatura alta en router", "router_hardware_failed": "Fallo de hardware en router",
		"router_interface_down": "Interfaz caída", "router_interface_up": "Interfaz recuperada", "router_rebooted": "Router reiniciado",
		"service_down": "Servicio de infraestructura caído", "service_degraded": "Servicio de infraestructura degradado",
		"service_recovered": "Servicio de infraestructura recuperado",
	}
)

func label(m map[string]string, k string) string {
	if v, ok := m[k]; ok {
		return v
	}
	if k == "" {
		return "—"
	}
	return k
}

// SeverityLabel, StateLabel, SecurityLabel, KindLabel, ChannelLabel y EventLabel traducen valores de dominio.
func SeverityLabel(s string) string { return label(severityLabels, s) }

// StateLabel traduce el estado de un hallazgo.
func StateLabel(s string) string { return label(stateLabels, s) }

// SecurityLabel traduce el estado de seguridad de un cliente.
func SecurityLabel(s string) string { return label(securityLabels, s) }

// KindLabel traduce el tipo de hallazgo.
func KindLabel(s string) string { return label(kindLabels, s) }

// ChannelLabel traduce el tipo de canal de notificación.
func ChannelLabel(s string) string { return label(channelLabels, s) }

// EventLabel traduce el tipo de evento notificado.
func EventLabel(s string) string { return label(eventLabels, s) }

// SecurityStates es el orden de presentación de los estados de seguridad.
var SecurityStates = []string{"clean", "suspected", "infected", "mitigated"}
