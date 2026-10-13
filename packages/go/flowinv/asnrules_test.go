package flowinv

import (
	"testing"

	"github.com/google/uuid"
)

func ruleData(id uuid.UUID, version int, asn int64, name, category string, service any, deleted bool) map[string]any {
	d := map[string]any{"id": id, "version": version, "asn": asn, "name": name, "category": category, "service": service,
		"created_at": "2026-10-13T14:00:00.000Z", "deleted_at": nil, "changed_fields": []string{}}
	if deleted {
		d["deleted_at"] = "2026-10-13T15:00:00.000Z"
	}
	return d
}

// Las reglas ASN propias se proyectan de horus.devices.asn_rule.*: alta,
// edición (last-writer-wins por aggregate_version, reentregas ignoradas),
// baja (deja de clasificar) y versión del conjunto monótona por tenant.
func TestProjectorASNRules(t *testing.T) {
	tenantA, tenantB := uuid.New(), uuid.New()
	r1, r2, rb := uuid.New(), uuid.New(), uuid.New()
	store := NewStore(nil)
	p := NewProjector(store, Data{}, nil)
	apply := func(b []byte) {
		t.Helper()
		if _, err := p.Apply(b); err != nil {
			t.Fatal(err)
		}
	}
	apply(event(t, "horus.devices.asn_rule.created", tenantA, 1, ruleData(r1, 1, 263237, "Fibra Andina", "cdn_infra", nil, false)))
	apply(event(t, "horus.devices.asn_rule.created", tenantA, 1, ruleData(r2, 1, 64512, "Caché local", "video_streaming", "netflix", false)))
	apply(event(t, "horus.devices.asn_rule.created", tenantB, 1, ruleData(rb, 1, 263237, "Otro ISP", "web", nil, false)))
	apply(event(t, "horus.devices.asn_rule.updated", tenantA, 2, ruleData(r1, 2, 263237, "Fibra Andina SA", "cdn_infra", nil, false)))
	// Reentrega de la v1: no pisa la edición.
	apply(event(t, "horus.devices.asn_rule.created", tenantA, 1, ruleData(r1, 1, 263237, "Fibra Andina", "cdn_infra", nil, false)))
	p.publish()

	s := store.Load()
	r, ok := s.ASNRule(tenantA, 263237)
	if !ok || r.Name != "Fibra Andina SA" || r.Category != "cdn_infra" || r.Service != "" {
		t.Fatalf("regla A/263237 = %+v %v", r, ok)
	}
	if r, ok := s.ASNRule(tenantA, 64512); !ok || r.Service != "netflix" {
		t.Fatalf("regla A/64512 = %+v", r)
	}
	if r, ok := s.ASNRule(tenantB, 263237); !ok || r.Name != "Otro ISP" {
		t.Fatalf("regla B = %+v", r)
	}
	if _, ok := s.ASNRule(tenantB, 64512); ok {
		t.Fatal("la regla de A se ve en B")
	}
	if got := s.TenantRulesVersion(tenantA); got != 3 {
		t.Fatalf("versión de reglas A = %d, quiero 3", got)
	}
	if rules := s.ASNRules(tenantA); len(rules) != 2 || rules[0].ASN != 64512 || rules[1].ASN != 263237 {
		t.Fatalf("reglas A = %+v", rules)
	}

	// Baja: deja de clasificar pero la versión sigue creciendo.
	apply(event(t, "horus.devices.asn_rule.deleted", tenantA, 3, ruleData(r1, 3, 263237, "Fibra Andina SA", "cdn_infra", nil, true)))
	p.publish()
	s = store.Load()
	if _, ok := s.ASNRule(tenantA, 263237); ok {
		t.Fatal("regla borrada sigue activa")
	}
	if got := s.TenantRulesVersion(tenantA); got != 4 {
		t.Fatalf("versión tras la baja = %d, quiero 4", got)
	}
	if got := s.TenantRulesVersion(uuid.New()); got != 0 {
		t.Fatalf("tenant sin reglas = %d", got)
	}

	// Instantánea y restauración: la proyección sobrevive a un reinicio.
	snap, err := p.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	store2 := NewStore(nil)
	p2 := NewProjector(store2, Data{}, nil)
	if err := p2.Restore(snap); err != nil {
		t.Fatal(err)
	}
	p2.publish()
	if r, ok := store2.Load().ASNRule(tenantA, 64512); !ok || r.Name != "Caché local" || store2.Load().TenantRulesVersion(tenantA) != 4 {
		t.Fatalf("tras restaurar: %+v %v", r, ok)
	}

	// Un ASN fuera de rango se rechaza sin tocar la proyección.
	if _, err := p.Apply(event(t, "horus.devices.asn_rule.created", tenantA, 1, ruleData(uuid.New(), 1, 0, "X", "web", nil, false))); err == nil {
		t.Fatal("ASN 0 aceptado")
	}
}

// Una regla incompleta del fichero de inventario no invalida el resto.
func TestSnapshotASNRulesFromFile(t *testing.T) {
	tenant := uuid.New()
	s, err := Parse([]byte(`
asn_rules:
  - {id: 0192e555-0000-7000-8000-000000000055, tenant_id: ` + tenant.String() + `, asn: 263237, name: Fibra Andina, category: cdn_infra, version: 2}
  - {id: 0192e555-0000-7000-8000-000000000056, tenant_id: ` + tenant.String() + `, asn: 0, name: Mal, category: web, version: 1}
`))
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := s.ASNRule(tenant, 263237); !ok || r.Name != "Fibra Andina" {
		t.Fatalf("regla = %+v", r)
	}
	if s.TenantRulesVersion(tenant) != 2 {
		t.Fatalf("versión = %d", s.TenantRulesVersion(tenant))
	}
}
