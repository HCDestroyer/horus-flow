//go:build wireguard

// Prueba contra una interfaz WireGuard real (I1-01 "Hecho cuando":
// make test-wireguard). Sin módulo de kernel se usa wireguard-go (espacio de
// usuario, socket UAPI). Requiere root o CAP_NET_ADMIN y /dev/net/tun:
//
//	sudo go test -tags=wireguard ./services/wgagent/internal/adapters/kernel/
//
// Si el kernel tiene el módulo, HORUS_TEST_WG_KERNEL=1 crea la interfaz con
// `ip link add … type wireguard` en lugar de wireguard-go.
package kernel

import (
	"context"
	"net/netip"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/hcdestroyer/horus-flow/services/wgagent/api/agentv1"
	"github.com/hcdestroyer/horus-flow/services/wgagent/internal/app"
)

func upInterface(t *testing.T, name string) {
	t.Helper()
	if os.Getenv("HORUS_TEST_WG_KERNEL") == "1" {
		if out, err := exec.Command("ip", "link", "add", name, "type", "wireguard").CombinedOutput(); err != nil {
			t.Skipf("ip link add: %v %s", err, out)
		}
		t.Cleanup(func() { _ = exec.Command("ip", "link", "del", name).Run() })
		return
	}
	bin, err := exec.LookPath("wireguard-go")
	if err != nil {
		t.Skip("wireguard-go no instalado")
	}
	cmd := exec.Command(bin, "-f", name)
	cmd.Env = append(os.Environ(), "WG_I_PREFER_BUGGY_USERSPACE_TO_POLISHED_KMOD=1")
	if err := cmd.Start(); err != nil {
		t.Skipf("wireguard-go: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = exec.Command("ip", "link", "del", name).Run()
		_ = os.Remove("/var/run/wireguard/" + name + ".sock")
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat("/var/run/wireguard/" + name + ".sock"); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Skip("wireguard-go no creó el socket UAPI (¿sin /dev/net/tun o sin NET_ADMIN?)")
}

func TestAgentOnRealInterface(t *testing.T) {
	const name = "hfwgtest0"
	upInterface(t, name)
	dev, err := Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dev.Close() }()
	hub, _ := wgtypes.GeneratePrivateKey()
	r1, _ := wgtypes.GeneratePrivateKey()
	r2, _ := wgtypes.GeneratePrivateKey()
	a := app.New(app.Options{Device: dev, HubID: "hub", PrivateKey: hub.String(), PublicKey: hub.PublicKey().String(), ListenPort: 51999})
	ctx := context.Background()
	req := &agentv1.ApplyDesiredStateRequest{HubId: "hub", DesiredVersion: 1, Peers: []*agentv1.DesiredPeer{
		{PeerId: "p1", PublicKey: r1.PublicKey().String(), AllowedIps: []string{"10.255.0.2/32"}, PersistentKeepaliveSeconds: 25},
		{PeerId: "p2", PublicKey: r2.PublicKey().String(), AllowedIps: []string{"10.255.0.3/32"}, PersistentKeepaliveSeconds: 25},
	}}
	start := time.Now()
	if _, err := a.ApplyDesiredState(ctx, req); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("alta del peer > 10 s")
	}
	peers, err := dev.Peers(ctx)
	if err != nil || len(peers) != 2 {
		t.Fatalf("peers = %v, %v", peers, err)
	}
	for _, p := range peers {
		if len(p.AllowedIPs) != 1 || !p.AllowedIPs[0].IsSingleIP() || !netip.MustParsePrefix("10.255.0.0/16").Contains(p.AllowedIPs[0].Addr()) {
			t.Fatalf("allowed-ips = %v", p.AllowedIPs)
		}
	}
	// Reinicio del agente (fail-static) y reconstrucción con el estado completo.
	a2 := app.New(app.Options{Device: dev, HubID: "hub", PrivateKey: hub.String(), ListenPort: 51999})
	req.DesiredVersion, req.Peers = 2, req.Peers[:1]
	if r, err := a2.ApplyDesiredState(ctx, req); err != nil || r.GetPeersRemoved() != 1 {
		t.Fatalf("reconstrucción = %v, %v", r, err)
	}
	if peers, _ := dev.Peers(ctx); len(peers) != 1 || peers[0].PublicKey != r1.PublicKey().String() {
		t.Fatalf("peers tras reconstruir = %v", peers)
	}
}
