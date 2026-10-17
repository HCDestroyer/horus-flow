package domain

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func TestBuildVRPDirectConfig(t *testing.T) {
	t.Parallel()
	v := VRPValues{RouterName: "ne40e", ManagementAddress: netip.MustParseAddr("198.51.100.20"), SNMPUser: "horus-000000a1",
		SNMPAuthPass: "AbcdEfgh2345Jkmn", SNMPPrivPass: "ZyxwVuts9876Rqpn"}
	c, err := BuildVRPDirectConfig(v)
	if err != nil {
		t.Fatal(err)
	}
	// Sin colector ni sondeador configurados: marcadores, y siempre el de la interfaz.
	if len(c.Placeholders) != 3 || !strings.Contains(c.Config, "ip netstream export host {HORUS_COLLECTOR_IP} 2055") ||
		!strings.Contains(c.Config, "rule 5 permit source {HORUS_POLLER_IP} 0") || !strings.Contains(c.Config, "fix-packets 1000 inbound") {
		t.Fatalf("marcadores:\n%s\n%+v", c.Config, c.Placeholders)
	}
	if len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], "NTP") || len(c.Notes) == 0 || !strings.Contains(c.Notes[0], "PENDIENTE DE VALIDACIÓN") {
		t.Fatalf("avisos/notas: %+v %+v", c.Warnings, c.Notes)
	}
	for _, oid := range VRPMIBViews {
		if !strings.Contains(c.Config, "snmp-agent mib-view included horus-ro "+oid+"\n") {
			t.Errorf("vista sin %s", oid)
		}
	}
	// La contraseña va dos veces tras cada modo (respuestas al prompt de VRP) y no hay líneas de comentario.
	if !strings.Contains(c.Config, "authentication-mode sha2-256\nAbcdEfgh2345Jkmn\nAbcdEfgh2345Jkmn\n") ||
		!strings.Contains(c.Config, "privacy-mode aes128\nZyxwVuts9876Rqpn\nZyxwVuts9876Rqpn\n") {
		t.Fatalf("contraseñas:\n%s", c.Config)
	}
	for _, l := range strings.Split(c.Config, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			t.Fatalf("comentario en la configuración: %q", l)
		}
	}
	v.CollectorAddress, v.PollerAddress, v.NTPServer = netip.MustParseAddr("2001:db8::10"), netip.MustParseAddr("203.0.113.11"), netip.MustParseAddr("192.0.2.123")
	v.SamplingRate, v.CollectorPort = 100, 4739
	c, err = BuildVRPDirectConfig(v)
	if err != nil || len(c.Placeholders) != 1 || !strings.Contains(c.Config, "ntp-service unicast-server 192.0.2.123") ||
		!strings.Contains(c.Config, "ip netstream export host 2001:db8::10 4739") || len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], "IPv6") {
		t.Fatalf("valores de la instalación: %v\n%s\n%+v", err, c.Config, c.Warnings)
	}
	for _, bad := range []VRPValues{
		{ManagementAddress: v.ManagementAddress, SNMPUser: "horus x", SNMPAuthPass: v.SNMPAuthPass, SNMPPrivPass: v.SNMPPrivPass},
		{ManagementAddress: v.ManagementAddress, SNMPUser: "horus", SNMPAuthPass: "corta", SNMPPrivPass: v.SNMPPrivPass},
		{ManagementAddress: v.ManagementAddress, SNMPUser: "horus", SNMPAuthPass: v.SNMPAuthPass, SNMPPrivPass: "con\nsalto123"},
		{SNMPUser: "horus", SNMPAuthPass: v.SNMPAuthPass, SNMPPrivPass: v.SNMPPrivPass},
		{ManagementAddress: v.ManagementAddress, SNMPUser: "horus", SNMPAuthPass: v.SNMPAuthPass, SNMPPrivPass: v.SNMPPrivPass, SamplingRate: 70000},
	} {
		if _, err := BuildVRPDirectConfig(bad); !errors.Is(err, ErrVRPValue) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestValidManagementAddress(t *testing.T) {
	t.Parallel()
	for s, want := range map[string]bool{"198.51.100.20": true, "10.0.0.1": true, "2001:db8::1": true, "0.0.0.0": false, "127.0.0.1": false,
		"169.254.1.1": false, "fe80::1": false, "224.0.0.5": false, "255.255.255.255": false, "::": false} {
		if got := ValidManagementAddress(netip.MustParseAddr(s)); got != want {
			t.Errorf("%s: %v", s, got)
		}
	}
	if ConnectionModeFor(VendorHuawei) != ConnectionDirect || ConnectionModeFor(VendorMikroTik) != ConnectionWireGuard || ConnectionModeFor("") != ConnectionWireGuard {
		t.Fatal("ConnectionModeFor")
	}
}
