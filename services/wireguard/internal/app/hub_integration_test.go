//go:build integration

package app_test

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/app"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/domain"
)

func platformCtx() context.Context {
	return authz.WithPrincipal(context.Background(), &authz.Principal{Type: authz.TypeUser, Subject: uuid.NewString(),
		Scope: authz.ScopePlatform, Perms: map[string][]string{app.PermManage: {authz.ScopeAll}}, AuthTime: time.Now()})
}

func ptr[T any](v T) *T { return &v }

func (e *env) hub(t *testing.T) *app.Hub {
	t.Helper()
	hubs, err := e.svc.ListHubs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := range hubs {
		if hubs[i].ID == e.hubID {
			return &hubs[i]
		}
	}
	t.Fatal("hub no encontrado")
	return nil
}

func (e *env) desiredVersion(t *testing.T) int64 {
	t.Helper()
	var v int64
	if err := e.admin.QueryRow(context.Background(), `SELECT desired_version FROM wireguard.server WHERE id = $1`, e.hubID).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// El entorno (HORUS_WG_*) da de alta el hub, pero lo editado desde la consola
// no se sobrescribe al arrancar; lo no editado sigue al entorno; los rangos
// del entorno nuevos se insertan y los añadidos desde la consola se conservan.
func TestEnsureHubKeepsConsoleEdits(t *testing.T) {
	t.Parallel()
	e := setup(t)
	ctx := context.Background()
	pctx := platformCtx()
	h := e.hub(t)
	if h.MTU != 1420 || h.Keepalive != 25 || h.Version != 1 || len(h.PoolInfo) != 1 {
		t.Fatalf("hub inicial = %+v", h)
	}
	// Reiniciar con el mismo entorno no cambia la versión (ETag estable).
	if err := app.New(e.opts).Init(ctx); err != nil {
		t.Fatal(err)
	}
	if v := e.hub(t).Version; v != 1 {
		t.Fatalf("versión tras reiniciar sin cambios = %d", v)
	}
	h, err := e.svc.UpdateHub(pctx, e.hubID, 1, app.HubPatch{Endpoint: ptr("wg.isp.example"), MTU: ptr(1380)}, app.AuditMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if h.Endpoint != "wg.isp.example" || h.MTU != 1380 || h.Version != 2 {
		t.Fatalf("tras PATCH = %+v", h)
	}
	if _, err := e.svc.AddPool(pctx, e.hubID, app.PoolInput{CIDR: ptr("10.254.0.0/24")}, app.AuditMeta{}); err != nil {
		t.Fatal(err)
	}
	// Arranque con otro entorno: endpoint distinto (editado: se ignora),
	// nombre y puerto distintos (no editados: se aplican), un rango nuevo.
	o := e.opts
	o.Hub.Endpoint, o.Hub.Name, o.Hub.ListenPort = "env-new.example", "hub-env", 51821
	o.Hub.Pools = []netip.Prefix{netip.MustParsePrefix("10.255.0.0/16"), netip.MustParsePrefix("10.240.0.0/16")}
	if err := app.New(o).Init(ctx); err != nil {
		t.Fatal(err)
	}
	h = e.hub(t)
	if h.Endpoint != "wg.isp.example" || h.MTU != 1380 || h.Name != "hub-env" || h.ListenPort != 51821 {
		t.Fatalf("tras reiniciar = %+v", h)
	}
	var got []string
	for _, p := range h.PoolInfo {
		got = append(got, p.CIDR.String())
	}
	if strings.Join(got, ",") != "10.255.0.0/16,10.254.0.0/24,10.240.0.0/16" {
		t.Fatalf("rangos = %v", got)
	}
	if h.Version <= 3 || !strings.Contains(strings.Join(h.Overrides, ","), "endpoint") {
		t.Fatalf("versión %d, overrides %v", h.Version, h.Overrides)
	}
	// Quitar del entorno un rango no lo borra.
	o.Hub.Pools = o.Hub.Pools[:1]
	if err := app.New(o).Init(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(e.hub(t).PoolInfo); n != 3 {
		t.Fatalf("rangos tras quitarlo del entorno = %d", n)
	}
	if e.audit.count(app.AuditHubUpdated) != 1 || e.audit.count(app.AuditPoolAdded) != 1 {
		t.Fatal("cambios sin auditar")
	}
	// If-Match viejo con la base real: 412 con la representación actual.
	_, err = e.svc.UpdateHub(pctx, e.hubID, 2, app.HubPatch{MTU: ptr(1400)}, app.AuditMeta{})
	if code(err) != "PRECONDITION_FAILED" {
		t.Fatalf("If-Match viejo: %v", err)
	}
}

// Un rango añadido desde la consola amplía la IPAM: con el primero agotado,
// el siguiente router recibe una /32 del nuevo; el agente recibe una versión
// deseada nueva. Un rango con direcciones (o en cuarentena) no se borra.
func TestAllocationAcrossAddedPool(t *testing.T) {
	t.Parallel()
	e := setupSmall(t) // 10.250.0.0/29, servicios /30: 3 IPs
	ctx := context.Background()
	pctx := platformCtx()
	tenant := uuid.Must(uuid.NewV7())
	for range 3 {
		if _, err := e.svc.EnsurePeer(ctx, e.dev.add(tenant, uuid.Must(uuid.NewV7()), "7.16")); err != nil {
			t.Fatal(err)
		}
	}
	r4 := e.dev.add(tenant, uuid.Must(uuid.NewV7()), "7.16")
	if _, err := e.svc.EnsurePeer(ctx, r4); !errors.Is(err, domain.ErrPoolExhausted) {
		t.Fatalf("rango agotado: %v", err)
	}
	before := e.desiredVersion(t)
	pool, err := e.svc.AddPool(pctx, e.hubID, app.PoolInput{CIDR: ptr("10.251.0.0/28")}, app.AuditMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if pool.Total != 14 || e.desiredVersion(t) != before+1 {
		t.Fatalf("pool %+v, desired %d → %d", pool, before, e.desiredVersion(t))
	}
	p4, err := e.svc.EnsurePeer(ctx, r4)
	if err != nil || p4.Address != netip.MustParseAddr("10.251.0.1") {
		t.Fatalf("peer en el rango nuevo = %v, %v", p4, err)
	}
	h := e.hub(t)
	if len(h.PoolInfo) != 2 || h.PoolInfo[0].Used != 3 || h.PoolInfo[1].Used != 1 || h.AddressesUsed != 4 || h.AddressesTotal != 3+14 {
		t.Fatalf("ocupación = %+v", h)
	}
	// Solape con la base (sin pasar por la validación): EXCLUDE → ErrPoolOverlap.
	if _, err := e.store.AddPool(ctx, e.hubID, netip.MustParsePrefix("10.251.0.0/24")); !errors.Is(err, app.ErrPoolOverlap) {
		t.Fatalf("EXCLUDE: %v", err)
	}
	if _, err := e.svc.AddPool(pctx, e.hubID, app.PoolInput{CIDR: ptr("10.251.0.0/24")}, app.AuditMeta{}); code(err) != domain.CodePoolOverlap {
		t.Fatalf("solape: %v", err)
	}
	// En uso: 409; tras revocar el peer sigue en cuarentena 24 h.
	if err := e.svc.DeletePool(pctx, e.hubID, pool.ID, app.AuditMeta{}); code(err) != domain.CodePoolInUse {
		t.Fatalf("borrar en uso: %v", err)
	}
	e.dev.del(r4.ID)
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	*e.now = e.now.Add(time.Hour)
	if err := e.svc.DeletePool(pctx, e.hubID, pool.ID, app.AuditMeta{}); code(err) != domain.CodePoolInUse {
		t.Fatalf("borrar en cuarentena: %v", err)
	}
	// El rango del entorno (y el último) no se borra.
	if err := e.svc.DeletePool(pctx, e.hubID, h.PoolInfo[0].ID, app.AuditMeta{}); code(err) != "CONFLICT" {
		t.Fatalf("borrar rango del entorno: %v", err)
	}
	*e.now = e.now.Add(24 * time.Hour)
	before = e.desiredVersion(t)
	if err := e.svc.DeletePool(pctx, e.hubID, pool.ID, app.AuditMeta{}); err != nil {
		t.Fatal(err)
	}
	if h := e.hub(t); len(h.PoolInfo) != 1 || e.desiredVersion(t) != before+1 || e.audit.count(app.AuditPoolDelete) != 1 {
		t.Fatalf("tras borrar = %+v", h)
	}
	if err := e.svc.DeletePool(pctx, e.hubID, pool.ID, app.AuditMeta{}); code(err) != domain.CodePoolNotFound {
		t.Fatalf("borrar dos veces: %v", err)
	}
	if err := e.svc.DeletePool(pctx, e.hubID, e.hub(t).PoolInfo[0].ID, app.AuditMeta{}); code(err) != "CONFLICT" {
		t.Fatalf("último rango: %v", err)
	}
	// El rango puede volver a añadirse (sin restos de asignaciones viejas).
	if _, err := e.svc.AddPool(pctx, e.hubID, app.PoolInput{CIDR: ptr("10.251.0.0/28")}, app.AuditMeta{}); err != nil {
		t.Fatal(err)
	}
}

// Los scripts nuevos usan endpoint, MTU y keepalive del hub; los peers
// creados después heredan el keepalive; los ya creados no cambian hasta que
// enrolan con un script nuevo.
func TestScriptUsesHubSettings(t *testing.T) {
	t.Parallel()
	e := setup(t)
	ctx := context.Background()
	tenant := uuid.Must(uuid.NewV7())
	uctx := userCtx(tenant, "wireguard.write", "wireguard.read")
	old := e.dev.add(tenant, uuid.Must(uuid.NewV7()), "7.16")
	oldPeer, err := e.svc.EnsurePeer(ctx, old)
	if err != nil || oldPeer.Keepalive != 25 {
		t.Fatalf("peer previo = %+v, %v", oldPeer, err)
	}
	if _, err := e.svc.UpdateHub(platformCtx(), e.hubID, 1, app.HubPatch{Endpoint: ptr("203.0.113.10"), MTU: ptr(1380),
		Keepalive: ptr(15)}, app.AuditMeta{}); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.svc.GetPeer(uctx, oldPeer.ID); p.Keepalive != 25 {
		t.Fatalf("peer previo cambiado: %d", p.Keepalive)
	}
	r := e.dev.add(tenant, uuid.Must(uuid.NewV7()), "7.16")
	res, err := e.svc.CreateProvisioningScript(uctx, r.ID, app.ProvisioningInput{})
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{"mtu=1380", "persistent-keepalive=15s", "endpoint-address=203.0.113.10 endpoint-port=51820"} {
		if !strings.Contains(res.Script, must) {
			t.Errorf("script sin %q", must)
		}
	}
	peer, err := e.svc.EnsurePeer(ctx, e.dev.routers[r.ID])
	if err != nil || peer.Keepalive != 15 {
		t.Fatalf("peer nuevo = %+v, %v", peer, err)
	}
	// El router previo recibe un script nuevo y enrola: adopta el keepalive.
	res, err = e.svc.CreateProvisioningScript(uctx, old.ID, app.ProvisioningInput{})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Enroll(ctx, app.EnrollInput{Token: tokenOf(t, res.Script), PublicKey: key(7), IP: netip.MustParseAddr("198.51.100.9")}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Push(ctx, true); err != nil {
		t.Fatal(err)
	}
	peers := e.agent.peers()
	if len(peers) != 1 || peers[0].GetPersistentKeepaliveSeconds() != 15 {
		t.Fatalf("estado deseado = %v", peers)
	}
}
