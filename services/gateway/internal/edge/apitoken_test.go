package edge

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/gateway/internal/routes"
)

// fakeExchanger canjea un único hf_pat_ por un access token de API token.
type fakeExchanger struct {
	raw    string
	signer *authz.Signer
	tid    uuid.UUID
	tokID  uuid.UUID
	calls  int
}

func (f *fakeExchanger) ExchangeAPIToken(_ context.Context, raw, _ string) (string, error) {
	f.calls++
	if raw != f.raw {
		return "", authapi.ErrAPITokenInvalid
	}
	c := authz.Claims{Typ: authz.TypeService, Scope: authz.ScopeTenant, TID: f.tid.String(), Via: authz.ViaAPIToken,
		APITokenID: f.tokID.String(), Perms: map[string][]string{"sites.read": {"*"}, "api_tokens.manage": {"*"}}}
	c.Subject = uuid.NewString()
	tok, _, err := f.signer.Sign(c)
	return tok, err
}

func TestEdgeAPIToken(t *testing.T) {
	t.Parallel()
	key, _ := authz.GenerateKey()
	table, err := routes.Load()
	if err != nil {
		t.Fatal(err)
	}
	ks := authz.KeySet{}
	ks.Add(key.Public().(ed25519.PublicKey))
	signer := authz.NewSigner(key, "test", time.Minute, nil)
	ex := &fakeExchanger{raw: "hf_pat_valido", signer: signer, tid: uuid.New(), tokID: uuid.New()}
	var seenAuth string
	var seen *authz.Principal
	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, r *http.Request) {
		seenAuth, seen = r.Header.Get("Authorization"), authz.FromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}
	for _, p := range []string{"GET /api/v1/sites", "GET /api/v1/api-tokens", "GET /api/v1/routers/status", "GET /api/v1/infra-services"} {
		mux.HandleFunc(p, ok)
	}
	sessions := &fakeSessions{resp: authapi.CheckSessionResponse{Active: true, MembershipActive: true, TenantActive: true}}
	limits := map[string]int{RateLimitAPIToken: 2}
	e := New(Options{Routes: table, Verifier: authz.NewVerifier(ks, "test", nil), Sessions: sessions, APITokens: ex,
		Local: matcher{mux}, RateLimits: limits})
	h := e.Middleware(mux)
	do := func(path, tok string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	// Ruta con api_token en x-principals: canje, Bearer sustituido, principal de API token sin consulta de sesión.
	if w := do("/api/v1/sites", "hf_pat_valido"); w.Code != http.StatusNoContent {
		t.Fatalf("sites = %d %s", w.Code, w.Body)
	}
	if seenAuth == "Bearer hf_pat_valido" || seen == nil || !seen.IsAPIToken() || seen.APITokenID != ex.tokID || seen.TenantID != ex.tid {
		t.Fatalf("el módulo debe recibir el access token canjeado: %q %+v", seenAuth, seen)
	}
	if sessions.calls != 0 {
		t.Fatal("un API token no tiene sesión que comprobar")
	}
	// Ruta cuyo x-principals excluye api_token → 403 aunque tenga el permiso.
	if w := do("/api/v1/api-tokens", "hf_pat_valido"); w.Code != http.StatusForbidden {
		t.Fatalf("api-tokens con API token = %d", w.Code)
	}
	// Token desconocido o revocado → 401.
	if w := do("/api/v1/sites", "hf_pat_otro"); w.Code != http.StatusUnauthorized {
		t.Fatalf("token desconocido = %d", w.Code)
	}
	// Cupo por token (2/min en el test; la ruta rechazada por x-principals no cuenta).
	if w := do("/api/v1/sites", "hf_pat_valido"); w.Code != http.StatusNoContent {
		t.Fatalf("2.º uso = %d", w.Code)
	}
	if w := do("/api/v1/sites", "hf_pat_valido"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("3.er uso = %d (cupo por token)", w.Code)
	}
	// Sin auth en el proceso → 503 (fail-closed).
	e2 := New(Options{Routes: table, Verifier: authz.NewVerifier(ks, "test", nil), Sessions: sessions, Local: matcher{mux}})
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sites", nil)
	r.Header.Set("Authorization", "Bearer hf_pat_valido")
	w := httptest.NewRecorder()
	e2.Middleware(mux).ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("sin canjeador = %d", w.Code)
	}
}

// H4 (contrato ArgosISP §3.3): GET /routers/status y /infra-services admiten API tokens.
func TestRoutesAcceptAPITokenForArgos(t *testing.T) {
	t.Parallel()
	table, err := routes.Load()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"GET /api/v1/routers/status": false, "GET /api/v1/infra-services": false,
		"POST /api/v1/integrations/ip-lookup": false, "POST /api/v1/integrations/consumption": false}
	for _, rt := range table {
		k := rt.Method + " " + rt.Prefix
		if _, ok := want[k]; ok {
			want[k] = true
			found := false
			for _, p := range rt.Principals {
				found = found || p == "api_token"
			}
			if !found {
				t.Errorf("%s no admite api_token: %v", k, rt.Principals)
			}
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("%s no está en la tabla", k)
		}
	}
}
