package domain

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Servicios de infraestructura del ISP (IPTV, HTTP, DNS, RADIUS…): sus
// servidores propios, no clientes. Chequeos activos desde el rol snmp y
// pasivos desde los flujos; estado por chequeo con histéresis y estado del
// servicio = el peor de sus chequeos conocidos.

// Estados de un chequeo o servicio.
const (
	StateUp       = "up"
	StateDown     = "down"
	StateDegraded = "degraded"
	StateUnknown  = "unknown"
)

// Tipos de chequeo.
const (
	CheckICMP      = "icmp"
	CheckTCP       = "tcp"
	CheckHTTP      = "http"
	CheckNoTraffic = "no_traffic"
	CheckDDoS      = "ddos_inbound"
)

// Razones (enum abierto del contrato).
const (
	ReasonOK              = "ok"
	ReasonTimeout         = "timeout"
	ReasonRefused         = "refused"
	ReasonUnreachable     = "unreachable"
	ReasonTLSError        = "tls_error"
	ReasonHTTPStatus      = "http_status"
	ReasonError           = "error"
	ReasonNoTraffic       = "no_traffic"
	ReasonNoBaseline      = "no_traffic_baseline"
	ReasonDDoSInbound     = "ddos_inbound"
	ReasonNoFlowData      = "no_flow_data"
	ReasonFlowsOff        = "flows_unavailable"
	ReasonICMPOff         = "icmp_unavailable"
	ReasonICMPv6          = "icmp_ipv6_unsupported"
	ReasonRouterOff       = "router_unavailable"
	ReasonNotForwardable  = "not_forwardable"
	ReasonPending         = "pending"
	ReasonDisabledService = "disabled"
)

// Límites del contrato.
const (
	MinIntervalSeconds   = 30
	MaxIntervalSeconds   = 3600
	DefaultInterval      = 60
	DefaultFailures      = 3
	DefaultRecoveries    = 2
	MaxChecks            = 10
	MaxServicesPerTenant = 500
	RecentResults        = 30
	DefaultWindowMinutes = 15
	DDoSWindow           = 5 * time.Minute
	// UnknownAfter: observaciones unknown seguidas (mínimo) para dejar de
	// afirmar un estado conocido.
	UnknownAfter = 3
)

// Check es un chequeo configurado (jsonb `checks` de snmp.infra_service).
type Check struct {
	ID                uuid.UUID `json:"id"`
	Type              string    `json:"type"`
	Port              *int      `json:"port"`
	Path              *string   `json:"path"`
	HTTPS             *bool     `json:"https"`
	TLSVerify         *bool     `json:"tls_verify"`
	ExpectedStatusMin *int      `json:"expected_status_min"`
	ExpectedStatusMax *int      `json:"expected_status_max"`
	ForwardPort       *int      `json:"forward_port"`
	WindowMinutes     *int      `json:"window_minutes"`
	MinBytes          *int64    `json:"min_bytes"`
	PPSThreshold      *float64  `json:"pps_threshold"`
	BPSThreshold      *float64  `json:"bps_threshold"`
}

// Active indica si el chequeo lo hace el sondeador (no los flujos).
func (c Check) Active() bool { return c.Type == CheckICMP || c.Type == CheckTCP || c.Type == CheckHTTP }

// EffectivePort es el puerto del servidor (tcp/http con defecto 80/443).
func (c Check) EffectivePort() int {
	if c.Port != nil {
		return *c.Port
	}
	if c.Type == CheckHTTP && c.HTTPS != nil && *c.HTTPS {
		return 443
	}
	if c.Type == CheckHTTP {
		return 80
	}
	return 0
}

// StatusRange devuelve el rango esperado de un chequeo http.
func (c Check) StatusRange() (lo, hi int) {
	lo, hi = 200, 399
	if c.ExpectedStatusMin != nil {
		lo = *c.ExpectedStatusMin
	}
	if c.ExpectedStatusMax != nil {
		hi = *c.ExpectedStatusMax
	}
	return lo, hi
}

// Window es la ventana de no_traffic.
func (c Check) Window() time.Duration {
	if c.WindowMinutes != nil {
		return time.Duration(*c.WindowMinutes) * time.Minute
	}
	return DefaultWindowMinutes * time.Minute
}

// MinBytesOrDefault es el mínimo de bytes de no_traffic.
func (c Check) MinBytesOrDefault() int64 {
	if c.MinBytes != nil {
		return *c.MinBytes
	}
	return 1
}

// MinuteTraffic es el tráfico de un objetivo en un minuto (flows_raw).
type MinuteTraffic struct {
	Minute time.Time
	// Bytes en ambos sentidos; InBytes/InPackets hacia el objetivo.
	Bytes, InBytes, InPackets uint64
}

// Result es un resultado reciente de un chequeo.
type Result struct {
	At        time.Time `json:"at"`
	State     string    `json:"state"`
	LatencyMs *float64  `json:"latency_ms"`
	Reason    *string   `json:"reason"`
}

// CheckObservation es lo que devuelve una ejecución de un chequeo antes de la
// histéresis: State up | down | degraded | unknown.
type CheckObservation struct {
	State     string
	Reason    string
	Detail    string
	LatencyMs *float64
	// HadTraffic: no_traffic vio tráfico en la ventana.
	HadTraffic bool
}

// CheckStatus es el estado de un chequeo (snmp.infra_check_state).
type CheckStatus struct {
	CheckID              uuid.UUID
	Type                 string
	State                string
	Reason               *string
	Detail               *string
	LatencyMs            *float64
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
	HadTraffic           bool
	Since                *time.Time
	LastCheckedAt        *time.Time
	Recent               []Result
}

// NewCheckStatus es el estado inicial (unknown, pendiente).
func NewCheckStatus(c Check) CheckStatus {
	r := ReasonPending
	return CheckStatus{CheckID: c.ID, Type: c.Type, State: StateUnknown, Reason: &r}
}

// Apply aplica una observación con histéresis y devuelve si cambió el
// estado: failuresBeforeDown observaciones malas iguales seguidas pasan a
// down/degraded; recoveriesBeforeUp buenas vuelven a up (desde unknown basta
// una). Las observaciones unknown no mueven el estado salvo que se repitan
// max(failuresBeforeDown, UnknownAfter) veces (p. ej. ClickHouse caído: el chequeo pasivo deja
// de saber).
func (s *CheckStatus) Apply(o CheckObservation, failuresBeforeDown, recoveriesBeforeUp int, at time.Time) bool {
	if failuresBeforeDown < 1 {
		failuresBeforeDown = 1
	}
	if recoveriesBeforeUp < 1 {
		recoveriesBeforeUp = 1
	}
	prevObs := ""
	if len(s.Recent) > 0 {
		prevObs = s.Recent[0].State
	}
	at = at.UTC()
	var reason *string
	if o.Reason != "" {
		r := o.Reason
		reason = &r
	}
	res := Result{At: at, State: o.State, LatencyMs: o.LatencyMs, Reason: reason}
	s.Recent = append([]Result{res}, s.Recent...)
	if len(s.Recent) > RecentResults {
		s.Recent = s.Recent[:RecentResults]
	}
	s.LastCheckedAt = &at
	s.LatencyMs = o.LatencyMs
	if o.Detail != "" {
		d := o.Detail
		s.Detail = &d
	} else {
		s.Detail = nil
	}
	if o.HadTraffic {
		s.HadTraffic = true
	}
	next := s.State
	switch o.State {
	case StateUp:
		s.ConsecutiveSuccesses++
		s.ConsecutiveFailures = 0
		if s.State == StateUnknown || s.ConsecutiveSuccesses >= recoveriesBeforeUp {
			next = StateUp
		}
	case StateDown, StateDegraded:
		s.ConsecutiveSuccesses = 0
		if prevObs == o.State {
			s.ConsecutiveFailures++
		} else {
			s.ConsecutiveFailures = 1
		}
		if s.ConsecutiveFailures >= failuresBeforeDown {
			next = o.State
		}
	default:
		s.ConsecutiveSuccesses = 0
		n := 0
		for _, r := range s.Recent {
			if r.State != StateUnknown {
				break
			}
			n++
		}
		if s.State == StateUnknown || n >= max(failuresBeforeDown, UnknownAfter) {
			next = StateUnknown
		}
	}
	changed := next != s.State
	if changed || next == o.State || s.Reason == nil {
		// La razón acompaña al estado: se actualiza al cambiar, cuando la
		// observación confirma el estado o si nunca la hubo.
		s.Reason = reason
	}
	if changed {
		s.State = next
		s.Since = &at
	}
	return changed
}

// ServiceStatus es el estado agregado del servicio.
type ServiceStatus struct {
	State     string
	Reason    *string
	CheckType *string
	Since     *time.Time
}

func rank(state string) int {
	switch state {
	case StateDown:
		return 3
	case StateDegraded:
		return 2
	case StateUp:
		return 1
	}
	return 0
}

// Worst devuelve el estado del servicio = el peor de sus chequeos conocidos
// (down > degraded > up); unknown solo si ninguno tiene estado. Devuelve el
// índice del chequeo que lo determina (-1 si no hay chequeos).
func Worst(checks []CheckStatus) (state string, idx int) {
	state, idx = StateUnknown, -1
	best := -1
	for i, c := range checks {
		if r := rank(c.State); r > best {
			best, state, idx = r, c.State, i
		}
	}
	if idx >= 0 {
		state = checks[idx].State
	}
	return state, idx
}

// Service es un servicio de infraestructura con su estado.
type Service struct {
	ID                 uuid.UUID
	TenantID           uuid.UUID
	Name               string
	SiteID             *uuid.UUID
	Target             netip.Addr
	Kind               string
	Checks             []Check
	IntervalSeconds    int
	FailuresBeforeDown int
	RecoveriesBeforeUp int
	Enabled            bool
	ProbeViaRouterID   *uuid.UUID
	Status             ServiceStatus
	CheckStatus        []CheckStatus
	CreatedAt          time.Time
	UpdatedAt          time.Time
	Version            int
}

// StatusOf devuelve el estado guardado de un chequeo (o el inicial).
func (s *Service) StatusOf(c Check) CheckStatus {
	for _, st := range s.CheckStatus {
		if st.CheckID == c.ID {
			st.Type = c.Type
			return st
		}
	}
	return NewCheckStatus(c)
}

// IsPrivate indica si la IP no es enrutable desde Internet (RFC 1918, ULA,
// CGNAT 100.64/10, enlace local): puede necesitar el script de router.
func IsPrivate(a netip.Addr) bool {
	a = a.Unmap()
	if a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLoopback() {
		return true
	}
	return netip.MustParsePrefix("100.64.0.0/10").Contains(a)
}

// FieldError es un error de validación de un campo.
type FieldError struct {
	Field string
	Code  string
	Msg   string
}

func (e FieldError) Error() string { return e.Field + ": " + e.Msg }

// Validate normaliza y valida el servicio (contrato InfraServiceInput):
// asigna id a los chequeos nuevos y aplica los defectos.
func (s *Service) Validate() []FieldError {
	var errs []FieldError
	add := func(field, code, msg string) { errs = append(errs, FieldError{Field: field, Code: code, Msg: msg}) }
	s.Name = strings.TrimSpace(s.Name)
	if n := len([]rune(s.Name)); n < 1 || n > 120 {
		add("name", "invalid_length", "El nombre debe tener entre 1 y 120 caracteres.")
	}
	s.Kind = strings.ToLower(strings.TrimSpace(s.Kind))
	if !kindOK(s.Kind) {
		add("kind", "invalid_format", "Etiqueta de 1 a 32 caracteres: minúsculas, dígitos, guion o guion bajo.")
	}
	if !s.Target.IsValid() || s.Target.IsUnspecified() || s.Target.IsMulticast() {
		add("target", "invalid_format", "IP del servidor no válida.")
	}
	s.Target = s.Target.Unmap()
	if s.IntervalSeconds == 0 {
		s.IntervalSeconds = DefaultInterval
	}
	if s.IntervalSeconds < MinIntervalSeconds || s.IntervalSeconds > MaxIntervalSeconds {
		add("interval_seconds", "out_of_range", fmt.Sprintf("El intervalo debe estar entre %d y %d s.", MinIntervalSeconds, MaxIntervalSeconds))
	}
	if s.FailuresBeforeDown == 0 {
		s.FailuresBeforeDown = DefaultFailures
	}
	if s.FailuresBeforeDown < 1 || s.FailuresBeforeDown > 20 {
		add("failures_before_down", "out_of_range", "Debe estar entre 1 y 20.")
	}
	if s.RecoveriesBeforeUp == 0 {
		s.RecoveriesBeforeUp = DefaultRecoveries
	}
	if s.RecoveriesBeforeUp < 1 || s.RecoveriesBeforeUp > 20 {
		add("recoveries_before_up", "out_of_range", "Debe estar entre 1 y 20.")
	}
	if len(s.Checks) == 0 || len(s.Checks) > MaxChecks {
		add("checks", "invalid_length", fmt.Sprintf("Entre 1 y %d chequeos.", MaxChecks))
	}
	seen := map[uuid.UUID]bool{}
	for i := range s.Checks {
		c := &s.Checks[i]
		if c.ID == uuid.Nil || seen[c.ID] {
			c.ID = uuid.Must(uuid.NewV7())
		}
		seen[c.ID] = true
		for _, e := range c.validate(s.ProbeViaRouterID != nil) {
			e.Field = fmt.Sprintf("checks[%d].%s", i, e.Field)
			errs = append(errs, e)
		}
	}
	return errs
}

func kindOK(k string) bool {
	if k == "" || len(k) > 32 {
		return false
	}
	for i, r := range k {
		alnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if !alnum && (i == 0 || (r != '-' && r != '_')) {
			return false
		}
	}
	return true
}

func (c *Check) validate(viaRouter bool) []FieldError {
	var errs []FieldError
	add := func(field, code, msg string) { errs = append(errs, FieldError{Field: field, Code: code, Msg: msg}) }
	only := func(allowed ...string) {
		set := map[string]bool{
			"port": c.Port != nil, "path": c.Path != nil, "https": c.HTTPS != nil, "tls_verify": c.TLSVerify != nil,
			"expected_status_min": c.ExpectedStatusMin != nil, "expected_status_max": c.ExpectedStatusMax != nil,
			"forward_port": c.ForwardPort != nil, "window_minutes": c.WindowMinutes != nil, "min_bytes": c.MinBytes != nil,
			"pps_threshold": c.PPSThreshold != nil, "bps_threshold": c.BPSThreshold != nil,
		}
		for f, present := range set {
			if present && !slices.Contains(allowed, f) {
				add(f, "not_allowed", "Campo no admitido para este tipo de chequeo.")
			}
		}
	}
	port := func(p *int, field string, required bool) {
		if p == nil {
			if required {
				add(field, "required", "Puerto obligatorio.")
			}
			return
		}
		if *p < 1 || *p > 65535 {
			add(field, "out_of_range", "Puerto entre 1 y 65535.")
		}
	}
	switch c.Type {
	case CheckICMP:
		only()
	case CheckTCP:
		only("port", "forward_port")
		port(c.Port, "port", true)
		port(c.ForwardPort, "forward_port", viaRouter)
	case CheckHTTP:
		only("port", "path", "https", "tls_verify", "expected_status_min", "expected_status_max", "forward_port")
		port(c.Port, "port", false)
		port(c.ForwardPort, "forward_port", viaRouter)
		if c.Path != nil && !pathOK(*c.Path) {
			add("path", "invalid_format", "Ruta absoluta sin espacios ni esquema (p. ej. /health).")
		}
		lo, hi := c.StatusRange()
		if lo < 100 || hi > 599 || lo > hi {
			add("expected_status_min", "out_of_range", "Rango de estado HTTP inválido (100–599, mínimo ≤ máximo).")
		}
	case CheckNoTraffic:
		only("window_minutes", "min_bytes")
		if c.WindowMinutes != nil && (*c.WindowMinutes < 5 || *c.WindowMinutes > 1440) {
			add("window_minutes", "out_of_range", "Ventana entre 5 y 1440 minutos.")
		}
		if c.MinBytes != nil && *c.MinBytes < 1 {
			add("min_bytes", "out_of_range", "Mínimo de bytes ≥ 1.")
		}
	case CheckDDoS:
		only("pps_threshold", "bps_threshold")
		if c.PPSThreshold == nil && c.BPSThreshold == nil {
			add("pps_threshold", "required", "Indique un umbral de pps o de bps.")
		}
		if (c.PPSThreshold != nil && *c.PPSThreshold <= 0) || (c.BPSThreshold != nil && *c.BPSThreshold <= 0) {
			add("pps_threshold", "out_of_range", "Los umbrales deben ser mayores que 0.")
		}
	default:
		add("type", "invalid_value", "Tipo de chequeo desconocido (icmp, tcp, http, no_traffic, ddos_inbound).")
	}
	return errs
}

func pathOK(p string) bool {
	if !strings.HasPrefix(p, "/") || len(p) > 256 || strings.HasPrefix(p, "//") {
		return false
	}
	for _, r := range p {
		if r <= ' ' || r == 0x7f || r == '#' || r == '\\' || r > 0x7e {
			return false
		}
	}
	return true
}
