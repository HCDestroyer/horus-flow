// Package domain es la lógica pura del rol snmp: correlación de señales y
// estado observado con histéresis (docs/architecture.md §10.4, docs/api.md
// §2.6), umbrales sostenidos, deltas de contadores y regla de interfaces.
package domain

import (
	"time"

	"github.com/hcdestroyer/horus-flow/services/snmp/api"
)

// Observation es lo que se sabe de un router en un ciclo de sondeo.
type Observation struct {
	Signals api.Signals
	// Hits son los umbrales superados en este ciclo (ya sostenidos).
	Hits []api.ThresholdHit
	// UplinkDown: la interfaz de subida está caída.
	UplinkDown bool
	// NoCredentials: el router no tiene credencial SNMP (solo ICMP).
	NoCredentials bool
	// ViaTunnelDown: el equipo se alcanza vía un router del sitio cuyo túnel
	// está caído o nunca subió (switch vía router, ADR-0032).
	ViaTunnelDown bool
}

// HysteresisObservations es el número de observaciones iguales seguidas
// necesarias para cambiar de estado observado.
const HysteresisObservations = 2

// Candidate correlaciona las señales de una observación en el estado
// observado candidato y su razón (tabla de architecture.md §10.4).
func Candidate(o Observation) (state, reason string) {
	s := o.Signals
	snmpOK := s.SNMP == api.SignalOK
	snmpBad := s.SNMP == api.SignalFail || s.SNMP == api.SignalAuthFailed
	switch {
	case o.ViaTunnelDown && !snmpOK:
		// El camino (túnel del router del sitio) está caído: no se sabe nada
		// del switch y no es culpa suya.
		return api.StatusUnreachable, api.ReasonViaRouterTunnelDown
	case s.ICMP == api.SignalFail && !snmpOK:
		// Nada responde: la señal del túnel decide la razón.
		if s.WireGuard == api.SignalStale || s.WireGuard == api.SignalNever {
			return api.StatusOffline, api.ReasonTunnelDown
		}
		return api.StatusOffline, api.ReasonHostUnreachable
	case s.ICMP == api.SignalUnknown && snmpBad && (s.WireGuard == api.SignalStale || s.WireGuard == api.SignalNever):
		// Sin ICMP disponible: SNMP ✗ + túnel sin handshake = caído.
		return api.StatusOffline, api.ReasonTunnelDown
	case s.SNMP == api.SignalAuthFailed:
		return api.StatusDegraded, api.ReasonSNMPAuthFailed
	case s.SNMP == api.SignalFail:
		return api.StatusDegraded, api.ReasonSNMPUnreachable
	case !snmpOK && o.NoCredentials:
		return api.StatusDegraded, api.ReasonNoCredentials
	case !snmpOK:
		// SNMP aún no observado pero ICMP responde.
		return api.StatusDegraded, api.ReasonSNMPUnreachable
	}
	// SNMP responde: umbrales.
	if r, ok := reasonFor(o.Hits, "critical"); ok {
		return api.StatusCritical, r
	}
	if r, ok := reasonFor(o.Hits, "warning"); ok {
		return api.StatusWarning, r
	}
	if o.UplinkDown {
		return api.StatusWarning, api.ReasonInterfaceDown
	}
	if s.Exporter != nil && *s.Exporter == "silent" {
		return api.StatusWarning, api.ReasonExporterSilent
	}
	return api.StatusOnline, api.ReasonOK
}

func reasonFor(hits []api.ThresholdHit, level string) (string, bool) {
	for _, h := range hits {
		if h.Level != level {
			continue
		}
		switch h.Metric {
		case "cpu_percent", "cpu_max_core_percent":
			return api.ReasonCPUHigh, true
		case "memory_percent":
			return api.ReasonMemoryHigh, true
		case "temperature_celsius", "cpu_temperature_celsius":
			return api.ReasonTemperatureHigh, true
		case "interface_down":
			return api.ReasonInterfaceDown, true
		case "interface_flapping":
			return api.ReasonInterfaceFlapping, true
		}
	}
	return "", false
}

// Machine es el estado observado de un router con histéresis: un estado
// candidato distinto del actual debe repetirse HysteresisObservations veces
// seguidas para adoptarse. El primer estado se adopta de inmediato. Un cambio
// de razón dentro del mismo estado se aplica sin histéresis (y no mueve Since).
type Machine struct {
	State   string     `json:"state"`
	Reason  string     `json:"reason"`
	Since   *time.Time `json:"since"`
	Version int        `json:"version"`

	Pending      string `json:"pending,omitempty"`
	PendingCount int    `json:"pending_count,omitempty"`
}

// Transition es el resultado de una observación.
type Transition struct {
	Changed       bool
	PreviousState string
	State         string
	Reason        string
	At            time.Time
}

// Observe aplica un candidato (state, reason) observado en at.
func (m *Machine) Observe(state, reason string, at time.Time) Transition {
	tr := Transition{PreviousState: m.State, State: state, Reason: reason, At: at}
	switch {
	case m.State == "":
		m.adopt(state, reason, at)
		tr.Changed = true
	case state == m.State:
		m.Pending, m.PendingCount = "", 0
		m.Reason = reason
	case m.Pending == state:
		m.PendingCount++
		if m.PendingCount >= HysteresisObservations {
			m.adopt(state, reason, at)
			tr.Changed = true
		}
	default:
		m.Pending, m.PendingCount = state, 1
	}
	if !tr.Changed {
		tr.State, tr.Reason = m.State, m.Reason
	}
	return tr
}

func (m *Machine) adopt(state, reason string, at time.Time) {
	m.State, m.Reason, m.Pending, m.PendingCount = state, reason, "", 0
	t := at
	m.Since = &t
	m.Version++
}
