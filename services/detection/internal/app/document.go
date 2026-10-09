package app

import (
	"time"

	"github.com/hcdestroyer/horus-flow/services/detection/internal/actions"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
)

// CustomerView es el bloque `customer` de la API (solo con customers.read).
type CustomerView struct {
	Address string  `json:"address"`
	Masked  bool    `json:"address_masked"`
	Alias   *string `json:"alias"`
	Kind    string  `json:"kind"`
}

// View decide qué datos personales lleva el documento.
type View struct {
	// Customer: nil en eventos y sin customers.read.
	Customer *CustomerView
	// Render rellena routeros.rendered_* (API y con customers.read).
	Render bool
}

func ts(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func tsp(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := ts(*t)
	return &s
}

// Document es el hallazgo en la forma de C8 (API y payload de eventos).
func Document(f *domain.Finding, v View) map[string]any {
	reasons := f.Reasons
	if reasons == nil {
		reasons = []domain.Reason{}
	}
	ev := f.Evidence
	if ev == nil {
		ev = map[string]any{}
	}
	var target any
	if f.Target.Type != domain.TargetNone {
		target = f.Target.Value
	}
	var res any
	if r := f.Resolution; r != nil {
		actionsTaken := r.ActionsTaken
		if actionsTaken == nil {
			actionsTaken = []string{}
		}
		res = map[string]any{"verdict": r.Verdict, "resolved_at": ts(r.ResolvedAt), "resolved_by": r.ResolvedBy, "comment": r.Comment,
			"silence_until": tsp(r.SilenceUntil), "actions_taken": actionsTaken}
	}
	acts := actions.Render(actions.Build(f), actions.Values(f), v.Render)
	var customer any
	if v.Customer != nil {
		customer = v.Customer
	}
	return map[string]any{
		"id": f.ID, "tenant_id": f.TenantID, "version": f.Version, "state": f.State, "kind": f.Kind, "category": f.Category,
		"severity": f.Severity, "confidence": f.Confidence, "confidence_level": f.ConfidenceLevel(), "subject_type": "customer",
		"customer_id": f.CustomerID, "realm_id": f.RealmID, "site_id": f.SiteID, "router_id": f.RouterID, "customer": customer,
		"summary": f.Summary, "window_from": ts(f.WindowFrom), "window_to": ts(f.WindowTo), "first_seen_at": ts(f.FirstSeenAt),
		"last_seen_at": ts(f.LastSeenAt), "opened_at": ts(f.OpenedAt), "updated_at": ts(f.UpdatedAt), "occurrences": f.Occurrences,
		"reasons": reasons, "evidence": ev, "primary_target": map[string]any{"type": f.Target.Type, "value": target},
		"rule_version": f.RuleVersion, "reputation_snapshot_version": f.ReputationSnapshotVersion, "min_sampling_rate": f.MinSamplingRate,
		"sampling_reduced_confidence": f.SamplingReducedConfidence, "previous_finding_id": f.PreviousFindingID,
		"acknowledged_by": f.AcknowledgedBy, "acknowledged_at": tsp(f.AcknowledgedAt), "resolution": res, "recommended_actions": acts,
	}
}
