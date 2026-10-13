package app

import (
	"log/slog"
	"net/netip"
	"testing"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/asn"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/catalog"
)

// Una regla ASN propia del tenant gana al catálogo (local_override) solo para
// ese tenant: organización = la regla, servicio del catálogo o propio y
// categoría de la regla; la fila guarda la versión de las reglas del tenant.
func TestEnrichTenantASNRule(t *testing.T) {
	e, err := NewEnrichment(nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	as, err := asn.NewSnapshot(datasets.SnapshotMeta{Version: "v7"}, []asn.Entry{
		{Prefix: netip.MustParsePrefix("45.57.0.0/17"), Route: asn.Route{ASN: 2906, Country: "US"}},
		{Prefix: netip.MustParsePrefix("186.0.0.0/16"), Route: asn.Route{ASN: 263237, Country: "PE"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.SetASN(as)
	tenantA, tenantB := uuid.New(), uuid.New()
	inv, err := flowinv.New(flowinv.Data{ASNRules: []flowinv.ASNRule{
		{ID: uuid.New(), TenantID: tenantA, ASN: 263237, Name: "Fibra Andina", Category: "cdn_infra", Version: 2},
		// Netflix (rango publicado del catálogo): la regla gana también al rango; servicio del catálogo.
		{ID: uuid.New(), TenantID: tenantA, ASN: 2906, Name: "Caché Netflix local", Category: "video_streaming", Service: "netflix", Version: 1},
		{ID: uuid.New(), TenantID: tenantA, ASN: 64512, Name: "Borrada", Category: "web", Version: 3, Deleted: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	e.SetInventory(flowinv.NewStore(inv))

	row := func(tenant uuid.UUID, ip string) Row {
		r := Row{RemoteIP: netip.MustParseAddr(ip), RemotePort: 443, Protocol: 6, Bytes: 100}
		e.Enrich(tenant, &r)
		return r
	}
	own := row(tenantA, "186.0.1.1")
	if own.RemoteASN != 263237 || own.RemoteOrgID != catalog.TenantASNOrgID(tenantA, 263237) ||
		own.ServiceID != catalog.TenantASNServiceID(tenantA, 263237) || own.CategoryID != catalog.ID("category", "cdn_infra") ||
		own.ClassificationMethod != catalog.MethodLocalOverride || own.ClassificationConfidence != 100 || own.TenantRulesVersion != 6 {
		t.Fatalf("fila con regla propia: %+v", own)
	}
	nf := row(tenantA, "45.57.3.3")
	if nf.ServiceID != catalog.ID("service", "netflix") || nf.CategoryID != catalog.ID("category", "video_streaming") ||
		nf.ClassificationMethod != catalog.MethodLocalOverride || nf.RemoteOrgID != catalog.TenantASNOrgID(tenantA, 2906) {
		t.Fatalf("regla con servicio del catálogo: %+v", nf)
	}
	// Otro tenant: catálogo de plataforma, sin reglas.
	other := row(tenantB, "45.57.3.3")
	if other.ClassificationMethod != catalog.MethodPrefix || other.RemoteOrgID != catalog.ID("org", "netflix") ||
		other.CategoryID != catalog.ID("category", "video_streaming") || other.TenantRulesVersion != 0 {
		t.Fatalf("fila de otro tenant: %+v", other)
	}
	unknown := row(tenantB, "186.0.1.1")
	if unknown.ClassificationMethod == catalog.MethodLocalOverride || unknown.RemoteOrgID != catalog.OrgIDForASN(263237) {
		t.Fatalf("ASN sin regla en otro tenant: %+v", unknown)
	}
	// Sin IP remota pública no hay ASN: la regla no aplica, pero la versión sí se guarda.
	priv := row(tenantA, "10.0.0.1")
	if priv.ClassificationMethod == catalog.MethodLocalOverride || priv.TenantRulesVersion != 6 {
		t.Fatalf("IP privada: %+v", priv)
	}
}
