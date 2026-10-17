package domain_test

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

func sugg(kind string, conf int, suspected bool) domain.KindSuggestion {
	return domain.KindSuggestion{Kind: kind, Confidence: conf, ApplyThreshold: 80, CommercialScore: conf, Suspected: suspected,
		Reasons: json.RawMessage(`[{"code":"inbound_services","detail":"x","weight":0.3}]`), ReasonCodes: []string{"inbound_services"},
		Model: "commercial-v1", ScoreRef: "commercial-v1@2026-10-08", At: time.Date(2026, 10, 8, 3, 0, 0, 0, time.UTC)}
}

func TestApplySuggestionChangesKind(t *testing.T) {
	now := time.Date(2026, 10, 8, 3, 1, 0, 0, time.UTC)
	c := &domain.Customer{ID: uuid.New(), Kind: "residential", KindSource: "default"}
	ch, changed := c.ApplySuggestion(sugg("commercial", 87, false), now)
	if ch == nil || ch.Source != "scoring" || *ch.FromKind != "residential" || ch.ToKind != "commercial" || *ch.Confidence != 87 ||
		*ch.ModelRef != "commercial-v1@2026-10-08" || !slices.Equal(ch.ReasonCodes, []string{"inbound_services"}) {
		t.Fatalf("historial = %+v", ch)
	}
	if c.Kind != "commercial" || c.KindSource != "scoring" || *c.KindConfidence != 87 || !c.KindChangedAt.Equal(now) || c.KindLocked {
		t.Fatalf("cliente = %+v", c)
	}
	if *c.SuggestedKind != "commercial" || *c.SuggestedConfidence != 87 || *c.CommercialScore != 87 || c.CommercialUseSuspected {
		t.Fatalf("sugerencia = %+v", c)
	}
	if !slices.Contains(changed, "kind") {
		t.Errorf("changed = %v", changed)
	}
}

func TestApplySuggestionBelowThreshold(t *testing.T) {
	c := &domain.Customer{ID: uuid.New(), Kind: "residential", KindSource: "default"}
	ch, changed := c.ApplySuggestion(sugg("commercial", 79, false), time.Now())
	if ch != nil || c.Kind != "residential" || *c.SuggestedKind != "commercial" {
		t.Fatalf("por debajo del umbral no cambia el tipo: %+v %+v", ch, c)
	}
	if len(changed) != 0 {
		t.Errorf("changed = %v", changed)
	}
}

func TestApplySuggestionLockedUntouched(t *testing.T) {
	c := &domain.Customer{ID: uuid.New(), Kind: "residential", KindSource: "manual", KindLocked: true}
	ch, _ := c.ApplySuggestion(sugg("commercial", 99, false), time.Now())
	if ch != nil || c.Kind != "residential" || c.KindSource != "manual" || !c.KindLocked || c.KindConfidence != nil {
		t.Fatalf("tipo manual intocable: %+v %+v", ch, c)
	}
	if c.SuggestedKind == nil || *c.SuggestedKind != "commercial" || *c.SuggestedConfidence != 99 {
		t.Fatalf("la sugerencia se guarda igualmente: %+v", c)
	}
}

func TestApplySuggestionSuspected(t *testing.T) {
	c := &domain.Customer{ID: uuid.New(), Kind: "residential", KindSource: "default"}
	s := sugg("residential", 40, true)
	s.CommercialScore = 60
	ch, changed := c.ApplySuggestion(s, time.Now())
	if ch != nil || !c.CommercialUseSuspected || *c.CommercialScore != 60 || !slices.Equal(changed, []string{"commercial_use_suspected"}) {
		t.Fatalf("residencial con indicios: %+v %v %v", c, ch, changed)
	}
	// Un comercial no se marca como "sospechoso de uso comercial".
	b := &domain.Customer{ID: uuid.New(), Kind: "commercial", KindSource: "manual", KindLocked: true}
	b.ApplySuggestion(s, time.Now())
	if b.CommercialUseSuspected {
		t.Error("commercial_use_suspected solo en residenciales")
	}
}

func TestApplySuggestionRevertsScoringKind(t *testing.T) {
	conf := 87
	c := &domain.Customer{ID: uuid.New(), Kind: "commercial", KindSource: "scoring", KindConfidence: &conf}
	ch, _ := c.ApplySuggestion(sugg("residential", 92, false), time.Now())
	if ch == nil || c.Kind != "residential" || c.KindSource != "scoring" {
		t.Fatalf("vuelve a residencial con confianza alta: %+v", c)
	}
	// Misma clase: solo refresca la confianza.
	ch, changed := c.ApplySuggestion(sugg("residential", 95, false), time.Now())
	if ch != nil || *c.KindConfidence != 95 || !slices.Equal(changed, []string{"kind_confidence"}) {
		t.Fatalf("refresco de confianza: %v %v", ch, changed)
	}
}

func TestResetClearsSuggestion(t *testing.T) {
	c := &domain.Customer{ID: uuid.New(), Kind: "residential", KindSource: "default"}
	c.ApplySuggestion(sugg("commercial", 87, false), time.Now())
	c.Reset("La IP pasó a otra persona", nil, time.Now())
	if c.SuggestedKind != nil || c.CommercialScore != nil || c.SuggestedReasons != nil || c.Kind != "residential" {
		t.Fatalf("reset = %+v", c)
	}
}
