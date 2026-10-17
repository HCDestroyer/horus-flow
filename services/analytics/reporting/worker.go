package reporting

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/mailer"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
)

// Tipos de evento (events.md §8.10; dominio lógico reporting, regla E3).
const (
	EventCompleted = "horus.reporting.report.completed"
	EventFailed    = "horus.reporting.report.failed"
)

// Worker planifica y genera los reportes: el planificador encola una
// ejecución por definición y periodo vencido (idempotente por la unicidad de
// PostgreSQL, seguro con varias réplicas), el generador reclama ejecuciones
// con FOR UPDATE SKIP LOCKED (una a la vez por proceso: render CPU
// intensivo) y la retención borra los archivos caducados.
type Worker struct {
	St        *Store
	Files     Files
	Collector *Collector
	Directory func() (authapi.TenantDirectory, bool)
	Now       func() time.Time
	Logger    *slog.Logger
	// Retention de los archivos (HORUS_REPORTS_RETENTION, 365 días por defecto).
	Retention time.Duration
	// Lease: plazo de una ejecución reclamada; vencido, otro worker la retoma.
	Lease       time.Duration
	MaxAttempts int
	// SMTP de la instalación para el aviso a los destinatarios (vacío = sin aviso).
	SMTP          mailer.Config
	PublicBaseURL string
	// GenerateTimeout acota la recogida de datos y el render de una ejecución.
	GenerateTimeout time.Duration
	// Periodos de los bucles (0 = por defecto).
	ScheduleEvery, PollEvery, RetentionEvery time.Duration

	wake    chan struct{}
	lastDir time.Time
}

func (w *Worker) now() time.Time { return w.Now().UTC() }

func (w *Worker) init() {
	if w.wake == nil {
		w.wake = make(chan struct{}, 1)
	}
	if w.Lease <= 0 {
		w.Lease = 15 * time.Minute
	}
	if w.MaxAttempts <= 0 {
		w.MaxAttempts = 3
	}
	if w.GenerateTimeout <= 0 {
		w.GenerateTimeout = 10 * time.Minute
	}
	if w.ScheduleEvery <= 0 {
		w.ScheduleEvery = time.Minute
	}
	if w.PollEvery <= 0 {
		w.PollEvery = 10 * time.Second
	}
	if w.RetentionEvery <= 0 {
		w.RetentionEvery = time.Hour
	}
	if w.Logger == nil {
		w.Logger = slog.New(slog.DiscardHandler)
	}
}

// Wake despierta al generador (no bloquea).
func (w *Worker) Wake() {
	w.init()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Run ejecuta los tres bucles hasta que ctx se cancela.
func (w *Worker) Run(ctx context.Context) {
	w.init()
	sched := time.NewTicker(w.ScheduleEvery)
	poll := time.NewTicker(w.PollEvery)
	ret := time.NewTicker(w.RetentionEvery)
	defer sched.Stop()
	defer poll.Stop()
	defer ret.Stop()
	w.Schedule(ctx)
	w.Drain(ctx)
	w.Purge(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-sched.C:
			w.Schedule(ctx)
			w.Drain(ctx)
		case <-poll.C:
			w.Drain(ctx)
		case <-w.wake:
			w.Drain(ctx)
		case <-ret.C:
			w.Purge(ctx)
		}
	}
}

// Schedule encola las ejecuciones programadas vencidas. Con auth en el
// proceso crea la definición por defecto (habilitada) de cada ISP activo y
// mantiene su copia de nombre y slug.
func (w *Worker) Schedule(ctx context.Context) int {
	w.init()
	now := w.now()
	status := map[uuid.UUID]string{}
	if w.Directory != nil {
		if dir, ok := w.Directory(); ok {
			if list, err := dir.TenantSummaries(ctx); err == nil {
				refresh := now.Sub(w.lastDir) >= 15*time.Minute
				known, _ := w.St.DefinitionTenants(ctx)
				for _, t := range list {
					status[t.ID] = t.Status
					if t.Status != "active" || (!refresh && known[t.ID]) {
						continue
					}
					for _, k := range Kinds {
						if _, err := w.St.EnsureDefinition(ctx, pgdb.TenantID(t.ID), k, t.Timezone, t.Name, t.Slug, now); err != nil {
							w.Logger.WarnContext(ctx, "reporting: default definition", slog.String("tenant_id", t.ID.String()), slog.Any("error", err))
						}
					}
					if err := w.St.RefreshDirectory(ctx, pgdb.TenantID(t.ID), t.Name, t.Slug); err != nil {
						w.Logger.WarnContext(ctx, "reporting: refresh directory", slog.Any("error", err))
					}
				}
				if refresh {
					w.lastDir = now
				}
			}
		}
	}
	defs, err := w.St.EnabledDefinitions(ctx)
	if err != nil {
		w.Logger.WarnContext(ctx, "reporting: scheduler", slog.Any("error", err))
		return 0
	}
	n := 0
	for i := range defs {
		d := &defs[i]
		if st, ok := status[d.TenantID]; ok && st != "active" {
			continue // ISP suspendido o en baja: sin reportes programados
		}
		p, ok := d.Schedule().Due(now, d.CreatedAt)
		if !ok {
			continue
		}
		created, id, err := w.St.InsertScheduled(ctx, d, p, now)
		if err != nil {
			w.Logger.WarnContext(ctx, "reporting: enqueue", slog.String("tenant_id", d.TenantID.String()), slog.Any("error", err))
			continue
		}
		if created {
			n++
			w.Logger.InfoContext(ctx, "reporting: scheduled run queued", slog.String("tenant_id", d.TenantID.String()),
				slog.String("report_id", id.String()), slog.Time("period_from", p.From))
		}
	}
	return n
}

// Drain genera ejecuciones pendientes hasta vaciar la cola o cancelar ctx.
func (w *Worker) Drain(ctx context.Context) int {
	w.init()
	if failed, err := w.St.FailExhausted(ctx, w.now(), w.MaxAttempts); err == nil {
		for i := range failed {
			w.Logger.ErrorContext(ctx, "reporting: run exhausted", slog.String("report_id", failed[i].ID.String()))
		}
	}
	n := 0
	for ctx.Err() == nil {
		r, err := w.St.Claim(ctx, w.now(), w.Lease, w.MaxAttempts)
		if err != nil {
			w.Logger.WarnContext(ctx, "reporting: claim", slog.Any("error", err))
			return n
		}
		if r == nil {
			return n
		}
		w.Process(ctx, r)
		n++
	}
	return n
}

// Process genera los archivos de una ejecución reclamada y la cierra.
func (w *Worker) Process(ctx context.Context, r *Run) {
	w.init()
	log := w.Logger.With(slog.String("tenant_id", r.TenantID.String()), slog.String("report_id", r.ID.String()))
	gctx, cancel := context.WithTimeout(ctx, w.GenerateTimeout)
	defer cancel()
	def := w.definition(ctx, r)
	name := w.tenantName(ctx, r.TenantID, def)
	started := time.Now()
	data := w.Collector.Collect(gctx, r.TenantID, name, r.Period())
	var files []File
	var failure string
	for _, format := range r.Formats {
		render := func(out io.Writer) error { return RenderPDF(out, data) }
		if format == FormatCSV {
			render = func(out io.Writer) error { return RenderCSV(out, data) }
		}
		f, err := w.Files.Write(r.TenantID, r.ID, format, render)
		if err != nil {
			log.ErrorContext(ctx, "reporting: render failed", slog.String("format", format), slog.Any("error", err))
			failure = "No se pudo generar el archivo " + strings.ToUpper(format) + " (error interno o de escritura en el almacén local)."
			break
		}
		files = append(files, f)
	}
	if ctx.Err() != nil {
		return // apagado: la ejecución vuelve a la cola al vencer su plazo
	}
	now := w.now()
	r.CompletedAt = &now
	evType := EventCompleted
	if failure != "" {
		for _, f := range files {
			_ = w.Files.Remove(f.ObjectKey)
		}
		r.Status, r.Error, r.Files = StatusFailed, &failure, []File{}
		evType = EventFailed
	} else {
		exp := now.Add(w.Retention)
		r.Status, r.Files, r.ExpiresAt = StatusCompleted, files, &exp
	}
	if err := w.St.Finish(ctx, r, runEvent(evType, r, now)); err != nil {
		log.ErrorContext(ctx, "reporting: finish", slog.Any("error", err))
		return
	}
	log.InfoContext(ctx, "reporting: run finished", slog.String("status", r.Status), slog.Int64("size_bytes", r.SizeBytes()),
		slog.Duration("took", time.Since(started)))
	if r.Status == StatusCompleted && r.DefinitionID != nil && def != nil {
		w.notify(ctx, def, r, name)
	}
}

func (w *Worker) definition(ctx context.Context, r *Run) *Definition {
	if r.DefinitionID == nil {
		defs, err := w.St.ListDefinitions(ctx, pgdb.TenantID(r.TenantID))
		if err == nil {
			for i := range defs {
				if defs[i].Kind == r.Kind {
					return &defs[i]
				}
			}
		}
		return nil
	}
	d, err := w.St.GetDefinition(ctx, pgdb.TenantID(r.TenantID), *r.DefinitionID)
	if err != nil {
		return nil
	}
	return d
}

// tenantName: directorio de auth, copia en la definición o un nombre genérico.
func (w *Worker) tenantName(ctx context.Context, t uuid.UUID, d *Definition) string {
	if w.Directory != nil {
		if dir, ok := w.Directory(); ok {
			if list, err := dir.TenantSummaries(ctx); err == nil {
				for _, x := range list {
					if x.ID == t && x.Name != "" {
						return x.Name
					}
				}
			}
		}
	}
	if d != nil && d.TenantName != "" {
		return d.TenantName
	}
	return "ISP " + t.String()[:8]
}

// runEvent es el evento de dominio de una ejecución cerrada (ReportRun, events.md §8.10).
func runEvent(typ string, r *Run, at time.Time) outbox.Event {
	files := make([]map[string]any, 0, len(r.Files))
	for _, f := range r.Files {
		files = append(files, map[string]any{"format": f.Format, "object_key": f.ObjectKey, "sha256": f.SHA256, "size_bytes": f.SizeBytes})
	}
	var def, by any
	if r.DefinitionID != nil {
		def = r.DefinitionID.String()
	}
	if r.CreatedBy != nil {
		by = r.CreatedBy.String()
	}
	var errText any
	if r.Error != nil {
		errText = *r.Error
	}
	actor := outbox.Actor{Type: "system", ID: "system:horus"}
	tid := r.TenantID
	return outbox.Event{Type: typ, Source: "horus/analytics", TenantID: &tid, AggregateType: "report", AggregateID: r.ID, AggregateVersion: 1,
		Actor: actor, OccurredAt: at, Data: map[string]any{
			"id": r.ID.String(), "report_definition_id": def, "kind": r.Kind, "status": r.Status, "formats": r.Formats,
			"period_from": r.PeriodFrom.UTC().Format(outbox.TimeFormat), "period_to": r.PeriodTo.UTC().Format(outbox.TimeFormat),
			"timezone": r.Timezone, "files": files, "size_bytes": r.SizeBytes(), "requested_by": by, "error": errText,
		}}
}

// notify avisa por correo a los destinatarios de la definición con un
// enlace a la página de reportes de la consola (sin adjunto: el archivo solo
// se descarga por la API, autenticado).
func (w *Worker) notify(ctx context.Context, d *Definition, r *Run, name string) {
	if len(d.Recipients) == 0 || !w.SMTP.Enabled() {
		return
	}
	msg := NoticeMessage(d, r, name, w.PublicBaseURL)
	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := mailer.Send(sctx, w.SMTP, msg); err != nil {
		w.Logger.WarnContext(ctx, "reporting: notice email failed", slog.String("report_id", r.ID.String()),
			slog.Int("recipients", len(d.Recipients)), slog.Bool("auth", errors.Is(err, mailer.ErrAuth)), slog.Any("error", err))
	}
}

// NoticeMessage compone el aviso de reporte listo (texto plano, español).
func NoticeMessage(d *Definition, r *Run, name, baseURL string) mailer.Message {
	p := r.Period()
	y, wk := p.ISOWeek()
	subject := fmt.Sprintf("Reporte de seguridad semanal de %s · semana %d de %d", name, wk, y)
	var b strings.Builder
	fmt.Fprintf(&b, "Hola:\n\nYa está disponible el reporte de seguridad semanal de %s.\n\n", name)
	fmt.Fprintf(&b, "Periodo: %s (%s)\n", FmtPeriod(p), p.Loc.String())
	fmt.Fprintf(&b, "Formatos: %s\n\n", strings.ToUpper(strings.Join(r.Formats, ", ")))
	if baseURL != "" && d.TenantSlug != "" {
		fmt.Fprintf(&b, "Descárguelo desde la consola (requiere iniciar sesión y el permiso de reportes):\n%s/t/%s/reports\n\n",
			strings.TrimRight(baseURL, "/"), d.TenantSlug)
	} else {
		b.WriteString("Descárguelo desde la consola de Horus Flow, sección Reportes (requiere iniciar sesión y el permiso de reportes).\n\n")
	}
	b.WriteString("Por seguridad, el reporte no va adjunto: contiene direcciones IP y alias de clientes.\n\n— Horus Flow\n")
	return mailer.Message{To: d.Recipients, Subject: subject, Text: b.String()}
}

// Purge borra los archivos de las ejecuciones caducadas (retención) y lo
// registra en la fila (la fila se conserva como historial).
func (w *Worker) Purge(ctx context.Context) int {
	w.init()
	n := 0
	for ctx.Err() == nil {
		runs, err := w.St.Expired(ctx, w.now(), 100)
		if err != nil {
			w.Logger.WarnContext(ctx, "reporting: retention", slog.Any("error", err))
			return n
		}
		if len(runs) == 0 {
			return n
		}
		for i := range runs {
			ok := true
			for _, f := range runs[i].Files {
				if err := w.Files.Remove(f.ObjectKey); err != nil {
					w.Logger.WarnContext(ctx, "reporting: retention remove", slog.String("report_id", runs[i].ID.String()), slog.Any("error", err))
					ok = false
				}
			}
			if !ok {
				return n // se reintenta en la próxima pasada
			}
			if err := w.St.MarkPurged(ctx, runs[i].ID, w.now()); err != nil {
				w.Logger.WarnContext(ctx, "reporting: retention mark", slog.Any("error", err))
				return n
			}
			n++
		}
	}
	return n
}
