// Package httpapi sirve la API REST del rol ingester: estado de los
// exportadores de flujos (I1-09, packages/schemas/openapi/v0/flows.yaml).
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	collectorapi "github.com/hcdestroyer/horus-flow/services/collector/api"
)

// PermFlowsRead es el permiso de las rutas (x-permission: flows.read).
const PermFlowsRead = "flows.read"

// States lee el estado que el collector guarda en NATS KV.
type States interface {
	Get(ctx context.Context, routerID uuid.UUID) (*collectorapi.FlowExporter, error)
}

// KVStates adapta el bucket flow_exporter_state.
type KVStates struct{ KV jetstream.KeyValue }

// Get implementa States (nil, nil si no hay estado).
func (k KVStates) Get(ctx context.Context, id uuid.UUID) (*collectorapi.FlowExporter, error) {
	e, err := k.KV.Get(ctx, id.String())
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var fe collectorapi.FlowExporter
	if err := json.Unmarshal(e.Value(), &fe); err != nil {
		return nil, err
	}
	return &fe, nil
}

// FlowExporter es la respuesta (esquema OpenAPI FlowExporter).
type FlowExporter struct {
	TenantID              uuid.UUID  `json:"tenant_id"`
	RouterID              uuid.UUID  `json:"router_id"`
	SiteID                uuid.UUID  `json:"site_id"`
	Version               int        `json:"version"`
	State                 string     `json:"state"`
	StateSince            time.Time  `json:"state_since"`
	ExporterIP            *string    `json:"exporter_ip"`
	LastFlowAt            *time.Time `json:"last_flow_at"`
	FlowsPerSecond        *float64   `json:"flows_per_second"`
	LossRatio5m           *float64   `json:"loss_ratio_5m"`
	ClockSkewSeconds      *float64   `json:"clock_skew_seconds"`
	FlowSource            *string    `json:"flow_source"`
	SamplingRate          *int       `json:"sampling_rate"`
	CoverageRatio         *float64   `json:"coverage_ratio"`
	DroppedRecordsQuota1h string     `json:"dropped_records_quota_1h"`
	Hints                 []string   `json:"hints"`
}

// Handler sirve /flow-exporters.
type Handler struct {
	guard  *authz.Guard
	inv    *flowinv.Store
	states States
	now    func() time.Time
}

// New crea el handler.
func New(guard *authz.Guard, inv *flowinv.Store, states States) *Handler {
	return &Handler{guard: guard, inv: inv, states: states, now: time.Now}
}

// Mount registra las rutas.
func (h *Handler) Mount(r *httpx.ServiceMux) {
	read := authz.Requirement{Scope: authz.ScopeTenant, Permission: PermFlowsRead, AllowKiosk: true}
	r.Handle("GET /api/v1/flow-exporters", h.guard.Wrap(read, http.HandlerFunc(h.list)))
	r.Handle("GET /api/v1/flow-exporters/{router_id}", h.guard.Tenant(PermFlowsRead, h.get))
}

// Exporters devuelve el estado de los exportadores de un tenant (lo usa
// también analytics para el widget exporters_status). sites vacío = todos.
func Exporters(ctx context.Context, inv *flowinv.Snapshot, st States, tenant uuid.UUID, sites []uuid.UUID, now time.Time) ([]FlowExporter, error) {
	var out []FlowExporter
	for _, e := range inv.Data().Exporters {
		if e.TenantID != tenant || (len(sites) > 0 && !slices.Contains(sites, e.SiteID)) {
			continue
		}
		var fe *collectorapi.FlowExporter
		if st != nil {
			var err error
			if fe, err = st.Get(ctx, e.RouterID); err != nil {
				return nil, err
			}
		}
		ip := e.TunnelIP.Unmap().String()
		if fe == nil || fe.TenantID != tenant {
			// Sin estado del collector: el router aún no ha exportado nada.
			out = append(out, FlowExporter{TenantID: tenant, RouterID: e.RouterID, SiteID: e.SiteID, Version: 1,
				State: collectorapi.StatePendingConfiguration, StateSince: now, ExporterIP: &ip, DroppedRecordsQuota1h: "0",
				Hints: []string{collectorapi.HintCheckTrafficFlowTarget, collectorapi.HintCheckTunnel}})
			continue
		}
		hints := fe.Hints
		if hints == nil {
			hints = []string{}
		}
		out = append(out, FlowExporter{TenantID: fe.TenantID, RouterID: fe.RouterID, SiteID: fe.SiteID, Version: fe.Version,
			State: fe.State, StateSince: fe.StateSince, ExporterIP: fe.ExporterIP, LastFlowAt: fe.LastFlowAt,
			FlowsPerSecond: fe.FlowsPerSecond, LossRatio5m: fe.LossRatio5m, ClockSkewSeconds: fe.ClockSkewSeconds,
			FlowSource: fe.FlowSource, SamplingRate: fe.SamplingRate, CoverageRatio: fe.CoverageRatio,
			DroppedRecordsQuota1h: fe.DroppedRecordsQuota1h, Hints: hints})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RouterID.String() < out[j].RouterID.String() })
	return out, nil
}

func (h *Handler) visible(r *http.Request) (uuid.UUID, []uuid.UUID, bool) {
	p := authz.FromContext(r.Context())
	if p == nil || p.TenantID == uuid.Nil {
		return uuid.Nil, nil, false
	}
	return p.TenantID, p.SiteScopes(PermFlowsRead), true
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tenant, sites, ok := h.visible(r)
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
			jsonapi.Write(w, http.StatusOK, map[string]any{"data": []FlowExporter{}})
			return
		}
		sites = []uuid.UUID{id}
	}
	out, err := Exporters(r.Context(), h.inv.Load(), h.states, tenant, sites, h.now())
	if err != nil {
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable)
		return
	}
	if st := r.URL.Query().Get("state"); st != "" {
		out = slices.DeleteFunc(out, func(e FlowExporter) bool { return e.State != st })
	}
	if out == nil {
		out = []FlowExporter{}
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": out})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	tenant, sites, ok := h.visible(r)
	id, err := uuid.Parse(r.PathValue("router_id"))
	if !ok || err != nil {
		problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
		return
	}
	out, err := Exporters(r.Context(), h.inv.Load(), h.states, tenant, sites, h.now())
	if err != nil {
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable)
		return
	}
	for _, e := range out {
		if e.RouterID == id {
			w.Header().Set("ETag", jsonapi.ETag(e.Version))
			jsonapi.Write(w, http.StatusOK, e)
			return
		}
	}
	// Router de otro ISP o inexistente: 404 sin distinguir.
	problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
}
