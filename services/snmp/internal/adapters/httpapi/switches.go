package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/clientip"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/observability"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/app"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// CodeSwitchNotFound: el equipo no es un switch del ISP (o no se ve).
const CodeSwitchNotFound = "SWITCH_NOT_FOUND"

// SwitchHandler sirve /switches/* (snmp.yaml, tag switches).
type SwitchHandler struct {
	svc   *app.Switches
	guard *authz.Guard
	audit func() (authapi.AuditRecorder, bool)
	log   *slog.Logger
	now   func() time.Time
}

// NewSwitches crea el handler.
func NewSwitches(svc *app.Switches, guard *authz.Guard, audit func() (authapi.AuditRecorder, bool), log *slog.Logger) *SwitchHandler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if audit == nil {
		audit = func() (authapi.AuditRecorder, bool) { return nil, false }
	}
	return &SwitchHandler{svc: svc, guard: guard, audit: audit, log: log, now: time.Now}
}

// Mount registra las rutas.
func (h *SwitchHandler) Mount(r *httpx.ServiceMux) {
	read := authz.Requirement{Scope: authz.ScopeTenant, AllowKiosk: true}
	r.Handle("GET /api/v1/switches/status", h.guard.Wrap(read, http.HandlerFunc(h.list)))
	r.Handle("GET /api/v1/switches/{router_id}/ports", h.guard.Tenant(PermDevicesRead, h.ports))
	r.Handle("PUT /api/v1/switches/{router_id}/ports", h.guard.Tenant(app.PermSNMPManage, h.putPorts))
	r.Handle("DELETE /api/v1/switches/{router_id}/ports", h.guard.Tenant(app.PermSNMPManage, h.resetPorts))
	r.Handle("GET /api/v1/switches/{router_id}/neighbors", h.guard.Tenant(PermDevicesRead, h.neighbors))
	r.Handle("GET /api/v1/switches/{router_id}/flows/top", h.guard.Tenant(PermSNMPRead, h.top))
}

func (h *SwitchHandler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, app.ErrSwitchNotFound):
		problem.Write(w, r, http.StatusNotFound, CodeSwitchNotFound, "Switch no encontrado")
	case errors.Is(err, app.ErrSwitchNoStore):
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable,
			problem.WithDetail("Elección de puertos no disponible: el rol snmp no tiene PostgreSQL (HORUS_POSTGRES_DSN)."))
	case errors.Is(err, app.ErrSwitchNoFlows):
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable,
			problem.WithDetail("Muestras sFlow no disponibles: el rol snmp no tiene ClickHouse."))
	case errors.Is(err, app.ErrSwitchVersion):
		problem.Std(w, r, http.StatusPreconditionFailed, problem.CodePreconditionFailed)
	default:
		h.log.ErrorContext(r.Context(), "snmp: switches", "error", err)
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable)
	}
}

func (h *SwitchHandler) list(w http.ResponseWriter, r *http.Request) {
	tenant, sites, ok := visible(r, PermDevicesRead)
	if !ok {
		problem.Std(w, r, http.StatusForbidden, problem.CodePermissionDenied)
		return
	}
	if s := r.URL.Query().Get("site_id"); s != "" {
		id, err := uuid.Parse(s)
		if err != nil {
			problem.Std(w, r, http.StatusBadRequest, problem.CodeValidationFailed, problem.WithDetail("site_id"))
			return
		}
		if len(sites) > 0 && !slices.Contains(sites, id) {
			h.writeList(w, nil, api.PollerHealth{IntervalSeconds: api.DefaultIntervalSeconds, Enabled: true})
			return
		}
		sites = []uuid.UUID{id}
	}
	if h.svc.Engine == nil {
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable)
		return
	}
	rows, poller, err := h.svc.Engine.ListSwitches(r.Context(), tenant, sites)
	if err != nil {
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable)
		return
	}
	h.writeList(w, rows, poller)
}

func (h *SwitchHandler) writeList(w http.ResponseWriter, rows []api.SwitchStatus, poller api.PollerHealth) {
	if rows == nil {
		rows = []api.SwitchStatus{}
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": rows,
		"meta": map[string]any{"poller": poller, "generated_at": h.now().UTC().Format(time.RFC3339)}})
}

// target resuelve el switch de la ruta en el tenant y el alcance por nodo.
func (h *SwitchHandler) target(w http.ResponseWriter, r *http.Request, perm string) (uuid.UUID, []uuid.UUID, uuid.UUID, bool) {
	p := authz.FromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("router_id"))
	if err != nil || h.svc.Engine == nil {
		problem.Write(w, r, http.StatusNotFound, CodeSwitchNotFound, "Switch no encontrado")
		return uuid.Nil, nil, uuid.Nil, false
	}
	sites := p.SiteScopes(perm)
	_, site, ok := h.svc.Engine.SwitchPortNames(p.TenantID, id)
	if !ok || (len(sites) > 0 && !slices.Contains(sites, site)) {
		problem.Write(w, r, http.StatusNotFound, CodeSwitchNotFound, "Switch no encontrado")
		return uuid.Nil, nil, uuid.Nil, false
	}
	return p.TenantID, sites, id, true
}

func (h *SwitchHandler) writePorts(w http.ResponseWriter, out *api.SwitchPorts) {
	if out.Selection.Version != nil {
		w.Header().Set("ETag", jsonapi.ETag(*out.Selection.Version))
	}
	jsonapi.Write(w, http.StatusOK, out)
}

func (h *SwitchHandler) ports(w http.ResponseWriter, r *http.Request) {
	tenant, _, id, ok := h.target(w, r, PermDevicesRead)
	if !ok {
		return
	}
	out, found, err := h.svc.Engine.SwitchPorts(r.Context(), tenant, id)
	if err != nil || !found {
		h.fail(w, r, errors.Join(app.ErrSwitchNotFound, err))
		return
	}
	h.writePorts(w, out)
}

func (h *SwitchHandler) putPorts(w http.ResponseWriter, r *http.Request) {
	tenant, sites, id, ok := h.target(w, r, app.PermSNMPManage)
	if !ok {
		return
	}
	expect, ok := ifMatch(w, r)
	if !ok {
		return
	}
	var in app.PortSelectionInput
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	p := authz.FromContext(r.Context())
	out, sel, errs, err := h.svc.PutPorts(r.Context(), tenant, sites, id, in, p.ActorID(), expect)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if invalid(w, r, errs) {
		return
	}
	ports := make([]map[string]any, 0, len(sel.Ports))
	for name, c := range sel.Ports {
		ports = append(ports, map[string]any{"name": name, "uplink": c.Uplink})
	}
	slices.SortFunc(ports, func(a, b map[string]any) int {
		switch {
		case a["name"].(string) < b["name"].(string):
			return -1
		case a["name"].(string) > b["name"].(string):
			return 1
		}
		return 0
	})
	h.record(r, "snmp.switch_ports.updated", id, map[string]any{"ports": ports, "version": sel.Version})
	h.writePorts(w, out)
}

func (h *SwitchHandler) resetPorts(w http.ResponseWriter, r *http.Request) {
	tenant, sites, id, ok := h.target(w, r, app.PermSNMPManage)
	if !ok {
		return
	}
	out, err := h.svc.ResetPorts(r.Context(), tenant, sites, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.record(r, "snmp.switch_ports.reset", id, map[string]any{"selection": "default"})
	h.writePorts(w, out)
}

func (h *SwitchHandler) neighbors(w http.ResponseWriter, r *http.Request) {
	tenant, _, id, ok := h.target(w, r, PermDevicesRead)
	if !ok {
		return
	}
	out, found, err := h.svc.Engine.SwitchNeighbors(r.Context(), tenant, id)
	if err != nil || !found {
		h.fail(w, r, errors.Join(app.ErrSwitchNotFound, err))
		return
	}
	jsonapi.Write(w, http.StatusOK, out)
}

var topDims = []string{domain.SwitchTopSrc, domain.SwitchTopDst, domain.SwitchTopProtocol}

func (h *SwitchHandler) top(w http.ResponseWriter, r *http.Request) {
	tenant, sites, id, ok := h.target(w, r, PermSNMPRead)
	if !ok {
		return
	}
	q := r.URL.Query()
	by := q.Get("by")
	if by == "" {
		by = domain.SwitchTopSrc
	}
	if !slices.Contains(topDims, by) {
		problem.Std(w, r, http.StatusBadRequest, problem.CodeValidationFailed,
			problem.WithErrors(problem.FieldError{Field: "by", Code: "INVALID_VALUE", Message: "src_ip, dst_ip o protocol."}))
		return
	}
	ifIndex, ok := intParam(w, r, "if_index", 0, 0, 1<<31-1)
	if !ok {
		return
	}
	minutes, ok := intParam(w, r, "window_minutes", 60, 5, 7*24*60)
	if !ok {
		return
	}
	limit, ok := intParam(w, r, "limit", 10, 1, 50)
	if !ok {
		return
	}
	out, err := h.svc.Top(r.Context(), tenant, sites, id, ifIndex, by, time.Duration(minutes)*time.Minute, limit)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, out)
}

func (h *SwitchHandler) record(r *http.Request, action string, id uuid.UUID, changes map[string]any) {
	rec, ok := h.audit()
	if !ok {
		return
	}
	p := authz.FromContext(r.Context())
	e := authapi.AuditEntry{TenantID: p.TenantID, OccurredAt: h.now(), ActorType: p.Type, ActorID: p.ActorID(), ViaPlatform: p.ViaPlatform,
		Action: action, ResourceType: "router", ResourceID: id.String(), Scope: "tenant", Outcome: "success", IP: clientip.String(r),
		UserAgent: r.UserAgent(), Changes: changes, RequestID: observability.RequestIDFrom(r.Context())}
	if err := rec.Record(r.Context(), e); err != nil {
		h.log.WarnContext(r.Context(), "snmp: audit not recorded", "error", err, "action", action)
	}
}
