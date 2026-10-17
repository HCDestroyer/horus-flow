package domain

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Tipos de enlace de subida (UplinkKind del contrato).
const (
	UplinkKindInternational = "international_transit"
	UplinkKindLocalIX       = "local_ix"
	UplinkKindCache         = "cache"
	UplinkKindOther         = "other"
)

// UplinkKinds son los tipos admitidos.
var UplinkKinds = []string{UplinkKindInternational, UplinkKindLocalIX, UplinkKindCache, UplinkKindOther}

// Límites de la configuración de capacidad.
const (
	MaxUplinksPerTenant     = 500
	MaxLocalASNs            = 200
	MaxProbeTargets         = 10
	MinContractedBps        = 1_000_000         // 1 Mb/s
	MaxContractedBps        = 1_600_000_000_000 // 1,6 Tb/s
	DefaultPlanningPct      = 0.80              // umbral de planificación
	DefaultUrgentPct        = 0.90              // umbral urgente
	DefaultLeadDays         = 90                // antelación de la alerta de previsión
	DefaultSaturationWarn   = 0.70              // horas sobre el 70 %
	DefaultSaturationCrit   = 0.85              // horas sobre el 85 %
	DefaultSustainedMinutes = 15                // saturación sostenida para alertar
	DefaultLossWarningPct   = 1.0               // % de pérdida de las sondas
	DefaultLossCriticalPct  = 5.0               // % de pérdida de las sondas
	DefaultLatencyWarnMs    = 80.0              // ms de RTT medio
	DefaultLatencyCritMs    = 200.0             // ms de RTT medio
	maxLabelLen             = 120               // nombre visible del enlace
	maxProviderLen          = 120               // proveedor
	maxLeadDays             = 365               // antelación máxima
	minSustainedMinutes     = 5                 // un ciclo de 5 min como mínimo
	maxSustainedMinutes     = 240               // 4 h
	countryLen              = 2                 // ISO 3166-1 alfa-2
	probeTargetLabelMax     = 60                // etiqueta de un destino de sonda
)

// UplinkConfig es la configuración de un enlace de subida (tabla
// snmp.uplink_config): proveedor, tipo, capacidad contratada (puede ser menor
// que la velocidad del puerto) y pertenencia a la salida internacional
// agregada. Una interfaz con configuración cuenta como enlace de subida
// aunque el sondeador no la haya elegido como «el» uplink del router.
type UplinkConfig struct {
	InterfaceID         uuid.UUID  `json:"interface_id"`
	RouterID            uuid.UUID  `json:"router_id"`
	Label               string     `json:"label"`
	Provider            *string    `json:"provider"`
	Kind                string     `json:"kind"`
	ContractedBps       *int64     `json:"contracted_bps"`
	InternationalEgress bool       `json:"international_egress"`
	UpdatedAt           time.Time  `json:"updated_at"`
	UpdatedBy           *string    `json:"-"`
	Version             int        `json:"version"`
	CreatedAt           *time.Time `json:"-"`
}

// UplinkConfigInput es el cuerpo de PUT /capacity/uplinks/{interface_id}.
type UplinkConfigInput struct {
	RouterID            uuid.UUID `json:"router_id"`
	Label               string    `json:"label"`
	Provider            *string   `json:"provider"`
	Kind                string    `json:"kind"`
	ContractedBps       *int64    `json:"contracted_bps"`
	InternationalEgress *bool     `json:"international_egress"`
}

// Validate normaliza y valida la entrada. international_egress, si falta,
// vale true solo para tránsito internacional.
func (in *UplinkConfigInput) Validate() []FieldError {
	var errs []FieldError
	add := func(field, code, msg string) { errs = append(errs, FieldError{Field: field, Code: code, Msg: msg}) }
	if in.RouterID == uuid.Nil {
		add("router_id", "REQUIRED", "Indique el router del enlace.")
	}
	in.Label = strings.TrimSpace(in.Label)
	if n := len([]rune(in.Label)); n < 1 || n > maxLabelLen {
		add("label", "INVALID_LENGTH", fmt.Sprintf("El nombre debe tener entre 1 y %d caracteres.", maxLabelLen))
	}
	if in.Provider != nil {
		p := strings.TrimSpace(*in.Provider)
		switch {
		case p == "":
			in.Provider = nil
		case len([]rune(p)) > maxProviderLen:
			add("provider", "INVALID_LENGTH", fmt.Sprintf("El proveedor admite hasta %d caracteres.", maxProviderLen))
		default:
			in.Provider = &p
		}
	}
	in.Kind = strings.TrimSpace(in.Kind)
	if !slices.Contains(UplinkKinds, in.Kind) {
		add("kind", "INVALID_VALUE", "Tipo no admitido: international_transit, local_ix, cache u other.")
	}
	if in.ContractedBps != nil && (*in.ContractedBps < MinContractedBps || *in.ContractedBps > MaxContractedBps) {
		add("contracted_bps", "OUT_OF_RANGE", "La capacidad contratada va de 1 Mb/s a 1,6 Tb/s (en bits/s).")
	}
	if in.InternationalEgress == nil {
		v := in.Kind == UplinkKindInternational
		in.InternationalEgress = &v
	}
	return errs
}

// CapacityBps es la capacidad con la que se mide la utilización: la
// contratada si existe, si no la velocidad del puerto (0 = desconocida).
func CapacityBps(contracted *int64, speed *int64) int64 {
	if contracted != nil && *contracted > 0 {
		return *contracted
	}
	if speed != nil && *speed > 0 {
		return *speed
	}
	return 0
}

// ProbeTarget es un destino de las sondas de respaldo desde el host de Horus
// («ruta Horus → destino»): nunca mide un enlace concreto del ISP.
type ProbeTarget struct {
	Label   string     `json:"label"`
	Address netip.Addr `json:"address"`
}

// CapacitySettings son los ajustes de capacidad de un ISP (tabla
// snmp.capacity_settings); sin fila rigen los valores por defecto.
type CapacitySettings struct {
	// Country sustituye al país del ISP (auth) para clasificar el tráfico
	// local; nil = el del ISP.
	Country            *string       `json:"country"`
	PlanningThreshold  float64       `json:"planning_threshold"`
	UrgentThreshold    float64       `json:"urgent_threshold"`
	LeadDays           int           `json:"lead_days"`
	SaturationWarning  float64       `json:"saturation_warning"`
	SaturationCritical float64       `json:"saturation_critical"`
	SustainedMinutes   int           `json:"sustained_minutes"`
	LossWarningPct     float64       `json:"loss_warning_pct"`
	LossCriticalPct    float64       `json:"loss_critical_pct"`
	LatencyWarningMs   float64       `json:"latency_warning_ms"`
	LatencyCriticalMs  float64       `json:"latency_critical_ms"`
	LocalASNs          []uint32      `json:"local_asns"`
	ProbeTargets       []ProbeTarget `json:"probe_targets"`
}

// DefaultCapacitySettings son los ajustes sin personalizar.
func DefaultCapacitySettings() CapacitySettings {
	return CapacitySettings{PlanningThreshold: DefaultPlanningPct, UrgentThreshold: DefaultUrgentPct, LeadDays: DefaultLeadDays,
		SaturationWarning: DefaultSaturationWarn, SaturationCritical: DefaultSaturationCrit, SustainedMinutes: DefaultSustainedMinutes,
		LossWarningPct: DefaultLossWarningPct, LossCriticalPct: DefaultLossCriticalPct, LatencyWarningMs: DefaultLatencyWarnMs,
		LatencyCriticalMs: DefaultLatencyCritMs, LocalASNs: []uint32{}, ProbeTargets: []ProbeTarget{}}
}

func ratioOK(v float64) bool { return v >= 0.3 && v <= 1 }

// Validate normaliza y valida los ajustes. deny son las redes internas de la
// instalación que no se pueden sondear.
func (s *CapacitySettings) Validate(deny []netip.Prefix) []FieldError {
	var errs []FieldError
	add := func(field, code, msg string) { errs = append(errs, FieldError{Field: field, Code: code, Msg: msg}) }
	if s.Country != nil {
		c := strings.ToUpper(strings.TrimSpace(*s.Country))
		switch {
		case c == "":
			s.Country = nil
		case len(c) != countryLen || strings.Trim(c, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "":
			add("country", "INVALID_FORMAT", "Código ISO 3166-1 de 2 letras (p. ej. GT).")
		default:
			s.Country = &c
		}
	}
	for _, f := range []struct {
		name string
		v    float64
	}{{"planning_threshold", s.PlanningThreshold}, {"urgent_threshold", s.UrgentThreshold},
		{"saturation_warning", s.SaturationWarning}, {"saturation_critical", s.SaturationCritical}} {
		if !ratioOK(f.v) {
			add(f.name, "OUT_OF_RANGE", "Proporción entre 0,3 y 1 (p. ej. 0,8 = 80 %).")
		}
	}
	if s.PlanningThreshold > s.UrgentThreshold {
		add("planning_threshold", "OUT_OF_RANGE", "El umbral de planificación no puede superar al urgente.")
	}
	if s.SaturationWarning > s.SaturationCritical {
		add("saturation_warning", "OUT_OF_RANGE", "El aviso de saturación no puede superar al crítico.")
	}
	if s.LeadDays < 7 || s.LeadDays > maxLeadDays {
		add("lead_days", "OUT_OF_RANGE", fmt.Sprintf("Antelación entre 7 y %d días.", maxLeadDays))
	}
	if s.SustainedMinutes < minSustainedMinutes || s.SustainedMinutes > maxSustainedMinutes {
		add("sustained_minutes", "OUT_OF_RANGE", fmt.Sprintf("Entre %d y %d minutos.", minSustainedMinutes, maxSustainedMinutes))
	}
	if s.LossWarningPct <= 0 || s.LossWarningPct > 100 || s.LossCriticalPct <= 0 || s.LossCriticalPct > 100 || s.LossWarningPct > s.LossCriticalPct {
		add("loss_warning_pct", "OUT_OF_RANGE", "Pérdida en % (0–100); el aviso no puede superar al crítico.")
	}
	if s.LatencyWarningMs <= 0 || s.LatencyCriticalMs > 10000 || s.LatencyWarningMs > s.LatencyCriticalMs {
		add("latency_warning_ms", "OUT_OF_RANGE", "Latencia en ms (hasta 10 000); el aviso no puede superar al crítico.")
	}
	if len(s.LocalASNs) > MaxLocalASNs {
		add("local_asns", "TOO_MANY", fmt.Sprintf("Hasta %d ASN locales.", MaxLocalASNs))
	}
	if s.LocalASNs == nil {
		s.LocalASNs = []uint32{}
	}
	slices.Sort(s.LocalASNs)
	s.LocalASNs = slices.Compact(s.LocalASNs)
	if slices.Contains(s.LocalASNs, 0) {
		add("local_asns", "INVALID_VALUE", "El ASN 0 no es válido.")
	}
	if len(s.ProbeTargets) > MaxProbeTargets {
		add("probe_targets", "TOO_MANY", fmt.Sprintf("Hasta %d destinos.", MaxProbeTargets))
	}
	if s.ProbeTargets == nil {
		s.ProbeTargets = []ProbeTarget{}
	}
	for i := range s.ProbeTargets {
		t := &s.ProbeTargets[i]
		t.Label = strings.TrimSpace(t.Label)
		field := fmt.Sprintf("probe_targets[%d]", i)
		if n := len([]rune(t.Label)); n < 1 || n > probeTargetLabelMax {
			add(field+".label", "INVALID_LENGTH", fmt.Sprintf("Etiqueta de 1 a %d caracteres.", probeTargetLabelMax))
		}
		t.Address = t.Address.Unmap()
		switch {
		case !t.Address.IsValid() || !t.Address.Is4():
			add(field+".address", "INVALID_FORMAT", "Dirección IPv4 (las sondas de Horus son IPv4).")
		case t.Address.IsLoopback() || t.Address.IsUnspecified() || t.Address.IsMulticast() || t.Address.IsLinkLocalUnicast():
			add(field+".address", "INVALID_VALUE", "Dirección no sondeable.")
		default:
			for _, p := range deny {
				if p.Contains(t.Address) {
					add(field+".address", "INVALID_VALUE", "Dirección interna de la instalación de Horus.")
					break
				}
			}
		}
	}
	return errs
}
