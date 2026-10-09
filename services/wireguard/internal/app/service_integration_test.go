//go:build integration

package app_test

import (
	"context"
	"encoding/base64"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	wgapi "github.com/hcdestroyer/horus-flow/services/wgagent/api"
	"github.com/hcdestroyer/horus-flow/services/wgagent/api/agentv1"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/adapters/postgres"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/app"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/wireguard/migrations"
)

const hubKey = "HIgo9xNzJMWLKASShiTqIybxZ0U3wGLiUeJ1PKf8ykw="

// ---------------------------------------------------------------- dobles

type fakeDevices struct {
	mu      sync.Mutex
	routers map[uuid.UUID]devapi.RouterInfo
	tunnels map[uuid.UUID]devapi.TunnelUpdate
	issued  int
}

func newDevices() *fakeDevices {
	return &fakeDevices{routers: map[uuid.UUID]devapi.RouterInfo{}, tunnels: map[uuid.UUID]devapi.TunnelUpdate{}}
}

func (f *fakeDevices) add(tenant, site uuid.UUID, version string) devapi.RouterInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := version
	r := devapi.RouterInfo{ID: uuid.Must(uuid.NewV7()), TenantID: tenant, SiteID: site, Name: "rt-" + version, RouterOSVersion: &v,
		OnboardingState: devapi.OnboardingPending}
	if version == "" {
		r.RouterOSVersion = nil
	}
	f.routers[r.ID] = r
	return r
}

func (f *fakeDevices) del(id uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.routers, id)
}

func (f *fakeDevices) GetRouter(_ context.Context, tenant, id uuid.UUID) (devapi.RouterInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.routers[id]
	if !ok || r.TenantID != tenant {
		return devapi.RouterInfo{}, devapi.ErrRouterNotFound
	}
	return r, nil
}

func (f *fakeDevices) ListRouters(context.Context) ([]devapi.RouterInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []devapi.RouterInfo
	for _, r := range f.routers {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeDevices) SetTunnel(_ context.Context, tenant, id uuid.UUID, u devapi.TunnelUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.routers[id]
	if !ok || r.TenantID != tenant {
		return devapi.ErrRouterNotFound
	}
	a := u.Address
	r.TunnelAddress, r.OnboardingState = &a, u.OnboardingState
	f.routers[id] = r
	f.tunnels[id] = u
	return nil
}

func (f *fakeDevices) IssueCredentials(_ context.Context, tenant, id uuid.UUID) (devapi.OnboardingCredentials, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.routers[id]; !ok || r.TenantID != tenant {
		return devapi.OnboardingCredentials{}, devapi.ErrRouterNotFound
	}
	f.issued++
	n := strings.Repeat(string(rune('a'+f.issued%26)), 20)
	return devapi.OnboardingCredentials{SNMPUser: "horus-" + id.String()[28:], SNMPAuthPassword: "A" + n, SNMPPrivPassword: "P" + n,
		APIUser: "horus", APIPassword: "X" + n}, nil
}

type fakeAgent struct {
	mu   sync.Mutex
	last *agentv1.ApplyDesiredStateRequest
	n    int
}

func (a *fakeAgent) ApplyDesiredState(_ context.Context, r *agentv1.ApplyDesiredStateRequest) (*agentv1.ApplyDesiredStateResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.last, a.n = r, a.n+1
	return &agentv1.ApplyDesiredStateResponse{AppliedVersion: r.GetDesiredVersion()}, nil
}

func (a *fakeAgent) peers() []*agentv1.DesiredPeer {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.last == nil {
		return nil
	}
	return a.last.GetPeers()
}

type fakeAudit struct {
	mu      sync.Mutex
	entries []authapi.AuditEntry
}

func (f *fakeAudit) Record(_ context.Context, e authapi.AuditEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, e)
	return nil
}

func (f *fakeAudit) count(action string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, e := range f.entries {
		if e.Action == action {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------- arnés

type env struct {
	svc   *app.Service
	dev   *fakeDevices
	agent *fakeAgent
	audit *fakeAudit
	admin *pgxpool.Pool
	now   *time.Time
	hubID uuid.UUID
}

func setup(t *testing.T) *env {
	t.Helper()
	return setupWith(t, []string{"10.255.0.0/16"}, "10.255.0.0/24")
}

func setupWith(t *testing.T, pools []string, services string) *env {
	t.Helper()
	ctx := context.Background()
	dsn := pgtest.New(t)
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: dsn, AppRole: "wireguard_app", PlatformRole: "wireguard_platform"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := pgdb.Migrate(ctx, db, migrations.Schema, migrations.Postgres(), nil); err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	var pp []netip.Prefix
	for _, p := range pools {
		pp = append(pp, netip.MustParsePrefix(p))
	}
	svcCIDR := netip.MustParsePrefix(services)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	e := &env{dev: newDevices(), agent: &fakeAgent{}, audit: &fakeAudit{}, admin: admin, now: &now, hubID: uuid.Must(uuid.NewV7())}
	e.svc = app.New(app.Options{
		Store:   postgres.New(db),
		Devices: func() (devapi.Onboarding, bool) { return e.dev, true },
		Agent:   func() (wgapi.Agent, bool) { return e.agent, true },
		Audit:   func() (authapi.AuditRecorder, bool) { return e.audit, true },
		Hub: app.Hub{ID: e.hubID, Name: "hub-test", Endpoint: "horus.example.net", ListenPort: 51820, PublicKey: hubKey,
			ServicesCIDR: svcCIDR, Pools: pp},
		CollectorIP: svcCIDR.Addr().Next(), PublicBaseURL: "https://horus.example.net", AccessMode: "domain",
		Now: func() time.Time { return *e.now },
	})
	if err := e.svc.Init(ctx); err != nil {
		t.Fatal(err)
	}
	return e
}

func userCtx(tenant uuid.UUID, perms ...string) context.Context {
	p := &authz.Principal{Type: authz.TypeUser, Subject: uuid.NewString(), Scope: authz.ScopeTenant, TenantID: tenant, Perms: map[string][]string{}}
	for _, perm := range perms {
		p.Perms[perm] = []string{authz.ScopeAll}
	}
	return authz.WithPrincipal(context.Background(), p)
}

func code(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Code
	}
	return ""
}

func tokenOf(t *testing.T, scriptText string) string {
	t.Helper()
	i := strings.Index(scriptText, `\"token\":\"`)
	if i < 0 {
		t.Fatal("script sin token")
	}
	rest := scriptText[i+len(`\"token\":\"`):]
	return rest[:strings.Index(rest, `\"`)]
}

// key genera una clave pública WireGuard válida distinta por n.
func key(n byte) string {
	b := make([]byte, 32)
	b[0], b[31] = n, 0x40
	return base64.StdEncoding.EncodeToString(b)
}

// ---------------------------------------------------------------- tests

// I1-01 criterio 1: router dado de alta → IP de túnel libre y única en toda
// la plataforma, proyectada en el router; agotado → WIREGUARD_IP_POOL_EXHAUSTED.
func TestTunnelAllocationUniqueAcrossTenants(t *testing.T) {
	t.Parallel()
	e := setup(t)
	ctx := context.Background()
	tA, tB := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	seen := map[netip.Addr]bool{}
	for i := range 6 {
		tenant := tA
		if i%2 == 1 {
			tenant = tB
		}
		e.dev.add(tenant, uuid.Must(uuid.NewV7()), "7.16")
	}
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Reconcile(ctx); err != nil { // idempotente
		t.Fatal(err)
	}
	for id, u := range e.dev.tunnels {
		if seen[u.Address] {
			t.Fatalf("IP %s repetida", u.Address)
		}
		seen[u.Address] = true
		if !netip.MustParsePrefix("10.255.0.0/16").Contains(u.Address) || netip.MustParsePrefix("10.255.0.0/24").Contains(u.Address) {
			t.Fatalf("IP %s fuera del rango o en la red de servicios", u.Address)
		}
		if u.OnboardingState != devapi.OnboardingPending || e.dev.routers[id].TunnelAddress == nil {
			t.Fatalf("túnel no proyectado: %+v", u)
		}
	}
	if len(seen) != 6 {
		t.Fatalf("asignadas %d", len(seen))
	}
	var n int
	_ = e.admin.QueryRow(ctx, `SELECT count(*) FROM wireguard.outbox WHERE subject LIKE 'horus.wireguard.peer.created.%'`).Scan(&n)
	if n != 6 {
		t.Fatalf("eventos peer.created = %d", n)
	}
}

func TestPoolExhausted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dsnEnv := setupSmall(t)
	tenant := uuid.Must(uuid.NewV7())
	var errs []error
	for range 4 {
		r := dsnEnv.dev.add(tenant, uuid.Must(uuid.NewV7()), "7.16")
		_, err := dsnEnv.svc.EnsurePeer(ctx, r)
		errs = append(errs, err)
	}
	// /29 con servicios /30: 8 − 4 − broadcast = 3 IPs de router.
	if errs[0] != nil || errs[1] != nil || errs[2] != nil || !errors.Is(errs[3], domain.ErrPoolExhausted) {
		t.Fatalf("errores = %v", errs)
	}
	// En el script: 409 WIREGUARD_IP_POOL_EXHAUSTED.
	r := dsnEnv.dev.add(tenant, uuid.Must(uuid.NewV7()), "7.16")
	_, err := dsnEnv.svc.CreateProvisioningScript(userCtx(tenant, "wireguard.write"), r.ID, app.ProvisioningInput{})
	if code(err) != domain.CodePoolExhausted {
		t.Fatalf("script con rango agotado: %v", err)
	}
}

// setupSmall usa 10.250.0.0/29 con servicios /30 (3 IPs de router).
func setupSmall(t *testing.T) *env {
	t.Helper()
	return setupWith(t, []string{"10.250.0.0/29"}, "10.250.0.0/30")
}
