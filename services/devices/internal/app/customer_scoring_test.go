package app_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// memCustomers guarda un cliente con concurrencia optimista (como
// UpdateCustomer en PostgreSQL) y recuerda historial y eventos.
type memCustomers struct {
	app.CustomerStore
	c        *domain.Customer
	changes  []*domain.KindChange
	events   []outbox.Event
	conflict int // nº de UpdateCustomer que fallan por versión
}

func (m *memCustomers) GetCustomer(_ context.Context, _ pgdb.TenantID, id uuid.UUID) (*domain.Customer, error) {
	if m.c == nil || m.c.ID != id {
		return nil, domain.ErrNotFound
	}
	cp := *m.c
	return &cp, nil
}

func (m *memCustomers) UpdateCustomer(_ context.Context, _ pgdb.TenantID, c *domain.Customer, expect int, ch *domain.KindChange, ev app.CustomerEvents) error {
	if m.conflict > 0 {
		m.conflict--
		m.c.Version++
		return app.ErrVersionChanged
	}
	if m.c.Version != expect {
		return app.ErrVersionChanged
	}
	c.Version = expect + 1
	cp := *c
	m.c = &cp
	if ch != nil {
		m.changes = append(m.changes, ch)
	}
	m.events = append(m.events, ev(c)...)
	return nil
}

func kindSuggestedMsg(t *testing.T, mutate func(d map[string]any)) *natsx.Message {
	t.Helper()
	env, err := natsx.DecodeEnvelope(example(t, "horus.detection.customer.kind_suggested.json"))
	if err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		var d map[string]any
		if err := json.Unmarshal(env.Data, &d); err != nil {
			t.Fatal(err)
		}
		mutate(d)
		if env.Data, err = json.Marshal(d); err != nil {
			t.Fatal(err)
		}
	}
	return &natsx.Message{Envelope: env, Tenant: *env.TenantID}
}

func customerOf(id string, kind, source string, locked bool) *domain.Customer {
	return &domain.Customer{ID: uuid.MustParse(id), Kind: kind, KindSource: source, KindLocked: locked, Status: "active", Version: 4,
		SecurityState: "clean", FirstSeen: time.Now(), LastSeen: time.Now()}
}

const exampleCustomer = "0192f0c4-7a1e-7c3a-9b1d-2f6e8a4c1d55"

// El golden file del contrato aplica el tipo comercial (87 ≥ 80) a un
// residencial sin bloquear: kind_changed con source = scoring, razones
// copiadas y la causa en el historial.
func TestHandleKindSuggestedAppliesContractExample(t *testing.T) {
	st := &memCustomers{c: customerOf(exampleCustomer, "residential", "default", false)}
	svc := app.NewCustomers(st, nil, nil, nil, nil)
	msg := kindSuggestedMsg(t, nil)
	if err := svc.HandleDetection(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	c := st.c
	if c.Kind != "commercial" || c.KindSource != "scoring" || *c.KindConfidence != 87 || *c.CommercialScore != 87 || c.Version != 5 {
		t.Fatalf("cliente = %+v", c)
	}
	if len(st.changes) != 1 {
		t.Fatalf("historial = %d", len(st.changes))
	}
	ch := st.changes[0]
	if ch.Source != "scoring" || *ch.ModelRef != "commercial-v1@2026-10-08" || len(ch.ReasonCodes) != 4 || ch.ReasonCodes[0] != "inbound_services" ||
		ch.CausationEventID == nil || *ch.CausationEventID != msg.Envelope.ID {
		t.Fatalf("cambio = %+v", ch)
	}
	if len(st.events) != 1 || st.events[0].Type != "horus.devices.customer.kind_changed" {
		t.Fatalf("eventos = %+v", st.events)
	}
	change := st.events[0].Data.(map[string]any)["change"].(map[string]any)
	if change["source"] != "scoring" || change["causation_event_id"] == nil {
		t.Errorf("change = %v", change)
	}
	// Reentrega: idempotente.
	if err := svc.HandleDetection(context.Background(), msg); err != nil || len(st.events) != 1 || st.c.Version != 5 {
		t.Fatalf("reentrega: %v, %d eventos", err, len(st.events))
	}
}

func TestHandleKindSuggestedLockedCustomer(t *testing.T) {
	st := &memCustomers{c: customerOf(exampleCustomer, "residential", "manual", true)}
	svc := app.NewCustomers(st, nil, nil, nil, nil)
	if err := svc.HandleKindSuggested(context.Background(), kindSuggestedMsg(t, nil)); err != nil {
		t.Fatal(err)
	}
	c := st.c
	if c.Kind != "residential" || c.KindSource != "manual" || !c.KindLocked || len(st.changes) != 0 {
		t.Fatalf("un tipo manual no lo cambia el scoring: %+v", c)
	}
	if c.SuggestedKind == nil || *c.SuggestedKind != "commercial" || *c.SuggestedConfidence != 87 || len(st.events) != 0 {
		t.Fatalf("la sugerencia se guarda sin evento: %+v, %d eventos", c, len(st.events))
	}
}

func TestHandleKindSuggestedThresholdAndSuspected(t *testing.T) {
	// Umbral del ISP en 90: 87 no alcanza, el tipo no cambia.
	st := &memCustomers{c: customerOf(exampleCustomer, "residential", "default", false)}
	svc := app.NewCustomers(st, nil, nil, nil, nil)
	if err := svc.HandleKindSuggested(context.Background(), kindSuggestedMsg(t, func(d map[string]any) { d["apply_threshold"] = 90 })); err != nil {
		t.Fatal(err)
	}
	if st.c.Kind != "residential" || len(st.changes) != 0 {
		t.Fatalf("umbral 90: %+v", st.c)
	}
	// Residencial con indicios: commercial_use_suspected + commercial_score y customer.updated.
	st = &memCustomers{c: customerOf(exampleCustomer, "residential", "default", false)}
	svc = app.NewCustomers(st, nil, nil, nil, nil)
	err := svc.HandleKindSuggested(context.Background(), kindSuggestedMsg(t, func(d map[string]any) {
		d["kind"], d["level"], d["confidence"], d["commercial_score"], d["commercial_use_suspected"] = "residential", "suspected", 0.4, 60, true
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !st.c.CommercialUseSuspected || *st.c.CommercialScore != 60 || st.c.Kind != "residential" || len(st.events) != 1 ||
		st.events[0].Type != "horus.devices.customer.updated" {
		t.Fatalf("sospecha: %+v %+v", st.c, st.events)
	}
}

func TestHandleKindSuggestedRetriesAndRejects(t *testing.T) {
	st := &memCustomers{c: customerOf(exampleCustomer, "residential", "default", false), conflict: 1}
	svc := app.NewCustomers(st, nil, nil, nil, nil)
	if err := svc.HandleKindSuggested(context.Background(), kindSuggestedMsg(t, nil)); err != nil || st.c.Kind != "commercial" {
		t.Fatalf("reintento tras conflicto de versión: %v %+v", err, st.c)
	}
	// Cliente purgado: se ignora sin error.
	gone := &memCustomers{}
	if err := app.NewCustomers(gone, nil, nil, nil, nil).HandleKindSuggested(context.Background(), kindSuggestedMsg(t, nil)); err != nil {
		t.Fatalf("cliente inexistente: %v", err)
	}
	// Payload inválido: permanente (sin reintento).
	err := svc.HandleKindSuggested(context.Background(), kindSuggestedMsg(t, func(d map[string]any) { d["kind"] = "industrial" }))
	if err == nil || !natsx.IsPermanent(err) {
		t.Fatalf("kind inválido: %v", err)
	}
	err = svc.HandleKindSuggested(context.Background(), kindSuggestedMsg(t, func(d map[string]any) { d["confidence"] = 87 }))
	if err == nil || !natsx.IsPermanent(err) {
		t.Fatalf("confianza fuera de 0..1: %v", err)
	}
}

func TestDetectionSubjects(t *testing.T) {
	if len(app.DetectionSubjects) != 2 || app.DetectionSubjects[1] != "horus.detection.customer.kind_suggested.>" {
		t.Fatalf("filtros = %v", app.DetectionSubjects)
	}
}
