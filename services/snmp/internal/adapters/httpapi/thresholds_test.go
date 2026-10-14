package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/pgstore"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/app"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/config"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// memThresholds es un ThresholdStore en memoria (por tenant).
type memThresholds struct {
	mu   sync.Mutex
	rows map[uuid.UUID]pgstore.Row
}

func (m *memThresholds) Get(_ context.Context, t uuid.UUID) (*pgstore.Row, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.rows[t]; ok {
		return &r, nil
	}
	return nil, nil
}

func (m *memThresholds) Put(_ context.Context, t uuid.UUID, v domain.ThresholdValues, _ string, expect int) (*pgstore.Row, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rows[t]
	if expect != 0 && r.Version != expect {
		return nil, pgstore.ErrVersion
	}
	r = pgstore.Row{Values: v, UpdatedAt: time.Now(), Version: r.Version + 1}
	m.rows[t] = r
	return &r, nil
}

func (m *memThresholds) Delete(_ context.Context, t uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, t)
	return nil
}

func doJSON(h http.Handler, method, path, tok string, body any, hdr map[string]string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+tok)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestRouterThresholdsAPI(t *testing.T) {
	_, s, _, _ := setup(t)
	key := s.PublicKey()
	ks := authz.KeySet{}
	ks.Add(key)
	mux := httpx.NewMux(nil, slog.New(slog.DiscardHandler))
	audit := &memAudit{}
	eng := app.New(app.Options{Config: config.Config{CPUWarning: 80, CPUCritical: 95, MemoryWarning: 85, MemoryCritical: 95,
		TemperatureWarning: 70, TemperatureCritical: 85}})
	store := &memThresholds{rows: map[uuid.UUID]pgstore.Row{}}
	hd := New(&fakePoller{}, authz.NewGuard(authz.NewVerifier(ks, "horus-auth", nil)), func() (authapi.AuditRecorder, bool) { return audit, true }, nil)
	hd.MountThresholds(mux.ForService("snmp"), app.NewThresholds(store, eng))
	h := mux.Handler()

	reader := token(t, s, tenantA, map[string][]string{"snmp.read": {"*"}})
	admin := token(t, s, tenantA, map[string][]string{"snmp.read": {"*"}, "snmp.manage": {"*"}})
	otherISP := token(t, s, routerB, map[string][]string{"snmp.read": {"*"}, "snmp.manage": {"*"}})

	w := doJSON(h, http.MethodGet, "/api/v1/routers/thresholds", reader, nil, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"customized":false`) || !strings.Contains(w.Body.String(), `"cpu_warning":80`) {
		t.Fatalf("get = %d %s", w.Code, w.Body)
	}
	in := domain.ThresholdValues{CPUWarning: 70, CPUCritical: 90, MemoryWarning: 80, MemoryCritical: 92, TemperatureWarning: 60, TemperatureCritical: 75}
	if w := doJSON(h, http.MethodPut, "/api/v1/routers/thresholds", reader, in, nil); w.Code != http.StatusForbidden {
		t.Fatalf("sin snmp.manage = 403, got %d", w.Code)
	}
	bad := in
	bad.CPUWarning = 99
	if w := doJSON(h, http.MethodPut, "/api/v1/routers/thresholds", admin, bad, nil); w.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(w.Body.String(), "cpu_warning") {
		t.Fatalf("aviso > crítico = 422, got %d %s", w.Code, w.Body)
	}
	w = doJSON(h, http.MethodPut, "/api/v1/routers/thresholds", admin, in, nil)
	if w.Code != http.StatusOK || w.Header().Get("ETag") != `"1"` || !strings.Contains(w.Body.String(), `"customized":true`) {
		t.Fatalf("put = %d %s", w.Code, w.Body)
	}
	if w := doJSON(h, http.MethodPut, "/api/v1/routers/thresholds", admin, in, map[string]string{"If-Match": `"7"`}); w.Code != http.StatusPreconditionFailed {
		t.Fatalf("If-Match viejo = 412, got %d", w.Code)
	}
	// Otro ISP no ve los umbrales de A.
	if w := doJSON(h, http.MethodGet, "/api/v1/routers/thresholds", otherISP, nil, nil); !strings.Contains(w.Body.String(), `"customized":false`) {
		t.Fatalf("otro ISP = %s", w.Body)
	}
	w = doJSON(h, http.MethodDelete, "/api/v1/routers/thresholds", admin, nil, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"customized":false`) {
		t.Fatalf("reset = %d %s", w.Code, w.Body)
	}
	if len(audit.entries) != 2 || audit.entries[0].Action != "snmp.thresholds.updated" || audit.entries[1].Action != "snmp.thresholds.reset" {
		t.Fatalf("auditoría = %+v", audit.entries)
	}
	// Sin PostgreSQL: lectura de los de la instalación, escritura 503.
	mux2 := httpx.NewMux(nil, slog.New(slog.DiscardHandler))
	New(&fakePoller{}, authz.NewGuard(authz.NewVerifier(ks, "horus-auth", nil)), nil, nil).MountThresholds(mux2.ForService("snmp"), app.NewThresholds(nil, eng))
	if w := doJSON(mux2.Handler(), http.MethodPut, "/api/v1/routers/thresholds", admin, in, nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("sin store = 503, got %d", w.Code)
	}
}
