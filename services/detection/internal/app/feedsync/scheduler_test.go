package feedsync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/services/detection/api/reputation"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/feeds"
)

// El rol sincroniza solo las fuentes vencidas, publica el snapshot donde lo
// leen el motor y el ingester y expone el estado por fuente (D20: catálogo +
// personalizadas).
func TestSchedulerSyncAndStatus(t *testing.T) {
	fx := filepath.Join("..", "..", "..", "..", "..", "tests", "fixtures", "feeds")
	dir := t.TempDir()
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	s := &Scheduler{ConfigPath: filepath.Join(fx, "feeds.yaml"), CustomPath: filepath.Join(fx, "custom"), DataDir: dir,
		Service: &Service{Store: &datasets.Store{Root: datasets.DatasetsDir(dir)}, Fetcher: datasets.DirFetcher{Dir: fx},
			Parse: feeds.EntriesFromFile, Now: func() time.Time { return now }}}
	res, err := s.Sync(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 {
		t.Fatal("ninguna fuente descargada")
	}
	m, path, err := datasets.SnapshotDir{Root: s.SnapshotDir()}.Latest()
	if err != nil || m.Version != "v1" {
		t.Fatalf("snapshot: %v %v", m, err)
	}
	f, err := os.Open(path) //nolint:gosec // test
	if err != nil {
		t.Fatal(err)
	}
	snap, err := reputation.ReadSnapshot(f)
	_ = f.Close()
	if err != nil || snap.Len() == 0 {
		t.Fatalf("snapshot ilegible: %v", err)
	}
	// Nada vencido un minuto después: no descarga ni publica otra versión.
	if res, err := s.Sync(context.Background(), now.Add(time.Minute)); err != nil || len(res) != 0 {
		t.Fatalf("segunda sincronización: %d %v", len(res), err)
	}
	if m2, _, _ := (datasets.SnapshotDir{Root: s.SnapshotDir()}).Latest(); m2.Version != "v1" {
		t.Fatalf("versión nueva sin cambios: %s", m2.Version)
	}
	st, err := s.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	custom, ok := 0, 0
	for _, x := range st {
		if x.Source.Origin == datasets.OriginCustom {
			custom++
		}
		if !x.State.LastSuccess.IsZero() && x.State.Current != nil && x.State.Current.Entries > 0 {
			ok++
		}
	}
	if custom == 0 || ok == 0 {
		t.Fatalf("estado: %d personalizadas, %d con datos, de %d", custom, ok, len(st))
	}
}
