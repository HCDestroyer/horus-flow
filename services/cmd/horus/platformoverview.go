package main

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/platformoverview"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
)

// PermPlatformOverview es el permiso de GET /platform/overview.
const PermPlatformOverview = "platform.status.read"

// overviewSourceTimeout limita lo que espera la vista a cada fuente: una
// fuente lenta deja sus campos sin valor en vez de colgar la respuesta.
const overviewSourceTimeout = 5 * time.Second

// overviewHandler sirve GET /api/v1/platform/overview (contrato v0
// platform.yaml, TenantOverview): compone por tenant los conteos de auth
// (tenants), devices (routers) y de las fuentes platformoverview de
// ingester (exportadores), wireguard (túneles) y detection (hallazgos), todo
// por module.Services. Sin la lista de tenants responde 503; sin otra fuente
// sus campos obligatorios valen 0, los opcionales se omiten y
// flow_coverage_ratio es null (y se registra un aviso).
type overviewHandler struct {
	services *module.Services
	timeout  time.Duration
	now      func() time.Time
	logger   *slog.Logger
}

func newOverviewHandler(services *module.Services, logger *slog.Logger) *overviewHandler {
	return &overviewHandler{services: services, timeout: overviewSourceTimeout, now: time.Now, logger: logger}
}

// tenantOverview es el esquema TenantOverview.
type tenantOverview struct {
	TenantID             uuid.UUID `json:"tenant_id"`
	Slug                 string    `json:"slug"`
	Name                 string    `json:"name"`
	Status               string    `json:"status"`
	RoutersTotal         int       `json:"routers_total"`
	ExportersExporting   int       `json:"exporters_exporting"`
	ExportersSilent      int       `json:"exporters_silent"`
	TunnelsDown          *int      `json:"tunnels_down,omitempty"`
	OpenFindingsCritical int       `json:"open_findings_critical"`
	IngestFlowsPerSecond float64   `json:"ingest_flows_per_second"`
	FlowsDroppedQuota1h  *string   `json:"flows_dropped_quota_last_hour,omitempty"`
	FlowCoverageRatio    *float64  `json:"flow_coverage_ratio"`
}

func (h *overviewHandler) get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dir, ok := module.Lookup[authapi.TenantDirectory](h.services, authapi.ServiceTenants)
	if !ok {
		h.logger.WarnContext(ctx, "platform overview: auth tenant directory not available in this process")
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable)
		return
	}
	var (
		tenants []authapi.TenantSummary
		tErr    error
		mu      sync.Mutex
		stats   = map[uuid.UUID]*platformoverview.Stats{}
		wg      sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		c, cancel := context.WithTimeout(ctx, h.timeout)
		defer cancel()
		tenants, tErr = dir.TenantSummaries(c)
	}()
	collect := func(name string, src platformoverview.Source) {
		defer wg.Done()
		c, cancel := context.WithTimeout(ctx, h.timeout)
		defer cancel()
		m, err := src.OverviewStats(c)
		if err != nil {
			h.logger.WarnContext(ctx, "platform overview: source failed", slog.String("source", name), slog.Any("error", err))
			return
		}
		mu.Lock()
		defer mu.Unlock()
		for t, s := range m {
			cur, ok := stats[t]
			if !ok {
				cur = &platformoverview.Stats{}
				stats[t] = cur
			}
			cur.Merge(s)
		}
	}
	// Conjunto de campos que alguna fuente disponible aporta (zero = 0 / "0").
	var have platformoverview.Stats
	if dev, ok := module.Lookup[devapi.Onboarding](h.services, devapi.ServiceOnboarding); ok {
		have.RoutersTotal = platformoverview.Ptr(0)
		wg.Add(1)
		go collect("devices", platformoverview.SourceFunc(func(c context.Context) (map[uuid.UUID]platformoverview.Stats, error) {
			return routerCounts(c, dev)
		}))
	} else {
		h.logger.WarnContext(ctx, "platform overview: source not available in this process", slog.String("source", devapi.ServiceOnboarding))
	}
	for _, name := range platformoverview.Services {
		src, ok := module.Lookup[platformoverview.Source](h.services, name)
		if !ok {
			h.logger.WarnContext(ctx, "platform overview: source not available in this process", slog.String("source", name))
			continue
		}
		switch name {
		case platformoverview.ServiceTunnels:
			have.TunnelsDown = platformoverview.Ptr(0)
		case platformoverview.ServiceExporters:
			have.FlowsDroppedQuota1h = platformoverview.Ptr(uint64(0))
		}
		wg.Add(1)
		go collect(name, src)
	}
	wg.Wait()
	if tErr != nil {
		h.logger.ErrorContext(ctx, "platform overview: list tenants", slog.Any("error", tErr))
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable)
		return
	}
	data := make([]tenantOverview, 0, len(tenants))
	for _, t := range tenants {
		s := have
		if got, ok := stats[t.ID]; ok {
			s.Merge(*got)
		}
		data = append(data, buildTenantOverview(t, s))
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{
		"generated_at": h.now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"data":         data,
	})
}

// routerCounts cuenta los routers vivos de cada tenant (devices).
func routerCounts(ctx context.Context, dev devapi.Onboarding) (map[uuid.UUID]platformoverview.Stats, error) {
	routers, err := dev.ListRouters(ctx)
	if err != nil {
		return nil, err
	}
	n := map[uuid.UUID]int{}
	for _, r := range routers {
		n[r.TenantID]++
	}
	out := make(map[uuid.UUID]platformoverview.Stats, len(n))
	for t, c := range n {
		out[t] = platformoverview.Stats{RoutersTotal: platformoverview.Ptr(c)}
	}
	return out, nil
}

func buildTenantOverview(t authapi.TenantSummary, s platformoverview.Stats) tenantOverview {
	o := tenantOverview{TenantID: t.ID, Slug: t.Slug, Name: t.Name, Status: t.Status,
		RoutersTotal: deref(s.RoutersTotal), ExportersExporting: deref(s.ExportersExporting), ExportersSilent: deref(s.ExportersSilent),
		TunnelsDown: s.TunnelsDown, OpenFindingsCritical: deref(s.OpenFindingsCritical),
		IngestFlowsPerSecond: deref(s.IngestFlowsPerSecond), FlowCoverageRatio: s.FlowCoverageRatio}
	if s.FlowsDroppedQuota1h != nil {
		v := strconv.FormatUint(*s.FlowsDroppedQuota1h, 10)
		o.FlowsDroppedQuota1h = &v
	}
	return o
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
