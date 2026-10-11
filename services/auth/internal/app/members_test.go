package app

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// memMemberStore simula lo que necesitan los casos de uso de miembros.
type memMemberStore struct {
	Store
	roles    []domain.TenantRoleRecord
	invited  *NewInvitation
	inviteRS InviteResult
	inv      *domain.Invitation
	accepted *AcceptedInvitation
	acceptEr error
	replace  func(expect int, as []domain.Assignment) (*domain.Member, error)
	revoked  bool
	member   *domain.Member
	user     *domain.User
	session  *domain.Session
	ms       []domain.Membership
	profile  *domain.User
	audits   []api.AuditEntry
}

func (m *memMemberStore) TenantRoles(context.Context, uuid.UUID) ([]domain.TenantRoleRecord, error) {
	return m.roles, nil
}

func (m *memMemberStore) InviteMember(_ context.Context, in NewInvitation) (*InviteResult, error) {
	m.invited = &in
	r := m.inviteRS
	return &r, nil
}

func (m *memMemberStore) Member(_ context.Context, _, userID uuid.UUID) (*domain.Member, error) {
	if m.member == nil || m.member.UserID != userID {
		return nil, domain.ErrNotFound
	}
	c := *m.member
	return &c, nil
}

func (m *memMemberStore) RevokeMember(context.Context, uuid.UUID, uuid.UUID, time.Time, MemberEvent) (bool, error) {
	return m.revoked, nil
}

func (m *memMemberStore) ReplaceAssignments(_ context.Context, _, _ uuid.UUID, expect int, as []domain.Assignment, _ time.Time, ev MemberEvent) (*domain.Member, error) {
	r, err := m.replace(expect, as)
	if err == nil && ev != nil {
		ev(*r, nil)
	}
	return r, err
}

func (m *memMemberStore) InvitationByHash(_ context.Context, hash []byte) (*domain.Invitation, error) {
	if m.inv == nil || !bytes.Equal(hash, domain.HashSecret(validToken)) {
		return nil, domain.ErrNotFound
	}
	i := *m.inv
	return &i, nil
}

func (m *memMemberStore) AcceptInvitation(_ context.Context, in AcceptedInvitation) error {
	if m.acceptEr != nil {
		return m.acceptEr
	}
	m.accepted = &in
	return nil
}

func (m *memMemberStore) InsertAudit(_ context.Context, e api.AuditEntry) error {
	m.audits = append(m.audits, e)
	return nil
}

func (m *memMemberStore) Session(context.Context, uuid.UUID) (*domain.Session, error) {
	return m.session, nil
}
func (m *memMemberStore) UserByID(context.Context, uuid.UUID) (*domain.User, error) {
	u := *m.user
	return &u, nil
}
func (m *memMemberStore) PlatformRoles(context.Context, uuid.UUID) ([]string, error) { return nil, nil }
func (m *memMemberStore) Memberships(context.Context, uuid.UUID) ([]domain.Membership, error) {
	return m.ms, nil
}
func (m *memMemberStore) TOTP(context.Context, uuid.UUID) (*domain.TOTP, error) {
	return nil, domain.ErrNotFound
}
func (m *memMemberStore) UpdateProfile(_ context.Context, u *domain.User, _ time.Time) error {
	c := *u
	m.profile = &c
	return nil
}

const validToken = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"

type captureSender struct{ got []InvitationDelivery }

func (c *captureSender) SendInvitation(_ context.Context, d InvitationDelivery) error {
	c.got = append(c.got, d)
	return nil
}

func newMemberEnv(t *testing.T) (*Service, *memMemberStore, *captureSender, *domain.Catalog) {
	t.Helper()
	cat, err := domain.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	st := &memMemberStore{}
	for _, r := range cat.TenantRoles {
		st.roles = append(st.roles, domain.TenantRoleRecord{ID: r.ID, Key: r.Key, Name: r.Name, IsSystem: true, RequiresMFA: r.RequiresMFA, Permissions: r.Permissions})
	}
	snd := &captureSender{}
	svc, err := NewService(st, Options{Catalog: cat, Invitations: snd,
		Argon: domain.Argon2Params{Memory: 1024, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}})
	if err != nil {
		t.Fatal(err)
	}
	return svc, st, snd, cat
}

func tenantCtx(tenant uuid.UUID, perms map[string][]string) context.Context {
	uid := uuid.New()
	return authz.WithPrincipal(context.Background(), &authz.Principal{Type: authz.TypeUser, Subject: uid.String(), UserID: uid,
		SessionID: uuid.New(), Scope: authz.ScopeTenant, TenantID: tenant, Perms: perms})
}

func allPerms(c *domain.Catalog, role string) map[string][]string {
	r, _ := c.TenantRole(role)
	out := map[string][]string{}
	for _, p := range r.Permissions {
		out[p] = []string{authz.ScopeAll}
	}
	return out
}

func wantErr(t *testing.T, err error, status int, code string) *apperr.Error {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Status() != status || e.Code != code {
		t.Fatalf("err = %v, want %d %s", err, status, code)
	}
	return e
}

func hasField(e *apperr.Error, field, code string) bool {
	for _, f := range e.Fields {
		if f.Field == field && f.Code == code {
			return true
		}
	}
	return false
}

func TestInviteMemberValidation(t *testing.T) {
	svc, _, _, cat := newMemberEnv(t)
	tenant := uuid.New()
	ctx := tenantCtx(tenant, allPerms(cat, "tenant_admin"))
	admin, _ := cat.TenantRole("tenant_admin")

	e := wantErr(t, svc.InviteMember(ctx, "no-es-email", nil, ""), 422, problem.CodeValidationFailed)
	if !hasField(e, "email", "INVALID_EMAIL") || !hasField(e, "role_assignments", "REQUIRED") {
		t.Fatalf("fields = %+v", e.Fields)
	}
	e = wantErr(t, svc.InviteMember(ctx, "a@isp.test", []AssignmentInput{{RoleID: uuid.NewString(), Scope: "tenant"}}, ""), 422, problem.CodeValidationFailed)
	if !hasField(e, "role_assignments[0].role_id", CodeUnknownRole) {
		t.Fatalf("fields = %+v", e.Fields)
	}
	e = wantErr(t, svc.InviteMember(ctx, "a@isp.test", []AssignmentInput{{RoleID: admin.ID.String(), Scope: "site:nope"}}, ""), 422, problem.CodeValidationFailed)
	if !hasField(e, "role_assignments[0].scope", "INVALID_FORMAT") {
		t.Fatalf("fields = %+v", e.Fields)
	}
	// Sin users.manage (defensa en profundidad tras el guard).
	wantErr(t, svc.InviteMember(tenantCtx(tenant, allPerms(cat, "viewer")), "a@isp.test",
		[]AssignmentInput{{RoleID: admin.ID.String(), Scope: "tenant"}}, ""), 403, problem.CodePermissionDenied)
	// Un token de sesión (sin tenant) no sirve.
	wantErr(t, svc.InviteMember(authz.WithPrincipal(context.Background(), &authz.Principal{Type: authz.TypeUser, Scope: authz.ScopeSession}),
		"a@isp.test", nil, ""), 403, problem.CodeTokenScopeInvalid)
}

// security.md §2 (E): nadie concede un permiso que no tiene en ese alcance.
func TestAssignmentsCannotEscalate(t *testing.T) {
	svc, st, _, cat := newMemberEnv(t)
	tenant := uuid.New()
	site := uuid.New()
	admin, _ := cat.TenantRole("tenant_admin")
	noc, _ := cat.TenantRole("noc")
	perms := allPerms(cat, "noc")
	perms[PermUsersManage] = []string{authz.ScopeAll}
	ctx := tenantCtx(tenant, perms)

	wantErr(t, svc.InviteMember(ctx, "a@isp.test", []AssignmentInput{{RoleID: admin.ID.String(), Scope: "tenant"}}, ""), 403, problem.CodePermissionDenied)
	if st.invited != nil {
		t.Fatal("se invitó con un rol con más permisos que el invitador")
	}
	if err := svc.InviteMember(ctx, "a@isp.test", []AssignmentInput{{RoleID: noc.ID.String(), Scope: "tenant"}, {RoleID: noc.ID.String(), Scope: "tenant"}}, ""); err != nil {
		t.Fatal(err)
	}
	if len(st.invited.Assignments) != 1 || st.invited.Assignments[0].RoleKey != "noc" {
		t.Fatalf("assignments = %+v", st.invited.Assignments)
	}

	// Con permisos solo en un nodo: puede conceder en ese nodo, no en todo el tenant.
	scoped := map[string][]string{PermUsersManage: {authz.ScopeAll}}
	for p := range allPerms(cat, "noc") {
		scoped[p] = []string{"site:" + site.String()}
	}
	sctx := tenantCtx(tenant, scoped)
	wantErr(t, svc.InviteMember(sctx, "b@isp.test", []AssignmentInput{{RoleID: noc.ID.String(), Scope: "tenant"}}, ""), 403, problem.CodePermissionDenied)
	if err := svc.InviteMember(sctx, "b@isp.test", []AssignmentInput{{RoleID: noc.ID.String(), Scope: "site:" + site.String()}}, ""); err != nil {
		t.Fatal(err)
	}
}

func TestInviteDeliversTokenOnlyWhenStored(t *testing.T) {
	svc, st, snd, cat := newMemberEnv(t)
	tenant := uuid.New()
	ctx := tenantCtx(tenant, allPerms(cat, "tenant_admin"))
	viewer, _ := cat.TenantRole("viewer")
	in := []AssignmentInput{{RoleID: viewer.ID.String(), Scope: "tenant"}}

	st.inviteRS = InviteResult{MembershipID: uuid.New(), UserID: uuid.New(), Status: domain.MembershipActive, AlreadyMember: true}
	if err := svc.InviteMember(ctx, "ya@isp.test", in, ""); err != nil {
		t.Fatal(err)
	}
	if len(snd.got) != 0 {
		t.Fatal("se entregó un token sin invitación guardada")
	}
	st.inviteRS = InviteResult{MembershipID: uuid.New(), UserID: uuid.New(), Status: domain.MembershipInvited, UserCreated: true, TokenStored: true}
	if err := svc.InviteMember(ctx, "nuevo@isp.test", in, ""); err != nil {
		t.Fatal(err)
	}
	if len(snd.got) != 1 {
		t.Fatalf("entregas = %d", len(snd.got))
	}
	d := snd.got[0]
	if len(d.Token) < 32 || !bytes.Equal(domain.HashSecret(d.Token), st.invited.TokenHash) || d.Email != "nuevo@isp.test" || d.TenantID != tenant {
		t.Fatalf("entrega = %+v", d)
	}
	if ttl := st.invited.ExpiresAt.Sub(st.invited.Now); ttl != domain.InvitationTTL {
		t.Fatalf("caducidad = %v", ttl)
	}
	// El evento es membership.granted con el sobre C4.
	ev := st.invited.Event(domain.Member{MembershipID: uuid.New(), UserID: uuid.New(), Status: domain.MembershipInvited, Version: 1,
		Assignments: st.invited.Assignments}, nil)
	if ev.Type != "horus.auth.membership.granted" || ev.TenantID == nil || *ev.TenantID != tenant {
		t.Fatalf("evento = %+v", ev)
	}
	for _, a := range st.audits {
		if b, _ := json.Marshal(a.Changes); bytes.Contains(b, []byte(d.Token)) {
			t.Fatal("el token aparece en la auditoría")
		}
	}
}

func TestAcceptInvitation(t *testing.T) {
	svc, st, _, _ := newMemberEnv(t)
	now := time.Now()
	base := domain.Invitation{ID: uuid.New(), TenantID: uuid.New(), MembershipID: uuid.New(), UserID: uuid.New(),
		ExpiresAt: now.Add(time.Hour), MembershipStatus: domain.MembershipInvited, UserStatus: domain.UserPending,
		UserEmail: "nueva@isp.test", UserDisplayName: "nueva"}
	ctx := context.Background()
	pw := func(s string) *string { return &s }

	wantErr(t, svc.AcceptInvitation(ctx, AcceptInvitationInput{Token: "corto"}, LoginInput{}), 422, problem.CodeValidationFailed)
	e := wantErr(t, svc.AcceptInvitation(ctx, AcceptInvitationInput{Token: validToken}, LoginInput{}), 422, problem.CodeValidationFailed)
	if !hasField(e, "token", CodeInvitationInvalid) {
		t.Fatalf("fields = %+v", e.Fields)
	}

	st.inv = &base
	e = wantErr(t, svc.AcceptInvitation(ctx, AcceptInvitationInput{Token: validToken}, LoginInput{}), 422, problem.CodeValidationFailed)
	if !hasField(e, "password", "REQUIRED") {
		t.Fatalf("fields = %+v", e.Fields)
	}
	e = wantErr(t, svc.AcceptInvitation(ctx, AcceptInvitationInput{Token: validToken, Password: pw("password1234")}, LoginInput{}), 422, problem.CodeValidationFailed)
	if !hasField(e, "password", domain.PwCommon) {
		t.Fatalf("fields = %+v", e.Fields)
	}
	if err := svc.AcceptInvitation(ctx, AcceptInvitationInput{Token: validToken, Password: pw("una-clave-bastante-larga"), DisplayName: pw(" Ana Pérez ")},
		LoginInput{IP: "192.0.2.1"}); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := domain.VerifyPassword("una-clave-bastante-larga", st.accepted.PasswordHash, svc.o.Argon); !ok || st.accepted.DisplayName != "Ana Pérez" {
		t.Fatalf("aceptación = %+v", st.accepted)
	}
	if ev := st.accepted.Event(domain.Member{MembershipID: base.MembershipID, Status: domain.MembershipActive, Version: 2}, nil); ev.Type != "horus.auth.membership.updated" {
		t.Fatalf("evento = %s", ev.Type)
	}

	// Usuario que ya existía: la invitación no cambia su contraseña ni su nombre.
	active := base
	active.UserStatus = domain.UserActive
	st.inv = &active
	if err := svc.AcceptInvitation(ctx, AcceptInvitationInput{Token: validToken, Password: pw("otra-clave-que-no-se-usa"), DisplayName: pw("X")}, LoginInput{}); err != nil {
		t.Fatal(err)
	}
	if st.accepted.PasswordHash != "" || st.accepted.DisplayName != "" {
		t.Fatal("la invitación cambió la contraseña de una cuenta existente")
	}

	// Usada, caducada, membresía ya no invitada, usuario deshabilitado o carrera perdida: misma respuesta.
	for name, mut := range map[string]func(i *domain.Invitation){
		"usada":         func(i *domain.Invitation) { u := now; i.UsedAt = &u },
		"caducada":      func(i *domain.Invitation) { i.ExpiresAt = now.Add(-time.Second) },
		"revocada":      func(i *domain.Invitation) { i.MembershipStatus = domain.MembershipRevoked },
		"deshabilitado": func(i *domain.Invitation) { i.UserStatus = "disabled" },
	} {
		bad := active
		mut(&bad)
		st.inv = &bad
		e := wantErr(t, svc.AcceptInvitation(ctx, AcceptInvitationInput{Token: validToken}, LoginInput{}), 422, problem.CodeValidationFailed)
		if !hasField(e, "token", CodeInvitationInvalid) {
			t.Fatalf("%s: fields = %+v", name, e.Fields)
		}
	}
	st.inv, st.acceptEr = &active, ErrInvitationUsed
	wantErr(t, svc.AcceptInvitation(ctx, AcceptInvitationInput{Token: validToken}, LoginInput{}), 422, problem.CodeValidationFailed)
}

func TestReplaceAssignmentsErrors(t *testing.T) {
	svc, st, _, cat := newMemberEnv(t)
	tenant := uuid.New()
	ctx := tenantCtx(tenant, allPerms(cat, "tenant_admin"))
	viewer, _ := cat.TenantRole("viewer")
	in := json.RawMessage(`[{"role_id":"` + viewer.ID.String() + `","scope":"tenant"}]`)
	cur := &domain.Member{MembershipID: uuid.New(), UserID: uuid.New(), Status: domain.MembershipActive, Version: 3}

	// Miembro inexistente o revocado: 404 aunque el cuerpo no sea válido.
	_, err := svc.ReplaceAssignments(ctx, uuid.New(), 1, json.RawMessage(`{}`), "")
	wantErr(t, err, 404, problem.CodeNotFound)
	st.member = &domain.Member{UserID: cur.UserID, Status: domain.MembershipRevoked}
	_, err = svc.ReplaceAssignments(ctx, cur.UserID, 1, in, "")
	wantErr(t, err, 404, problem.CodeNotFound)

	st.member = cur
	_, err = svc.ReplaceAssignments(ctx, cur.UserID, 3, json.RawMessage(`{"role_id":"x"}`), "")
	if e := wantErr(t, err, 422, problem.CodeValidationFailed); !hasField(e, "body", "INVALID_TYPE") {
		t.Fatalf("fields = %+v", e.Fields)
	}
	_, err = svc.ReplaceAssignments(ctx, cur.UserID, 3, json.RawMessage(`[{"role_id":"`+uuid.NewString()+`","scope":"tenant"}]`), "")
	if e := wantErr(t, err, 422, problem.CodeValidationFailed); !hasField(e, "[0].role_id", CodeUnknownRole) {
		t.Fatalf("fields = %+v", e.Fields)
	}

	st.replace = func(int, []domain.Assignment) (*domain.Member, error) { return nil, domain.ErrNotFound }
	_, err = svc.ReplaceAssignments(ctx, cur.UserID, 3, in, "")
	wantErr(t, err, 404, problem.CodeNotFound)

	st.replace = func(int, []domain.Assignment) (*domain.Member, error) { return nil, &MemberVersionError{Current: cur} }
	_, err = svc.ReplaceAssignments(ctx, cur.UserID, 1, in, "")
	if e := wantErr(t, err, 412, problem.CodePreconditionFailed); e.Current != cur {
		t.Fatal("412 sin la representación actual")
	}

	st.replace = func(expect int, as []domain.Assignment) (*domain.Member, error) {
		return &domain.Member{MembershipID: cur.MembershipID, UserID: cur.UserID, Version: expect + 1, Assignments: as}, nil
	}
	m, err := svc.ReplaceAssignments(ctx, cur.UserID, 3, in, "")
	if err != nil || m.Version != 4 || len(m.Assignments) != 1 || m.Assignments[0].RoleKey != "viewer" {
		t.Fatalf("m = %+v, %v", m, err)
	}
	if len(st.audits) == 0 || st.audits[len(st.audits)-1].Action != "members.role_assignments.replaced" || st.audits[len(st.audits)-1].TenantID != tenant {
		t.Fatalf("auditoría = %+v", st.audits)
	}
	// Vaciar las asignaciones es válido (array vacío).
	if _, err := svc.ReplaceAssignments(ctx, cur.UserID, 3, json.RawMessage(`[]`), ""); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveMember(t *testing.T) {
	svc, st, _, cat := newMemberEnv(t)
	ctx := tenantCtx(uuid.New(), allPerms(cat, "tenant_admin"))
	wantErr(t, svc.RemoveMember(ctx, uuid.New(), ""), 404, problem.CodeNotFound)
	st.revoked = true
	if err := svc.RemoveMember(ctx, uuid.New(), ""); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateMe(t *testing.T) {
	svc, st, _, _ := newMemberEnv(t)
	now := time.Now()
	uid, sid, tenant := uuid.New(), uuid.New(), uuid.New()
	st.user = &domain.User{ID: uid, Email: "yo@isp.test", DisplayName: "Yo", Status: domain.UserActive}
	st.session = &domain.Session{ID: sid, UserID: uid, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	st.ms = []domain.Membership{{Tenant: domain.Tenant{ID: tenant}}}
	p := &authz.Principal{Type: authz.TypeUser, Subject: uid.String(), UserID: uid, SessionID: sid, Scope: authz.ScopeSession}
	ctx := context.Background()
	patch := func(s string) map[string]json.RawMessage {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	_, err := svc.UpdateMe(ctx, p, patch(`{"email":"x@y.z","timezone":"Mars/Base","locale":"???","display_name":"","default_tenant_id":"`+uuid.NewString()+`"}`), "")
	e := wantErr(t, err, 422, problem.CodeValidationFailed)
	for f, code := range map[string]string{"email": "UNKNOWN_FIELD", "timezone": "INVALID_TIMEZONE", "locale": "INVALID_FORMAT",
		"display_name": "INVALID_VALUE", "default_tenant_id": CodeNotAMember} {
		if !hasField(e, f, code) {
			t.Fatalf("falta %s %s: %+v", f, code, e.Fields)
		}
	}
	if st.profile != nil {
		t.Fatal("se guardó un perfil inválido")
	}
	v, err := svc.UpdateMe(ctx, p, patch(`{"display_name":" Nuevo ","locale":"es-MX","timezone":"America/Mexico_City","default_tenant_id":"`+tenant.String()+`"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	if st.profile == nil || st.profile.DisplayName != "Nuevo" || *st.profile.Locale != "es-MX" || *st.profile.Timezone != "America/Mexico_City" ||
		*st.profile.DefaultTenantID != tenant || v == nil {
		t.Fatalf("perfil = %+v", st.profile)
	}
	st.profile = nil
	if _, err := svc.UpdateMe(ctx, p, patch(`{"locale":null,"timezone":null,"default_tenant_id":null}`), ""); err != nil {
		t.Fatal(err)
	}
	if st.profile.Locale != nil || st.profile.Timezone != nil || st.profile.DefaultTenantID != nil {
		t.Fatalf("null no borró: %+v", st.profile)
	}
}

func TestListMembersFilters(t *testing.T) {
	svc, _, _, cat := newMemberEnv(t)
	ctx := tenantCtx(uuid.New(), allPerms(cat, "viewer"))
	_, err := svc.ListMembers(ctx, pagination.NewCodec(nil), pagination.Request{Limit: 50}, "", "borrado")
	wantErr(t, err, 400, problem.CodeInvalidFilter)
	_, err = svc.ListMembers(ctx, pagination.NewCodec(nil), pagination.Request{Limit: 50, Cursor: "x.y"}, "", "")
	wantErr(t, err, 400, problem.CodeInvalidCursor)
	// Sin users.read.
	_, err = svc.ListMembers(tenantCtx(uuid.New(), allPerms(cat, "noc")), pagination.NewCodec(nil), pagination.Request{Limit: 50}, "", "")
	wantErr(t, err, 403, problem.CodePermissionDenied)
}
