package domain

import (
	"slices"
	"time"
)

// Estados de seguridad del cliente (api.md §2.9, D5). `infected` se muestra
// como "Infectado" (D18), siempre con razones y confianza.
const (
	SecurityClean     = "clean"
	SecuritySuspected = "suspected"
	SecurityInfected  = "infected"
	SecurityMitigated = "mitigated"
	// SecurityModel es la versión de la regla que deriva el estado.
	SecurityModel = "security-v1"
	// MitigatedFor es cuánto dura `mitigated` tras resolver el último hallazgo.
	MitigatedFor = 7 * 24 * time.Hour
)

// SecurityStateLabel es la etiqueta de presentación en español (D18).
var SecurityStateLabel = map[string]string{
	SecurityClean: "Limpio", SecuritySuspected: "Sospechoso", SecurityInfected: "Infectado", SecurityMitigated: "Mitigado",
}

// SecurityState es el estado derivado de los hallazgos de un cliente.
type SecurityState struct {
	State        string
	OpenFindings int
	TopKind      *string
	MaxSeverity  *string
	Confidence   *float64
	Reasons      []Reason
}

// DeriveSecurityState calcula el estado de un cliente a partir de sus
// hallazgos activos y del último cierre:
//
//   - infected: un hallazgo activo de severidad ≥ alta con confianza alta, o
//     señales de dos kinds distintos (correlación, ADR-0024 §1: nunca por
//     una sola señal débil);
//   - suspected: cualquier otro hallazgo activo;
//   - mitigated: sin activos y un hallazgo resuelto por el ISP hace < 7 días;
//   - clean: el resto.
func DeriveSecurityState(active []Finding, lastResolvedAt *time.Time, now time.Time) SecurityState {
	if len(active) == 0 {
		if lastResolvedAt != nil && now.Sub(*lastResolvedAt) < MitigatedFor {
			return SecurityState{State: SecurityMitigated, Reasons: []Reason{{Code: "findings_resolved",
				Detail: "El ISP resolvió los hallazgos del cliente; se mantiene en observación 7 días"}}}
		}
		return SecurityState{State: SecurityClean, Reasons: []Reason{}}
	}
	sorted := slices.Clone(active)
	slices.SortFunc(sorted, func(a, b Finding) int {
		if d := SeverityRank(b.Severity) - SeverityRank(a.Severity); d != 0 {
			return d
		}
		switch {
		case a.Confidence > b.Confidence:
			return -1
		case a.Confidence < b.Confidence:
			return 1
		}
		return b.LastSeenAt.Compare(a.LastSeenAt)
	})
	top := sorted[0]
	kinds := map[string]bool{}
	var reasons []Reason
	for _, f := range sorted {
		if !kinds[f.Kind] && len(reasons) < 5 {
			reasons = append(reasons, Reason{Code: f.Kind, Detail: f.Summary.Text, Weight: W(f.Confidence),
				Data: map[string]any{"finding_id": f.ID.String(), "severity": f.Severity, "confidence_level": f.ConfidenceLevel()}})
		}
		kinds[f.Kind] = true
	}
	state := SecuritySuspected
	if (SeverityRank(top.Severity) >= SeverityRank(SeverityHigh) && top.ConfidenceLevel() == ConfidenceHigh) ||
		(len(kinds) >= 2 && top.ConfidenceLevel() != ConfidenceLow) {
		state = SecurityInfected
	}
	kind, sev, conf := top.Kind, top.Severity, top.Confidence
	return SecurityState{State: state, OpenFindings: len(active), TopKind: &kind, MaxSeverity: &sev, Confidence: &conf, Reasons: reasons}
}
