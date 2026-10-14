package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/clientip"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/observability"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/app"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// PermSNMPManage edita los umbrales de los routers del ISP.
const PermSNMPManage = "snmp.manage"

// ThresholdService es lo que el handler necesita de los umbrales por ISP.
type ThresholdService interface {
	Get(ctx context.Context, tenant uuid.UUID) (app.TenantThresholds, error)
	Put(ctx context.Context, tenant uuid.UUID, v domain.ThresholdValues, actor string, expect int) (app.TenantThresholds, error)
	Reset(ctx context.Context, tenant uuid.UUID) (app.TenantThresholds, error)
}

// MountThresholds registra GET/PUT/DELETE /routers/thresholds.
func (h *Handler) MountThresholds(r *httpx.ServiceMux, th ThresholdService) {
	h.th = th
	r.Handle("GET /api/v1/routers/thresholds", h.guard.Tenant(PermSNMPRead, h.getThresholds))
	r.Handle("PUT /api/v1/routers/thresholds", h.guard.Tenant(PermSNMPManage, h.putThresholds))
	r.Handle("DELETE /api/v1/routers/thresholds", h.guard.Tenant(PermSNMPManage, h.resetThresholds))
}

func writeThresholds(w http.ResponseWriter, status int, t app.TenantThresholds) {
	w.Header().Set("ETag", jsonapi.ETag(t.Version))
	jsonapi.Write(w, status, t)
}

func (h *Handler) thresholdsFail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, app.ErrNoStore):
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable,
			problem.WithDetail("Umbrales por ISP no disponibles: el rol snmp no tiene PostgreSQL (HORUS_POSTGRES_DSN)."))
	case errors.Is(err, app.ErrThresholdVersion):
		problem.Std(w, r, http.StatusPreconditionFailed, problem.CodePreconditionFailed)
	default:
		h.log.ErrorContext(r.Context(), "snmp: thresholds", "error", err)
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable)
	}
}

func (h *Handler) getThresholds(w http.ResponseWriter, r *http.Request) {
	tenant := authz.FromContext(r.Context()).TenantID
	t, err := h.th.Get(r.Context(), tenant)
	if err != nil {
		h.thresholdsFail(w, r, err)
		return
	}
	writeThresholds(w, http.StatusOK, t)
}

func (h *Handler) putThresholds(w http.ResponseWriter, r *http.Request) {
	p := authz.FromContext(r.Context())
	expect := 0
	if r.Header.Get("If-Match") != "" {
		v, err := jsonapi.IfMatch(r)
		if err != nil {
			problem.Std(w, r, http.StatusBadRequest, problem.CodeValidationFailed, problem.WithDetail("If-Match"))
			return
		}
		expect = v
	}
	var in domain.ThresholdValues
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	if errs := in.Validate(); len(errs) > 0 {
		fields := make([]problem.FieldError, 0, len(errs))
		for _, e := range errs {
			fields = append(fields, problem.FieldError{Field: e.Field, Code: "OUT_OF_RANGE", Message: e.Message})
		}
		problem.Std(w, r, http.StatusUnprocessableEntity, problem.CodeValidationFailed, problem.WithErrors(fields...))
		return
	}
	before, _ := h.th.Get(r.Context(), p.TenantID)
	t, err := h.th.Put(r.Context(), p.TenantID, in, p.ActorID(), expect)
	if err != nil {
		h.thresholdsFail(w, r, err)
		return
	}
	h.recordThresholds(r, "snmp.thresholds.updated", map[string]any{"before": before.ThresholdValues, "after": t.ThresholdValues})
	writeThresholds(w, http.StatusOK, t)
}

func (h *Handler) resetThresholds(w http.ResponseWriter, r *http.Request) {
	p := authz.FromContext(r.Context())
	t, err := h.th.Reset(r.Context(), p.TenantID)
	if err != nil {
		h.thresholdsFail(w, r, err)
		return
	}
	h.recordThresholds(r, "snmp.thresholds.reset", map[string]any{"after": t.ThresholdValues})
	writeThresholds(w, http.StatusOK, t)
}

func (h *Handler) recordThresholds(r *http.Request, action string, changes map[string]any) {
	rec, ok := h.audit()
	if !ok {
		return
	}
	p := authz.FromContext(r.Context())
	e := authapi.AuditEntry{TenantID: p.TenantID, OccurredAt: h.now(), ActorType: p.Type, ActorID: p.ActorID(), ViaPlatform: p.ViaPlatform,
		Action: action, ResourceType: "router_thresholds", ResourceID: p.TenantID.String(), Scope: "tenant", Outcome: "success",
		IP: clientip.String(r), UserAgent: r.UserAgent(), Changes: changes, RequestID: observability.RequestIDFrom(r.Context())}
	if err := rec.Record(r.Context(), e); err != nil {
		h.log.WarnContext(r.Context(), "snmp: audit not recorded", "error", err, "action", action)
	}
}

