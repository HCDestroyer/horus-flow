package platformevents

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/observability"
)

// Options configura un [Recorder].
type Options struct {
	// Store es el registro en PostgreSQL (nil = solo logs).
	Store *Store
	// Logger recibe una línea `platform event` por evento.
	Logger *slog.Logger
	// Process, Instance y Version se aplican a los eventos que no los traen.
	Process  string
	Instance string
	Version  string
	// SpoolDir: si no está vacío, los eventos que no se pudieron guardar se
	// apilan en SpoolDir/spool.jsonl hasta que PostgreSQL vuelva.
	SpoolDir string
	// Retention borra los eventos más antiguos (0 = no borra; lo hace un
	// solo proceso de la instalación).
	Retention time.Duration
	// Buffer en memoria (por defecto 4096 eventos).
	Buffer int
	// FlushEvery es el periodo máximo entre escrituras (por defecto 1 s).
	FlushEvery time.Duration
	Now        func() time.Time
}

// Recorder registra eventos de plataforma: log inmediato y escritura
// asíncrona por lotes en PostgreSQL, con reintento, spool en disco y
// retención. Implementa [Emitter].
type Recorder struct {
	o      Options
	mu     sync.Mutex
	queue  []Event
	wake   chan struct{}
	done   chan struct{}
	stopMu sync.Mutex
	// dropped cuenta eventos descartados por búfer lleno (también en logs).
	dropped int
}

// NewRecorder crea el registrador.
func NewRecorder(o Options) *Recorder {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Buffer <= 0 {
		o.Buffer = 4096
	}
	if o.FlushEvery <= 0 {
		o.FlushEvery = time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Recorder{o: o, wake: make(chan struct{}, 1), done: make(chan struct{})}
}

var _ Emitter = (*Recorder)(nil)

// Fill completa los campos por defecto de ev (id, hora, proceso, rol del
// contexto, traza).
func (r *Recorder) Fill(ctx context.Context, ev Event) Event {
	if ev.ID == uuid.Nil {
		ev.ID = uuid.Must(uuid.NewV7())
	}
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = r.o.Now()
	}
	ev.OccurredAt = ev.OccurredAt.UTC()
	if ev.Severity == "" {
		ev.Severity = SeverityInfo
	}
	if ev.Process == "" {
		ev.Process = r.o.Process
	}
	if ev.Instance == "" {
		ev.Instance = r.o.Instance
	}
	if ev.Version == "" {
		ev.Version = r.o.Version
	}
	if ev.Role == "" {
		ev.Role = observability.RoleFrom(ctx)
	}
	if ev.TraceID == "" {
		ev.TraceID, _ = observability.TraceFrom(ctx)
	}
	return ev
}

// Record implementa [Emitter]: escribe el log y encola el evento.
func (r *Recorder) Record(ctx context.Context, ev Event) {
	ev = r.Fill(ctx, ev)
	level := slog.LevelInfo
	switch ev.Severity {
	case SeverityWarn:
		level = slog.LevelWarn
	case SeverityError:
		level = slog.LevelError
	}
	attrs := []slog.Attr{slog.String("platform_event", ev.Kind), slog.String("platform_event_id", ev.ID.String()),
		slog.String("event_message", ev.Message)}
	if ev.TenantID != nil {
		attrs = append(attrs, slog.String(observability.KeyTenantID, ev.TenantID.String()))
	}
	if len(ev.Details) > 0 {
		attrs = append(attrs, slog.Any("details", ev.Details))
	}
	lctx := ctx
	if ev.Role != "" && observability.RoleFrom(ctx) == "" {
		lctx = observability.WithRole(ctx, ev.Role)
	}
	r.o.Logger.LogAttrs(lctx, level, "platform event", attrs...)
	if r.o.Store == nil {
		return
	}
	r.mu.Lock()
	if len(r.queue) >= r.o.Buffer {
		r.dropped++
		r.mu.Unlock()
		return
	}
	r.queue = append(r.queue, ev)
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Run escribe los eventos en PostgreSQL hasta que ctx se cancela (entonces
// hace un último intento acotado). Cada hora aplica la retención.
func (r *Recorder) Run(ctx context.Context) {
	defer close(r.done)
	if r.o.Store == nil {
		<-ctx.Done()
		return
	}
	t := time.NewTicker(r.o.FlushEvery)
	defer t.Stop()
	lastPurge := time.Time{}
	backoff := time.Duration(0)
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			_ = r.flush(fctx)
			cancel()
			return
		case <-t.C:
		case <-r.wake:
		}
		if backoff > 0 {
			select {
			case <-ctx.Done():
				continue
			case <-time.After(backoff):
			}
		}
		if err := r.flush(ctx); err != nil {
			backoff = min(max(2*backoff, time.Second), 30*time.Second)
		} else {
			backoff = 0
		}
		if r.o.Retention > 0 && time.Since(lastPurge) > time.Hour && backoff == 0 {
			lastPurge = time.Now()
			if n, err := r.o.Store.Purge(ctx, r.o.Now().Add(-r.o.Retention)); err != nil {
				r.o.Logger.WarnContext(ctx, "platform events retention failed", slog.Any("error", err))
			} else if n > 0 {
				r.o.Logger.InfoContext(ctx, "platform events purged", slog.Int64("count", n), slog.Duration("retention", r.o.Retention))
			}
		}
	}
}

// Flush escribe lo pendiente (apagado ordenado), con el plazo de ctx.
func (r *Recorder) Flush(ctx context.Context) error {
	if r.o.Store == nil {
		return nil
	}
	return r.flush(ctx)
}

func (r *Recorder) flush(ctx context.Context) error {
	r.stopMu.Lock()
	defer r.stopMu.Unlock()
	spooled, _ := r.readSpool()
	r.mu.Lock()
	batch := append(spooled, r.queue...)
	r.queue = nil
	dropped := r.dropped
	r.dropped = 0
	r.mu.Unlock()
	if dropped > 0 {
		r.o.Logger.WarnContext(ctx, "platform events dropped: buffer full", slog.Int("count", dropped))
	}
	if len(batch) == 0 {
		return nil
	}
	for i := 0; i < len(batch); i += 500 {
		end := min(i+500, len(batch))
		if err := r.o.Store.Insert(ctx, batch[i:end]); err != nil {
			rest := batch[i:]
			if r.o.SpoolDir != "" {
				if serr := r.writeSpool(rest); serr == nil {
					return err
				}
			}
			// Sin spool: vuelven a la cola (acotada) para el siguiente intento.
			r.mu.Lock()
			keep := append(rest, r.queue...)
			if len(keep) > r.o.Buffer {
				r.dropped += len(keep) - r.o.Buffer
				keep = keep[len(keep)-r.o.Buffer:]
			}
			r.queue = keep
			r.mu.Unlock()
			return err
		}
	}
	if len(spooled) > 0 {
		_ = os.Remove(r.spoolPath())
		r.o.Logger.InfoContext(ctx, "platform events spool replayed", slog.Int("count", len(spooled)))
	}
	return nil
}

func (r *Recorder) spoolPath() string { return filepath.Join(r.o.SpoolDir, "spool.jsonl") }

// writeSpool reescribe el spool con evs (los del spool anterior ya van en
// evs). Escritura atómica (tmp + rename).
func (r *Recorder) writeSpool(evs []Event) error {
	if err := os.MkdirAll(r.o.SpoolDir, 0o750); err != nil {
		return err //nolint:wrapcheck // error de sistema de archivos autoexplicativo
	}
	if len(evs) > 100_000 {
		evs = evs[len(evs)-100_000:]
	}
	tmp := r.spoolPath() + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640) //nolint:gosec // ruta del operador
	if err != nil {
		return err //nolint:wrapcheck // error de sistema de archivos autoexplicativo
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, ev := range evs {
		if err := enc.Encode(ev); err != nil {
			_ = f.Close()
			return err //nolint:wrapcheck // error de codificación autoexplicativo
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return err //nolint:wrapcheck // error de sistema de archivos autoexplicativo
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err //nolint:wrapcheck // error de sistema de archivos autoexplicativo
	}
	if err := f.Close(); err != nil {
		return err //nolint:wrapcheck // error de sistema de archivos autoexplicativo
	}
	return os.Rename(tmp, r.spoolPath()) //nolint:wrapcheck // error de sistema de archivos autoexplicativo
}

func (r *Recorder) readSpool() ([]Event, error) {
	if r.o.SpoolDir == "" {
		return nil, nil
	}
	f, err := os.Open(r.spoolPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err //nolint:wrapcheck // error de sistema de archivos autoexplicativo
	}
	defer func() { _ = f.Close() }()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var ev Event
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.ID != uuid.Nil {
			out = append(out, ev)
		}
	}
	return out, nil
}
