package app

import (
	"log/slog"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
	"github.com/hcdestroyer/horus-flow/services/detection/api/reputation"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/asn"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/catalog"
)

func TestEnrichmentASNServiceReputation(t *testing.T) {
	e, err := NewEnrichment(nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	as, err := asn.NewSnapshot(datasets.SnapshotMeta{Version: "v7"}, []asn.Entry{
		{Prefix: netip.MustParsePrefix("45.57.0.0/17"), Route: asn.Route{ASN: 2906, Country: "US"}},
		{Prefix: netip.MustParsePrefix("185.10.0.0/16"), Route: asn.Route{ASN: 64999, Country: "DE"}},
	}, map[uint32]asn.ASInfo{64999: {Name: "Example GmbH"}})
	if err != nil {
		t.Fatal(err)
	}
	e.SetASN(as)
	rep, err := reputation.NewSnapshot(datasets.SnapshotMeta{Version: "v3"}, []reputation.Entry{
		{Prefix: netip.MustParsePrefix("185.10.1.1/32"), Indicator: reputation.Indicator{Source: "abusech-feodo",
			Category: reputation.CategoryBotnetCC, Confidence: 90}},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.SetReputation(rep)
	p := &Processor{Inv: store(t), Enricher: e}
	now := time.Now()
	fb := batchFor([]flowpb.FlowRecord{
		{SrcIP: a("10.20.0.5"), DstIP: a("45.57.3.3"), DstPort: 443, Protocol: 6, Bytes: 1000, Packets: 1, TS: now},
		{SrcIP: a("10.20.0.5"), DstIP: a("185.10.1.1"), DstPort: 443, Protocol: 6, Bytes: 10, Packets: 1, TS: now},
		{SrcIP: a("10.20.0.5"), DstIP: a("142.250.1.1"), DstPort: 443, Protocol: 6, Bytes: 10, Packets: 1, TS: now}, // respaldo del catálogo
		{SrcIP: a("10.20.0.5"), DstIP: a("10.20.1.1"), DstPort: 445, Protocol: 6, Bytes: 10, Packets: 1, TS: now},   // interno
	})
	rows, err := p.Rows(fb, fb.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	nf := rows[0]
	if nf.RemoteASN != 2906 || nf.RemoteCountry != "US" || nf.RemoteOrgID != catalog.ID("org", "netflix") ||
		nf.ServiceID != catalog.ID("service", "netflix") || nf.CategoryID != catalog.ID("category", "video_streaming") ||
		nf.ClassificationMethod != "prefix" || nf.CatalogVersion != 1 || nf.RemotePrefixLen != 17 {
		t.Fatalf("netflix row %+v", nf)
	}
	c2 := rows[1]
	if c2.ReputationCategory != "botnet_cc" || c2.ReputationSourceID != SourceID("abusech-feodo") ||
		c2.ReputationConfidence != 90 || c2.ReputationVersion != 3 || c2.RemoteOrgID != catalog.OrgIDForASN(64999) {
		t.Fatalf("c2 row %+v", c2)
	}
	if rows[2].RemoteASN != 15169 || rows[2].ServiceID != catalog.ID("service", "google_generic") {
		t.Fatalf("catalog fallback %+v", rows[2])
	}
	if rows[3].RemoteASN != 0 || rows[3].ReputationCategory != "" {
		t.Fatalf("internal row enriched as Internet %+v", rows[3])
	}
	if SourceID("x") == 0 {
		t.Fatal("source id 0")
	}
}

// TestCatalogHotReload: un snapshot nuevo se carga sin reiniciar y cada fila
// guarda su versión; uno corrupto se rechaza y sigue el anterior (I1-07 criterios 2 y 3).
func TestCatalogHotReload(t *testing.T) {
	e, err := NewEnrichment(nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	c, _ := catalog.Seed()
	for range 2 {
		if _, _, err := c.Publish(datasets.SnapshotDir{Root: dir}, datasets.SnapshotMeta{}); err != nil {
			t.Fatal(err)
		}
	}
	e.Reload("", dir, "")
	if e.CatalogVersion() != 2 {
		t.Fatalf("version = %d, want 2", e.CatalogVersion())
	}
	// v3 corrupto: el contenido no coincide con el sha256 del manifiesto.
	m, path, err := c.Publish(datasets.SnapshotDir{Root: dir}, datasets.SnapshotMeta{})
	if err != nil || m.Version != "v3" {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	b[len(b)-3] ^= 0xff
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	e.Reload("", dir, "")
	if e.CatalogVersion() != 2 {
		t.Fatalf("corrupt snapshot loaded: version = %d", e.CatalogVersion())
	}
	var row Row
	row.RemoteIP, row.RemotePort, row.Protocol = a("45.57.1.1"), 443, 6
	e.Enrich(uuid.New(), &row)
	if row.CatalogVersion != 2 {
		t.Fatalf("row catalog_version = %d", row.CatalogVersion)
	}
}
