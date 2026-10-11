//go:build integration

package tenancy

import (
	"context"
	"crypto/sha256"
	"fmt"
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

// I1 · Alta de un ISP con un administrador sin cuenta: la respuesta trae el
// enlace de un solo uso (en la base solo su hash), que la persona canjea para
// activar su cuenta y entrar como tenant_admin.
func TestCreateTenantInvitesFirstAdmin(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	ctx := context.Background()
	pt := a.superadmin().platformToken()
	r := a.must(a.do(req{Method: "POST", Path: "/api/v1/platform/tenants", Token: pt, Header: map[string]string{"Idempotency-Key": uuid.NewString()},
		Body: map[string]any{"slug": "isp-inv", "name": "ISP Invitado", "country": "MX", "timezone": "America/Mexico_City",
			"initial_admin_email": "primera@isp-inv.test"}}), 201)
	tenant := r.str("id")
	inv, _ := r.Body["initial_admin_invitation"].(map[string]any)
	link, _ := inv["invitation_url"].(string)
	const prefix = "/invitation#token="
	if r.str("slug") != "isp-inv" || inv["outcome"] != "invited" || inv["email_sent"] != false || inv["expires_at"] == nil ||
		!strings.Contains(link, prefix) || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("alta = %s", r.Raw)
	}
	token := link[strings.Index(link, prefix)+len(prefix):]
	sum := sha256.Sum256([]byte(token))
	var n int
	if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM auth.invitation i JOIN auth.tenant_membership m ON m.id = i.membership_id
		WHERE i.token_hash = $1 AND i.tenant_id = $2 AND i.user_id = $3 AND m.status = 'invited' AND i.used_at IS NULL`,
		sum[:], tenant, inv["user_id"]).Scan(&n); err != nil || n != 1 {
		t.Fatalf("invitación guardada = %d, %v", n, err)
	}
	if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM auth.outbox WHERE tenant_id = $1
		AND payload->>'type' IN ('horus.auth.tenant.created', 'horus.auth.membership.granted')`, tenant).Scan(&n); err != nil || n != 2 {
		t.Fatalf("eventos del alta = %d, %v", n, err)
	}
	if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM auth.audit_log WHERE action = 'platform.tenant.created'
		AND changes->>'link_issued' = 'true' AND changes->>'email_sent' = 'false' AND changes::text NOT LIKE '%' || $1 || '%'`, token).Scan(&n); err != nil || n != 2 {
		t.Fatalf("auditoría del alta = %d, %v", n, err)
	}
	ins := a.must(a.do(req{Method: "POST", Path: "/api/v1/auth/invitations/inspect", Body: map[string]string{"token": token}}), 200)
	if ins.str("email") != "primera@isp-inv.test" || ins.str("tenant_name") != "ISP Invitado" || ins.Body["needs_password"] != true {
		t.Fatalf("inspect = %s", ins.Raw)
	}
	a.must(a.do(req{Method: "POST", Path: "/api/v1/auth/invitations/accept",
		Body: map[string]string{"token": token, "password": "frase-larga-segura-987", "display_name": "Primera Admin"}}), 204)
	s := a.login("primera@isp-inv.test", "frase-larga-segura-987")
	me := a.must(a.do(req{Method: "GET", Path: "/api/v1/me", Token: s.token}), 200)
	if me.str("display_name") != "Primera Admin" || !strings.Contains(string(me.Raw), tenant) {
		t.Fatalf("me = %s", me.Raw)
	}
	s.enrollTOTP()
	tok := s.tenantToken(tenant)
	if m := a.member(tok, "active", "primera@isp-inv.test"); m == nil || !strings.Contains(fmt.Sprint(m["role_assignments"]), "tenant_admin") {
		t.Fatalf("la administradora no quedó activa como tenant_admin: %v", m)
	}
}

// I1 · Miembros del ISP: alta de un usuario existente, invitación de uno
// nuevo con el enlace de la respuesta, inspección, reenvío (invalida el
// anterior) y aceptación (token de un uso), reasignación de roles con
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

	// Usuario existente: membresía activa directa (outcome added, sin enlace).
	op := a.createUser("op@isp-m.test")
	added := a.must(a.do(req{Method: "POST", Path: "/api/v1/members", Token: tok, Header: map[string]string{"Idempotency-Key": uuid.NewString()},
		Body: map[string]any{"email": "op@isp-m.test", "role_assignments": []map[string]string{{"role_id": viewer, "scope": "tenant"}}}}), 202)
	if added.str("outcome") != "added" || added.str("user_id") != op.String() || added.Body["invitation_url"] != nil || added.Body["email_sent"] != false {
		t.Fatalf("alta de cuenta existente = %s", added.Raw)
	}
	// Reenviar la invitación de un miembro activo: 409.
	if r := a.do(req{Method: "POST", Path: "/api/v1/members/" + op.String() + "/invitation", Token: tok}); r.Status != 409 || r.code() != "CONFLICT" {
		t.Fatalf("reenviar a un activo = %d %s", r.Status, r.Raw)
	}
	m := a.member(tok, "active", "op@isp-m.test")
	if m == nil || m["user_id"] != op.String() || m["version"] != float64(1) {
		t.Fatalf("op no es miembro activo: %v", m)
	}
	opTok := a.login("op@isp-m.test", userPassword).tenantToken(tA)
	a.must(a.do(req{Method: "GET", Path: "/api/v1/members", Token: opTok}), 200)

	// Email nuevo: usuario pendiente, membresía invitada y el enlace (token en el fragmento) en la respuesta; en la base solo el hash.
	inv := a.must(a.do(req{Method: "POST", Path: "/api/v1/members", Token: tok,
		Body: map[string]any{"email": "nueva@isp-m.test", "role_assignments": []map[string]string{{"role_id": noc, "scope": "tenant"}}}}), 202)
	link := inv.str("invitation_url")
	const prefix = "/invitation#token="
	if inv.str("outcome") != "invited" || inv.Body["email_sent"] != false || inv.str("expires_at") == "" || !strings.Contains(link, prefix) {
		t.Fatalf("invitación = %s", inv.Raw)
	}
	firstToken := link[strings.Index(link, prefix)+len(prefix):]
	if a.member(tok, "invited", "nueva@isp-m.test") == nil {
		t.Fatal("la invitada no aparece con status=invited")
	}
	var n int
	if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM auth.invitation i JOIN auth."user" u ON u.id = i.user_id
		WHERE u.email = 'nueva@isp-m.test' AND octet_length(i.token_hash) = 32 AND i.used_at IS NULL`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("invitaciones = %d, %v", n, err)
	}
	sum := sha256.Sum256([]byte(firstToken))
	if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM auth.invitation WHERE token_hash = $1`, sum[:]).Scan(&n); err != nil || n != 1 {
		t.Fatalf("el hash guardado no es el del enlace: %d, %v", n, err)
	}
	// La página de aceptación consulta la invitación sin consumirla.
	ins := a.must(a.do(req{Method: "POST", Path: "/api/v1/auth/invitations/inspect", Body: map[string]string{"token": firstToken}}), 200)
	if ins.str("email") != "nueva@isp-m.test" || ins.str("tenant_name") == "" || ins.Body["needs_password"] != true || ins.str("expires_at") == "" {
		t.Fatalf("inspect = %s", ins.Raw)
	}
	// Enlace nuevo: el anterior deja de valer (misma respuesta que uno desconocido).
	newUser := inv.str("user_id")
	re := a.must(a.do(req{Method: "POST", Path: "/api/v1/members/" + newUser + "/invitation", Token: tok}), 200)
	link = re.str("invitation_url")
	token := link[strings.Index(link, prefix)+len(prefix):]
	if re.str("outcome") != "invited" || re.str("user_id") != newUser || token == firstToken || a.auditCount("members.invitation_reissued") != 1 {
		t.Fatalf("reenvío = %s", re.Raw)
	}
	unknown := a.do(req{Method: "POST", Path: "/api/v1/auth/invitations/inspect", Body: map[string]string{"token": strings.Repeat("u", 43)}})
	old := a.do(req{Method: "POST", Path: "/api/v1/auth/invitations/inspect", Body: map[string]string{"token": firstToken}})
	if old.Status != 422 || old.code() != "VALIDATION_FAILED" || !strings.Contains(string(old.Raw), "INVITATION_INVALID") || unknown.Status != 422 ||
		unknown.code() != old.code() {
		t.Fatalf("enlace anterior = %d %s; desconocido = %d %s", old.Status, old.Raw, unknown.Status, unknown.Raw)
	}
	if r := a.do(req{Method: "POST", Path: "/api/v1/auth/invitations/accept", Body: map[string]string{"token": firstToken,
		"password": "frase-larga-segura-987"}}); r.Status != 422 {
		t.Fatalf("aceptar con el enlace anterior = %d %s", r.Status, r.Raw)
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
	if r := a.do(req{Method: "POST", Path: "/api/v1/auth/invitations/inspect", Body: map[string]string{"token": token}}); r.Status != 422 {
		t.Fatalf("inspect tras el uso = %d %s", r.Status, r.Raw)
	}
	ns := a.login("nueva@isp-m.test", "frase-larga-segura-987")
	me := a.must(a.do(req{Method: "GET", Path: "/api/v1/me", Token: ns.token}), 200)
	if me.str("display_name") != "Nueva Persona" || !strings.Contains(string(me.Raw), tA) {
		t.Fatalf("me de la invitada = %s", me.Raw)
	}
	if a.member(tok, "active", "nueva@isp-m.test") == nil {
		t.Fatal("la invitada no quedó activa")
	}

	// Invitación caducada: el mismo 422.
	exp := a.must(a.do(req{Method: "POST", Path: "/api/v1/members", Token: tok,
		Body: map[string]any{"email": "tarde@isp-m.test", "role_assignments": []map[string]string{{"role_id": viewer, "scope": "tenant"}}}}), 202)
	link = exp.str("invitation_url")
	late := link[strings.Index(link, prefix)+len(prefix):]
	if _, err := a.admin.Exec(ctx, `UPDATE auth.invitation SET expires_at = now() - interval '1 minute'
		WHERE user_id = (SELECT id FROM auth."user" WHERE email = 'tarde@isp-m.test')`); err != nil {
		t.Fatal(err)
	}
	if r := a.do(req{Method: "POST", Path: "/api/v1/auth/invitations/inspect", Body: map[string]string{"token": late}}); r.Status != 422 ||
		!strings.Contains(string(r.Raw), "INVITATION_INVALID") {
		t.Fatalf("inspect caducada = %d %s", r.Status, r.Raw)
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
	a.must(a.do(req{Method: "POST", Path: "/api/v1/members/" + newUser + "/invitation", Token: otok}), 404)

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
