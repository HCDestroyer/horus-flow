package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/pgstore"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/app"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/config"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// memCapRepo guarda solo los ajustes (lo demás vacío).
type memCapRepo struct {
	mu   sync.Mutex
	rows map[uuid.UUID]domain.SettingsRow
}

func (m *memCapRepo) ListUplinkConfigs(context.Context, uuid.UUID) ([]domain.UplinkConfig, error) {
	return nil, nil
}
func (m *memCapRepo) GetUplinkConfig(context.Context, uuid.UUID, uuid.UUID) (*domain.UplinkConfig, error) {
	return nil, nil
}
func (m *memCapRepo) PutUplinkConfig(context.Context, uuid.UUID, uuid.UUID, domain.UplinkConfigInput, string, int) (*domain.UplinkConfig, error) {
	return nil, nil
}
func (m *memCapRepo) DeleteUplinkConfig(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
}
func (m *memCapRepo) GetCapacitySettings(_ context.Context, t uuid.UUID) (*domain.SettingsRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.rows[t]; ok {
		return &r, nil
	}
	return nil, nil
}
func (m *memCapRepo) PutCapacitySettings(_ context.Context, t uuid.UUID, v domain.CapacitySettings, _ string, expect int) (*domain.SettingsRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rows[t]
	if expect != 0 && r.Version != expect {
		return nil, pgstore.ErrVersion
	}
	r = domain.SettingsRow{Settings: v, Version: r.Version + 1}
	m.rows[t] = r
	return &r, nil
}
func (m *memCapRepo) DeleteCapacitySettings(_ context.Context, t uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, t)
	return nil
}
func (m *memCapRepo) LoadCapacity(context.Context) (*pgstore.CapacityState, error) {
	return &pgstore.CapacityState{}, nil
}
func (m *memCapRepo) SaveCondition(context.Context, domain.ConditionRow, *outbox.Event) error {
	return nil
}
func (m *memCapRepo) ListConditions(context.Context, uuid.UUID) ([]domain.ConditionRow, error) {
	return nil, nil
}
func (m *memCapRepo) SaveForecast(context.Context, domain.ForecastRow, *outbox.Event) error {
	return nil
}
func (m *memCapRepo) ListForecasts(context.Context, uuid.UUID) ([]domain.ForecastRow, error) {
	return nil, nil
}

func TestCapacityAPI(t *testing.T) {
	_, s, _, _ := setup(t)
	ks := authz.KeySet{}
	ks.Add(s.PublicKey())
	guard := authz.NewGuard(authz.NewVerifier(ks, "horus-auth", nil))
	audit := &memAudit{}
	eng := app.New(app.Options{Config: config.Config{Enabled: true}})
	repo := &memCapRepo{rows: map[uuid.UUID]domain.SettingsRow{}}
	deny := []netip.Prefix{netip.MustParsePrefix("10.255.0.0/24")}
	mux := httpx.NewMux(nil, slog.New(slog.DiscardHandler))
	NewCapacity(app.NewCapacity(app.CapacityOptions{Repo: repo, Engine: eng}), guard, func() (authapi.AuditRecorder, bool) { return audit, true },
		deny, nil).Mount(mux.ForService("snmp"))
	h := mux.Handler()
	reader := token(t, s, tenantA, map[string][]string{"snmp.read": {"*"}})
	admin := token(t, s, tenantA, map[string][]string{"snmp.read": {"*"}, "snmp.manage": {"*"}})
	noPerm := token(t, s, tenantA, map[string][]string{"devices.read": {"*"}})

	if w := doJSON(h, http.MethodGet, "/api/v1/capacity/overview", "", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token = %d", w.Code)
	}
	if w := doJSON(h, http.MethodGet, "/api/v1/capacity/overview", noPerm, nil, nil); w.Code != http.StatusForbidden {
		t.Fatalf("sin snmp.read = %d", w.Code)
	}
	w := doJSON(h, http.MethodGet, "/api/v1/capacity/overview", reader, nil, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"data":[]`) || !strings.Contains(w.Body.String(), `"planning_threshold":0.8`) ||
		!strings.Contains(w.Body.String(), `"history":false`) {
		t.Fatalf("overview = %d %s", w.Code, w.Body)
	}
	if w := doJSON(h, http.MethodGet, "/api/v1/capacity/overview?days=90", reader, nil, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("days fuera de rango = %d", w.Code)
	}
	if w := doJSON(h, http.MethodGet, "/api/v1/capacity/uplinks/"+uuid.NewString(), reader, nil, nil); w.Code != http.StatusNotFound ||
		!strings.Contains(w.Body.String(), CodeUplinkNotFound) {
		t.Fatalf("enlace inexistente = %d %s", w.Code, w.Body)
	}
	if w := doJSON(h, http.MethodGet, "/api/v1/capacity/egress", reader, nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("sin salida internacional = %d", w.Code)
	}
	if w := doJSON(h, http.MethodGet, "/api/v1/routers/"+routerA.String()+"/netwatch-snippet", admin, nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("script de router desconocido = %d", w.Code)
	}
	if w := doJSON(h, http.MethodGet, "/api/v1/routers/"+routerA.String()+"/netwatch-snippet", reader, nil, nil); w.Code != http.StatusForbidden {
		t.Fatalf("script sin snmp.manage = %d", w.Code)
	}
	body := map[string]any{"router_id": routerA, "label": "", "kind": "fibra", "contracted_bps": 10}
	if w := doJSON(h, http.MethodPut, "/api/v1/capacity/uplinks/"+uuid.NewString(), admin, body, nil); w.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(w.Body.String(), "contracted_bps") || !strings.Contains(w.Body.String(), "kind") {
		t.Fatalf("validación = %d %s", w.Code, w.Body)
	}
	good := map[string]any{"router_id": routerA, "label": "Proveedor A", "kind": "international_transit"}
	if w := doJSON(h, http.MethodPut, "/api/v1/capacity/uplinks/"+uuid.NewString(), admin, good, nil); w.Code != http.StatusNotFound {
		t.Fatalf("interfaz que no es del ISP = %d %s", w.Code, w.Body)
	}
	if w := doJSON(h, http.MethodPut, "/api/v1/capacity/uplinks/"+uuid.NewString(), reader, good, nil); w.Code != http.StatusForbidden {
		t.Fatalf("sin snmp.manage = %d", w.Code)
	}

	// Ajustes: lectura por defecto, escritura validada y auditada, If-Match.
	w = doJSON(h, http.MethodGet, "/api/v1/capacity/settings", reader, nil, nil)
	if w.Code != http.StatusOK || w.Header().Get("ETag") != `"0"` || !strings.Contains(w.Body.String(), `"customized":false`) {
		t.Fatalf("ajustes = %d %s", w.Code, w.Body)
	}
	st := domain.DefaultCapacitySettings()
	st.ProbeTargets = []domain.ProbeTarget{{Label: "Horus interno", Address: netip.MustParseAddr("10.255.0.4")}}
	if w := doJSON(h, http.MethodPut, "/api/v1/capacity/settings", admin, st, nil); w.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(w.Body.String(), "probe_targets[0].address") {
		t.Fatalf("destino interno = %d %s", w.Code, w.Body)
	}
	st.ProbeTargets = []domain.ProbeTarget{{Label: "Gateway del ISP", Address: netip.MustParseAddr("203.0.113.1")}}
	st.LocalASNs = []uint32{2906, 15169}
	w = doJSON(h, http.MethodPut, "/api/v1/capacity/settings", admin, st, nil)
	if w.Code != http.StatusOK || w.Header().Get("ETag") != `"1"` || !strings.Contains(w.Body.String(), `"local_asns":[2906,15169]`) {
		t.Fatalf("guardar ajustes = %d %s", w.Code, w.Body)
	}
	if w := doJSON(h, http.MethodPut, "/api/v1/capacity/settings", admin, st, map[string]string{"If-Match": `"9"`}); w.Code != http.StatusPreconditionFailed {
		t.Fatalf("If-Match viejo = %d", w.Code)
	}
	if w := doJSON(h, http.MethodDelete, "/api/v1/capacity/settings", admin, nil, nil); w.Code != http.StatusOK {
		t.Fatalf("reset = %d", w.Code)
	}
	if len(audit.entries) != 2 || audit.entries[0].Action != "snmp.capacity_settings.updated" || audit.entries[1].Action != "snmp.capacity_settings.reset" {
		t.Fatalf("auditoría = %+v", audit.entries)
	}
	if w := doJSON(h, http.MethodGet, "/api/v1/capacity/traffic-split?days=7", reader, nil, nil); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `"country_source":"none"`) {
		t.Fatalf("reparto = %d %s", w.Code, w.Body)
	}

	// Sin PostgreSQL: lectura con valores por defecto, escritura 503.
	mux2 := httpx.NewMux(nil, slog.New(slog.DiscardHandler))
	NewCapacity(app.NewCapacity(app.CapacityOptions{Engine: eng}), guard, nil, deny, nil).Mount(mux2.ForService("snmp"))
	if w := doJSON(mux2.Handler(), http.MethodPut, "/api/v1/capacity/settings", admin, domain.DefaultCapacitySettings(), nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("sin store = %d", w.Code)
	}
	if w := doJSON(mux2.Handler(), http.MethodPut, "/api/v1/capacity/uplinks/"+uuid.NewString(), admin, good, nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("enlace sin store = %d", w.Code)
	}
}
