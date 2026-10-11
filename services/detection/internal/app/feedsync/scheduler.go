package feedsync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/services/detection/api/customfeeds"
	"github.com/hcdestroyer/horus-flow/services/detection/api/reputation"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/feeds"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/config"
)

// Scheduler integra la sincronización de feeds en el rol detection: resuelve
// el catálogo embebido + las listas personalizadas (D20), descarga solo las
// fuentes vencidas según su frecuencia, compila y publica el snapshot en
// $HORUS_DATA_DIR/catalog/reputation/ (lo leen el motor y el ingester) y
// expone el estado de cada fuente para GET /reputation/sources.
type Scheduler struct {
	// ConfigPath: declaración de feeds (vacío = embebida); CustomPath: YAML o
	// directorio de listas personalizadas (vacío = ninguna).
	ConfigPath, CustomPath string
	DataDir                string
	Service                *Service
	Log                    *slog.Logger
	// Registry: fuentes gestionadas por la API de plataforma (PostgreSQL):
	// activación de las del catálogo y listas personalizadas. nil = solo
	// archivos.
	Registry Registry
	// Policy valida las listas personalizadas (cero = customfeeds.DefaultPolicy).
	Policy *customfeeds.Policy

	// mu serializa las descargas y la publicación (un solo escritor por
	// almacén); state protege forced/dirty.
	mu      sync.Mutex
	state   sync.Mutex
	forced  map[string]bool
	dirty   bool
	kick    chan struct{}
	kickOne sync.Once
}

// Registry son las fuentes que gestiona la API de plataforma.
type Registry interface {
	customfeeds.SourceProvider
	// CatalogEnabled devuelve la activación de las fuentes del catálogo por
	// id (las ausentes conservan la de la declaración).
	CatalogEnabled(ctx context.Context) (map[string]bool, error)
}

// Kick devuelve el canal que avisa de que hay cargas pedidas (Refresh) o un
// snapshot que recompilar (Invalidate).
func (s *Scheduler) Kick() <-chan struct{} { return s.kickCh() }

func (s *Scheduler) kickCh() chan struct{} {
	s.kickOne.Do(func() { s.kick = make(chan struct{}, 1) })
	return s.kick
}

func (s *Scheduler) wake() {
	select {
	case s.kickCh() <- struct{}{}:
	default:
	}
}

// Refresh encola una carga inmediata de la fuente id (asíncrona: la hace el
// siguiente SyncPending o Sync).
func (s *Scheduler) Refresh(id string) {
	s.state.Lock()
	if s.forced == nil {
		s.forced = map[string]bool{}
	}
	s.forced[id] = true
	s.state.Unlock()
	s.wake()
}

// Pending indica si la fuente id tiene una carga encolada.
func (s *Scheduler) Pending(id string) bool {
	s.state.Lock()
	defer s.state.Unlock()
	return s.forced[id]
}

// Invalidate pide recompilar el snapshot (p. ej. al desactivar o borrar una
// fuente: sus indicadores salen del siguiente snapshot).
func (s *Scheduler) Invalidate() {
	s.state.Lock()
	s.dirty = true
	s.state.Unlock()
	s.wake()
}

// requeue devuelve a la cola lo que no se pudo hacer (sin despertar: se
// reintenta en la siguiente vuelta, no en bucle).
func (s *Scheduler) requeue(forced map[string]bool, dirty bool) {
	s.state.Lock()
	defer s.state.Unlock()
	if len(forced) > 0 && s.forced == nil {
		s.forced = map[string]bool{}
	}
	for id := range forced {
		s.forced[id] = true
	}
	s.dirty = s.dirty || dirty
}

// take devuelve y limpia las cargas encoladas y la marca de recompilación.
func (s *Scheduler) take() (map[string]bool, bool) {
	s.state.Lock()
	defer s.state.Unlock()
	f, d := s.forced, s.dirty
	s.forced, s.dirty = nil, false
	return f, d
}

func (s *Scheduler) policy() customfeeds.Policy {
	if s.Policy != nil {
		return *s.Policy
	}
	return customfeeds.DefaultPolicy()
}

// Catalog devuelve las fuentes de reputación de la declaración (catálogo
// base) con formato soportado, sin las activaciones del Registry.
func (s *Scheduler) Catalog() ([]datasets.Source, error) {
	cfg, err := config.LoadFeeds(s.ConfigPath)
	if err != nil {
		return nil, err
	}
	var out []datasets.Source
	for _, src := range cfg.ByKind(SourceKind) {
		if feeds.Supports(src.Format) {
			out = append(out, src)
		}
	}
	return out, nil
}

// LoadState devuelve el estado de descarga de la fuente id.
func (s *Scheduler) LoadState(id string) (datasets.State, error) {
	return s.Service.Store.LoadState(id)
}

// SourceStatus es el estado de una fuente para la API.
type SourceStatus struct {
	Source datasets.Source
	State  datasets.State
}

// SnapshotDir es la carpeta de snapshots publicados.
func (s *Scheduler) SnapshotDir() string { return datasets.CatalogDir(s.DataDir, reputation.Kind) }

// Sources resuelve las fuentes de reputación vigentes (las personalizadas
// inválidas se informan y no se cargan).
func (s *Scheduler) Sources(ctx context.Context) ([]datasets.Source, error) {
	cfg, err := config.LoadFeeds(s.ConfigPath)
	if err != nil {
		return nil, err
	}
	catalog := cfg.ByKind(SourceKind)
	var provs customfeeds.Multi
	if s.CustomPath != "" {
		p, err := customfeeds.PathProvider(s.CustomPath)
		if err != nil {
			return nil, fmt.Errorf("listas personalizadas: %w", err)
		}
		provs = append(provs, p)
	}
	if s.Registry != nil {
		enabled, err := s.Registry.CatalogEnabled(ctx)
		if err != nil {
			return nil, fmt.Errorf("fuentes de plataforma: %w", err)
		}
		for i := range catalog {
			if on, ok := enabled[catalog[i].ID]; ok {
				catalog[i].Enabled = &on
			}
		}
		provs = append(provs, s.Registry)
	}
	var prov customfeeds.SourceProvider
	if len(provs) > 0 {
		prov = provs
	}
	srcs, rejected, err := customfeeds.Resolve(ctx, catalog, prov, s.policy())
	if err != nil {
		return nil, err
	}
	for _, r := range rejected {
		s.log().WarnContext(ctx, "custom reputation list rejected", "id", r.ID, "err", r.Err)
	}
	out := srcs[:0]
	for _, src := range srcs {
		if feeds.Supports(src.Format) {
			out = append(out, src)
		}
	}
	return out, nil
}

// Status devuelve el estado de cada fuente.
func (s *Scheduler) Status(ctx context.Context) ([]SourceStatus, error) {
	srcs, err := s.Sources(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SourceStatus, 0, len(srcs))
	for _, src := range srcs {
		st, err := s.Service.Store.LoadState(src.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, SourceStatus{Source: src, State: st})
	}
	return out, nil
}

// Sync descarga las fuentes vencidas (último intento hace ≥ su frecuencia)
// y las encoladas con Refresh y, si alguna cambió, se pidió recompilar o aún
// no hay snapshot, publica uno nuevo. Devuelve los resultados de la descarga.
func (s *Scheduler) Sync(ctx context.Context, now time.Time) ([]datasets.Result, error) {
	return s.sync(ctx, now, true)
}

// SyncPending hace solo lo pedido (Refresh, Invalidate): sirve cuando la
// sincronización periódica está desactivada.
func (s *Scheduler) SyncPending(ctx context.Context, now time.Time) ([]datasets.Result, error) {
	return s.sync(ctx, now, false)
}

func (s *Scheduler) sync(ctx context.Context, now time.Time, periodic bool) (results []datasets.Result, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	forced, dirty := s.take()
	defer func() {
		if err != nil {
			s.requeue(forced, dirty)
		}
	}()
	srcs, err := s.Sources(ctx)
	if err != nil {
		return nil, err
	}
	var due []datasets.Source
	for _, src := range srcs {
		if ok, _ := src.Allowed(s.Service.AllowUnverified); !ok {
			continue // licencia o desactivada: nunca se descarga
		}
		if forced[src.ID] {
			due = append(due, src)
			continue
		}
		if !periodic {
			continue
		}
		st, err := s.Service.Store.LoadState(src.ID)
		if err != nil {
			return nil, err
		}
		if st.LastAttempt.IsZero() || now.Sub(st.LastAttempt) >= time.Duration(src.Frequency) {
			due = append(due, src)
		}
	}
	changed := dirty
	if len(due) > 0 {
		results = s.Service.Fetch(ctx, due, s.log())
		for _, r := range results {
			changed = changed || r.Status == datasets.StatusUpdated
		}
	}
	dir := datasets.SnapshotDir{Root: s.SnapshotDir()}
	if _, _, err := dir.Latest(); errors.Is(err, datasets.ErrNotFound) && (periodic || len(due) > 0) {
		changed = true
	}
	if !changed {
		return results, nil
	}
	snap, _, err := s.Service.Compile(srcs)
	if errors.Is(err, ErrNoSources) {
		return results, nil // sin datos válidos: sigue vigente el anterior (o ninguno)
	}
	if err != nil {
		return results, err
	}
	m, _, err := snap.Publish(dir)
	if err != nil {
		return results, err
	}
	s.log().InfoContext(ctx, "reputation snapshot published", "version", m.Version, "entries", m.Entries, "sources", len(m.Sources))
	return results, nil
}

func (s *Scheduler) log() *slog.Logger {
	if s.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.Log
}
