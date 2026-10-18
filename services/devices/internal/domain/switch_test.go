package domain

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func TestBuildSwitchConfig(t *testing.T) {
	t.Parallel()
	mgmt := netip.MustParseAddr("10.20.0.5")
	base := SwitchValues{Name: "sw\"core$1", ManagementAddress: mgmt, SNMPUser: "horus-0000abcd", SNMPAuthPass: "AuthTestPass1234",
		SNMPPrivPass: "PrivTestPass1234"}

	ros := base
	ros.Platform = PlatformRouterOS
	c, err := BuildSwitchConfig(ros)
	if err != nil {
		t.Fatal(err)
	}
	if c.Language != "routeros" || !strings.Contains(c.Config, "addresses={HORUS_POLLER_IP}") || strings.Contains(c.Config, "$") ||
		len(c.Placeholders) != 1 || c.SFlowConfig != nil {
		t.Fatalf("routeros: %+v\n%s", c, c.Config)
	}

	sw := SwitchValues{Name: "css", Platform: PlatformSwOS, ManagementAddress: mgmt, Community: "CommunityTestValue2345",
		PollerAddress: netip.MustParseAddr("203.0.113.11")}
	c, err = BuildSwitchConfig(sw)
	if err != nil || c.Language != "swos_web" || !strings.Contains(c.Config, "Community = CommunityTestValue2345") || len(c.Warnings) < 2 {
		t.Fatalf("swos: %v %+v", err, c)
	}
	sw.Community = "corta"
	if _, err := BuildSwitchConfig(sw); !errors.Is(err, ErrSwitchValue) {
		t.Fatalf("comunidad corta: %v", err)
	}

	vrp := base
	vrp.Platform, vrp.SFlow, vrp.SamplingRate = PlatformVRP, true, 8192
	vrp.CollectorAddress, vrp.CollectorPort = netip.MustParseAddr("203.0.113.10"), 6343
	c, err = BuildSwitchConfig(vrp)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"acl number 2991", "snmp-agent usm-user v3 horus-0000abcd authentication-mode sha2-256", "snmp-agent usm-user v3 horus-0000abcd acl 2991"} {
		if !strings.Contains(c.Config, want) {
			t.Errorf("vrp: falta %q", want)
		}
	}
	if c.SFlowConfig == nil || !strings.Contains(*c.SFlowConfig, "sflow collector 1 ip 203.0.113.10 udp-port 6343 description horus") ||
		!strings.Contains(*c.SFlowConfig, " sflow flow-sampling inbound") || strings.Contains(*c.SFlowConfig, "outbound") {
		t.Fatalf("sflow: %v", c.SFlowConfig)
	}
	// sFlow solo en vrp y con tasa válida.
	ros.SFlow = true
	if _, err := BuildSwitchConfig(ros); !errors.Is(err, ErrSwitchValue) {
		t.Fatal("sflow en routeros")
	}
	vrp.SamplingRate = 100
	if _, err := BuildSwitchConfig(vrp); !errors.Is(err, ErrSwitchValue) {
		t.Fatal("tasa inválida")
	}
	// Credencial con caracteres peligrosos o plataforma desconocida.
	bad := base
	bad.Platform, bad.SNMPAuthPass = PlatformVRP, "x\"; /system reset"
	if _, err := BuildSwitchConfig(bad); !errors.Is(err, ErrSwitchValue) {
		t.Fatal("credencial peligrosa")
	}
	bad.Platform = "ios"
	if _, err := BuildSwitchConfig(bad); !errors.Is(err, ErrSwitchValue) {
		t.Fatal("plataforma desconocida")
	}
	if VendorForPlatform(PlatformVRP) != VendorHuawei || VendorForPlatform(PlatformSwOS) != VendorMikroTik || !SNMPv2cOnly(PlatformSwOS) ||
		SFlowSupported(PlatformRouterOS) {
		t.Fatal("tablas de plataforma")
	}
}
