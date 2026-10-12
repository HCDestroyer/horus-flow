package remote

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
)

type httpEnv struct {
	h      http.Handler
	signer *authz.Signer
	logs   *bytes.Buffer
	audit  *fakeAudit
	repo   *memRepo
}

func newHTTPEnv(t *testing.T) *httpEnv {
	t.Helper()
	key, err := authz.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer := authz.NewSigner(key, "horus-auth", time.Hour, nil)
	ks := authz.KeySet{}
	ks.Add(signer.PublicKey())
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	idn, _ := testIdentity(t)
	repo := newMemRepo()
	audit := &fakeAudit{}
	svc, err := NewService(Options{Repo: repo, Sealer: envelope.Ephemeral(), Recipient: idn.Recipient(), Root: t.TempDir(),
		TestTimeout: 5 * time.Second, Logger: logger, Audit: func() (authapi.AuditRecorder, bool) { return audit, true }})
	if err != nil {
		t.Fatal(err)
	}
	mux := httpx.NewMux(nil, logger)
	NewHandler(svc, authz.NewGuard(authz.NewVerifier(ks, "horus-auth", nil)), "https://horus.example", logger).Mount(mux.ForService("jobs"))
	return &httpEnv{h: mux.Handler(), signer: signer, logs: logs, audit: audit, repo: repo}
}

func (e *httpEnv) token(t *testing.T, scope string, perms map[string][]string, authAge time.Duration) string {
	t.Helper()
	c := authz.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: uuid.NewString()}, Typ: authz.TypeUser, Scope: scope, Perms: perms,
		AuthTime: time.Now().Add(-authAge).Unix()}
	if scope == authz.ScopeTenant {
		c.TID = uuid.NewString()
	}
	tok, _, err := e.signer.Sign(c)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

var manage = map[string][]string{PermManage: {"*"}}

type reqOpt func(*http.Request)

func ifMatch(v int) reqOpt {
	return func(r *http.Request) { r.Header.Set("If-Match", `"`+strconv.Itoa(v)+`"`) }
}

func header(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }

func (e *httpEnv) do(t *testing.T, method, path, tok, body string, opts ...reqOpt) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	for _, o := range opts {
		o(r)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func code(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var p struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	return p.Code
}

func TestHandlersAllRoutes(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	tok := e.token(t, authz.ScopePlatform, manage, 0)
	old := e.token(t, authz.ScopePlatform, manage, time.Hour)
	const base = "/api/v1/platform/remote-destinations"

	// Permisos y alcance.
	if w := e.do(t, "GET", base, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token = %d", w.Code)
	}
	if w := e.do(t, "GET", base, e.token(t, authz.ScopeTenant, manage, 0), ""); w.Code != http.StatusForbidden {
		t.Fatalf("token de tenant = %d", w.Code)
	}
	if w := e.do(t, "GET", base, e.token(t, authz.ScopePlatform, map[string][]string{"platform.status.read": {"*"}}, 0), ""); w.Code != http.StatusForbidden {
		t.Fatalf("sin permiso = %d", w.Code)
	}
	// Cero destinos es válido.
	if w := e.do(t, "GET", base, tok, ""); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"data":[]}` {
		t.Fatalf("lista vacía = %d %s", w.Code, w.Body)
	}

	// Alta: reauth, validación, 201 con Location y ETag, nombre repetido.
	body := `{"kind":"sftp","name":"NAS","host":"127.0.0.1","port":2222,"username":"horus","path":"/copias"}`
	if w := e.do(t, "POST", base, old, body); w.Code != http.StatusForbidden || code(t, w) != "REAUTH_REQUIRED" {
		t.Fatalf("alta sin reauth = %d %s", w.Code, w.Body)
	}
	if w := e.do(t, "POST", base, tok, `{"kind":"sftp","name":"x","host":"sftp://x","username":"u","path":"../x"}`); w.Code != 422 {
		t.Fatalf("alta inválida = %d", w.Code)
	}
	if w := e.do(t, "POST", base, tok, `{"kind":"sftp","name":"x","host":"h","username":"u","path":"p","extra":1}`); w.Code != 422 {
		t.Fatalf("campo desconocido = %d", w.Code)
	}
	w := e.do(t, "POST", base, tok, body)
	if w.Code != http.StatusCreated || w.Header().Get("ETag") != `"1"` || !strings.HasPrefix(w.Header().Get("Location"), "https://horus.example"+base+"/") {
		t.Fatalf("alta = %d %s %v", w.Code, w.Body, w.Header())
	}
	var v View
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Status != StatusDisabled || v.Enabled || v.HasCredentials || v.HostKeyFingerprint != nil || v.RetentionDays != 30 || v.Port != 2222 {
		t.Fatalf("destino = %+v", v)
	}
	if w := e.do(t, "POST", base, tok, strings.Replace(body, `"NAS"`, `"nas"`, 1)); w.Code != http.StatusConflict {
		t.Fatalf("nombre repetido = %d", w.Code)
	}
	item := base + "/" + v.ID.String()

	// PATCH: If-Match obligatorio, 412 con versión vieja, 200 con ETag nuevo.
	if w := e.do(t, "PATCH", item, tok, `{"name":"NAS 2"}`); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("sin If-Match = %d", w.Code)
	}
	if w := e.do(t, "PATCH", item, tok, `{"name":"NAS 2"}`, ifMatch(9)); w.Code != http.StatusPreconditionFailed {
		t.Fatalf("If-Match viejo = %d", w.Code)
	}
	if w := e.do(t, "PATCH", item, old, `{"name":"NAS 2"}`, ifMatch(1)); w.Code != http.StatusForbidden {
		t.Fatalf("PATCH sin reauth = %d", w.Code)
	}
	if w := e.do(t, "PATCH", item, tok, `{"enabled":true}`, ifMatch(1)); w.Code != http.StatusConflict || code(t, w) != CodeNotReady {
		t.Fatalf("habilitar sin credenciales = %d %s", w.Code, w.Body)
	}
	if w := e.do(t, "PATCH", item, tok, `{"name":"NAS 2"}`, ifMatch(1)); w.Code != 200 || w.Header().Get("ETag") != `"2"` {
		t.Fatalf("PATCH = %d %s", w.Code, w.Body)
	}
	if w := e.do(t, "PATCH", base+"/"+uuid.NewString(), tok, `{"name":"x"}`, ifMatch(1)); w.Code != 404 || code(t, w) != CodeNotFound {
		t.Fatalf("PATCH inexistente = %d %s", w.Code, w.Body)
	}
	if w := e.do(t, "PATCH", base+"/no-uuid", tok, `{"name":"x"}`, ifMatch(1)); w.Code != 404 {
		t.Fatalf("PATCH id inválido = %d", w.Code)
	}

	// Credenciales write-only: nunca en la respuesta, en los logs ni en la auditoría.
	const pw = "s3cr3t-pw-NO-LOG"
	keyPEM, _ := clientKeyPEM(t, "frase-NO-LOG")
	if w := e.do(t, "PUT", item+"/credentials", old, `{"password":"`+pw+`"}`); w.Code != http.StatusForbidden {
		t.Fatalf("credenciales sin reauth = %d", w.Code)
	}
	if w := e.do(t, "PUT", item+"/credentials", tok, `{}`); w.Code != 422 {
		t.Fatalf("credenciales vacías = %d", w.Code)
	}
	if w := e.do(t, "PUT", item+"/credentials", tok, `{"private_key":"basura `+pw+`"}`); w.Code != 422 || strings.Contains(w.Body.String(), pw) {
		t.Fatalf("clave inválida = %d %s", w.Code, w.Body)
	}
	kb, _ := json.Marshal(map[string]string{"password": pw, "private_key": keyPEM, "passphrase": "frase-NO-LOG"})
	w = e.do(t, "PUT", item+"/credentials", tok, string(kb))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"has_credentials":true`) {
		t.Fatalf("credenciales = %d %s", w.Code, w.Body)
	}
	for _, secret := range []string{pw, "frase-NO-LOG", "PRIVATE KEY"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("la respuesta contiene un secreto")
		}
	}

	// Prueba contra un servidor SFTP en proceso (TOFU).
	signer := newHostSigner(t)
	srv := startSFTP(t, signer, "horus", pw, nil)
	d, _ := e.repo.Get(t.Context(), v.ID)
	w = e.do(t, "PATCH", item, tok, `{"port":`+strconv.Itoa(srv.port)+`,"path":"`+filepath.ToSlash(filepath.Join(t.TempDir(), "r"))+`"}`, ifMatch(d.Version))
	if w.Code != 200 {
		t.Fatalf("PATCH puerto = %d %s", w.Code, w.Body)
	}
	if w := e.do(t, "POST", item+"/test", old, ""); w.Code != http.StatusForbidden {
		t.Fatalf("test sin reauth = %d", w.Code)
	}
	w = e.do(t, "POST", item+"/test", tok, "")
	var tr TestResult
	_ = json.Unmarshal(w.Body.Bytes(), &tr)
	fp := Fingerprint(signer.PublicKey())
	if w.Code != 200 || tr.OK || tr.Stage != StageHostKey || tr.HostKeyFingerprint == nil || *tr.HostKeyFingerprint != fp {
		t.Fatalf("test sin confirmar = %d %s", w.Code, w.Body)
	}
	w = e.do(t, "POST", item+"/test", tok, `{"confirm_host_key":"`+fp+`"}`)
	_ = json.Unmarshal(w.Body.Bytes(), &tr)
	if w.Code != 200 || !tr.OK || tr.Stage != StageDone || !tr.HostKeyConfirmed || tr.Destination == nil ||
		tr.Destination.HostKeyFingerprint == nil || *tr.Destination.HostKeyFingerprint != fp {
		t.Fatalf("test confirmado = %d %s", w.Code, w.Body)
	}
	ver := tr.Destination.Version

	// Sync: Idempotency-Key obligatoria; no lista mientras esté deshabilitado.
	if w := e.do(t, "POST", item+"/sync", tok, ""); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("sync sin Idempotency-Key = %d", w.Code)
	}
	if w := e.do(t, "POST", item+"/sync", tok, "", header("Idempotency-Key", uuid.NewString())); w.Code != 409 || code(t, w) != CodeNotReady {
		t.Fatalf("sync deshabilitado = %d %s", w.Code, w.Body)
	}
	if w := e.do(t, "PATCH", item, tok, `{"enabled":true}`, ifMatch(ver)); w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Fatalf("habilitar = %d %s", w.Code, w.Body)
	}
	key := uuid.NewString()
	if w := e.do(t, "POST", item+"/sync", tok, "", header("Idempotency-Key", key)); w.Code != http.StatusAccepted {
		t.Fatalf("sync = %d %s", w.Code, w.Body)
	}
	if w := e.do(t, "POST", item+"/sync", tok, "", header("Idempotency-Key", key)); w.Code != http.StatusAccepted {
		t.Fatalf("sync repetido = %d %s", w.Code, w.Body)
	}
	if w := e.do(t, "POST", item+"/sync", tok, "", header("Idempotency-Key", uuid.NewString())); w.Code != 409 || code(t, w) != CodeSyncRunning {
		t.Fatalf("sync en curso = %d %s", w.Code, w.Body)
	}

	// Cambiar el host borra la huella y deshabilita.
	d, _ = e.repo.Get(t.Context(), v.ID)
	w = e.do(t, "PATCH", item, tok, `{"host":"localhost"}`, ifMatch(d.Version))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"host_key_fingerprint":null`) || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("cambio de host = %d %s", w.Code, w.Body)
	}

	// Listado con los campos del contrato.
	w = e.do(t, "GET", base, tok, "")
	var list struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Data) != 1 {
		t.Fatalf("lista = %s", w.Body)
	}
	for _, f := range []string{"id", "kind", "name", "status", "host", "port", "username", "path", "enabled", "has_credentials",
		"host_key_fingerprint", "retention_days", "last_success_at", "last_attempt_at", "last_error", "lag_seconds",
		"files_pending", "bytes_pending", "version"} {
		if _, ok := list.Data[0][f]; !ok {
			t.Errorf("falta %s en RemoteDestination", f)
		}
	}
	for k := range list.Data[0] {
		if (strings.Contains(k, "credential") && k != "has_credentials") || k == "password" || k == "private_key" {
			t.Errorf("campo secreto %s en la respuesta", k)
		}
	}

	// Borrado: If-Match y 204.
	d, _ = e.repo.Get(t.Context(), v.ID)
	if w := e.do(t, "DELETE", item, tok, ""); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("DELETE sin If-Match = %d", w.Code)
	}
	if w := e.do(t, "DELETE", item, tok, "", ifMatch(d.Version+1)); w.Code != http.StatusPreconditionFailed {
		t.Fatalf("DELETE viejo = %d", w.Code)
	}
	if w := e.do(t, "DELETE", item, old, "", ifMatch(d.Version)); w.Code != http.StatusForbidden {
		t.Fatalf("DELETE sin reauth = %d", w.Code)
	}
	if w := e.do(t, "DELETE", item, tok, "", ifMatch(d.Version)); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", w.Code, w.Body)
	}
	if w := e.do(t, "DELETE", item, tok, "", ifMatch(d.Version)); w.Code != 404 {
		t.Fatalf("DELETE repetido = %d", w.Code)
	}

	// Ningún secreto en logs ni en la auditoría.
	logs := e.logs.String()
	auditJSON, _ := json.Marshal(e.audit.entries)
	for _, secret := range []string{pw, "frase-NO-LOG", "PRIVATE KEY"} {
		if strings.Contains(logs, secret) || strings.Contains(string(auditJSON), secret) {
			t.Fatalf("secreto %q en logs o auditoría", secret)
		}
	}
	want := map[string]bool{AuditCreated: false, AuditUpdated: false, AuditCredentialsSet: false, AuditTested: false,
		AuditSyncRequested: false, AuditDeleted: false}
	for _, a := range e.audit.actions() {
		want[a] = true
	}
	for a, seen := range want {
		if !seen {
			t.Errorf("falta la auditoría %s", a)
		}
	}
	for _, en := range e.audit.entries {
		if en.Scope != "platform" || en.ResourceType != "remote_destination" {
			t.Errorf("auditoría fuera de la cadena de plataforma: %+v", en)
		}
	}
}
