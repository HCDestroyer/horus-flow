package httpapi

import (
	"net/http"
	"strconv"

	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
)

// mountASN registra GET /asn/{asn}: cómo se llama hoy un ASN (Administración › ASN propios).
func (h *Handler) mountASN(r *httpx.ServiceMux) {
	r.Handle("GET /api/v1/asn/{asn}", h.guard.Tenant(PermTrafficRead, h.asnInfo))
}

func (h *Handler) asnInfo(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.ParseUint(r.PathValue("asn"), 10, 32)
	if err != nil || n == 0 {
		problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
		return
	}
	info := h.svc.LookupASN(tenantOf(r), uint32(n))
	body := map[string]any{"asn": n, "name": nil, "country": nil, "catalog_organization": nil, "tenant_rule": nil}
	if info.Name != "" {
		body["name"] = info.Name
	}
	if info.Country != "" {
		body["country"] = info.Country
	}
	if o := info.CatalogOrg; o != nil {
		body["catalog_organization"] = map[string]any{"slug": o.Slug, "name": o.Name}
	}
	if rule := info.Rule; rule != nil {
		var svc any
		if rule.Service != "" {
			svc = rule.Service
		}
		body["tenant_rule"] = map[string]any{"name": rule.Name, "category": rule.Category, "service": svc}
	}
	jsonapi.Write(w, http.StatusOK, body)
}
