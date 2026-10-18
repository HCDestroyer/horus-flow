//go:build ipsec

package vici_test

import (
	"context"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	govici "github.com/strongswan/govici/vici"

	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/adapters/vici"
	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/domain"
)

// TestIKEv2AgainstStrongSwan negocia IKEv2 de verdad: el hub es charon
// (imagen horus-ipsec) configurado SOLO por este adaptador (load-shared +
// load-conn por vici, if_id del túnel) y el "router" es otro charon con
// swanctl. Lo orquesta tests/tunnels/ipsec-e2e.sh:
//
//	HORUS_TEST_VICI_SOCKET=<socket vici del hub> HORUS_TEST_ROUTER_CONTAINER=<contenedor> \
//	  go test -tags=ipsec ./services/tunnelagent/internal/adapters/vici/
func TestIKEv2AgainstStrongSwan(t *testing.T) {
	// HORUS_TEST_ROUTER_CONTAINER vacío: el router inicia solo (CHR, scripts/lab/tunnels.sh) y el test
	// espera la SA hasta HORUS_TEST_SA_TIMEOUT.
	sock, router := os.Getenv("HORUS_TEST_VICI_SOCKET"), os.Getenv("HORUS_TEST_ROUTER_CONTAINER")
	if sock == "" {
		t.Skip("HORUS_TEST_VICI_SOCKET sin definir (tests/tunnels/ipsec-e2e.sh, scripts/lab/tunnels.sh)")
	}
	remoteID := os.Getenv("HORUS_TEST_ROUTER_ID")
	if remoteID == "" {
		remoteID = "fqdn:rt-centro.isp.example"
	}
	timeout := 30 * time.Second
	if v, err := time.ParseDuration(os.Getenv("HORUS_TEST_SA_TIMEOUT")); err == nil {
		timeout = v
	}
	ctx := context.Background()
	d := vici.New(sock)
	tun := domain.Tunnel{Transport: domain.TransportIPsec, Index: 7, TunnelAddress: netip.MustParseAddr("10.255.0.17"),
		HubAddress: netip.MustParseAddr("10.255.0.1"), Remote: netip.MustParseAddr(os.Getenv("HORUS_TEST_ROUTER_IP")),
		LocalID: os.Getenv("HORUS_TEST_HUB_ID"), RemoteID: remoteID, PSK: os.Getenv("HORUS_TEST_PSK"), MTU: 1400}
	if err := d.Load(ctx, tun); err != nil {
		t.Fatalf("load: %v", err)
	}
	conns, err := d.Conns(ctx)
	if err != nil || len(conns) != 1 || conns[0] != 7 {
		t.Fatalf("conns = %v, %v", conns, err)
	}
	// Sin ESP en el kernel (sandbox/CI sin esp4) la CHILD SA no se instala: HORUS_TEST_EXPECT_CHILD=0 comprueba
	// solo IKE (propuesta, PSK, identidades) y la selección de la propuesta ESP.
	expectChild := os.Getenv("HORUS_TEST_EXPECT_CHILD") != "0"
	// Registro del hub por vici (evento "log") para ver la propuesta ESP elegida.
	logs, err := govici.NewSession(govici.WithSocketPath(sock))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logs.Close() }()
	if err := logs.Subscribe("log"); err != nil {
		t.Fatal(err)
	}
	events := make(chan govici.Event, 256)
	logs.NotifyEvents(events)
	if router != "" {
		out, err := exec.CommandContext(ctx, "docker", "exec", "-u", "0:1430", router, "swanctl", "--initiate", "--child", "horus", "--timeout", "30",
			"--uri", "unix:///run/horus-ipsec/charon.vici").CombinedOutput()
		if err != nil && expectChild {
			t.Fatalf("initiate: %v\n%s", err, out)
		}
		t.Logf("router:\n%s", out)
	}
	deadline := time.Now().Add(timeout)
	for {
		sas, err := d.SAs(ctx)
		if err != nil {
			t.Fatal(err)
		}
		sa, ok := sas[7]
		if ok && sa.State == "established" && (sa.ChildInstalled || !expectChild) {
			t.Logf("IKE SA establecida desde %s (CHILD SA instalada: %v)", sa.Endpoint, sa.ChildInstalled)
			if !strings.HasPrefix(sa.Endpoint, os.Getenv("HORUS_TEST_ROUTER_IP")) {
				t.Fatalf("endpoint = %s", sa.Endpoint)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sin SA tras %s: %+v", timeout, sas)
		}
		time.Sleep(500 * time.Millisecond)
	}
	// La CHILD SA del hub lleva el if_id del túnel (route-based). Con HORUS_TEST_HUB_CONTAINER se mira
	// dentro del contenedor del hub (su red); si no, en la red del host (hub con network_mode host).
	if !expectChild {
		var hub []string
		for len(events) > 0 {
			ev := <-events
			if msg, ok := ev.Message.Get("msg").(string); ok {
				hub = append(hub, msg)
			}
		}
		joined := strings.Join(hub, "\n")
		if !strings.Contains(joined, "selected proposal: ESP:") {
			t.Fatalf("el hub no eligió propuesta ESP:\n%s", joined)
		}
		for _, l := range hub {
			if strings.Contains(l, "selected proposal") {
				t.Logf("hub: %s", l)
			}
		}
	} else if out, err := xfrmState(ctx); err != nil || !strings.Contains(string(out), "if_id 0x7") {
		t.Fatalf("SA sin if_id 7: %v\n%s", err, out)
	}
	if err := d.Unload(ctx, 7); err != nil {
		t.Fatalf("unload: %v", err)
	}
	if conns, _ := d.Conns(ctx); len(conns) != 0 {
		t.Fatalf("conexión sin descargar: %v", conns)
	}
	sas, _ := d.SAs(ctx)
	if _, ok := sas[7]; ok {
		t.Fatalf("SA sin cerrar tras unload: %+v", sas)
	}
}

func xfrmState(ctx context.Context) ([]byte, error) {
	if hub := os.Getenv("HORUS_TEST_HUB_CONTAINER"); hub != "" {
		return exec.CommandContext(ctx, "docker", "exec", hub, "ip", "xfrm", "state").CombinedOutput() //nolint:gosec // prueba manual
	}
	return exec.CommandContext(ctx, "ip", "xfrm", "state").CombinedOutput()
}
