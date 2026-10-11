package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	inviteS string // estado de la membresía tras InviteMember
	inv     *domain.Invitation
	invHash []byte
	reissue int
}

func (f *fakeStore) Tenant(_ context.Context, id uuid.UUID) (*domain.Tenant, error) {
	return &domain.Tenant{ID: id, Name: "ISP Uno"}, nil
}

func (f *fakeStore) ReissueInvitation(_ context.Context, in app.ReissuedInvitation) (*app.ReissueResult, error) {
	f.reissue++
	f.invHash = in.TokenHash
	return &app.ReissueResult{MembershipID: f.current.MembershipID, Email: f.current.Email, Replaced: 1}, nil
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

func (f *fakeStore) InviteMember(_ context.Context, in app.NewInvitation) (*app.InviteResult, error) {
	f.invites++
	if f.inviteS == domain.MembershipInvited {
		f.invHash = in.TokenHash
		return &app.InviteResult{MembershipID: uuid.New(), UserID: uuid.New(), Status: domain.MembershipInvited, UserCreated: true, TokenStored: true}, nil
	}
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

func (f *fakeStore) InvitationByHash(_ context.Context, hash []byte) (*domain.Invitation, error) {
	if f.inv == nil || !bytes.Equal(hash, f.invHash) {
		return nil, domain.ErrNotFound
	}
	i := *f.inv
	return &i, nil
}

// fakeSender es el canal de correo de las invitaciones.
type fakeSender struct {
	got []app.InvitationDelivery
	err error
}

func (s *fakeSender) SendInvitation(_ context.Context, d app.InvitationDelivery) error {
	s.got = append(s.got, d)
	return s.err
}

func (f *fakeStore) InsertAudit(context.Context, api.AuditEntry) error { return nil }

type env struct {
	t      *testing.T
	srv    *httptest.Server
	signer *authz.Signer
	store  *fakeStore
	sender *fakeSender
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
	snd := &fakeSender{}
	svc, err := app.NewService(st, app.Options{Catalog: cat, Argon: domain.Argon2Params{Memory: 1024, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32},
		Invitations: snd, PublicBaseURL: "https://horus.example"})
	if err != nil {
		t.Fatal(err)
	}
	mux := httpx.NewMux(nil, nil)
	New(svc, authz.NewGuard(authz.NewVerifier(keys, "horus-auth", nil)), Options{}).Mount(mux.ForService("auth"))
	srv := httptest.NewServer(mux.Handler())
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, signer: signer, store: st, sender: snd, cat: cat, tenant: uuid.New()}
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
	defer func() { _ = resp.Body.Close() }()
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
		{"POST", "/api/v1/members/" + uid + "/invitation"},
		{"PUT", "/api/v1/members/" + uid + "/role-assignments"},
		{"GET", "/api/v1/platform/users"},
		{"GET", "/api/v1/roles"},
	} {
		if r := e.do(rt.method, rt.path, "", "{}"); r.status != http.StatusUnauthorized || r.code() != "UNAUTHENTICATED" {
			t.Errorf("%s %s sin token = %d %s", rt.method, rt.path, r.status, r.raw)
		}
	}
	for _, p := range []string{"accept", "inspect"} {
		if r := e.do("POST", "/api/v1/auth/invitations/"+p, "", `{"token":"`+strings.Repeat("x", 43)+`"}`); r.status != 422 || r.code() != "VALIDATION_FAILED" {
			t.Errorf("%s = %d %s", p, r.status, r.raw)
		}
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
	// Cuenta activa existente: added, sin enlace.
	r := e.do("POST", "/api/v1/members", tok, body, "Idempotency-Key", uuid.NewString())
	if r.status != http.StatusAccepted || e.store.invites != 1 || r.body["outcome"] != "added" || r.body["invitation_url"] != nil ||
		r.body["expires_at"] != nil || r.body["email_sent"] != false || r.body["user_id"] == nil {
		t.Fatalf("invitar = %d %q", r.status, r.raw)
	}
	for _, k := range []string{"invitation_url", "expires_at"} {
		if _, ok := r.body[k]; !ok {
			t.Fatalf("InvitationIssued sin %s: %s", k, r.raw)
		}
	}
	// Email nuevo: invited con el enlace (token en el fragmento) y el correo enviado.
	e.store.inviteS = domain.MembershipInvited
	r = e.do("POST", "/api/v1/members", tok, body)
	link, _ := r.body["invitation_url"].(string)
	if r.status != http.StatusAccepted || r.body["outcome"] != "invited" || r.body["email_sent"] != true || r.header.Get("Cache-Control") != "no-store" ||
		!strings.HasPrefix(link, "https://horus.example/invitation#token=") || len(e.sender.got) != 1 || e.sender.got[0].URL != link {
		t.Fatalf("invitar nuevo = %d %s", r.status, r.raw)
	}
	if exp, _ := r.body["expires_at"].(string); !strings.HasSuffix(exp, "Z") {
		t.Fatalf("expires_at = %s", r.raw)
	}
	// El correo falla: la invitación sigue (202) con email_sent=false.
	e.sender.err = errors.New("smtp: RCPT: 550")
	r = e.do("POST", "/api/v1/members", tok, body)
	if r.status != http.StatusAccepted || r.body["email_sent"] != false || r.body["invitation_url"] == nil {
		t.Fatalf("correo fallido = %d %s", r.status, r.raw)
	}
	e.sender.err = nil
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

func TestReissueAndInspectHTTP(t *testing.T) {
	e := newEnv(t)
	noc, _ := e.cat.TenantRole("noc")
	uid := uuid.New()
	e.store.current = &domain.Member{MembershipID: uuid.New(), UserID: uid, TenantID: e.tenant, Email: "inv@isp.test", Status: domain.MembershipActive,
		Version: 1, Assignments: []domain.Assignment{{RoleID: noc.ID, RoleKey: noc.Key, ScopeType: "tenant", Permissions: noc.Permissions}}}
	tok := e.token(authz.ScopeTenant, "tenant_admin", 0)
	path := "/api/v1/members/" + uid.String() + "/invitation"

	if r := e.do("POST", path, e.token(authz.ScopeTenant, "viewer", 0), ""); r.status != 403 {
		t.Fatalf("viewer = %d %s", r.status, r.raw)
	}
	if r := e.do("POST", "/api/v1/members/"+uuid.NewString()+"/invitation", tok, ""); r.status != 404 || r.code() != "NOT_FOUND" {
		t.Fatalf("no miembro = %d %s", r.status, r.raw)
	}
	if r := e.do("POST", "/api/v1/members/no-uuid/invitation", tok, ""); r.status != 404 {
		t.Fatalf("id inválido = %d %s", r.status, r.raw)
	}
	if r := e.do("POST", path, tok, ""); r.status != 409 || r.code() != "CONFLICT" || e.store.reissue != 0 {
		t.Fatalf("miembro activo = %d %s", r.status, r.raw)
	}
	e.store.current.Status = domain.MembershipInvited
	r := e.do("POST", path, tok, "")
	link, _ := r.body["invitation_url"].(string)
	if r.status != 200 || r.body["outcome"] != "invited" || r.body["user_id"] != uid.String() || r.body["email_sent"] != true ||
		r.header.Get("Cache-Control") != "no-store" || !strings.HasPrefix(link, "https://horus.example/invitation#token=") || e.store.reissue != 1 {
		t.Fatalf("reenviar = %d %s", r.status, r.raw)
	}

	// Inspección del enlace: sin consumirlo, sin token de acceso.
	token := strings.TrimPrefix(link, "https://horus.example/invitation#token=")
	e.store.inv = &domain.Invitation{ID: uuid.New(), TenantID: e.tenant, MembershipID: e.store.current.MembershipID, UserID: uid,
		ExpiresAt: time.Now().Add(time.Hour), MembershipStatus: domain.MembershipInvited, UserStatus: domain.UserPending,
		UserEmail: "inv@isp.test", TenantName: "ISP Uno"}
	r = e.do("POST", "/api/v1/auth/invitations/inspect", "", `{"token":"`+token+`"}`)
	if r.status != 200 || r.body["email"] != "inv@isp.test" || r.body["tenant_name"] != "ISP Uno" || r.body["needs_password"] != true ||
		r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("inspect = %d %s", r.status, r.raw)
	}
	if exp, _ := r.body["expires_at"].(string); !strings.HasSuffix(exp, "Z") {
		t.Fatalf("expires_at = %s", r.raw)
	}
	// Usada, caducada y desconocida: el mismo 422 que accept.
	unknown := e.do("POST", "/api/v1/auth/invitations/inspect", "", `{"token":"`+strings.Repeat("q", 43)+`"}`)
	used := *e.store.inv
	now := time.Now()
	used.UsedAt = &now
	e.store.inv = &used
	usedR := e.do("POST", "/api/v1/auth/invitations/inspect", "", `{"token":"`+token+`"}`)
	expired := used
	expired.UsedAt, expired.ExpiresAt = nil, now.Add(-time.Minute)
	e.store.inv = &expired
	expiredR := e.do("POST", "/api/v1/auth/invitations/inspect", "", `{"token":"`+token+`"}`)
	acceptR := e.do("POST", "/api/v1/auth/invitations/accept", "", `{"token":"`+token+`"}`)
	strip := func(r result) string {
		delete(r.body, "request_id")
		delete(r.body, "instance")
		delete(r.body, "trace_id")
		b, _ := json.Marshal(r.body)
		return string(b)
	}
	for name, x := range map[string]result{"desconocida": unknown, "usada": usedR, "caducada": expiredR, "accept": acceptR} {
		if x.status != 422 || x.code() != "VALIDATION_FAILED" || !strings.Contains(x.raw, `"INVITATION_INVALID"`) || strip(x) != strip(unknown) {
			t.Fatalf("%s = %d %s (desconocida: %s)", name, x.status, x.raw, unknown.raw)
		}
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
