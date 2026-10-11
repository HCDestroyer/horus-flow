package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/app"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// fakeStore simula el repositorio para probar el adaptador HTTP.
type fakeStore struct {
	app.Store
	roles   []domain.TenantRoleRecord
	members []domain.Member
	users   []domain.PlatformUser
	current *domain.Member
	invites int
}

func (f *fakeStore) TenantRoles(context.Context, uuid.UUID) ([]domain.TenantRoleRecord, error) {
	return f.roles, nil
}

func (f *fakeStore) ListMembers(_ context.Context, _ uuid.UUID, flt app.MemberFilter) ([]domain.Member, error) {
	if len(f.members) > flt.Limit {
		return f.members[:flt.Limit], nil
	}
	return f.members, nil
}

func (f *fakeStore) ListUsers(context.Context, app.UserFilter) ([]domain.PlatformUser, error) {
	return f.users, nil
}

func (f *fakeStore) InviteMember(context.Context, app.NewInvitation) (*app.InviteResult, error) {
	f.invites++
	return &app.InviteResult{MembershipID: uuid.New(), UserID: uuid.New(), Status: domain.MembershipActive}, nil
}

func (f *fakeStore) Member(_ context.Context, _, userID uuid.UUID) (*domain.Member, error) {
	if f.current == nil || f.current.UserID != userID {
		return nil, domain.ErrNotFound
	}
	m := *f.current
	return &m, nil
}

func (f *fakeStore) RevokeMember(context.Context, uuid.UUID, uuid.UUID, time.Time, app.MemberEvent) (bool, error) {
	return true, nil
}

func (f *fakeStore) ReplaceAssignments(_ context.Context, _, _ uuid.UUID, expect int, as []domain.Assignment, _ time.Time, _ app.MemberEvent) (*domain.Member, error) {
	if expect != f.current.Version {
		return nil, &app.MemberVersionError{Current: f.current}
	}
	m := *f.current
	m.Version++
	m.Assignments = as
	return &m, nil
}

func (f *fakeStore) InvitationByHash(context.Context, []byte) (*domain.Invitation, error) {
	return nil, domain.ErrNotFound
}

func (f *fakeStore) InsertAudit(context.Context, api.AuditEntry) error { return nil }

type env struct {
	t      *testing.T
	srv    *httptest.Server
	signer *authz.Signer
	store  *fakeStore
	cat    *domain.Catalog
	tenant uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	cat, err := domain.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	key, err := authz.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer := authz.NewSigner(key, "horus-auth", 10*time.Minute, nil)
	keys := authz.KeySet{}
	keys.Add(signer.PublicKey())
	st := &fakeStore{}
	for _, r := range cat.TenantRoles {
		st.roles = append(st.roles, domain.TenantRoleRecord{ID: r.ID, Key: r.Key, Name: r.Name, IsSystem: true, RequiresMFA: r.RequiresMFA, Permissions: r.Permissions})
	}
	svc, err := app.NewService(st, app.Options{Catalog: cat, Argon: domain.Argon2Params{Memory: 1024, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}})
	if err != nil {
		t.Fatal(err)
	}
	mux := httpx.NewMux(nil, nil)
	New(svc, authz.NewGuard(authz.NewVerifier(keys, "horus-auth", nil)), Options{}).Mount(mux.ForService("auth"))
	srv := httptest.NewServer(mux.Handler())
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, signer: signer, store: st, cat: cat, tenant: uuid.New()}
}

func (e *env) token(scope string, role string, authAge time.Duration) string {
	e.t.Helper()
	uid := uuid.New()
	c := authz.Claims{Typ: authz.TypeUser, Scope: scope, SID: uuid.NewString(), AuthTime: time.Now().Add(-authAge).Unix()}
	c.Subject = uid.String()
	perms := map[string][]string{}
	switch scope {
	case authz.ScopeTenant:
		c.TID = e.tenant.String()
		r, _ := e.cat.TenantRole(role)
		for _, p := range r.Permissions {
			perms[p] = []string{authz.ScopeAll}
		}
	case authz.ScopePlatform:
		r, _ := e.cat.PlatformRole(role)
		for _, p := range r.Permissions {
			perms[p] = []string{authz.ScopeAll}
		}
	}
	c.Perms = perms
	tok, _, err := e.signer.Sign(c)
	if err != nil {
		e.t.Fatal(err)
	}
	return tok
}

type result struct {
	status int
	header http.Header
	body   map[string]any
	raw    string
}

func (e *env) do(method, path, token, body string, hdr ...string) result {
	e.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	r := result{status: resp.StatusCode, header: resp.Header, raw: string(b)}
	_ = json.Unmarshal(b, &r.body)
	return r
}

func (r result) code() string { s, _ := r.body["code"].(string); return s }

// Las rutas del contrato que el gateway enruta a auth existen en el proceso
// (antes respondían 503 "El módulo auth no atiende esta ruta").
func TestContractRoutesRegistered(t *testing.T) {
	e := newEnv(t)
	uid := uuid.NewString()
	for _, rt := range []struct{ method, path string }{
		{"PATCH", "/api/v1/me"},
		{"GET", "/api/v1/members"},
		{"POST", "/api/v1/members"},
		{"DELETE", "/api/v1/members/" + uid},
		{"PUT", "/api/v1/members/" + uid + "/role-assignments"},
		{"GET", "/api/v1/platform/users"},
		{"GET", "/api/v1/roles"},
	} {
		if r := e.do(rt.method, rt.path, "", "{}"); r.status != http.StatusUnauthorized || r.code() != "UNAUTHENTICATED" {
			t.Errorf("%s %s sin token = %d %s", rt.method, rt.path, r.status, r.raw)
		}
	}
	if r := e.do("POST", "/api/v1/auth/invitations/accept", "", `{"token":"`+strings.Repeat("x", 43)+`"}`); r.status != 422 || r.code() != "VALIDATION_FAILED" {
		t.Errorf("accept = %d %s", r.status, r.raw)
	}
}

func TestMembersScopeAndPermissions(t *testing.T) {
	e := newEnv(t)
	if r := e.do("GET", "/api/v1/members", e.token(authz.ScopeSession, "", 0), ""); r.status != 403 || r.code() != "TOKEN_SCOPE_INVALID" {
		t.Fatalf("token de sesión = %d %s", r.status, r.raw)
	}
	if r := e.do("GET", "/api/v1/members", e.token(authz.ScopeTenant, "noc", 0), ""); r.status != 403 || r.code() != "PERMISSION_DENIED" {
		t.Fatalf("noc = %d %s", r.status, r.raw)
	}
	if r := e.do("POST", "/api/v1/members", e.token(authz.ScopeTenant, "viewer", 0), `{"email":"a@isp.test","role_assignments":[]}`); r.status != 403 {
		t.Fatalf("viewer invita = %d %s", r.status, r.raw)
	}
	if r := e.do("GET", "/api/v1/platform/users", e.token(authz.ScopeTenant, "tenant_admin", 0), ""); r.status != 403 || r.code() != "TOKEN_SCOPE_INVALID" {
		t.Fatalf("platform/users con token de ISP = %d %s", r.status, r.raw)
	}
	if r := e.do("GET", "/api/v1/platform/users", e.token(authz.ScopePlatform, "platform_operator", 0), ""); r.status != 403 || r.code() != "PERMISSION_DENIED" {
		t.Fatalf("platform_operator = %d %s", r.status, r.raw)
	}
}

func TestMembersAndRolesJSON(t *testing.T) {
	e := newEnv(t)
	viewer, _ := e.cat.TenantRole("viewer")
	e.store.members = []domain.Member{{MembershipID: uuid.New(), UserID: uuid.New(), TenantID: e.tenant, DisplayName: "Ana", Email: "ana@isp.test",
		Status: domain.MembershipActive, MFAEnabled: true, Version: 2, CreatedAt: time.Now(),
		Assignments: []domain.Assignment{{RoleID: viewer.ID, RoleKey: "viewer", ScopeType: "tenant"}}}}
	tok := e.token(authz.ScopeTenant, "viewer", 0)
	r := e.do("GET", "/api/v1/members?limit=10&include_total=false", tok, "")
	if r.status != 200 {
		t.Fatalf("members = %d %s", r.status, r.raw)
	}
	data, _ := r.body["data"].([]any)
	m, _ := data[0].(map[string]any)
	for _, k := range []string{"user_id", "tenant_id", "display_name", "email", "status", "mfa_enabled", "role_assignments", "version"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("Member sin %s: %s", k, r.raw)
		}
	}
	ra, _ := m["role_assignments"].([]any)
	if a, _ := ra[0].(map[string]any); a["scope"] != "tenant" || a["role_key"] != "viewer" || a["role_id"] != viewer.ID.String() {
		t.Fatalf("role_assignments = %s", r.raw)
	}
	page, _ := r.body["page"].(map[string]any)
	for _, k := range []string{"next_cursor", "prev_cursor", "has_more", "limit"} {
		if _, ok := page[k]; !ok {
			t.Fatalf("page sin %s: %s", k, r.raw)
		}
	}
	if r := e.do("GET", "/api/v1/members?status=borrado", tok, ""); r.status != 400 || r.code() != "INVALID_FILTER" {
		t.Fatalf("status inválido = %d %s", r.status, r.raw)
	}
	if r := e.do("GET", "/api/v1/members?limit=0", tok, ""); r.status != 400 {
		t.Fatalf("limit=0 = %d %s", r.status, r.raw)
	}

	r = e.do("GET", "/api/v1/roles", tok, "")
	if r.status != 200 {
		t.Fatalf("roles = %d %s", r.status, r.raw)
	}
	roles, _ := r.body["data"].([]any)
	if len(roles) != len(e.cat.TenantRoles) {
		t.Fatalf("roles = %s", r.raw)
	}
	ro, _ := roles[0].(map[string]any)
	for _, k := range []string{"id", "tenant_id", "key", "name", "is_system", "requires_mfa", "permissions"} {
		if _, ok := ro[k]; !ok {
			t.Fatalf("Role sin %s: %s", k, r.raw)
		}
	}
	if _, ok := r.body["page"].(map[string]any); !ok {
		t.Fatalf("roles sin page: %s", r.raw)
	}
}

func TestInviteMemberHTTP(t *testing.T) {
	e := newEnv(t)
	viewer, _ := e.cat.TenantRole("viewer")
	tok := e.token(authz.ScopeTenant, "tenant_admin", 0)
	body := `{"email":"nuevo@isp.test","role_assignments":[{"role_id":"` + viewer.ID.String() + `","scope":"tenant"}]}`
	if r := e.do("POST", "/api/v1/members", tok, body, "Idempotency-Key", "no-uuid"); r.status != 400 {
		t.Fatalf("Idempotency-Key inválida = %d %s", r.status, r.raw)
	}
	r := e.do("POST", "/api/v1/members", tok, body, "Idempotency-Key", uuid.NewString())
	if r.status != http.StatusAccepted || r.raw != "" || e.store.invites != 1 {
		t.Fatalf("invitar = %d %q", r.status, r.raw)
	}
	if r := e.do("POST", "/api/v1/members", tok, `{"email":"x","role_assignments":[]}`); r.status != 422 || r.code() != "VALIDATION_FAILED" {
		t.Fatalf("inválida = %d %s", r.status, r.raw)
	}
	if r := e.do("DELETE", "/api/v1/members/"+uuid.NewString(), tok, ""); r.status != http.StatusNoContent {
		t.Fatalf("quitar = %d %s", r.status, r.raw)
	}
	if r := e.do("DELETE", "/api/v1/members/no-uuid", tok, ""); r.status != 404 {
		t.Fatalf("quitar id inválido = %d %s", r.status, r.raw)
	}
}

func TestReplaceAssignmentsHTTP(t *testing.T) {
	e := newEnv(t)
	viewer, _ := e.cat.TenantRole("viewer")
	uid := uuid.New()
	e.store.current = &domain.Member{MembershipID: uuid.New(), UserID: uid, TenantID: e.tenant, DisplayName: "Ana", Email: "ana@isp.test",
		Status: domain.MembershipActive, Version: 3, Assignments: []domain.Assignment{}}
	path := "/api/v1/members/" + uid.String() + "/role-assignments"
	body := `[{"role_id":"` + viewer.ID.String() + `","scope":"tenant"}]`

	if r := e.do("PUT", path, e.token(authz.ScopeTenant, "tenant_admin", time.Hour), body, "If-Match", `"3"`); r.status != 403 || r.code() != "REAUTH_REQUIRED" {
		t.Fatalf("sin re-auth = %d %s", r.status, r.raw)
	}
	fresh := e.token(authz.ScopeTenant, "tenant_admin", 0)
	if r := e.do("PUT", path, fresh, body); r.status != 428 {
		t.Fatalf("sin If-Match = %d %s", r.status, r.raw)
	}
	r := e.do("PUT", path, fresh, body, "If-Match", `"2"`)
	if r.status != 412 || r.code() != "PRECONDITION_FAILED" {
		t.Fatalf("If-Match viejo = %d %s", r.status, r.raw)
	}
	if cur, _ := r.body["current"].(map[string]any); cur["user_id"] != uid.String() || cur["version"] != float64(3) {
		t.Fatalf("412 sin current: %s", r.raw)
	}
	if r := e.do("PUT", "/api/v1/members/"+uuid.NewString()+"/role-assignments", fresh, `{}`, "If-Match", `"3"`); r.status != 404 {
		t.Fatalf("miembro inexistente = %d %s", r.status, r.raw)
	}
	r = e.do("PUT", path, fresh, body, "If-Match", `"3"`)
	if r.status != 200 || r.header.Get("ETag") != `"4"` {
		t.Fatalf("reemplazo = %d %s %v", r.status, r.raw, r.header)
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(r.raw), &arr); err != nil || len(arr) != 1 || arr[0]["role_key"] != "viewer" || arr[0]["scope"] != "tenant" {
		t.Fatalf("respuesta = %s", r.raw)
	}
}

func TestPlatformUsersJSON(t *testing.T) {
	e := newEnv(t)
	e.store.users = []domain.PlatformUser{{ID: uuid.New(), Email: "root@horus.test", DisplayName: "Root", Status: domain.UserActive, MFAEnabled: true,
		PlatformRoles: []string{"platform_admin"}, CreatedAt: time.Now(),
		Memberships: []domain.PlatformMembership{{TenantID: e.tenant, TenantSlug: "isp-a", RoleKeys: []string{"tenant_admin"}}}}}
	r := e.do("GET", "/api/v1/platform/users?q=root", e.token(authz.ScopePlatform, "platform_admin", 0), "")
	if r.status != 200 {
		t.Fatalf("platform/users = %d %s", r.status, r.raw)
	}
	data, _ := r.body["data"].([]any)
	u, _ := data[0].(map[string]any)
	for _, k := range []string{"id", "email", "display_name", "status", "mfa_enabled", "platform_roles", "memberships"} {
		if _, ok := u[k]; !ok {
			t.Fatalf("PlatformUser sin %s: %s", k, r.raw)
		}
	}
	ms, _ := u["memberships"].([]any)
	if m, _ := ms[0].(map[string]any); m["tenant_slug"] != "isp-a" || m["tenant_id"] != e.tenant.String() {
		t.Fatalf("memberships = %s", r.raw)
	}
}
