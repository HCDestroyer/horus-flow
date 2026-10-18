//go:build integration

package tenancy

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/hcdestroyer/horus-flow/packages/go/module"
	"github.com/hcdestroyer/horus-flow/packages/go/testkit"
	tunapi "github.com/hcdestroyer/horus-flow/services/tunnelagent/api"
	"github.com/hcdestroyer/horus-flow/services/tunnelagent/api/tunnelv1"
)

// peerOf devuelve el túnel vivo del router (GET /wireguard/peers?router_id=).
func (a *app) peerOf(token, router string) map[string]any {
	a.t.Helper()
	r := a.must(a.do(req{Method: "GET", Path: "/api/v1/wireguard/peers?router_id=" + router, Token: token}), 200)
	for _, x := range r.Body["data"].([]any) {
		p := x.(map[string]any)
		if p["status"] != "revoked" {
			return p
		}
	}
	return nil
}

// Hub multitransporte por la API completa (gateway + auth + devices +
// wireguard + tunnel-agent en memoria; docs/tunnels.md): transporte IPsec,
// configuración con PSK de un solo uso, túnel en el agente, SA establecida →
// tunnel_up, paso a GRE por Internet con confirmación (misma /32), auditoría.
func TestTunnelTransportsE2E(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	pt := a.superadmin().platformToken()
	A := a.newISP(pt, "isp-a")
	ctx := context.Background()

	site := a.must(a.post(A.token, "/api/v1/sites", map[string]any{"name": "Nodo"}), 201).str("id")
	router := a.must(a.post(A.token, "/api/v1/routers", map[string]any{"site_id": site, "name": "rt-1", "routeros_version": "7.16"}), 201).str("id")
	transport := "/api/v1/routers/" + router + "/transport"
	config := "/api/v1/routers/" + router + "/tunnel-config"

	// WireGuard: la configuración IPsec/GRE no aplica.
	a.must(a.do(req{Method: "POST", Path: config, Token: A.token}), 422)
	// isp_viewer → 403.
	viewer := a.createUser("viewer@isp-a.test")
	a.grant(viewer, A.id, "viewer")
	vt := a.login("viewer@isp-a.test", userPassword).tenantToken(A.id)
	a.must(a.do(req{Method: "PUT", Path: transport, Token: vt, Body: map[string]any{"connection_mode": "ipsec", "tunnel_remote_address": "198.51.100.7"}}), 403)

	// 1) IPsec con la IP pública del router (identidad por defecto).
	r := a.must(a.do(req{Method: "PUT", Path: transport, Token: A.token, Body: map[string]any{"connection_mode": "ipsec",
		"tunnel_remote_address": "198.51.100.7"}}), 200)
	if r.str("connection_mode") != "ipsec" || r.str("protection_level") != "encrypted" || r.str("tunnel_remote_id") != "198.51.100.7" ||
		r.str("onboarding_state") != "pending_configuration" {
		t.Fatalf("transporte ipsec: %s", r.Raw)
	}
	if a.auditCount("devices.router.transport_changed") != 1 {
		t.Fatal("cambio de transporte sin auditar")
	}

	// 2) Configuración: PSK de 48 caracteres solo en la respuesta, sin caché; RouterOS + strongSwan.
	c := a.must(a.do(req{Method: "POST", Path: config, Token: A.token, Body: map[string]any{}}), 201)
	if c.Header.Get("Cache-Control") != "no-store" || len(c.str("psk")) != 48 || c.str("transport") != "ipsec" {
		t.Fatalf("configuración: %s", c.Raw)
	}
	cfgs := c.Body["configs"].([]any)
	ros := cfgs[0].(map[string]any)
	if len(cfgs) != 2 || ros["platform"] != "routeros" || !strings.Contains(ros["config"].(string), c.str("psk")) ||
		!strings.Contains(ros["config"].(string), "remote-id=address:203.0.113.10") {
		t.Fatalf("configs: %v", cfgs)
	}
	tunIP := strings.TrimSuffix(c.str("tunnel_address"), "/32")
	if a.auditCount("wireguard.tunnel_config.created") != 1 {
		t.Fatal("configuración sin auditar")
	}
	// La PSK está cifrada en devices.credential (kind ipsec_psk), nunca en claro.
	var n int
	if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM devices.credential WHERE router_id = $1 AND kind = 'ipsec_psk' AND deleted_at IS NULL
		AND position($2::bytea IN secret_ciphertext) = 0`, router, []byte(c.str("psk"))).Scan(&n); err != nil || n != 1 {
		t.Fatalf("psk en reposo: %d, %v", n, err)
	}

	// 3) El túnel llega a tunnel-agent (reporta su estado: sin SA todavía).
	var peer map[string]any
	testkit.Eventually(t, 10*time.Second, func() bool {
		peer = a.peerOf(A.token, router)
		tun, _ := peer["tunnel"].(map[string]any)
		return peer["transport"] == "ipsec" && peer["status"] == "pending_handshake" && tun != nil && tun["state"] == "down"
	}, "túnel ipsec en el agente")
	if peer["address"] != tunIP+"/32" {
		t.Fatalf("peer = %v", peer)
	}

	// 4) SA establecida (reporte del agente) → active, router tunnel_up.
	ctrl, ok := module.Lookup[tunapi.Control](a.services, tunapi.ServiceControl)
	if !ok {
		t.Fatal("sin TunnelControlService")
	}
	now := time.Now().UTC()
	ep := "198.51.100.7:4500"
	rep := &tunnelv1.ReportTunnelStatusRequest{AppliedVersion: 1 << 40, ObservedAt: timestamppb.New(now), IkeDaemonUp: true,
		Tunnels: []*tunnelv1.ObservedTunnel{{PeerId: peer["id"].(string), Transport: "ipsec", State: "established", Alive: true,
			EstablishedAt: timestamppb.New(now), LastRekeyAt: timestamppb.New(now), Endpoint: &ep}}}
	if _, err := ctrl.ReportTunnelStatus(ctx, rep); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, 5*time.Second, func() bool {
		return a.must(a.do(req{Method: "GET", Path: "/api/v1/routers/" + router, Token: A.token}), 200).str("onboarding_state") == "tunnel_up"
	}, "router tunnel_up")
	if p := a.peerOf(A.token, router); p["status"] != "active" || p["endpoint"] != ep {
		t.Fatalf("peer activo: %v", p)
	}

	// 5) GRE por Internet: sin confirmación 422; con confirmación, mismo /32 y aviso permanente.
	gre := map[string]any{"connection_mode": "gre", "tunnel_remote_address": "198.51.100.7", "path": "internet"}
	if r := a.do(req{Method: "PUT", Path: transport, Token: A.token, Body: gre}); r.Status != 422 || r.code() != "UNENCRYPTED_TRANSPORT_CONFIRMATION_REQUIRED" {
		t.Fatalf("sin confirmación: %d %s", r.Status, r.Raw)
	}
	gre["confirm_unencrypted_internet"] = true
	r = a.must(a.do(req{Method: "PUT", Path: transport, Token: A.token, Body: gre}), 200)
	if r.str("protection_level") != "unencrypted_internet" || r.str("tunnel_address") != tunIP {
		t.Fatalf("gre: %s", r.Raw)
	}
	c = a.must(a.do(req{Method: "POST", Path: config, Token: A.token}), 201)
	if c.Body["psk"] != nil || c.str("tunnel_address") != tunIP+"/32" || len(c.Body["warnings"].([]any)) == 0 {
		t.Fatalf("configuración gre: %s", c.Raw)
	}
	if strings.Contains(string(c.Raw), "key=") {
		t.Fatal("RouterOS no admite clave GRE")
	}
	testkit.Eventually(t, 10*time.Second, func() bool {
		p := a.peerOf(A.token, router)
		tun, _ := p["tunnel"].(map[string]any)
		return p["transport"] == "gre" && p["address"] == tunIP+"/32" && tun != nil && tun["state"] == "up"
	}, "túnel gre en el agente con la misma /32")
	if a.auditCount("devices.router.transport_changed") != 2 {
		t.Fatal("paso a GRE sin auditar")
	}
	// Otro router no puede usar la misma IP pública por GRE.
	r2 := a.must(a.post(A.token, "/api/v1/routers", map[string]any{"site_id": site, "name": "rt-2", "is_primary": false}), 201).str("id")
	if r := a.do(req{Method: "PUT", Path: "/api/v1/routers/" + r2 + "/transport", Token: A.token, Body: map[string]any{"connection_mode": "gre",
		"tunnel_remote_address": "198.51.100.7", "path": "private"}}); r.Status != 409 || r.code() != "TUNNEL_REMOTE_IN_USE" {
		t.Fatalf("IP pública repetida: %d %s", r.Status, r.Raw)
	}
}
