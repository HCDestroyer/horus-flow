// Package httpapi sirve la API de tráfico de analytics (OpenAPI
// packages/schemas/openapi/v0/analytics.yaml): tops, series, atribución,
// tráfico de un cliente y propuestas del modo descubrimiento. Los
// dashboards y playlists son de CORE (services/analytics/**/dashboards/).
package httpapi

import (
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/analytics/internal/adapters/chread"
	"github.com/hcdestroyer/horus-flow/services/analytics/internal/app"
)

// Códigos de error propios (api.md §1.5).
const (
	CodeAnalyticsUnavailable = "ANALYTICS_UNAVAILABLE"
	CodeTimeRangeTooLarge    = "TIME_RANGE_TOO_LARGE"
)

// Meta es AnalyticsMeta.
type Meta struct {
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Step     *int      `json:"step"`
	Source   string    `json:"source,omitempty"`
	Partial  bool      `json:"partial"`
	Coverage float64   `json:"coverage"`
}

// Handler sirve la API.
type Handler struct {
	guard *authz.Guard
	svc   *app.Service
}

// New crea el handler.
func New(guard *authz.Guard, svc *app.Service) *Handler { return &Handler{guard: guard, svc: svc} }

// Mount registra las rutas.
func (h *Handler) Mount(r *httpx.ServiceMux) {
	r.Handle("GET /api/v1/sites/{site_id}/prefix-proposals", h.guard.Tenant("sites.read", h.prefixProposals))
	h.mountTraffic(r)
}

func tenantOf(r *http.Request) uuid.UUID {
	if p := authz.FromContext(r.Context()); p != nil {
		return p.TenantID
	}
	return uuid.Nil
}

// siteAllowed aplica el alcance por nodo del permiso (ACL dentro del tenant).
func siteAllowed(r *http.Request, perm string, site uuid.UUID) bool {
	p := authz.FromContext(r.Context())
	scopes := p.SiteScopes(perm)
	return p.TenantWide(perm) || slices.Contains(scopes, site)
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, app.ErrRangeTooLarge):
		problem.Write(w, r, http.StatusUnprocessableEntity, CodeTimeRangeTooLarge, "El rango pedido es demasiado grande")
	case errors.Is(err, app.ErrRange), errors.Is(err, app.ErrBadRequest):
		problem.Std(w, r, http.StatusUnprocessableEntity, problem.CodeValidationFailed, problem.WithDetail(err.Error()))
	case errors.Is(err, app.ErrNotFound):
		problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
	case errors.Is(err, chread.ErrUnavailable), errors.Is(err, app.ErrNoBackend):
		w.Header().Set("Retry-After", "30")
		problem.Write(w, r, http.StatusServiceUnavailable, CodeAnalyticsUnavailable, "La analítica no está disponible")
	default:
		problem.Std(w, r, http.StatusInternalServerError, problem.CodeInternal)
	}
}

func (h *Handler) prefixProposals(w http.ResponseWriter, r *http.Request) {
	site, err := uuid.Parse(r.PathValue("site_id"))
	if err != nil || !siteAllowed(r, "sites.read", site) {
		problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
		return
	}
	tenant := tenantOf(r)
	if inv := h.svc.Inv.Load(); !inv.SiteOf(tenant, site) {
		problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
		return
	}
	rg, err := app.ParseRange(r.URL.Query().Get("range"), "", "", "24h", time.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out, err := h.svc.PrefixProposals(r.Context(), tenant, site, rg)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	step := 3600
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": out,
		"meta": Meta{From: rg.From, To: rg.To, Step: &step, Source: "flows", Coverage: 1}})
}
