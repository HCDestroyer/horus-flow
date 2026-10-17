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

// Huawei VRP en modo directo contra PostgreSQL real (migración 20261018100000): vendor huawei con
// connection_mode direct, dirección de gestión, verificación SNMP (snmp_verified) y la unicidad de la
// dirección verificada en toda la plataforma (otro ISP no puede apropiarse de ella).
func TestDirectRouterPostgres(t *testing.T) {
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
	newRouter := func(vendor string) (pgdb.TenantID, *domain.Router) {
		tid := uuid.Must(uuid.NewV7())
		tenant := pgdb.TenantID(tid)
		site := &domain.Site{ID: uuid.Must(uuid.NewV7()), TenantID: tid, Name: "Centro", Kind: "node", Tags: []string{}, CreatedAt: now,
			UpdatedAt: now, Version: 1, PrivateRealmID: uuid.Must(uuid.NewV7())}
		if err := store.CreateSite(ctx, tenant, site, func(*domain.Site) []outbox.Event { return nil }); err != nil {
			t.Fatal(err)
		}
		r := &domain.Router{ID: uuid.Must(uuid.NewV7()), TenantID: tid, SiteID: site.ID, Hostname: "rt-" + vendor, Vendor: vendor,
			ConnectionMode: domain.ConnectionModeFor(vendor), IsPrimary: true, AdminState: "active", OnboardingState: devapi.OnboardingPending,
			Tags: []string{}, CreatedAt: now, UpdatedAt: now, Version: 1}
		if err := store.CreateRouter(ctx, tenant, r, none); err != nil {
			t.Fatal(err)
		}
		return tenant, r
	}

	tA, hw := newRouter(domain.VendorHuawei)
	got, err := store.GetRouter(ctx, tA, hw.ID)
	if err != nil || got.Vendor != "huawei" || got.ConnectionMode != "direct" || got.ManagementAddress != nil || got.TunnelAddress != nil {
		t.Fatalf("alta Huawei: %+v %v", got, err)
	}
	tB, mk := newRouter(domain.VendorMikroTik)
	if got, _ := store.GetRouter(ctx, tB, mk.ID); got.ConnectionMode != "wireguard" {
		t.Fatalf("MikroTik: %+v", got)
	}

	sealer := envelope.Ephemeral()
	on := app.NewOnboarding(store, sealer, nil)
	mgmt := "198.51.100.20"
	set := func(t pgdb.TenantID, id uuid.UUID, addr string) error {
		return store.SetRouterTunnel(ctx, t, id, func(r *domain.Router) bool {
			r.ManagementAddress = &addr
			return true
		}, none)
	}
	if err := set(tA, hw.ID, mgmt); err != nil {
		t.Fatal(err)
	}
	// Credencial SNMPv3 directa (SHA-256/AES-128, sin API de RouterOS), cifrada en reposo.
	creds, err := on.IssueDirectCredentials(ctx, tA.UUID(), hw.ID)
	if err != nil || creds.APIUser != "" {
		t.Fatalf("credenciales: %+v %v", creds, err)
	}
	snmp, err := on.RevealSNMPCredential(ctx, tA.UUID(), hw.ID)
	if err != nil || snmp.AuthProtocol != "sha256" || snmp.AuthPassword != creds.SNMPAuthPassword {
		t.Fatalf("revelar: %+v %v", snmp, err)
	}
	if err := on.VerifyDirect(ctx, tA.UUID(), hw.ID, netip.MustParseAddr(mgmt)); err != nil {
		t.Fatal(err)
	}
	info, err := on.GetRouter(ctx, tA.UUID(), hw.ID)
	if err != nil || info.OnboardingState != devapi.OnboardingSNMPVerified || info.ManagementAddress.String() != mgmt {
		t.Fatalf("verificado: %+v %v", info, err)
	}

	// Otro ISP con un Huawei que declara la MISMA dirección: puede guardarla (pendiente), pero no verificarla.
	tC, other := newRouter(domain.VendorHuawei)
	if err := set(tC, other.ID, mgmt); err != nil {
		t.Fatalf("dirección pendiente duplicada: %v", err)
	}
	if err := on.VerifyDirect(ctx, tC.UUID(), other.ID, netip.MustParseAddr(mgmt)); !errors.Is(err, devapi.ErrManagementAddressTaken) {
		t.Fatalf("la dirección verificada es única en la plataforma: %v", err)
	}
	// Un MikroTik no admite dirección de gestión (CHECK de la migración).
	if err := set(tB, mk.ID, "198.51.100.30"); err == nil {
		t.Fatal("MikroTik con management_address")
	}
	// ListAllRouters (rol de plataforma) proyecta vendor y modo para wireguard y snmp.
	all, err := on.ListRouters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	modes := map[uuid.UUID]string{}
	for _, r := range all {
		modes[r.ID] = r.Vendor + "/" + r.ConnectionMode
	}
	if modes[hw.ID] != "huawei/direct" || modes[mk.ID] != "mikrotik/wireguard" {
		t.Fatalf("ListRouters: %v", modes)
	}
}
