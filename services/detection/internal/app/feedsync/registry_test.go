package feedsync

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/services/detection/api/customfeeds"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/feeds"
)

// memRegistry es el Registry de la API de plataforma en memoria.
type memRegistry struct {
	enabled map[string]bool
	custom  []customfeeds.SourceSpec
}

func (r *memRegistry) CustomSources(context.Context) ([]customfeeds.SourceSpec, error) {
	return r.custom, nil
}
func (r *memRegistry) CatalogEnabled(context.Context) (map[string]bool, error) { return r.enabled, nil }

// recFetcher anota qué fuentes se descargan y con qué cabeceras.
type recFetcher struct {
	datasets.DirFetcher
	mu      sync.Mutex
	fetched []string
	headers map[string]http.Header
}

func (f *recFetcher) Fetch(ctx context.Context, s datasets.Source) (io.ReadCloser, error) {
	f.mu.Lock()
	f.fetched = append(f.fetched, s.ID)
	f.headers[s.ID] = s.Header
	f.mu.Unlock()
	return f.DirFetcher.Fetch(ctx, s)
}

func (f *recFetcher) take() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.fetched
	f.fetched = nil
	return out
}

// Las fuentes de la API de plataforma entran en la sincronización: el
// catálogo con su activación, las personalizadas con su cabecera; Refresh
// fuerza una carga y Invalidate recompila el snapshot sin descargar.
func TestSchedulerRegistryRefreshAndInvalidate(t *testing.T) {
	fx := filepath.Join("..", "..", "..", "..", "..", "tests", "fixtures", "feeds")
	dir := t.TempDir()
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	on := true
	reg := &memRegistry{enabled: map[string]bool{"tor-exit": false}, custom: []customfeeds.SourceSpec{{
		ID: "custom-honeypot", Name: "Honeypot", URL: "https://honeypot.example.net/ssh.txt", Format: "ip-list", Category: "scanner",
		Confidence: 70, Frequency: datasets.Duration(15 * time.Minute), Enabled: &on, Header: http.Header{"Auth-Key": {"k"}},
	}}}
	f := &recFetcher{DirFetcher: datasets.DirFetcher{Dir: fx}, headers: map[string]http.Header{}}
	pol := customfeeds.DefaultPolicy()
	pol.MinFrequency = 15 * time.Minute
	s := &Scheduler{ConfigPath: filepath.Join(fx, "feeds.yaml"), DataDir: dir, Registry: reg, Policy: &pol,
		Service: &Service{Store: &datasets.Store{Root: datasets.DatasetsDir(dir)}, Fetcher: f, Parse: feeds.EntriesFromFile,
			Now: func() time.Time { return now }}}

	// Sin nada pedido, SyncPending no descarga ni publica.
	if res, err := s.SyncPending(context.Background(), now); err != nil || len(res) != 0 {
		t.Fatalf("SyncPending vacío: %v %v", res, err)
	}
	if _, err := s.Sync(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, id := range f.take() {
		got[id] = true
	}
	if got["tor-exit"] || !got["custom-honeypot"] || !got["abusech-feodo"] {
		t.Fatalf("descargadas: %v", got)
	}
	if f.headers["custom-honeypot"].Get("Auth-Key") != "k" {
		t.Fatalf("cabecera: %v", f.headers["custom-honeypot"])
	}
	m1, _, err := datasets.SnapshotDir{Root: s.SnapshotDir()}.Latest()
	if err != nil {
		t.Fatal(err)
	}

	// Refresh: solo esa fuente, aunque no haya vencido.
	s.Refresh("abusech-feodo")
	if !s.Pending("abusech-feodo") {
		t.Fatal("no queda pendiente")
	}
	select {
	case <-s.Kick():
	default:
		t.Fatal("Refresh no despierta al planificador")
	}
	if _, err := s.SyncPending(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if ids := f.take(); len(ids) != 1 || ids[0] != "abusech-feodo" || s.Pending("abusech-feodo") {
		t.Fatalf("refresh: %v", ids)
	}

	// Invalidate (p. ej. al borrar la personalizada): nuevo snapshot sin descargar.
	reg.custom = nil
	s.Invalidate()
	if _, err := s.SyncPending(context.Background(), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if ids := f.take(); len(ids) != 0 {
		t.Fatalf("Invalidate descargó %v", ids)
	}
	m2, _, err := datasets.SnapshotDir{Root: s.SnapshotDir()}.Latest()
	if err != nil || m2.Version == m1.Version {
		t.Fatalf("snapshot no recompilado: %v → %v (%v)", m1.Version, m2.Version, err)
	}
	for _, src := range m2.Sources {
		if src.ID == "custom-honeypot" {
			t.Fatal("la fuente borrada sigue en el snapshot")
		}
	}
}
