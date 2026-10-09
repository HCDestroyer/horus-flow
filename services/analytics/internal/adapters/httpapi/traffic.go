package httpapi

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/analytics/internal/app"
)

// Permisos (packages/schemas/openapi/v0/analytics.yaml).
const (
	PermTrafficRead         = "traffic.read"
	PermTrafficCustomerRead = "traffic.customer.read"
	PermCustomersRead       = "customers.read"
)

// mountTraffic registra la API de tráfico (I1-08).
func (h *Handler) mountTraffic(r *httpx.ServiceMux) {
	r.Handle("GET /api/v1/analytics/traffic/top", h.guard.Tenant(PermTrafficRead, h.top))
	r.Handle("GET /api/v1/analytics/traffic/timeseries", h.guard.Tenant(PermTrafficRead, h.timeseries))
	r.Handle("GET /api/v1/analytics/traffic/attribution", h.guard.Tenant(PermTrafficRead, h.attribution))
	r.Handle("GET /api/v1/analytics/customers/{customer_id}/traffic", h.guard.Tenant(PermTrafficCustomerRead, h.customerTraffic))
}

// scope resuelve tenant y nodos: site_id (multivalor) ∩ alcance del permiso.
// ok=false si se pide un nodo fuera del alcance (404).
func (h *Handler) scope(r *http.Request, perm string) (app.Scope, bool) {
	p := authz.FromContext(r.Context())
	sc := app.Scope{Tenant: p.TenantID}
	allowed := p.SiteScopes(perm)
	var asked []uuid.UUID
	if s := r.URL.Query().Get("site_id"); s != "" {
		for _, part := range strings.Split(s, ",") {
			id, err := uuid.Parse(strings.TrimSpace(part))
			if err != nil {
				return sc, false
			}
			// Un nodo de otro ISP (o inexistente) es 404, sin distinguir.
			if !h.svc.Inv.Load().SiteOf(p.TenantID, id) {
				return sc, false
			}
			asked = append(asked, id)
		}
	}
	switch {
	case len(asked) > 0 && len(allowed) > 0:
		for _, a := range asked {
			if !slices.Contains(allowed, a) {
				return sc, false
			}
		}
		sc.Sites = asked
	case len(asked) > 0:
		sc.Sites = asked
	case len(allowed) > 0:
		sc.Sites = allowed
	}
	return sc, true
}

func u64(v uint64) string { return strconv.FormatUint(v, 10) }

// MaskIP enmascara una IP de cliente (10.20.0.••• / 2001:db8:1000:••••::).
func MaskIP(s string) string {
	if i := strings.LastIndexByte(s, '.'); i > 0 && !strings.Contains(s, ":") {
		return s[:i] + ".•••"
	}
	parts := strings.Split(strings.TrimSuffix(s, "::"), ":")
	if len(parts) > 3 {
		parts = parts[:3]
	}
	return strings.Join(parts, ":") + ":••••::"
}

func (h *Handler) top(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := authz.FromContext(r.Context())
	dim := q.Get("dimension")
	if dim == app.DimCustomers && !p.Has(PermCustomersRead) {
		problem.Std(w, r, http.StatusForbidden, problem.CodePermissionDenied)
		return
	}
	sc, ok := h.scope(r, PermTrafficRead)
	if !ok {
		problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
		return
	}
	rg, err := app.ParseRange(q.Get("range"), q.Get("from"), q.Get("to"), "24h", time.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if id := q.Get("customer_id"); id != "" {
		if !p.Has(PermTrafficCustomerRead) {
			problem.Std(w, r, http.StatusForbidden, problem.CodePermissionDenied)
			return
		}
		cid, err := uuid.Parse(id)
		if err != nil {
			problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
			return
		}
		c, err := h.svc.Customer(r.Context(), sc.Tenant, cid)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		sc.Customer = c
	}
	n, _ := strconv.Atoi(q.Get("n"))
	if n == 0 {
		n = 10
	}
	if n < 1 || n > 10 {
		problem.Std(w, r, http.StatusBadRequest, problem.CodeValidationFailed, problem.WithDetail("n must be 1..10"))
		return
	}
	res, err := h.svc.Top(r.Context(), app.TopQuery{Scope: sc, Dimension: dim, Direction: q.Get("direction"), N: n, Range: rg})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	rows := make([]map[string]any, 0, len(res.Rows))
	for _, row := range res.Rows {
		m := map[string]any{"key": row.Key, "label": row.Label, "down_bytes": u64(row.Down), "up_bytes": u64(row.Up), "share": row.Share}
		if c := row.Customer; c != nil {
			m["customer"] = map[string]any{"customer_id": c.ID, "address": c.Address.String(), "address_masked": false,
				"alias": c.Alias, "kind": c.Kind}
			m["label"] = c.Address.String()
			if c.Alias != nil {
				m["label"] = *c.Alias
			}
		}
		rows = append(rows, m)
	}
	step := int(Pick(rg).Seconds())
	jsonapi.Write(w, http.StatusOK, map[string]any{
		"data": map[string]any{"dimension": dim, "rows": rows,
			"others": map[string]string{"down_bytes": u64(res.OthersDown), "up_bytes": u64(res.OthersUp)},
			"totals": map[string]string{"down_bytes": u64(res.TotalDown), "up_bytes": u64(res.TotalUp)}},
		"meta": Meta{From: rg.From, To: rg.To, Step: &step, Source: "flows", Coverage: 1},
	})
}

// Pick devuelve el paso de los agregados que usa un rango.
func Pick(rg app.Range) time.Duration { return app.Pick(rg, 0).Step }

func seriesJSON(res *app.SeriesResult) map[string]any {
	series := make([]map[string]any, 0, len(res.Series))
	for _, s := range res.Series {
		pts := make([][2]any, 0, len(s.Points))
		for _, p := range s.Points {
			var v any
			if p.Value != nil {
				v = *p.Value
			}
			pts = append(pts, [2]any{p.T.UTC().Format(time.RFC3339), v})
		}
		series = append(series, map[string]any{"metric": s.Metric, "unit": s.Unit, "group": s.Group, "points": pts})
	}
	step := int(res.Step.Seconds())
	return map[string]any{"data": map[string]any{"series": series},
		"meta": Meta{From: res.Range.From, To: res.Range.To, Step: &step, Source: "flows", Partial: res.Partial, Coverage: res.Coverage}}
}

func parseStep(s string) (time.Duration, bool) {
	if s == "" {
		return 0, true
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 60 {
		return 0, false
	}
	return time.Duration(n) * time.Second, true
}

func (h *Handler) timeseries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sc, ok := h.scope(r, PermTrafficRead)
	if !ok {
		problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
		return
	}
	rg, err := app.ParseRange(q.Get("range"), q.Get("from"), q.Get("to"), "24h", time.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	step, ok := parseStep(q.Get("step"))
	if !ok {
		problem.Std(w, r, http.StatusUnprocessableEntity, problem.CodeValidationFailed, problem.WithDetail("step"))
		return
	}
	var metrics []string
	if m := q.Get("metrics"); m != "" {
		metrics = strings.Split(m, ",")
	}
	res, err := h.svc.Timeseries(r.Context(), sc, metrics, rg, step)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, seriesJSON(res))
}

func (h *Handler) attribution(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sc, ok := h.scope(r, PermTrafficRead)
	if !ok {
		problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
		return
	}
	rg, err := app.ParseRange(q.Get("range"), "", "", "24h", time.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	a, i, u, err := h.svc.Attribution(r.Context(), sc, rg)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{
		"data": map[string]float64{"attributed_ratio": a, "infrastructure_ratio": i, "unattributed_ratio": u},
		"meta": Meta{From: rg.From, To: rg.To, Source: "flows", Coverage: 1},
	})
}

func (h *Handler) customerTraffic(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tenant := tenantOf(r)
	id, err := uuid.Parse(r.PathValue("customer_id"))
	if err != nil {
		problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
		return
	}
	c, err := h.svc.Customer(r.Context(), tenant, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if !siteAllowed(r, PermTrafficCustomerRead, c.SiteID) {
		problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
		return
	}
	rg, err := app.ParseRange(q.Get("range"), q.Get("from"), q.Get("to"), "24h", time.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	step, ok := parseStep(q.Get("step"))
	if !ok {
		problem.Std(w, r, http.StatusUnprocessableEntity, problem.CodeValidationFailed, problem.WithDetail("step"))
		return
	}
	res, err := h.svc.CustomerTraffic(r.Context(), tenant, c, q.Get("group_by"), rg, step)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, seriesJSON(res))
}
