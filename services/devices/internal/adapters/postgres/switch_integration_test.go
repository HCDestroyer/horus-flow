//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/adapters/postgres"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/devices/migrations"
)

// Switches contra PostgreSQL real (migración 20261018201716): fila de devices.router con device_type
// switch, nunca principal, siempre directo, plataforma coherente con el fabricante, sFlow solo en vrp,
// comunidad v2c (SwOS) cifrada, verificación y unicidad de la dirección de gestión compartida con los
// routers directos, y el filtro device_type del listado (los routers no ven switches por defecto).
func TestSwitchPostgres(t *testing.T) {
	ctx := context.Background()
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: pgtest.New(t), AppRole: "devices_app", PlatformRole: "devices_platform"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := pgdb.Migrate(ctx, db, migrations.Schema, migrations.Postgres(), nil); err != nil {
		t.Fatal(err)
	}
	store := postgres.New(db)
	now := time.Now().UTC()
	none := func(*domain.Router) []outbox.Event { return nil }
	tid := uuid.Must(uuid.NewV7())
	tenant := pgdb.TenantID(tid)
	site := &domain.Site{ID: uuid.Must(uuid.NewV7()), TenantID: tid, Name: "Centro", Kind: "node", Tags: []string{}, CreatedAt: now,
		UpdatedAt: now, Version: 1, PrivateRealmID: uuid.Must(uuid.NewV7())}
	if err := store.CreateSite(ctx, tenant, site, func(*domain.Site) []outbox.Event { return nil }); err != nil {
		t.Fatal(err)
	}
	router := &domain.Router{ID: uuid.Must(uuid.NewV7()), TenantID: tid, SiteID: site.ID, Hostname: "rt-1", Vendor: domain.VendorMikroTik,
		ConnectionMode: domain.ConnectionWireGuard, IsPrimary: true, AdminState: "active", OnboardingState: devapi.OnboardingPending,
		Tags: []string{}, CreatedAt: now, UpdatedAt: now, Version: 1}
	if err := store.CreateRouter(ctx, tenant, router, none); err != nil {
		t.Fatal(err)
	}
	newSwitch := func(name, platform string, mut func(*domain.Router)) (*domain.Router, error) {
		p := platform
		r := &domain.Router{ID: uuid.Must(uuid.NewV7()), TenantID: tid, SiteID: site.ID, Hostname: name, Vendor: domain.VendorForPlatform(p),
			DeviceType: domain.DeviceTypeSwitch, SwitchPlatform: &p, ConnectionMode: domain.ConnectionDirect,
			ProtectionLevel: domain.ProtectionUnencryptedPrivate, AdminState: "active", OnboardingState: devapi.OnboardingPending,
			Tags: []string{}, CreatedAt: now, UpdatedAt: now, Version: 1}
		if mut != nil {
			mut(r)
		}
		return r, store.CreateRouter(ctx, tenant, r, none)
	}
	// Un switch convive con el router principal del nodo (no es principal).
	swos, err := newSwitch("sw-css", domain.PlatformSwOS, nil)
	if err != nil {
		t.Fatal(err)
	}
	hw, err := newSwitch("sw-s5735", domain.PlatformVRP, func(r *domain.Router) { r.SFlowEnabled = true })
	if err != nil {
		t.Fatal(err)
	}
	// CHECK de la migración: principal, sFlow en MikroTik, fabricante incoherente, túnel.
	for name, mut := range map[string]func(*domain.Router){
		"principal":      func(r *domain.Router) { r.IsPrimary = true },
		"sflow mikrotik": func(r *domain.Router) { r.SFlowEnabled = true },
		"fabricante":     func(r *domain.Router) { r.Vendor = domain.VendorHuawei },
		"wireguard": func(r *domain.Router) {
			r.ConnectionMode = domain.ConnectionWireGuard
			r.ProtectionLevel = domain.ProtectionEncrypted
		},
		"por internet":   func(r *domain.Router) { r.ProtectionLevel = domain.ProtectionUnencryptedInternet },
		"sin plataforma": func(r *domain.Router) { r.SwitchPlatform = nil },
	} {
		if _, err := newSwitch("bad-"+name, domain.PlatformRouterOS, mut); err == nil {
			t.Errorf("%s: el CHECK debía rechazarlo", name)
		}
	}
	got, err := store.GetRouter(ctx, tenant, hw.ID)
	if err != nil || !got.IsSwitch() || got.SwitchPlatform == nil || *got.SwitchPlatform != "vrp" || !got.SFlowEnabled || got.IsPrimary {
		t.Fatalf("leer switch: %+v %v", got, err)
	}
	// Listado: por defecto (router) no aparecen los switches.
	list := func(dt string) int {
		rows, err := store.ListRouters(ctx, tenant, app.ListQuery{DeviceType: dt, Limit: 50, SortCol: "hostname"})
		if err != nil {
			t.Fatal(err)
		}
		return len(rows)
	}
	if list(domain.DeviceTypeRouter) != 1 || list(domain.DeviceTypeSwitch) != 2 || list("") != 3 {
		t.Fatalf("filtro device_type: %d %d %d", list("router"), list("switch"), list(""))
	}

	// SwOS: comunidad v2c cifrada; se revela como versión 2c.
	on := app.NewOnboarding(store, envelope.Ephemeral(), nil)
	_, community, err := on.IssueSwitchCredentials(ctx, tid, swos.ID, domain.PlatformSwOS)
	if err != nil || len(community) < 16 {
		t.Fatalf("comunidad: %v", err)
	}
	c, err := on.RevealSNMPCredential(ctx, tid, swos.ID)
	if err != nil || c.Version != "2c" || c.Community != community {
		t.Fatalf("revelar v2c: %+v %v", c, err)
	}
	// Verificación en la dirección de gestión; la dirección verificada es única en la plataforma.
	mgmt := "10.20.0.5"
	setAddr := func(id uuid.UUID) {
		if err := store.SetRouterTunnel(ctx, tenant, id, func(r *domain.Router) bool { r.ManagementAddress = &mgmt; return true }, none); err != nil {
			t.Fatal(err)
		}
	}
	setAddr(swos.ID)
	if err := on.VerifyDirect(ctx, tid, swos.ID, netip.MustParseAddr(mgmt)); err != nil {
		t.Fatal(err)
	}
	setAddr(hw.ID)
	if err := on.VerifyDirect(ctx, tid, hw.ID, netip.MustParseAddr(mgmt)); !errors.Is(err, devapi.ErrManagementAddressTaken) {
		t.Fatalf("dirección verificada duplicada: %v", err)
	}
	all, err := on.ListRouters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range all {
		if r.ID == swos.ID && (r.DeviceType != "switch" || r.SwitchPlatform != "swos" || r.OnboardingState != devapi.OnboardingSNMPVerified) {
			t.Fatalf("RouterInfo del switch: %+v", r)
		}
	}
}
