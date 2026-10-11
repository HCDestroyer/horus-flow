package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/platformoverview"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
)

type fakeTenants struct {
	list []authapi.TenantSummary
	err  error
}

func (f fakeTenants) TenantSummaries(context.Context) ([]authapi.TenantSummary, error) {
	return f.list, f.err
}

type fakeRouters struct {
	devapi.Onboarding
	list []devapi.RouterInfo
}

func (f fakeRouters) ListRouters(context.Context) ([]devapi.RouterInfo, error) { return f.list, nil }

var (
	ovTenantA = uuid.MustParse("0192e000-0000-7000-8000-00000000000a")
	ovTenantB = uuid.MustParse("0192e000-0000-7000-8000-00000000000b")
)

func overviewServices(t *testing.T, full bool) *module.Services {
	t.Helper()
	s := module.NewServices()
	provide := func(name string, v any) {
		if err := s.Provide(name, v); err != nil {
			t.Fatal(err)
		}
	}
	provide(authapi.ServiceTenants, fakeTenants{list: []authapi.TenantSummary{
		{ID: ovTenantA, Slug: "isp-a", Name: "ISP A", Status: "active"},
		{ID: ovTenantB, Slug: "isp-b", Name: "ISP B", Status: "suspended"},
	}})
	if !full {
		return s
	}
	provide(devapi.ServiceOnboarding, fakeRouters{list: []devapi.RouterInfo{
		{ID: uuid.New(), TenantID: ovTenantA}, {ID: uuid.New(), TenantID: ovTenantA}, {ID: uuid.New(), TenantID: ovTenantA},
	}})
	provide(platformoverview.ServiceExporters, platformoverview.SourceFunc(func(context.Context) (map[uuid.UUID]platformoverview.Stats, error) {
		return map[uuid.UUID]platformoverview.Stats{ovTenantA: {
			ExportersExporting: platformoverview.Ptr(2), ExportersSilent: platformoverview.Ptr(1),
			IngestFlowsPerSecond: platformoverview.Ptr(150.5), FlowsDroppedQuota1h: platformoverview.Ptr(uint64(12)),
			FlowCoverageRatio: platformoverview.Ptr(0.8),
		}}, nil
	}))
	provide(platformoverview.ServiceTunnels, platformoverview.SourceFunc(func(context.Context) (map[uuid.UUID]platformoverview.Stats, error) {
		return map[uuid.UUID]platformoverview.Stats{ovTenantA: {TunnelsDown: platformoverview.Ptr(1)}}, nil
	}))
	provide(platformoverview.ServiceFindings, platformoverview.SourceFunc(func(context.Context) (map[uuid.UUID]platformoverview.Stats, error) {
		return nil, errors.New("detection db down")
	}))
	return s
}

func overviewServer(t *testing.T, services *module.Services) (http.Handler, *authz.Signer) {
	t.Helper()
	key, err := authz.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer := authz.NewSigner(key, "horus-auth", time.Minute, nil)
	ks := authz.KeySet{}
	ks.Add(signer.PublicKey())
	h := newOverviewHandler(services, slog.New(slog.DiscardHandler))
	h.now = func() time.Time { return time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC) }
	mux := httpx.NewMux(nil, slog.New(slog.DiscardHandler))
	mux.ForService("gateway").Handle("GET /api/v1/platform/overview",
		authz.NewGuard(authz.NewVerifier(ks, "horus-auth", nil)).Platform(PermPlatformOverview, h.get))
	return mux.Handler(), signer
}

func overviewToken(t *testing.T, s *authz.Signer, scope string, perms map[string][]string) string {
	t.Helper()
	c := authz.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: uuid.NewString()}, Typ: authz.TypeUser, Scope: scope, Perms: perms}
	if scope == authz.ScopeTenant {
		c.TID = ovTenantA.String()
	}
	tok, _, err := s.Sign(c)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func getOverview(h http.Handler, tok string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/platform/overview", nil)
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

type overviewBody struct {
	GeneratedAt string           `json:"generated_at"`
	Data        []map[string]any `json:"data"`
}

func decodeOverview(t *testing.T, w *httptest.ResponseRecorder) overviewBody {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	var b overviewBody
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	return b
}

var statusRead = map[string][]string{PermPlatformOverview: {"*"}}

func TestPlatformOverviewComposesSources(t *testing.T) {
	h, s := overviewServer(t, overviewServices(t, true))
	b := decodeOverview(t, getOverview(h, overviewToken(t, s, authz.ScopePlatform, statusRead)))
	if b.GeneratedAt != "2026-10-11T12:00:00.000Z" || len(b.Data) != 2 {
		t.Fatalf("body = %+v", b)
	}
	a, bb := b.Data[0], b.Data[1]
	want := map[string]any{
		"tenant_id": ovTenantA.String(), "slug": "isp-a", "name": "ISP A", "status": "active",
		"routers_total": 3.0, "exporters_exporting": 2.0, "exporters_silent": 1.0, "tunnels_down": 1.0,
		// detection falla: el conteo obligatorio cae a 0 en vez de tumbar la vista.
		"open_findings_critical": 0.0, "ingest_flows_per_second": 150.5,
		"flows_dropped_quota_last_hour": "12", "flow_coverage_ratio": 0.8,
	}
	for k, v := range want {
		if a[k] != v {
			t.Errorf("tenant a %s = %v, want %v", k, a[k], v)
		}
	}
	if len(a) != len(want) {
		t.Errorf("tenant a has unexpected fields: %v", a)
	}
	// Tenant sin datos en las fuentes disponibles: ceros, no ausencias.
	wantB := map[string]any{
		"tenant_id": ovTenantB.String(), "slug": "isp-b", "name": "ISP B", "status": "suspended",
		"routers_total": 0.0, "exporters_exporting": 0.0, "exporters_silent": 0.0, "tunnels_down": 0.0,
		"open_findings_critical": 0.0, "ingest_flows_per_second": 0.0, "flows_dropped_quota_last_hour": "0",
		"flow_coverage_ratio": nil,
	}
	for k, v := range wantB {
		if got, ok := bb[k]; !ok || got != v {
			t.Errorf("tenant b %s = %v (present %v), want %v", k, got, ok, v)
		}
	}
}

func TestPlatformOverviewDegradesWithoutSources(t *testing.T) {
	h, s := overviewServer(t, overviewServices(t, false))
	b := decodeOverview(t, getOverview(h, overviewToken(t, s, authz.ScopePlatform, statusRead)))
	if len(b.Data) != 2 {
		t.Fatalf("data = %v", b.Data)
	}
	a := b.Data[0]
	for _, k := range []string{"routers_total", "exporters_exporting", "exporters_silent", "open_findings_critical", "ingest_flows_per_second"} {
		if a[k] != 0.0 {
			t.Errorf("%s = %v, want 0", k, a[k])
		}
	}
	// Opcionales sin fuente: se omiten; la cobertura es null.
	for _, k := range []string{"tunnels_down", "flows_dropped_quota_last_hour"} {
		if _, ok := a[k]; ok {
			t.Errorf("%s present without its source: %v", k, a[k])
		}
	}
	if v, ok := a["flow_coverage_ratio"]; !ok || v != nil {
		t.Errorf("flow_coverage_ratio = %v (present %v), want null", v, ok)
	}
}

func TestPlatformOverviewWithoutTenants(t *testing.T) {
	h, s := overviewServer(t, module.NewServices())
	if w := getOverview(h, overviewToken(t, s, authz.ScopePlatform, statusRead)); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("no auth = %d %s", w.Code, w.Body)
	}
	svc := module.NewServices()
	if err := svc.Provide(authapi.ServiceTenants, fakeTenants{err: errors.New("db down")}); err != nil {
		t.Fatal(err)
	}
	h, s = overviewServer(t, svc)
	if w := getOverview(h, overviewToken(t, s, authz.ScopePlatform, statusRead)); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("auth error = %d %s", w.Code, w.Body)
	}
}

func TestPlatformOverviewEmptyInstallation(t *testing.T) {
	svc := module.NewServices()
	if err := svc.Provide(authapi.ServiceTenants, fakeTenants{}); err != nil {
		t.Fatal(err)
	}
	h, s := overviewServer(t, svc)
	w := getOverview(h, overviewToken(t, s, authz.ScopePlatform, statusRead))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil || string(raw["data"]) != "[]" {
		t.Fatalf("data = %s, err %v", raw["data"], err)
	}
}

func TestPlatformOverviewRequiresPlatformToken(t *testing.T) {
	h, s := overviewServer(t, overviewServices(t, true))
	if w := getOverview(h, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", w.Code)
	}
	if w := getOverview(h, overviewToken(t, s, authz.ScopeTenant, statusRead)); w.Code == http.StatusOK {
		t.Fatalf("tenant token = %d", w.Code)
	}
	if w := getOverview(h, overviewToken(t, s, authz.ScopePlatform, map[string][]string{"platform.tenants.read": {"*"}})); w.Code != http.StatusForbidden {
		t.Fatalf("missing permission = %d", w.Code)
	}
}

func TestMountAPIServesOverviewWithoutEventsStore(t *testing.T) {
	key, err := authz.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer := authz.NewSigner(key, "horus-auth", time.Minute, nil)
	ks := authz.KeySet{}
	ks.Add(signer.PublicKey())
	services := overviewServices(t, false)
	if err := services.Provide(authapi.ServiceVerifier, authz.NewVerifier(ks, "horus-auth", nil)); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.DiscardHandler)
	api := httpx.NewMux(nil, logger)
	(&platform{roles: []string{"analytics"}, logger: logger}).mountAPI(api, services)
	if api.Routes() != 0 {
		t.Fatalf("routes without gateway role = %d", api.Routes())
	}
	(&platform{roles: []string{"gateway"}, logger: logger}).mountAPI(api, services)
	if api.Routes() != 1 {
		t.Fatalf("routes = %d, want overview only (no events store)", api.Routes())
	}
	b := decodeOverview(t, getOverview(api.Handler(), overviewToken(t, signer, authz.ScopePlatform, statusRead)))
	if len(b.Data) != 2 {
		t.Fatalf("data = %v", b.Data)
	}
}
