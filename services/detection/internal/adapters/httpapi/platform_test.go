package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/app"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
)

type memRepo struct {
	mu   sync.Mutex
	rows map[uuid.UUID]domain.ReputationSource
}

func (m *memRepo) ListReputationSources(context.Context) ([]domain.ReputationSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.ReputationSource
	for _, r := range m.rows {
		out = append(out, r)
	}
	return out, nil
}

func (m *memRepo) GetReputationSource(_ context.Context, id uuid.UUID) (*domain.ReputationSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.rows[id]; ok {
		return &r, nil
	}
	return nil, domain.ErrSourceNotFound
}

func (m *memRepo) InsertReputationSource(_ context.Context, r *domain.ReputationSource) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.rows {
		if x.Key == r.Key {
			return domain.ErrSourceExists
		}
	}
	m.rows[r.ID] = *r
	return nil
}

func (m *memRepo) UpdateReputationSource(_ context.Context, r *domain.ReputationSource, expect int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows[r.ID].Version != expect {
		return domain.ErrSourceStale
	}
	m.rows[r.ID] = *r
	return nil
}

func (m *memRepo) DeleteReputationSource(_ context.Context, id uuid.UUID, _ int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, id)
	return nil
}

func (m *memRepo) SyncCatalog(_ context.Context, rows []domain.ReputationSource) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range rows {
		if _, ok := m.rows[r.ID]; !ok {
			m.rows[r.ID] = r
		}
	}
	return nil
}

type memSync struct {
	mu      sync.Mutex
	pending map[string]bool
}

func (s *memSync) Catalog() ([]datasets.Source, error) {
	return []datasets.Source{{ID: "spamhaus-drop-v4", Name: "Spamhaus DROP", Kind: "reputation", URL: "https://www.spamhaus.org/drop/drop_v4.json",
		Format: "spamhaus-drop-json", CommercialUse: datasets.CommercialYes, Frequency: datasets.Duration(12 * time.Hour),
		Category: "blocklist", Confidence: 80, MinEntries: 100, MaxBytes: 8 << 20}}, nil
}
func (s *memSync) LoadState(string) (datasets.State, error) { return datasets.State{}, nil }
func (s *memSync) Refresh(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[id] = true
}
func (s *memSync) Pending(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending[id]
}
func (s *memSync) Invalidate() {}

func platformSetup(t *testing.T) (http.Handler, *authz.Signer, *memSync) {
	t.Helper()
	key, err := authz.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer := authz.NewSigner(key, "horus-auth", time.Minute, nil)
	ks := authz.KeySet{}
	ks.Add(signer.PublicKey())
	sy := &memSync{pending: map[string]bool{}}
	svc := app.NewReputationSources(app.SourcesOptions{Repo: &memRepo{rows: map[uuid.UUID]domain.ReputationSource{}}, Sync: sy,
		Sealer: envelope.Ephemeral(), Guard: &datasets.EgressGuard{Resolver: noDNS{}}})
	mux := httpx.NewMux(nil, slog.New(slog.DiscardHandler))
	NewPlatform(svc, authz.NewGuard(authz.NewVerifier(ks, "horus-auth", nil)), "https://horus.example", nil).
		Mount(mux.ForService("detection"))
	return mux.Handler(), signer, sy
}

type noDNS struct{}

func (noDNS) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return nil, errors.New("no such host")
}

func platformToken(t *testing.T, s *authz.Signer, scope string, perms map[string][]string) string {
	t.Helper()
	c := authz.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: uuid.NewString()}, Typ: authz.TypeUser, Scope: scope, Perms: perms}
	if scope == authz.ScopeTenant {
		c.TID = uuid.NewString()
	}
	tok, _, err := s.Sign(c)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func call(h http.Handler, method, path, tok, body string, hdr ...string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestPlatformReputationSourcesAPI(t *testing.T) {
	h, s, sy := platformSetup(t)
	manage := map[string][]string{app.PermReputationSources: {"*"}}
	tok := platformToken(t, s, authz.ScopePlatform, manage)

	// Guard: sin token, con token de ISP y sin permiso.
	if w := call(h, "GET", sourcesPath, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token = %d", w.Code)
	}
	if w := call(h, "GET", sourcesPath, platformToken(t, s, authz.ScopeTenant, manage), ""); w.Code != http.StatusForbidden {
		t.Fatalf("token de ISP = %d", w.Code)
	}
	if w := call(h, "GET", sourcesPath, platformToken(t, s, authz.ScopePlatform, map[string][]string{"platform.status.read": {"*"}}), ""); w.Code != http.StatusForbidden {
		t.Fatalf("sin permiso = %d", w.Code)
	}

	w := call(h, "GET", sourcesPath, tok, "")
	var list struct {
		Data []map[string]any `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Data) != 1 || list.Data[0]["key"] != "spamhaus_drop_v4" {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}
	for _, k := range []string{"id", "numeric_id", "key", "origin", "name", "url", "format", "frequency", "category", "confidence",
		"enabled", "commercial_use", "has_auth", "status", "entries", "last_attempt_at", "last_success_at", "last_error",
		"consecutive_failures", "version", "created_at", "updated_at"} {
		if _, ok := list.Data[0][k]; !ok {
			t.Errorf("falta %s en ReputationSource", k)
		}
	}
	if w := call(h, "GET", sourcesPath+"?origin=bogus", tok, ""); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("filtro inválido = %d", w.Code)
	}

	body := `{"key":"csirt_scanners","name":"CSIRT","url":"https://feeds.example.net/s.netset","format":"netset","frequency":"6h",
		"category":"scanner","confidence":70,"auth_header_name":"Auth-Key","auth_header_value":"top-secret","terms_acknowledged":true}`
	w = call(h, "POST", sourcesPath, tok, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "top-secret") {
		t.Fatal("el secreto sale en la respuesta")
	}
	var created map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	id := created["id"].(string)
	if w.Header().Get("Location") != "https://horus.example"+sourcesPath+"/"+id || w.Header().Get("ETag") != `"1"` ||
		created["has_auth"] != true || created["status"] != "pending" || !sy.Pending("custom-csirt_scanners") {
		t.Fatalf("create: %v %v", w.Header(), created)
	}
	if w := call(h, "POST", sourcesPath, tok, body); w.Code != http.StatusConflict {
		t.Fatalf("duplicada = %d", w.Code)
	}
	if w := call(h, "POST", sourcesPath, tok, `{"key":"x_y_z","bogus":1}`); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("campo desconocido = %d", w.Code)
	}
	w = call(h, "POST", sourcesPath, tok, strings.Replace(body, "feeds.example.net", "10.1.1.1", 1))
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), app.CodeSourceURLNotAllowed) {
		t.Fatalf("url privada = %d %s", w.Code, w.Body)
	}

	one := sourcesPath + "/" + id
	if w := call(h, "GET", one, tok, ""); w.Code != http.StatusOK || w.Header().Get("ETag") != `"1"` {
		t.Fatalf("get = %d", w.Code)
	}
	if w := call(h, "GET", sourcesPath+"/"+uuid.NewString(), tok, ""); w.Code != http.StatusNotFound ||
		!strings.Contains(w.Body.String(), app.CodeSourceNotFound) {
		t.Fatalf("get inexistente = %d %s", w.Code, w.Body)
	}
	if w := call(h, "GET", sourcesPath+"/not-a-uuid", tok, ""); w.Code != http.StatusNotFound {
		t.Fatalf("id inválido = %d", w.Code)
	}

	if w := call(h, "PATCH", one, tok, `{"confidence":50}`); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("patch sin If-Match = %d", w.Code)
	}
	if w := call(h, "PATCH", one, tok, `{"confidence":50}`, "If-Match", `"7"`); w.Code != http.StatusPreconditionFailed {
		t.Fatalf("patch If-Match viejo = %d", w.Code)
	}
	w = call(h, "PATCH", one, tok, `{"confidence":50,"notes":null}`, "If-Match", `"1"`, "Content-Type", "application/merge-patch+json")
	if w.Code != http.StatusOK || w.Header().Get("ETag") != `"2"` || !strings.Contains(w.Body.String(), `"confidence":50`) {
		t.Fatalf("patch = %d %s", w.Code, w.Body)
	}

	if w := call(h, "POST", one+"/refresh", tok, ""); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("refresh sin Idempotency-Key = %d", w.Code)
	}
	if w := call(h, "POST", one+"/refresh", tok, "", "Idempotency-Key", uuid.NewString()); w.Code != http.StatusAccepted {
		t.Fatalf("refresh = %d %s", w.Code, w.Body)
	}

	catalogID := list.Data[0]["id"].(string)
	if w := call(h, "DELETE", sourcesPath+"/"+catalogID, tok, "", "If-Match", `"1"`); w.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(w.Body.String(), app.CodeSourceReadOnly) {
		t.Fatalf("borrar catálogo = %d %s", w.Code, w.Body)
	}
	if w := call(h, "DELETE", one, tok, "", "If-Match", `"2"`); w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", w.Code, w.Body)
	}
	if w := call(h, "GET", one, tok, ""); w.Code != http.StatusNotFound {
		t.Fatalf("tras borrar = %d", w.Code)
	}
}
