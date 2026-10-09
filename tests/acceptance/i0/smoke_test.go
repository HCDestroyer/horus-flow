//go:build acceptance

// Package i0 es el e2e de humo del incremento 0 (historia I0-19) contra el
// backend REAL: el binario horus del compose (perfil `app`) detrás de Traefik.
// Lo ejecuta scripts/accept/accept-i0.sh; a mano:
//
//	ACCEPT_BASE_URL=http://127.0.0.1:8000 \
//	ACCEPT_ADMIN_EMAIL=admin@horus.localhost \
//	ACCEPT_ADMIN_PASSWORD_FILE=deployments/compose/secrets/seed_admin_password.txt \
//	go test -tags acceptance -count=1 -v ./tests/acceptance/i0/
//
// Necesita una base recién creada: el primer login del superadministrador
// semilla cambia su contraseña y activa TOTP (no se puede repetir).
package i0

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // TOTP RFC 6238 usa HMAC-SHA1
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// client habla con la API pública /api/v1 del gateway.
type client struct {
	t    *testing.T
	base string
	http *http.Client
}

type resp struct {
	Status int
	Body   map[string]any
	Raw    []byte
	Header http.Header
}

func (r resp) str(k string) string { s, _ := r.Body[k].(string); return s }
func (r resp) code() string        { return r.str("code") }

type call struct {
	Method, Path, Token string
	Body                any
	Header              map[string]string
}

func (c *client) do(r call) resp {
	c.t.Helper()
	var body io.Reader
	if r.Body != nil {
		b, err := json.Marshal(r.Body)
		if err != nil {
			c.t.Fatal(err)
		}
		body = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, r.Method, c.base+r.Path, body)
	if err != nil {
		c.t.Fatal(err)
	}
	if r.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	}
	for k, v := range r.Header {
		req.Header.Set(k, v)
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", r.Method, r.Path, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	out := resp{Status: res.StatusCode, Raw: raw, Header: res.Header, Body: map[string]any{}}
	_ = json.Unmarshal(raw, &out.Body)
	return out
}

// expect falla si el estado no es el esperado, con la respuesta completa.
func (c *client) expect(what string, r resp, status int) resp {
	c.t.Helper()
	if r.Status != status {
		c.t.Fatalf("FALLO %s: HTTP %d, se esperaba %d\n%s", what, r.Status, status, r.Raw)
	}
	c.t.Logf("ok  %s (HTTP %d)", what, r.Status)
	return r
}

func (c *client) post(token, path string, body any) resp {
	return c.do(call{Method: http.MethodPost, Path: path, Token: token, Body: body})
}

func (c *client) get(token, path string) resp {
	return c.do(call{Method: http.MethodGet, Path: path, Token: token})
}

// totp calcula el código TOTP de un secreto base32 (RFC 6238, SHA-1, 6 dígitos, 30 s).
func totp(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimRight(secret, "=")))
	if err != nil {
		t.Fatalf("secreto TOTP no es base32: %v", err)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(at.Unix()/30)) //nolint:gosec // instante positivo
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[off:off+4])&0x7fffffff)%1_000_000)
}

// waitNextStep espera al siguiente paso TOTP: auth rechaza reutilizar un paso ya usado.
func waitNextStep(t *testing.T) {
	t.Helper()
	d := time.Duration(30-time.Now().Unix()%30)*time.Second + 500*time.Millisecond
	t.Logf("..  esperando %s al siguiente paso TOTP (anti-reutilización)", d.Round(time.Second))
	time.Sleep(d)
}

func env(t *testing.T, key, def string) string {
	t.Helper()
	if v := os.Getenv(key); v != "" {
		return v
	}
	if def == "" {
		t.Fatalf("falta la variable %s", key)
	}
	return def
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// TestSmokeI0 recorre la demostración del incremento 0 contra el backend real.
func TestSmokeI0(t *testing.T) {
	c := &client{t: t, base: strings.TrimRight(env(t, "ACCEPT_BASE_URL", "http://127.0.0.1:8000"), "/"),
		http: &http.Client{Timeout: 30 * time.Second}}
	email := env(t, "ACCEPT_ADMIN_EMAIL", "admin@horus.localhost")
	pwFile := env(t, "ACCEPT_ADMIN_PASSWORD_FILE", "")
	raw, err := os.ReadFile(pwFile) //nolint:gosec // ruta de configuración del test
	if err != nil {
		t.Fatalf("no se puede leer la contraseña semilla: %v", err)
	}
	seedPassword := strings.TrimSpace(string(raw))
	newPassword := "accept-i0-" + randomSuffix(t) + "-clave-larga"
	suffix := randomSuffix(t)

	// --- Borde del gateway (I0-04/I0-08): sin token, 401 problem+json ------------------------
	if r := c.expect("GET /system/status sin token → 401", c.get("", "/api/v1/system/status"), 401); r.code() != "UNAUTHENTICATED" {
		t.Fatalf("FALLO se esperaba problem+json UNAUTHENTICATED: %s", r.Raw)
	}

	// --- Login del superadministrador semilla (I0-06/I0-07) -----------------------------------
	c.expect("ruta de ISP sin token → 401", c.get("", "/api/v1/sites"), 401)
	bad := c.expect("login con contraseña errónea → 401",
		c.post("", "/api/v1/auth/login", map[string]string{"username": email, "password": seedPassword + "x"}), 401)
	if strings.Contains(string(bad.Raw), email) {
		t.Fatalf("FALLO el error de login revela el usuario: %s", bad.Raw)
	}
	login := c.expect("login del superadmin semilla",
		c.post("", "/api/v1/auth/login", map[string]string{"username": email, "password": seedPassword}), 200)
	sess := login.str("access_token")
	if sess == "" || login.Body["mfa_required"] == true {
		t.Fatalf("FALLO el primer login debe devolver un token de sesión sin 2FA: %s", login.Raw)
	}
	// Estado del sistema (gateway): con la sesión, auth e inventario disponibles.
	st := c.expect("GET /system/status", c.get(sess, "/api/v1/system/status"), 200)
	caps, _ := st.Body["capabilities"].(map[string]any)
	if st.str("status") != "ok" || caps["auth"] != "ok" || caps["inventory"] != "ok" {
		t.Fatalf("FALLO /system/status: se esperaba status=ok con auth e inventory en ok: %s", st.Raw)
	}
	pt := c.post(sess, "/api/v1/auth/token", map[string]string{"scope": "platform"})
	if pt.Status != 403 || pt.code() != "PASSWORD_CHANGE_REQUIRED" {
		t.Fatalf("FALLO sin cambiar la contraseña semilla, el token de plataforma debe dar 403 PASSWORD_CHANGE_REQUIRED: HTTP %d %s", pt.Status, pt.Raw)
	}
	t.Logf("ok  token de plataforma bloqueado hasta cambiar la contraseña (403 PASSWORD_CHANGE_REQUIRED)")
	c.expect("cambio de contraseña obligatorio", c.post(sess, "/api/v1/me/password",
		map[string]string{"current_password": seedPassword, "new_password": newPassword}), 204)
	if r := c.post(sess, "/api/v1/auth/token", map[string]string{"scope": "platform"}); r.Status != 403 || r.code() != "MFA_ENROLLMENT_REQUIRED" {
		t.Fatalf("FALLO sin TOTP, el token de plataforma debe dar 403 MFA_ENROLLMENT_REQUIRED: HTTP %d %s", r.Status, r.Raw)
	}
	t.Logf("ok  token de plataforma exige alta de TOTP (403 MFA_ENROLLMENT_REQUIRED)")

	// --- Enrolamiento TOTP (D14) --------------------------------------------------------------
	enr := c.expect("POST /me/totp/enroll", c.post(sess, "/api/v1/me/totp/enroll", nil), 200)
	secret := enr.str("secret")
	if secret == "" || !strings.HasPrefix(enr.str("otpauth_uri"), "otpauth://totp/") {
		t.Fatalf("FALLO enroll sin secreto u otpauth_uri: %s", enr.Raw)
	}
	conf := c.expect("POST /me/totp/confirm", c.post(sess, "/api/v1/me/totp/confirm",
		map[string]string{"code": totp(t, secret, time.Now())}), 200)
	if codes, _ := conf.Body["recovery_codes"].([]any); len(codes) == 0 {
		t.Fatalf("FALLO confirm sin códigos de recuperación: %s", conf.Raw)
	}

	// Nuevo login: ahora pide el segundo factor.
	waitNextStep(t)
	l2 := c.expect("login con la contraseña nueva",
		c.post("", "/api/v1/auth/login", map[string]string{"username": email, "password": newPassword}), 200)
	if l2.Body["mfa_required"] != true || l2.str("mfa_token") == "" {
		t.Fatalf("FALLO con TOTP activo el login debe pedir 2FA: %s", l2.Raw)
	}
	c.expect("código TOTP erróneo → 401", c.post("", "/api/v1/auth/mfa/verify",
		map[string]string{"mfa_token": l2.str("mfa_token"), "code": "000000"}), 401)
	mfa := c.expect("POST /auth/mfa/verify", c.post("", "/api/v1/auth/mfa/verify",
		map[string]string{"mfa_token": l2.str("mfa_token"), "code": totp(t, secret, time.Now())}), 200)
	sess = mfa.str("access_token")
	me := c.expect("GET /me", c.get(sess, "/api/v1/me"), 200)
	if me.Body["mfa_enabled"] != true || me.Body["must_change_password"] != false || me.str("email") != email {
		t.Fatalf("FALLO /me no refleja TOTP activo y contraseña cambiada: %s", me.Raw)
	}
	platform := c.expect("POST /auth/token scope=platform", c.post(sess, "/api/v1/auth/token",
		map[string]string{"scope": "platform"}), 200).str("access_token")

	// --- Alta de dos ISP de prueba (I0-06/I0-09) ----------------------------------------------
	newTenant := func(slug string) string {
		r := c.expect("POST /platform/tenants "+slug, c.do(call{Method: http.MethodPost, Path: "/api/v1/platform/tenants",
			Token: platform, Header: map[string]string{"Idempotency-Key": uuid.NewString()},
			Body: map[string]any{"slug": slug, "name": "ISP " + slug, "country": "MX", "timezone": "America/Mexico_City",
				"initial_admin_email": "noc@" + slug + ".example.net"}}), 201)
		if r.str("id") == "" || r.str("status") != "active" {
			t.Fatalf("FALLO alta de ISP: %s", r.Raw)
		}
		return r.str("id")
	}
	ispA := newTenant("accept-demo-" + suffix)
	ispB := newTenant("accept-otro-" + suffix)
	c.expect("ISP visible en GET /platform/tenants/{id}", c.get(platform, "/api/v1/platform/tenants/"+ispA), 200)
	c.expect("token de plataforma en ruta de ISP → 403", c.get(platform, "/api/v1/sites"), 403)

	// --- POST /auth/token para el ISP y alta de nodo, router y prefijos -----------------------
	tokA := c.expect("POST /auth/token tenant_id=ISP A", c.post(sess, "/api/v1/auth/token",
		map[string]string{"tenant_id": ispA}), 200)
	if tokA.str("tenant_id") != ispA {
		t.Fatalf("FALLO el token de ISP no lleva su tenant_id: %s", tokA.Raw)
	}
	a := tokA.str("access_token")
	site := c.expect("alta de nodo Centro", c.post(a, "/api/v1/sites", map[string]any{"name": "Centro", "code": "CEN"}), 201)
	siteID := site.str("id")
	rt := c.expect("alta del router principal", c.post(a, "/api/v1/routers", map[string]any{
		"site_id": siteID, "name": "rt-centro-01", "model": "CCR2116-12G-4S+", "routeros_version": "7.16.1"}), 201)
	routerID := rt.str("id")
	if rt.Body["is_primary"] != true {
		t.Fatalf("FALLO el primer router del nodo debe ser principal: %s", rt.Raw)
	}
	cp := c.expect("alta de client_prefix 10.20.0.0/24", c.post(a, "/api/v1/sites/"+siteID+"/client-prefixes",
		map[string]any{"prefix": "10.20.0.0/24", "role": "customers", "assignment_mode": "dynamic"}), 201)
	prefixID := cp.str("id")
	if r := c.post(a, "/api/v1/sites/"+siteID+"/client-prefixes", map[string]any{"prefix": "10.20.0.128/25", "role": "customers"}); r.Status != 409 || r.code() != "CLIENT_PREFIX_OVERLAP" {
		t.Fatalf("FALLO un prefijo solapado debe dar 409 CLIENT_PREFIX_OVERLAP: HTTP %d %s", r.Status, r.Raw)
	}
	t.Logf("ok  prefijo solapado rechazado (409 CLIENT_PREFIX_OVERLAP)")
	list := c.expect("GET /sites/{id}/client-prefixes (ISP A)", c.get(a, "/api/v1/sites/"+siteID+"/client-prefixes"), 200)
	if data, _ := list.Body["data"].([]any); len(data) != 1 {
		t.Fatalf("FALLO el ISP A debe ver su prefijo: %s", list.Raw)
	}
	c.expect("GET /routers/{id} (ISP A)", c.get(a, "/api/v1/routers/"+routerID), 200)

	// --- Aislamiento: el ISP B no ve nada del ISP A (I0-08) -----------------------------------
	b := c.expect("POST /auth/token tenant_id=ISP B", c.post(sess, "/api/v1/auth/token",
		map[string]string{"tenant_id": ispB}), 200).str("access_token")
	c.expect("ISP B: GET /sites/{nodo de A} → 404", c.get(b, "/api/v1/sites/"+siteID), 404)
	c.expect("ISP B: GET /routers/{router de A} → 404", c.get(b, "/api/v1/routers/"+routerID), 404)
	c.expect("ISP B: GET /sites/{nodo de A}/client-prefixes → 404", c.get(b, "/api/v1/sites/"+siteID+"/client-prefixes"), 404)
	c.expect("ISP B: PATCH /client-prefixes/{prefijo de A} → 404", c.do(call{Method: http.MethodPatch,
		Path: "/api/v1/client-prefixes/" + prefixID, Token: b, Header: map[string]string{"If-Match": `"1"`},
		Body: map[string]any{"role": "excluded"}}), 404)
	c.expect("ISP B: POST router en el nodo de A → 422", c.post(b, "/api/v1/routers",
		map[string]any{"site_id": siteID, "name": "rt-pirata"}), 422)
	for _, path := range []string{"/api/v1/sites", "/api/v1/routers"} {
		r := c.expect("ISP B: GET "+path+" vacío", c.get(b, path), 200)
		if data, _ := r.Body["data"].([]any); len(data) != 0 {
			t.Fatalf("FALLO el ISP B ve datos del ISP A en %s: %s", path, r.Raw)
		}
	}
	c.expect("token del ISP B con tenant_id de A en el cuerpo → 403", c.post(b, "/api/v1/sites",
		map[string]any{"name": "Intruso", "tenant_id": ispA}), 403)
	// El ISP A sigue viendo lo suyo.
	c.expect("ISP A: GET /sites/{id} sigue en 200", c.get(a, "/api/v1/sites/"+siteID), 200)

	// --- Estado final ------------------------------------------------------------------------
	c.expect("GET /system/status con token de ISP", c.get(a, "/api/v1/system/status"), 200)
}
