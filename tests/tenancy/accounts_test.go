//go:build integration

package tenancy

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func resetToken(t *testing.T, r resp) string {
	t.Helper()
	const prefix = "/reset-password#token="
	link := r.str("reset_url")
	if !strings.Contains(link, prefix) || r.Header.Get("Cache-Control") != "no-store" || r.str("expires_at") == "" || r.Body["email_sent"] != false {
		t.Fatalf("enlace = %s", r.Raw)
	}
	return link[strings.Index(link, prefix)+len(prefix):]
}

// Cuentas: corrección de nombre y correo y restablecimiento de contraseña
// desde el ISP (solo cuentas exclusivas del ISP), canje del enlace (un uso,
// revoca sesiones), "olvidé mi contraseña" sin SMTP, y desde la plataforma
// la edición, el enlace y la baja lógica (sesiones, membresías, correo libre).
func TestAccountLifecycle(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	ctx := context.Background()
	root := a.superadmin()
	pt := root.platformToken()
	tA := a.createTenant(pt, "isp-c", "admin-c@isp.test")
	tB := a.createTenant(pt, "isp-d", "admin-d@isp.test")
	boss := a.createUser("jefa@isp-c.test")
	a.grant(boss, tA, "tenant_admin")
	bs := a.login("jefa@isp-c.test", userPassword)
	bs.enrollTOTP()
	tok := bs.tenantToken(tA)

	solo := a.createUser("solo@isp-c.test")
	a.grant(solo, tA, "viewer")
	shared := a.createUser("compartida@isp.test")
	a.grant(shared, tA, "viewer")
	a.grant(shared, tB, "viewer")
	soloSess := a.login("solo@isp-c.test", userPassword)
	a.must(a.do(req{Method: "GET", Path: "/api/v1/me", Token: soloSess.token}), 200)

	// --- Edición desde el ISP ---------------------------------------------------------------
	if r := a.do(req{Method: "PATCH", Path: "/api/v1/members/" + shared.String(), Token: tok, Body: map[string]any{"display_name": "Otra"}}); r.Status != 409 || r.code() != "USER_SHARED" {
		t.Fatalf("editar cuenta compartida = %d %s", r.Status, r.Raw)
	}
	if r := a.do(req{Method: "PATCH", Path: "/api/v1/members/" + solo.String(), Token: tok, Body: map[string]any{"email": "compartida@isp.test"}}); r.Status != 409 || r.code() != "ALREADY_EXISTS" {
		t.Fatalf("correo repetido = %d %s", r.Status, r.Raw)
	}
	if r := a.do(req{Method: "PATCH", Path: "/api/v1/members/" + solo.String(), Token: tok, Body: map[string]any{"email": "mal"}}); r.Status != 422 {
		t.Fatalf("correo inválido = %d %s", r.Status, r.Raw)
	}
	ed := a.must(a.do(req{Method: "PATCH", Path: "/api/v1/members/" + solo.String(), Token: tok,
		Body: map[string]any{"display_name": "Sola Mente", "email": "sola@isp-c.test"}}), 200)
	if ed.str("display_name") != "Sola Mente" || ed.str("email") != "sola@isp-c.test" {
		t.Fatalf("editado = %s", ed.Raw)
	}
	var n int
	if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM auth.outbox WHERE payload->>'type' = 'horus.auth.user.updated' AND aggregate_id = $1`, solo).Scan(&n); err != nil || n != 1 {
		t.Fatalf("evento user.updated = %d, %v", n, err)
	}
	if a.auditCount("members.updated") != 1 {
		t.Fatal("sin auditoría members.updated")
	}
	// El otro ISP no ve la cuenta: 404 al editarla o restablecerla (la matriz de aislamiento lo cubre; aquí con un admin real).
	other := a.createUser("jefe@isp-d.test")
	a.grant(other, tB, "tenant_admin")
	os := a.login("jefe@isp-d.test", userPassword)
	os.enrollTOTP()
	tokB := os.tenantToken(tB)
	for _, r := range []resp{
		a.do(req{Method: "PATCH", Path: "/api/v1/members/" + solo.String(), Token: tokB, Body: map[string]any{"display_name": "pirata"}}),
		a.do(req{Method: "POST", Path: "/api/v1/members/" + solo.String() + "/password-reset", Token: tokB}),
	} {
		if r.Status != 404 {
			t.Fatalf("otro ISP = %d %s", r.Status, r.Raw)
		}
	}

	// --- Enlace de restablecimiento desde el ISP ---------------------------------------------
	if r := a.do(req{Method: "POST", Path: "/api/v1/members/" + shared.String() + "/password-reset", Token: tok}); r.Status != 409 || r.code() != "USER_SHARED" {
		t.Fatalf("reset de cuenta compartida = %d %s", r.Status, r.Raw)
	}
	first := a.must(a.do(req{Method: "POST", Path: "/api/v1/members/" + solo.String() + "/password-reset", Token: tok}), 200)
	tok1 := resetToken(t, first)
	second := a.must(a.do(req{Method: "POST", Path: "/api/v1/members/" + solo.String() + "/password-reset", Token: tok}), 200)
	tok2 := resetToken(t, second)
	if a.auditCount("members.password_reset_issued") != 2 {
		t.Fatal("sin auditoría del enlace")
	}
	// El primero quedó invalidado por el segundo.
	if r := a.do(req{Method: "POST", Path: "/api/v1/auth/password/reset/inspect", Body: map[string]string{"token": tok1}}); r.Status != 422 {
		t.Fatalf("enlace sustituido = %d %s", r.Status, r.Raw)
	}
	ins := a.must(a.do(req{Method: "POST", Path: "/api/v1/auth/password/reset/inspect", Body: map[string]string{"token": tok2}}), 200)
	if ins.str("email") != "sola@isp-c.test" || ins.str("display_name") != "Sola Mente" {
		t.Fatalf("inspect = %s", ins.Raw)
	}
	if r := a.do(req{Method: "POST", Path: "/api/v1/auth/password/reset", Body: map[string]string{"token": tok2, "new_password": "corta"}}); r.Status != 422 ||
		!strings.Contains(string(r.Raw), "PASSWORD_TOO_SHORT") {
		t.Fatalf("política = %d %s", r.Status, r.Raw)
	}
	a.must(a.do(req{Method: "POST", Path: "/api/v1/auth/password/reset", Body: map[string]string{"token": tok2, "new_password": "clave nueva y larga 2026"}}), 204)
	// Un uso: la segunda vez, USED.
	if r := a.do(req{Method: "POST", Path: "/api/v1/auth/password/reset", Body: map[string]string{"token": tok2, "new_password": "otra clave larga 2026"}}); r.Status != 422 ||
		!strings.Contains(string(r.Raw), `"USED"`) {
		t.Fatalf("segundo uso = %d %s", r.Status, r.Raw)
	}
	// Todas las sesiones anteriores quedan revocadas; la contraseña nueva entra.
	if r := a.do(req{Method: "GET", Path: "/api/v1/me", Token: soloSess.token}); r.Status != 401 {
		t.Fatalf("sesión vieja tras el reset = %d %s", r.Status, r.Raw)
	}
	if r := a.do(req{Method: "POST", Path: "/api/v1/auth/login", Body: map[string]string{"username": "sola@isp-c.test", "password": userPassword}}); r.Status != 401 {
		t.Fatalf("contraseña vieja = %d", r.Status)
	}
	a.login("sola@isp-c.test", "clave nueva y larga 2026")
	if a.auditCount("auth.password.reset") != 1 {
		t.Fatal("sin auditoría del canje")
	}
	if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM auth.password_reset WHERE user_id = $1 AND used_at IS NULL`, solo).Scan(&n); err != nil || n != 0 {
		t.Fatalf("enlaces abiertos tras el canje = %d, %v", n, err)
	}

	// --- Olvidé mi contraseña sin SMTP: 202 idéntico y nada emitido ---------------------------
	for _, email := range []string{"sola@isp-c.test", "nadie@isp.test"} {
		r := a.must(a.do(req{Method: "POST", Path: "/api/v1/auth/password/forgot", Body: map[string]string{"email": email}}), 202)
		if r.Body["email_delivery"] != false {
			t.Fatalf("forgot %s = %s", email, r.Raw)
		}
	}
	if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM auth.password_reset WHERE used_at IS NULL`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("forgot sin SMTP emitió %d enlaces, %v", n, err)
	}

	// --- Plataforma: edición, enlace y baja ------------------------------------------------------
	up := a.must(a.do(req{Method: "PATCH", Path: "/api/v1/platform/users/" + shared.String(), Token: pt,
		Body: map[string]any{"email": "compartida2@isp.test", "display_name": "Compartida Dos"}}), 200)
	if up.str("email") != "compartida2@isp.test" || up.str("display_name") != "Compartida Dos" || up.str("status") != "active" {
		t.Fatalf("plataforma editó = %s", up.Raw)
	}
	pr := a.must(a.do(req{Method: "POST", Path: "/api/v1/platform/users/" + shared.String() + "/password-reset", Token: pt}), 200)
	_ = resetToken(t, pr)
	if a.auditCount("platform.user.password_reset_issued") != 1 || a.auditCount("platform.user.updated") != 1 {
		t.Fatal("sin auditoría de plataforma")
	}
	// Nadie se elimina a sí mismo; el último platform_admin tampoco se elimina.
	rootID := a.must(a.do(req{Method: "GET", Path: "/api/v1/me", Token: root.token}), 200).str("id")
	if r := a.do(req{Method: "DELETE", Path: "/api/v1/platform/users/" + rootID, Token: pt}); r.Status != 422 || !strings.Contains(string(r.Raw), "CANNOT_DELETE_SELF") {
		t.Fatalf("eliminarse = %d %s", r.Status, r.Raw)
	}
	// Baja de la cuenta compartida: membresías en A y B revocadas, sesiones fuera, correo libre.
	shSess := a.login("compartida2@isp.test", userPassword)
	a.must(a.do(req{Method: "DELETE", Path: "/api/v1/platform/users/" + shared.String(), Token: pt}), 204)
	if r := a.do(req{Method: "GET", Path: "/api/v1/me", Token: shSess.token}); r.Status != 401 {
		t.Fatalf("sesión tras la baja = %d", r.Status)
	}
	if r := a.do(req{Method: "POST", Path: "/api/v1/auth/login", Body: map[string]string{"username": "compartida2@isp.test", "password": userPassword}}); r.Status != 401 {
		t.Fatalf("login tras la baja = %d", r.Status)
	}
	if m := a.member(tok, "active", "compartida2@isp.test"); m != nil {
		t.Fatalf("sigue siendo miembro de A: %v", m)
	}
	if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM auth.tenant_membership WHERE user_id = $1 AND status = 'revoked'`, shared).Scan(&n); err != nil || n != 2 {
		t.Fatalf("membresías revocadas = %d, %v", n, err)
	}
	if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM auth.outbox WHERE payload->>'type' = 'horus.auth.user.deleted' AND aggregate_id = $1`, shared).Scan(&n); err != nil || n != 1 {
		t.Fatalf("evento user.deleted = %d, %v", n, err)
	}
	if r := a.do(req{Method: "DELETE", Path: "/api/v1/platform/users/" + shared.String(), Token: pt}); r.Status != 404 {
		t.Fatalf("segunda baja = %d %s", r.Status, r.Raw)
	}
	if r := a.do(req{Method: "GET", Path: "/api/v1/platform/users?q=compartida2", Token: pt}); !strings.Contains(string(r.Raw), `"data":[]`) {
		t.Fatalf("la cuenta dada de baja sigue listada: %s", r.Raw)
	}
	again := a.createUser("compartida2@isp.test") // el correo queda libre
	if again == uuid.Nil || a.auditCount("platform.user.deleted") != 1 {
		t.Fatal("el correo no quedó libre o falta la auditoría")
	}
}
