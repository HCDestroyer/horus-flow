package domain

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestConfidenceBands(t *testing.T) {
	for _, c := range []struct {
		in    float64
		level string
		lower string
	}{{0.9, ConfidenceHigh, ConfidenceMedium}, {0.75, ConfidenceHigh, ConfidenceMedium}, {0.6, ConfidenceMedium, ConfidenceLow},
		{0.45, ConfidenceMedium, ConfidenceLow}, {0.3, ConfidenceLow, ConfidenceLow}} {
		if got := ConfidenceLevel(c.in); got != c.level {
			t.Errorf("level(%v) = %s, want %s", c.in, got, c.level)
		}
		if got := ConfidenceLevel(LowerOneLevel(c.in)); got != c.lower {
			t.Errorf("lower(%v) = %s, want %s", c.in, got, c.lower)
		}
	}
}

func TestSamplingLowersConfidence(t *testing.T) {
	c := Candidate{Confidence: 0.9, MaxSamplingRate: 1, Reasons: []Reason{{Code: "x"}}}
	c.ApplySampling()
	if c.SamplingLow || len(c.Reasons) != 1 {
		t.Fatal("sin muestreo no debe cambiar")
	}
	c.MaxSamplingRate = 100
	c.ApplySampling()
	if !c.SamplingLow || ConfidenceLevel(c.Confidence) != ConfidenceMedium || c.Reasons[1].Code != "sampling_declared" {
		t.Fatalf("muestreo: %+v", c)
	}
}

func TestTransitions(t *testing.T) {
	now := time.Now()
	actor := uuid.New()
	f := Finding{State: StateOpen, Version: 1}
	if err := f.Acknowledge(&actor, now); err != nil || f.State != StateAcknowledged || f.Version != 2 {
		t.Fatalf("ack: %v %+v", err, f)
	}
	if err := f.Acknowledge(&actor, now); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("doble ack: %v", err)
	}
	if err := f.MarkFalsePositive(&actor, "", time.Hour, now); !errors.Is(err, ErrCommentNeeded) {
		t.Fatalf("FP sin comentario: %v", err)
	}
	if err := f.MarkFalsePositive(&actor, "servidor del cliente", 30*24*time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if f.State != StateFalsePositive || f.SilenceUntil == nil || f.Resolution.Verdict != VerdictFalsePositive || f.Version != 3 {
		t.Fatalf("FP: %+v", f)
	}
	if err := f.Resolve(&actor, nil, nil, now); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("resolver un FP: %v", err)
	}
	g := Finding{State: StateOpen, Version: 1}
	if err := g.Resolve(&actor, nil, []string{"contact_customer"}, now); err != nil || g.State != StateResolved || g.Resolution.ActionsTaken[0] != "contact_customer" {
		t.Fatalf("resolve: %v %+v", err, g)
	}
}

func TestSecurityState(t *testing.T) {
	now := time.Now()
	high := Finding{ID: uuid.New(), Kind: KindC2, Severity: SeverityHigh, Confidence: 0.9, Summary: Summary{Text: "C2"}}
	med := Finding{ID: uuid.New(), Kind: KindBeaconing, Severity: SeverityMedium, Confidence: 0.6, Summary: Summary{Text: "beacon"}}
	low := Finding{ID: uuid.New(), Kind: KindFanout, Severity: SeverityMedium, Confidence: 0.4}
	if s := DeriveSecurityState([]Finding{high}, nil, now); s.State != SecurityInfected || *s.TopKind != KindC2 || len(s.Reasons) != 1 {
		t.Fatalf("alta+alta: %+v", s)
	}
	if s := DeriveSecurityState([]Finding{med}, nil, now); s.State != SecuritySuspected {
		t.Fatalf("una media: %+v", s)
	}
	if s := DeriveSecurityState([]Finding{med, low}, nil, now); s.State != SecurityInfected || len(s.Reasons) != 2 {
		t.Fatalf("correlación: %+v", s)
	}
	resolved := now.Add(-time.Hour)
	if s := DeriveSecurityState(nil, &resolved, now); s.State != SecurityMitigated {
		t.Fatalf("mitigado: %+v", s)
	}
	old := now.Add(-8 * 24 * time.Hour)
	if s := DeriveSecurityState(nil, &old, now); s.State != SecurityClean {
		t.Fatalf("limpio: %+v", s)
	}
	if SecurityStateLabel[SecurityInfected] != "Infectado" {
		t.Fatal("D18")
	}
}

func TestParamsMerge(t *testing.T) {
	p := DefaultParams()
	if err := p.Merge(DetSMTP, json.RawMessage(`{"residential_servers": 40, "window": "30m"}`)); err != nil {
		t.Fatal(err)
	}
	if p.SMTP.ResidentialServers != 40 || p.SMTP.Window.D() != 30*time.Minute || p.SMTP.CommercialServers != 200 {
		t.Fatalf("merge: %+v", p.SMTP)
	}
	if err := p.Merge(DetSMTP, json.RawMessage(`{"nope": 1}`)); err == nil {
		t.Fatal("campo desconocido aceptado")
	}
	if err := p.Merge("x", nil); err == nil {
		t.Fatal("detector desconocido aceptado")
	}
	if err := p.Merge(DetScan, json.RawMessage(`{"window": "-1m"}`)); err == nil {
		t.Fatal("duración negativa aceptada")
	}
}
