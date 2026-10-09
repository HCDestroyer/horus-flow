package feedsync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	var prov customfeeds.SourceProvider
	if s.CustomPath != "" {
		if prov, err = customfeeds.PathProvider(s.CustomPath); err != nil {
			return nil, fmt.Errorf("listas personalizadas: %w", err)
		}
	}
	srcs, rejected, err := customfeeds.Resolve(ctx, cfg.ByKind(SourceKind), prov, customfeeds.DefaultPolicy())
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
// y, si alguna cambió o aún no hay snapshot, publica uno nuevo. Devuelve los
// resultados de la descarga.
func (s *Scheduler) Sync(ctx context.Context, now time.Time) ([]datasets.Result, error) {
	srcs, err := s.Sources(ctx)
	if err != nil {
		return nil, err
	}
	var due []datasets.Source
	for _, src := range srcs {
		if ok, _ := src.Allowed(s.Service.AllowUnverified); !ok {
			continue // licencia o desactivada: nunca se descarga
		}
		st, err := s.Service.Store.LoadState(src.ID)
		if err != nil {
			return nil, err
		}
		if st.LastAttempt.IsZero() || now.Sub(st.LastAttempt) >= time.Duration(src.Frequency) {
			due = append(due, src)
		}
	}
	var results []datasets.Result
	changed := false
	if len(due) > 0 {
		results = s.Service.Fetch(ctx, due, s.log())
		for _, r := range results {
			changed = changed || r.Status == datasets.StatusUpdated
		}
	}
	dir := datasets.SnapshotDir{Root: s.SnapshotDir()}
	if _, _, err := dir.Latest(); errors.Is(err, datasets.ErrNotFound) {
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
