//go:build integration

package tenancy

import (
	"context"
	"strings"
	"testing"
	"time"
)

// I0-06 criterio 4 + I0-07: el superadmin semilla existe, debe cambiar la
// contraseña y activar TOTP (D14) antes de obtener un token de plataforma.
func TestSeedSuperadminFirstLogin(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	s := a.login(seedEmail, seedPassword)
	me := a.must(a.do(req{Method: "GET", Path: "/api/v1/me", Token: s.token}), 200)
	if me.Body["must_change_password"] != true || me.Body["mfa_enabled"] != false {
		t.Fatalf("me = %s", me.Raw)
	}
	if r := a.do(req{Method: "POST", Path: "/api/v1/auth/token", Token: s.token, Body: map[string]string{"scope": "platform"}}); r.Status != 403 || r.code() != "PASSWORD_CHANGE_REQUIRED" {
		t.Fatalf("token antes de cambiar contraseña: %d %s", r.Status, r.Raw)
	}
	a.must(a.do(req{Method: "POST", Path: "/api/v1/me/password", Token: s.token,
		Body: map[string]string{"current_password": seedPassword, "new_password": "password1234"}}), 422)
	a.must(a.do(req{Method: "POST", Path: "/api/v1/me/password", Token: s.token,
		Body: map[string]string{"current_password": seedPassword, "new_password": "otra-clave-del-sistema-1"}}), 204)
	if r := a.do(req{Method: "POST", Path: "/api/v1/auth/token", Token: s.token, Body: map[string]string{"scope": "platform"}}); r.Status != 403 || r.code() != "MFA_ENROLLMENT_REQUIRED" {
		t.Fatalf("token sin TOTP: %d %s", r.Status, r.Raw)
	}
	s.enrollTOTP()
	pt := s.platformToken()
	me = a.must(a.do(req{Method: "GET", Path: "/api/v1/me", Token: pt}), 200)
	if me.Body["must_change_password"] != false || me.Body["mfa_enabled"] != true ||
		!strings.Contains(string(me.Raw), `"platform_admin"`) || !strings.Contains(string(me.Raw), "platform.tenants.manage") {
		t.Fatalf("me = %s", me.Raw)
	}
	a.must(a.do(req{Method: "GET", Path: "/api/v1/platform/tenants", Token: pt}), 200)

	// El siguiente login exige el segundo factor y el mfa_token es de un uso.
	r := a.must(a.do(req{Method: "POST", Path: "/api/v1/auth/login", Body: map[string]string{"username": seedEmail, "password": "otra-clave-del-sistema-1"}}), 200)
	if r.Body["mfa_required"] != true || refreshCookie(r) != "" {
		t.Fatalf("login con 2FA: %s", r.Raw)
	}
	mfa := r.str("mfa_token")
	code := totpCode(s.secret, time.Now().Add(30*time.Second)) // paso siguiente (el actual ya se usó)
	ok := a.must(a.do(req{Method: "POST", Path: "/api/v1/auth/mfa/verify", Body: map[string]string{"mfa_token": mfa, "code": code}}), 200)
	if ok.str("scope") != "session" || refreshCookie(ok) == "" {
		t.Fatalf("mfa verify: %s", ok.Raw)
	}
	a.must(a.do(req{Method: "POST", Path: "/api/v1/auth/mfa/verify", Body: map[string]string{"mfa_token": mfa, "code": code}}), 401)
}

// I0-07 criterios 1, 3 y 5 y token por ISP.
func TestLoginTokensAndMe(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	root := a.superadmin()
	pt := root.platformToken()
	tA := a.createTenant(pt, "isp-a", "admin-a@isp.test")
	tB := a.createTenant(pt, "isp-b", "admin-b@isp.test")
	tC := a.createTenant(pt, "isp-c", "admin-c@isp.test")
	op := a.createUser("noc@isp.test")
	a.grant(op, tA, "noc")
	a.grant(op, tB, "viewer")

	// Criterio 1: access corto + refresh HttpOnly.
	r := a.must(a.do(req{Method: "POST", Path: "/api/v1/auth/login", Body: map[string]string{"username": "noc@isp.test", "password": userPassword}}), 200)
	if r.str("token_type") != "Bearer" || r.str("scope") != "session" || r.Body["tenant_id"] != nil {
		t.Fatalf("login = %s", r.Raw)
	}
	sc := r.Header.Get("Set-Cookie")
	for _, want := range []string{"__Secure-hf_rt=", "HttpOnly", "Secure", "SameSite=Strict", "Path=/api/v1/auth"} {
		if !strings.Contains(sc, want) {
			t.Fatalf("Set-Cookie sin %q: %s", want, sc)
		}
	}
	exp, _ := time.Parse(time.RFC3339, r.str("expires_at"))
	if d := time.Until(exp); d <= 0 || d > 11*time.Minute {
		t.Fatalf("expires_at = %s", r.str("expires_at"))
	}
	s := &session{a: a, token: r.str("access_token"), refresh: refreshCookie(r)}

	// Criterio 3: /me con los dos ISP, su rol y permisos efectivos.
	me := a.must(a.do(req{Method: "GET", Path: "/api/v1/me", Token: s.token}), 200)
	ms, _ := me.Body["memberships"].([]any)
	if len(ms) != 2 {
		t.Fatalf("memberships = %s", me.Raw)
	}
	byTenant := map[string]map[string]any{}
	for _, m := range ms {
		mm := m.(map[string]any)
		byTenant[mm["tenant_id"].(string)] = mm
	}
	roleOf := func(tid string) string {
		return byTenant[tid]["roles"].([]any)[0].(map[string]any)["role_key"].(string)
	}
	if roleOf(tA) != "noc" || roleOf(tB) != "viewer" || byTenant[tA]["tenant_slug"] != "isp-a" {
		t.Fatalf("roles = %s", me.Raw)
	}
	permsB := byTenant[tB]["permissions_with_scope"].(map[string]any)
	if _, ok := permsB["sites.read"]; !ok {
		t.Fatalf("viewer sin sites.read: %v", permsB)
	}
	if _, ok := permsB["customers.read"]; ok {
		t.Fatal("viewer con customers.read")
	}

	// Token por ISP con tid y perms de ese ISP; ISP ajeno → 404 sin revelar.
	tok := s.tenantToken(tA)
	if r := a.do(req{Method: "POST", Path: "/api/v1/auth/token", Token: s.token, Body: map[string]string{"tenant_id": tC}}); r.Status != 404 || r.code() != "TENANT_NOT_FOUND" {
		t.Fatalf("token de ISP ajeno: %d %s", r.Status, r.Raw)
	}
	if r := a.do(req{Method: "POST", Path: "/api/v1/auth/token", Token: s.token, Body: map[string]string{"scope": "platform"}}); r.Status != 403 {
		t.Fatalf("token de plataforma sin rol: %d", r.Status)
	}
	if r := a.do(req{Method: "POST", Path: "/api/v1/auth/token", Token: s.token, Body: map[string]string{"tenant_id": tA, "extra": "x"}}); r.Status != 422 {
		t.Fatalf("cuerpo con campos de más: %d", r.Status)
	}
	// El token de ISP no sirve en plataforma ni el de sesión en ISP (gateway).
	if r := a.do(req{Method: "GET", Path: "/api/v1/platform/tenants", Token: tok}); r.code() != "TOKEN_SCOPE_INVALID" {
		t.Fatalf("token de ISP en plataforma: %s", r.Raw)
	}

	// Criterio 5: errores idénticos exista o no el usuario.
	bad := a.do(req{Method: "POST", Path: "/api/v1/auth/login", Body: map[string]string{"username": "noc@isp.test", "password": "incorrecta-123456"}})
	ghost := a.do(req{Method: "POST", Path: "/api/v1/auth/login", Body: map[string]string{"username": "nadie@isp.test", "password": "incorrecta-123456"}})
	if bad.Status != 401 || ghost.Status != 401 || bad.code() != "INVALID_CREDENTIALS" || bad.str("detail") != ghost.str("detail") ||
		bad.str("title") != ghost.str("title") || bad.code() != ghost.code() {
		t.Fatalf("respuestas distintas:\n%s\n%s", bad.Raw, ghost.Raw)
	}
}

// I0-07 criterio 2: reutilizar un refresh revoca la familia (la sesión).
func TestRefreshRotationAndReuse(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	a.createUser("ana@isp.test")
	s := a.login("ana@isp.test", userPassword)
	first := s.refresh
	if r := a.do(req{Method: "POST", Path: "/api/v1/auth/refresh", Cookie: first, Header: map[string]string{"X-Requested-With": ""}}); r.Status != 403 || r.code() != "ORIGIN_NOT_ALLOWED" {
		t.Fatalf("sin X-Requested-With: %d %s", r.Status, r.Raw)
	}
	r := a.must(a.do(req{Method: "POST", Path: "/api/v1/auth/refresh", Cookie: first}), 200)
	second := refreshCookie(r)
	if second == "" || second == first || r.str("scope") != "session" {
		t.Fatalf("refresh = %s", r.Raw)
	}
	// Segundo uso del primero: 401 y sesión revocada.
	if r := a.do(req{Method: "POST", Path: "/api/v1/auth/refresh", Cookie: first}); r.Status != 401 || r.code() != "SESSION_REVOKED" {
		t.Fatalf("reutilización: %d %s", r.Status, r.Raw)
	}
	if r := a.do(req{Method: "POST", Path: "/api/v1/auth/refresh", Cookie: second}); r.Status != 401 {
		t.Fatalf("la familia debe quedar revocada: %d", r.Status)
	}
	if r := a.do(req{Method: "GET", Path: "/api/v1/me", Token: s.token}); r.Status != 401 || r.code() != "SESSION_REVOKED" {
		t.Fatalf("access token de sesión revocada en el gateway: %d %s", r.Status, r.Raw)
	}
	if a.auditCount("auth.refresh_token.reused") != 1 {
		t.Fatal("reutilización no auditada")
	}
	var n int
	_ = a.admin.QueryRow(context.Background(), `SELECT count(*) FROM auth.outbox WHERE subject LIKE 'horus.auth.session.revoked.%' AND payload->'data'->>'reason' = 'refresh_token_reuse'`).Scan(&n)
	if n != 1 {
		t.Fatalf("evento session.revoked en outbox = %d", n)
	}

	// Logout revoca y borra la cookie.
	s2 := a.login("ana@isp.test", userPassword)
	lo := a.must(a.do(req{Method: "POST", Path: "/api/v1/auth/logout", Cookie: s2.refresh}), 204)
	if !strings.Contains(lo.Header.Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("logout no borra la cookie: %s", lo.Header.Get("Set-Cookie"))
	}
	a.must(a.do(req{Method: "POST", Path: "/api/v1/auth/refresh", Cookie: s2.refresh}), 401)
}

// I0-07 criterio 4: tras 5 fallos, el sexto intento se bloquea (aunque la
// contraseña sea correcta) con la misma respuesta, y se registra el evento.
func TestProgressiveLockout(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	a.createUser("lu@isp.test")
	for i := range 5 {
		r := a.do(req{Method: "POST", Path: "/api/v1/auth/login", Body: map[string]string{"username": "lu@isp.test", "password": "mala-clave-numero-" + string(rune('a'+i))}})
		if r.Status != 401 {
			t.Fatalf("fallo %d: %d", i+1, r.Status)
		}
	}
	r := a.do(req{Method: "POST", Path: "/api/v1/auth/login", Body: map[string]string{"username": "lu@isp.test", "password": userPassword}})
	if r.Status != 401 || r.code() != "INVALID_CREDENTIALS" {
		t.Fatalf("sexto intento: %d %s", r.Status, r.Raw)
	}
	if a.auditCount("auth.login.locked") < 2 { // al bloquear y al rechazar el sexto
		t.Fatal("bloqueo no registrado en auditoría")
	}
	var until time.Time
	if err := a.admin.QueryRow(context.Background(), `SELECT locked_until FROM auth."user" WHERE email = 'lu@isp.test'`).Scan(&until); err != nil {
		t.Fatal(err)
	}
	if time.Until(until) > 2*time.Second {
		t.Fatalf("el primer bloqueo dura 1 s: %v", until)
	}
}
