package tunnelcfg_test

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/tunnelcfg"
)

func viaValues(vendor, transport string, subnet, sflow bool) tunnelcfg.ViaValues {
	v := tunnelcfg.ViaValues{Vendor: vendor, Transport: transport, RouterName: "rt-centro", DeviceID: "0192f0d2-0000-7000-8000-0000000000aa",
		DeviceName: "sw-acceso-1", Target: netip.MustParseAddr("192.168.88.2"), Mapped: netip.MustParseAddr("10.255.1.40"),
		ServicesCIDR: netip.MustParsePrefix("10.255.0.0/24"), CollectorIP: netip.MustParseAddr("10.255.0.1"), CollectorPort: 6343, SFlow: sflow}
	if subnet {
		v.SubnetID, v.Subnet, v.Block = "0192f0d2-0000-7000-8000-0000000000bb", netip.MustParsePrefix("192.168.88.0/28"),
			netip.MustParsePrefix("10.255.1.16/28")
		v.Mapped = netip.MustParseAddr("10.255.1.18")
	}
	return v
}

// Configuración del router del sitio para un switch vía router (ADR-0032):
// NAT 1:1 entre la IP privada y la dirección Horus, solo con la red de
// servicios, y cortafuegos mínimo (SNMP y, con sFlow, UDP 6343), fijada por
// golden files para RouterOS (WireGuard, IPsec, GRE) y VRP (IPsec, GRE).
func TestViaRouterGolden(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		vendor, transport string
		subnet, sflow     bool
		file              string
	}{
		{"mikrotik", tunnelcfg.TransportWireGuard, false, false, "via-mikrotik-wireguard.rsc"},
		{"mikrotik", tunnelcfg.TransportIPsec, false, true, "via-mikrotik-ipsec.rsc"},
		{"mikrotik", tunnelcfg.TransportGRE, true, true, "via-mikrotik-gre-subnet.rsc"},
		{"mikrotik", tunnelcfg.TransportWireGuard, true, false, "via-mikrotik-wireguard-subnet.rsc"},
		{"huawei", tunnelcfg.TransportIPsec, false, true, "via-huawei-ipsec.vrp"},
		{"huawei", tunnelcfg.TransportGRE, true, false, "via-huawei-gre-subnet.vrp"},
	} {
		c, err := tunnelcfg.ViaRouter(viaValues(tc.vendor, tc.transport, tc.subnet, tc.sflow))
		if err != nil {
			t.Fatalf("%s: %v", tc.file, err)
		}
		golden(t, tc.file, c.Config)
		if strings.Contains(c.Config, "action=masquerade") || strings.Contains(c.Config, "192.168.88.0/24") {
			t.Errorf("%s: NAT más amplio de lo pedido", tc.file)
		}
		if tc.vendor == "mikrotik" && !strings.Contains(c.Config, "dst-address=10.255.0.0/24 action=netmap") {
			t.Errorf("%s: el srcnat debe limitarse a la red de servicios", tc.file)
		}
		if (tc.transport == tunnelcfg.TransportGRE) != (len(c.Warnings) > 0) {
			t.Errorf("%s: aviso de GRE = %v", tc.file, c.Warnings)
		}
		if tc.sflow != strings.Contains(c.Config, "6343") {
			t.Errorf("%s: sFlow abierto = %v, se pidió %v", tc.file, !tc.sflow, tc.sflow)
		}
	}
}

func TestViaRouterRejects(t *testing.T) {
	t.Parallel()
	bad := map[string]func(v *tunnelcfg.ViaValues){
		"vrp sin wireguard":    func(v *tunnelcfg.ViaValues) { v.Vendor, v.Transport = "huawei", tunnelcfg.TransportWireGuard },
		"transporte":           func(v *tunnelcfg.ViaValues) { v.Transport = "direct" },
		"id con comillas":      func(v *tunnelcfg.ViaValues) { v.DeviceID = `x" ; /system reset` },
		"privada en servicios": func(v *tunnelcfg.ViaValues) { v.Target = netip.MustParseAddr("10.255.0.9") },
		"ipv6":                 func(v *tunnelcfg.ViaValues) { v.Target = netip.MustParseAddr("fd00::2") },
		"fuera de la red": func(v *tunnelcfg.ViaValues) {
			*v = viaValues("mikrotik", "gre", true, false)
			v.Target = netip.MustParseAddr("192.168.89.2")
		},
		"bloque de otro tamaño": func(v *tunnelcfg.ViaValues) {
			*v = viaValues("mikrotik", "gre", true, false)
			v.Block = netip.MustParsePrefix("10.255.1.0/27")
		},
	}
	for name, mut := range bad {
		v := viaValues("mikrotik", tunnelcfg.TransportWireGuard, false, false)
		mut(&v)
		if _, err := tunnelcfg.ViaRouter(v); !errors.Is(err, tunnelcfg.ErrValue) {
			t.Errorf("%s: aceptado (%v)", name, err)
		}
	}
	// Un nombre con caracteres de script se sustituye, nunca se inserta.
	v := viaValues("mikrotik", tunnelcfg.TransportWireGuard, false, false)
	v.DeviceName = `sw"; /user add`
	c, err := tunnelcfg.ViaRouter(v)
	if err != nil || strings.Contains(c.Config, "/user add") {
		t.Fatalf("nombre inseguro: %v\n%s", err, c.Config)
	}
}

// TestEmitViaLab escribe la configuración del router del laboratorio CHR para
// un switch vía router (scripts/lab/via-router.sh) en HORUS_TUNNELCFG_EMIT_DIR:
// túnel WireGuard wg-horus, switch simulado 10.20.0.50 en la LAN del CHR,
// dirección Horus 10.255.3.40, red de servicios 10.255.0.0/24 y colector
// 10.255.0.1; via.rsc y su bloque «Deshacer» sin comentar (via-undo.rsc).
func TestEmitViaLab(t *testing.T) {
	dir := os.Getenv("HORUS_TUNNELCFG_EMIT_DIR")
	if dir == "" {
		t.Skip("HORUS_TUNNELCFG_EMIT_DIR sin definir (scripts/lab/via-router.sh)")
	}
	v := viaValues("mikrotik", envOr("HORUS_VIA_TRANSPORT", tunnelcfg.TransportWireGuard), os.Getenv("HORUS_VIA_SUBNET") != "", true)
	v.RouterName, v.DeviceName = "horus-lab-chr", "sw-lab"
	v.Target, v.Mapped = netip.MustParseAddr(envOr("HORUS_VIA_TARGET", "10.20.0.50")), netip.MustParseAddr(envOr("HORUS_VIA_MAPPED", "10.255.3.40"))
	if v.Block.IsValid() {
		v.Subnet, v.Block = netip.MustParsePrefix("10.20.0.48/28"), netip.MustParsePrefix("10.255.3.48/28")
		v.Mapped, _ = netip.AddrFromSlice([]byte{10, 255, 3, 48 + v.Target.As4()[3] - 48})
	}
	c, err := tunnelcfg.ViaRouter(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "via.rsc"), []byte(c.Config), 0o600); err != nil {
		t.Fatal(err)
	}
	var undo []string
	in := false
	for _, l := range strings.Split(c.Config, "\n") {
		if strings.HasPrefix(l, "# ----- Deshacer") {
			in = true
			continue
		}
		if in && strings.HasPrefix(l, "# /") {
			undo = append(undo, strings.TrimPrefix(l, "# "))
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "via-undo.rsc"), []byte(strings.Join(undo, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
