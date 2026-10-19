//go:build wireguard

// Camino de datos de los switches vía router (ADR-0032) en espacios de red: el hub (este proceso, interfaz
// WireGuard gestionada por el wg-agent REAL con su adaptador de kernel/UAPI) y dos "routers" de DOS ISP, cada uno
// en su netns con su propio WireGuard y un "switch" detrás con la MISMA IP privada 192.168.88.2. Cada router hace
// el NAT 1:1 que genera Horus (iptables DNAT/SNAT /32 = netmap de RouterOS) entre la dirección Horus de su switch
// y la IP privada, solo con la red de servicios. Se comprueba:
//   - SNMP (UDP 161) del hub a cada dirección Horus llega a SU switch (que ve como origen la IP de servicios del
//     hub) y la respuesta vuelve con la dirección Horus como origen;
//   - el sFlow (UDP 6343) de cada switch llega al hub con la dirección Horus de ese switch;
//   - el router de un ISP no puede suplantar la dirección Horus del otro (WireGuard lo descarta: allowed-ips).
//
//	sudo go test -tags=wireguard -run TestViaRouterDataPath ./services/wgagent/internal/adapters/kernel/
package kernel

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/hcdestroyer/horus-flow/services/wgagent/api/agentv1"
	"github.com/hcdestroyer/horus-flow/services/wgagent/internal/app"
)

func run(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput() //nolint:gosec // prueba con root
	if err != nil {
		t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func nsRun(t *testing.T, ns string, args ...string) string {
	t.Helper()
	return run(t, append([]string{"ip", "netns", "exec", ns}, args...)...)
}

// wgInNS levanta wireguard-go dentro del netns (el socket UAPI queda en /var/run/wireguard).
func wgInNS(t *testing.T, ns, name string) {
	t.Helper()
	cmd := exec.Command("ip", "netns", "exec", ns, "wireguard-go", "-f", name) //nolint:gosec // prueba con root
	cmd.Env = append(os.Environ(), "WG_I_PREFER_BUGGY_USERSPACE_TO_POLISHED_KMOD=1")
	if err := cmd.Start(); err != nil {
		t.Skipf("wireguard-go en netns: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = os.Remove("/var/run/wireguard/" + name + ".sock")
	})
	for i := 0; i < 100; i++ {
		if _, err := os.Stat("/var/run/wireguard/" + name + ".sock"); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Skip("wireguard-go no creó el socket UAPI en el netns")
}

// udpServer: en el netns del switch responde "<origen visto>" en el puerto 161.
func udpServer(t *testing.T, ns string) {
	t.Helper()
	cmd := exec.Command("ip", "netns", "exec", ns, "python3", "-I", "-c", //nolint:gosec // prueba con root
		"import socket\ns=socket.socket(socket.AF_INET,socket.SOCK_DGRAM)\ns.bind(('0.0.0.0',161))\nwhile True:\n d,a=s.recvfrom(2048)\n s.sendto(a[0].encode(),a)\n")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
}

func TestViaRouterDataPath(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requiere root (netns, WireGuard, iptables)")
	}
	for _, b := range []string{"wireguard-go", "iptables", "python3"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skipf("falta %s", b)
		}
	}
	const (
		hubIf    = "hfvia0"
		hubIP    = "10.254.0.1"
		services = "10.254.0.0/24"
		private  = "192.168.88.2"
	)
	upInterface(t, hubIf)
	run(t, "ip", "addr", "add", hubIP+"/24", "dev", hubIf)
	run(t, "ip", "link", "set", hubIf, "up")
	dev, err := Open(hubIf)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dev.Close() }()
	hubKey, _ := wgtypes.GeneratePrivateKey()

	type site struct {
		ns, sw, wg     string
		tunnel, horus  string
		underHub, unde string
		key            wgtypes.Key
	}
	sites := []*site{
		{ns: "hfvia-r1", sw: "hfvia-s1", wg: "hfviaw1", tunnel: "10.254.9.2", horus: "10.254.9.40", underHub: "172.31.91.1", unde: "172.31.91.2"},
		{ns: "hfvia-r2", sw: "hfvia-s2", wg: "hfviaw2", tunnel: "10.254.9.3", horus: "10.254.9.41", underHub: "172.31.92.1", unde: "172.31.92.2"},
	}
	for i, s := range sites {
		s.key, _ = wgtypes.GeneratePrivateKey()
		for _, ns := range []string{s.ns, s.sw} {
			_ = exec.Command("ip", "netns", "del", ns).Run()
			run(t, "ip", "netns", "add", ns)
			nsName := ns
			t.Cleanup(func() { _ = exec.Command("ip", "netns", "del", nsName).Run() })
		}
		// Subyacente hub ↔ router.
		hv, rv := fmt.Sprintf("hfviah%d", i+1), fmt.Sprintf("hfviau%d", i+1)
		_ = exec.Command("ip", "link", "del", hv).Run()
		run(t, "ip", "link", "add", hv, "type", "veth", "peer", "name", rv, "netns", s.ns)
		hvName := hv
		t.Cleanup(func() { _ = exec.Command("ip", "link", "del", hvName).Run() })
		run(t, "ip", "addr", "add", s.underHub+"/30", "dev", hv)
		run(t, "ip", "link", "set", hv, "up")
		nsRun(t, s.ns, "ip", "addr", "add", s.unde+"/30", "dev", rv)
		nsRun(t, s.ns, "ip", "link", "set", rv, "up")
		nsRun(t, s.ns, "ip", "link", "set", "lo", "up")
		// LAN de gestión del sitio: la MISMA red en los dos ISP.
		nsRun(t, s.ns, "ip", "link", "add", "lan", "type", "veth", "peer", "name", "eth0", "netns", s.sw)
		nsRun(t, s.ns, "ip", "addr", "add", "192.168.88.1/24", "dev", "lan")
		nsRun(t, s.ns, "ip", "link", "set", "lan", "up")
		nsRun(t, s.sw, "ip", "addr", "add", private+"/24", "dev", "eth0")
		nsRun(t, s.sw, "ip", "link", "set", "eth0", "up")
		nsRun(t, s.sw, "ip", "link", "set", "lo", "up")
		nsRun(t, s.sw, "ip", "route", "add", "default", "via", "192.168.88.1")
		nsRun(t, s.ns, "sysctl", "-qw", "net.ipv4.ip_forward=1")
		// WireGuard del router (como wg-horus: allowed-address = red de servicios, ruta por el túnel).
		wgInNS(t, s.ns, s.wg)
		kf, err := os.CreateTemp(t.TempDir(), "k")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = kf.WriteString(s.key.String())
		_ = kf.Close()
		nsRun(t, s.ns, "wg", "set", s.wg, "private-key", kf.Name(), "peer", hubKey.PublicKey().String(),
			"endpoint", s.underHub+":51998", "allowed-ips", services, "persistent-keepalive", "5")
		nsRun(t, s.ns, "ip", "addr", "add", s.tunnel+"/32", "dev", s.wg)
		nsRun(t, s.ns, "ip", "link", "set", s.wg, "up")
		nsRun(t, s.ns, "ip", "route", "add", services, "dev", s.wg)
		// NAT 1:1 que genera Horus (netmap /32), solo con la red de servicios.
		nsRun(t, s.ns, "iptables", "-t", "nat", "-A", "PREROUTING", "-i", s.wg, "-d", s.horus, "-j", "DNAT", "--to-destination", private)
		nsRun(t, s.ns, "iptables", "-t", "nat", "-A", "POSTROUTING", "-o", s.wg, "-s", private, "-d", services, "-j", "SNAT", "--to-source", s.horus)
	}

	// Estado deseado del hub (lo que empuja wireguard): cada router con su /32 de túnel y la dirección Horus de su switch.
	a := app.New(app.Options{Device: dev, HubID: "hub", PrivateKey: hubKey.String(), PublicKey: hubKey.PublicKey().String(), ListenPort: 51998})
	req := &agentv1.ApplyDesiredStateRequest{HubId: "hub", DesiredVersion: 1}
	for i, s := range sites {
		req.Peers = append(req.Peers, &agentv1.DesiredPeer{PeerId: fmt.Sprintf("p%d", i+1), PublicKey: s.key.PublicKey().String(),
			AllowedIps: []string{s.tunnel + "/32", s.horus + "/32"}, PersistentKeepaliveSeconds: 5})
	}
	if _, err := a.ApplyDesiredState(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	// Una IP privada repetida nunca llega al hub: el estado con 192.168.88.2 en dos peers se rechaza entero.
	bad := &agentv1.ApplyDesiredStateRequest{HubId: "hub", DesiredVersion: 2}
	for i, s := range sites {
		bad.Peers = append(bad.Peers, &agentv1.DesiredPeer{PeerId: fmt.Sprintf("p%d", i+1), PublicKey: s.key.PublicKey().String(),
			AllowedIps: []string{s.tunnel + "/32", private + "/32"}})
	}
	if _, err := a.ApplyDesiredState(context.Background(), bad); err == nil {
		t.Fatal("estado con la misma IP privada en dos peers aceptado")
	}
	if got := routesOn(t, hubIf, "104"); len(got) != 4 {
		t.Fatalf("rutas del hub = %v", got)
	}
	for _, s := range sites {
		udpServer(t, s.sw)
	}

	// SNMP: cada dirección Horus llega a SU switch; la respuesta vuelve desde la dirección Horus.
	for _, s := range sites {
		var seen, from string
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) && from == "" {
			seen, from = query(t, hubIP, s.horus)
		}
		if seen != hubIP || from != s.horus {
			t.Fatalf("SNMP a %s: el switch vio %q y respondió %q", s.horus, seen, from)
		}
	}
	// sFlow: cada switch envía al colector; el hub lo ve con la dirección Horus de ESE switch.
	for _, s := range sites {
		if got := sflowFrom(t, hubIP, s.sw); got != s.horus {
			t.Fatalf("sFlow del switch de %s llegó desde %q (quiero %s)", s.ns, got, s.horus)
		}
	}
	// Suplantación: el router 2 intenta usar la dirección Horus del switch 1 → WireGuard lo descarta.
	r2 := sites[1]
	nsRun(t, r2.ns, "iptables", "-t", "nat", "-I", "POSTROUTING", "1", "-o", r2.wg, "-s", private, "-d", services, "-j", "SNAT",
		"--to-source", sites[0].horus)
	if got := sflowFrom(t, hubIP, r2.sw); got != "" {
		t.Fatalf("el router de otro ISP suplantó la dirección Horus del switch 1: llegó desde %q", got)
	}
}

// query envía un "SNMP" desde la IP de servicios del hub y devuelve lo que vio el switch y el origen de la respuesta.
func query(t *testing.T, src, dst string) (seen, from string) {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(src)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.WriteToUDP([]byte("horus"), &net.UDPAddr{IP: net.ParseIP(dst), Port: 161}); err != nil {
		return "", ""
	}
	buf := make([]byte, 256)
	n, addr, err := c.ReadFromUDP(buf)
	if err != nil {
		return "", ""
	}
	return string(buf[:n]), addr.IP.String()
}

// sflowFrom escucha en el colector y hace que el switch envíe un datagrama; devuelve el origen visto ("" si no llega).
func sflowFrom(t *testing.T, collector, swNS string) string {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(collector), Port: 6343})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	nsRun(t, swNS, "python3", "-I", "-c",
		fmt.Sprintf("import socket\nx=socket.socket(socket.AF_INET,socket.SOCK_DGRAM)\nx.sendto(b'sflow',('%s',6343))\n", collector))
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64)
	_, addr, err := c.ReadFromUDP(buf)
	if err != nil {
		return ""
	}
	return addr.IP.String()
}
