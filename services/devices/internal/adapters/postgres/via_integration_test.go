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

// Migración 20261018222025 contra PostgreSQL real: la dirección de gestión es
// única por ISP (dos ISP verifican la misma IP privada en modo directo) y,
// vía router, por (ISP, router, IP); CHECK y FK del modo via_router (solo
// switches, router del mismo ISP, nunca él mismo); redes de gestión
// declaradas sin solapes por router y con RLS.
func TestViaRouterPostgres(t *testing.T) {
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
	on := app.NewOnboarding(store, envelope.Ephemeral(), nil)
	now := time.Now().UTC()
	none := func(*domain.Router) []outbox.Event { return nil }
	type isp struct {
		id     uuid.UUID
		t      pgdb.TenantID
		site   uuid.UUID
		router *domain.Router
	}
	newISP := func(name string) isp {
		tid := uuid.Must(uuid.NewV7())
		x := isp{id: tid, t: pgdb.TenantID(tid)}
		site := &domain.Site{ID: uuid.Must(uuid.NewV7()), TenantID: tid, Name: name, Kind: "node", Tags: []string{}, CreatedAt: now,
			UpdatedAt: now, Version: 1, PrivateRealmID: uuid.Must(uuid.NewV7())}
		if err := store.CreateSite(ctx, x.t, site, func(*domain.Site) []outbox.Event { return nil }); err != nil {
			t.Fatal(err)
		}
		x.site = site.ID
		return x
	}
	newRouter := func(x isp, name string, primary bool) *domain.Router {
		r := &domain.Router{ID: uuid.Must(uuid.NewV7()), TenantID: x.id, SiteID: x.site, Hostname: name, Vendor: domain.VendorMikroTik,
			ConnectionMode: domain.ConnectionWireGuard, IsPrimary: primary, AdminState: "active", OnboardingState: devapi.OnboardingPending,
			Tags: []string{}, CreatedAt: now, UpdatedAt: now, Version: 1}
		if err := store.CreateRouter(ctx, x.t, r, none); err != nil {
			t.Fatal(err)
		}
		return r
	}
	newSwitch := func(x isp, name string) *domain.Router {
		p := domain.PlatformVRP
		r := &domain.Router{ID: uuid.Must(uuid.NewV7()), TenantID: x.id, SiteID: x.site, Hostname: name, Vendor: domain.VendorHuawei,
			DeviceType: domain.DeviceTypeSwitch, SwitchPlatform: &p, ConnectionMode: domain.ConnectionDirect,
			ProtectionLevel: domain.ProtectionUnencryptedPrivate, AdminState: "active", OnboardingState: devapi.OnboardingPending,
			Tags: []string{}, CreatedAt: now, UpdatedAt: now, Version: 1}
		if err := store.CreateRouter(ctx, x.t, r, none); err != nil {
			t.Fatal(err)
		}
		return r
	}
	set := func(x isp, id uuid.UUID, fn func(r *domain.Router)) error {
		return store.SetRouterTunnel(ctx, x.t, id, func(r *domain.Router) bool { fn(r); return true }, none)
	}
	A, B := newISP("A"), newISP("B")
	A.router, B.router = newRouter(A, "rt-a", true), newRouter(B, "rt-b", true)
	private := "192.168.88.2"

	// 1) Modo directo: la misma IP privada verificada en dos ISP (antes, única en la plataforma).
	dA, dB := newSwitch(A, "sw-direct-a"), newSwitch(B, "sw-direct-b")
	for _, c := range []struct {
		x  isp
		sw *domain.Router
	}{{A, dA}, {B, dB}} {
		if err := set(c.x, c.sw.ID, func(r *domain.Router) { r.ManagementAddress = &private }); err != nil {
			t.Fatal(err)
		}
		if err := on.VerifyDirect(ctx, c.x.id, c.sw.ID, netip.MustParseAddr(private)); err != nil {
			t.Fatalf("verificación directa en %s: %v", c.sw.Hostname, err)
		}
	}
	// Dentro de un ISP sigue siendo única entre verificados.
	dA2 := newSwitch(A, "sw-direct-a2")
	if err := set(A, dA2.ID, func(r *domain.Router) { r.ManagementAddress = &private }); err != nil {
		t.Fatal(err)
	}
	if err := on.VerifyDirect(ctx, A.id, dA2.ID, netip.MustParseAddr(private)); !errors.Is(err, devapi.ErrManagementAddressTaken) {
		t.Fatalf("duplicada en el mismo ISP: %v", err)
	}

	// 2) Vía router: única por (ISP, router, IP privada).
	via := func(x isp, sw *domain.Router, router uuid.UUID, addr *string) error {
		return set(x, sw.ID, func(r *domain.Router) {
			r.ConnectionMode, r.ViaRouterID, r.ManagementAddress = domain.ConnectionViaRouter, &router, addr
		})
	}
	vA1, vA2 := newSwitch(A, "sw-via-a1"), newSwitch(A, "sw-via-a2")
	if err := via(A, vA1, A.router.ID, &private); err != nil {
		t.Fatal(err)
	}
	if err := via(A, vA2, A.router.ID, &private); !errors.Is(err, app.ErrManagementAddressTaken) {
		t.Fatalf("misma IP por el mismo router: %v", err)
	}
	rA2 := newRouter(A, "rt-a2", false)
	if err := via(A, vA2, rA2.ID, &private); err != nil {
		t.Fatalf("misma IP por otro router del mismo ISP: %v", err)
	}
	vB := newSwitch(B, "sw-via-b")
	if err := via(B, vB, B.router.ID, &private); err != nil {
		t.Fatalf("misma IP en otro ISP: %v", err)
	}
	// La dirección Horus se proyecta (única en la plataforma) y la verificación va por ella.
	if err := on.SetViaAddress(ctx, A.id, vA1.ID, A.router.ID, netip.MustParseAddr(private), netip.MustParseAddr("10.255.1.40")); err != nil {
		t.Fatal(err)
	}
	if err := set(B, vB.ID, func(r *domain.Router) { a := "10.255.1.40"; r.ViaAddress = &a }); !errors.Is(err, app.ErrViaAddressTaken) {
		t.Fatalf("dirección Horus repetida: %v", err)
	}
	if err := on.VerifyDirect(ctx, A.id, vA1.ID, netip.MustParseAddr("10.255.1.40")); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetRouter(ctx, A.t, vA1.ID)
	if err != nil || got.OnboardingState != devapi.OnboardingSNMPVerified || got.ViaRouterID == nil || *got.ViaRouterID != A.router.ID ||
		got.ViaAddress == nil || *got.ViaAddress != "10.255.1.40" {
		t.Fatalf("switch vía router: %+v %v", got, err)
	}
	info, err := on.GetRouter(ctx, A.id, vA1.ID)
	if err != nil || info.ConnectionMode != devapi.ConnectionViaRouter || info.ViaAddress == nil || info.ViaRouterID == nil {
		t.Fatalf("RouterInfo: %+v %v", info, err)
	}

	// 3) CHECK y FK.
	for name, fn := range map[string]func() error{
		"via sin router": func() error {
			return set(A, dA2.ID, func(r *domain.Router) { r.ConnectionMode = domain.ConnectionViaRouter; r.ViaRouterID = nil })
		},
		"router vía router": func() error {
			return set(A, rA2.ID, func(r *domain.Router) { r.ConnectionMode, r.ViaRouterID = domain.ConnectionViaRouter, &A.router.ID })
		},
		"él mismo": func() error { id := dA2.ID; return via(A, dA2, id, nil) },
		"router de otro ISP": func() error {
			return via(A, dA2, B.router.ID, nil)
		},
		"via_router_id en directo": func() error {
			return set(A, dA2.ID, func(r *domain.Router) { r.ViaRouterID = &A.router.ID })
		},
	} {
		if err := fn(); err == nil {
			t.Errorf("%s: aceptado", name)
		}
	}

	// 4) Listado por router del sitio y recuento (baja y cambio de transporte lo consultan).
	rows, err := store.ListRouters(ctx, A.t, app.ListQuery{DeviceType: domain.DeviceTypeSwitch, ViaRouterID: &A.router.ID, Limit: 50, SortCol: "hostname"})
	if err != nil || len(rows) != 1 || rows[0].ID != vA1.ID {
		t.Fatalf("switches vía rt-a: %+v %v", rows, err)
	}
	if n, err := store.CountViaDevices(ctx, A.t, A.router.ID); err != nil || n != 1 {
		t.Fatalf("CountViaDevices: %d %v", n, err)
	}
	if n, err := store.CountViaDevices(ctx, B.t, A.router.ID); err != nil || n != 0 {
		t.Fatalf("CountViaDevices desde otro ISP: %d %v", n, err)
	}

	// 5) Redes de gestión declaradas.
	sub := func(x isp, router uuid.UUID, p string) error {
		return store.CreateManagementSubnet(ctx, x.t, &domain.ManagementSubnet{ID: uuid.Must(uuid.NewV7()), TenantID: x.id, RouterID: router,
			Prefix: netip.MustParsePrefix(p), CreatedAt: now, CreatedBy: "user:test"})
	}
	if err := sub(A, A.router.ID, "192.168.88.0/24"); err != nil {
		t.Fatal(err)
	}
	if err := sub(A, A.router.ID, "192.168.88.128/25"); !errors.Is(err, app.ErrSubnetOverlap) {
		t.Fatalf("solape en el mismo router: %v", err)
	}
	if err := sub(A, rA2.ID, "192.168.88.0/24"); err != nil {
		t.Fatalf("la misma red en otro router del ISP: %v", err)
	}
	if err := sub(B, B.router.ID, "192.168.88.0/24"); err != nil {
		t.Fatalf("la misma red en otro ISP: %v", err)
	}
	if err := sub(B, A.router.ID, "10.10.0.0/24"); err == nil {
		t.Fatal("red en un router de otro ISP aceptada")
	}
	m, err := store.ManagementSubnetFor(ctx, A.t, A.router.ID, netip.MustParseAddr(private))
	if err != nil || m.Prefix.String() != "192.168.88.0/24" {
		t.Fatalf("red que contiene la IP: %+v %v", m, err)
	}
	if n, err := store.ViaDevicesIn(ctx, A.t, A.router.ID, m.Prefix); err != nil || n != 1 {
		t.Fatalf("switches dentro de la red: %d %v", n, err)
	}
	if subs, err := store.ListManagementSubnets(ctx, B.t, A.router.ID); err != nil || len(subs) != 0 {
		t.Fatalf("B ve las redes de A (RLS): %+v %v", subs, err)
	}
	if err := store.DeleteManagementSubnet(ctx, B.t, A.router.ID, m.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("B borra una red de A: %v", err)
	}
	if err := store.DeleteManagementSubnet(ctx, A.t, A.router.ID, m.ID); err != nil {
		t.Fatal(err)
	}
}
