//go:build integration

package tenancy

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func kioskCookie(r resp) string {
	for _, c := range (&http.Response{Header: r.Header}).Cookies() {
		if c.Name == "__Secure-hf_kiosk" {
			return c.Value
		}
	}
	return ""
}

func (a *app) kioskDevice(path, cookie string, body any) resp {
	h := map[string]string{"X-Requested-With": "horus"}
	if cookie != "" {
		h["Cookie"] = "__Secure-hf_kiosk=" + cookie
	}
	return a.do(req{Method: "POST", Path: path, Body: body, Header: h})
}

func (a *app) kioskCode(token, kiosk string) string {
	a.t.Helper()
	r := a.must(a.do(req{Method: "POST", Path: "/api/v1/kiosks/" + kiosk + "/enrollment-codes", Token: token,
		Header: map[string]string{"Idempotency-Key": uuid.NewString()}}), 201)
	if r.Header.Get("Cache-Control") != "no-store" || len(r.str("code")) != 8 {
		a.t.Fatalf("código = %s", r.Raw)
	}
	return r.str("code")
}

// enrolledKiosk crea un kiosco, lo enrola y devuelve su ID, cookie y JWT.
func (a *app) enrolledKiosk(x isp, body map[string]any) (id, cookie, jwt string) {
	a.t.Helper()
	k := a.must(a.post(x.token, "/api/v1/kiosks", body), 201)
	id = k.str("id")
	code := a.kioskCode(x.token, id)
	en := a.must(a.kioskDevice("/api/v1/kiosk/enroll", "", map[string]any{"code": code}), 204)
	cookie = kioskCookie(en)
	tok := a.must(a.kioskDevice("/api/v1/kiosk/token", cookie, nil), 200)
	return id, kioskCookie(tok), tok.str("access_token")
}

// I1-14: alta, código de un uso, cookie rotativa, JWT de solo lectura con
// lista blanca, allowed_cidrs, revocación y detección de reutilización.
func TestKioskEnrollmentAndCredential(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	pt := a.superadmin().platformToken()
	A, B := a.newISP(pt, "isp-a"), a.newISP(pt, "isp-b")

	// Criterio 1: alta (show_personal_data falso por defecto) y código con Idempotency-Key.
	k := a.must(a.post(A.token, "/api/v1/kiosks", map[string]any{"name": "TV sala NOC"}), 201)
	if k.Body["show_personal_data"] != false || k.Body["status"] != "pending_enrollment" || k.Body["cidr_risk"] != true {
		t.Fatalf("kiosco = %s", k.Raw)
	}
	kid := k.str("id")
	if r := a.do(req{Method: "POST", Path: "/api/v1/kiosks/" + kid + "/enrollment-codes", Token: A.token}); r.Status != 428 {
		t.Fatalf("sin Idempotency-Key: %d", r.Status)
	}
	if r := a.post(A.token, "/api/v1/kiosks", map[string]any{"name": "x", "show_personal_data": true}); r.Status != 422 {
		t.Fatalf("show_personal_data sin motivo: %d", r.Status)
	}
	code := a.kioskCode(A.token, kid)

	// Criterio 2: canje → cookie; segundo canje → error.
	en := a.must(a.kioskDevice("/api/v1/kiosk/enroll", "", map[string]any{"code": code}), 204)
	cookie := kioskCookie(en)
	if cookie == "" {
		t.Fatal("sin cookie de dispositivo")
	}
	if r := a.kioskDevice("/api/v1/kiosk/enroll", "", map[string]any{"code": code}); r.Status != 422 || r.code() != "KIOSK_ENROLLMENT_CODE_INVALID" {
		t.Fatalf("segundo canje: %d %s", r.Status, r.Raw)
	}
	if r := a.do(req{Method: "POST", Path: "/api/v1/kiosk/token", Header: map[string]string{"Cookie": "__Secure-hf_kiosk=" + cookie}}); r.Status != 403 {
		t.Fatalf("sin X-Requested-With: %d", r.Status)
	}
	// (el canje está limitado a 5/min por IP: este test hace exactamente 5)

	// JWT de kiosco y cookie rotada.
	tok := a.must(a.kioskDevice("/api/v1/kiosk/token", cookie, nil), 200)
	jwt, rotated := tok.str("access_token"), kioskCookie(tok)
	if tok.str("scope") != "kiosk" || tok.str("tenant_id") != A.id || rotated == "" || rotated == cookie {
		t.Fatalf("token = %s", tok.Raw)
	}

	// Criterio 3: lista blanca.
	if r := a.do(req{Method: "GET", Path: "/api/v1/customers/stats", Token: jwt}); r.Status != 200 {
		t.Fatalf("kiosco en /customers/stats: %d %s", r.Status, r.Raw)
	}
	for _, p := range []struct{ m, path string }{{"GET", "/api/v1/me"}, {"GET", "/api/v1/customers"}, {"GET", "/api/v1/sites"},
		{"POST", "/api/v1/kiosks"}, {"GET", "/api/v1/kiosks/" + kid}} {
		if r := a.do(req{Method: p.m, Path: p.path, Token: jwt, Body: map[string]any{}}); r.Status != 403 || r.code() != "KIOSK_FORBIDDEN" {
			t.Errorf("kiosco en %s %s: %d %s", p.m, p.path, r.Status, r.Raw)
		}
	}

	// Criterio 6: reutilizar la cookie ya rotada revoca el kiosco.
	if r := a.kioskDevice("/api/v1/kiosk/token", cookie, nil); r.Status != 401 {
		t.Fatalf("cookie rotada: %d", r.Status)
	}
	if got := a.must(a.do(req{Method: "GET", Path: "/api/v1/kiosks/" + kid, Token: A.token}), 200); got.str("status") != "revoked" {
		t.Fatalf("reutilización sin revocar: %s", got.Raw)
	}
	if r := a.kioskDevice("/api/v1/kiosk/token", rotated, nil); r.Status != 401 {
		t.Fatalf("credencial de un kiosco revocado: %d", r.Status)
	}
	if a.auditCount("kiosks.credential.reused") != 1 {
		t.Fatal("reutilización no auditada")
	}

	// Criterio 5: revocación manual → el JWT deja de valer de inmediato.
	k2, _, jwt2 := a.enrolledKiosk(A, map[string]any{"name": "TV 2"})
	a.must(a.do(req{Method: "GET", Path: "/api/v1/customers/stats", Token: jwt2}), 200)
	a.must(a.post(A.token, "/api/v1/kiosks/"+k2+"/revoke", map[string]any{"reason": "device_lost"}), 200)
	if r := a.do(req{Method: "GET", Path: "/api/v1/customers/stats", Token: jwt2}); r.Status != 401 {
		t.Fatalf("JWT tras revocar: %d %s", r.Status, r.Raw)
	}

	// Criterio 4: allowed_cidrs (el test llega desde 127.0.0.1).
	k3 := a.must(a.post(A.token, "/api/v1/kiosks", map[string]any{"name": "TV 3", "allowed_cidrs": []string{"203.0.113.0/24"}}), 201).str("id")
	code3 := a.kioskCode(A.token, k3)
	if r := a.kioskDevice("/api/v1/kiosk/enroll", "", map[string]any{"code": code3}); r.Status != 403 || r.code() != "KIOSK_FORBIDDEN" {
		t.Fatalf("canje fuera de allowed_cidrs: %d %s", r.Status, r.Raw)
	}
	// Un kiosco enrolado cuyo CIDR cambia después deja de poder pedir datos.
	k4, _, jwt4 := a.enrolledKiosk(A, map[string]any{"name": "TV 4", "allowed_cidrs": []string{"127.0.0.0/8"}})
	a.must(a.do(req{Method: "GET", Path: "/api/v1/customers/stats", Token: jwt4}), 200)
	a.must(a.do(req{Method: "PATCH", Path: "/api/v1/kiosks/" + k4, Token: A.token, Header: map[string]string{"If-Match": `"2"`},
		Body: map[string]any{"allowed_cidrs": []string{"203.0.113.0/24"}}}), 200)
	a.waitFor("CIDR aplicado", func() bool {
		r := a.do(req{Method: "GET", Path: "/api/v1/customers/stats", Token: jwt4})
		return r.Status == 403 && r.code() == "KIOSK_FORBIDDEN"
	})

	// Aislamiento: el kiosco de A no existe para B.
	if r := a.do(req{Method: "GET", Path: "/api/v1/kiosks/" + kid, Token: B.token}); r.Status != 404 {
		t.Fatalf("kiosco de A visto por B: %d", r.Status)
	}
}
