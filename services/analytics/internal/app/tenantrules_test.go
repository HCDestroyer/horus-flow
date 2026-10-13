package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/services/analytics/internal/adapters/chread"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/asn"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/catalog"
)

// fakeRows devuelve filas (clave, ↓, ↑) como los agregados por columna.
type fakeRows struct {
	chread.Rows
	rows [][3]any
	i    int
}

func (f *fakeRows) Next() bool { f.i++; return f.i <= len(f.rows) }
func (f *fakeRows) Scan(dest ...any) error {
	r := f.rows[f.i-1]
	*dest[0].(*string) = r[0].(string)
	*dest[1].(*uint64) = r[1].(uint64)
	*dest[2].(*uint64) = r[2].(uint64)
	return nil
}
func (f *fakeRows) Close() error { return nil }
func (f *fakeRows) Err() error   { return nil }

// fakeQ responde por remote_asn o por service_id según la consulta.
type fakeQ struct{ byASN, bySvc [][3]any }

func (q fakeQ) Query(_ context.Context, _ uuid.UUID, sql string, _ ...any) (chread.Rows, context.CancelFunc, error) {
	if strings.Contains(sql, "remote_asn") {
		return &fakeRows{rows: q.byASN}, func() {}, nil
	}
	return &fakeRows{rows: q.bySvc}, func() {}, nil
}

func labels(res *TopResult) map[string]string {
	out := map[string]string{}
	for _, r := range res.Rows {
		out[r.Key] = r.Label
	}
	return out
}

// Las reglas ASN propias del tenant nombran sus ASN en los tops por ASN y
// organización, sus servicios propios en el top por servicio y los reparten
// en la categoría de la regla; otro tenant sigue viendo el catálogo.
func TestTopWithTenantASNRules(t *testing.T) {
	tenantA, tenantB := uuid.New(), uuid.New()
	inv, err := flowinv.New(flowinv.Data{ASNRules: []flowinv.ASNRule{
		{ID: uuid.New(), TenantID: tenantA, ASN: 263237, Name: "Fibra Andina", Category: "cdn_infra", Version: 1},
		{ID: uuid.New(), TenantID: tenantA, ASN: 2906, Name: "Caché Netflix", Category: "video_streaming", Service: "netflix", Version: 1},
	}})
	if err != nil {
		t.Fatal(err)
	}
	names := &ASNNames{}
	snap, err := asn.NewSnapshot(datasets.SnapshotMeta{Version: "v1"}, nil, map[uint32]asn.ASInfo{
		263237: {Name: "FIBRA-ANDINA-AS", Country: "PE"}, 2906: {Name: "AS-SSI", Country: "US"}})
	if err != nil {
		t.Fatal(err)
	}
	names.Set(snap)
	own := catalog.TenantASNServiceID(tenantA, 263237).String()
	netflix := catalog.ID("service", "netflix").String()
	s := &Service{Inv: flowinv.NewStore(inv), Names: names, Q: fakeQ{
		byASN: [][3]any{{"263237", uint64(500), uint64(50)}, {"2906", uint64(400), uint64(10)}, {"64999", uint64(100), uint64(1)}},
		bySvc: [][3]any{{own, uint64(500), uint64(50)}, {netflix, uint64(400), uint64(10)}},
	}}
	rg := Range{From: time.Now().Add(-time.Hour), To: time.Now()}
	top := func(tenant uuid.UUID, dim string) *TopResult {
		t.Helper()
		res, err := s.Top(context.Background(), TopQuery{Scope: Scope{Tenant: tenant}, Dimension: dim, Range: rg})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	if got := labels(top(tenantA, DimASNs)); got["263237"] != "AS263237 · Fibra Andina" || got["2906"] != "AS2906 · Caché Netflix" ||
		got["64999"] != "AS64999" {
		t.Fatalf("asns A = %v", got)
	}
	if got := labels(top(tenantB, DimASNs)); got["263237"] != "AS263237 · FIBRA-ANDINA-AS" {
		t.Fatalf("asns B = %v", got)
	}
	orgA := labels(top(tenantA, DimOrganizations))
	if orgA[catalog.TenantASNOrgID(tenantA, 263237).String()] != "Fibra Andina" ||
		orgA[catalog.TenantASNOrgID(tenantA, 2906).String()] != "Caché Netflix" || orgA["AS64999"] != "AS64999" {
		t.Fatalf("organizations A = %v", orgA)
	}
	if orgB := labels(top(tenantB, DimOrganizations)); orgB[catalog.ID("org", "netflix").String()] != "Netflix" ||
		orgB["AS263237"] != "FIBRA-ANDINA-AS" {
		t.Fatalf("organizations B = %v", orgB)
	}
	if svc := labels(top(tenantA, DimServices)); svc[own] != "Fibra Andina" || svc[netflix] != "Netflix" {
		t.Fatalf("services A = %v", svc)
	}
	cats := top(tenantA, DimCategories)
	got := labels(cats)
	if got[catalog.ID("category", "cdn_infra").String()] != "CDN e infraestructura" ||
		got[catalog.ID("category", "video_streaming").String()] != "Streaming de video" || cats.OthersDown != 0 {
		t.Fatalf("categories A = %v (otros %d)", got, cats.OthersDown)
	}
	// Otro tenant: el servicio propio de A no es suyo y queda sin categoría ("Otros").
	if b := top(tenantB, DimCategories); b.OthersDown != 500 {
		t.Fatalf("categories B: otros = %d, quiero 500", b.OthersDown)
	}

	info := s.LookupASN(tenantA, 2906)
	if info.Name != "AS-SSI" || info.Country != "US" || info.CatalogOrg == nil || info.CatalogOrg.Slug != "netflix" ||
		info.Rule == nil || info.Rule.Name != "Caché Netflix" {
		t.Fatalf("LookupASN A = %+v", info)
	}
	if info := s.LookupASN(tenantB, 263237); info.Rule != nil || info.CatalogOrg != nil || info.Name != "FIBRA-ANDINA-AS" || info.Country != "PE" {
		t.Fatalf("LookupASN B = %+v", info)
	}
}
