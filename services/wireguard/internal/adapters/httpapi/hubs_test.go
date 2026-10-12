package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/app"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/domain"
)

var (
	hubID   = uuid.MustParse("0192f0d2-0000-7000-8000-000000000001")
	envPool = uuid.MustParse("0192f0d2-0000-7000-8000-0000000000a1")
	apiPool = uuid.MustParse("0192f0d2-0000-7000-8000-0000000000a2")
	t0      = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
)

// fakeStore implementa la parte de app.Store de la administración del hub.
type fakeStore struct {
	app.Store
	mu       sync.Mutex
	hub      app.Hub
	inUse    map[uuid.UUID]bool
	overlap  bool // simula la carrera con el EXCLUDE de ip_pool
	added    []netip.Prefix
	deleted  []uuid.UUID
	patched  []app.HubPatch
	lastSeen time.Time
}

func (f *fakeStore) GetHub(_ context.Context, id uuid.UUID) (*app.Hub, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.hub.ID {
		return nil, domain.ErrNotFound
	}
	h := f.hub
	h.Pools, h.PoolInfo = slices.Clone(f.hub.Pools), slices.Clone(f.hub.PoolInfo)
	return &h, nil
}

func (f *fakeStore) ListHubs(ctx context.Context) ([]app.Hub, error) {
	h, _ := f.GetHub(ctx, f.hub.ID)
	return []app.Hub{*h}, nil
}

func (f *fakeStore) UpdateHub(_ context.Context, id uuid.UUID, expect int, p app.HubPatch) (*app.Hub, error) {
	f.mu.Lock()
	if id != f.hub.ID {
		f.mu.Unlock()
		return nil, domain.ErrNotFound
	}
	if expect != f.hub.Version {
		f.mu.Unlock()
		return nil, app.ErrVersionChanged
	}
	if p.Name != nil {
		f.hub.Name = *p.Name
	}
	if p.Endpoint != nil {
		f.hub.Endpoint = *p.Endpoint
	}
	if p.MTU != nil {
		f.hub.MTU = *p.MTU
	}
	if p.Keepalive != nil {
		f.hub.Keepalive = *p.Keepalive
	}
	f.hub.Version++
	f.patched = append(f.patched, p)
	f.mu.Unlock()
	return f.GetHub(context.Background(), id)
}

func (f *fakeStore) AddPool(_ context.Context, id uuid.UUID, cidr netip.Prefix) (*app.Pool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.hub.ID {
		return nil, domain.ErrNotFound
	}
	if f.overlap {
		return nil, app.ErrPoolOverlap
	}
	p := app.Pool{ID: uuid.Must(uuid.NewV7()), CIDR: cidr, CreatedAt: time.Now()}
	f.hub.Pools, f.hub.PoolInfo = append(f.hub.Pools, cidr), append(f.hub.PoolInfo, p)
	f.hub.Version++
	f.added = append(f.added, cidr)
	return &p, nil
}

func (f *fakeStore) DeletePool(_ context.Context, id, poolID uuid.UUID, since time.Time, guard func(app.Pool) error) (*app.Pool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastSeen = since
	if id != f.hub.ID {
		return nil, domain.ErrNotFound
	}
	i := slices.IndexFunc(f.hub.PoolInfo, func(p app.Pool) bool { return p.ID == poolID })
	if i < 0 {
		return nil, app.ErrPoolNotFound
	}
	if len(f.hub.PoolInfo) == 1 {
		return nil, app.ErrLastPool
	}
	p := f.hub.PoolInfo[i]
	if err := guard(p); err != nil {
		return nil, err
	}
	if f.inUse[poolID] {
		return nil, app.ErrPoolInUse
	}
	f.hub.PoolInfo = slices.Delete(f.hub.PoolInfo, i, i+1)
	f.hub.Pools = slices.Delete(f.hub.Pools, i, i+1)
	f.hub.Version++
	f.deleted = append(f.deleted, poolID)
	return &p, nil
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

type fixture struct {
	h      http.Handler
	signer *authz.Signer
	store  *fakeStore
	audit  *memAudit
}

func setup(t *testing.T) *fixture {
	t.Helper()
	key, err := authz.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer := authz.NewSigner(key, "horus-auth", time.Minute, nil)
	ks := authz.KeySet{}
	ks.Add(signer.PublicKey())
	guard := authz.NewGuard(authz.NewVerifier(ks, "horus-auth", nil))
	envCIDR, extra := netip.MustParsePrefix("10.255.0.0/16"), netip.MustParsePrefix("10.254.0.0/24")
	services := netip.MustParsePrefix("10.255.0.0/24")
	st := &fakeStore{inUse: map[uuid.UUID]bool{}, hub: app.Hub{ID: hubID, Name: "hub-1", Endpoint: "horus.example.net", ListenPort: 51820,
		PublicKey: "HIgo9xNzJMWLKASShiTqIybxZ0U3wGLiUeJ1PKf8ykw=", ServicesCIDR: services, Status: "up", MTU: 1420, Keepalive: 25, Version: 3,
		Pools:         []netip.Prefix{envCIDR, extra},
		PoolInfo:      []app.Pool{{ID: envPool, CIDR: envCIDR, CreatedAt: t0, Used: 7}, {ID: apiPool, CIDR: extra, CreatedAt: t0.Add(time.Hour), Used: 2}},
		AddressesUsed: 9, PeersActive: 5, Tenants: 2}}
	audit := &memAudit{}
	svc := app.New(app.Options{Store: st, Audit: func() (authapi.AuditRecorder, bool) { return audit, true },
		Hub: app.Hub{ID: hubID, ServicesCIDR: services, Pools: []netip.Prefix{envCIDR}}})
	mux := httpx.NewMux(nil, slog.New(slog.DiscardHandler))
	New(svc, guard, nil, nil).Mount(mux.ForService("wireguard"))
	return &fixture{h: mux.Handler(), signer: signer, store: st, audit: audit}
}

var manage = map[string][]string{app.PermManage: {"*"}}

func (fx *fixture) token(t *testing.T, scope string, perms map[string][]string, authAge time.Duration) string {
	t.Helper()
	c := authz.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: uuid.NewString()}, Typ: authz.TypeUser, Scope: scope,
		Perms: perms, AuthTime: time.Now().Add(-authAge).Unix()}
	if scope == authz.ScopeTenant {
		c.TID = uuid.NewString()
	}
	tok, _, err := fx.signer.Sign(c)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (fx *fixture) admin(t *testing.T) string {
	return fx.token(t, authz.ScopePlatform, manage, time.Minute)
}

func (fx *fixture) do(method, path, tok, body string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	fx.h.ServeHTTP(w, r)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("json %q: %v", w.Body, err)
	}
	return m
}

func codeOf(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	c, _ := decode(t, w)["code"].(string)
	return c
}

const hubPath = "/api/v1/platform/wireguard/hubs/0192f0d2-0000-7000-8000-000000000001"

// GET /platform/wireguard/hubs: todos los rangos con su ocupación, MTU,
// keepalive y versión; tunnel_cidr sigue siendo el primero.
func TestListHubsShowsAllPools(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	w := fx.do(http.MethodGet, "/api/v1/platform/wireguard/hubs", fx.token(t, authz.ScopePlatform,
		map[string][]string{"platform.status.read": {"*"}}, time.Hour), "")
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}
	data, _ := decode(t, w)["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("data = %v", data)
	}
	h, _ := data[0].(map[string]any)
	if h["tunnel_cidr"] != "10.255.0.0/16" || h["mtu"] != float64(1420) || h["persistent_keepalive_seconds"] != float64(25) ||
		h["version"] != float64(3) || h["addresses_used"] != float64(9) || h["addresses_total"] != float64(65534-255+254) {
		t.Fatalf("hub = %v", h)
	}
	pools, _ := h["pools"].([]any)
	if len(pools) != 2 {
		t.Fatalf("pools = %v", pools)
	}
	p1, _ := pools[1].(map[string]any)
	if p1["id"] != apiPool.String() || p1["cidr"] != "10.254.0.0/24" || p1["addresses_total"] != float64(254) ||
		p1["addresses_used"] != float64(2) || p1["created_at"] != "2026-10-12T13:00:00.000Z" {
		t.Fatalf("pool = %v", p1)
	}
}

func TestPatchHub(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	w := fx.do(http.MethodPatch, hubPath, fx.admin(t), `{"endpoint":"wg.isp.example","mtu":1380,"persistent_keepalive_seconds":0}`,
		"If-Match", `"3"`)
	if w.Code != http.StatusOK || w.Header().Get("ETag") != `"4"` {
		t.Fatalf("patch = %d %s %v", w.Code, w.Body, w.Header())
	}
	h := decode(t, w)
	if h["endpoint"] != "wg.isp.example" || h["mtu"] != float64(1380) || h["persistent_keepalive_seconds"] != float64(0) ||
		h["version"] != float64(4) || h["name"] != "hub-1" {
		t.Fatalf("hub = %v", h)
	}
	if len(fx.audit.entries) != 1 {
		t.Fatalf("audit = %v", fx.audit.entries)
	}
	e := fx.audit.entries[0]
	if e.Action != app.AuditHubUpdated || e.Scope != "platform" || e.TenantID != uuid.Nil || e.ResourceID != hubID.String() ||
		e.Changes["name"] != nil || e.Changes["mtu"] == nil || e.Changes["endpoint"] == nil || e.Changes["persistent_keepalive_seconds"] == nil {
		t.Fatalf("audit entry = %+v", e)
	}
}

func TestPatchHubPreconditions(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	tok := fx.admin(t)
	if w := fx.do(http.MethodPatch, hubPath, tok, `{"mtu":1400}`); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("sin If-Match = %d %s", w.Code, w.Body)
	}
	for _, etag := range []string{`"2"`, `"x"`, `"9"`} {
		w := fx.do(http.MethodPatch, hubPath, tok, `{"mtu":1400}`, "If-Match", etag)
		if w.Code != http.StatusPreconditionFailed || w.Header().Get("ETag") != `"3"` {
			t.Fatalf("If-Match %s = %d %s", etag, w.Code, w.Body)
		}
		cur, _ := decode(t, w)["current"].(map[string]any)
		if cur["version"] != float64(3) || cur["mtu"] != float64(1420) || cur["pools"] == nil {
			t.Fatalf("current = %v", cur)
		}
	}
	if len(fx.store.patched) != 0 || len(fx.audit.entries) != 0 {
		t.Fatal("cambio aplicado sin precondición")
	}
	// Hub desconocido o id mal formado: 404 WIREGUARD_HUB_NOT_FOUND.
	for _, p := range []string{"/api/v1/platform/wireguard/hubs/" + uuid.NewString(), "/api/v1/platform/wireguard/hubs/nope"} {
		if w := fx.do(http.MethodPatch, p, tok, `{"mtu":1400}`, "If-Match", `"3"`); w.Code != http.StatusNotFound ||
			codeOf(t, w) != domain.CodeHubNotFound {
			t.Fatalf("%s = %d %s", p, w.Code, w.Body)
		}
	}
}

func TestPatchHubValidation(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	tok := fx.admin(t)
	for body, field := range map[string]string{
		`{"mtu":1279}`:                                       "mtu",
		`{"mtu":1501}`:                                       "mtu",
		`{"persistent_keepalive_seconds":-1}`:                "persistent_keepalive_seconds",
		`{"persistent_keepalive_seconds":121}`:               "persistent_keepalive_seconds",
		`{"endpoint":"https://horus.example"}`:               "endpoint",
		`{"endpoint":"horus.example:51820"}`:                 "endpoint",
		`{"endpoint":"horus .example"}`:                      "endpoint",
		`{"endpoint":""}`:                                    "endpoint",
		`{"endpoint":"` + strings.Repeat("a.", 127) + `ab"}`: "endpoint",
		`{"name":""}`:                                        "name",
		`{"name":"` + strings.Repeat("n", 65) + `"}`:         "name",
		`{}`:                    "",
		`{"listen_port":51821}`: "listen_port", // el puerto lo publica Docker
	} {
		w := fx.do(http.MethodPatch, hubPath, tok, body, "If-Match", `"3"`)
		if w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d %s", body, w.Code, w.Body)
			continue
		}
		if field != "" && !strings.Contains(w.Body.String(), `"field":"`+field+`"`) {
			t.Errorf("%s: sin error de %s: %s", body, field, w.Body)
		}
	}
	if len(fx.store.patched) != 0 {
		t.Fatal("cambio inválido aplicado")
	}
}

// x-reauth y permiso: sin re-autenticación reciente 403 REAUTH_REQUIRED; sin
// platform.wireguard.manage o con token de tenant 403.
func TestHubAdminRequiresReauthAndPermission(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	stale := fx.token(t, authz.ScopePlatform, manage, 10*time.Minute)
	reader := fx.token(t, authz.ScopePlatform, map[string][]string{"platform.status.read": {"*"}}, time.Minute)
	tenant := fx.token(t, authz.ScopeTenant, manage, time.Minute)
	reqs := []struct{ method, path, body string }{
		{http.MethodPatch, hubPath, `{"mtu":1400}`},
		{http.MethodPost, hubPath + "/pools", `{"cidr":"10.253.0.0/24"}`},
		{http.MethodDelete, hubPath + "/pools/" + apiPool.String(), ""},
	}
	for _, rq := range reqs {
		if w := fx.do(rq.method, rq.path, stale, rq.body, "If-Match", `"3"`); w.Code != http.StatusForbidden || codeOf(t, w) != "REAUTH_REQUIRED" {
			t.Errorf("%s %s sin reauth = %d %s", rq.method, rq.path, w.Code, w.Body)
		}
		if w := fx.do(rq.method, rq.path, reader, rq.body, "If-Match", `"3"`); w.Code != http.StatusForbidden || codeOf(t, w) != "PERMISSION_DENIED" {
			t.Errorf("%s %s sin permiso = %d %s", rq.method, rq.path, w.Code, w.Body)
		}
		if w := fx.do(rq.method, rq.path, tenant, rq.body, "If-Match", `"3"`); w.Code != http.StatusForbidden {
			t.Errorf("%s %s con token de tenant = %d %s", rq.method, rq.path, w.Code, w.Body)
		}
		if w := fx.do(rq.method, rq.path, "", rq.body, "If-Match", `"3"`); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s sin token = %d", rq.method, rq.path, w.Code)
		}
	}
	if len(fx.store.patched)+len(fx.store.added)+len(fx.store.deleted) != 0 || len(fx.audit.entries) != 0 {
		t.Fatal("cambios sin autorización")
	}
}

func TestAddPool(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	tok := fx.admin(t)
	w := fx.do(http.MethodPost, hubPath+"/pools", tok, `{"cidr":"10.253.0.0/24"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("add = %d %s", w.Code, w.Body)
	}
	p := decode(t, w)
	if p["cidr"] != "10.253.0.0/24" || p["addresses_total"] != float64(254) || p["addresses_used"] != float64(0) || p["id"] == nil ||
		p["created_at"] == nil {
		t.Fatalf("pool = %v", p)
	}
	if len(fx.audit.entries) != 1 || fx.audit.entries[0].Action != app.AuditPoolAdded || fx.audit.entries[0].Scope != "platform" ||
		fx.audit.entries[0].Changes["cidr"] != "10.253.0.0/24" {
		t.Fatalf("audit = %+v", fx.audit.entries)
	}
	cases := []struct {
		body   string
		status int
		code   string
	}{
		{`{"cidr":"10.253.0.0/24"}`, http.StatusConflict, domain.CodePoolOverlap},   // ya añadido
		{`{"cidr":"10.255.128.0/24"}`, http.StatusConflict, domain.CodePoolOverlap}, // dentro de un rango
		{`{"cidr":"10.255.0.0/24"}`, http.StatusConflict, domain.CodePoolOverlap},   // red de servicios
		{`{"cidr":"100.64.0.0/16"}`, http.StatusUnprocessableEntity, domain.CodePoolInvalid},
		{`{"cidr":"10.252.0.1/24"}`, http.StatusUnprocessableEntity, domain.CodePoolInvalid},
		{`{"cidr":"10.0.0.0/8"}`, http.StatusUnprocessableEntity, domain.CodePoolInvalid},
		{`{"cidr":"10.252.0.0/30"}`, http.StatusUnprocessableEntity, domain.CodePoolInvalid},
		{`{"cidr":"127.0.0.0/16"}`, http.StatusUnprocessableEntity, domain.CodePoolInvalid},
		{`{"cidr":"169.254.0.0/16"}`, http.StatusUnprocessableEntity, domain.CodePoolInvalid},
		{`{"cidr":"224.1.0.0/16"}`, http.StatusUnprocessableEntity, domain.CodePoolInvalid},
		{`{"cidr":"0.0.0.0/16"}`, http.StatusUnprocessableEntity, domain.CodePoolInvalid},
		{`{"cidr":"fd00::/64"}`, http.StatusUnprocessableEntity, domain.CodePoolInvalid},
		{`{"cidr":"x"}`, http.StatusUnprocessableEntity, domain.CodePoolInvalid},
		{`{}`, http.StatusUnprocessableEntity, "VALIDATION_FAILED"},
		{`{"cidr":"10.252.0.0/24","x":1}`, http.StatusUnprocessableEntity, "VALIDATION_FAILED"},
	}
	for _, c := range cases {
		w := fx.do(http.MethodPost, hubPath+"/pools", tok, c.body)
		if w.Code != c.status || codeOf(t, w) != c.code {
			t.Errorf("%s = %d %s", c.body, w.Code, w.Body)
		}
		if c.code == domain.CodePoolInvalid && !strings.Contains(w.Body.String(), `"field":"cidr"`) {
			t.Errorf("%s: sin error de campo: %s", c.body, w.Body)
		}
	}
	if len(fx.store.added) != 1 {
		t.Fatalf("añadidos = %v", fx.store.added)
	}
	// Carrera con otro alta: el EXCLUDE de la base responde 409 igual.
	fx.store.overlap = true
	if w := fx.do(http.MethodPost, hubPath+"/pools", tok, `{"cidr":"10.252.0.0/24"}`); w.Code != http.StatusConflict ||
		codeOf(t, w) != domain.CodePoolOverlap {
		t.Fatalf("EXCLUDE = %d %s", w.Code, w.Body)
	}
	if w := fx.do(http.MethodPost, "/api/v1/platform/wireguard/hubs/"+uuid.NewString()+"/pools", tok, `{"cidr":"10.252.0.0/24"}`); w.Code !=
		http.StatusNotFound || codeOf(t, w) != domain.CodeHubNotFound {
		t.Fatalf("hub desconocido = %d %s", w.Code, w.Body)
	}
}

func TestDeletePool(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	tok := fx.admin(t)
	// Con direcciones (o en cuarentena): 409 WIREGUARD_POOL_IN_USE.
	fx.store.inUse[apiPool] = true
	w := fx.do(http.MethodDelete, hubPath+"/pools/"+apiPool.String(), tok, "")
	if w.Code != http.StatusConflict || codeOf(t, w) != domain.CodePoolInUse {
		t.Fatalf("en uso = %d %s", w.Code, w.Body)
	}
	if d := time.Since(fx.store.lastSeen); d < 24*time.Hour-time.Minute || d > 24*time.Hour+time.Minute {
		t.Fatalf("cuarentena desde %v", fx.store.lastSeen)
	}
	// Rango de HORUS_WG_TUNNEL_CIDRS (y con la red de servicios): 409.
	if w := fx.do(http.MethodDelete, hubPath+"/pools/"+envPool.String(), tok, ""); w.Code != http.StatusConflict ||
		!strings.Contains(w.Body.String(), "HORUS_WG_TUNNEL_CIDRS") {
		t.Fatalf("rango del entorno = %d %s", w.Code, w.Body)
	}
	for _, p := range []string{uuid.NewString(), "nope"} {
		if w := fx.do(http.MethodDelete, hubPath+"/pools/"+p, tok, ""); w.Code != http.StatusNotFound || codeOf(t, w) != domain.CodePoolNotFound {
			t.Fatalf("rango desconocido = %d %s", w.Code, w.Body)
		}
	}
	if w := fx.do(http.MethodDelete, "/api/v1/platform/wireguard/hubs/"+uuid.NewString()+"/pools/"+apiPool.String(), tok, ""); w.Code !=
		http.StatusNotFound || codeOf(t, w) != domain.CodeHubNotFound {
		t.Fatalf("hub desconocido = %d %s", w.Code, w.Body)
	}
	if len(fx.audit.entries) != 0 {
		t.Fatal("auditado sin borrar")
	}
	fx.store.inUse[apiPool] = false
	if w := fx.do(http.MethodDelete, hubPath+"/pools/"+apiPool.String(), tok, ""); w.Code != http.StatusNoContent {
		t.Fatalf("borrar = %d %s", w.Code, w.Body)
	}
	if len(fx.audit.entries) != 1 || fx.audit.entries[0].Action != app.AuditPoolDelete || fx.audit.entries[0].Changes["cidr"] != "10.254.0.0/24" {
		t.Fatalf("audit = %+v", fx.audit.entries)
	}
	// Último rango: nunca.
	if w := fx.do(http.MethodDelete, hubPath+"/pools/"+envPool.String(), tok, ""); w.Code != http.StatusConflict ||
		!strings.Contains(w.Body.String(), "último rango") {
		t.Fatalf("último rango = %d %s", w.Code, w.Body)
	}
}
