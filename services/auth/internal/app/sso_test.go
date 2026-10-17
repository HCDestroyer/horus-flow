package app

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// ssoMemStore es un Store + SSOStore en memoria con lo que usa el autologin
// (el resto del interfaz Store queda sin implementar).
type ssoMemStore struct {
	Store
	mu       sync.Mutex
	users    map[uuid.UUID]*domain.User
	ms       map[uuid.UUID][]domain.Membership
	tenants  map[uuid.UUID]*domain.Tenant
	totp     map[uuid.UUID]*domain.TOTP
	sessions []domain.Session
	refresh  int
	audit    []api.AuditEntry
	tickets  map[uuid.UUID]*domain.SSOTicket
	links    map[uuid.UUID]*domain.SSOLink
}

func (m *ssoMemStore) UserByID(_ context.Context, id uuid.UUID) (*domain.User, error) {
	if u, ok := m.users[id]; ok {
		c := *u
		return &c, nil
	}
	return nil, domain.ErrNotFound
}

func (m *ssoMemStore) Memberships(_ context.Context, id uuid.UUID) ([]domain.Membership, error) {
	return m.ms[id], nil
}

func (m *ssoMemStore) Tenant(_ context.Context, id uuid.UUID) (*domain.Tenant, error) {
	if t, ok := m.tenants[id]; ok {
		c := *t
		return &c, nil
	}
	return nil, domain.ErrNotFound
}

func (m *ssoMemStore) TOTP(_ context.Context, id uuid.UUID) (*domain.TOTP, error) {
	if t, ok := m.totp[id]; ok {
		return t, nil
	}
	return nil, domain.ErrNotFound
}

func (m *ssoMemStore) CreateSession(_ context.Context, s *domain.Session, rt *domain.RefreshToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions = append(m.sessions, *s)
	if rt != nil {
		m.refresh++
	}
	return nil
}

func (m *ssoMemStore) InsertAudit(_ context.Context, e api.AuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audit = append(m.audit, e)
	return nil
}

func (m *ssoMemStore) CreateSSOTicket(_ context.Context, t *domain.SSOTicket) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := *t
	m.tickets[t.ID] = &c
	return nil
}

func (m *ssoMemStore) RedeemSSOTicket(_ context.Context, hash []byte, now time.Time, _ string) (*domain.SSOTicket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tickets {
		if bytes.Equal(t.Hash, hash) && t.RedeemedAt == nil && now.Before(t.ExpiresAt) {
			at := now
			t.RedeemedAt = &at
			c := *t
			return &c, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (m *ssoMemStore) SSOTicketByID(_ context.Context, id uuid.UUID) (*domain.SSOTicket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tickets[id]; ok {
		c := *t
		return &c, nil
	}
	return nil, domain.ErrNotFound
}

func (m *ssoMemStore) ConsumeSSOLinkToken(_ context.Context, _, id uuid.UUID, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tickets[id]
	if !ok || t.RedeemedAt == nil || t.LinkConsumedAt != nil {
		return false, nil
	}
	t.LinkConsumedAt = &now
	return true, nil
}

func (m *ssoMemStore) PurgeSSOTickets(context.Context, time.Time) (int, error) { return 0, nil }

func (m *ssoMemStore) SSOLinkByExternal(_ context.Context, tenant, connector uuid.UUID, ext string) (*domain.SSOLink, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, l := range m.links {
		if l.TenantID == tenant && l.ConnectorID == connector && l.ExternalUserID == ext {
			c := *l
			return &c, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (m *ssoMemStore) CreateSSOLink(_ context.Context, l *domain.SSOLink) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.links {
		if x.TenantID == l.TenantID && x.ConnectorID == l.ConnectorID && x.ExternalUserID == l.ExternalUserID {
			return ErrSSOLinkTaken
		}
	}
	c := *l
	m.links[l.ID] = &c
	return nil
}

func (m *ssoMemStore) TouchSSOLink(context.Context, uuid.UUID, uuid.UUID, time.Time) error { return nil }

func (m *ssoMemStore) ListSSOLinksByUser(_ context.Context, user uuid.UUID) ([]domain.SSOLink, error) {
	var out []domain.SSOLink
	for _, l := range m.links {
		if l.UserID == user {
			out = append(out, *l)
		}
	}
	return out, nil
}

func (m *ssoMemStore) ListSSOLinks(_ context.Context, tenant uuid.UUID) ([]domain.SSOLink, error) {
	var out []domain.SSOLink
	for _, l := range m.links {
		if l.TenantID == tenant {
			out = append(out, *l)
		}
	}
	return out, nil
}

func (m *ssoMemStore) DeleteSSOLink(_ context.Context, tenant, id uuid.UUID, user *uuid.UUID) (*domain.SSOLink, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.links[id]
	if !ok || (tenant != uuid.Nil && l.TenantID != tenant) || (user != nil && l.UserID != *user) {
		return nil, domain.ErrNotFound
	}
	delete(m.links, id)
	return l, nil
}

func (m *ssoMemStore) DeleteSSOLinks(_ context.Context, tenant uuid.UUID) (int, error) {
	n := 0
	for id, l := range m.links {
		if l.TenantID == tenant {
			delete(m.links, id)
			n++
		}
	}
	return n, nil
}

type fakeConnectors struct{ active bool }

func (f *fakeConnectors) SSOConnectorActive(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return f.active, nil
}

type ssoEnv struct {
	sso       *SSO
	st        *ssoMemStore
	conns     *fakeConnectors
	now       *time.Time
	tenant    uuid.UUID
	connector uuid.UUID
	user      *domain.User // miembro del ISP, sin 2FA
	outsider  *domain.User // usuario de otro ISP, mismo correo que el externo
}

func newSSOEnv(t *testing.T) *ssoEnv {
	t.Helper()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	e := &ssoEnv{now: &now, tenant: uuid.New(), connector: uuid.New(), conns: &fakeConnectors{active: true}}
	e.user = &domain.User{ID: uuid.New(), Email: "ana@isp.test", DisplayName: "Ana", Status: domain.UserActive}
	e.outsider = &domain.User{ID: uuid.New(), Email: "ana@isp.gt", DisplayName: "Ana (otro ISP)", Status: domain.UserActive}
	tenant := &domain.Tenant{ID: e.tenant, Slug: "fibra-norte", Name: "Fibra Norte", Status: domain.TenantActive}
	other := &domain.Tenant{ID: uuid.New(), Slug: "otro", Name: "Otro", Status: domain.TenantActive}
	e.st = &ssoMemStore{users: map[uuid.UUID]*domain.User{e.user.ID: e.user, e.outsider.ID: e.outsider},
		ms: map[uuid.UUID][]domain.Membership{
			e.user.ID:     {{Status: domain.MembershipActive, Tenant: *tenant}},
			e.outsider.ID: {{Status: domain.MembershipActive, Tenant: *other}},
		},
		tenants: map[uuid.UUID]*domain.Tenant{tenant.ID: tenant, other.ID: other}, totp: map[uuid.UUID]*domain.TOTP{},
		tickets: map[uuid.UUID]*domain.SSOTicket{}, links: map[uuid.UUID]*domain.SSOLink{}}
	key, _ := authz.GenerateKey()
	sealer, err := domain.NewSealer(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(e.st, Options{Signer: authz.NewSigner(key, "horus-auth", time.Minute, func() time.Time { return *e.now }),
		Sealer: sealer, SessionMax: 12 * time.Hour, RefreshIdle: time.Hour, Now: func() time.Time { return *e.now },
		Argon: domain.Argon2Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}})
	if err != nil {
		t.Fatal(err)
	}
	e.sso = NewSSO(svc, e.st, func() (api.SSOConnectors, bool) { return e.conns, true })
	return e
}

func (e *ssoEnv) issue(t *testing.T, ext string) string {
	t.Helper()
	mail := "ana@isp.gt"
	res, err := e.sso.IssueSSOTicket(context.Background(), api.SSOTicketRequest{TenantID: e.tenant, ConnectorID: e.connector,
		APITokenID: uuid.New(), ExternalUserID: ext, ExternalEmail: &mail, TargetType: "servicio", TargetID: "812"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.ExpiresAt.Equal(e.now.Add(60 * time.Second)) {
		t.Fatalf("caducidad = %s", res.ExpiresAt)
	}
	return res.Ticket
}

func codeOf(err error) string {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

func sessionPrincipal(u *domain.User, authTime time.Time) *authz.Principal {
	return &authz.Principal{Type: authz.TypeUser, Subject: u.ID.String(), UserID: u.ID, SessionID: uuid.New(), Scope: authz.ScopeSession,
		AuthTime: authTime}
}

func TestSSOTicketHashOnlySingleUseAndExpiry(t *testing.T) {
	t.Parallel()
	e := newSSOEnv(t)
	raw := e.issue(t, "u-17")
	for _, tk := range e.st.tickets {
		if bytes.Contains(tk.Hash, []byte(raw)) || !bytes.Equal(tk.Hash, domain.HashSSOTicket(raw)) || len(tk.Hash) != 32 {
			t.Fatal("solo se guarda el SHA-256 del ticket")
		}
	}
	if _, err := e.sso.Redeem(context.Background(), raw, LoginInput{}); err != nil {
		t.Fatal(err)
	}
	// Repetir el canje (replay) → 410.
	if _, err := e.sso.Redeem(context.Background(), raw, LoginInput{}); codeOf(err) != CodeSSOTicketInvalid {
		t.Fatalf("segundo canje: %v", err)
	}
	// Caducado a los 60 s.
	late := e.issue(t, "u-17")
	*e.now = e.now.Add(61 * time.Second)
	if _, err := e.sso.Redeem(context.Background(), late, LoginInput{}); codeOf(err) != CodeSSOTicketInvalid {
		t.Fatalf("ticket caducado: %v", err)
	}
	// Forma inválida: sin tocar la base.
	if _, err := e.sso.Redeem(context.Background(), "corto", LoginInput{}); codeOf(err) != CodeSSOTicketInvalid {
		t.Fatalf("ticket mal formado: %v", err)
	}
}

func TestSSOConnectorInactiveRejectsTicket(t *testing.T) {
	t.Parallel()
	e := newSSOEnv(t)
	raw := e.issue(t, "u-17")
	e.conns.active = false
	if _, err := e.sso.Redeem(context.Background(), raw, LoginInput{}); codeOf(err) != CodeSSOTicketInvalid {
		t.Fatalf("conector apagado: %v", err)
	}
	// Sin el dueño del conector en el proceso: fail-closed.
	e2 := newSSOEnv(t)
	e2.sso.connectors = func() (api.SSOConnectors, bool) { return nil, false }
	if _, err := e2.sso.Redeem(context.Background(), e2.issue(t, "u-1"), LoginInput{}); codeOf(err) != CodeSSOTicketInvalid {
		t.Fatalf("sin conector local: %v", err)
	}
}

func TestSSOFirstUseRequiresLoginNeverAutoCreatesNorLinksByEmail(t *testing.T) {
	t.Parallel()
	e := newSSOEnv(t)
	users := len(e.st.users)
	// El correo externo coincide con el de un usuario de Horus (outsider): no se vincula ni se entra.
	res, err := e.sso.Redeem(context.Background(), e.issue(t, "u-17"), LoginInput{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "link_required" || res.Tokens != nil || res.LinkToken == "" || len(e.st.sessions) != 0 {
		t.Fatalf("primer uso = %+v", res)
	}
	if len(e.st.users) != users || len(e.st.links) != 0 {
		t.Fatal("el canje no crea usuarios ni vinculaciones")
	}
	if res.Target.TenantSlug != "fibra-norte" || res.Target.Type != "servicio" || res.Target.ExternalID != "812" {
		t.Fatalf("destino = %+v", res.Target)
	}
	// Una sesión abierta ANTES del canje no basta: hay que autenticarse otra vez.
	old := sessionPrincipal(e.user, e.now.Add(-time.Hour))
	if _, _, err := e.sso.Link(context.Background(), old, res.LinkToken, LoginInput{}); codeOf(err) != problem.CodeReauthRequired {
		t.Fatalf("vincular con una sesión vieja: %v", err)
	}
	// Un usuario de otro ISP (aunque su correo coincida) no puede vincular.
	if _, _, err := e.sso.Link(context.Background(), sessionPrincipal(e.outsider, *e.now), res.LinkToken, LoginInput{}); codeOf(err) != CodeSSONotMember {
		t.Fatalf("usuario de otro ISP: %v", err)
	}
	// El acceso de soporte de plataforma no vincula.
	via := sessionPrincipal(e.user, *e.now)
	via.ViaPlatform = true
	if _, _, err := e.sso.Link(context.Background(), via, res.LinkToken, LoginInput{}); err == nil {
		t.Fatal("via_platform no vincula")
	}
	// Login reciente del miembro: vincula una sola vez.
	l, target, err := e.sso.Link(context.Background(), sessionPrincipal(e.user, *e.now), res.LinkToken, LoginInput{})
	if err != nil {
		t.Fatal(err)
	}
	if l.UserID != e.user.ID || l.ExternalUserID != "u-17" || target.ExternalID != "812" {
		t.Fatalf("vinculación = %+v", l)
	}
	if _, _, err := e.sso.Link(context.Background(), sessionPrincipal(e.user, *e.now), res.LinkToken, LoginInput{}); codeOf(err) != CodeSSOLinkInvalid {
		t.Fatalf("link_token reutilizado: %v", err)
	}
	// Un link_token manipulado no vale.
	if _, _, err := e.sso.Link(context.Background(), sessionPrincipal(e.user, *e.now), res.LinkToken+"x", LoginInput{}); codeOf(err) != CodeSSOLinkInvalid {
		t.Fatalf("link_token manipulado: %v", err)
	}
	if !slices.ContainsFunc(e.st.audit, func(a api.AuditEntry) bool { return a.Action == "auth.sso.linked" && a.TenantID == e.tenant }) {
		t.Fatal("vinculación sin auditar")
	}
}

func TestSSOLinkedUserGetsOwnSessionAndMFA(t *testing.T) {
	t.Parallel()
	e := newSSOEnv(t)
	res, _ := e.sso.Redeem(context.Background(), e.issue(t, "u-17"), LoginInput{})
	if _, _, err := e.sso.Link(context.Background(), sessionPrincipal(e.user, *e.now), res.LinkToken, LoginInput{}); err != nil {
		t.Fatal(err)
	}
	// Con vinculación y sin 2FA: sesión normal del usuario local (refresh incluido).
	got, err := e.sso.Redeem(context.Background(), e.issue(t, "u-17"), LoginInput{IP: "198.51.100.7"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "authenticated" || got.Tokens == nil || got.Tokens.Refresh == "" || got.Tokens.Scope != authz.ScopeSession {
		t.Fatalf("canje vinculado = %+v", got)
	}
	last := e.st.sessions[len(e.st.sessions)-1]
	if last.UserID != e.user.ID || !slices.Contains(last.AMR, domain.AMRSSO) || last.ExpiresAt.Sub(last.CreatedAt) != 12*time.Hour {
		t.Fatalf("sesión = %+v", last)
	}
	// Con 2FA confirmado: sesión pendiente y mfa_token (la política de Horus se aplica igual).
	confirmed := *e.now
	e.st.totp[e.user.ID] = &domain.TOTP{UserID: e.user.ID, ConfirmedAt: &confirmed}
	mfa, err := e.sso.Redeem(context.Background(), e.issue(t, "u-17"), LoginInput{})
	if err != nil {
		t.Fatal(err)
	}
	if mfa.Status != "mfa_required" || mfa.MFAToken == "" || mfa.Tokens != nil {
		t.Fatalf("con 2FA = %+v", mfa)
	}
	// La cuenta vinculada pierde la membresía: no entra (el destino aplica su ACL).
	e.st.ms[e.user.ID] = nil
	if _, err := e.sso.Redeem(context.Background(), e.issue(t, "u-17"), LoginInput{}); codeOf(err) != CodeSSOAccountUnavailable {
		t.Fatalf("sin membresía: %v", err)
	}
	if !slices.ContainsFunc(e.st.audit, func(a api.AuditEntry) bool { return a.Action == "auth.sso.ticket_issued" }) ||
		!slices.ContainsFunc(e.st.audit, func(a api.AuditEntry) bool { return a.Action == "auth.sso.redeemed" && a.Outcome == "denied" }) {
		t.Fatal("emisión y canje sin auditar")
	}
}

func TestSSOLinksRevocableByUserAndAdmin(t *testing.T) {
	t.Parallel()
	e := newSSOEnv(t)
	for _, ext := range []string{"u-1", "u-2"} {
		res, _ := e.sso.Redeem(context.Background(), e.issue(t, ext), LoginInput{})
		if _, _, err := e.sso.Link(context.Background(), sessionPrincipal(e.user, *e.now), res.LinkToken, LoginInput{}); err != nil {
			t.Fatal(err)
		}
	}
	mine, _ := e.sso.MyLinks(context.Background(), sessionPrincipal(e.user, *e.now))
	if len(mine) != 2 {
		t.Fatalf("mis vinculaciones = %d", len(mine))
	}
	// Otro usuario no quita las mías.
	if err := e.sso.DeleteMyLink(context.Background(), sessionPrincipal(e.outsider, *e.now), mine[0].ID, LoginInput{}); codeOf(err) != CodeSSOLinkNotFound {
		t.Fatalf("quitar la de otro: %v", err)
	}
	if err := e.sso.DeleteMyLink(context.Background(), sessionPrincipal(e.user, *e.now), mine[0].ID, LoginInput{}); err != nil {
		t.Fatal(err)
	}
	admin := &authz.Principal{Type: authz.TypeUser, Subject: uuid.NewString(), UserID: uuid.New(), Scope: authz.ScopeTenant, TenantID: e.tenant,
		Perms: map[string][]string{PermUsersManage: {"*"}, PermUsersRead: {"*"}}}
	ctx := authz.WithPrincipal(context.Background(), admin)
	n, err := e.sso.RevokeAllLinks(ctx)
	if err != nil || n != 1 {
		t.Fatalf("revocar todas = %d %v", n, err)
	}
	// Sin users.manage no se revoca.
	viewer := &authz.Principal{Type: authz.TypeUser, Subject: uuid.NewString(), Scope: authz.ScopeTenant, TenantID: e.tenant,
		Perms: map[string][]string{PermUsersRead: {"*"}}}
	if _, err := e.sso.RevokeAllLinks(authz.WithPrincipal(context.Background(), viewer)); codeOf(err) != problem.CodePermissionDenied {
		t.Fatalf("sin permiso: %v", err)
	}
}
