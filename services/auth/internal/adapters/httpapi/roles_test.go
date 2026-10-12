package httpapi

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/app"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

func (f *fakeStore) roleIndex(tenant, id uuid.UUID) int {
	for i, r := range f.roles {
		if r.ID == id && (r.TenantID == nil || *r.TenantID == tenant) {
			return i
		}
	}
	return -1
}

func (f *fakeStore) TenantRole(_ context.Context, tenant, id uuid.UUID) (*domain.TenantRoleRecord, error) {
	i := f.roleIndex(tenant, id)
	if i < 0 {
		return nil, domain.ErrNotFound
	}
	r := f.roles[i]
	return &r, nil
}

func (f *fakeStore) CreateRole(_ context.Context, _ uuid.UUID, r *domain.TenantRoleRecord) error {
	for _, x := range f.roles {
		if x.Key == r.Key {
			return app.ErrRoleKeyTaken
		}
	}
	f.roles = append(f.roles, *r)
	return nil
}

func (f *fakeStore) UpdateRole(_ context.Context, tenant uuid.UUID, r *domain.TenantRoleRecord, expect int, _ time.Time) error {
	i := f.roleIndex(tenant, r.ID)
	if f.roles[i].Version != expect {
		c := f.roles[i]
		return &app.RoleVersionError{Current: &c}
	}
	r.Version = expect + 1
	f.roles[i] = *r
	return nil
}

func (f *fakeStore) DeleteRole(_ context.Context, tenant, id uuid.UUID, _ int) error {
	i := f.roleIndex(tenant, id)
	f.roles = slices.Delete(f.roles, i, i+1)
	return nil
}

func (f *fakeStore) PlatformUser(_ context.Context, id uuid.UUID) (*domain.PlatformUser, error) {
	u, ok := f.pusers[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	c := *u
	return &c, nil
}

func (f *fakeStore) admins(except uuid.UUID) int {
	n := 0
	for id, u := range f.pusers {
		if id != except && u.Status == domain.UserActive && slices.Contains(u.PlatformRoles, domain.RolePlatformAdmin) {
			n++
		}
	}
	return n
}

func (f *fakeStore) SetUserStatus(_ context.Context, in app.UserStatusChange) (*app.UserStatusResult, error) {
	u := f.pusers[in.UserID]
	res := &app.UserStatusResult{Previous: u.Status}
	if in.Disable {
		if slices.Contains(u.PlatformRoles, domain.RolePlatformAdmin) && f.admins(u.ID) == 0 {
			return nil, app.ErrLastPlatformAdmin
		}
		u.Status = domain.UserDisabled
		f.revoked++
	} else {
		u.Status = domain.UserActive
	}
	res.Status = u.Status
	return res, nil
}

func (f *fakeStore) ReplacePlatformRoles(_ context.Context, in app.PlatformRolesChange) (*app.PlatformRolesResult, error) {
	u := f.pusers[in.UserID]
	if slices.Contains(u.PlatformRoles, domain.RolePlatformAdmin) && !slices.Contains(in.Roles, domain.RolePlatformAdmin) && f.admins(u.ID) == 0 {
		return nil, app.ErrLastPlatformAdmin
	}
	res := &app.PlatformRolesResult{Previous: u.PlatformRoles}
	u.PlatformRoles = in.Roles
	return res, nil
}

func TestCustomRolesHTTP(t *testing.T) {
	e := newEnv(t)
	tok := e.token(authz.ScopeTenant, "tenant_admin", 0)
	noc, _ := e.cat.TenantRole("noc")

	// Alta: 201 con Location, ETag "1" y Role completo.
	r := e.do("POST", "/api/v1/roles", tok, `{"key":"soporte_n2","name":"Soporte N2","description":"Escalado","permissions":["devices.read","devices.credentials.reveal"]}`)
	id, _ := r.body["id"].(string)
	if r.status != http.StatusCreated || id == "" || r.header.Get("ETag") != `"1"` || !strings.HasSuffix(r.header.Get("Location"), "/api/v1/roles/"+id) ||
		r.body["version"] != float64(1) || r.body["is_system"] != false || r.body["requires_mfa"] != true || r.body["tenant_id"] != e.tenant.String() ||
		r.body["description"] != "Escalado" || r.body["assignments_count"] != float64(0) {
		t.Fatalf("alta = %d %v %s", r.status, r.header, r.raw)
	}
	if r := e.do("POST", "/api/v1/roles", tok, `{"key":"soporte_n2","name":"Otra","permissions":["devices.read"]}`); r.status != 409 || r.code() != "ALREADY_EXISTS" {
		t.Fatalf("clave repetida = %d %s", r.status, r.raw)
	}
	if r := e.do("POST", "/api/v1/roles", tok, `{"key":"x2","name":"X","permissions":["platform.users.manage"]}`); r.status != 422 ||
		!strings.Contains(r.raw, app.CodeUnknownPermission) {
		t.Fatalf("permiso de plataforma = %d %s", r.status, r.raw)
	}
	if r := e.do("POST", "/api/v1/roles", tok, `{"key":"x2","name":"X","permissions":["devices.read"],"extra":1}`); r.status != 422 || !strings.Contains(r.raw, "UNKNOWN_FIELD") {
		t.Fatalf("campo desconocido = %d %s", r.status, r.raw)
	}
	// Sin roles.manage.
	if r := e.do("POST", "/api/v1/roles", e.token(authz.ScopeTenant, "auditor", 0), `{"key":"x3","name":"X","permissions":["roles.read"]}`); r.status != 403 ||
		r.code() != "PERMISSION_DENIED" {
		t.Fatalf("auditor = %d %s", r.status, r.raw)
	}
	// Anti-escalada: un analista con roles.manage no crea un rol con lo que no tiene.
	nocTok := e.tokenWith(authz.ScopeTenant, "noc", map[string][]string{app.PermRolesManage: {"*"}})
	if r := e.do("POST", "/api/v1/roles", nocTok, `{"key":"x4","name":"X","permissions":["devices.delete"]}`); r.status != 403 || r.code() != "PERMISSION_DENIED" {
		t.Fatalf("escalada = %d %s", r.status, r.raw)
	}

	// Listado: description, version solo en los propios y assignments_count.
	l := e.do("GET", "/api/v1/roles", tok, "")
	data, _ := l.body["data"].([]any)
	seen := 0
	for _, x := range data {
		m, _ := x.(map[string]any)
		_, hasVersion := m["version"]
		if _, ok := m["assignments_count"]; !ok {
			t.Fatalf("rol sin assignments_count: %v", m)
		}
		switch {
		case m["is_system"] == true && hasVersion:
			t.Fatalf("plantilla con version: %v", m)
		case m["id"] == id:
			seen++
			if !hasVersion {
				t.Fatalf("rol propio sin version: %v", m)
			}
		}
	}
	if seen != 1 {
		t.Fatalf("listado = %s", l.raw)
	}

	path := "/api/v1/roles/" + id
	// PATCH: 428 sin If-Match, 412 con el rol actual, 422 en plantillas, 404 desconocido.
	if r := e.do("PATCH", path, tok, `{"name":"Nuevo"}`); r.status != 428 {
		t.Fatalf("sin If-Match = %d %s", r.status, r.raw)
	}
	r = e.do("PATCH", path, tok, `{"name":"Nuevo"}`, "If-Match", `"7"`)
	if cur, _ := r.body["current"].(map[string]any); r.status != 412 || cur["id"] != id || cur["version"] != float64(1) {
		t.Fatalf("If-Match viejo = %d %s", r.status, r.raw)
	}
	if r := e.do("PATCH", "/api/v1/roles/"+noc.ID.String(), tok, `{"name":"Nuevo"}`); r.status != 422 || r.code() != app.CodeRoleReadOnly {
		t.Fatalf("plantilla = %d %s", r.status, r.raw)
	}
	if r := e.do("PATCH", "/api/v1/roles/"+uuid.NewString(), tok, `{"name":"Nuevo"}`, "If-Match", `"1"`); r.status != 404 || r.code() != app.CodeRoleNotFound {
		t.Fatalf("desconocido = %d %s", r.status, r.raw)
	}
	if r := e.do("PATCH", "/api/v1/roles/no-uuid", tok, `{"name":"Nuevo"}`); r.status != 404 || r.code() != app.CodeRoleNotFound {
		t.Fatalf("id no válido = %d %s", r.status, r.raw)
	}
	r = e.do("PATCH", path, tok, `{"name":"Nuevo","permissions":["devices.read"]}`, "If-Match", `"1"`)
	if r.status != 200 || r.header.Get("ETag") != `"2"` || r.body["name"] != "Nuevo" || r.body["requires_mfa"] != false || r.body["version"] != float64(2) {
		t.Fatalf("PATCH = %d %s", r.status, r.raw)
	}

	// DELETE: plantillas no; If-Match obligatorio; 204.
	if r := e.do("DELETE", "/api/v1/roles/"+noc.ID.String(), tok, ""); r.status != 422 || r.code() != app.CodeRoleReadOnly {
		t.Fatalf("borrar plantilla = %d %s", r.status, r.raw)
	}
	if r := e.do("DELETE", path, tok, ""); r.status != 428 {
		t.Fatalf("borrar sin If-Match = %d %s", r.status, r.raw)
	}
	if r := e.do("DELETE", path, tok, "", "If-Match", `"1"`); r.status != 412 {
		t.Fatalf("borrar con versión vieja = %d %s", r.status, r.raw)
	}
	// Asignado: 409 ROLE_IN_USE.
	for i := range e.store.roles {
		if e.store.roles[i].ID.String() == id {
			e.store.roles[i].AssignmentsCount = 1
		}
	}
	if r := e.do("DELETE", path, tok, "", "If-Match", `"2"`); r.status != 409 || r.code() != app.CodeRoleInUse {
		t.Fatalf("borrar asignado = %d %s", r.status, r.raw)
	}
	for i := range e.store.roles {
		e.store.roles[i].AssignmentsCount = 0
	}
	if r := e.do("DELETE", path, tok, "", "If-Match", `"2"`); r.status != 204 {
		t.Fatalf("borrar = %d %s", r.status, r.raw)
	}
	if r := e.do("DELETE", path, tok, "", "If-Match", `"2"`); r.status != 404 {
		t.Fatalf("borrar dos veces = %d %s", r.status, r.raw)
	}
}

func TestPlatformUserManagementHTTP(t *testing.T) {
	e := newEnv(t)
	root := &domain.PlatformUser{ID: uuid.New(), Email: "root@horus.test", Status: domain.UserActive, PlatformRoles: []string{domain.RolePlatformAdmin}}
	op := &domain.PlatformUser{ID: uuid.New(), Email: "op@horus.test", Status: domain.UserActive, PlatformRoles: []string{"platform_operator"}}
	e.store.pusers = map[uuid.UUID]*domain.PlatformUser{root.ID: root, op.ID: op}
	tok := e.token(authz.ScopePlatform, "platform_admin", 0)
	opPath := "/api/v1/platform/users/" + op.ID.String()

	// Re-autenticación antigua (x-reauth): 403 REAUTH_REQUIRED sin cambios.
	if r := e.do("PATCH", opPath, e.token(authz.ScopePlatform, "platform_admin", time.Hour), `{"status":"disabled"}`); r.status != 403 || r.code() != "REAUTH_REQUIRED" {
		t.Fatalf("sin re-auth = %d %s", r.status, r.raw)
	}
	if r := e.do("PUT", opPath+"/platform-roles", e.token(authz.ScopePlatform, "platform_admin", time.Hour), `{"platform_roles":[]}`); r.status != 403 ||
		r.code() != "REAUTH_REQUIRED" {
		t.Fatalf("sin re-auth = %d %s", r.status, r.raw)
	}
	if op.Status != domain.UserActive {
		t.Fatal("se bloqueó sin re-autenticación")
	}
	// Permiso y ámbito.
	if r := e.do("PATCH", opPath, e.token(authz.ScopePlatform, "platform_auditor", 0), `{"status":"disabled"}`); r.status != 403 || r.code() != "PERMISSION_DENIED" {
		t.Fatalf("auditor = %d %s", r.status, r.raw)
	}
	if r := e.do("PATCH", opPath, e.token(authz.ScopeTenant, "tenant_admin", 0), `{"status":"disabled"}`); r.status != 403 || r.code() != "TOKEN_SCOPE_INVALID" {
		t.Fatalf("token de ISP = %d %s", r.status, r.raw)
	}
	// 404 y validación.
	if r := e.do("PATCH", "/api/v1/platform/users/"+uuid.NewString(), tok, `{"status":"disabled"}`); r.status != 404 || r.code() != "NOT_FOUND" {
		t.Fatalf("desconocido = %d %s", r.status, r.raw)
	}
	if r := e.do("PATCH", opPath, tok, `{"status":"locked"}`); r.status != 422 {
		t.Fatalf("estado no válido = %d %s", r.status, r.raw)
	}

	// Bloquear: 200 con PlatformUser y sesiones revocadas.
	r := e.do("PATCH", opPath, tok, `{"status":"disabled"}`)
	if r.status != 200 || r.body["status"] != "disabled" || r.body["id"] != op.ID.String() || e.store.revoked != 1 {
		t.Fatalf("bloquear = %d %s", r.status, r.raw)
	}
	// Último platform_admin activo: 409 LAST_PLATFORM_ADMIN.
	if r := e.do("PATCH", "/api/v1/platform/users/"+root.ID.String(), tok, `{"status":"disabled"}`); r.status != 409 || r.code() != "LAST_PLATFORM_ADMIN" {
		t.Fatalf("último admin = %d %s", r.status, r.raw)
	}
	if r := e.do("PUT", "/api/v1/platform/users/"+root.ID.String()+"/platform-roles", tok, `{"platform_roles":["platform_auditor"]}`); r.status != 409 ||
		r.code() != "LAST_PLATFORM_ADMIN" {
		t.Fatalf("último admin (roles) = %d %s", r.status, r.raw)
	}

	// Roles de plataforma: validación y sustitución.
	if r := e.do("PUT", opPath+"/platform-roles", tok, `{}`); r.status != 422 {
		t.Fatalf("sin platform_roles = %d %s", r.status, r.raw)
	}
	if r := e.do("PUT", opPath+"/platform-roles", tok, `{"platform_roles":["tenant_admin"]}`); r.status != 422 {
		t.Fatalf("rol no válido = %d %s", r.status, r.raw)
	}
	r = e.do("PUT", opPath+"/platform-roles", tok, `{"platform_roles":["platform_auditor","platform_admin"]}`)
	if roles, _ := r.body["platform_roles"].([]any); r.status != 200 || len(roles) != 2 || roles[0] != "platform_admin" {
		t.Fatalf("PUT = %d %s", r.status, r.raw)
	}
}

// tokenWith firma un token como token() con permisos extra.
func (e *env) tokenWith(scope, role string, extra map[string][]string) string {
	e.t.Helper()
	uid := uuid.New()
	c := authz.Claims{Typ: authz.TypeUser, Scope: scope, SID: uuid.NewString(), TID: e.tenant.String(), AuthTime: time.Now().Unix()}
	c.Subject = uid.String()
	c.Perms = map[string][]string{}
	r, _ := e.cat.TenantRole(role)
	for _, p := range r.Permissions {
		c.Perms[p] = []string{authz.ScopeAll}
	}
	for k, v := range extra {
		c.Perms[k] = v
	}
	tok, _, err := e.signer.Sign(c)
	if err != nil {
		e.t.Fatal(err)
	}
	return tok
}
