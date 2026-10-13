package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/snmp/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

var (
	_ api.RouterStatusReader = (*Engine)(nil)
	_ api.InterfaceLister    = (*Engine)(nil)
)

// ErrRateLimited: ya se pidió un sondeo manual de ese router hace < 10 s.
var ErrRateLimited = errors.New("snmp: poll already requested")

// Health devuelve meta.poller para un tenant.
func (e *Engine) Health(tenant uuid.UUID, now time.Time) api.PollerHealth {
	e.mu.RLock()
	last := e.lastCycle
	targets := 0
	for _, r := range e.routers {
		if r.TenantID == tenant {
			targets++
		}
	}
	e.mu.RUnlock()
	return api.PollerHealth{Healthy: e.pollerHealthy(now), LastCycleAt: last, IntervalSeconds: int(e.Interval().Seconds()),
		Targets: targets, Enabled: e.o.Config.Enabled}
}

// List implementa api.RouterStatusReader.
func (e *Engine) List(_ context.Context, tenant uuid.UUID, sites []uuid.UUID) ([]api.RouterStatus, api.PollerHealth, error) {
	now := e.now()
	h := e.Health(tenant, now)
	out := []api.RouterStatus{}
	for _, r := range e.snapshot() {
		if r.TenantID != tenant || (len(sites) > 0 && !slices.Contains(sites, r.SiteID)) {
			continue
		}
		r.mu.Lock()
		out = append(out, e.status(r, now, h.Healthy))
		r.mu.Unlock()
	}
	domain.SortProblemsFirst(out)
	return out, h, nil
}

// FilterStatus deja solo los estados de la lista separada por comas.
func FilterStatus(rows []api.RouterStatus, csv string) []api.RouterStatus {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return rows
	}
	want := map[string]bool{}
	for _, s := range strings.Split(csv, ",") {
		if s = strings.TrimSpace(s); s != "" {
			want[s] = true
		}
	}
	out := []api.RouterStatus{}
	for _, r := range rows {
		if want[r.Status] {
			out = append(out, r)
		}
	}
	return out
}

func (e *Engine) find(tenant, id uuid.UUID) *router {
	e.mu.RLock()
	defer e.mu.RUnlock()
	r := e.routers[id]
	if r == nil || r.TenantID != tenant {
		return nil
	}
	return r
}

// Live implementa api.RouterStatusReader.
func (e *Engine) Live(_ context.Context, tenant, routerID uuid.UUID) (*api.RouterLiveMetrics, bool, error) {
	r := e.find(tenant, routerID)
	if r == nil {
		return nil, false, nil
	}
	now := e.now()
	healthy := e.pollerHealthy(now)
	r.mu.Lock()
	defer r.mu.Unlock()
	return e.live(r, now, healthy), true, nil
}

// Interfaces implementa api.InterfaceLister.
func (e *Engine) Interfaces(_ context.Context, tenant, routerID uuid.UUID) (*api.RouterInterfaces, bool, error) {
	r := e.find(tenant, routerID)
	if r == nil {
		return nil, false, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return e.interfaces(r), true, nil
}

// SiteOf devuelve el nodo de un router del tenant (para el alcance por nodo).
func (e *Engine) SiteOf(tenant, routerID uuid.UUID) (uuid.UUID, bool) {
	r := e.find(tenant, routerID)
	if r == nil {
		return uuid.Nil, false
	}
	return r.SiteID, true
}

// PollNow encola un sondeo inmediato. found=false si el router no es del
// tenant (o no es objetivo); queued=false con reason si no se puede sondear;
// ErrRateLimited si ya se pidió hace menos de 10 s.
func (e *Engine) PollNow(ctx context.Context, tenant, routerID uuid.UUID) (found, queued bool, reason *string, err error) {
	r := e.find(tenant, routerID)
	if r == nil {
		return false, false, nil, nil
	}
	now := e.now()
	r.mu.Lock()
	if now.Sub(r.lastManua) < manualPollWindow {
		r.mu.Unlock()
		return true, false, nil, ErrRateLimited
	}
	r.lastManua = now
	r.mu.Unlock()
	if !e.o.Config.Enabled {
		return true, false, str("poller_disabled"), nil
	}
	if !r.Tunnel.IsValid() {
		return true, false, str(api.ReasonNoTunnel), nil
	}
	if c, _ := e.credential(ctx, r, now); c == nil {
		return true, false, str(api.ReasonNoCredentials), nil
	}
	select {
	case e.manual <- routerID:
		return true, true, nil, nil
	default:
		return true, false, str("poller_busy"), nil
	}
}
