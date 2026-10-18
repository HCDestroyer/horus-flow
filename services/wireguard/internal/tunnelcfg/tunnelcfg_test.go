package tunnelcfg_test

import (
	"errors"
	"flag"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/tunnelcfg"
)

var update = flag.Bool("update", false, "regenerar los golden de testdata")

// Secretos de prueba de baja entropía (no son reales).
var (
	testPSK  = strings.Repeat("Prueba", 8)
	testSNMP = "PruebaSnmp1234"
)

func values(transport, vendor string) tunnelcfg.Values {
	v := tunnelcfg.Values{Transport: transport, Vendor: vendor, RouterID: "0192f0d2-0000-7000-8000-000000000001", RouterName: "rt-centro",
		TunnelAddress: netip.MustParseAddr("10.255.0.17"), ServicesCIDR: netip.MustParsePrefix("10.255.0.0/24"),
		CollectorIP: netip.MustParseAddr("10.255.0.1"), HubAddress: netip.MustParseAddr("203.0.113.10"), HubID: "203.0.113.10",
		RemoteAddress: netip.MustParseAddr("198.51.100.7"), RemoteID: "rt-centro.isp.example", PSK: testPSK, MTU: 1400,
		SNMPUser: "horus-00000001", SNMPAuthPass: testSNMP, SNMPPrivPass: testSNMP, NTPServer: "pool.ntp.org"}
	if vendor == "mikrotik" {
		v.APIUser, v.APIPassword = "horus", "PruebaApi12345"
	}
	if transport == tunnelcfg.TransportGRE {
		v.RemoteID, v.PSK = "", ""
		if vendor == "huawei" {
			v.GREKey = 7
		}
	}
	return v
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (go test -update)", path, err)
	}
	if string(want) != got {
		t.Fatalf("%s distinto del golden (go test ./services/wireguard/internal/tunnelcfg -update):\n%s", name, got)
	}
}

func TestBuildGolden(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ transport, vendor string }{
		{"ipsec", "mikrotik"}, {"gre", "mikrotik"}, {"ipsec", "huawei"}, {"gre", "huawei"},
	} {
		cfgs, err := tunnelcfg.Build(values(tc.transport, tc.vendor))
		if err != nil {
			t.Fatalf("%s/%s: %v", tc.vendor, tc.transport, err)
		}
		if len(cfgs) != 2 || cfgs[1].Platform != tunnelcfg.PlatformStrongSwan {
			t.Fatalf("plataformas = %+v", cfgs)
		}
		golden(t, tc.vendor+"-"+tc.transport+"."+map[string]string{"mikrotik": "rsc", "huawei": "vrp"}[tc.vendor], cfgs[0].Config)
		golden(t, "strongswan-"+tc.transport+"-"+tc.vendor+".sh", cfgs[1].Config)
		for _, c := range cfgs {
			if len(c.Placeholders) > 0 && c.Platform != tunnelcfg.PlatformVRP {
				t.Errorf("%s: marcadores inesperados %v", c.Platform, c.Placeholders)
			}
			if tc.transport == "gre" && strings.Contains(c.Config, testPSK) {
				t.Errorf("%s: PSK en GRE", c.Platform)
			}
		}
	}
}

func TestPlaceholdersAndIdentities(t *testing.T) {
	t.Parallel()
	v := values("ipsec", "mikrotik")
	v.HubAddress = netip.Addr{}
	v.RemoteID = "keyid:rt-centro"
	cfgs, err := tunnelcfg.Build(v)
	if err != nil {
		t.Fatal(err)
	}
	ros := cfgs[0]
	if !strings.Contains(ros.Config, "address="+tunnelcfg.PlaceholderHubAddress+"/32") || len(ros.Placeholders) != 1 {
		t.Fatalf("sin marcador del hub: %v", ros.Placeholders)
	}
	if !strings.Contains(ros.Config, "my-id=key-id:rt-centro remote-id=address:203.0.113.10") {
		t.Fatalf("identidades RouterOS:\n%s", ros.Config)
	}
	if !strings.Contains(cfgs[1].Config, "id = keyid:rt-centro") {
		t.Fatal("identidad strongSwan")
	}
	for in, want := range map[string]string{"203.0.113.1": "203.0.113.1", "rt.isp.example": "fqdn:rt.isp.example",
		"noc@isp.example": "email:noc@isp.example", "keyid:abc": "keyid:abc"} {
		if got := tunnelcfg.StrongSwanID(in); got != want {
			t.Errorf("StrongSwanID(%s) = %s", in, got)
		}
	}
}

func TestRejectsUnsafeValues(t *testing.T) {
	t.Parallel()
	bad := map[string]func(*tunnelcfg.Values){
		"psk con comillas": func(v *tunnelcfg.Values) { v.PSK = testPSK + "\"; /system reset" },
		"psk corta":        func(v *tunnelcfg.Values) { v.PSK = "corta" },
		"id con espacio":   func(v *tunnelcfg.Values) { v.RemoteID = "rt centro" },
		"snmp":             func(v *tunnelcfg.Values) { v.SNMPAuthPass = "a$b" },
		"ntp":              func(v *tunnelcfg.Values) { v.NTPServer = "pool.ntp.org; /x" },
		"mtu":              func(v *tunnelcfg.Values) { v.MTU = 9000 },
		"colector fuera":   func(v *tunnelcfg.Values) { v.CollectorIP = netip.MustParseAddr("192.0.2.1") },
		"transporte":       func(v *tunnelcfg.Values) { v.Transport = "wireguard" },
	}
	for name, f := range bad {
		v := values("ipsec", "mikrotik")
		f(&v)
		if _, err := tunnelcfg.Build(v); !errors.Is(err, tunnelcfg.ErrValue) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	v := values("gre", "huawei")
	v.RemoteAddress = netip.Addr{}
	if _, err := tunnelcfg.Build(v); !errors.Is(err, tunnelcfg.ErrValue) {
		t.Errorf("gre sin IP pública: %v", err)
	}
}
