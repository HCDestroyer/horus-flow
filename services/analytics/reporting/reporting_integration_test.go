//go:build integration

package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
	"github.com/hcdestroyer/horus-flow/services/analytics/dashboards"
	secapi "github.com/hcdestroyer/horus-flow/services/detection/api/securityreport"
)

type itEnv struct {
	db    *pgdb.DB
	st    *Store
	svc   *Service
	files Files
	now   time.Time
	sec   *fakeSecurity
}

func newEnv(t *testing.T) *itEnv {
	t.Helper()
	ctx := context.Background()
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: pgtest.New(t), AppRole: "analytics_app", PlatformRole: "analytics_platform"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := pgdb.Migrate(ctx, db, Schema, dashboards.Migrations(), nil); err != nil {
		t.Fatal(err)
	}
	e := &itEnv{db: db, st: &Store{DB: db}, files: Files{Root: t.TempDir()}, now: time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC),
		sec: &fakeSecurity{rep: sampleReport()}}
	e.svc = &Service{St: e.st, Files: e.files, Cursor: pagination.NewCodec(nil), Now: func() time.Time { return e.now },
		Logger: discard(), Retention: 365 * 24 * time.Hour}
	return e
}

func (e *itEnv) worker() *Worker {
	return &Worker{St: e.st, Files: e.files, Now: func() time.Time { return e.now }, Logger: discard(), Retention: 365 * 24 * time.Hour,
		Collector: &Collector{Security: func() (secapi.Reader, bool) { return e.sec, true }, Now: func() time.Time { return e.now }}}
}

func as(tenant uuid.UUID, perms ...string) context.Context {
	p := &authz.Principal{Type: authz.TypeUser, Subject: "user:" + tenant.String()[:8], UserID: uuid.NewSHA1(tenant, []byte("u")),
		Scope: authz.ScopeTenant, TenantID: tenant, Perms: map[string][]string{}}
	for _, x := range perms {
		p.Perms[x] = []string{authz.ScopeAll}
	}
	return authz.WithPrincipal(context.Background(), p)
}

var allPerms = []string{PermRead, PermExport, PermSettings}

func code(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Code
	}
	return ""
}

func TestReportsEndToEnd(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	A, B := uuid.New(), uuid.New()
	ctxA, ctxB := as(A, allPerms...), as(B, allPerms...)

	// Generación a petición (sin rango: última semana ISO completa en la zona del ISP, UTC por defecto).
	key := uuid.New()
	run, replayed, err := e.svc.CreateRun(ctxA, CreateInput{}, key)
	if err != nil || replayed || run.Status != StatusQueued || !run.PeriodFrom.Equal(time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("create = %+v %v %v", run, replayed, err)
	}
	again, replayed, err := e.svc.CreateRun(ctxA, CreateInput{Formats: []string{"csv"}}, key)
	if err != nil || !replayed || again.ID != run.ID {
		t.Fatalf("misma Idempotency-Key: %+v %v %v", again, replayed, err)
	}
	// B no ve ni descarga la ejecución de A (404, como si no existiera).
	if _, err := e.svc.GetRun(ctxB, run.ID); code(err) != CodeReportNotFound {
		t.Fatalf("B lee a A: %v", err)
	}
	// No generado aún → 409.
	if _, _, _, err := e.svc.Download(ctxA, run.ID, "pdf", "", ""); code(err) != CodeReportNotReady {
		t.Fatalf("descarga antes de generar: %v", err)
	}

	w := e.worker()
	if n := w.Drain(context.Background()); n != 1 {
		t.Fatalf("drain = %d", n)
	}
	got, err := e.svc.GetRun(ctxA, run.ID)
	if err != nil || got.Status != StatusCompleted || len(got.Files) != 2 || got.ExpiresAt == nil {
		t.Fatalf("tras generar: %+v %v", got, err)
	}
	r, f, fh, err := e.svc.Download(ctxA, run.ID, "pdf", "198.51.100.4", "test")
	if err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 5)
	_, _ = io.ReadFull(fh, head)
	_ = fh.Close()
	if string(head) != "%PDF-" || r.ID != run.ID || f.SizeBytes == 0 || len(f.SHA256) != 64 {
		t.Fatalf("descarga = %q %+v", head, f)
	}
	if _, _, _, err := e.svc.Download(ctxB, run.ID, "pdf", "", ""); code(err) != CodeReportNotFound {
		t.Fatalf("B descarga a A: %v", err)
	}
	// Auditoría por el outbox (auth no está en el proceso) y evento completed.
	var audits, completed int
	_ = e.db.Pool.QueryRow(context.Background(), `SELECT count(*) FILTER (WHERE subject LIKE 'horus.analytics.audit.recorded.%'),
		count(*) FILTER (WHERE subject = 'horus.reporting.report.completed.' || $1) FROM analytics.outbox`, run.ID.String()).Scan(&audits, &completed)
	if audits != 1 || completed != 1 {
		t.Fatalf("outbox: auditorías %d, completed %d", audits, completed)
	}
	// Archivos en reports/<tenant_id>/.
	if _, err := os.Stat(filepath.Join(e.files.Root, "reports", A.String(), run.ID.String()+".pdf")); err != nil {
		t.Fatal(err)
	}
	// Listados: A ve la suya, B nada.
	rowsA, _, err := e.svc.ListRuns(ctxA, "", 20, "")
	if err != nil || len(rowsA) != 1 {
		t.Fatalf("listado A = %v %v", rowsA, err)
	}
	if rowsB, _, _ := e.svc.ListRuns(ctxB, "", 20, ""); len(rowsB) != 0 {
		t.Fatalf("listado B = %v", rowsB)
	}

	// Retención: caducado → archivos borrados y 409 REPORT_EXPIRED.
	if _, err := e.db.Pool.Exec(context.Background(), `UPDATE analytics.report_run SET expires_at = $1 WHERE id = $2`, e.now.Add(-time.Hour), run.ID); err != nil {
		t.Fatal(err)
	}
	if n := w.Purge(context.Background()); n != 1 {
		t.Fatalf("purge = %d", n)
	}
	if _, err := os.Stat(filepath.Join(e.files.Root, f.ObjectKey)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("archivo caducado sigue: %v", err)
	}
	if _, _, _, err := e.svc.Download(ctxA, run.ID, "pdf", "", ""); code(err) != CodeReportExpired {
		t.Fatalf("descarga caducada: %v", err)
	}
}

func TestReportsSchedulerIdempotentAcrossReplicas(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	A := uuid.New()
	d, err := e.st.EnsureDefinition(context.Background(), pgdb.TenantID(A), KindSecurityWeekly, "America/Mexico_City", "ISP A", "isp-a",
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || !d.Enabled || d.Weekday != 1 || d.Hour != 7 || d.Timezone != "America/Mexico_City" {
		t.Fatalf("definición por defecto = %+v %v", d, err)
	}
	// Lunes 12/10/2026 07:05 en México: varias réplicas planifican a la vez.
	e.now = time.Date(2026, 10, 12, 13, 5, 0, 0, time.UTC)
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n := e.worker().Schedule(context.Background())
			mu.Lock()
			total += n
			mu.Unlock()
		}()
	}
	wg.Wait()
	// Y otra pasada más tarde la misma semana.
	e.now = e.now.Add(48 * time.Hour)
	total += e.worker().Schedule(context.Background())
	var n int
	var from time.Time
	_ = e.db.Pool.QueryRow(context.Background(), `SELECT count(*), min(period_from) FROM analytics.report_run WHERE tenant_id = $1`, A).Scan(&n, &from)
	if total != 1 || n != 1 || !from.Equal(time.Date(2026, 10, 5, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("ejecuciones = %d (creadas %d), periodo %v", n, total, from)
	}
	// Deshabilitada → no se encola la semana siguiente.
	ctx := as(A, allPerms...)
	if _, err := e.svc.UpdateDefinition(ctx, d.ID, d.Version, map[string]json.RawMessage{"enabled": json.RawMessage(`false`)}); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(7 * 24 * time.Hour)
	if n := e.worker().Schedule(context.Background()); n != 0 {
		t.Fatalf("deshabilitada encoló %d", n)
	}
}

func TestReportDefinitionsAPI(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	A, B := uuid.New(), uuid.New()
	ctxA := as(A, allPerms...)
	defs, err := e.svc.ListDefinitions(ctxA)
	if err != nil || len(defs) != 1 || defs[0].Kind != KindSecurityWeekly || !defs[0].Enabled {
		t.Fatalf("definiciones = %+v %v", defs, err)
	}
	d := defs[0]
	patch := map[string]json.RawMessage{"weekday": json.RawMessage(`3`), "hour": json.RawMessage(`6`), "timezone": json.RawMessage(`"America/Bogota"`),
		"formats": json.RawMessage(`["csv"]`), "recipients": json.RawMessage(`["NOC@isp.example", "noc@isp.example"]`)}
	got, err := e.svc.UpdateDefinition(ctxA, d.ID, d.Version, patch)
	if err != nil || got.Weekday != 3 || got.Hour != 6 || got.Timezone != "America/Bogota" || len(got.Formats) != 1 ||
		len(got.Recipients) != 1 || got.Version != d.Version+1 {
		t.Fatalf("patch = %+v %v", got, err)
	}
	// If-Match viejo → 412.
	if _, err := e.svc.UpdateDefinition(ctxA, d.ID, d.Version, patch); code(err) != "PRECONDITION_FAILED" {
		t.Fatalf("If-Match viejo: %v", err)
	}
	// Validación.
	_, err = e.svc.UpdateDefinition(ctxA, d.ID, got.Version, map[string]json.RawMessage{"hour": json.RawMessage(`24`),
		"timezone": json.RawMessage(`"Marte/Base"`), "recipients": json.RawMessage(`["no-es-correo"]`), "formats": json.RawMessage(`["xlsx"]`)})
	if ae, ok := apperr.As(err); !ok || ae.Code != "VALIDATION_FAILED" || len(ae.Fields) != 4 {
		t.Fatalf("validación: %v", err)
	}
	// Otro ISP → 404; sin settings.manage → 403.
	if _, err := e.svc.UpdateDefinition(as(B, allPerms...), d.ID, got.Version, patch); code(err) != CodeDefinitionNotFound {
		t.Fatalf("B sobre A: %v", err)
	}
	if _, err := e.svc.UpdateDefinition(as(A, PermRead), d.ID, got.Version, patch); code(err) != "PERMISSION_DENIED" {
		t.Fatalf("sin settings.manage: %v", err)
	}
	// Permiso limitado a un nodo → 403 (el reporte cubre todo el ISP).
	p := authz.FromContext(as(A, PermRead))
	p.Perms[PermRead] = []string{"site:" + uuid.NewString()}
	if _, err := e.svc.ListDefinitions(authz.WithPrincipal(context.Background(), p)); code(err) != "PERMISSION_DENIED" {
		t.Fatalf("alcance de nodo: %v", err)
	}
}

func TestReportsRateLimitPerUser(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	A := uuid.New()
	ctx := as(A, allPerms...)
	for i := 0; i < RunsPerHour; i++ {
		if _, _, err := e.svc.CreateRun(ctx, CreateInput{}, uuid.New()); err != nil {
			t.Fatal(err)
		}
		e.now = e.now.Add(time.Minute)
	}
	_, _, err := e.svc.CreateRun(ctx, CreateInput{}, uuid.New())
	if ae, ok := apperr.As(err); !ok || ae.Code != "RATE_LIMITED" || ae.RetryAfter < 1 {
		t.Fatalf("11.º reporte en una hora: %v", err)
	}
	// Rango a petición inválido → 422.
	from, to := e.now.Add(-40*24*time.Hour), e.now
	e.now = e.now.Add(2 * time.Hour)
	if _, _, err := e.svc.CreateRun(ctx, CreateInput{From: &from, To: &to}, uuid.New()); code(err) != "VALIDATION_FAILED" {
		t.Fatalf("rango de 40 días: %v", err)
	}
}

func TestReportFailsWithoutLeakingAndExhausts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	A := uuid.New()
	ctx := as(A, allPerms...)
	run, _, err := e.svc.CreateRun(ctx, CreateInput{Formats: []string{"pdf"}}, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	// El almacén no admite escritura → failed con mensaje genérico.
	w := e.worker()
	ro := filepath.Join(t.TempDir(), "ro")
	if err := os.WriteFile(ro, []byte("x"), 0o400); err != nil {
		t.Fatal(err)
	}
	w.Files = Files{Root: ro} // un archivo, no un directorio: MkdirAll falla
	w.Drain(context.Background())
	got, _ := e.svc.GetRun(ctx, run.ID)
	if got.Status != StatusFailed || got.Error == nil || len(got.Files) != 0 {
		t.Fatalf("fallo = %+v", got)
	}
	var failed int
	_ = e.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM analytics.outbox WHERE subject = 'horus.reporting.report.failed.' || $1`,
		run.ID.String()).Scan(&failed)
	if failed != 1 {
		t.Fatal("sin evento failed")
	}
	// Una ejecución cuyo worker murió varias veces termina failed.
	run2, _, _ := e.svc.CreateRun(ctx, CreateInput{}, uuid.New())
	if _, err := e.db.Pool.Exec(context.Background(), `UPDATE analytics.report_run SET status = 'running', attempts = 3,
		claimed_until = $1 WHERE id = $2`, e.now.Add(-time.Minute), run2.ID); err != nil {
		t.Fatal(err)
	}
	e.worker().Drain(context.Background())
	if got, _ := e.svc.GetRun(ctx, run2.ID); got.Status != StatusFailed {
		t.Fatalf("agotada = %+v", got)
	}
}

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }
