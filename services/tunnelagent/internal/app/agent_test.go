package app_test

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/services/tunnelagent/api/tunnelv1"
	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/adapters/memdev"
	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/app"
	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/domain"
)

var testPSK = strings.Repeat("prueba", 8)

type ctrl struct {
	mu      sync.Mutex
	reports []*tunnelv1.ReportTunnelStatusRequest
	fail    bool
}

func (c *ctrl) ReportTunnelStatus(_ context.Context, r *tunnelv1.ReportTunnelStatusRequest) (*tunnelv1.ReportTunnelStatusResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return nil, errors.New("control caído")
	}
	c.reports = append(c.reports, r)
	return &tunnelv1.ReportTunnelStatusResponse{DesiredVersion: 1}, nil
}

func (c *ctrl) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.reports)
}

func desired(v int64, tunnels ...*tunnelv1.DesiredTunnel) *tunnelv1.ApplyTunnelsRequest {
	return &tunnelv1.ApplyTunnelsRequest{HubId: "hub-1", DesiredVersion: v, Tunnels: tunnels}
}

func ipsecT(idx uint32, addr string) *tunnelv1.DesiredTunnel {
	return &tunnelv1.DesiredTunnel{PeerId: "peer-" + addr, TenantId: "t", Transport: "ipsec", Index: idx, TunnelAddress: addr + "/32",
		HubTunnelAddress: "10.255.0.1", LocalAddress: "203.0.113.1", LocalId: "203.0.113.1", RemoteId: "rt-" + addr, Psk: testPSK}
}

func greT(idx uint32, addr, remote string) *tunnelv1.DesiredTunnel {
	return &tunnelv1.DesiredTunnel{PeerId: "peer-" + addr, TenantId: "t", Transport: "gre", Index: idx, TunnelAddress: addr + "/32",
		HubTunnelAddress: "10.255.0.1", LocalAddress: "203.0.113.1", RemoteAddress: remote, GreKey: idx}
}

// Switches vía router (ADR-0032): las direcciones Horus de los equipos se
// ligan a la interfaz del router (antisuplantación) y un cambio en la lista
// reaplica el túnel.
func TestApplyTunnelsRouted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	host, ike := memdev.NewHost(), memdev.NewIKE()
	a := app.New(app.Options{Links: host, IKE: ike})
	g := greT(2, "10.255.1.3", "198.51.100.7")
	g.RoutedPrefixes = []string{"10.255.1.40/32"}
	if _, err := a.ApplyTunnels(ctx, desired(1, ipsecT(1, "10.255.1.2"), g)); err != nil {
		t.Fatal(err)
	}
	if len(host.Bindings) != 3 || host.Bindings[2].Prefix.String() != "10.255.1.40/32" || host.Bindings[2].Interface != "hgre2" ||
		len(host.Links["hgre2"].Routed) != 1 {
		t.Fatalf("ligaduras = %v, túnel = %+v", host.Bindings, host.Links["hgre2"])
	}
	g.RoutedPrefixes = []string{"10.255.1.40/32", "10.255.2.0/28"}
	r, err := a.ApplyTunnels(ctx, desired(2, ipsecT(1, "10.255.1.2"), g))
	if err != nil || r.GetTunnelsAdded() != 1 || len(host.Links["hgre2"].Routed) != 2 || len(host.Bindings) != 4 {
		t.Fatalf("cambio de direcciones = %v %v, ligaduras %v", r, err, host.Bindings)
	}
	g.RoutedPrefixes = nil
	if _, err := a.ApplyTunnels(ctx, desired(3, ipsecT(1, "10.255.1.2"), g)); err != nil || len(host.Bindings) != 2 {
		t.Fatalf("baja del switch = %v, ligaduras %v", err, host.Bindings)
	}
}

func TestApplyTunnels(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	host, ike := memdev.NewHost(), memdev.NewIKE()
	a := app.New(app.Options{Links: host, IKE: ike})
	r, err := a.ApplyTunnels(ctx, desired(1, ipsecT(1, "10.255.1.2"), greT(2, "10.255.1.3", "198.51.100.7")))
	if err != nil || r.GetAppliedVersion() != 1 || r.GetTunnelsAdded() != 2 {
		t.Fatalf("apply = %v, %v", r, err)
	}
	if _, ok := host.Links["hxfrm1"]; !ok || len(host.Links) != 2 || len(ike.Loaded) != 1 || ike.Loaded[1].PSK != testPSK {
		t.Fatalf("host = %+v, ike = %+v", host.Links, ike.Loaded)
	}
	if host.Links["hxfrm1"].PSK != "" {
		t.Fatal("la interfaz no guarda la PSK")
	}
	if len(host.GREPeers) != 1 || host.GREPeers[0] != netip.MustParseAddr("198.51.100.7") || len(host.Bindings) != 2 ||
		host.Bindings[0].Interface != "hxfrm1" {
		t.Fatalf("filtros = %v %v", host.GREPeers, host.Bindings)
	}
	ensures := host.Ensures
	// Idempotente: mismo estado, nada cambia ni se corta.
	if r, _ := a.ApplyTunnels(ctx, desired(1, ipsecT(1, "10.255.1.2"), greT(2, "10.255.1.3", "198.51.100.7"))); r.GetTunnelsAdded() != 0 ||
		host.Ensures != ensures {
		t.Fatalf("reaplicar = %v (ensures %d → %d)", r, ensures, host.Ensures)
	}
	// El túnel 1 pasa de IPsec a GRE: la conexión IKE se descarga.
	if _, err := a.ApplyTunnels(ctx, desired(2, greT(1, "10.255.1.2", "198.51.100.9"), greT(2, "10.255.1.3", "198.51.100.7"))); err != nil {
		t.Fatal(err)
	}
	if _, ok := host.Links["hgre1"]; !ok || len(ike.Loaded) != 0 {
		t.Fatalf("cambio de transporte: host %v ike %v", host.Links, ike.Loaded)
	}
	if _, ok := host.Links["hxfrm1"]; ok {
		t.Fatal("hxfrm1 sigue")
	}
	// Versión vieja: ignorada. Otro hub: rechazado. Estado inválido: no toca nada.
	if r, _ := a.ApplyTunnels(ctx, desired(1)); r.GetAppliedVersion() != 2 || len(host.Links) != 2 {
		t.Fatalf("versión vieja aplicada: %v", r)
	}
	other := desired(3)
	other.HubId = "hub-2"
	if _, err := a.ApplyTunnels(ctx, other); !errors.Is(err, app.ErrWrongHub) {
		t.Fatalf("otro hub: %v", err)
	}
	bad := desired(3, greT(1, "10.255.1.2", ""))
	if _, err := a.ApplyTunnels(ctx, bad); !errors.Is(err, domain.ErrInvalidState) || len(host.Links) != 2 {
		t.Fatalf("inválido: %v (%d interfaces)", err, len(host.Links))
	}
}

// Demonio IKE caído: los túneles GRE se aplican, la versión no avanza y el
// control reintenta; al volver el demonio se cargan las conexiones.
func TestIKEDownAndHeal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	host, ike := memdev.NewHost(), memdev.NewIKE()
	ike.Down = true
	now := time.Unix(1_800_000_000, 0).UTC()
	a := app.New(app.Options{Links: host, IKE: ike, Now: func() time.Time { return now }})
	if _, err := a.ApplyTunnels(ctx, desired(1, ipsecT(1, "10.255.1.2"), greT(2, "10.255.1.3", "198.51.100.7"))); err == nil {
		t.Fatal("sin error con el demonio caído")
	}
	if _, ok := host.Links["hgre2"]; !ok || a.AppliedVersion() != 0 {
		t.Fatalf("gre no aplicado o versión avanzada: %v %d", host.Links, a.AppliedVersion())
	}
	st := a.Status(ctx)
	if st.GetIkeDaemonUp() || st.GetTunnels()[0].GetError() == "" {
		t.Fatalf("estado = %v", st)
	}
	ike.Down = false
	if _, err := a.ApplyTunnels(ctx, desired(1, ipsecT(1, "10.255.1.2"), greT(2, "10.255.1.3", "198.51.100.7"))); err != nil {
		t.Fatal(err)
	}
	// El demonio se reinicia y pierde sus conexiones: el agente las recarga
	// en su ciclo (heal) sin esperar al control.
	ike.Restart()
	c := &ctrl{}
	a.SetControl(c)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = a.Run(runCtx); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for c.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if _, ok := ike.Loaded[1]; !ok {
		t.Fatal("conexión no recargada tras reiniciar el demonio")
	}
}

func TestStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	host, ike := memdev.NewHost(), memdev.NewIKE()
	now := time.Unix(1_800_000_000, 0).UTC()
	a := app.New(app.Options{Links: host, IKE: ike, HubID: "hub-1", Now: func() time.Time { return now }})
	if _, err := a.ApplyTunnels(ctx, desired(4, ipsecT(1, "10.255.1.2"), greT(2, "10.255.1.3", "198.51.100.7"))); err != nil {
		t.Fatal(err)
	}
	st := a.Status(ctx)
	if !st.GetIkeDaemonUp() || st.GetAppliedVersion() != 4 || len(st.GetTunnels()) != 2 {
		t.Fatalf("estado = %v", st)
	}
	for _, ot := range st.GetTunnels() {
		if ot.GetAlive() {
			t.Fatalf("vivo sin SA ni paquetes: %v", ot)
		}
	}
	ike.Establish(1, now.Add(-time.Minute), "198.51.100.20:4500")
	host.Count("hgre2", 3, 300)
	st = a.Status(ctx)
	ip, g := st.GetTunnels()[0], st.GetTunnels()[1]
	if !ip.GetAlive() || ip.GetState() != "established" || ip.GetEndpoint() != "198.51.100.20:4500" ||
		!ip.GetEstablishedAt().AsTime().Equal(now.Add(-time.Minute)) {
		t.Fatalf("ipsec = %v", ip)
	}
	if !g.GetAlive() || !g.GetLastPacketAt().AsTime().Equal(now) || g.GetRxBytes() != 300 {
		t.Fatalf("gre = %v", g)
	}
	// Sin paquetes nuevos en 181 s: GRE deja de estar vivo.
	now = now.Add(181 * time.Second)
	if g := a.Status(ctx).GetTunnels()[1]; g.GetAlive() {
		t.Fatalf("gre sigue vivo: %v", g)
	}
	// Reporte con el control caído: error, nada cambia (fail-static).
	c := &ctrl{fail: true}
	a.SetControl(c)
	if err := a.Report(ctx); err == nil || len(host.Links) != 2 {
		t.Fatal("fail-static roto")
	}
}

// Reinicio del agente: nada se borra hasta recibir un estado completo.
func TestRestartIsFailStatic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	host, ike := memdev.NewHost(), memdev.NewIKE()
	a := app.New(app.Options{Links: host, IKE: ike, HubID: "hub-1"})
	if _, err := a.ApplyTunnels(ctx, desired(5, ipsecT(1, "10.255.1.2"), greT(2, "10.255.1.3", "198.51.100.7"))); err != nil {
		t.Fatal(err)
	}
	a2 := app.New(app.Options{Links: host, IKE: ike, HubID: "hub-1"})
	if st := a2.Status(ctx); st.GetAppliedVersion() != 0 || len(host.Links) != 2 || len(ike.Loaded) != 1 {
		t.Fatalf("reinicio tocó el host: %v", st)
	}
	if r, err := a2.ApplyTunnels(ctx, desired(5, ipsecT(1, "10.255.1.2"), greT(2, "10.255.1.3", "198.51.100.7"))); err != nil ||
		r.GetAppliedVersion() != 5 || r.GetTunnelsRemoved() != 0 {
		t.Fatalf("reconstrucción = %v, %v", r, err)
	}
}
