package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// fakeAsnStore es un AsnRuleStore en memoria (un solo tenant por prueba).
type fakeAsnStore struct {
	rules  map[uuid.UUID]*domain.AsnRule
	events []outbox.Event
}

func newFakeAsnStore() *fakeAsnStore { return &fakeAsnStore{rules: map[uuid.UUID]*domain.AsnRule{}} }

func (f *fakeAsnStore) ListAsnRules(_ context.Context, t pgdb.TenantID) ([]domain.AsnRule, error) {
	var out []domain.AsnRule
	for _, r := range f.rules {
		if r.TenantID == t.UUID() && r.DeletedAt == nil {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (f *fakeAsnStore) GetAsnRule(_ context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.AsnRule, error) {
	r, ok := f.rules[id]
	if !ok || r.TenantID != t.UUID() || r.DeletedAt != nil {
		return nil, domain.ErrNotFound
	}
	c := *r
	return &c, nil
}

func (f *fakeAsnStore) CreateAsnRule(_ context.Context, _ pgdb.TenantID, r *domain.AsnRule, ev Events[domain.AsnRule]) error {
	c := *r
	f.rules[r.ID] = &c
	f.events = append(f.events, ev(r)...)
	return nil
}

func (f *fakeAsnStore) UpdateAsnRule(_ context.Context, _ pgdb.TenantID, r *domain.AsnRule, expect int, ev Events[domain.AsnRule]) error {
	cur := f.rules[r.ID]
	if cur == nil || cur.Version != expect {
		return ErrVersionChanged
	}
	r.Version = expect + 1
	c := *r
	f.rules[r.ID] = &c
	f.events = append(f.events, ev(r)...)
	return nil
}

func (f *fakeAsnStore) DeleteAsnRule(_ context.Context, _ pgdb.TenantID, id uuid.UUID, expect int, ev Events[domain.AsnRule]) error {
	cur := f.rules[id]
	if cur == nil || cur.Version != expect {
		return ErrVersionChanged
	}
	cur.Version++
	now := time.Now()
	cur.DeletedAt = &now
	f.events = append(f.events, ev(cur)...)
	return nil
}

func asnCtx(tenant uuid.UUID, perms ...string) context.Context {
	p := &authz.Principal{Type: "user", Scope: authz.ScopeTenant, TenantID: tenant, UserID: uuid.New(), Perms: map[string][]string{}}
	for _, perm := range perms {
		p.Perms[perm] = []string{authz.ScopeAll}
	}
	return authz.WithPrincipal(context.Background(), p)
}

func body(t *testing.T, v map[string]any) Fields {
	t.Helper()
	b, _ := json.Marshal(v)
	var f Fields
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

// fieldCodes devuelve campo → código de un error de validación.
func fieldCodes(t *testing.T, err error) map[string]string {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Status() != http.StatusUnprocessableEntity {
		t.Fatalf("esperaba 422, obtuve %v", err)
	}
	out := map[string]string{}
	for _, f := range e.Fields {
		out[f.Field] = f.Code
	}
	return out
}

func TestAsnRulesCreateValidation(t *testing.T) {
	tenant := uuid.New()
	svc := NewAsnRules(newFakeAsnStore(), nil, nil)
	ctx := asnCtx(tenant, PermSettingsRead, PermSettingsManage)
	cases := []struct {
		name string
		in   map[string]any
		want map[string]string
	}{
		{"sin campos", map[string]any{}, map[string]string{"asn": "REQUIRED", "name": "REQUIRED", "category": "REQUIRED"}},
		{"asn fuera de rango", map[string]any{"asn": 4294967296, "name": "X", "category": "web"}, map[string]string{"asn": "OUT_OF_RANGE"}},
		{"asn cero", map[string]any{"asn": 0, "name": "X", "category": "web"}, map[string]string{"asn": "OUT_OF_RANGE"}},
		{"asn decimal", map[string]any{"asn": 1.5, "name": "X", "category": "web"}, map[string]string{"asn": "OUT_OF_RANGE"}},
		{"nombre vacío", map[string]any{"asn": 64512, "name": "  ", "category": "web"}, map[string]string{"name": "REQUIRED"}},
		{"categoría desconocida", map[string]any{"asn": 64512, "name": "X", "category": "nope"}, map[string]string{"category": "INVALID_VALUE"}},
		{"servicio desconocido", map[string]any{"asn": 64512, "name": "X", "category": "web", "service": "nope"}, map[string]string{"service": "INVALID_VALUE"}},
		{"categoría distinta de la del servicio", map[string]any{"asn": 64512, "name": "X", "category": "web", "service": "netflix"}, map[string]string{"category": "INVALID_VALUE"}},
		{"campo extra", map[string]any{"asn": 64512, "name": "X", "category": "web", "prefix": "1.2.3.0/24"}, map[string]string{"prefix": "UNKNOWN_FIELD"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := svc.Create(ctx, body(t, c.in))
			got := fieldCodes(t, err)
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("campo %s: código %q, esperaba %q (todos: %v)", k, got[k], v, got)
				}
			}
		})
	}
}

func TestAsnRulesLifecycle(t *testing.T) {
	tenant := uuid.New()
	store := newFakeAsnStore()
	svc := NewAsnRules(store, nil, nil)
	ctx := asnCtx(tenant, PermSettingsRead, PermSettingsManage)

	r, err := svc.Create(ctx, body(t, map[string]any{"asn": 263237, "name": " Fibra Andina ", "category": "cdn_infra", "note": "tránsito"}))
	if err != nil {
		t.Fatal(err)
	}
	if r.ASN != 263237 || r.Name != "Fibra Andina" || r.Service != nil || r.Version != 1 || r.CreatedBy == nil {
		t.Fatalf("regla creada inesperada: %+v", r)
	}
	// ASN repetido → 422 en `asn`.
	_, err = svc.Create(ctx, body(t, map[string]any{"asn": 263237, "name": "Otra", "category": "web"}))
	if got := fieldCodes(t, err); got["asn"] != "ALREADY_EXISTS" {
		t.Fatalf("ASN repetido: %v", got)
	}
	// Edición con If-Match: servicio del catálogo.
	r2, err := svc.Update(ctx, r.ID, 1, body(t, map[string]any{"service": "netflix", "category": "video_streaming"}))
	if err != nil {
		t.Fatal(err)
	}
	if r2.Version != 2 || r2.Service == nil || *r2.Service != "netflix" || r2.Category != "video_streaming" {
		t.Fatalf("regla editada inesperada: %+v", r2)
	}
	// Con un servicio del catálogo y sin categoría, la categoría es la del servicio.
	withSvc, err := svc.Create(ctx, body(t, map[string]any{"asn": 2906, "name": "Caché Netflix", "service": "netflix"}))
	if err != nil || withSvc.Category != "video_streaming" {
		t.Fatalf("regla con servicio: %+v %v", withSvc, err)
	}
	if err := svc.Delete(ctx, withSvc.ID, 1); err != nil {
		t.Fatal(err)
	}
	store.events = store.events[:len(store.events)-2]
	// El ASN no se puede cambiar.
	_, err = svc.Update(ctx, r.ID, 2, body(t, map[string]any{"asn": 1}))
	if got := fieldCodes(t, err); got["asn"] != "UNKNOWN_FIELD" {
		t.Fatalf("PATCH asn: %v", got)
	}
	// If-Match viejo → 412 con la versión vigente.
	_, err = svc.Update(ctx, r.ID, 1, body(t, map[string]any{"name": "X"}))
	if e, ok := apperr.As(err); !ok || e.Status() != http.StatusPreconditionFailed {
		t.Fatalf("If-Match viejo: %v", err)
	}
	// service null vuelve a "el nombre hace de servicio".
	r3, err := svc.Update(ctx, r.ID, 2, body(t, map[string]any{"service": nil}))
	if err != nil || r3.Service != nil {
		t.Fatalf("service null: %+v %v", r3, err)
	}
	if err := svc.Delete(ctx, r.ID, 3); err != nil {
		t.Fatal(err)
	}
	list, err := svc.List(ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("lista tras borrar: %v %v", list, err)
	}
	// Tras la baja el ASN queda libre.
	if _, err := svc.Create(ctx, body(t, map[string]any{"asn": 263237, "name": "De nuevo", "category": "web"})); err != nil {
		t.Fatal(err)
	}
	wantTypes := []string{"horus.devices.asn_rule.created", "horus.devices.asn_rule.updated", "horus.devices.asn_rule.updated",
		"horus.devices.asn_rule.deleted", "horus.devices.asn_rule.created"}
	if len(store.events) != len(wantTypes) {
		t.Fatalf("eventos: %d, esperaba %d", len(store.events), len(wantTypes))
	}
	for i, ev := range store.events {
		if ev.Type != wantTypes[i] || ev.AggregateType != "asn_rule" || ev.TenantID == nil || *ev.TenantID != tenant {
			t.Errorf("evento %d: %+v", i, ev)
		}
	}
	del := store.events[3].Data.(map[string]any)
	if del["deleted_at"] == nil || del["asn"] != int64(263237) {
		t.Errorf("payload de baja: %v", del)
	}
}

func TestAsnRulesPermissions(t *testing.T) {
	tenant := uuid.New()
	svc := NewAsnRules(newFakeAsnStore(), nil, nil)
	readOnly := asnCtx(tenant, PermSettingsRead)
	if _, err := svc.List(readOnly); err != nil {
		t.Fatalf("lectura con settings.read: %v", err)
	}
	if _, _, err := svc.Catalog(readOnly); err != nil {
		t.Fatalf("catálogo con settings.read: %v", err)
	}
	_, err := svc.Create(readOnly, body(t, map[string]any{"asn": 64512, "name": "X", "category": "web"}))
	if e, ok := apperr.As(err); !ok || e.Status() != http.StatusForbidden {
		t.Fatalf("alta sin settings.manage: %v", err)
	}
	if _, err := svc.List(asnCtx(tenant)); err == nil {
		t.Fatal("lectura sin permiso debía fallar")
	}
	// Una regla de otro tenant no existe para este.
	other := asnCtx(uuid.New(), PermSettingsRead, PermSettingsManage)
	r, err := svc.Create(other, body(t, map[string]any{"asn": 64512, "name": "X", "category": "web"}))
	if err != nil {
		t.Fatal(err)
	}
	err = svc.Delete(asnCtx(tenant, PermSettingsManage), r.ID, 1)
	if e, ok := apperr.As(err); !ok || e.Status() != http.StatusNotFound {
		t.Fatalf("regla de otro tenant: %v", err)
	}
}
