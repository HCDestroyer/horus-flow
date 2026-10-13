package traffic

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/archtest"
	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
)

// El rol traffic publica el snapshot IP→ASN en el almacén (sin Internet: fixtures) y no lo
// renueva hasta que caduca.
func TestASNSyncPublishesSnapshot(t *testing.T) {
	root, err := archtest.FindRepoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	fx := filepath.Join(root, "tests", "fixtures", "datasets")
	now := time.Date(2026, 10, 8, 4, 0, 0, 0, time.UTC)
	m := &mod{
		cfg: Config{DataDir: t.TempDir(), ASNSync: true, ASNSyncInterval: 168 * time.Hour,
			ASNDatasetsConfig: filepath.Join(fx, "datasets.yaml"), ASNSources: []string{"all"}},
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:     func() time.Time { return now },
		fetcher: datasets.DirFetcher{Dir: fx},
	}
	if !m.due() {
		t.Fatal("sin snapshot debe tocar sincronizar")
	}
	if err := m.syncASN(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.due() {
		t.Fatal("recién publicado no debe volver a sincronizar")
	}
	_, path, err := m.snapshots().Latest()
	if err != nil {
		t.Fatal(err)
	}
	snap, err := readSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := snap.Lookup(netip.MustParseAddr("192.0.2.1")); !ok || r.ASN != 64500 || r.AS.Name == "" { // BGP > iptoasn > RIR; nombre de PeeringDB
		t.Fatalf("lookup 192.0.2.1 = %+v %v", r, ok)
	}
	now = now.Add(169 * time.Hour)
	if !m.due() {
		t.Fatal("caducado: debe volver a sincronizar")
	}
	if _, err := os.Stat(filepath.Join(m.cfg.DataDir, "catalog", "asn", "latest")); err != nil {
		t.Fatal(err)
	}
}

func TestASNSourcesDefaultSkipsRIS(t *testing.T) {
	m := &mod{cfg: Config{ASNSources: []string{"iptoasn-v4", "peeringdb"}}}
	got := m.selected([]datasets.Source{{ID: "ris-rrc00"}, {ID: "iptoasn-v4"}, {ID: "peeringdb"}})
	if len(got) != 2 || got[0].ID != "iptoasn-v4" {
		t.Fatalf("selected = %+v", got)
	}
}
