package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

var (
	tenantA = uuid.MustParse("0192e000-0000-7000-8000-000000000001")
	tenantB = uuid.MustParse("0192e000-0000-7000-8000-000000000002")
	siteA   = uuid.MustParse("0192e111-0000-7000-8000-000000000011")
	siteA2  = uuid.MustParse("0192e111-0000-7000-8000-000000000012")
	routerA = uuid.MustParse("0192e333-0000-7000-8000-000000000033")
)

// fakeStore implementa app.CredentialStore y la parte de app.Store que usa
// la confirmación de prefijos.
type fakeStore struct {
	app.Store
	mu       sync.Mutex
	routers  map[uuid.UUID]domain.Router
	creds    map[string]domain.Credential // router/kind
	prefixes map[uuid.UUID]domain.ClientPrefix
	emitted  []outbox.Event
	puts     int
}

func (f *fakeStore) GetRouter(_ context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Router, error) {
	r, ok := f.routers[id]
	if !ok || r.TenantID != t.UUID() {
		return nil, domain.ErrNotFound
	}
	return &r, nil
}

func (f *fakeStore) GetCredential(_ context.Context, _ pgdb.TenantID, routerID uuid.UUID, kind string) (*domain.Credential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.creds[routerID.String()+"/"+kind]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &c, nil
}

func (f *fakeStore) PutCredentials(_ context.Context, _ pgdb.TenantID, routerID uuid.UUID, cs []domain.Credential) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range cs {
		f.creds[routerID.String()+"/"+c.Kind] = c
	}
	f.puts++
	return nil
}

func (f *fakeStore) Emit(_ context.Context, evs []outbox.Event) error {
	f.emitted = append(f.emitted, evs...)
	return nil
}

func (f *fakeStore) GetPrefix(_ context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.ClientPrefix, error) {
	p, ok := f.prefixes[id]
	if !ok || p.TenantID != t.UUID() {
		return nil, domain.ErrNotFound
	}
	return &p, nil
}

func (f *fakeStore) UpdatePrefix(_ context.Context, _ pgdb.TenantID, p *domain.ClientPrefix, expect int, ev app.Events[domain.ClientPrefix]) error {
	cur := f.prefixes[p.ID]
	if cur.Version != expect {
		return app.ErrVersionChanged
	}
	p.Version = expect + 1
	f.prefixes[p.ID] = *p
	f.emitted = append(f.emitted, ev(p)...)
	return nil
}

type memAudit struct{ entries []authapi.AuditEntry }

func (m *memAudit) Record(_ context.Context, e authapi.AuditEntry) error {
	m.entries = append(m.entries, e)
	return nil
}

type fixture struct {
	h      http.Handler
	signer *authz.Signer
	store  *fakeStore
	sealer *envelope.Sealer
	audit  *memAudit
	logs   *bytes.Buffer
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
	st := &fakeStore{routers: map[uuid.UUID]domain.Router{routerA: {ID: routerA, TenantID: tenantA, SiteID: siteA, Version: 1}},
		creds: map[string]domain.Credential{}, prefixes: map[uuid.UUID]domain.ClientPrefix{}}
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sealer := envelope.Ephemeral()
	audit := &memAudit{}
	creds := app.NewCredentials(st, sealer, func() (authapi.AuditRecorder, bool) { return audit, true }, nil, logger)
	mux := httpx.NewMux(nil, logger)
	sm := mux.ForService("devices")
	New(app.NewService(st, nil, nil), guard, "", logger).Mount(sm)
	NewCredentials(creds, guard, logger).Mount(sm)
	return &fixture{h: mux.Handler(), signer: signer, store: st, sealer: sealer, audit: audit, logs: logs}
}

func (fx *fixture) token(t *testing.T, tenant uuid.UUID, perms map[string][]string, authAge time.Duration) string {
	t.Helper()
	tok, _, err := fx.signer.Sign(authz.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: uuid.NewString()},
		Typ: authz.TypeUser, Scope: authz.ScopeTenant, TID: tenant.String(), Perms: perms,
		AuthTime: time.Now().Add(-authAge).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (fx *fixture) do(method, path, tok, body string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	fx.h.ServeHTTP(w, r)
	return w
}

var credsWrite = map[string][]string{app.PermCredentialsWrite: {"*"}}

const apiPassword = "S3cr3t-RouterOS-pw-XYZ"

func credPath(router uuid.UUID, kind string) string {
	return "/api/v1/routers/" + router.String() + "/credentials/" + kind
}

// La credencial de RouterOS se guarda cifrada con su AAD, nunca vuelve en la
// respuesta, ni en los logs, ni en la auditoría.
func TestPutRouterOSCredential(t *testing.T) {
	fx := setup(t)
	tok := fx.token(t, tenantA, credsWrite, time.Minute)
	body := `{"username":"horus-ro","password":"` + apiPassword + `"}`
	w := fx.do(http.MethodPut, credPath(routerA, "routeros_api"), tok, body, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("put = %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), apiPassword) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response leaks secret or is cacheable: %s", w.Body)
	}
	var st map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	if st["kind"] != "routeros_api" || st["configured"] != true || st["updated_at"] == nil || st["last_used_at"] != nil ||
		st["last_result"] != nil {
		t.Fatalf("status %v", st)
	}
	c := fx.store.creds[routerA.String()+"/"+domain.CredentialRouterOSAPI]
	if c.Username != "horus-ro" || c.KEKID == "" || bytes.Contains(c.Ciphertext, []byte(apiPassword)) {
		t.Fatalf("stored credential %+v", c)
	}
	pt, err := fx.sealer.Open(c.Ciphertext, c.DEKWrapped, app.CredentialAAD(tenantA, routerA, domain.CredentialRouterOSAPI))
	if err != nil || string(pt) != apiPassword {
		t.Fatalf("open = %q, %v", pt, err)
	}
	// Ligada a tenant/router/tipo: con otra AAD no se abre.
	if _, err := fx.sealer.Open(c.Ciphertext, c.DEKWrapped, app.CredentialAAD(tenantB, routerA, domain.CredentialRouterOSAPI)); err == nil {
		t.Fatal("credential opened with a foreign AAD")
	}
	if len(fx.audit.entries) != 1 {
		t.Fatalf("audit entries = %d", len(fx.audit.entries))
	}
	e := fx.audit.entries[0]
	raw, _ := json.Marshal(e)
	if e.Action != "devices.credentials.write" || e.ResourceID != routerA.String() || e.TenantID != tenantA ||
		e.Scope != "site:"+siteA.String() || e.Changes["credential_kind"] != domain.CredentialRouterOSAPI ||
		strings.Contains(string(raw), apiPassword) {
		t.Fatalf("audit %s", raw)
	}
	if strings.Contains(fx.logs.String(), apiPassword) {
		t.Fatal("secret logged")
	}
}

func TestPutSNMPCredential(t *testing.T) {
	fx := setup(t)
	tok := fx.token(t, tenantA, credsWrite, time.Minute)
	body := `{"username":"horus-snmp","auth_protocol":"sha256","auth_password":"authpass1","priv_protocol":"aes128","priv_password":"privpass1"}`
	w := fx.do(http.MethodPut, credPath(routerA, "snmp"), tok, body, nil)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "authpass1") {
		t.Fatalf("put = %d %s", w.Code, w.Body)
	}
	c, ok := fx.store.creds[routerA.String()+"/"+domain.CredentialSNMPv3]
	if !ok || c.Username != "horus-snmp" {
		t.Fatalf("stored %+v", c)
	}
	pt, err := fx.sealer.Open(c.Ciphertext, c.DEKWrapped, app.CredentialAAD(tenantA, routerA, domain.CredentialSNMPv3))
	if err != nil {
		t.Fatal(err)
	}
	var sec map[string]string
	if json.Unmarshal(pt, &sec) != nil || sec["auth_protocol"] != "sha256" || sec["auth_password"] != "authpass1" ||
		sec["priv_protocol"] != "aes128" || sec["priv_password"] != "privpass1" {
		t.Fatalf("secret %s", pt)
	}
	var st map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	if st["kind"] != "snmp" {
		t.Fatalf("kind %v", st["kind"])
	}
}

func TestPutCredentialRejects(t *testing.T) {
	fx := setup(t)
	tok := fx.token(t, tenantA, credsWrite, time.Minute)
	okAPI := `{"username":"horus-ro","password":"` + apiPassword + `"}`
	cases := []struct {
		name, path, tok, body string
		code                  int
		problemCode           string
	}{
		{"unknown kind", credPath(routerA, "ssh"), tok, okAPI, http.StatusUnprocessableEntity, "VALIDATION_FAILED"},
		{"short password", credPath(routerA, "routeros_api"), tok, `{"username":"u","password":"tooShort-pw"}`,
			http.StatusUnprocessableEntity, "VALIDATION_FAILED"},
		{"snmp field on api kind", credPath(routerA, "routeros_api"), tok,
			`{"username":"u","password":"` + apiPassword + `","auth_protocol":"sha1"}`, http.StatusUnprocessableEntity, "VALIDATION_FAILED"},
		{"snmp bad protocol", credPath(routerA, "snmp"), tok,
			`{"username":"u","auth_protocol":"md5","auth_password":"authpass1","priv_protocol":"aes128","priv_password":"privpass1"}`,
			http.StatusUnprocessableEntity, "VALIDATION_FAILED"},
		{"snmp missing priv", credPath(routerA, "snmp"), tok, `{"username":"u","auth_protocol":"sha1","auth_password":"authpass1"}`,
			http.StatusUnprocessableEntity, "VALIDATION_FAILED"},
		{"bad fingerprint", credPath(routerA, "routeros_api"), tok,
			`{"username":"u","password":"` + apiPassword + `","tls_fingerprint_sha256":"zz"}`, http.StatusUnprocessableEntity, "VALIDATION_FAILED"},
		{"stale auth", credPath(routerA, "routeros_api"), fx.token(t, tenantA, credsWrite, 10*time.Minute), okAPI,
			http.StatusForbidden, "REAUTH_REQUIRED"},
		{"no permission", credPath(routerA, "routeros_api"), fx.token(t, tenantA, map[string][]string{"devices.update": {"*"}}, time.Minute),
			okAPI, http.StatusForbidden, ""},
		{"other tenant", credPath(routerA, "routeros_api"), fx.token(t, tenantB, credsWrite, time.Minute), okAPI, http.StatusNotFound,
			domain.CodeRouterNotFound},
		{"site out of scope", credPath(routerA, "routeros_api"),
			fx.token(t, tenantA, map[string][]string{app.PermCredentialsWrite: {"site:" + siteA2.String()}}, time.Minute), okAPI,
			http.StatusNotFound, domain.CodeRouterNotFound},
		{"unknown router", credPath(uuid.New(), "routeros_api"), tok, okAPI, http.StatusNotFound, domain.CodeRouterNotFound},
		{"no token", credPath(routerA, "routeros_api"), "", okAPI, http.StatusUnauthorized, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := fx.do(http.MethodPut, tc.path, tc.tok, tc.body, nil)
			if w.Code != tc.code {
				t.Fatalf("code = %d %s", w.Code, w.Body)
			}
			if tc.problemCode != "" && !strings.Contains(w.Body.String(), `"`+tc.problemCode+`"`) {
				t.Fatalf("problem %s", w.Body)
			}
			if strings.Contains(w.Body.String(), apiPassword) || strings.Contains(w.Body.String(), "authpass1") {
				t.Fatalf("secret echoed: %s", w.Body)
			}
		})
	}
	if fx.store.puts != 0 || len(fx.audit.entries) != 0 {
		t.Fatalf("rejected requests wrote: puts=%d audit=%d", fx.store.puts, len(fx.audit.entries))
	}
	if strings.Contains(fx.logs.String(), apiPassword) {
		t.Fatal("secret logged")
	}
}

// Sin tls_fingerprint_sha256 la huella fijada se conserva; null la borra
// (TOFU otra vez); un valor se normaliza a AA:BB:….
func TestPutCredentialFingerprint(t *testing.T) {
	fx := setup(t)
	tok := fx.token(t, tenantA, credsWrite, time.Minute)
	key := routerA.String() + "/" + domain.CredentialRouterOSAPI
	fp := strings.Repeat("ab", 32)
	want := strings.TrimSuffix(strings.Repeat("AB:", 32), ":")
	put := func(extra string) {
		t.Helper()
		w := fx.do(http.MethodPut, credPath(routerA, "routeros_api"), tok, `{"username":"horus-ro","password":"`+apiPassword+`"`+extra+`}`, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("put = %d %s", w.Code, w.Body)
		}
	}
	put(`,"tls_fingerprint_sha256":"` + fp + `"`)
	if c := fx.store.creds[key]; c.TLSFingerprintSHA256 == nil || *c.TLSFingerprintSHA256 != want {
		t.Fatalf("set: %v", c.TLSFingerprintSHA256)
	}
	put("")
	if c := fx.store.creds[key]; c.TLSFingerprintSHA256 == nil || *c.TLSFingerprintSHA256 != want {
		t.Fatalf("kept: %v", c.TLSFingerprintSHA256)
	}
	put(`,"tls_fingerprint_sha256":null`)
	if c := fx.store.creds[key]; c.TLSFingerprintSHA256 != nil {
		t.Fatalf("cleared: %v", *c.TLSFingerprintSHA256)
	}
	if got := fx.audit.entries[0].Changes["tls_fingerprint_sha256"]; got != "set" {
		t.Fatalf("audit fingerprint change = %v", got)
	}
}

func TestConfirmClientPrefix(t *testing.T) {
	fx := setup(t)
	id := uuid.New()
	now := time.Now().UTC()
	fx.store.prefixes[id] = domain.ClientPrefix{ID: id, TenantID: tenantA, SiteID: siteA, RealmID: uuid.New(), RealmKind: "public",
		Prefix: netip.MustParsePrefix("100.64.0.0/22"), Role: "customers", DefaultKind: "residential", AssignmentMode: "dynamic",
		Source: "routeros_api", Confirmed: false, CreatedAt: now, UpdatedAt: now, Version: 3}
	update := map[string][]string{"sites.update": {"*"}}
	tok := fx.token(t, tenantA, update, time.Hour)
	path := "/api/v1/client-prefixes/" + id.String() + "/confirm"

	if w := fx.do(http.MethodPost, path, tok, "", nil); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("no If-Match = %d", w.Code)
	}
	if w := fx.do(http.MethodPost, path, tok, "", map[string]string{"If-Match": `"2"`}); w.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match = %d", w.Code)
	}
	if w := fx.do(http.MethodPost, path, fx.token(t, tenantB, update, time.Hour), "", map[string]string{"If-Match": `"3"`}); w.Code != http.StatusNotFound {
		t.Fatalf("other tenant = %d", w.Code)
	}
	scoped := fx.token(t, tenantA, map[string][]string{"sites.update": {"site:" + siteA2.String()}}, time.Hour)
	if w := fx.do(http.MethodPost, path, scoped, "", map[string]string{"If-Match": `"3"`}); w.Code != http.StatusNotFound {
		t.Fatalf("site out of scope = %d", w.Code)
	}
	if len(fx.store.emitted) != 0 {
		t.Fatal("event emitted on a rejected confirm")
	}

	w := fx.do(http.MethodPost, path, tok, "", map[string]string{"If-Match": `"3"`})
	if w.Code != http.StatusOK || w.Header().Get("ETag") != `"4"` {
		t.Fatalf("confirm = %d %s etag=%s", w.Code, w.Body, w.Header().Get("ETag"))
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["confirmed"] != true || body["version"] != 4.0 || body["prefix"] != "100.64.0.0/22" {
		t.Fatalf("body %v", body)
	}
	if len(fx.store.emitted) != 1 || fx.store.emitted[0].Type != "horus.devices.client_prefix.updated" {
		t.Fatalf("events %+v", fx.store.emitted)
	}
	data, _ := fx.store.emitted[0].Data.(map[string]any)
	if cf, _ := data["changed_fields"].([]string); len(cf) != 1 || cf[0] != "confirmed" || data["confirmed"] != true {
		t.Fatalf("event data %v", data)
	}

	// Ya confirmado: idempotente, sin nueva versión ni evento.
	w = fx.do(http.MethodPost, path, tok, "", map[string]string{"If-Match": `"4"`})
	if w.Code != http.StatusOK || w.Header().Get("ETag") != `"4"` || len(fx.store.emitted) != 1 {
		t.Fatalf("idempotent confirm = %d etag=%s events=%d", w.Code, w.Header().Get("ETag"), len(fx.store.emitted))
	}
}
