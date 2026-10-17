package app

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

type fakeTokenStore struct {
	mu      sync.Mutex
	tokens  []domain.APIToken
	user    domain.User
	ms      []domain.Membership
	lookups int
	touches int
}

func (f *fakeTokenStore) CreateAPIToken(_ context.Context, t *domain.APIToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = append(f.tokens, *t)
	return nil
}

func (f *fakeTokenStore) ListAPITokens(_ context.Context, tenant uuid.UUID) ([]domain.APIToken, error) {
	var out []domain.APIToken
	for _, t := range f.tokens {
		if t.TenantID == tenant {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *fakeTokenStore) RevokeAPIToken(_ context.Context, tenant, id, by uuid.UUID, now time.Time) (*domain.APIToken, error) {
	for i := range f.tokens {
		t := &f.tokens[i]
		if t.ID == id && t.TenantID == tenant && t.RevokedAt == nil {
			t.RevokedAt, t.RevokedBy = &now, &by
			c := *t
			return &c, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *fakeTokenStore) APITokenByHash(_ context.Context, hash []byte) (*domain.APIToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups++
	for _, t := range f.tokens {
		if bytes.Equal(t.Hash, hash) {
			c := t
			return &c, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *fakeTokenStore) TouchAPIToken(_ context.Context, id uuid.UUID, now time.Time, ip string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.touches++
	for i := range f.tokens {
		if f.tokens[i].ID == id {
			f.tokens[i].LastUsedAt, f.tokens[i].LastUsedIP = &now, &ip
		}
	}
	return nil
}

func (f *fakeTokenStore) UserByID(_ context.Context, id uuid.UUID) (*domain.User, error) {
	if id != f.user.ID {
		return nil, domain.ErrNotFound
	}
	u := f.user
	return &u, nil
}

func (f *fakeTokenStore) Memberships(context.Context, uuid.UUID) ([]domain.Membership, error) {
	return f.ms, nil
}

type tokenFixture struct {
	svc    *APITokens
	store  *fakeTokenStore
	ver    *authz.Verifier
	tenant uuid.UUID
	user   uuid.UUID
	now    *time.Time
	ctx    context.Context
}

func newTokenFixture(t *testing.T) *tokenFixture {
	t.Helper()
	cat, err := domain.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	key, _ := authz.GenerateKey()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	f := &tokenFixture{tenant: uuid.New(), user: uuid.New(), now: &now}
	site := uuid.New()
	f.store = &fakeTokenStore{user: domain.User{ID: f.user, Status: domain.UserActive}}
	f.store.ms = []domain.Membership{{Status: domain.MembershipActive, Tenant: domain.Tenant{ID: f.tenant, Status: domain.TenantActive},
		Assignments: []domain.Assignment{
			{ScopeType: "tenant", Permissions: []string{"customers.read", "snmp.read", "security.findings.read", "traffic.customer.read", "api_tokens.manage"}},
			{ScopeType: "site", ScopeID: &site, Permissions: []string{"devices.read"}},
		}}}
	signer := authz.NewSigner(key, "horus-auth", APITokenTTL(), func() time.Time { return *f.now })
	keys := authz.KeySet{}
	keys.Add(signer.PublicKey())
	f.ver = authz.NewVerifier(keys, "horus-auth", func() time.Time { return *f.now })
	f.svc = NewAPITokens(f.store, signer, cat, nil, func() time.Time { return *f.now }, nil)
	_ = clock
	p := &authz.Principal{Type: authz.TypeUser, Subject: f.user.String(), UserID: f.user, Scope: authz.ScopeTenant, TenantID: f.tenant,
		Perms: map[string][]string{"customers.read": {"*"}, "snmp.read": {"*"}, "security.findings.read": {"*"},
			"traffic.customer.read": {"*"}, "api_tokens.manage": {"*"}, "devices.read": {"site:" + site.String()}}}
	f.ctx = authz.WithPrincipal(context.Background(), p)
	return f
}

func TestAPITokenCreateValidation(t *testing.T) {
	t.Parallel()
	f := newTokenFixture(t)
	tpl := "argosisp"
	res, err := f.svc.Create(f.ctx, APITokenInput{Name: "ArgosISP", Template: &tpl})
	if err != nil {
		t.Fatal(err)
	}
	if !domain.LooksLikeAPIToken(res.Raw) || len(res.Token.Permissions) != 4 || *res.Token.Template != "argosisp" {
		t.Fatalf("token = %+v", res.Token)
	}
	if !res.Token.ExpiresAt.Equal(f.now.Add(domain.APITokenDefaultTTL)) {
		t.Fatalf("caducidad por defecto = %v", res.Token.ExpiresAt)
	}
	if !bytes.Equal(res.Token.Hash, domain.HashAPIToken(res.Raw)) {
		t.Fatal("solo se guarda el SHA-256")
	}
	tooLong := f.now.Add(400 * 24 * time.Hour)
	for name, in := range map[string]APITokenInput{
		"sin permisos":       {Name: "x"},
		"permiso ajeno":      {Name: "x", Permissions: []string{"alerts.manage"}},
		"meta-permiso":       {Name: "x", Permissions: []string{"api_tokens.manage"}},
		"desconocido":        {Name: "x", Permissions: []string{"no.existe"}},
		"de plataforma":      {Name: "x", Permissions: []string{"platform.tenants.read"}},
		"caducidad excesiva": {Name: "x", Permissions: []string{"snmp.read"}, ExpiresAt: &tooLong},
		"sin nombre":         {Name: " ", Permissions: []string{"snmp.read"}},
	} {
		if _, err := f.svc.Create(f.ctx, in); err == nil {
			t.Errorf("%s: debía fallar", name)
		} else if e, ok := apperr.As(err); !ok || e.Kind != apperr.KindInvalid {
			t.Errorf("%s: error %v", name, err)
		}
	}
	// Un token (no una persona) no crea tokens.
	tp := &authz.Principal{Type: authz.TypeService, Subject: f.user.String(), Scope: authz.ScopeTenant, TenantID: f.tenant,
		Perms: map[string][]string{"api_tokens.manage": {"*"}, "snmp.read": {"*"}}, Via: authz.ViaAPIToken, APITokenID: uuid.New()}
	if _, err := f.svc.Create(authz.WithPrincipal(context.Background(), tp), APITokenInput{Name: "x", Permissions: []string{"snmp.read"}}); err == nil {
		t.Fatal("un API token no debe crear API tokens")
	}
}

func TestAPITokenExchange(t *testing.T) {
	t.Parallel()
	f := newTokenFixture(t)
	res, err := f.svc.Create(f.ctx, APITokenInput{Name: "integración", Permissions: []string{"customers.read", "snmp.read", "devices.read"}})
	if err != nil {
		t.Fatal(err)
	}
	jwt, err := f.svc.ExchangeAPIToken(context.Background(), res.Raw, "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.ver.Verify(jwt)
	if err != nil {
		t.Fatal(err)
	}
	if !p.IsAPIToken() || p.APITokenID != res.Token.ID || p.TenantID != f.tenant || p.Type != authz.TypeService || p.Subject != f.user.String() {
		t.Fatalf("principal = %+v", p)
	}
	if !p.TenantWide("customers.read") || p.Has("traffic.customer.read") || p.TenantWide("devices.read") || !p.Has("devices.read") {
		t.Fatalf("permisos = %v (intersección con alcances del creador)", p.Perms)
	}
	if !p.ExpiresAt.Equal(f.now.Add(2 * time.Minute)) {
		t.Fatalf("vida = %v", p.ExpiresAt.Sub(*f.now))
	}
	if f.store.touches != 1 || *f.store.tokens[0].LastUsedIP != "203.0.113.7" {
		t.Fatalf("último uso no registrado: %d", f.store.touches)
	}
	// Caché: un segundo canje no consulta la base.
	if _, err := f.svc.ExchangeAPIToken(context.Background(), res.Raw, "203.0.113.7"); err != nil || f.store.lookups != 1 {
		t.Fatalf("caché: lookups=%d err=%v", f.store.lookups, err)
	}
	// Pasado el margen de la caché se vuelve a canjear (y a intersectar).
	*f.now = f.now.Add(100 * time.Second)
	f.store.ms[0].Assignments[0].Permissions = []string{"snmp.read"} // al creador le quitan customers.read
	jwt, err = f.svc.ExchangeAPIToken(context.Background(), res.Raw, "203.0.113.7")
	if err != nil || f.store.lookups != 2 {
		t.Fatalf("recanje: lookups=%d err=%v", f.store.lookups, err)
	}
	p, _ = f.ver.Verify(jwt)
	if p.Has("customers.read") || !p.Has("snmp.read") {
		t.Fatalf("permisos tras quitarle customers.read al creador = %v", p.Perms)
	}
	// Revocar invalida de inmediato aunque esté en caché.
	if err := f.svc.Revoke(f.ctx, res.Token.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ExchangeAPIToken(context.Background(), res.Raw, ""); !errors.Is(err, api.ErrAPITokenInvalid) {
		t.Fatalf("revocado: %v", err)
	}
	if err := f.svc.Revoke(f.ctx, res.Token.ID); err == nil {
		t.Fatal("revocar dos veces → 404")
	}
}

func TestAPITokenExchangeDenied(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(f *tokenFixture){
		"caducado":           func(f *tokenFixture) { *f.now = f.now.Add(domain.APITokenMaxTTL) },
		"usuario bloqueado":  func(f *tokenFixture) { f.store.user.Status = "disabled" },
		"membresía revocada": func(f *tokenFixture) { f.store.ms[0].Status = "revoked" },
		"ISP suspendido":     func(f *tokenFixture) { f.store.ms[0].Tenant.Status = "suspended" },
		"sin permisos en común": func(f *tokenFixture) {
			f.store.ms[0].Assignments = f.store.ms[0].Assignments[1:]
		},
		"otro ISP": func(f *tokenFixture) { f.store.ms[0].Tenant.ID = uuid.New() },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newTokenFixture(t)
			res, err := f.svc.Create(f.ctx, APITokenInput{Name: "x", Permissions: []string{"customers.read"}})
			if err != nil {
				t.Fatal(err)
			}
			mutate(f)
			if _, err := f.svc.ExchangeAPIToken(context.Background(), res.Raw, ""); !errors.Is(err, api.ErrAPITokenInvalid) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	f := newTokenFixture(t)
	if _, err := f.svc.ExchangeAPIToken(context.Background(), "hf_pat_corto", ""); !errors.Is(err, api.ErrAPITokenInvalid) || f.store.lookups != 0 {
		t.Fatalf("formato inválido sin tocar la base: %v (%d)", err, f.store.lookups)
	}
}
