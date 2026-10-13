package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/app"
)

var (
	tenantA  = uuid.MustParse("0192e000-0000-7000-8000-000000000001")
	siteA    = uuid.MustParse("0192e111-0000-7000-8000-000000000011")
	siteA2   = uuid.MustParse("0192e111-0000-7000-8000-000000000012")
	routerA  = uuid.MustParse("0192e333-0000-7000-8000-000000000031")
	routerA2 = uuid.MustParse("0192e333-0000-7000-8000-000000000032")
	routerB  = uuid.MustParse("0192e333-0000-7000-8000-000000000033")
)

type fakePoller struct {
	mu    sync.Mutex
	polls int
	rate  bool
}

func (f *fakePoller) rows(tenant uuid.UUID) []api.RouterStatus {
	all := []api.RouterStatus{
		{RouterID: routerA, SiteID: siteA, Name: "core", Status: api.StatusOnline, Signals: api.Signals{ICMP: "ok", SNMP: "ok", WireGuard: "ok"}},
		{RouterID: routerA2, SiteID: siteA2, Name: "agg", Status: api.StatusOffline},
	}
	if tenant != tenantA {
		return []api.RouterStatus{{RouterID: routerB, SiteID: uuid.New(), Name: "otro", Status: api.StatusOnline}}
	}
	return all
}

func (f *fakePoller) List(_ context.Context, tenant uuid.UUID, sites []uuid.UUID) ([]api.RouterStatus, api.PollerHealth, error) {
	var out []api.RouterStatus
	for _, r := range f.rows(tenant) {
		keep := len(sites) == 0
		for _, s := range sites {
			keep = keep || s == r.SiteID
		}
		if keep {
			out = append(out, r)
		}
	}
	return out, api.PollerHealth{Healthy: true, IntervalSeconds: 60, Targets: len(out), Enabled: true}, nil
}

func (f *fakePoller) Live(_ context.Context, tenant, id uuid.UUID) (*api.RouterLiveMetrics, bool, error) {
	for _, r := range f.rows(tenant) {
		if r.RouterID == id {
			return &api.RouterLiveMetrics{Status: r, IntervalSeconds: 60, Interfaces: []api.InterfaceSample{}}, true, nil
		}
	}
	return nil, false, nil
}

func (f *fakePoller) Interfaces(_ context.Context, tenant, id uuid.UUID) (*api.RouterInterfaces, bool, error) {
	if _, ok := f.SiteOf(tenant, id); !ok {
		return nil, false, nil
	}
	return &api.RouterInterfaces{Data: []api.RouterInterface{{ID: uuid.New(), IfIndex: 7, Name: "sfp-sfpplus1", Type: "ethernetCsmacd", Monitored: true, Uplink: true}}}, true, nil
}

func (f *fakePoller) SiteOf(tenant, id uuid.UUID) (uuid.UUID, bool) {
	for _, r := range f.rows(tenant) {
		if r.RouterID == id {
			return r.SiteID, true
		}
	}
	return uuid.Nil, false
}

func (f *fakePoller) PollNow(_ context.Context, tenant, id uuid.UUID) (bool, bool, *string, error) {
	if _, ok := f.SiteOf(tenant, id); !ok {
		return false, false, nil, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rate {
		return true, false, nil, app.ErrRateLimited
	}
	f.polls++
	f.rate = true
	return true, true, nil, nil
}

type memAudit struct {
	mu      sync.Mutex
	entries []authapi.AuditEntry
}

func (m *memAudit) Record(_ context.Context, e authapi.AuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, e)
	return nil
}

func setup(t *testing.T) (http.Handler, *authz.Signer, *fakePoller, *memAudit) {
	t.Helper()
	key, err := authz.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer := authz.NewSigner(key, "horus-auth", time.Minute, nil)
	ks := authz.KeySet{}
	ks.Add(signer.PublicKey())
	mux := httpx.NewMux(nil, slog.New(slog.DiscardHandler))
	p, audit := &fakePoller{}, &memAudit{}
	New(p, authz.NewGuard(authz.NewVerifier(ks, "horus-auth", nil)), func() (authapi.AuditRecorder, bool) { return audit, true },
		slog.New(slog.DiscardHandler)).Mount(mux.ForService("snmp"))
	return mux.Handler(), signer, p, audit
}

func token(t *testing.T, s *authz.Signer, tenant uuid.UUID, perms map[string][]string) string {
	t.Helper()
	tok, _, err := s.Sign(authz.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: uuid.NewString()},
		Typ: authz.TypeUser, Scope: authz.ScopeTenant, TID: tenant.String(), Perms: perms})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func kiosk(t *testing.T, s *authz.Signer, tenant uuid.UUID) string {
	t.Helper()
	tok, _, err := s.Sign(authz.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "kiosk:" + uuid.NewString()},
		Typ: authz.TypeKiosk, Scope: authz.ScopeKiosk, TID: tenant.String(), KioskID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func do(h http.Handler, method, path, tok string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestRoutersStatusAPI(t *testing.T) {
	h, s, p, audit := setup(t)
	all := map[string][]string{"devices.read": {"*"}, "snmp.read": {"*"}, "devices.update": {"*"}}
	readA := token(t, s, tenantA, all)
	if w := do(h, http.MethodGet, "/api/v1/routers/status", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token = 401, got %d", w.Code)
	}
	if w := do(h, http.MethodGet, "/api/v1/routers/status", token(t, s, tenantA, map[string][]string{"flows.read": {"*"}}), nil); w.Code != http.StatusForbidden {
		t.Fatalf("sin devices.read = 403, got %d", w.Code)
	}
	w := do(h, http.MethodGet, "/api/v1/routers/status", readA, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}
	var list struct {
		Data []api.RouterStatus `json:"data"`
		Meta struct {
			Poller      api.PollerHealth `json:"poller"`
			GeneratedAt string           `json:"generated_at"`
		} `json:"meta"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Data) != 2 || !list.Meta.Poller.Healthy || list.Meta.GeneratedAt == "" {
		t.Fatalf("list: %s", w.Body)
	}
	// Filtros: status y site_id; alcance por nodo del permiso.
	w = do(h, http.MethodGet, "/api/v1/routers/status?status=offline", readA, nil)
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Data) != 1 || list.Data[0].Name != "agg" {
		t.Fatalf("filtro status: %s", w.Body)
	}
	scoped := token(t, s, tenantA, map[string][]string{"devices.read": {"site:" + siteA.String()}, "snmp.read": {"site:" + siteA.String()}})
	w = do(h, http.MethodGet, "/api/v1/routers/status", scoped, nil)
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Data) != 1 || list.Data[0].Name != "core" {
		t.Fatalf("alcance por nodo: %s", w.Body)
	}
	w = do(h, http.MethodGet, "/api/v1/routers/status?site_id="+siteA2.String(), scoped, nil)
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != http.StatusOK || len(list.Data) != 0 {
		t.Fatalf("site fuera del alcance = lista vacía: %d %s", w.Code, w.Body)
	}
	if w := do(h, http.MethodGet, "/api/v1/routers/status?site_id=x", readA, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("site_id inválido = 400, got %d", w.Code)
	}
	// Kiosco: ve todo su ISP sin permisos.
	if w := do(h, http.MethodGet, "/api/v1/routers/status", kiosk(t, s, tenantA), nil); w.Code != http.StatusOK {
		t.Fatalf("kiosco = 200, got %d %s", w.Code, w.Body)
	}
	if w := do(h, http.MethodGet, "/api/v1/routers/"+routerA.String()+"/metrics/live", kiosk(t, s, tenantA), nil); w.Code != http.StatusOK {
		t.Fatalf("kiosco live = 200, got %d", w.Code)
	}
	// Live e interfaces; router de otro tenant = 404; fuera del alcance = 404.
	if w := do(h, http.MethodGet, "/api/v1/routers/"+routerA.String()+"/metrics/live", readA, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"interval_seconds":60`) {
		t.Fatalf("live = %d %s", w.Code, w.Body)
	}
	if w := do(h, http.MethodGet, "/api/v1/routers/"+routerB.String()+"/metrics/live", readA, nil); w.Code != http.StatusNotFound {
		t.Fatalf("router de otro ISP = 404, got %d", w.Code)
	}
	if w := do(h, http.MethodGet, "/api/v1/routers/"+routerA2.String()+"/metrics/live", scoped, nil); w.Code != http.StatusNotFound {
		t.Fatalf("router fuera del alcance = 404, got %d", w.Code)
	}
	if w := do(h, http.MethodGet, "/api/v1/routers/"+routerA.String()+"/metrics/live", token(t, s, tenantA, map[string][]string{"devices.read": {"*"}}), nil); w.Code != http.StatusForbidden {
		t.Fatalf("live sin snmp.read = 403, got %d", w.Code)
	}
	if w := do(h, http.MethodGet, "/api/v1/routers/"+routerA.String()+"/interfaces", readA, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "sfp-sfpplus1") {
		t.Fatalf("interfaces = %d %s", w.Code, w.Body)
	}
	if w := do(h, http.MethodGet, "/api/v1/routers/no-uuid/interfaces", readA, nil); w.Code != http.StatusNotFound {
		t.Fatalf("id inválido = 404, got %d", w.Code)
	}
	// POST /poll: 428 sin Idempotency-Key, permiso devices.update, 202, 429, 404 otro tenant, auditoría.
	poll := "/api/v1/routers/" + routerA.String() + "/poll"
	if w := do(h, http.MethodPost, poll, readA, nil); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("sin Idempotency-Key = 428, got %d", w.Code)
	}
	idem := map[string]string{"Idempotency-Key": uuid.NewString()}
	if w := do(h, http.MethodPost, poll, token(t, s, tenantA, map[string][]string{"devices.read": {"*"}}), idem); w.Code != http.StatusForbidden {
		t.Fatalf("sin devices.update = 403, got %d", w.Code)
	}
	if w := do(h, http.MethodPost, poll, kiosk(t, s, tenantA), idem); w.Code != http.StatusForbidden {
		t.Fatalf("kiosco no puede sondear, got %d", w.Code)
	}
	w = do(h, http.MethodPost, poll, readA, idem)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"queued":true`) || p.polls != 1 {
		t.Fatalf("poll = %d %s", w.Code, w.Body)
	}
	w = do(h, http.MethodPost, poll, readA, idem)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "10" {
		t.Fatalf("segundo poll en 10 s = 429, got %d", w.Code)
	}
	if w := do(h, http.MethodPost, "/api/v1/routers/"+routerB.String()+"/poll", readA, idem); w.Code != http.StatusNotFound {
		t.Fatalf("poll de otro ISP = 404, got %d", w.Code)
	}
	if len(audit.entries) != 2 || audit.entries[0].Action != "snmp.router.poll_requested" || audit.entries[0].Outcome != "success" || audit.entries[1].Outcome != "denied" {
		t.Fatalf("auditoría: %+v", audit.entries)
	}
}
