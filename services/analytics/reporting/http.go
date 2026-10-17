package reporting

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/clientip"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
)

// Handler sirve la API de reportes (analytics.yaml, tag reports).
type Handler struct {
	Svc     *Service
	Guard   *authz.Guard
	BaseURL string
	Logger  *slog.Logger
}

// Mount registra las rutas.
func (h *Handler) Mount(r *httpx.ServiceMux) {
	tenant := func(f http.HandlerFunc) http.Handler {
		return h.Guard.Wrap(authz.Requirement{Scope: authz.ScopeTenant}, f)
	}
	r.Handle("GET /api/v1/reports", tenant(h.list))
	r.Handle("POST /api/v1/reports", tenant(h.create))
	r.Handle("GET /api/v1/reports/{report_id}", tenant(h.get))
	r.Handle("GET /api/v1/reports/{report_id}/download", tenant(h.download))
	r.Handle("GET /api/v1/report-definitions", tenant(h.listDefinitions))
	r.Handle("PATCH /api/v1/report-definitions/{definition_id}", tenant(h.updateDefinition))
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if e, ok := apperr.As(err); ok {
		if d, ok := e.Current.(*Definition); ok {
			e.Current = h.definitionJSON(d)
		}
		if e.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(e.RetryAfter))
		}
	}
	apperr.WriteHTTP(w, r, h.Logger, err)
}

func fmtTS(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func tsPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return fmtTS(*t)
}

// RunJSON es la representación ReportRun del contrato para quien la pide:
// files, size_bytes y personal_data_masked describen la variante que esa
// persona descargaría (MaskedFor).
func RunJSON(r *Run, masked bool) map[string]any {
	variant := r.VariantFiles(masked)
	files := make([]map[string]any, 0, len(variant))
	for _, f := range variant {
		files = append(files, map[string]any{"format": f.Format, "size_bytes": f.SizeBytes, "sha256": f.SHA256,
			"download_path": "/api/v1/reports/" + r.ID.String() + "/download?format=" + f.Format})
	}
	var def, by any
	if r.DefinitionID != nil {
		def = r.DefinitionID.String()
	}
	if r.CreatedBy != nil {
		by = r.CreatedBy.String()
	}
	y, wk := r.Period().ISOWeek()
	return map[string]any{
		"id": r.ID, "tenant_id": r.TenantID, "kind": r.Kind, "status": r.Status, "definition_id": def, "scheduled": r.DefinitionID != nil,
		"period_from": fmtTS(r.PeriodFrom), "period_to": fmtTS(r.PeriodTo), "timezone": r.Timezone, "iso_week": fmt.Sprintf("%d-W%02d", y, wk),
		"formats": r.Formats, "files": files, "size_bytes": sumSize(variant), "personal_data_masked": masked, "error": r.Error, "created_by": by,
		"created_at": fmtTS(r.CreatedAt), "started_at": tsPtr(r.StartedAt), "completed_at": tsPtr(r.CompletedAt),
		"expires_at": tsPtr(r.ExpiresAt), "expired": r.PurgedAt != nil,
	}
}

func (h *Handler) definitionJSON(d *Definition) map[string]any {
	next := d.Schedule().Next(h.Svc.now())
	var nextRun any
	if d.Enabled {
		nextRun = fmtTS(next)
	}
	return map[string]any{
		"id": d.ID, "tenant_id": d.TenantID, "kind": d.Kind, "enabled": d.Enabled, "weekday": d.Weekday, "hour": d.Hour, "minute": d.Minute,
		"timezone": d.Timezone, "formats": d.Formats, "recipients": d.Recipients, "next_run_at": nextRun,
		"email_available": h.emailAvailable(), "version": d.Version, "created_at": fmtTS(d.CreatedAt), "updated_at": fmtTS(d.UpdatedAt),
	}
}

// emailAvailable lo fija Module según HORUS_SMTP_HOST (nil = desconocido → false).
func (h *Handler) emailAvailable() bool { return h.Svc.EmailAvailable }

func pathID(w http.ResponseWriter, r *http.Request, name, code string) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		problem.Std(w, r, http.StatusNotFound, code)
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			problem.Std(w, r, http.StatusBadRequest, problem.CodeValidationFailed, problem.WithDetail("limit debe estar entre 1 y 200"))
			return
		}
		limit = n
	}
	rows, pg, err := h.Svc.ListRuns(r.Context(), r.URL.Query().Get("kind"), limit, r.URL.Query().Get("cursor"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	masked := MaskedCtx(r.Context())
	data := make([]map[string]any, 0, len(rows))
	for i := range rows {
		data = append(data, RunJSON(&rows[i], masked))
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": data, "page": pg})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	idem, err := uuid.Parse(r.Header.Get("Idempotency-Key"))
	if err != nil {
		h.fail(w, r, apperr.PreconditionRequired("Idempotency-Key (UUID) es obligatoria."))
		return
	}
	var in CreateInput
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	run, replayed, err := h.Svc.CreateRun(r.Context(), in, idem)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.SetLocation(w, h.BaseURL, "/api/v1/reports/"+run.ID.String())
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	jsonapi.Write(w, http.StatusAccepted, RunJSON(run, MaskedCtx(r.Context())))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "report_id", CodeReportNotFound)
	if !ok {
		return
	}
	run, err := h.Svc.GetRun(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonapi.Write(w, http.StatusOK, RunJSON(run, MaskedCtx(r.Context())))
}

// download sirve el archivo: attachment, no-store, nosniff y el sha256 en
// Repr-Digest (RFC 9530) para que el cliente pueda verificarlo.
func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "report_id", CodeReportNotFound)
	if !ok {
		return
	}
	run, f, fh, err := h.Svc.Download(r.Context(), id, r.URL.Query().Get("format"), clientip.String(r), r.UserAgent())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer func() { _ = fh.Close() }()
	ctype, ext := "application/pdf", ".pdf"
	if f.Format == FormatCSV {
		ctype, ext = "application/zip", "-csv.zip"
	}
	if f.Masked {
		ext = "-enmascarado" + ext
	}
	y, wk := run.Period().ISOWeek()
	name := fmt.Sprintf("horus-seguridad-%d-W%02d-%s%s", y, wk, run.ID.String()[:8], ext)
	if run.DefinitionID == nil {
		name = fmt.Sprintf("horus-seguridad-%s-%s%s", run.PeriodFrom.In(LoadLocation(run.Timezone)).Format("20060102"), run.ID.String()[:8], ext)
	}
	hd := w.Header()
	hd.Set("Content-Type", ctype)
	hd.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Content-Type-Options", "nosniff")
	variant := VariantFull
	if f.Masked {
		variant = VariantMasked
	}
	hd.Set("X-Horus-Report-Variant", variant)
	hd.Set("Content-Length", strconv.FormatInt(f.SizeBytes, 10))
	if sum, err := hex.DecodeString(f.SHA256); err == nil {
		hd.Set("Repr-Digest", "sha-256=:"+base64.StdEncoding.EncodeToString(sum)+":")
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, fh); err != nil {
		h.Logger.WarnContext(r.Context(), "reporting: download interrupted", slog.String("report_id", run.ID.String()), slog.Any("error", err))
	}
}

func (h *Handler) listDefinitions(w http.ResponseWriter, r *http.Request) {
	defs, err := h.Svc.ListDefinitions(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data := make([]map[string]any, 0, len(defs))
	for i := range defs {
		data = append(data, h.definitionJSON(&defs[i]))
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": data})
}

func (h *Handler) updateDefinition(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "definition_id", CodeDefinitionNotFound)
	if !ok {
		return
	}
	ver, err := jsonapi.IfMatch(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var patch map[string]json.RawMessage
	if !jsonapi.Decode(w, r, &patch, false) {
		return
	}
	d, err := h.Svc.UpdateDefinition(r.Context(), id, ver, patch)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("ETag", jsonapi.ETag(d.Version))
	jsonapi.Write(w, http.StatusOK, h.definitionJSON(d))
}
