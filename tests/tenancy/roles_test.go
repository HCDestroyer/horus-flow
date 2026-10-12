//go:build integration

package tenancy

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// roleByKey devuelve el Role de /roles con esa clave (nil si no está).
func (a *app) roleByKey(token, key string) map[string]any {
	a.t.Helper()
	r := a.must(a.do(req{Method: "GET", Path: "/api/v1/roles", Token: token}), 200)
	data, _ := r.Body["data"].([]any)
	for _, x := range data {
		if m, _ := x.(map[string]any); m["key"] == key {
			return m
		}
	}
	return nil
}

// tenantPerms devuelve permissions_with_scope de la membresía en tenant (GET /me).
func (a *app) tenantPerms(sessionToken, tenant string) map[string]any {
	a.t.Helper()
	me := a.must(a.do(req{Method: "GET", Path: "/api/v1/me", Token: sessionToken}), 200)
	ms, _ := me.Body["memberships"].([]any)
	for _, x := range ms {
		if m, _ := x.(map[string]any); m["tenant_id"] == tenant {
			p, _ := m["permissions_with_scope"].(map[string]any)
			return p
		}
	}
	return nil
}

// I1 · Roles propios del ISP: alta con permisos del catálogo, regla
// anti-escalada, asignación con PUT /members/{user_id}/role-assignments,
// edición con If-Match que llega a los tokens nuevos, plantillas de solo
// lectura, borrado solo sin asignaciones y aislamiento entre ISP.
func TestCustomRolesLifecycle(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	ctx := context.Background()
	pt := a.superadmin().platformToken()
	A, B := a.newISP(pt, "isp-r"), a.newISP(pt, "isp-s")

	r := a.must(a.post(A.token, "/api/v1/roles", map[string]any{"key": "soporte", "name": "Soporte", "description": "Nivel 1",
		"permissions": []string{"sites.read", "devices.read"}}), 201)
	soporte := r.str("id")
	if r.Header.Get("ETag") != `"1"` || !strings.HasSuffix(r.Header.Get("Location"), "/api/v1/roles/"+soporte) || r.Body["requires_mfa"] != false ||
		r.str("tenant_id") != A.id || r.Body["version"] != float64(1) {
		t.Fatalf("alta = %v %s", r.Header, r.Raw)
	}
	var tenantID string
	if err := a.admin.QueryRow(ctx, `SELECT tenant_id::text FROM auth.role WHERE id = $1 AND NOT is_system`, soporte).Scan(&tenantID); err != nil || tenantID != A.id {
		t.Fatalf("rol guardado en %s, %v", tenantID, err)
	}
	for _, body := range []map[string]any{
		{"key": "soporte", "name": "Otra", "permissions": []string{"sites.read"}}, // repetida en el ISP
		{"key": "noc", "name": "Otra", "permissions": []string{"sites.read"}},     // de un rol de sistema
	} {
		if r := a.post(A.token, "/api/v1/roles", body); r.Status != 409 || r.code() != "ALREADY_EXISTS" {
			t.Fatalf("clave %v = %d %s", body["key"], r.Status, r.Raw)
		}
	}
	if r := a.post(A.token, "/api/v1/roles", map[string]any{"key": "plat", "name": "X", "permissions": []string{"platform.tenants.manage"}}); r.Status != 422 ||
		!strings.Contains(string(r.Raw), "UNKNOWN_PERMISSION") {
		t.Fatalf("permiso de plataforma = %d %s", r.Status, r.Raw)
	}
	// La misma clave en otro ISP es válida.
	a.must(a.post(B.token, "/api/v1/roles", map[string]any{"key": "soporte", "name": "Soporte B", "permissions": []string{"sites.read"}}), 201)

	// Un rol propio se asigna como uno de sistema; quien lo tiene solo crea roles con permisos que tiene en todo el ISP.
	gestor := a.must(a.post(A.token, "/api/v1/roles", map[string]any{"key": "gestor_roles", "name": "Gestor de roles",
		"permissions": []string{"roles.manage", "roles.read", "sites.read"}}), 201).str("id")
	op := a.createUser("op@isp-r.test")
	a.must(a.do(req{Method: "POST", Path: "/api/v1/members", Token: A.token,
		Body: map[string]any{"email": "op@isp-r.test", "role_assignments": []map[string]string{{"role_id": gestor, "scope": "tenant"}}}}), 202)
	ops := a.login("op@isp-r.test", userPassword)
	opTok := ops.tenantToken(A.id)
	if r := a.post(opTok, "/api/v1/roles", map[string]any{"key": "mas", "name": "Más", "permissions": []string{"devices.delete"}}); r.Status != 403 ||
		r.code() != "PERMISSION_DENIED" {
		t.Fatalf("escalada = %d %s", r.Status, r.Raw)
	}
	a.must(a.post(opTok, "/api/v1/roles", map[string]any{"key": "solo_nodos", "name": "Solo nodos", "permissions": []string{"sites.read"}}), 201)

	path := "/api/v1/members/" + op.String() + "/role-assignments"
	a.must(a.do(req{Method: "PUT", Path: path, Token: A.token, Header: map[string]string{"If-Match": `"1"`},
		Body: []map[string]string{{"role_id": gestor, "scope": "tenant"}, {"role_id": soporte, "scope": "tenant"}}}), 200)
	if m := a.roleByKey(A.token, "soporte"); m == nil || m["assignments_count"] != float64(1) || m["description"] != "Nivel 1" {
		t.Fatalf("soporte en /roles = %v", m)
	}
	if r := a.do(req{Method: "DELETE", Path: "/api/v1/roles/" + soporte, Token: A.token, Header: map[string]string{"If-Match": `"1"`}}); r.Status != 409 ||
		r.code() != "ROLE_IN_USE" {
		t.Fatalf("borrar asignado = %d %s", r.Status, r.Raw)
	}

	// Editar permisos: If-Match obligatorio; los miembros los reciben en su siguiente token.
	rp := "/api/v1/roles/" + soporte
	if r := a.do(req{Method: "PATCH", Path: rp, Token: A.token, Body: map[string]any{"name": "x"}}); r.Status != 428 {
		t.Fatalf("sin If-Match = %d %s", r.Status, r.Raw)
	}
	if r := a.do(req{Method: "PATCH", Path: rp, Token: A.token, Body: map[string]any{"name": "x"}, Header: map[string]string{"If-Match": `"5"`}}); r.Status != 412 {
		t.Fatalf("If-Match viejo = %d %s", r.Status, r.Raw)
	}
	if p := a.tenantPerms(ops.token, A.id); p["sites.update"] != nil {
		t.Fatalf("permisos antes de editar = %v", p)
	}
	r = a.must(a.do(req{Method: "PATCH", Path: rp, Token: A.token, Header: map[string]string{"If-Match": `"1"`},
		Body: map[string]any{"permissions": []string{"sites.read", "devices.read", "sites.update"}}}), 200)
	if r.Header.Get("ETag") != `"2"` || !strings.Contains(string(r.Raw), "sites.update") {
		t.Fatalf("PATCH = %s", r.Raw)
	}
	if p := a.tenantPerms(ops.token, A.id); p["sites.update"] == nil {
		t.Fatalf("el permiso nuevo no llegó al miembro: %v", p)
	}
	newTok := ops.tenantToken(A.id)
	site := a.must(a.post(A.token, "/api/v1/sites", map[string]any{"name": "Nodo R"}), 201).str("id")
	a.must(a.do(req{Method: "PATCH", Path: "/api/v1/sites/" + site, Token: newTok, Body: map[string]any{"name": "Nodo R2"},
		Header: map[string]string{"If-Match": `"1"`}}), 200)

	// Plantillas de sistema: solo lectura.
	noc := a.roleID(A.token, "noc")
	if r := a.do(req{Method: "PATCH", Path: "/api/v1/roles/" + noc, Token: A.token, Body: map[string]any{"name": "x"},
		Header: map[string]string{"If-Match": `"1"`}}); r.Status != 422 || r.code() != "ROLE_READ_ONLY" {
		t.Fatalf("editar plantilla = %d %s", r.Status, r.Raw)
	}
	if r := a.do(req{Method: "DELETE", Path: "/api/v1/roles/" + noc, Token: A.token, Header: map[string]string{"If-Match": `"1"`}}); r.Status != 422 ||
		r.code() != "ROLE_READ_ONLY" {
		t.Fatalf("borrar plantilla = %d %s", r.Status, r.Raw)
	}

	// Aislamiento: B no ve, edita ni borra los roles de A.
	if a.roleByKey(B.token, "gestor_roles") != nil {
		t.Fatal("B ve un rol propio de A")
	}
	if r := a.do(req{Method: "PATCH", Path: rp, Token: B.token, Body: map[string]any{"name": "x"}, Header: map[string]string{"If-Match": `"2"`}}); r.Status != 404 ||
		r.code() != "ROLE_NOT_FOUND" {
		t.Fatalf("B edita = %d %s", r.Status, r.Raw)
	}
	if r := a.do(req{Method: "DELETE", Path: rp, Token: B.token, Header: map[string]string{"If-Match": `"2"`}}); r.Status != 404 {
		t.Fatalf("B borra = %d %s", r.Status, r.Raw)
	}
	// B no puede asignar un rol de A a sus miembros.
	bm := a.createUser("m@isp-s.test")
	a.grant(bm, B.id, "viewer")
	if r := a.do(req{Method: "PUT", Path: "/api/v1/members/" + bm.String() + "/role-assignments", Token: B.token,
		Header: map[string]string{"If-Match": `"1"`}, Body: []map[string]string{{"role_id": soporte, "scope": "tenant"}}}); r.Status != 422 ||
		!strings.Contains(string(r.Raw), "UNKNOWN_ROLE") {
		t.Fatalf("B asigna un rol de A = %d %s", r.Status, r.Raw)
	}

	// Sin asignaciones, se borra.
	a.must(a.do(req{Method: "PUT", Path: path, Token: A.token, Header: map[string]string{"If-Match": `"2"`},
		Body: []map[string]string{{"role_id": gestor, "scope": "tenant"}}}), 200)
	a.must(a.do(req{Method: "DELETE", Path: rp, Token: A.token, Header: map[string]string{"If-Match": `"2"`}}), 204)
	if a.roleByKey(A.token, "soporte") != nil {
		t.Fatal("el rol borrado sigue en /roles")
	}
	var n int
	if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM auth.role_permission WHERE role_id = $1`, soporte).Scan(&n); err != nil || n != 0 {
		t.Fatalf("permisos huérfanos = %d, %v", n, err)
	}
	for action, want := range map[string]int{"roles.created": 4, "roles.updated": 1, "roles.deleted": 1} {
		if got := a.auditCount(action); got != want {
			t.Errorf("auditoría %s = %d, quiero %d", action, got, want)
		}
	}
	if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM auth.audit_log WHERE action LIKE 'roles.%' AND tenant_id IS NULL`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("auditoría de roles fuera del tenant = %d, %v", n, err)
	}
}

// I1 · Gestión de usuarios por la plataforma: bloquear corta el acceso al
// momento (sesiones revocadas), nadie se bloquea a sí mismo, los roles de
// plataforma exigen TOTP para usarse, quitarlos revoca las sesiones y nunca
// queda la instalación sin un platform_admin activo.
func TestPlatformUserManagement(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	ctx := context.Background()
	root := a.superadmin()
	pt := root.platformToken()
	A := a.newISP(pt, "isp-p")
	op := a.createUser("op@isp-p.test")
	a.grant(op, A.id, "noc")
	ops := a.login("op@isp-p.test", userPassword)
	opTok := ops.tenantToken(A.id)
	a.must(a.do(req{Method: "GET", Path: "/api/v1/sites", Token: opTok}), 200)
	userPath := "/api/v1/platform/users/" + op.String()

	// Bloquear: 200 con el PlatformUser; su token deja de valer al momento y no puede refrescar ni entrar.
	r := a.must(a.do(req{Method: "PATCH", Path: userPath, Token: pt, Body: map[string]string{"status": "disabled"}}), 200)
	if r.str("status") != "disabled" || r.str("id") != op.String() || r.str("email") != "op@isp-p.test" {
		t.Fatalf("bloquear = %s", r.Raw)
	}
	if r := a.do(req{Method: "GET", Path: "/api/v1/sites", Token: opTok}); r.Status != 401 || r.code() != "SESSION_REVOKED" {
		t.Fatalf("token del bloqueado = %d %s", r.Status, r.Raw)
	}
	if r := a.do(req{Method: "POST", Path: "/api/v1/auth/refresh", Cookie: ops.refresh}); r.Status != 401 {
		t.Fatalf("refresh del bloqueado = %d %s", r.Status, r.Raw)
	}
	if r := a.do(req{Method: "POST", Path: "/api/v1/auth/login", Body: map[string]string{"username": "op@isp-p.test", "password": userPassword}}); r.Status != 401 {
		t.Fatalf("login del bloqueado = %d %s", r.Status, r.Raw)
	}
	var n int
	if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM auth.session WHERE user_id = $1 AND revoked_at IS NULL`, op).Scan(&n); err != nil || n != 0 {
		t.Fatalf("sesiones vigentes del bloqueado = %d, %v", n, err)
	}
	if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM auth.outbox WHERE payload->>'type' = 'horus.auth.session.revoked'
		AND payload->'data'->>'user_id' = $1 AND payload->'data'->>'reason' = 'user_disabled'`, op.String()).Scan(&n); err != nil || n < 1 {
		t.Fatalf("eventos session.revoked = %d, %v", n, err)
	}
	// Repetir es idempotente; reactivar le deja volver a entrar.
	a.must(a.do(req{Method: "PATCH", Path: userPath, Token: pt, Body: map[string]string{"status": "disabled"}}), 200)
	if r := a.must(a.do(req{Method: "PATCH", Path: userPath, Token: pt, Body: map[string]string{"status": "active"}}), 200); r.str("status") != "active" {
		t.Fatalf("reactivar = %s", r.Raw)
	}
	ops = a.login("op@isp-p.test", userPassword)

	// Nadie se bloquea a sí mismo ni cambia sus roles de plataforma.
	var rootID string
	if err := a.admin.QueryRow(ctx, `SELECT id::text FROM auth."user" WHERE email = $1`, seedEmail).Scan(&rootID); err != nil {
		t.Fatal(err)
	}
	if r := a.do(req{Method: "PATCH", Path: "/api/v1/platform/users/" + rootID, Token: pt, Body: map[string]string{"status": "disabled"}}); r.Status != 422 ||
		!strings.Contains(string(r.Raw), "CANNOT_DISABLE_SELF") {
		t.Fatalf("bloquearse = %d %s", r.Status, r.Raw)
	}
	if r := a.do(req{Method: "PUT", Path: "/api/v1/platform/users/" + rootID + "/platform-roles", Token: pt,
		Body: map[string]any{"platform_roles": []string{"platform_admin", "platform_auditor"}}}); r.Status != 403 {
		t.Fatalf("cambiar los propios roles = %d %s", r.Status, r.Raw)
	}
	if r := a.do(req{Method: "PATCH", Path: "/api/v1/platform/users/" + uuid.NewString(), Token: pt, Body: map[string]string{"status": "disabled"}}); r.Status != 404 ||
		r.code() != "NOT_FOUND" {
		t.Fatalf("desconocido = %d %s", r.Status, r.Raw)
	}
	// Un token de ISP no gestiona usuarios de plataforma.
	if r := a.do(req{Method: "PATCH", Path: userPath, Token: A.token, Body: map[string]string{"status": "disabled"}}); r.Status != 403 {
		t.Fatalf("token de ISP = %d %s", r.Status, r.Raw)
	}

	// Conceder un rol de plataforma: hasta activar TOTP no obtiene token de plataforma.
	rolesPath := userPath + "/platform-roles"
	r = a.must(a.do(req{Method: "PUT", Path: rolesPath, Token: pt, Body: map[string]any{"platform_roles": []string{"platform_auditor"}}}), 200)
	if !strings.Contains(string(r.Raw), `"platform_roles":["platform_auditor"]`) {
		t.Fatalf("PUT = %s", r.Raw)
	}
	if r := a.do(req{Method: "POST", Path: "/api/v1/auth/token", Token: ops.token, Body: map[string]string{"scope": "platform"}}); r.Status != 403 ||
		r.code() != "MFA_ENROLLMENT_REQUIRED" {
		t.Fatalf("token de plataforma sin TOTP = %d %s", r.Status, r.Raw)
	}
	ops.enrollTOTP()
	opPT := ops.platformToken()
	a.must(a.do(req{Method: "GET", Path: "/api/v1/platform/users", Token: opPT}), 200)
	// Quitarlo revoca sus sesiones: el token de plataforma deja de valer.
	a.must(a.do(req{Method: "PUT", Path: rolesPath, Token: pt, Body: map[string]any{"platform_roles": []string{}}}), 200)
	if r := a.do(req{Method: "GET", Path: "/api/v1/platform/users", Token: opPT}); r.Status != 401 || r.code() != "SESSION_REVOKED" {
		t.Fatalf("token tras quitar el rol = %d %s", r.Status, r.Raw)
	}

	// Último platform_admin activo: ni se bloquea ni pierde el rol.
	adm2 := a.createUser("adm2@horus.test")
	a.must(a.do(req{Method: "PUT", Path: "/api/v1/platform/users/" + adm2.String() + "/platform-roles", Token: pt,
		Body: map[string]any{"platform_roles": []string{"platform_admin"}}}), 200)
	var enforced bool
	if err := a.admin.QueryRow(ctx, `SELECT is_platform_admin AND mfa_enforced FROM auth."user" WHERE id = $1`, adm2).Scan(&enforced); err != nil || !enforced {
		t.Fatalf("adm2 sin is_platform_admin/mfa_enforced: %v", err)
	}
	// El superadministrador semilla deja de ser platform_admin (su token sigue vigente hasta caducar).
	if _, err := a.admin.Exec(ctx, `DELETE FROM auth.platform_role_assignment WHERE user_id = $1`, rootID); err != nil {
		t.Fatal(err)
	}
	adm2Path := "/api/v1/platform/users/" + adm2.String()
	if r := a.do(req{Method: "PATCH", Path: adm2Path, Token: pt, Body: map[string]string{"status": "disabled"}}); r.Status != 409 || r.code() != "LAST_PLATFORM_ADMIN" {
		t.Fatalf("bloquear al último admin = %d %s", r.Status, r.Raw)
	}
	if r := a.do(req{Method: "PUT", Path: adm2Path + "/platform-roles", Token: pt, Body: map[string]any{"platform_roles": []string{"platform_operator"}}}); r.Status != 409 ||
		r.code() != "LAST_PLATFORM_ADMIN" {
		t.Fatalf("quitar el rol al último admin = %d %s", r.Status, r.Raw)
	}
	for action, want := range map[string]int{"platform.user.status_changed": 2, "platform.user.roles_replaced": 3} {
		if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM auth.audit_log WHERE action = $1 AND tenant_id IS NULL`, action).Scan(&n); err != nil || n != want {
			t.Errorf("auditoría %s = %d, quiero %d (%v)", action, n, want, err)
		}
	}
}
