package notify

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// NotifyConsumer es un durable del canal mínimo: uno por stream de origen
// (streams.yaml consumers_i1, alerts-notify-<sufijo>; deliver new).
type NotifyConsumer struct {
	Stream   string
	Suffix   string
	Subjects []string
}

// NotifyConsumers son los durables alerts-notify-*.
var NotifyConsumers = []NotifyConsumer{
	{Stream: "DETECTION_EVENTS", Suffix: "detection", Subjects: []string{"horus.detection.finding.opened.>", "horus.detection.finding.updated.>",
		"horus.detection.finding.resolved.>", "horus.detection.customer.security_state_changed.>"}},
	{Stream: "FLOWS_EVENTS", Suffix: "flows", Subjects: []string{"horus.flows.exporter.silent.>", "horus.flows.exporter.recovered.>"}},
	{Stream: "WIREGUARD_EVENTS", Suffix: "wireguard", Subjects: []string{"horus.wireguard.peer.handshake_stale.>", "horus.wireguard.peer.handshake_recovered.>"}},
	{Stream: "SNMP_EVENTS", Suffix: "snmp", Subjects: []string{"horus.snmp.router.state_changed.>", "horus.snmp.router.rebooted.>",
		"horus.snmp.interface.oper_status_changed.>", "horus.snmp.service.state_changed.>", "horus.snmp.uplink.condition_changed.>",
		"horus.snmp.capacity.forecast_changed.>"}},
}

// NotifySubjects son todos los filtros de los durables alerts-notify-*.
var NotifySubjects = func() []string {
	var out []string
	for _, c := range NotifyConsumers {
		out = append(out, c.Subjects...)
	}
	return out
}()

// Estados del router (rol snmp) y de un servicio de infraestructura.
const (
	routerOnline   = "online"
	routerWarning  = "warning"
	routerCritical = "critical"
	routerDegraded = "degraded"
	routerOffline  = "offline"
	serviceUp      = "up"
	serviceDown    = "down"
	serviceDegr    = "degraded"
)

// RouterInfo es lo que alerts necesita para nombrar un router en un mensaje.
type RouterInfo struct {
	Name   string
	SiteID uuid.UUID
}

// Directory resuelve nombres para los mensajes (opcional: sin él los
// mensajes usan lo que traiga el evento).
type Directory struct {
	// Router devuelve el nombre y el nodo de un router del tenant.
	Router func(ctx context.Context, tenant, routerID uuid.UUID) (RouterInfo, bool)
	// TenantSlug devuelve el slug del ISP (enlaces de la consola).
	TenantSlug func(ctx context.Context, tenant uuid.UUID) (string, bool)
}

// SlugCache memoriza los slugs (cambian rara vez) para no listar tenants en
// cada evento.
type SlugCache struct {
	mu   sync.Mutex
	at   time.Time
	byID map[uuid.UUID]string
}

// NewSlugCache crea la caché.
func NewSlugCache() *SlugCache { return &SlugCache{} }

// Get devuelve el slug del tenant (recarga con load cada 5 min o si falta).
func (c *SlugCache) Get(now time.Time, tenant uuid.UUID, load func() (map[uuid.UUID]string, bool)) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.byID[tenant]; ok && now.Sub(c.at) < 5*time.Minute {
		return s, true
	}
	m, ok := load()
	if !ok {
		return "", false
	}
	c.byID, c.at = m, now
	s, ok := m[tenant]
	return s, ok
}

func str(data map[string]any, k string) string {
	v, _ := data[k].(string)
	return v
}

// eventResource es el recurso que agrupa avisos (throttle) y empareja una
// recuperación con su alarma.
func eventResource(typ string, data map[string]any) string {
	// Una condición de un enlace (saturación, pérdida, latencia) es su propio
	// recurso: la pérdida no queda agrupada con la saturación del mismo enlace.
	if typ == "horus.snmp.uplink.condition_changed" {
		return str(data, "interface_id") + ":" + str(data, "condition")
	}
	if typ == "horus.snmp.capacity.forecast_changed" {
		return "forecast:" + str(data, "subject_id")
	}
	for _, k := range []string{"id", "interface_id", "service_id", "router_id", "peer_id", "customer_id"} {
		if v, ok := data[k].(string); ok && v != "" {
			return v
		}
	}
	return typ
}

var stateRank = map[string]int{routerOnline: 0, routerWarning: 1, routerCritical: 2, routerDegraded: 1, routerOffline: 3}

// mapEvent devuelve el tipo de notificación de un evento de dominio ("" =
// no se notifica).
func mapEvent(typ string, data map[string]any) string {
	switch typ {
	case "horus.detection.finding.opened":
		if prev, _ := data["previous_finding_id"].(string); prev != "" {
			return eventFindingReopened
		}
		return eventFindingOpened
	case "horus.detection.finding.updated":
		if st, _ := data["state"].(string); st == "open" {
			if prev, _ := data["previous_state"].(string); prev == "resolved" || prev == "false_positive" {
				return eventFindingReopened
			}
		}
	case "horus.detection.finding.resolved":
		return eventFindingResolved
	case "horus.detection.customer.security_state_changed":
		if str(data, "state") != str(data, "previous_state") {
			return eventCustomerSecurity
		}
	case "horus.flows.exporter.silent":
		return eventExporterSilent
	case "horus.flows.exporter.recovered":
		return eventExporterRecovered
	case "horus.wireguard.peer.handshake_stale":
		return eventTunnelDown
	case "horus.wireguard.peer.handshake_recovered":
		return eventTunnelRecovered
	case "horus.snmp.router.state_changed":
		return mapRouterState(str(data, "previous_state"), str(data, "state"), str(data, "reason"))
	case "horus.snmp.router.rebooted":
		return eventRouterRebooted
	case "horus.snmp.interface.oper_status_changed":
		prev, cur := str(data, "previous"), str(data, "current")
		switch {
		case cur == "up" && prev != "up":
			return eventRouterInterfaceUp
		case prev == "up" && (cur == "down" || cur == "lower_layer_down"):
			return eventRouterInterfaceDown
		}
	case "horus.snmp.service.state_changed":
		prev, cur := str(data, "previous_state"), str(data, "state")
		switch {
		case cur == serviceDown && prev != serviceDown:
			return eventServiceDown
		case cur == serviceDegr && prev != serviceDegr && prev != serviceDown:
			return eventServiceDegraded
		case cur == serviceUp && (prev == serviceDown || prev == serviceDegr):
			return eventServiceRecovered
		}
	case "horus.snmp.uplink.condition_changed":
		return mapUplinkCondition(str(data, "condition"), str(data, "previous_level"), str(data, "level"))
	case "horus.snmp.capacity.forecast_changed":
		return mapForecast(str(data, "previous_level"), str(data, "level"))
	}
	return ""
}

var levelRank = map[string]int{"ok": 0, "": 0, "warning": 1, "critical": 2}

// mapUplinkCondition: subir de nivel (ok → aviso → crítico) avisa; volver a
// ok recupera; bajar de crítico a aviso no se notifica (como los routers).
func mapUplinkCondition(condition, prev, level string) string {
	switch {
	case level == "ok" && levelRank[prev] > 0:
		return eventUplinkRecovered
	case levelRank[level] > levelRank[prev]:
		if condition == "saturation" {
			return eventUplinkSaturated
		}
		return eventUplinkDegraded
	}
	return ""
}

// mapForecast: entrar en planificación o en urgente avisa (urgente también
// desde planificación); volver a ok (o sin datos) desde un nivel accionable
// cierra el aviso; de urgente a planificación no se notifica.
func mapForecast(prev, level string) string {
	actionable := func(l string) bool { return l == "planning" || l == "urgent" }
	switch {
	case level == "urgent" && prev != "urgent":
		return eventCapacityUrgent
	case level == "planning" && !actionable(prev):
		return eventCapacityPlanning
	case !actionable(level) && actionable(prev):
		return eventCapacityCleared
	}
	return ""
}

// mapRouterState traduce una transición del estado observado del router. Los
// avisos de exportador e interfaz de subida llegan por sus propios eventos;
// una bajada de nivel (critical → warning) no se notifica.
func mapRouterState(prev, state, reason string) string {
	switch state {
	case routerOffline:
		if prev != routerOffline {
			return eventRouterOffline
		}
	case routerOnline:
		if prev != "" && prev != routerOnline {
			return eventRouterRecovered
		}
	case routerDegraded:
		// Vuelve a responder (ICMP) aunque SNMP falle: el router ya no está caído.
		if prev == routerOffline {
			return eventRouterRecovered
		}
	case routerWarning, routerCritical:
		if prev != "" && stateRank[state] < stateRank[prev] && prev != routerOffline && prev != routerDegraded {
			return ""
		}
		switch reason {
		case "cpu_high":
			return eventRouterCPUHigh
		case "memory_high":
			return eventRouterMemoryHigh
		case "temperature_high":
			return eventRouterTemperatureHigh
		case "psu_failed", "fan_failed":
			return eventRouterHardwareFailed
		}
	}
	return ""
}

var securityStateLabel = map[string]string{"clean": "limpio", "suspected": "sospechoso", "infected": "infectado", "mitigated": "mitigado"}

var severityLabel = map[string]string{"info": "informativa", "low": "baja", "medium": "media", "high": "alta", "critical": "crítica"}

var routerReasonText = map[string]string{
	"tunnel_down":                 "Sin respuesta y sin handshake del túnel WireGuard.",
	"host_unreachable_via_tunnel": "El túnel está activo pero el router no responde a ping ni a SNMP.",
	"snmp_unreachable":            "Responde a ping pero no a SNMP.",
	"snmp_auth_failed":            "Responde a ping pero rechaza la credencial SNMP.",
	"no_snmp_credentials":         "Responde a ping; sin credencial SNMP configurada.",
	"psu_failed":                  "Fallo de una fuente de alimentación.",
	"fan_failed":                  "Fallo de un ventilador.",
}

var conditionLabel = map[string]string{"saturation": "saturación", "loss": "pérdida de paquetes", "latency": "latencia"}

// conditionValue escribe el valor de una condición con su unidad.
func conditionValue(data map[string]any, key string) string {
	v, ok := data[key].(float64)
	if !ok {
		return "—"
	}
	switch str(data, "unit") {
	case "ratio":
		return strconv.FormatFloat(float64(int(v*100+0.5)), 'f', -1, 64) + " %"
	case "percent":
		return round1(v) + " %"
	case "ms":
		return strconv.FormatFloat(float64(int(v+0.5)), 'f', -1, 64) + " ms"
	}
	return num(v)
}

var checkLabel = map[string]string{"icmp": "ping (ICMP)", "tcp": "puerto TCP", "http": "HTTP", "no_traffic": "sin tráfico",
	"ddos_inbound": "DDoS entrante"}

// hitFor busca el umbral superado de una métrica en el evento (si viene).
func hitFor(data map[string]any, metrics ...string) (value, threshold float64, ok bool) {
	hits, _ := data["thresholds"].([]any)
	for _, h := range hits {
		m, _ := h.(map[string]any)
		if m == nil || !slices.Contains(metrics, str(m, "metric")) {
			continue
		}
		v, _ := m["value"].(float64)
		t, _ := m["threshold"].(float64)
		return v, t, true
	}
	return 0, 0, false
}

func num(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func round1(v float64) string { return strconv.FormatFloat(float64(int(v*10+0.5))/10, 'f', -1, 64) }

// renderCtx son los nombres resueltos para un evento.
type renderCtx struct {
	routerName string
	siteName   string
	slug       string
}

func (r renderCtx) router(id string) string {
	if r.routerName != "" {
		return "«" + r.routerName + "»"
	}
	if id == "" {
		return "(sin identificar)"
	}
	return id
}

func (r renderCtx) site() string {
	if r.siteName == "" {
		return ""
	}
	return "\nNodo: " + r.siteName
}

// render construye el mensaje sin la IP del cliente (los eventos de
// detection ya no la llevan, events.md §5.7) y devuelve su severidad.
func (s *Service) render(eventType string, data map[string]any, rc renderCtx) (Message, string) {
	sev := str(data, "severity")
	routerID := str(data, "router_id")
	link := func(path string) string {
		if s.baseURL == "" {
			return ""
		}
		if rc.slug != "" {
			return s.baseURL + "/t/" + rc.slug + path
		}
		return s.baseURL + path
	}
	routerLink := func() string {
		if routerID == "" {
			return ""
		}
		return link("/routers/" + routerID)
	}
	var m Message
	switch eventType {
	case eventFindingOpened, eventFindingReopened:
		summary := str(data, "kind")
		if sm, ok := data["summary"].(map[string]any); ok {
			if t, _ := sm["text"].(string); t != "" {
				summary = t
			}
		}
		verb := "Hallazgo abierto"
		if eventType == eventFindingReopened {
			verb = "Hallazgo reabierto"
		}
		m.Subject = verb + ": señales compatibles con " + strings.ReplaceAll(str(data, "kind"), "_", " ")
		m.Text = summary + "\nSeveridad: " + severityLabel[sev]
		if c := str(data, "confidence_level"); c != "" {
			m.Text += " · confianza " + c
		}
		if id := str(data, "id"); id != "" {
			m.Link = link("/security/findings/" + id)
		}
	case eventFindingResolved:
		m.Subject = "Hallazgo resuelto: " + strings.ReplaceAll(str(data, "kind"), "_", " ")
		m.Text, sev = "El hallazgo se cerró.", "info"
		if id := str(data, "id"); id != "" {
			m.Link = link("/security/findings/" + id)
		}
	case eventCustomerSecurity:
		m.Subject = "Estado de seguridad de un cliente: " + securityStateLabel[str(data, "state")]
		m.Text = "Antes: " + securityStateLabel[str(data, "previous_state")]
		if n, ok := data["open_findings"].(float64); ok {
			m.Text += " · hallazgos abiertos: " + num(n)
		}
		sev = "info"
		if s := str(data, "max_severity"); s != "" && (str(data, "state") == "suspected" || str(data, "state") == "infected") {
			sev = s
		}
	case eventExporterSilent:
		m.Subject, m.Text = "Exportador silencioso", "El router "+rc.router(routerID)+" dejó de enviar flujos."+rc.site()
		m.Link, sev = routerLink(), "high"
	case eventExporterRecovered:
		m.Subject, m.Text = "Exportador recuperado", "El router "+rc.router(routerID)+" vuelve a enviar flujos."+rc.site()
		m.Link, sev = routerLink(), "info"
	case eventTunnelDown:
		m.Subject, m.Text = "Túnel WireGuard caído", "Sin handshake del router "+rc.router(routerID)+"."+rc.site()
		m.Link, sev = routerLink(), "high"
	case eventTunnelRecovered:
		m.Subject, m.Text = "Túnel WireGuard recuperado", "El router "+rc.router(routerID)+" vuelve a tener túnel activo."+rc.site()
		m.Link, sev = routerLink(), "info"
	case eventRouterOffline:
		m.Subject = "Router " + rc.router(routerID) + " fuera de línea"
		m.Text = routerReasonText[str(data, "reason")]
		if m.Text == "" {
			m.Text = "No responde."
		}
		m.Text += rc.site()
		m.Link, sev = routerLink(), "critical"
	case eventRouterRecovered:
		m.Subject = "Router " + rc.router(routerID) + " recuperado"
		m.Text = "Vuelve a estar en línea."
		if str(data, "state") == routerDegraded {
			m.Text = "Vuelve a responder. " + routerReasonText[str(data, "reason")]
		}
		if p := str(data, "previous_state"); p != "" {
			m.Text += "\nEstado anterior: " + p
		}
		m.Text += rc.site()
		m.Link, sev = routerLink(), "info"
	case eventRouterCPUHigh, eventRouterMemoryHigh, eventRouterTemperatureHigh:
		label, unit, metrics := "CPU alta", " %", []string{"cpu_percent", "cpu_max_core_percent"}
		switch eventType {
		case eventRouterMemoryHigh:
			label, metrics = "Memoria alta", []string{"memory_percent"}
		case eventRouterTemperatureHigh:
			label, unit, metrics = "Temperatura alta", " °C", []string{"temperature_celsius", "cpu_temperature_celsius"}
		}
		m.Subject = label + " en el router " + rc.router(routerID)
		if v, t, ok := hitFor(data, metrics...); ok {
			m.Text = "Valor: " + round1(v) + unit + " (umbral " + num(t) + unit + ")."
		} else {
			m.Text = "Supera el umbral configurado."
		}
		sev = "medium"
		if str(data, "state") == routerCritical {
			sev = "high"
			m.Text = "Nivel crítico. " + m.Text
		}
		m.Text += rc.site()
		m.Link = routerLink()
	case eventRouterHardwareFailed:
		m.Subject = "Fallo de hardware en el router " + rc.router(routerID)
		m.Text = routerReasonText[str(data, "reason")] + rc.site()
		m.Link, sev = routerLink(), "critical"
	case eventRouterInterfaceDown, eventRouterInterfaceUp:
		name := str(data, "name")
		if name == "" {
			name = "(sin nombre)"
		}
		uplink, _ := data["uplink"].(bool)
		kind := "Interfaz"
		if uplink {
			kind = "Enlace de subida"
		}
		if eventType == eventRouterInterfaceDown {
			m.Subject = kind + " " + name + " caída en el router " + rc.router(routerID)
			m.Text = "Estado operativo: " + str(data, "previous") + " → " + str(data, "current") + "."
			sev = "medium"
			if uplink {
				sev = "high"
			}
		} else {
			m.Subject = kind + " " + name + " activa de nuevo en el router " + rc.router(routerID)
			m.Text = "Estado operativo: " + str(data, "previous") + " → up."
			sev = "info"
		}
		m.Text += rc.site()
		m.Link = routerLink()
	case eventRouterRebooted:
		m.Subject = "Router " + rc.router(routerID) + " reiniciado"
		m.Text = "Se detectó un reinicio"
		if prev, ok := data["previous_uptime_seconds"].(float64); ok && prev > 0 {
			m.Text += " tras " + humanDuration(time.Duration(prev)*time.Second) + " encendido"
		}
		m.Text += "." + rc.site()
		m.Link, sev = routerLink(), "medium"
	case eventServiceDown, eventServiceDegraded, eventServiceRecovered:
		name := str(data, "name")
		if name == "" {
			name = "(sin nombre)"
		}
		check := checkLabel[str(data, "check_type")]
		if check == "" {
			check = str(data, "check_type")
		}
		ddos := str(data, "check_type") == "ddos_inbound"
		switch eventType {
		case eventServiceDown:
			m.Subject, sev = "Servicio «"+name+"» caído", "high"
			if ddos {
				m.Subject, sev = "Posible DDoS entrante contra «"+name+"»", "critical"
			}
		case eventServiceDegraded:
			m.Subject, sev = "Servicio «"+name+"» degradado", "medium"
			if ddos {
				m.Subject, sev = "Tráfico entrante anómalo contra «"+name+"»", "high"
			}
		default:
			m.Subject, sev = "Servicio «"+name+"» recuperado", "info"
		}
		m.Text = "Comprobación: " + check
		if t := str(data, "target"); t != "" && slices.Contains([]string{"icmp", "tcp", "http"}, str(data, "check_type")) {
			// Destino de una comprobación activa: infraestructura del ISP, no un
			// cliente (ddos_inbound y no_traffic pueden apuntar a clientes: no se muestra).
			m.Text += " · " + t
		}
		if r := str(data, "reason"); r != "" {
			m.Text += "\nMotivo: " + strings.ReplaceAll(r, "_", " ")
		}
		m.Text += rc.site()
	case eventUplinkSaturated, eventUplinkDegraded, eventUplinkRecovered:
		label := str(data, "label")
		if label == "" {
			label = str(data, "interface_name")
		}
		cond := conditionLabel[str(data, "condition")]
		if cond == "" {
			cond = str(data, "condition")
		}
		where := "«" + label + "» del router " + rc.router(routerID)
		switch eventType {
		case eventUplinkRecovered:
			m.Subject, sev = "Enlace "+where+" normalizado ("+cond+")", "info"
			m.Text = "Valor actual: " + conditionValue(data, "value") + "."
		default:
			m.Subject = "Enlace " + where + ": " + cond + " sostenida"
			if str(data, "condition") == "saturation" {
				m.Subject = "Enlace " + where + " saturado"
			}
			sev = "medium"
			if str(data, "level") == "critical" {
				sev = "high"
			}
			m.Text = "Valor: " + conditionValue(data, "value") + " (umbral " + conditionValue(data, "threshold") + "), sostenido desde " +
				shortTime(str(data, "since")) + "."
		}
		if site := str(data, "site_name"); site != "" && rc.siteName == "" {
			m.Text += "\nNodo: " + site
		} else {
			m.Text += rc.site()
		}
		if id := str(data, "interface_id"); id != "" {
			m.Link = link("/capacity/uplinks/" + id)
		}
	case eventCapacityPlanning, eventCapacityUrgent, eventCapacityCleared:
		label := str(data, "label")
		if str(data, "subject_kind") == "international_egress" {
			label = "Salida internacional"
		}
		switch eventType {
		case eventCapacityUrgent:
			m.Subject, sev = "Capacidad urgente: "+label, "high"
		case eventCapacityPlanning:
			m.Subject, sev = "Planificar capacidad: "+label, "low"
		default:
			m.Subject, sev = "Capacidad sin urgencia: "+label, "info"
		}
		m.Text = str(data, "recommendation")
		m.Link = link("/capacity")
		if id := str(data, "subject_id"); id != "" && str(data, "subject_kind") == "uplink" {
			m.Link = link("/capacity/uplinks/" + id)
		}
	}
	return m, sev
}

// shortTime escribe un instante RFC 3339 como dd/mm/aaaa HH:mm UTC.
func shortTime(s string) string {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return s
	}
	return t.UTC().Format("02/01/2006 15:04") + " UTC"
}

func humanDuration(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d días", int(d.Hours()/24))
	case d >= 2*time.Hour:
		return fmt.Sprintf("%d horas", int(d.Hours()))
	default:
		return fmt.Sprintf("%d minutos", int(d.Minutes()))
	}
}

func isRecovery(eventType string) bool {
	_, ok := RecoveryOf[eventType]
	return ok
}

// matches indica si el canal recibe el evento por su suscripción (tipo o
// categoría, severidad mínima y nodo).
func matches(c *Channel, eventType, severity string, site *uuid.UUID) bool {
	if !c.Enabled || !c.Subscription.Subscribes(eventType) {
		return false
	}
	if c.Subscription.MinSeverity != nil && severity != "" && SeverityRank(severity) < SeverityRank(*c.Subscription.MinSeverity) {
		return false
	}
	if len(c.Subscription.SiteIDs) > 0 && (site == nil || !slices.Contains(c.Subscription.SiteIDs, *site)) {
		return false
	}
	return true
}
