package edge

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"github.com/hcdestroyer/horus-flow/packages/go/clientip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/archtest"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/gateway/internal/routes"
)

type fakeSessions struct {
	resp  authapi.CheckSessionResponse
	calls int
}

func (f *fakeSessions) CheckSession(context.Context, authapi.CheckSessionRequest) (authapi.CheckSessionResponse, error) {
	f.calls++
	return f.resp, nil
}

type fakeAudit struct{ entries []authapi.AuditEntry }

func (f *fakeAudit) Record(_ context.Context, e authapi.AuditEntry) error {
	f.entries = append(f.entries, e)
	return nil
}

type env struct {
	h        http.Handler
	signer   *authz.Signer
	sessions *fakeSessions
	audit    *fakeAudit
	reached  *int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	key, err := authz.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	table, err := routes.Load()
	if err != nil {
		t.Fatal(err)
	}
	ks := authz.KeySet{}
	ks.Add(key.Public().(ed25519.PublicKey))
	mux := http.NewServeMux()
	reached := 0
	ok := func(w http.ResponseWriter, r *http.Request) {
		reached++
		if authz.FromContext(r.Context()) == nil && !strings.HasPrefix(r.URL.Path, "/api/v1/auth/") {
			t.Errorf("sin principal en %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}
	for _, p := range []string{"GET /api/v1/sites", "POST /api/v1/sites", "GET /api/v1/sites/{site_id}",
		"POST /api/v1/auth/login", "GET /api/v1/me", "GET /api/v1/platform/tenants", "POST /api/v1/platform/tenants"} {
		mux.HandleFunc(p, ok)
	}
	s := &fakeSessions{resp: authapi.CheckSessionResponse{Active: true, MembershipActive: true, TenantActive: true}}
	a := &fakeAudit{}
	e := New(Options{Routes: table, Verifier: authz.NewVerifier(ks, "test", nil), Sessions: s, Audit: a, Local: matcher{mux}})
	return &env{h: e.Middleware(mux), signer: authz.NewSigner(key, "test", time.Minute, nil), sessions: s, audit: a, reached: &reached}
}

type matcher struct{ m *http.ServeMux }

func (m matcher) Matches(r *http.Request) bool { _, p := m.m.Handler(r); return p != "" }

func (e *env) token(t *testing.T, scope string, tid uuid.UUID, perms map[string][]string, mut func(*authz.Claims)) string {
	t.Helper()
	c := authz.Claims{Typ: authz.TypeUser, Scope: scope, SID: uuid.NewString(), Perms: perms, AuthTime: time.Now().Unix()}
	c.Subject = uuid.NewString()
	if tid != uuid.Nil {
		c.TID = tid.String()
	}
	if mut != nil {
		mut(&c)
	}
	tok, _, err := e.signer.Sign(c)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (e *env) do(method, path, token string, body any, hdr map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rd)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	m := map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return w, m
}

func TestEdge(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	tid := uuid.New()
	read := map[string][]string{"sites.read": {"*"}}
	cases := []struct {
		name, method, path, token string
		body                      any
		hdr                       map[string]string
		status                    int
		code                      string
	}{
		{name: "ruta fuera de la tabla", method: "GET", path: "/api/v1/nope", status: 404, code: "NOT_FOUND"},
		{name: "método no declarado", method: "PUT", path: "/api/v1/sites", status: 405, code: "METHOD_NOT_ALLOWED"},
		{name: "sin token", method: "GET", path: "/api/v1/sites", status: 401, code: "UNAUTHENTICATED"},
		{name: "token basura", method: "GET", path: "/api/v1/sites", token: "x.y.z", status: 401, code: "UNAUTHENTICATED"},
		{name: "token de sesión en ruta de ISP", method: "GET", path: "/api/v1/sites", token: e.token(t, "session", uuid.Nil, nil, nil), status: 403, code: "TOKEN_SCOPE_INVALID"},
		{name: "token de plataforma en ruta de ISP", method: "GET", path: "/api/v1/sites", token: e.token(t, "platform", uuid.Nil, map[string][]string{"platform.tenants.read": {"*"}}, nil), status: 403, code: "TOKEN_SCOPE_INVALID"},
		{name: "token de ISP en plataforma", method: "GET", path: "/api/v1/platform/tenants", token: e.token(t, "tenant", tid, read, nil), status: 403, code: "TOKEN_SCOPE_INVALID"},
		{name: "sin permiso", method: "POST", path: "/api/v1/sites", token: e.token(t, "tenant", tid, read, nil), body: map[string]any{"name": "x"}, status: 403, code: "PERMISSION_DENIED"},
		{name: "tenant_id ajeno en el cuerpo", method: "POST", path: "/api/v1/sites", token: e.token(t, "tenant", tid, map[string][]string{"sites.create": {"*"}}, nil), body: map[string]any{"name": "x", "tenant_id": uuid.NewString()}, status: 403, code: "TENANT_MISMATCH"},
		{name: "tenant_id propio en el cuerpo", method: "POST", path: "/api/v1/sites", token: e.token(t, "tenant", tid, map[string][]string{"sites.create": {"*"}}, nil), body: map[string]any{"name": "x", "tenant_id": tid.String()}, status: 204},
		{name: "lectura permitida", method: "GET", path: "/api/v1/sites/" + uuid.NewString(), token: e.token(t, "tenant", tid, read, nil), status: 204},
		{name: "sesión en /me", method: "GET", path: "/api/v1/me", token: e.token(t, "session", uuid.Nil, nil, nil), status: 204},
		{name: "público sin token", method: "POST", path: "/api/v1/auth/login", body: map[string]any{}, status: 204},
		{name: "re-auth antigua", method: "POST", path: "/api/v1/platform/tenants", token: e.token(t, "platform", uuid.Nil, map[string][]string{"platform.tenants.manage": {"*"}}, func(c *authz.Claims) { c.AuthTime = time.Now().Add(-time.Hour).Unix() }), hdr: map[string]string{"Idempotency-Key": uuid.NewString()}, status: 403, code: "REAUTH_REQUIRED"},
		{name: "sin Idempotency-Key", method: "POST", path: "/api/v1/platform/tenants", token: e.token(t, "platform", uuid.Nil, map[string][]string{"platform.tenants.manage": {"*"}}, nil), status: 428, code: "PRECONDITION_REQUIRED"},
		{name: "ruta declarada sin módulo local", method: "GET", path: "/api/v1/routers", token: e.token(t, "tenant", tid, map[string][]string{"devices.read": {"*"}}, nil), status: 503, code: "SERVICE_UNAVAILABLE"},
	}
	for _, tc := range cases {
		w, body := e.do(tc.method, tc.path, tc.token, tc.body, tc.hdr)
		if w.Code != tc.status || (tc.code != "" && body["code"] != tc.code) {
			t.Errorf("%s: %d %v, want %d %s", tc.name, w.Code, body["code"], tc.status, tc.code)
		}
		if w.Code >= 400 && w.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("%s: content-type %q", tc.name, w.Header().Get("Content-Type"))
		}
	}
}

// I0-08 criterio 1: sin ámbito de ISP se rechaza sin consultar la base
// (ni la comprobación de sesión ni el módulo).
func TestScopeRejectedBeforeAnyLookup(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	w, _ := e.do("GET", "/api/v1/sites", e.token(t, "session", uuid.Nil, nil, nil), nil, nil)
	if w.Code != 403 || e.sessions.calls != 0 || *e.reached != 0 {
		t.Fatalf("status %d, session checks %d, module reached %d", w.Code, e.sessions.calls, *e.reached)
	}
}

func TestRevocationAndSuspension(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	tid := uuid.New()
	tok := e.token(t, "tenant", tid, map[string][]string{"sites.read": {"*"}}, nil)
	e.sessions.resp = authapi.CheckSessionResponse{Active: false}
	if w, b := e.do("GET", "/api/v1/sites", tok, nil, nil); w.Code != 401 || b["code"] != "SESSION_REVOKED" {
		t.Fatalf("revocada: %d %v", w.Code, b)
	}
	e.sessions.resp = authapi.CheckSessionResponse{Active: true, MembershipActive: true, TenantActive: false}
	if w, b := e.do("GET", "/api/v1/sites", tok, nil, nil); w.Code != 403 || b["code"] != "TENANT_SUSPENDED" {
		t.Fatalf("suspendido: %d %v", w.Code, b)
	}
	e.sessions.resp = authapi.CheckSessionResponse{Active: true, MembershipActive: false, TenantActive: true}
	if w, _ := e.do("GET", "/api/v1/sites", tok, nil, nil); w.Code != 403 {
		t.Fatalf("membresía revocada: %d", w.Code)
	}
}

// I0-08 criterio 4: el acceso de plataforma a datos de un ISP queda auditado.
func TestPlatformAccessAudited(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	tid, site := uuid.New(), uuid.NewString()
	tok := e.token(t, "tenant", tid, map[string][]string{"sites.read": {"*"}}, func(c *authz.Claims) { c.ViaPlatform = true })
	if w, _ := e.do("GET", "/api/v1/sites/"+site, tok, nil, nil); w.Code != 204 {
		t.Fatalf("status %d", w.Code)
	}
	if len(e.audit.entries) != 1 {
		t.Fatalf("auditoría = %+v", e.audit.entries)
	}
	a := e.audit.entries[0]
	if a.TenantID != tid || !a.ViaPlatform || a.ResourceID != site || a.Action != "platform.tenant_data.accessed" || a.ActorID == "" {
		t.Fatalf("entrada = %+v", a)
	}
}

func TestRateLimit(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	var last int
	for range 21 {
		w, _ := e.do("POST", "/api/v1/auth/login", "", map[string]any{}, nil)
		last = w.Code
	}
	if last != 429 {
		t.Fatalf("21.º login por IP = %d, quiero 429", last)
	}
}

// La tabla embebida es copia exacta del contrato C5.
func TestRouteTableMatchesContract(t *testing.T) {
	t.Parallel()
	root, err := archtest.FindRepoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(root, "packages/schemas/openapi/v0/gateway-routes.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, routes.TableYAML()) {
		t.Fatal("services/gateway/internal/routes/gateway-routes.v0.yaml difiere del contrato: cópielo de packages/schemas/openapi/v0/gateway-routes.yaml")
	}
}

// Detrás de Traefik (o del proxy inverso del modo TLS externo) el rate limit
// cuenta por la IP REAL del cliente (X-Forwarded-For desde un proxy de
// confianza) y una cabecera falsificada por el cliente no lo esquiva.
func TestRateLimitRealClientIP(t *testing.T) {
	prev := clientip.Default()
	r, err := clientip.Parse("192.0.2.1") // RemoteAddr de httptest = el proxy de confianza
	if err != nil {
		t.Fatal(err)
	}
	clientip.SetDefault(r)
	t.Cleanup(func() { clientip.SetDefault(prev) })
	e := newEnv(t)
	login := func(xff string) int {
		w, _ := e.do("POST", "/api/v1/auth/login", "", map[string]any{}, map[string]string{"X-Forwarded-For": xff})
		return w.Code
	}
	for i := range 20 {
		if c := login("198.51.100.7"); c == 429 {
			t.Fatalf("login %d del cliente A limitado antes de tiempo", i+1)
		}
	}
	// El cliente A falsifica X-Forwarded-For: el proxy añade su IP real a la derecha.
	if c := login("203.0.113.99, 198.51.100.7"); c != 429 {
		t.Fatalf("21.º login de A con XFF falsificado = %d, quiero 429", c)
	}
	if c := login("198.51.100.8"); c == 429 {
		t.Fatal("el cliente B no debe heredar el límite de A (antes todos compartían la IP del proxy)")
	}
}
