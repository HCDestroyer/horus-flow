package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/services/wgagent/api/agentv1"
	"github.com/hcdestroyer/horus-flow/services/wgagent/internal/adapters/memdev"
	"github.com/hcdestroyer/horus-flow/services/wgagent/internal/app"
	"github.com/hcdestroyer/horus-flow/services/wgagent/internal/domain"
)

const (
	hubPriv = "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="
	k1      = "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg="
	k2      = "HIgo9xNzJMWLKASShiTqIybxZ0U3wGLiUeJ1PKf8ykw="
)

type ctrl struct {
	reports []*agentv1.ReportStatusRequest
	fail    bool
}

func (c *ctrl) ReportStatus(_ context.Context, r *agentv1.ReportStatusRequest) (*agentv1.ReportStatusResponse, error) {
	if c.fail {
		return nil, errors.New("control caído")
	}
	c.reports = append(c.reports, r)
	return &agentv1.ReportStatusResponse{DesiredVersion: 1}, nil
}

func desired(v int64, keys ...string) *agentv1.ApplyDesiredStateRequest {
	r := &agentv1.ApplyDesiredStateRequest{HubId: "hub-1", DesiredVersion: v}
	for i, k := range keys {
		r.Peers = append(r.Peers, &agentv1.DesiredPeer{PeerId: k[:4], TenantId: "t", PublicKey: k,
			AllowedIps: []string{[]string{"10.255.0.2/32", "10.255.0.3/32"}[i]}, PersistentKeepaliveSeconds: 25})
	}
	return r
}

func TestApplyDesiredState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dev := memdev.New()
	a := app.New(app.Options{Device: dev, PrivateKey: hubPriv, ListenPort: 51820})
	r, err := a.ApplyDesiredState(ctx, desired(1, k1, k2))
	if err != nil || r.GetAppliedVersion() != 1 || r.GetPeersAdded() != 2 {
		t.Fatalf("apply = %v, %v", r, err)
	}
	if dev.PrivateKey != hubPriv || dev.ListenPort != 51820 {
		t.Fatal("interfaz sin configurar")
	}
	// Idempotente: mismo estado → sin cambios.
	if r, _ := a.ApplyDesiredState(ctx, desired(1, k1, k2)); r.GetPeersAdded() != 0 || r.GetPeersRemoved() != 0 {
		t.Fatalf("reaplicar = %v", r)
	}
	// Nuevo estado sin k2 → lo quita.
	if r, _ := a.ApplyDesiredState(ctx, desired(2, k1)); r.GetPeersRemoved() != 1 || r.GetAppliedVersion() != 2 {
		t.Fatalf("quitar = %v", r)
	}
	// Versión vieja: ignorada.
	if r, _ := a.ApplyDesiredState(ctx, desired(1, k1, k2)); r.GetAppliedVersion() != 2 || r.GetPeersAdded() != 0 {
		t.Fatalf("versión vieja aplicada: %v", r)
	}
	// Otro hub.
	other := desired(3, k1)
	other.HubId = "hub-2"
	if _, err := a.ApplyDesiredState(ctx, other); !errors.Is(err, app.ErrWrongHub) {
		t.Fatalf("otro hub: %v", err)
	}
	// Estado inválido: se rechaza entero y no toca nada.
	bad := desired(3, k1)
	bad.Peers[0].AllowedIps = []string{"0.0.0.0/0"}
	if _, err := a.ApplyDesiredState(ctx, bad); !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("inválido: %v", err)
	}
	if peers, _ := dev.Peers(ctx); len(peers) != 1 {
		t.Fatalf("peers = %v", peers)
	}
}

// I1-01 criterio 6: reinicio del agente → no borra nada hasta recibir el
// estado deseado; con el control caído los peers siguen (fail-static).
func TestFailStaticAndRebuild(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dev := memdev.New()
	a := app.New(app.Options{Device: dev, PrivateKey: hubPriv, ListenPort: 51820, HubID: "hub-1"})
	if _, err := a.ApplyDesiredState(ctx, desired(5, k1, k2)); err != nil {
		t.Fatal(err)
	}
	// "Reinicio": nuevo agente sobre la misma interfaz.
	a2 := app.New(app.Options{Device: dev, PrivateKey: hubPriv, ListenPort: 51820, HubID: "hub-1"})
	c := &ctrl{fail: true}
	a2.SetControl(c)
	if err := a2.Report(ctx); err == nil {
		t.Fatal("reporte con control caído sin error")
	}
	if peers, _ := dev.Peers(ctx); len(peers) != 2 {
		t.Fatalf("fail-static roto: %d peers", len(peers))
	}
	c.fail = false
	dev.Handshake(k1, "198.51.100.7:40000", time.Unix(1_800_000_000, 0))
	if err := a2.Report(ctx); err != nil {
		t.Fatal(err)
	}
	rep := c.reports[0]
	if rep.GetAppliedVersion() != 0 || !rep.GetInterfaceUp() || len(rep.GetPeers()) != 2 {
		t.Fatalf("reporte = %v", rep)
	}
	var hs bool
	for _, p := range rep.GetPeers() {
		if p.GetPublicKey() == k1 && p.GetLastHandshakeAt().AsTime().Unix() == 1_800_000_000 && p.GetEndpoint() == "198.51.100.7:40000" {
			hs = true
		}
	}
	if !hs {
		t.Fatal("handshake no reportado")
	}
	// El control reenvía el estado completo: reconstruye sin cambios.
	if r, err := a2.ApplyDesiredState(ctx, desired(5, k1, k2)); err != nil || r.GetPeersAdded() != 0 || r.GetAppliedVersion() != 5 {
		t.Fatalf("reconstrucción = %v, %v", r, err)
	}
	// Interfaz caída: el reporte dice interface_up=false.
	dev.Fail = true
	if st := a2.Status(ctx); st.GetInterfaceUp() {
		t.Fatal("interfaz caída reportada como activa")
	}
}
