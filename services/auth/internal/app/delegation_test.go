package app

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

type memDelegStore struct {
	Store
	user domain.User
	ms   []domain.Membership
}

func (m *memDelegStore) UserByID(_ context.Context, id uuid.UUID) (*domain.User, error) {
	if id != m.user.ID {
		return nil, domain.ErrNotFound
	}
	u := m.user
	return &u, nil
}

func (m *memDelegStore) Memberships(context.Context, uuid.UUID) ([]domain.Membership, error) {
	return m.ms, nil
}

func TestDelegatedToken(t *testing.T) {
	t.Parallel()
	key, err := authz.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer := authz.NewSigner(key, "horus-auth", 2*time.Minute, nil)
	keys := authz.KeySet{}
	keys.Add(signer.PublicKey())
	verifier := authz.NewVerifier(keys, "horus-auth", nil)
	user, tenant, site := uuid.New(), uuid.New(), uuid.New()
	st := &memDelegStore{user: domain.User{ID: user, Status: domain.UserActive}, ms: []domain.Membership{{
		Status: domain.MembershipActive, Tenant: domain.Tenant{ID: tenant, Status: domain.TenantActive},
		Assignments: []domain.Assignment{
			{ScopeType: "tenant", Permissions: []string{"security.findings.read", "security.findings.manage", "users.manage"}},
			{ScopeType: "site", ScopeID: &site, Permissions: []string{"devices.read"}},
		},
	}}}
	d := NewDelegation(st, signer)
	req := api.DelegationRequest{UserID: user, TenantID: tenant, Purpose: "assist-mcp",
		Permissions: []string{"security.findings.read", "devices.read", "flows.read"}}
	tok, err := d.DelegatedToken(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	p, err := verifier.Verify(tok)
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != authz.TypeService || p.Scope != authz.ScopeTenant || p.TenantID != tenant || p.Subject != user.String() || p.SessionID != uuid.Nil {
		t.Fatalf("principal = %+v", p)
	}
	// Solo la intersección con la lista blanca, conservando los alcances.
	if !p.TenantWide("security.findings.read") || p.Has("security.findings.manage") || p.Has("users.manage") || p.Has("flows.read") {
		t.Fatalf("perms = %v", p.Perms)
	}
	if !slices.Equal(p.Perms["devices.read"], []string{"site:" + site.String()}) {
		t.Fatalf("alcance de nodo perdido: %v", p.Perms["devices.read"])
	}
	if ttl := time.Until(p.ExpiresAt); ttl > 2*time.Minute+time.Second {
		t.Fatalf("vida = %v", ttl)
	}

	deny := func(name string, mut func(*memDelegStore), r api.DelegationRequest) {
		t.Helper()
		cp := *st
		cp.ms = slices.Clone(st.ms)
		mut(&cp)
		if _, err := NewDelegation(&cp, signer).DelegatedToken(context.Background(), r); !errors.Is(err, api.ErrDelegationDenied) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	deny("usuario bloqueado", func(s *memDelegStore) { s.user.Status = "disabled" }, req)
	deny("membresía revocada", func(s *memDelegStore) { s.ms[0].Status = "revoked" }, req)
	deny("ISP suspendido", func(s *memDelegStore) { s.ms[0].Tenant.Status = "suspended" }, req)
	deny("otro ISP", func(*memDelegStore) {}, api.DelegationRequest{UserID: user, TenantID: uuid.New(), Permissions: req.Permissions})
	deny("sin permisos en común", func(*memDelegStore) {}, api.DelegationRequest{UserID: user, TenantID: tenant, Permissions: []string{"flows.read"}})
	deny("usuario inexistente", func(*memDelegStore) {}, api.DelegationRequest{UserID: uuid.New(), TenantID: tenant, Permissions: req.Permissions})
}
