package app

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// memRoleStore simula los roles de un tenant y los usuarios de plataforma.
type memRoleStore struct {
	Store
	tenant   uuid.UUID
	roles    map[uuid.UUID]*domain.TenantRoleRecord
	created  *domain.TenantRoleRecord
	createE  error
	updated  *domain.TenantRoleRecord
	updateE  error
	deleted  []uuid.UUID
	deleteE  error
	users    map[uuid.UUID]*domain.PlatformUser
	sessions map[uuid.UUID][]uuid.UUID // usuario → sesiones vigentes
	audits   []api.AuditEntry
}

func (m *memRoleStore) TenantRole(_ context.Context, tenant, id uuid.UUID) (*domain.TenantRoleRecord, error) {
	r, ok := m.roles[id]
	if !ok || (r.TenantID != nil && *r.TenantID != tenant) {
		return nil, domain.ErrNotFound
	}
	c := *r
	return &c, nil
}

func (m *memRoleStore) CreateRole(_ context.Context, _ uuid.UUID, r *domain.TenantRoleRecord) error {
	if m.createE != nil {
		return m.createE
	}
	c := *r
	m.created = &c
	m.roles[r.ID] = &c
	return nil
}

func (m *memRoleStore) UpdateRole(_ context.Context, _ uuid.UUID, r *domain.TenantRoleRecord, expect int, _ time.Time) error {
	if m.updateE != nil {
		return m.updateE
	}
	cur := m.roles[r.ID]
	if cur.Version != expect {
		c := *cur
		return &RoleVersionError{Current: &c}
	}
	r.Version = expect + 1
	c := *r
	m.roles[r.ID], m.updated = &c, &c
	return nil
}

func (m *memRoleStore) DeleteRole(_ context.Context, _, id uuid.UUID, _ int) error {
	if m.deleteE != nil {
		return m.deleteE
	}
	m.deleted = append(m.deleted, id)
	delete(m.roles, id)
	return nil
}

func (m *memRoleStore) InsertAudit(_ context.Context, e api.AuditEntry) error {
	m.audits = append(m.audits, e)
	return nil
}

func (m *memRoleStore) PlatformUser(_ context.Context, id uuid.UUID) (*domain.PlatformUser, error) {
	u, ok := m.users[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	c := *u
	c.PlatformRoles = slices.Clone(u.PlatformRoles)
	return &c, nil
}

// activeAdmins cuenta los platform_admin activos salvo except.
func (m *memRoleStore) activeAdmins(except uuid.UUID) int {
	n := 0
	for id, u := range m.users {
		if id != except && u.Status == domain.UserActive && slices.Contains(u.PlatformRoles, domain.RolePlatformAdmin) {
			n++
		}
	}
	return n
}

func (m *memRoleStore) SetUserStatus(_ context.Context, in UserStatusChange) (*UserStatusResult, error) {
	u := m.users[in.UserID]
	res := &UserStatusResult{Previous: u.Status}
	if in.Disable {
		if slices.Contains(u.PlatformRoles, domain.RolePlatformAdmin) && u.Status == domain.UserActive && m.activeAdmins(u.ID) == 0 {
			return nil, ErrLastPlatformAdmin
		}
		u.Status = domain.UserDisabled
		res.Revoked = m.sessions[u.ID]
		m.sessions[u.ID] = nil
	} else {
		u.Status = domain.UserActive
	}
	res.Status = u.Status
	return res, nil
}

func (m *memRoleStore) ReplacePlatformRoles(_ context.Context, in PlatformRolesChange) (*PlatformRolesResult, error) {
	u := m.users[in.UserID]
	if slices.Contains(u.PlatformRoles, domain.RolePlatformAdmin) && !slices.Contains(in.Roles, domain.RolePlatformAdmin) &&
		u.Status == domain.UserActive && m.activeAdmins(u.ID) == 0 {
		return nil, ErrLastPlatformAdmin
	}
	res := &PlatformRolesResult{Previous: u.PlatformRoles}
	for _, r := range u.PlatformRoles {
		if !slices.Contains(in.Roles, r) {
			res.Revoked = m.sessions[u.ID]
			m.sessions[u.ID] = nil
			break
		}
	}
	u.PlatformRoles = slices.Clone(in.Roles)
	return res, nil
}

func newRoleEnv(t *testing.T) (*Service, *memRoleStore, *domain.Catalog) {
	t.Helper()
	cat, err := domain.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	st := &memRoleStore{tenant: uuid.New(), roles: map[uuid.UUID]*domain.TenantRoleRecord{}, users: map[uuid.UUID]*domain.PlatformUser{},
		sessions: map[uuid.UUID][]uuid.UUID{}}
	for _, r := range cat.TenantRoles {
		st.roles[r.ID] = &domain.TenantRoleRecord{ID: r.ID, Key: r.Key, Name: r.Name, IsSystem: true, RequiresMFA: r.RequiresMFA, Permissions: r.Permissions}
	}
	svc, err := NewService(st, Options{Catalog: cat, Argon: domain.Argon2Params{Memory: 1024, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}})
	if err != nil {
		t.Fatal(err)
	}
	return svc, st, cat
}

func (m *memRoleStore) customRole(key string, perms []string, assigned int) *domain.TenantRoleRecord {
	tid := m.tenant
	r := &domain.TenantRoleRecord{ID: uuid.New(), TenantID: &tid, Key: key, Name: key, Permissions: perms, Version: 3, AssignmentsCount: assigned}
	m.roles[r.ID] = r
	return r
}

func intp(v int) *int { return &v }

func rawPatch(t *testing.T, s string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCreateRole(t *testing.T) {
	svc, st, cat := newRoleEnv(t)
	ctx := tenantCtx(st.tenant, allPerms(cat, "tenant_admin"))

	// Validación: clave, nombre, permisos desconocidos o de plataforma, repetidos, ninguno.
	_, err := svc.CreateRole(ctx, RoleInput{Key: "Mal-Clave", Name: "", Permissions: []string{"devices.read", "platform.tenants.read", "nada", "devices.read"}}, "")
	e := wantErr(t, err, 422, problem.CodeValidationFailed)
	for f, code := range map[string]string{"key": "INVALID_FORMAT", "name": "INVALID_VALUE", "permissions[1]": CodeUnknownPermission,
		"permissions[2]": CodeUnknownPermission, "permissions[3]": "DUPLICATE"} {
		if !hasField(e, f, code) {
			t.Fatalf("falta %s %s: %+v", f, code, e.Fields)
		}
	}
	_, err = svc.CreateRole(ctx, RoleInput{Key: "vacio", Name: "Vacío", Permissions: nil}, "")
	if e := wantErr(t, err, 422, problem.CodeValidationFailed); !hasField(e, "permissions", "REQUIRED") {
		t.Fatalf("fields = %+v", e.Fields)
	}
	// Clave de un rol de sistema (de tenant o de plataforma) o ya usada en el ISP: 409.
	for _, key := range []string{"noc", "platform_admin"} {
		_, err = svc.CreateRole(ctx, RoleInput{Key: key, Name: "x", Permissions: []string{"devices.read"}}, "")
		_ = wantErr(t, err, 409, problem.CodeAlreadyExists)
	}
	st.createE = ErrRoleKeyTaken
	_, err = svc.CreateRole(ctx, RoleInput{Key: "repetido", Name: "x", Permissions: []string{"devices.read"}}, "")
	_ = wantErr(t, err, 409, problem.CodeAlreadyExists)
	st.createE = nil

	// Sin roles.manage (defensa en profundidad) o con token sin ISP.
	_, err = svc.CreateRole(tenantCtx(st.tenant, allPerms(cat, "viewer")), RoleInput{Key: "x1", Name: "x", Permissions: []string{"devices.read"}}, "")
	_ = wantErr(t, err, 403, problem.CodePermissionDenied)
	_, err = svc.CreateRole(authz.WithPrincipal(context.Background(), &authz.Principal{Type: authz.TypeUser, Scope: authz.ScopeSession}),
		RoleInput{Key: "x1", Name: "x", Permissions: []string{"devices.read"}}, "")
	_ = wantErr(t, err, 403, problem.CodeTokenScopeInvalid)

	// Alta correcta: permisos ordenados, requires_mfa por devices.credentials.reveal, versión 1 y auditoría.
	r, err := svc.CreateRole(ctx, RoleInput{Key: "soporte_n2", Name: " Soporte N2 ", Description: strp(" Escalado "),
		Permissions: []string{"devices.read", "devices.credentials.reveal"}}, "192.0.2.9")
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "Soporte N2" || *r.Description != "Escalado" || !r.RequiresMFA || r.Version != 1 || r.IsSystem || r.TenantID == nil ||
		*r.TenantID != st.tenant || !slices.Equal(r.Permissions, []string{"devices.credentials.reveal", "devices.read"}) || st.created == nil {
		t.Fatalf("rol = %+v", r)
	}
	last := st.audits[len(st.audits)-1]
	if last.Action != "roles.created" || last.TenantID != st.tenant || last.ResourceID != r.ID.String() || last.IP != "192.0.2.9" {
		t.Fatalf("auditoría = %+v", last)
	}
	// Sin permisos que exijan 2FA: requires_mfa false.
	r, err = svc.CreateRole(ctx, RoleInput{Key: "lectura", Name: "Lectura", Permissions: []string{"sites.read"}}, "")
	if err != nil || r.RequiresMFA || r.Description != nil {
		t.Fatalf("rol = %+v, %v", r, err)
	}
}

// security.md §2 (E): un rol propio solo lleva permisos que quien lo crea
// tiene en todo el ISP.
func TestCreateRoleCannotEscalate(t *testing.T) {
	svc, st, cat := newRoleEnv(t)
	perms := allPerms(cat, "noc")
	perms[PermRolesManage] = []string{authz.ScopeAll}
	ctx := tenantCtx(st.tenant, perms)
	_, err := svc.CreateRole(ctx, RoleInput{Key: "mas_que_yo", Name: "x", Permissions: []string{"devices.read", "devices.delete"}}, "")
	_ = wantErr(t, err, 403, problem.CodePermissionDenied)
	if st.created != nil {
		t.Fatal("se creó un rol con más permisos que su autor")
	}
	// Permiso solo en un nodo: no basta (el rol puede asignarse en todo el ISP).
	site := "site:" + uuid.NewString()
	perms["devices.update"] = []string{site}
	_, err = svc.CreateRole(ctx, RoleInput{Key: "nodo", Name: "x", Permissions: []string{"devices.update"}}, "")
	_ = wantErr(t, err, 403, problem.CodePermissionDenied)
	if _, err := svc.CreateRole(ctx, RoleInput{Key: "como_yo", Name: "x", Permissions: []string{"devices.read", "alerts.ack"}}, ""); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateRole(t *testing.T) {
	svc, st, cat := newRoleEnv(t)
	ctx := tenantCtx(st.tenant, allPerms(cat, "tenant_admin"))
	noc, _ := cat.TenantRole("noc")
	custom := st.customRole("soporte", []string{"devices.read", "sites.read"}, 2)
	tid := uuid.New()
	foreign := &domain.TenantRoleRecord{ID: uuid.New(), TenantID: &tid, Key: "ajeno", Version: 1, Permissions: []string{"devices.read"}}
	st.roles[foreign.ID] = foreign

	// Inexistente o de otro ISP: 404 ROLE_NOT_FOUND (antes que If-Match y cuerpo).
	for _, id := range []uuid.UUID{uuid.New(), foreign.ID} {
		_, err := svc.UpdateRole(ctx, id, nil, rawPatch(t, `{"bogus":1}`), "")
		_ = wantErr(t, err, 404, CodeRoleNotFound)
	}
	// Plantilla de sistema: 422 ROLE_READ_ONLY.
	_, err := svc.UpdateRole(ctx, noc.ID, intp(1), rawPatch(t, `{"name":"x"}`), "")
	_ = wantErr(t, err, 422, CodeRoleReadOnly)
	// Sin If-Match: 428; versión vieja: 412 con el rol actual.
	_, err = svc.UpdateRole(ctx, custom.ID, nil, rawPatch(t, `{"name":"x"}`), "")
	_ = wantErr(t, err, 428, problem.CodePreconditionRequired)
	_, err = svc.UpdateRole(ctx, custom.ID, intp(1), rawPatch(t, `{"name":"x"}`), "")
	if e := wantErr(t, err, 412, problem.CodePreconditionFailed); e.Current.(*domain.TenantRoleRecord).Version != 3 {
		t.Fatalf("412 sin el rol actual: %+v", e.Current)
	}
	// Validación.
	_, err = svc.UpdateRole(ctx, custom.ID, intp(3), rawPatch(t, `{"key":"x","name":"","permissions":["platform.users.manage"]}`), "")
	e := wantErr(t, err, 422, problem.CodeValidationFailed)
	if !hasField(e, "key", "UNKNOWN_FIELD") || !hasField(e, "name", "INVALID_VALUE") || !hasField(e, "permissions[0]", CodeUnknownPermission) {
		t.Fatalf("fields = %+v", e.Fields)
	}
	_, err = svc.UpdateRole(ctx, custom.ID, intp(3), rawPatch(t, `{}`), "")
	_ = wantErr(t, err, 422, problem.CodeValidationFailed)

	// Anti-escalada sobre los permisos añadidos; quitar uno que no se tiene sí se puede.
	perms := allPerms(cat, "noc")
	perms[PermRolesManage] = []string{authz.ScopeAll}
	delete(perms, "sites.read")
	nctx := tenantCtx(st.tenant, perms)
	_, err = svc.UpdateRole(nctx, custom.ID, intp(3), rawPatch(t, `{"permissions":["devices.read","devices.delete"]}`), "")
	_ = wantErr(t, err, 403, problem.CodePermissionDenied)
	if st.updated != nil {
		t.Fatal("se amplió un rol con un permiso que su editor no tiene")
	}
	svc.cache["x|y"] = cachedStatus{}
	r, err := svc.UpdateRole(nctx, custom.ID, intp(3), rawPatch(t, `{"permissions":["devices.read"],"description":"Solo routers"}`), "198.51.100.1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != 4 || !slices.Equal(r.Permissions, []string{"devices.read"}) || *r.Description != "Solo routers" || r.Key != "soporte" {
		t.Fatalf("rol = %+v", r)
	}
	if len(svc.cache) != 0 {
		t.Fatal("cambiar los permisos de un rol no vació la caché de sesiones")
	}
	last := st.audits[len(st.audits)-1]
	if last.Action != "roles.updated" || last.TenantID != st.tenant || !slices.Equal(last.Changes["removed_permissions"].([]string), []string{"sites.read"}) {
		t.Fatalf("auditoría = %+v", last)
	}
	// requires_mfa se recalcula con los permisos; description null la borra.
	r, err = svc.UpdateRole(ctx, custom.ID, intp(4), rawPatch(t, `{"permissions":["devices.credentials.reveal"],"description":null}`), "")
	if err != nil || !r.RequiresMFA || r.Description != nil {
		t.Fatalf("rol = %+v, %v", r, err)
	}
	// Carrera: la versión cambió entre la lectura y el bloqueo.
	st.updateE = &RoleVersionError{Current: st.roles[custom.ID]}
	_, err = svc.UpdateRole(ctx, custom.ID, intp(5), rawPatch(t, `{"name":"y"}`), "")
	_ = wantErr(t, err, 412, problem.CodePreconditionFailed)
}

func TestDeleteRole(t *testing.T) {
	svc, st, cat := newRoleEnv(t)
	ctx := tenantCtx(st.tenant, allPerms(cat, "tenant_admin"))
	viewer, _ := cat.TenantRole("viewer")
	used := st.customRole("usado", []string{"devices.read"}, 1)
	free := st.customRole("libre", []string{"devices.read"}, 0)

	_ = wantErr(t, svc.DeleteRole(ctx, uuid.New(), intp(1), ""), 404, CodeRoleNotFound)
	_ = wantErr(t, svc.DeleteRole(ctx, viewer.ID, intp(1), ""), 422, CodeRoleReadOnly)
	_ = wantErr(t, svc.DeleteRole(ctx, free.ID, nil, ""), 428, problem.CodePreconditionRequired)
	_ = wantErr(t, svc.DeleteRole(ctx, free.ID, intp(1), ""), 412, problem.CodePreconditionFailed)
	_ = wantErr(t, svc.DeleteRole(ctx, used.ID, intp(3), ""), 409, CodeRoleInUse)
	// Asignación concurrente: la base lo detecta.
	st.deleteE = ErrRoleInUse
	_ = wantErr(t, svc.DeleteRole(ctx, free.ID, intp(3), ""), 409, CodeRoleInUse)
	st.deleteE = nil
	_ = wantErr(t, svc.DeleteRole(tenantCtx(st.tenant, allPerms(cat, "auditor")), free.ID, intp(3), ""), 403, problem.CodePermissionDenied)
	if err := svc.DeleteRole(ctx, free.ID, intp(3), ""); err != nil {
		t.Fatal(err)
	}
	if len(st.deleted) != 1 || st.deleted[0] != free.ID {
		t.Fatalf("borrados = %v", st.deleted)
	}
	if last := st.audits[len(st.audits)-1]; last.Action != "roles.deleted" || last.TenantID != st.tenant || last.ResourceID != free.ID.String() {
		t.Fatalf("auditoría = %+v", last)
	}
}

func platformCtx(uid uuid.UUID, roles ...string) context.Context {
	perms := map[string][]string{}
	cat, _ := domain.LoadCatalog()
	ps, _ := cat.PlatformPermissions(roles)
	for _, p := range ps {
		perms[p] = []string{authz.ScopeAll}
	}
	return authz.WithPrincipal(context.Background(), &authz.Principal{Type: authz.TypeUser, Subject: uid.String(), UserID: uid,
		SessionID: uuid.New(), Scope: authz.ScopePlatform, Perms: perms})
}

func (m *memRoleStore) addUser(status string, roles ...string) *domain.PlatformUser {
	u := &domain.PlatformUser{ID: uuid.New(), Email: uuid.NewString() + "@horus.test", Status: status, PlatformRoles: roles}
	m.users[u.ID] = u
	m.sessions[u.ID] = []uuid.UUID{uuid.New(), uuid.New()}
	return u
}

func TestSetUserStatus(t *testing.T) {
	svc, st, _ := newRoleEnv(t)
	root := st.addUser(domain.UserActive, domain.RolePlatformAdmin)
	ctx := platformCtx(root.ID, domain.RolePlatformAdmin)
	op := st.addUser(domain.UserActive, "platform_operator")
	disable := rawPatch(t, `{"status":"disabled"}`)

	_, err := svc.SetUserStatus(ctx, uuid.New(), disable, "")
	_ = wantErr(t, err, 404, problem.CodeNotFound)
	_, err = svc.SetUserStatus(ctx, op.ID, rawPatch(t, `{"status":"locked","email":"x"}`), "")
	if e := wantErr(t, err, 422, problem.CodeValidationFailed); !hasField(e, "status", "INVALID_VALUE") || !hasField(e, "email", "UNKNOWN_FIELD") {
		t.Fatalf("fields = %+v", e.Fields)
	}
	_, err = svc.SetUserStatus(ctx, op.ID, rawPatch(t, `{}`), "")
	_ = wantErr(t, err, 422, problem.CodeValidationFailed)
	// Bloquearse a sí mismo: 422 de campo.
	_, err = svc.SetUserStatus(ctx, root.ID, disable, "")
	if e := wantErr(t, err, 422, problem.CodeValidationFailed); !hasField(e, "status", CodeCannotDisableSelf) {
		t.Fatalf("fields = %+v", e.Fields)
	}
	// Sin platform.users.manage o con un token de ISP.
	_, err = svc.SetUserStatus(platformCtx(uuid.New(), "platform_auditor"), op.ID, disable, "")
	_ = wantErr(t, err, 403, problem.CodePermissionDenied)
	_, err = svc.SetUserStatus(tenantCtx(uuid.New(), map[string][]string{PermPlatformUsersManage: {"*"}}), op.ID, disable, "")
	_ = wantErr(t, err, 403, problem.CodeTokenScopeInvalid)

	// Bloquear revoca todas sus sesiones, vacía la caché y audita en la plataforma.
	svc.cache["x|y"] = cachedStatus{}
	u, err := svc.SetUserStatus(ctx, op.ID, disable, "203.0.113.4")
	if err != nil {
		t.Fatal(err)
	}
	if u.Status != domain.UserDisabled || len(st.sessions[op.ID]) != 0 || len(svc.cache) != 0 {
		t.Fatalf("usuario = %+v, sesiones = %v", u, st.sessions[op.ID])
	}
	last := st.audits[len(st.audits)-1]
	if last.Action != "platform.user.status_changed" || last.TenantID != uuid.Nil || last.ResourceID != op.ID.String() ||
		last.Changes["revoked_sessions"] != 2 || last.Changes["status"] != domain.UserDisabled || last.IP != "203.0.113.4" {
		t.Fatalf("auditoría = %+v", last)
	}
	// Idempotente.
	n := len(st.audits)
	if u, err := svc.SetUserStatus(ctx, op.ID, disable, ""); err != nil || u.Status != domain.UserDisabled || len(st.audits) != n {
		t.Fatalf("repetido = %+v, %v", u, err)
	}
	if u, err := svc.SetUserStatus(ctx, op.ID, rawPatch(t, `{"status":"active"}`), ""); err != nil || u.Status != domain.UserActive {
		t.Fatalf("reactivar = %+v, %v", u, err)
	}

	// El último platform_admin activo no se bloquea (lo hace otro admin).
	other := st.addUser(domain.UserActive, domain.RolePlatformAdmin)
	if _, err := svc.SetUserStatus(platformCtx(other.ID, domain.RolePlatformAdmin), root.ID, disable, ""); err != nil {
		t.Fatal(err)
	}
	_, err = svc.SetUserStatus(platformCtx(uuid.New(), domain.RolePlatformAdmin), other.ID, disable, "")
	_ = wantErr(t, err, 409, CodeLastPlatformAdmin)
}

func TestReplacePlatformRoles(t *testing.T) {
	svc, st, _ := newRoleEnv(t)
	root := st.addUser(domain.UserActive, domain.RolePlatformAdmin)
	ctx := platformCtx(root.ID, domain.RolePlatformAdmin)
	u := st.addUser(domain.UserActive)

	_, err := svc.ReplacePlatformRoles(ctx, uuid.New(), nil, true, "")
	_ = wantErr(t, err, 404, problem.CodeNotFound)
	_, err = svc.ReplacePlatformRoles(ctx, u.ID, nil, false, "")
	_ = wantErr(t, err, 422, problem.CodeValidationFailed)
	_, err = svc.ReplacePlatformRoles(ctx, u.ID, []string{"tenant_admin", "platform_auditor", "platform_auditor"}, true, "")
	if e := wantErr(t, err, 422, problem.CodeValidationFailed); !hasField(e, "platform_roles[0]", "INVALID_VALUE") || !hasField(e, "platform_roles[2]", "DUPLICATE") {
		t.Fatalf("fields = %+v", e.Fields)
	}
	// Nadie cambia sus propios roles de plataforma.
	_, err = svc.ReplacePlatformRoles(ctx, root.ID, []string{domain.RolePlatformAdmin, "platform_auditor"}, true, "")
	_ = wantErr(t, err, 403, problem.CodePermissionDenied)

	// Conceder no revoca sesiones; el usuario sale con sus roles nuevos.
	got, err := svc.ReplacePlatformRoles(ctx, u.ID, []string{"platform_operator", domain.RolePlatformAdmin}, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.PlatformRoles, []string{domain.RolePlatformAdmin, "platform_operator"}) || len(st.sessions[u.ID]) != 2 {
		t.Fatalf("usuario = %+v, sesiones = %v", got, st.sessions[u.ID])
	}
	last := st.audits[len(st.audits)-1]
	if last.Action != "platform.user.roles_replaced" || last.TenantID != uuid.Nil || last.ResourceID != u.ID.String() {
		t.Fatalf("auditoría = %+v", last)
	}
	// Quitar roles revoca sus sesiones.
	got, err = svc.ReplacePlatformRoles(ctx, u.ID, []string{}, true, "")
	if err != nil || len(got.PlatformRoles) != 0 || len(st.sessions[u.ID]) != 0 {
		t.Fatalf("usuario = %+v, %v, sesiones = %v", got, err, st.sessions[u.ID])
	}
	if st.audits[len(st.audits)-1].Changes["revoked_sessions"] != 2 {
		t.Fatalf("auditoría = %+v", st.audits[len(st.audits)-1])
	}
	// El último platform_admin activo conserva el rol.
	_, err = svc.ReplacePlatformRoles(platformCtx(uuid.New(), domain.RolePlatformAdmin), root.ID, []string{"platform_auditor"}, true, "")
	_ = wantErr(t, err, 409, CodeLastPlatformAdmin)
}

func strp(s string) *string { return &s }
