package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/packages/go/testkit"
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
	reissued *ReissuedInvitation
	reissueE error
}

func (m *memMemberStore) Tenant(_ context.Context, id uuid.UUID) (*domain.Tenant, error) {
	return &domain.Tenant{ID: id, Name: "ISP Ejemplo"}, nil
}

func (m *memMemberStore) ReissueInvitation(_ context.Context, in ReissuedInvitation) (*ReissueResult, error) {
	if m.reissueE != nil {
		return nil, m.reissueE
	}
	m.reissued = &in
	return &ReissueResult{MembershipID: m.member.MembershipID, Email: m.member.Email, Replaced: 1}, nil
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
func (m *memMemberStore) UpdateProfile(_ context.Context, u *domain.User, _ time.Time, _ *OutboxEvent) error {
	c := *u
	m.profile = &c
	return nil
}

const validToken = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"

type captureSender struct {
	got []InvitationDelivery
	err error
}

func (c *captureSender) SendInvitation(_ context.Context, d InvitationDelivery) error {
	c.got = append(c.got, d)
	return c.err
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
	svc, err := NewService(st, Options{Catalog: cat, Invitations: snd, PublicBaseURL: "https://horus.example/",
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

	e := wantErr(t, errOf(svc.InviteMember(ctx, "no-es-email", nil, "")), 422, problem.CodeValidationFailed)
	if !hasField(e, "email", "INVALID_EMAIL") || !hasField(e, "role_assignments", "REQUIRED") {
		t.Fatalf("fields = %+v", e.Fields)
	}
	e = wantErr(t, errOf(svc.InviteMember(ctx, "a@isp.test", []AssignmentInput{{RoleID: uuid.NewString(), Scope: "tenant"}}, "")), 422, problem.CodeValidationFailed)
	if !hasField(e, "role_assignments[0].role_id", CodeUnknownRole) {
		t.Fatalf("fields = %+v", e.Fields)
	}
	e = wantErr(t, errOf(svc.InviteMember(ctx, "a@isp.test", []AssignmentInput{{RoleID: admin.ID.String(), Scope: "site:nope"}}, "")), 422, problem.CodeValidationFailed)
	if !hasField(e, "role_assignments[0].scope", "INVALID_FORMAT") {
		t.Fatalf("fields = %+v", e.Fields)
	}
	// Sin users.manage (defensa en profundidad tras el guard).
	_ = wantErr(t, errOf(svc.InviteMember(tenantCtx(tenant, allPerms(cat, "viewer")), "a@isp.test",
		[]AssignmentInput{{RoleID: admin.ID.String(), Scope: "tenant"}}, "")), 403, problem.CodePermissionDenied)
	// Un token de sesión (sin tenant) no sirve.
	_ = wantErr(t, errOf(svc.InviteMember(authz.WithPrincipal(context.Background(), &authz.Principal{Type: authz.TypeUser, Scope: authz.ScopeSession}),
		"a@isp.test", nil, "")), 403, problem.CodeTokenScopeInvalid)
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

	_ = wantErr(t, errOf(svc.InviteMember(ctx, "a@isp.test", []AssignmentInput{{RoleID: admin.ID.String(), Scope: "tenant"}}, "")), 403, problem.CodePermissionDenied)
	if st.invited != nil {
		t.Fatal("se invitó con un rol con más permisos que el invitador")
	}
	if err := errOf(svc.InviteMember(ctx, "a@isp.test", []AssignmentInput{{RoleID: noc.ID.String(), Scope: "tenant"}, {RoleID: noc.ID.String(), Scope: "tenant"}}, "")); err != nil {
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
	_ = wantErr(t, errOf(svc.InviteMember(sctx, "b@isp.test", []AssignmentInput{{RoleID: noc.ID.String(), Scope: "tenant"}}, "")), 403, problem.CodePermissionDenied)
	if err := errOf(svc.InviteMember(sctx, "b@isp.test", []AssignmentInput{{RoleID: noc.ID.String(), Scope: "site:" + site.String()}}, "")); err != nil {
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
	res, err := svc.InviteMember(ctx, "ya@isp.test", in, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(snd.got) != 0 {
		t.Fatal("se entregó un token sin invitación guardada")
	}
	if res.Outcome != OutcomeAdded || res.UserID != st.inviteRS.UserID || res.URL != nil || res.ExpiresAt != nil || res.EmailSent {
		t.Fatalf("cuenta existente = %+v", res)
	}
	st.inviteRS = InviteResult{MembershipID: uuid.New(), UserID: uuid.New(), Status: domain.MembershipInvited, UserCreated: true, TokenStored: true}
	res, err = svc.InviteMember(ctx, "nuevo@isp.test", in, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(snd.got) != 1 {
		t.Fatalf("entregas = %d", len(snd.got))
	}
	d := snd.got[0]
	if len(d.Token) < 32 || !bytes.Equal(domain.HashSecret(d.Token), st.invited.TokenHash) || d.Email != "nuevo@isp.test" || d.TenantID != tenant ||
		d.TenantName != "ISP Ejemplo" || d.URL != "https://horus.example/invitation#token="+d.Token {
		t.Fatalf("entrega = %+v", d)
	}
	if res.Outcome != OutcomeInvited || res.URL == nil || *res.URL != d.URL || res.ExpiresAt == nil || !res.ExpiresAt.Equal(st.invited.ExpiresAt) || !res.EmailSent {
		t.Fatalf("invitada = %+v", res)
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

// Un fallo del correo no falla la invitación: 202 con email_sent=false y un
// aviso en el log sin la dirección, el token ni el enlace.
func TestInviteEmailFailureIsNotFatal(t *testing.T) {
	svc, st, snd, cat := newMemberEnv(t)
	logger, logs := testkit.Logger(t)
	svc.o.Logger = logger
	snd.err = errors.New("550 <nuevo@isp.test> rechazado")
	ctx := tenantCtx(uuid.New(), allPerms(cat, "tenant_admin"))
	viewer, _ := cat.TenantRole("viewer")
	st.inviteRS = InviteResult{MembershipID: uuid.New(), UserID: uuid.New(), Status: domain.MembershipInvited, UserCreated: true, TokenStored: true}
	res, err := svc.InviteMember(ctx, "nuevo@isp.test", []AssignmentInput{{RoleID: viewer.ID.String(), Scope: "tenant"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.EmailSent || res.URL == nil {
		t.Fatalf("res = %+v", res)
	}
	out := logs.String()
	if !strings.Contains(out, "invitation email not sent") || strings.Contains(out, "nuevo@isp.test") || strings.Contains(out, snd.got[0].Token) {
		t.Fatalf("log = %s", out)
	}
	if last := st.audits[len(st.audits)-1]; last.Changes["email_sent"] != false || last.Changes["link_issued"] != true {
		t.Fatalf("auditoría = %+v", last.Changes)
	}
	// Sin canal de correo: email_sent=false.
	svc.o.Invitations = nil
	res, err = svc.InviteMember(ctx, "otro@isp.test", []AssignmentInput{{RoleID: viewer.ID.String(), Scope: "tenant"}}, "")
	if err != nil || res.EmailSent || res.URL == nil {
		t.Fatalf("sin SMTP = %+v, %v", res, err)
	}
}

func TestReissueInvitation(t *testing.T) {
	svc, st, snd, cat := newMemberEnv(t)
	tenant := uuid.New()
	ctx := tenantCtx(tenant, allPerms(cat, "tenant_admin"))
	admin, _ := cat.TenantRole("tenant_admin")
	noc, _ := cat.TenantRole("noc")
	uid := uuid.New()

	// No es miembro de este ISP (o fue revocado): 404.
	_, err := svc.ReissueInvitation(ctx, uid, "")
	_ = wantErr(t, err, 404, problem.CodeNotFound)
	st.member = &domain.Member{MembershipID: uuid.New(), UserID: uid, Email: "inv@isp.test", Status: domain.MembershipRevoked}
	_, err = svc.ReissueInvitation(ctx, uid, "")
	_ = wantErr(t, err, 404, problem.CodeNotFound)
	// Miembro activo: 409.
	st.member.Status = domain.MembershipActive
	_, err = svc.ReissueInvitation(ctx, uid, "")
	_ = wantErr(t, err, 409, problem.CodeConflict)
	if st.reissued != nil {
		t.Fatal("se renovó la invitación de un miembro activo")
	}

	st.member.Status = domain.MembershipInvited
	st.member.Assignments = []domain.Assignment{{RoleID: admin.ID, RoleKey: admin.Key, ScopeType: "tenant", Permissions: admin.Permissions}}
	// Anti-escalada: quien no tiene los permisos de los roles del invitado no obtiene su enlace.
	perms := allPerms(cat, "noc")
	perms[PermUsersManage] = []string{authz.ScopeAll}
	_, err = svc.ReissueInvitation(tenantCtx(tenant, perms), uid, "")
	_ = wantErr(t, err, 403, problem.CodePermissionDenied)
	if st.reissued != nil {
		t.Fatal("enlace renovado con escalada")
	}
	// Sin users.manage.
	_, err = svc.ReissueInvitation(tenantCtx(tenant, allPerms(cat, "viewer")), uid, "")
	_ = wantErr(t, err, 403, problem.CodePermissionDenied)

	st.member.Assignments = []domain.Assignment{{RoleID: noc.ID, RoleKey: noc.Key, ScopeType: "tenant", Permissions: noc.Permissions}}
	res, err := svc.ReissueInvitation(ctx, uid, "198.51.100.7")
	if err != nil {
		t.Fatal(err)
	}
	if st.reissued == nil || st.reissued.TenantID != tenant || st.reissued.UserID != uid || st.reissued.ExpiresAt.Sub(st.reissued.Now) != domain.InvitationTTL {
		t.Fatalf("reissued = %+v", st.reissued)
	}
	d := snd.got[len(snd.got)-1]
	if !bytes.Equal(domain.HashSecret(d.Token), st.reissued.TokenHash) || d.Email != "inv@isp.test" || res.URL == nil || *res.URL != d.URL ||
		res.Outcome != OutcomeInvited || res.UserID != uid || !res.EmailSent {
		t.Fatalf("res = %+v, entrega = %+v", res, d)
	}
	last := st.audits[len(st.audits)-1]
	if last.Action != "members.invitation_reissued" || last.TenantID != tenant || last.ResourceID != st.member.MembershipID.String() || last.IP != "198.51.100.7" {
		t.Fatalf("auditoría = %+v", last)
	}
	if b, _ := json.Marshal(last); bytes.Contains(b, []byte(d.Token)) {
		t.Fatal("el token aparece en la auditoría")
	}
	// Carrera: la membresía cambió entre la lectura y el bloqueo.
	st.reissueE = ErrNotInvited
	_, err = svc.ReissueInvitation(ctx, uid, "")
	_ = wantErr(t, err, 409, problem.CodeConflict)
	st.reissueE = domain.ErrNotFound
	_, err = svc.ReissueInvitation(ctx, uid, "")
	_ = wantErr(t, err, 404, problem.CodeNotFound)
}

func TestInspectInvitation(t *testing.T) {
	svc, st, _, _ := newMemberEnv(t)
	now := time.Now()
	base := domain.Invitation{ID: uuid.New(), TenantID: uuid.New(), MembershipID: uuid.New(), UserID: uuid.New(),
		ExpiresAt: now.Add(time.Hour), MembershipStatus: domain.MembershipInvited, UserStatus: domain.UserPending,
		UserEmail: "nueva@isp.test", UserDisplayName: "nueva", TenantName: "ISP Norte"}
	ctx := context.Background()

	st.inv = &base
	info, err := svc.InspectInvitation(ctx, validToken)
	if err != nil {
		t.Fatal(err)
	}
	if info.Email != "nueva@isp.test" || info.TenantName != "ISP Norte" || !info.NeedsPassword || !info.ExpiresAt.Equal(base.ExpiresAt) {
		t.Fatalf("info = %+v", info)
	}
	if st.accepted != nil {
		t.Fatal("inspeccionar consumió la invitación")
	}
	active := base
	active.UserStatus = domain.UserActive
	st.inv = &active
	if info, err := svc.InspectInvitation(ctx, validToken); err != nil || info.NeedsPassword {
		t.Fatalf("cuenta existente = %+v, %v", info, err)
	}

	// Desconocida, corta, usada, caducada, revocada o de una cuenta deshabilitada: el mismo 422 que accept.
	check := func(name, token string) {
		t.Helper()
		_, err := svc.InspectInvitation(ctx, token)
		e := wantErr(t, err, 422, problem.CodeValidationFailed)
		if len(e.Fields) != 1 || !hasField(e, "token", CodeInvitationInvalid) {
			t.Fatalf("%s: fields = %+v", name, e.Fields)
		}
		aerr := svc.AcceptInvitation(ctx, AcceptInvitationInput{Token: token}, LoginInput{})
		ae := wantErr(t, aerr, 422, problem.CodeValidationFailed)
		if ae.Detail != e.Detail || len(ae.Fields) != 1 || ae.Fields[0] != e.Fields[0] {
			t.Fatalf("%s: inspect %+v != accept %+v", name, e, ae)
		}
	}
	check("desconocida", strings.Repeat("z", 43))
	check("corta", "corto")
	for name, mut := range map[string]func(i *domain.Invitation){
		"usada":         func(i *domain.Invitation) { u := now; i.UsedAt = &u },
		"caducada":      func(i *domain.Invitation) { i.ExpiresAt = now.Add(-time.Second) },
		"revocada":      func(i *domain.Invitation) { i.MembershipStatus = domain.MembershipRevoked },
		"deshabilitado": func(i *domain.Invitation) { i.UserStatus = "disabled" },
	} {
		bad := active
		mut(&bad)
		st.inv = &bad
		check(name, validToken)
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

	_ = wantErr(t, svc.AcceptInvitation(ctx, AcceptInvitationInput{Token: "corto"}, LoginInput{}), 422, problem.CodeValidationFailed)
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
	_ = wantErr(t, svc.AcceptInvitation(ctx, AcceptInvitationInput{Token: validToken}, LoginInput{}), 422, problem.CodeValidationFailed)
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
	_ = wantErr(t, err, 404, problem.CodeNotFound)
	st.member = &domain.Member{UserID: cur.UserID, Status: domain.MembershipRevoked}
	_, err = svc.ReplaceAssignments(ctx, cur.UserID, 1, in, "")
	_ = wantErr(t, err, 404, problem.CodeNotFound)

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
	_ = wantErr(t, err, 404, problem.CodeNotFound)

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
	_ = wantErr(t, svc.RemoveMember(ctx, uuid.New(), ""), 404, problem.CodeNotFound)
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

	_, err := svc.UpdateMe(ctx, p, patch(`{"email":"no-es-email","timezone":"Mars/Base","locale":"???","display_name":"","default_tenant_id":"`+uuid.NewString()+`"}`), "")
	e := wantErr(t, err, 422, problem.CodeValidationFailed)
	for f, code := range map[string]string{"email": "INVALID_EMAIL", "timezone": "INVALID_TIMEZONE", "locale": "INVALID_FORMAT",
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
	_ = wantErr(t, err, 400, problem.CodeInvalidFilter)
	_, err = svc.ListMembers(ctx, pagination.NewCodec(nil), pagination.Request{Limit: 50, Cursor: "x.y"}, "", "")
	_ = wantErr(t, err, 400, problem.CodeInvalidCursor)
	// Sin users.read.
	_, err = svc.ListMembers(tenantCtx(uuid.New(), allPerms(cat, "noc")), pagination.NewCodec(nil), pagination.Request{Limit: 50}, "", "")
	_ = wantErr(t, err, 403, problem.CodePermissionDenied)
}

func errOf(_ *InvitationIssued, err error) error { return err }
