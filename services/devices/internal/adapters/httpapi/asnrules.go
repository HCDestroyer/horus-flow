package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// AsnRulesHandler sirve /asn-rules: ASN propios del ISP (nombre, categoría y
// servicio que flows aplica al tráfico nuevo).
type AsnRulesHandler struct {
	svc     *app.AsnRules
	guard   *authz.Guard
	baseURL string
	logger  *slog.Logger
}

// NewAsnRules crea el handler.
func NewAsnRules(svc *app.AsnRules, guard *authz.Guard, baseURL string, logger *slog.Logger) *AsnRulesHandler {
	return &AsnRulesHandler{svc: svc, guard: guard, baseURL: baseURL, logger: logger}
}

// Mount registra las rutas.
func (h *AsnRulesHandler) Mount(r *httpx.ServiceMux) {
	r.Handle("GET /api/v1/asn-rules", h.guard.Tenant(app.PermSettingsRead, h.list))
	r.Handle("POST /api/v1/asn-rules", h.guard.Tenant(app.PermSettingsManage, h.create))
	r.Handle("GET /api/v1/asn-rules/catalog", h.guard.Tenant(app.PermSettingsRead, h.catalog))
	r.Handle("PATCH /api/v1/asn-rules/{asn_rule_id}", h.guard.Tenant(app.PermSettingsManage, h.update))
	r.Handle("DELETE /api/v1/asn-rules/{asn_rule_id}", h.guard.Tenant(app.PermSettingsManage, h.remove))
}

func asnRuleJSON(r *domain.AsnRule) map[string]any {
	return map[string]any{
		"id": r.ID, "tenant_id": r.TenantID, "version": r.Version, "created_at": ts(r.CreatedAt), "updated_at": ts(r.UpdatedAt),
		"asn": int64(r.ASN), "name": r.Name, "category": r.Category, "service": r.Service, "note": r.Note,
	}
}

func (h *AsnRulesHandler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if e, ok := apperr.As(err); ok && e.Current != nil {
		if c, ok := e.Current.(*domain.AsnRule); ok {
			e.Current = asnRuleJSON(c)
		}
	}
	apperr.WriteHTTP(w, r, h.logger, err)
}

func (h *AsnRulesHandler) list(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.List(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": listJSON(rows, asnRuleJSON)})
}

func (h *AsnRulesHandler) catalog(w http.ResponseWriter, r *http.Request) {
	cats, svcs, err := h.svc.Catalog(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"categories": cats, "services": svcs})
}

func (h *AsnRulesHandler) create(w http.ResponseWriter, r *http.Request) {
	var f app.Fields
	if !jsonapi.Decode(w, r, &f, false) {
		return
	}
	x, err := h.svc.Create(r.Context(), f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.SetLocation(w, h.baseURL, "/api/v1/asn-rules/"+x.ID.String())
	writeItem(w, http.StatusCreated, x.Version, asnRuleJSON(x))
}

func (h *AsnRulesHandler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "asn_rule_id", domain.CodeAsnRuleNotFound)
	if !ok {
		return
	}
	ver, err := jsonapi.IfMatch(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var f app.Fields
	if !jsonapi.Decode(w, r, &f, false) {
		return
	}
	x, err := h.svc.Update(r.Context(), id, ver, f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeItem(w, http.StatusOK, x.Version, asnRuleJSON(x))
}

func (h *AsnRulesHandler) remove(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "asn_rule_id", domain.CodeAsnRuleNotFound)
	if !ok {
		return
	}
	ver, err := jsonapi.IfMatch(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.svc.Delete(r.Context(), id, ver); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
