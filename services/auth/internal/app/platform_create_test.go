package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/packages/go/testkit"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// tenantStore añade el alta de ISP al repositorio simulado de miembros: lo
// que haría el repositorio con la invitación del primer administrador lo
// decide rs, y el hash guardado es el que acepta InvitationByHash.
type tenantStore struct {
	*memMemberStore
	created *NewTenant
	rs      InviteResult
	err     error
}

func (s *tenantStore) CreateTenant(_ context.Context, nt NewTenant) (*InviteResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.created = &nt
	r := s.rs
	if r.TokenStored {
		s.inv = &domain.Invitation{ID: uuid.New(), TenantID: nt.Tenant.ID, MembershipID: r.MembershipID, UserID: r.UserID,
			ExpiresAt: nt.Admin.ExpiresAt, MembershipStatus: domain.MembershipInvited, UserStatus: domain.UserPending,
			UserEmail: nt.Admin.Email, TenantName: nt.Tenant.Name}
	}
	return &r, nil
}

func (s *tenantStore) InvitationByHash(_ context.Context, hash []byte) (*domain.Invitation, error) {
	if s.inv == nil || s.created == nil || !bytes.Equal(hash, s.created.Admin.TokenHash) {
		return nil, domain.ErrNotFound
	}
	i := *s.inv
	return &i, nil
}

func newTenantEnv(t *testing.T) (*Service, *tenantStore, *captureSender, *domain.Catalog) {
	t.Helper()
	_, mem, snd, cat := newMemberEnv(t)
	st := &tenantStore{memMemberStore: mem}
	svc, err := NewService(st, Options{Catalog: cat, Invitations: snd, PublicBaseURL: "https://horus.example/",
		Argon: domain.Argon2Params{Memory: 1024, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}})
	if err != nil {
		t.Fatal(err)
	}
	return svc, st, snd, cat
}

func platformPrincipal() *authz.Principal {
	uid := uuid.New()
	return &authz.Principal{Type: authz.TypeUser, Subject: uid.String(), UserID: uid, SessionID: uuid.New(), Scope: authz.ScopePlatform}
}

func tenantInput(email string) TenantInput {
	return TenantInput{Slug: "isp-norte", Name: "ISP Norte", Country: "MX", Timezone: "America/Mexico_City", InitialAdminEmail: email}
}

// Email sin cuenta: el alta guarda en la misma operación la invitación
// (solo el hash) del tenant_admin; el enlace se devuelve, se envía por
// correo y sirve para inspeccionar y aceptar la invitación.
func TestCreateTenantInvitesNewAdmin(t *testing.T) {
	svc, st, snd, cat := newTenantEnv(t)
	p := platformPrincipal()
	st.rs = InviteResult{MembershipID: uuid.New(), UserID: uuid.New(), Status: domain.MembershipInvited, UserCreated: true, TokenStored: true}
	res, err := svc.CreateTenant(context.Background(), p, tenantInput("noc@isp-norte.test"), "198.51.100.7")
	if err != nil {
		t.Fatal(err)
	}
	nt := st.created
	admin, _ := cat.TenantRole(domain.RoleTenantAdmin)
	if nt.Admin.TenantID != nt.Tenant.ID || nt.Admin.Email != "noc@isp-norte.test" || nt.Admin.GrantedBy != p.UserID ||
		len(nt.Admin.Assignments) != 1 || nt.Admin.Assignments[0].RoleID != admin.ID || nt.Admin.Assignments[0].Scope() != "tenant" {
		t.Fatalf("invitación del administrador = %+v", nt.Admin)
	}
	if ttl := nt.Admin.ExpiresAt.Sub(nt.Admin.Now); ttl != domain.InvitationTTL {
		t.Fatalf("caducidad = %v", ttl)
	}
	if nt.Event.Type != "horus.auth.tenant.created" || nt.Event.TenantID == nil || *nt.Event.TenantID != nt.Tenant.ID {
		t.Fatalf("evento del ISP = %+v", nt.Event)
	}
	ev := nt.Admin.Event(domain.Member{MembershipID: st.rs.MembershipID, UserID: st.rs.UserID, Status: domain.MembershipInvited, Version: 1,
		Assignments: nt.Admin.Assignments}, nil)
	if ev.Type != "horus.auth.membership.granted" || *ev.TenantID != nt.Tenant.ID || ev.Data["status"] != domain.MembershipInvited {
		t.Fatalf("evento de membresía = %+v", ev)
	}

	inv := res.InitialAdminInvitation
	if res.Tenant.ID != nt.Tenant.ID || res.Tenant.Members != 1 || inv == nil || inv.Outcome != OutcomeInvited || inv.UserID != st.rs.UserID ||
		inv.URL == nil || inv.ExpiresAt == nil || !inv.ExpiresAt.Equal(nt.Admin.ExpiresAt) || !inv.EmailSent {
		t.Fatalf("respuesta = %+v / %+v", res.Tenant, inv)
	}
	const prefix = "https://horus.example/invitation#token="
	token := strings.TrimPrefix(*inv.URL, prefix)
	if token == *inv.URL || !bytes.Equal(domain.HashSecret(token), nt.Admin.TokenHash) {
		t.Fatalf("el enlace %q no corresponde al hash guardado", *inv.URL)
	}
	if len(snd.got) != 1 || snd.got[0].URL != *inv.URL || snd.got[0].Email != "noc@isp-norte.test" || snd.got[0].TenantID != nt.Tenant.ID {
		t.Fatalf("entregas = %+v", snd.got)
	}

	// Auditoría en plataforma y en el ISP: link_issued y email_sent, sin secretos.
	if len(st.audits) != 2 {
		t.Fatalf("auditorías = %d", len(st.audits))
	}
	for _, a := range st.audits {
		if a.Action != "platform.tenant.created" || a.Changes["link_issued"] != true || a.Changes["email_sent"] != true {
			t.Fatalf("auditoría = %+v", a)
		}
		b, _ := json.Marshal(a.Changes)
		if bytes.Contains(b, []byte(token)) || bytes.Contains(b, []byte("noc@isp-norte.test")) {
			t.Fatalf("secretos en la auditoría: %s", b)
		}
	}

	// El enlace sirve en la página de aceptación.
	info, err := svc.InspectInvitation(context.Background(), token)
	if err != nil || info.Email != "noc@isp-norte.test" || info.TenantName != "ISP Norte" || !info.NeedsPassword {
		t.Fatalf("inspect = %+v, %v", info, err)
	}
	pw := "frase-larga-segura-987"
	if err := svc.AcceptInvitation(context.Background(), AcceptInvitationInput{Token: token, Password: &pw}, LoginInput{}); err != nil {
		t.Fatal(err)
	}
	if st.accepted == nil || st.accepted.Invitation.TenantID != nt.Tenant.ID || st.accepted.PasswordHash == "" {
		t.Fatalf("aceptación = %+v", st.accepted)
	}
}

// Cuenta activa existente (p. ej. el propio superadministrador): miembro
// activo sin enlace ni correo.
func TestCreateTenantAddsExistingAdmin(t *testing.T) {
	svc, st, snd, _ := newTenantEnv(t)
	st.rs = InviteResult{MembershipID: uuid.New(), UserID: uuid.New(), Status: domain.MembershipActive}
	res, err := svc.CreateTenant(context.Background(), platformPrincipal(), tenantInput("root@horus.test"), "")
	if err != nil {
		t.Fatal(err)
	}
	inv := res.InitialAdminInvitation
	if inv.Outcome != OutcomeAdded || inv.UserID != st.rs.UserID || inv.URL != nil || inv.ExpiresAt != nil || inv.EmailSent || len(snd.got) != 0 {
		t.Fatalf("cuenta existente = %+v", inv)
	}
	if a := st.audits[0]; a.Changes["link_issued"] != false || a.Changes["email_sent"] != false || a.Changes["admin_status"] != domain.MembershipActive {
		t.Fatalf("auditoría = %+v", a.Changes)
	}
}

// Cuenta deshabilitada: como InviteMember, la membresía queda invitada pero
// no se emite enlace (la cuenta no puede aceptarla).
func TestCreateTenantDisabledAdminGetsNoLink(t *testing.T) {
	svc, st, snd, _ := newTenantEnv(t)
	st.rs = InviteResult{MembershipID: uuid.New(), UserID: uuid.New(), Status: domain.MembershipInvited}
	res, err := svc.CreateTenant(context.Background(), platformPrincipal(), tenantInput("baja@isp.test"), "")
	if err != nil {
		t.Fatal(err)
	}
	inv := res.InitialAdminInvitation
	if inv.Outcome != OutcomeInvited || inv.URL != nil || inv.ExpiresAt != nil || inv.EmailSent || len(snd.got) != 0 {
		t.Fatalf("cuenta deshabilitada = %+v", inv)
	}
}

// Un fallo del correo no falla el alta: email_sent=false, enlace devuelto y
// un aviso en el log sin la dirección, el token ni el enlace.
func TestCreateTenantEmailFailureIsNotFatal(t *testing.T) {
	svc, st, snd, _ := newTenantEnv(t)
	logger, logs := testkit.Logger(t)
	svc.o.Logger = logger
	snd.err = errors.New("550 <noc@isp-norte.test> rechazado")
	st.rs = InviteResult{MembershipID: uuid.New(), UserID: uuid.New(), Status: domain.MembershipInvited, UserCreated: true, TokenStored: true}
	res, err := svc.CreateTenant(context.Background(), platformPrincipal(), tenantInput("noc@isp-norte.test"), "")
	if err != nil {
		t.Fatal(err)
	}
	inv := res.InitialAdminInvitation
	if inv.EmailSent || inv.URL == nil || inv.Outcome != OutcomeInvited {
		t.Fatalf("res = %+v", inv)
	}
	out := logs.String()
	if !strings.Contains(out, "invitation email not sent") || strings.Contains(out, "noc@isp-norte.test") || strings.Contains(out, snd.got[0].Token) {
		t.Fatalf("log = %s", out)
	}
	if a := st.audits[0]; a.Changes["link_issued"] != true || a.Changes["email_sent"] != false {
		t.Fatalf("auditoría = %+v", a.Changes)
	}
}

func TestCreateTenantSlugTaken(t *testing.T) {
	svc, st, snd, _ := newTenantEnv(t)
	st.err = ErrSlugTaken
	_, err := svc.CreateTenant(context.Background(), platformPrincipal(), tenantInput("noc@isp-norte.test"), "")
	_ = wantErr(t, err, 409, problem.CodeAlreadyExists)
	if len(snd.got) != 0 || len(st.audits) != 0 {
		t.Fatal("un alta fallida envió correo o auditó")
	}
}
