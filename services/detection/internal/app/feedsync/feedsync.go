// Package feedsync orquesta los feeds de reputación de plataforma: descarga
// y validación de cada fuente (con conservación de la última versión válida)
// y compilación del snapshot de reputación a partir de las versiones vigentes
// (docs/traffic-model.md §11, ADR-0024).
package feedsync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/services/detection/api/reputation"
)

// SourceKind es el valor de `kind` de los feeds de reputación en
// config/feeds.yaml.
const SourceKind = "reputation"

// ParseFunc interpreta el archivo de una fuente (puerto implementado por
// adapters/feeds). warnings resume lo descartado sin rechazar el archivo
// (p. ej. entradas peligrosas de una lista personalizada en modo "warn").
type ParseFunc func(path string, src datasets.Source, fetchedAt time.Time) (entries []reputation.Entry, warnings []string, err error)

// Service agrupa las dependencias del caso de uso.
type Service struct {
	Store           *datasets.Store
	Fetcher         datasets.Fetcher
	Parse           ParseFunc
	AllowUnverified bool
	Now             func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Validator adapta Parse al pipeline de datasets.
func (s *Service) Validator() datasets.Validator { return s.validator(nil) }

// validator registra en log (si no es nil) los avisos de cada descarga
// aceptada.
func (s *Service) validator(log *slog.Logger) datasets.Validator {
	return func(_ context.Context, src datasets.Source, path string) (int, error) {
		entries, warnings, err := s.Parse(path, src, s.now())
		if err == nil && log != nil {
			for _, w := range warnings {
				log.Warn("entradas descartadas por la política de listas peligrosas", "source", src.ID, "detail", w)
			}
		}
		return len(entries), err
	}
}

// Fetch descarga y valida las fuentes de reputación.
func (s *Service) Fetch(ctx context.Context, sources []datasets.Source, log *slog.Logger) []datasets.Result {
	r := &datasets.Runner{
		Store: s.Store, Fetcher: s.Fetcher, Validate: s.validator(log),
		AllowUnverified: s.AllowUnverified, Now: s.now, Log: log,
	}
	return r.Run(ctx, filterKind(sources))
}

func filterKind(sources []datasets.Source) []datasets.Source {
	var out []datasets.Source
	for _, src := range sources {
		if src.Kind == SourceKind {
			out = append(out, src)
		}
	}
	return out
}

// SourceReport resume la aportación de una fuente al snapshot.
type SourceReport struct {
	SourceID string
	Used     bool
	Entries  int
	Expired  int
	Reason   string
	Warnings []string
}

// ErrNoSources indica que ninguna fuente tenía una versión válida: no se
// genera snapshot y sigue vigente el anterior.
var ErrNoSources = errors.New("ninguna fuente de reputación tiene una versión válida")

// Compile construye el snapshot con la versión vigente (última válida) de
// cada fuente permitida. Una fuente caída aporta su último dataset válido;
// una que nunca tuvo versión válida, o cuya licencia ya no lo permite, se
// omite. Se descartan las entradas caducadas.
func (s *Service) Compile(sources []datasets.Source) (*reputation.Snapshot, []SourceReport, error) {
	now := s.now()
	var (
		all     []reputation.Entry
		refs    []datasets.SourceRef
		reports []SourceReport
	)
	for _, src := range filterKind(sources) {
		rep := SourceReport{SourceID: src.ID}
		if ok, reason := src.Allowed(s.AllowUnverified); !ok {
			rep.Reason = reason
			reports = append(reports, rep)
			continue
		}
		f, m, err := s.Store.OpenCurrent(src.ID)
		if err != nil {
			rep.Reason = err.Error()
			reports = append(reports, rep)
			continue
		}
		path := f.Name()
		_ = f.Close()
		entries, warnings, err := s.Parse(path, src, m.FetchedAt)
		rep.Warnings = warnings
		if err != nil {
			rep.Reason = fmt.Sprintf("versión vigente ilegible: %v", err)
			reports = append(reports, rep)
			continue
		}
		kept := entries[:0]
		for _, e := range entries {
			if !e.ExpiresAt.IsZero() && e.ExpiresAt.Before(now) {
				rep.Expired++
				continue
			}
			kept = append(kept, e)
		}
		rep.Used, rep.Entries = true, len(kept)
		all = append(all, kept...)
		refs = append(refs, datasets.RefFromManifest(src, m, len(kept)))
		reports = append(reports, rep)
	}
	if len(refs) == 0 {
		return nil, reports, ErrNoSources
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })
	snap, err := reputation.NewSnapshot(datasets.SnapshotMeta{CreatedAt: now, Sources: refs}, all)
	if err != nil {
		return nil, reports, err
	}
	return snap, reports, nil
}
