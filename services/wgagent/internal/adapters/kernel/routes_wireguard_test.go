//go:build wireguard

package kernel

import (
	"context"
	"net/netip"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// tunInterface crea una interfaz tun (basta para rutas; no hace falta
// WireGuard). Requiere root o CAP_NET_ADMIN.
func tunInterface(t *testing.T, name string) {
	t.Helper()
	if out, err := exec.Command("ip", "tuntap", "add", name, "mode", "tun").CombinedOutput(); err != nil {
		t.Skipf("ip tuntap add: %v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("ip", "link", "del", name).Run() })
	if out, err := exec.Command("ip", "link", "set", name, "up").CombinedOutput(); err != nil {
		t.Fatalf("ip link set up: %v %s", err, out)
	}
}

func routesOn(t *testing.T, name, proto string) []string {
	t.Helper()
	out, err := exec.Command("ip", "-4", "route", "show", "dev", name, "proto", proto).CombinedOutput()
	if err != nil {
		t.Fatalf("ip route show: %v %s", err, out)
	}
	var dst []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if f := strings.Fields(l); len(f) > 0 {
			dst = append(dst, f[0])
		}
	}
	slices.Sort(dst)
	return dst
}

// Un rango añadido desde la consola queda enrutado por la interfaz: una ruta
// por allowed-ip, idempotente; las rutas de peers que ya no están se borran
// (también las de un arranque anterior) y las del despliegue no se tocan.
func TestSyncRoutes(t *testing.T) {
	const name = "hfrttest0"
	tunInterface(t, name)
	// Ruta del despliegue (horus-tunnel: proto boot) que el agente no toca.
	if out, err := exec.Command("ip", "route", "replace", "10.255.0.0/16", "dev", name).CombinedOutput(); err != nil {
		t.Fatalf("ip route replace: %v %s", err, out)
	}
	ctx := context.Background()
	a, b := netip.MustParsePrefix("10.255.1.2/32"), netip.MustParsePrefix("10.254.0.1/32")
	d := &Device{name: name}
	if err := d.SyncRoutes(ctx, []netip.Prefix{a, b}); err != nil {
		t.Fatal(err)
	}
	if err := d.SyncRoutes(ctx, []netip.Prefix{a, b}); err != nil { // idempotente
		t.Fatal(err)
	}
	if got := routesOn(t, name, "104"); !slices.Equal(got, []string{"10.254.0.1", "10.255.1.2"}) {
		t.Fatalf("rutas del agente = %v", got)
	}
	// Reinicio: un Device nuevo reconoce sus rutas y borra la que sobra.
	d2 := &Device{name: name}
	if err := d2.SyncRoutes(ctx, []netip.Prefix{a}); err != nil {
		t.Fatal(err)
	}
	if got := routesOn(t, name, "104"); !slices.Equal(got, []string{"10.255.1.2"}) {
		t.Fatalf("rutas tras reiniciar = %v", got)
	}
	if err := d2.SyncRoutes(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if got := routesOn(t, name, "104"); len(got) != 0 {
		t.Fatalf("rutas sin peers = %v", got)
	}
	if got := routesOn(t, name, "boot"); !slices.Equal(got, []string{"10.255.0.0/16"}) {
		t.Fatalf("ruta del despliegue = %v", got)
	}
}
