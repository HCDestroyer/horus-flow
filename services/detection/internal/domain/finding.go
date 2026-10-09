// Package domain es el modelo de hallazgos de seguridad (C8,
// packages/schemas/finding/v0/finding.schema.json): estados y transiciones,
// severidad, confianza y su banda, objetivo principal (clave de
// deduplicación), razones y evidencia, y el estado de seguridad del cliente
// (D5, D18).
package domain

import (
	"errors"
	"fmt"
	"math"
	"net/netip"
	"slices"
	"time"

	"github.com/google/uuid"
)

// Kinds de hallazgo (enum abierto, api.md §2.10). outbound_fanout no está en
// la lista cerrada de C8 pero cumple su patrón: es el kind provisional del
// simulador (tools/flowsim, escenario fanout).
const (
	KindC2             = "botnet_c2_communication"
	KindDDoS           = "ddos_participation"
	KindScanning       = "outbound_scanning"
	KindFanout         = "outbound_fanout"
	KindSpam           = "spam_smtp_outbound"
	KindOpenProxy      = "open_proxy_abuse"
	KindCryptomining   = "cryptomining"
	KindBeaconing      = "beaconing"
	KindReputationHit  = "reputation_hit"
	CategorySecurity   = "security"
	SubjectTypeCustome = "customer"
)

// Estados (api.md §2.10).
const (
	StateOpen          = "open"
	StateAcknowledged  = "acknowledged"
	StateResolved      = "resolved"
	StateFalsePositive = "false_positive"
)

// Veredictos de resolución.
const (
	VerdictResolved      = "resolved"
	VerdictFalsePositive = "false_positive"
	VerdictAutoExpired   = "auto_expired"
)

// Severidades.
const (
	SeverityLow      = "low"
	SeverityMedium   = "medium"
	SeverityHigh     = "high"
	SeverityCritical = "critical"
)

// SeverityRank ordena severidades (mayor = más grave; 0 = desconocida).
func SeverityRank(s string) int {
	return map[string]int{SeverityLow: 1, SeverityMedium: 2, SeverityHigh: 3, SeverityCritical: 4}[s]
}

// Señales (SecuritySummary.by_signal de detection.yaml).
const (
	SignalC2         = "c2_contact"
	SignalBeaconing  = "beaconing"
	SignalFanout     = "fan_out"
	SignalScanning   = "scanning"
	SignalWatchPorts = "watched_ports"
	SignalSustained  = "sustained_upload"
	SignalSMTP       = "smtp"
	SignalDDoS       = "ddos"
)

// Bandas de confianza (confidence_level). La UI muestra severidad y
// confianza por separado.
const (
	ConfidenceLow    = "low"
	ConfidenceMedium = "medium"
	ConfidenceHigh   = "high"
	highFrom         = 0.75
	mediumFrom       = 0.45
)

// ConfidenceLevel devuelve la banda de c.
func ConfidenceLevel(c float64) string {
	switch {
	case c >= highFrom:
		return ConfidenceHigh
	case c >= mediumFrom:
		return ConfidenceMedium
	}
	return ConfidenceLow
}

// LowerOneLevel baja c una banda (muestreo declarado, I1-11 criterio 4).
func LowerOneLevel(c float64) float64 {
	switch ConfidenceLevel(c) {
	case ConfidenceHigh:
		return Round2(math.Min(c-0.3, highFrom-0.01))
	case ConfidenceMedium:
		return Round2(math.Min(c-0.3, mediumFrom-0.01))
	}
	return Round2(c / 2)
}

// Clamp acota c a [0, 1] con dos decimales.
func Clamp(c float64) float64 { return Round2(math.Max(0, math.Min(1, c))) }

// Round2 redondea a dos decimales.
func Round2(f float64) float64 { return math.Round(f*100) / 100 }

// Tipos de objetivo principal.
const (
	TargetRemoteIP     = "remote_ip"
	TargetRemotePort   = "remote_port"
	TargetRemoteASN    = "remote_asn"
	TargetRemotePrefix = "remote_prefix"
	TargetNone         = "none"
)

// Target es el objetivo principal: con (tenant, cliente, kind) forma la clave
// de deduplicación.
type Target struct {
	Type  string
	Value string
}

// Reason es una razón explicable (C8 Reason).
type Reason struct {
	Code   string         `json:"code"`
	Detail string         `json:"detail"`
	Weight *float64       `json:"weight"`
	Data   map[string]any `json:"data"`
}

// W es un atajo para el peso de una razón.
func W(f float64) *float64 { f = Round2(f); return &f }

// Summary es el resumen legible (sin IP del cliente).
type Summary struct {
	Code   string         `json:"code"`
	Text   string         `json:"text"`
	Params map[string]any `json:"params,omitempty"`
}

// Resolution es el cierre de un hallazgo.
type Resolution struct {
	Verdict      string     `json:"verdict"`
	ResolvedAt   time.Time  `json:"resolved_at"`
	ResolvedBy   *uuid.UUID `json:"resolved_by"`
	Comment      *string    `json:"comment"`
	SilenceUntil *time.Time `json:"silence_until"`
	ActionsTaken []string   `json:"actions_taken"`
}

// Finding es un hallazgo persistido.
type Finding struct {
	ID, TenantID                uuid.UUID
	Version                     int
	State, Kind, Category       string
	Severity                    string
	Confidence                  float64
	CustomerID, RealmID, SiteID uuid.UUID
	RouterID                    uuid.UUID
	Address                     netip.Prefix // IP (/32, /128) o prefijo IPv6 del cliente (D22)
	CustomerKind                string
	Target                      Target
	Signals                     []string
	Summary                     Summary
	Reasons                     []Reason
	Evidence                    map[string]any
	WindowFrom, WindowTo        time.Time
	FirstSeenAt, LastSeenAt     time.Time
	OpenedAt, UpdatedAt         time.Time
	Occurrences                 int
	RuleVersion                 string
	ReputationSnapshotVersion   *int
	MinSamplingRate             *int
	SamplingReducedConfidence   bool
	PreviousFindingID           *uuid.UUID
	AcknowledgedBy              *uuid.UUID
	AcknowledgedAt              *time.Time
	Resolution                  *Resolution
	ResolvedAt, SilenceUntil    *time.Time
}

// Active indica si el hallazgo sigue abierto (open o acknowledged).
func (f *Finding) Active() bool { return f.State == StateOpen || f.State == StateAcknowledged }

// ConfidenceLevel es la banda de la confianza.
func (f *Finding) ConfidenceLevel() string { return ConfidenceLevel(f.Confidence) }

// Errores de transición.
var (
	ErrStateInvalid  = errors.New("finding: invalid state transition")
	ErrCommentNeeded = errors.New("finding: comment required")
)

// Acknowledge: open → acknowledged.
func (f *Finding) Acknowledge(actor *uuid.UUID, at time.Time) error {
	if f.State != StateOpen {
		return fmt.Errorf("%w: %s → %s", ErrStateInvalid, f.State, StateAcknowledged)
	}
	f.State, f.AcknowledgedBy, f.AcknowledgedAt = StateAcknowledged, actor, &at
	f.touch(at)
	return nil
}

// Resolve: open | acknowledged → resolved. Si el patrón vuelve, se abre un
// hallazgo nuevo enlazado (reincidente).
func (f *Finding) Resolve(actor *uuid.UUID, comment *string, actions []string, at time.Time) error {
	return f.close(VerdictResolved, StateResolved, actor, comment, actions, nil, at)
}

// MarkFalsePositive: open | acknowledged → false_positive con comentario
// obligatorio y periodo de silencio (no se reabre mientras dure).
func (f *Finding) MarkFalsePositive(actor *uuid.UUID, comment string, silence time.Duration, at time.Time) error {
	if len([]rune(comment)) < 3 {
		return ErrCommentNeeded
	}
	until := at.Add(silence)
	return f.close(VerdictFalsePositive, StateFalsePositive, actor, &comment, nil, &until, at)
}

// AutoExpire cierra un hallazgo sin ocurrencias durante el periodo dado.
func (f *Finding) AutoExpire(at time.Time) error {
	return f.close(VerdictAutoExpired, StateResolved, nil, nil, nil, nil, at)
}

func (f *Finding) close(verdict, state string, actor *uuid.UUID, comment *string, actions []string, silence *time.Time, at time.Time) error {
	if !f.Active() {
		return fmt.Errorf("%w: %s → %s", ErrStateInvalid, f.State, state)
	}
	if actions == nil {
		actions = []string{}
	}
	f.State = state
	f.Resolution = &Resolution{Verdict: verdict, ResolvedAt: at, ResolvedBy: actor, Comment: comment,
		SilenceUntil: silence, ActionsTaken: actions}
	f.ResolvedAt, f.SilenceUntil = &at, silence
	f.touch(at)
	return nil
}

func (f *Finding) touch(at time.Time) {
	f.Version++
	f.UpdatedAt = at
}

// Candidate es lo que produce un detector en una evaluación (una ocurrencia).
type Candidate struct {
	Detector, RuleVersion string
	Kind, Severity        string
	Confidence            float64
	RealmID, SiteID       uuid.UUID
	RouterID              uuid.UUID
	Client                netip.Addr // IP del cliente; IPv6 = dirección base del prefijo delegado
	Target                Target
	Signals               []string
	Summary               Summary
	Reasons               []Reason
	Evidence              map[string]any
	WindowFrom, WindowTo  time.Time
	FirstSeen, LastSeen   time.Time
	MaxSamplingRate       uint32
	MinSamplingRate       uint32
	ReputationVersion     *int
	// RemoteIP / RemoteASN del destino principal (allowlist del ISP).
	RemoteIP  netip.Addr
	RemoteASN uint32
	// Rellenados por el motor.
	CustomerID   uuid.UUID
	CustomerKind string
	SamplingLow  bool
}

// AddSignal añade s si no está.
func (c *Candidate) AddSignal(s string) {
	if !slices.Contains(c.Signals, s) {
		c.Signals = append(c.Signals, s)
	}
}

// ApplySampling baja la confianza un nivel si el exportador declara
// muestreo (> 1) y lo explica en las razones (I1-11 criterio 4).
func (c *Candidate) ApplySampling() {
	if c.MaxSamplingRate <= 1 {
		return
	}
	c.Confidence = LowerOneLevel(c.Confidence)
	c.SamplingLow = true
	c.Reasons = append(c.Reasons, Reason{
		Code:   "sampling_declared",
		Detail: fmt.Sprintf("El exportador declara muestreo 1:%d: los recuentos son estimados y la confianza baja un nivel", c.MaxSamplingRate),
		Weight: W(0),
		Data:   map[string]any{"sampling_rate": c.MaxSamplingRate},
	})
}
