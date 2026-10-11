//go:build integration

package tenancy

import (
	"context"
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func (a *app) roleID(token, key string) string {
	a.t.Helper()
	r := a.must(a.do(req{Method: "GET", Path: "/api/v1/roles", Token: token}), 200)
	data, _ := r.Body["data"].([]any)
	for _, x := range data {
		if m, _ := x.(map[string]any); m["key"] == key {
			s, _ := m["id"].(string)
			return s
		}
	}
	a.t.Fatalf("rol %s no está en /roles: %s", key, r.Raw)
	return ""
}

func (a *app) member(token, status, email string) map[string]any {
	a.t.Helper()
	r := a.must(a.do(req{Method: "GET", Path: "/api/v1/members?status=" + status + "&q=" + email, Token: token}), 200)
	data, _ := r.Body["data"].([]any)
	for _, x := range data {
		if m, _ := x.(map[string]any); m["email"] == email {
			return m
		}
	}
	return nil
}

// I1 · Miembros del ISP: alta de un usuario existente, invitación de uno
// nuevo y su aceptación (token de un uso), reasignación de roles con
// If-Match, baja (corta el acceso) y aislamiento entre ISP.
func TestMembersLifecycle(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	ctx := context.Background()
	root := a.superadmin()
	pt := root.platformToken()
	tA := a.createTenant(pt, "isp-m", "admin-m@isp.test")
	tB := a.createTenant(pt, "isp-n", "admin-n@isp.test")
	boss := a.createUser("jefe@isp-m.test")
	a.grant(boss, tA, "tenant_admin")
	bs := a.login("jefe@isp-m.test", userPassword)
	bs.enrollTOTP()
	tok := bs.tenantToken(tA)

	viewer, noc := a.roleID(tok, "viewer"), a.roleID(tok, "noc")

	// Usuario existente: membresía activa directa.
	op := a.createUser("op@isp-m.test")
	a.must(a.do(req{Method: "POST", Path: "/api/v1/members", Token: tok, Header: map[string]string{"Idempotency-Key": uuid.NewString()},
		Body: map[string]any{"email": "op@isp-m.test", "role_assignments": []map[string]string{{"role_id": viewer, "scope": "tenant"}}}}), 202)
	m := a.member(tok, "active", "op@isp-m.test")
	if m == nil || m["user_id"] != op.String() || m["version"] != float64(1) {
		t.Fatalf("op no es miembro activo: %v", m)
	}
	opTok := a.login("op@isp-m.test", userPassword).tenantToken(tA)
	a.must(a.do(req{Method: "GET", Path: "/api/v1/members", Token: opTok}), 200)

	// Email nuevo: usuario pendiente, membresía invitada e invitación con el token guardado como hash.
	a.must(a.do(req{Method: "POST", Path: "/api/v1/members", Token: tok,
		Body: map[string]any{"email": "nueva@isp-m.test", "role_assignments": []map[string]string{{"role_id": noc, "scope": "tenant"}}}}), 202)
	if inv := a.member(tok, "invited", "nueva@isp-m.test"); inv == nil {
		t.Fatal("la invitada no aparece con status=invited")
	}
	var n int
	if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM auth.invitation i JOIN auth."user" u ON u.id = i.user_id
		WHERE u.email = 'nueva@isp-m.test' AND octet_length(i.token_hash) = 32 AND i.used_at IS NULL`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("invitaciones = %d, %v", n, err)
	}
	// El token solo lo conoce el canal de entrega: el test fija uno conocido.
	token := strings.Repeat("t", 43)
	sum := sha256.Sum256([]byte(token))
	if _, err := a.admin.Exec(ctx, `UPDATE auth.invitation SET token_hash = $1 WHERE user_id = (SELECT id FROM auth."user" WHERE email = 'nueva@isp-m.test')`, sum[:]); err != nil {
		t.Fatal(err)
	}
	if r := a.do(req{Method: "POST", Path: "/api/v1/auth/invitations/accept", Body: map[string]string{"token": token}}); r.Status != 422 {
		t.Fatalf("aceptar sin contraseña = %d %s", r.Status, r.Raw)
	}
	a.must(a.do(req{Method: "POST", Path: "/api/v1/auth/invitations/accept",
		Body: map[string]string{"token": token, "password": "frase-larga-segura-987", "display_name": "Nueva Persona"}}), 204)
	if r := a.do(req{Method: "POST", Path: "/api/v1/auth/invitations/accept",
		Body: map[string]string{"token": token, "password": "frase-larga-segura-987"}}); r.Status != 422 || r.code() != "VALIDATION_FAILED" {
		t.Fatalf("segundo uso = %d %s", r.Status, r.Raw)
	}
	ns := a.login("nueva@isp-m.test", "frase-larga-segura-987")
	me := a.must(a.do(req{Method: "GET", Path: "/api/v1/me", Token: ns.token}), 200)
	if me.str("display_name") != "Nueva Persona" || !strings.Contains(string(me.Raw), tA) {
		t.Fatalf("me de la invitada = %s", me.Raw)
	}
	if a.member(tok, "active", "nueva@isp-m.test") == nil {
		t.Fatal("la invitada no quedó activa")
	}

	// Reasignación de roles con If-Match.
	path := "/api/v1/members/" + op.String() + "/role-assignments"
	body := []map[string]string{{"role_id": noc, "scope": "tenant"}}
	if r := a.do(req{Method: "PUT", Path: path, Token: tok, Body: body}); r.Status != 428 {
		t.Fatalf("sin If-Match = %d %s", r.Status, r.Raw)
	}
	if r := a.do(req{Method: "PUT", Path: path, Token: tok, Body: body, Header: map[string]string{"If-Match": `"7"`}}); r.Status != 412 {
		t.Fatalf("If-Match viejo = %d %s", r.Status, r.Raw)
	}
	r := a.must(a.do(req{Method: "PUT", Path: path, Token: tok, Body: body, Header: map[string]string{"If-Match": `"1"`}}), 200)
	if r.Header.Get("ETag") != `"2"` || !strings.Contains(string(r.Raw), `"role_key":"noc"`) {
		t.Fatalf("PUT = %s %v", r.Raw, r.Header.Get("ETag"))
	}
	if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM auth.outbox WHERE payload->>'type' = 'horus.auth.membership.updated'
		AND tenant_id = $1`, tA).Scan(&n); err != nil || n != 2 {
		t.Fatalf("eventos membership.updated = %d, %v", n, err)
	}

	// Aislamiento: el admin de otro ISP no ve ni quita miembros de este.
	other := a.createUser("jefe@isp-n.test")
	a.grant(other, tB, "tenant_admin")
	os := a.login("jefe@isp-n.test", userPassword)
	os.enrollTOTP()
	otok := os.tenantToken(tB)
	if a.member(otok, "active", "op@isp-m.test") != nil {
		t.Fatal("un ISP ve miembros de otro")
	}
	a.must(a.do(req{Method: "DELETE", Path: "/api/v1/members/" + op.String(), Token: otok}), 404)

	// Baja: corta el acceso al ISP.
	a.must(a.do(req{Method: "DELETE", Path: "/api/v1/members/" + op.String(), Token: tok}), 204)
	a.must(a.do(req{Method: "DELETE", Path: "/api/v1/members/" + op.String(), Token: tok}), 404)
	if a.member(tok, "active", "op@isp-m.test") != nil || a.member(tok, "revoked", "op@isp-m.test") == nil {
		t.Fatal("la baja no dejó la membresía revocada")
	}
	a.waitFor("op sin acceso al ISP", func() bool {
		return a.do(req{Method: "GET", Path: "/api/v1/members", Token: opTok}).Status != 200
	})
	if a.auditCount("members.removed") != 1 || a.auditCount("auth.invitation.accepted") != 1 {
		t.Fatal("faltan registros de auditoría")
	}

	// Usuarios de la plataforma con sus membresías.
	pu := a.must(a.do(req{Method: "GET", Path: "/api/v1/platform/users?q=jefe@isp-m", Token: pt}), 200)
	if !strings.Contains(string(pu.Raw), `"tenant_slug":"isp-m"`) || !strings.Contains(string(pu.Raw), `"tenant_admin"`) {
		t.Fatalf("platform/users = %s", pu.Raw)
	}

	// Cuenta propia.
	me = a.must(a.do(req{Method: "PATCH", Path: "/api/v1/me", Token: bs.token,
		Body: map[string]any{"display_name": "Jefa", "timezone": "America/Bogota", "default_tenant_id": tA}}), 200)
	if me.str("display_name") != "Jefa" || me.str("timezone") != "America/Bogota" || me.str("default_tenant_id") != tA {
		t.Fatalf("PATCH /me = %s", me.Raw)
	}
	a.must(a.do(req{Method: "PATCH", Path: "/api/v1/me", Token: bs.token, Body: map[string]any{"default_tenant_id": tB}}), 422)
}
