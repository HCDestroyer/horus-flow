//go:build integration

package app_test

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/google/uuid"

	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	viaapi "github.com/hcdestroyer/horus-flow/services/wireguard/api"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/app"
)

// enrolled da de alta un router WireGuard con su clave en el hub.
func enrolled(t *testing.T, e *env, tenant uuid.UUID, n byte) devapi.RouterInfo {
	t.Helper()
	r := e.dev.add(tenant, uuid.Must(uuid.NewV7()), "7.16.1")
	e.dev.mu.Lock()
	r.ConnectionMode = devapi.ConnectionWireGuard
	e.dev.routers[r.ID] = r
	e.dev.mu.Unlock()
	res, err := e.svc.CreateProvisioningScript(userCtx(tenant, "wireguard.write"), r.ID, app.ProvisioningInput{})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Enroll(context.Background(), app.EnrollInput{Token: tokenOf(t, res.Script), PublicKey: key(n)}); err != nil {
		t.Fatal(err)
	}
	return e.dev.routers[r.ID]
}

// viaSwitch registra en el doble de devices un switch vía router.
func viaSwitch(e *env, tenant uuid.UUID, via devapi.RouterInfo, private string) devapi.RouterInfo {
	e.dev.mu.Lock()
	defer e.dev.mu.Unlock()
	p, vid := netip.MustParseAddr(private), via.ID
	sw := devapi.RouterInfo{ID: uuid.Must(uuid.NewV7()), TenantID: tenant, SiteID: via.SiteID, Name: "sw", DeviceType: devapi.DeviceTypeSwitch,
		SwitchPlatform: devapi.PlatformVRP, Vendor: devapi.VendorHuawei, ConnectionMode: devapi.ConnectionViaRouter, ManagementAddress: &p,
		ViaRouterID: &vid, OnboardingState: devapi.OnboardingPending}
	e.dev.routers[sw.ID] = sw
	return sw
}

func allowedOf(e *env, publicKey string) []string {
	for _, p := range e.agent.peers() {
		if p.GetPublicKey() == publicKey {
			return p.GetAllowedIps()
		}
	}
	return nil
}

// ADR-0032: dos ISP con el MISMO switch privado (192.168.88.2) detrás de sus
// routers reciben direcciones Horus distintas de la IPAM de túneles; el hub
// enruta cada una solo por el peer de su router (nunca la IP privada); la
// misma IP privada no se repite por router dentro de un ISP; una red de
// gestión declarada usa un bloque alineado del mismo tamaño; dar de baja el
// switch libera la dirección y la quita del hub.
func TestViaRouterOverlappingPrivateAddresses(t *testing.T) {
	t.Parallel()
	e := setup(t)
	ctx := context.Background()
	tA, tB := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	rA, rB := enrolled(t, e, tA, 1), enrolled(t, e, tB, 2)
	swA, swB := viaSwitch(e, tA, rA, "192.168.88.2"), viaSwitch(e, tB, rB, "192.168.88.2")
	req := func(sw devapi.RouterInfo, via devapi.RouterInfo) viaapi.ViaPathRequest {
		return viaapi.ViaPathRequest{TenantID: sw.TenantID, DeviceID: sw.ID, DeviceName: "sw-acceso", RouterID: via.ID, Target: *sw.ManagementAddress, SFlow: true}
	}
	pA, err := e.svc.EnsureViaPath(ctx, req(swA, rA))
	if err != nil {
		t.Fatal(err)
	}
	pB, err := e.svc.EnsureViaPath(ctx, req(swB, rB))
	if err != nil {
		t.Fatal(err)
	}
	svc := netip.MustParsePrefix("10.255.0.0/24")
	if pA.Address == pB.Address || svc.Contains(pA.Address) || pA.Address == *rA.TunnelAddress || pB.Address == *rB.TunnelAddress ||
		!netip.MustParsePrefix("10.255.0.0/16").Contains(pA.Address) {
		t.Fatalf("direcciones Horus: A=%s B=%s", pA.Address, pB.Address)
	}
	if pA.Transport != "wireguard" || pA.PollerAddress != netip.MustParseAddr("10.255.0.1") || pA.CollectorPort != 6343 ||
		!strings.Contains(pA.RouterConfig.Config, "dst-address="+pA.Address.String()+" action=netmap to-addresses=192.168.88.2") {
		t.Fatalf("camino A: %+v", pA)
	}
	// Idempotente: misma dirección.
	if again, err := e.svc.EnsureViaPath(ctx, req(swA, rA)); err != nil || again.Address != pA.Address {
		t.Fatalf("reintento: %v %s", err, again.Address)
	}
	// El hub enruta la dirección Horus solo por el peer de su router; nunca la IP privada.
	if err := e.svc.Push(ctx, true); err != nil {
		t.Fatal(err)
	}
	gotA, gotB := strings.Join(allowedOf(e, key(1)), ","), strings.Join(allowedOf(e, key(2)), ",")
	if !strings.Contains(gotA, pA.Address.String()+"/32") || strings.Contains(gotA, pB.Address.String()) ||
		!strings.Contains(gotB, pB.Address.String()+"/32") || strings.Contains(gotA+gotB, "192.168.") {
		t.Fatalf("allowed-ips: A=%s B=%s", gotA, gotB)
	}
	// Misma IP privada por el mismo router en el mismo ISP → en uso.
	dup := viaSwitch(e, tA, rA, "192.168.88.2")
	if _, err := e.svc.EnsureViaPath(ctx, req(dup, rA)); !errors.Is(err, viaapi.ErrViaTargetInUse) {
		t.Fatalf("IP privada repetida por el mismo router: %v", err)
	}
	// Router de otro ISP, un switch como router o un router en modo directo → inválido.
	if _, err := e.svc.EnsureViaPath(ctx, req(swA, rB)); !errors.Is(err, viaapi.ErrViaRouterInvalid) {
		t.Fatalf("router de otro ISP: %v", err)
	}
	if _, err := e.svc.EnsureViaPath(ctx, req(dup, swA)); !errors.Is(err, viaapi.ErrViaRouterInvalid) {
		t.Fatalf("switch como router: %v", err)
	}
	// Red de gestión declarada en el router de A: bloque /28 alineado; la dirección del switch dentro del bloque.
	sw2 := viaSwitch(e, tA, rA, "192.168.89.5")
	r2 := req(sw2, rA)
	r2.Subnet = &viaapi.ViaSubnet{ID: uuid.Must(uuid.NewV7()), Prefix: netip.MustParsePrefix("192.168.89.0/28")}
	p2, err := e.svc.EnsureViaPath(ctx, r2)
	if err != nil {
		t.Fatal(err)
	}
	if p2.Block == nil || p2.Block.Bits() != 28 || !p2.Block.Contains(p2.Address) || p2.Address.As4()[3]%16 != 5 ||
		!strings.Contains(p2.RouterConfig.Config, "src-address=192.168.89.0/28") {
		t.Fatalf("bloque de la red: %+v", p2)
	}
	if err := e.svc.Push(ctx, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(allowedOf(e, key(1)), ","), p2.Block.String()) {
		t.Fatalf("el bloque no viaja con el peer: %v", allowedOf(e, key(1)))
	}
	// Baja del switch de A: la reconciliación libera su dirección y el hub deja de enrutarla.
	e.dev.del(swA.ID)
	e.dev.del(dup.ID)
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Push(ctx, true); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(allowedOf(e, key(1)), ","); strings.Contains(got, pA.Address.String()+"/32") {
		t.Fatalf("dirección de un switch dado de baja sigue en el hub: %s", got)
	}
	var released int
	if err := e.admin.QueryRow(ctx, `SELECT count(*) FROM wireguard.ip_allocation WHERE address = $1::inet AND released_at IS NOT NULL`,
		pA.Address.String()+"/32").Scan(&released); err != nil || released != 1 {
		t.Fatalf("asignación liberada (cuarentena): %d %v", released, err)
	}
	// RLS: el tenant B no ve las asignaciones de A.
	tx, err := e.admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE wireguard_app`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('horus.tenant_id', $1, true)`, tB.String()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM wireguard.via_mapping WHERE tenant_id = $1`, tA).Scan(&n); err != nil || n != 0 {
		t.Fatalf("B ve asignaciones de A: %d %v", n, err)
	}
}
