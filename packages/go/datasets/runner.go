package datasets

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// Validator interpreta el archivo descargado de una fuente (en path) y
// devuelve el número de entradas válidas. Un error, o menos entradas que
// Source.MinEntries, rechaza la descarga sin tocar la versión vigente.
type Validator func(ctx context.Context, src Source, path string) (entries int, err error)

// Status es el resultado de procesar una fuente.
type Status string

const (
	StatusUpdated   Status = "updated"   // nueva versión válida guardada y vigente
	StatusUnchanged Status = "unchanged" // mismo SHA-256 que la vigente
	StatusSkipped   Status = "skipped"   // no se descarga (licencia, desactivada)
	StatusFailed    Status = "failed"    // caída, corrupta o vacía: se conserva la vigente
)

// Result es el resultado de Runner.RunOne.
type Result struct {
	SourceID string
	Status   Status
	Reason   string
	Manifest *Manifest // versión vigente tras el intento (puede ser la anterior)
	Err      error
}

// Runner ejecuta el ciclo descargar → validar → versionar de cada fuente.
type Runner struct {
	Store           *Store
	Fetcher         Fetcher
	Validate        Validator
	AllowUnverified bool
	Now             func() time.Time
	Log             *slog.Logger
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func (r *Runner) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.New(slog.DiscardHandler)
}

// Run procesa las fuentes en orden; el fallo de una no detiene a las demás.
func (r *Runner) Run(ctx context.Context, srcs []Source) []Result {
	out := make([]Result, 0, len(srcs))
	for _, s := range srcs {
		out = append(out, r.RunOne(ctx, s))
	}
	return out
}

// RunOne procesa una fuente. Ante cualquier fallo conserva la última versión
// válida y lo registra en el estado (para la métrica de antigüedad).
func (r *Runner) RunOne(ctx context.Context, src Source) Result {
	log := r.log().With("source", src.ID)
	if ok, reason := src.Allowed(r.AllowUnverified); !ok {
		log.Info("fuente omitida", "reason", reason)
		return Result{SourceID: src.ID, Status: StatusSkipped, Reason: reason}
	}
	st, err := r.Store.LoadState(src.ID)
	if err != nil {
		return Result{SourceID: src.ID, Status: StatusFailed, Err: err}
	}
	now := r.now()
	st.LastAttempt = now

	fail := func(err error) Result {
		st.LastError = err.Error()
		st.ConsecutiveFailures++
		if serr := r.Store.saveState(st); serr != nil {
			err = errors.Join(err, serr)
		}
		log.Warn("fuente fallida; se conserva la última versión válida",
			"error", err, "consecutive_failures", st.ConsecutiveFailures, "has_current", st.Current != nil)
		return Result{SourceID: src.ID, Status: StatusFailed, Err: err, Manifest: st.Current}
	}

	rc, err := r.Fetcher.Fetch(ctx, src)
	if err != nil {
		return fail(err)
	}
	tmp, sum, size, err := r.Store.incoming(src.ID, rc, src.MaxSize())
	if cerr := rc.Close(); err == nil && cerr != nil {
		err = cerr
	}
	if err != nil {
		return fail(fmt.Errorf("guardar descarga: %w", err))
	}
	defer func() { _ = os.Remove(tmp) }() // no-op si ya se renombró

	if st.Current != nil && st.Current.SHA256 == sum {
		st.LastSuccess, st.LastError, st.ConsecutiveFailures = now, "", 0
		if err := r.Store.saveState(st); err != nil {
			return Result{SourceID: src.ID, Status: StatusFailed, Err: err, Manifest: st.Current}
		}
		log.Info("fuente sin cambios", "sha256", sum)
		return Result{SourceID: src.ID, Status: StatusUnchanged, Manifest: st.Current}
	}

	entries, err := r.Validate(ctx, src, tmp)
	if err != nil {
		return fail(fmt.Errorf("contenido rechazado: %w", err))
	}
	minEntries := max(src.MinEntries, 1)
	if entries < minEntries {
		return fail(fmt.Errorf("contenido rechazado: %d entradas válidas, mínimo %d", entries, minEntries))
	}
	m := &Manifest{
		SchemaVersion: ManifestSchemaVersion,
		Kind:          src.Kind,
		SourceID:      src.ID,
		URL:           src.URL,
		Format:        src.Format,
		License:       src.License,
		LicenseURL:    src.LicenseURL,
		CommercialUse: src.CommercialUse,
		FetchedAt:     now,
		SHA256:        sum,
		Size:          size,
		Entries:       entries,
		Tool:          r.Store.Tool,
	}
	if err := r.Store.commit(tmp, m); err != nil {
		return fail(fmt.Errorf("versionar: %w", err))
	}
	st.Current, st.LastSuccess, st.LastError, st.ConsecutiveFailures = m, now, "", 0
	if err := r.Store.saveState(st); err != nil {
		return Result{SourceID: src.ID, Status: StatusFailed, Err: err, Manifest: m}
	}
	log.Info("fuente actualizada", "file", m.File, "sha256", sum, "entries", entries)
	return Result{SourceID: src.ID, Status: StatusUpdated, Manifest: m}
}
