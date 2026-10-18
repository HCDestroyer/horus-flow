package tunnelcfg

import (
	"fmt"
	"strings"
)

// strongSwan genera el ejemplo genérico Linux: swanctl.conf (strongSwan ≥ 5.8,
// https://docs.strongswan.org/docs/latest/swanctl/swanctlConf.html) con
// interfaz XFRM route-based (https://docs.strongswan.org/docs/latest/features/routeBasedVpn.html)
// o túnel GRE de iproute2. Sirve de referencia para equipos que no son
// MikroTik ni Huawei (Cisco, Juniper, FortiGate…: mismas propuestas, PSK e
// identidades).
func strongSwan(v Values) Config {
	title := "Linux / strongSwan (ejemplo genérico) · IPsec IKEv2 route-based"
	if v.Transport == TransportGRE {
		title = "Linux / iproute2 (ejemplo genérico) · GRE (sin cifrado)"
	}
	c := newConfig(PlatformStrongSwan, "shell", title)
	hub := hubOr(v, &c)
	tun, svc := v.TunnelAddress.String(), v.ServicesCIDR.String()
	var b builder
	b.w("#!/bin/sh")
	b.w("# Horus Flow · túnel %s · ejemplo genérico Linux (router %s)", strings.ToUpper(v.Transport), v.RouterName)
	b.w("set -eu")
	switch v.Transport {
	case TransportIPsec:
		b.w("# 1) Interfaz XFRM (if_id 42 local) con la IP de túnel y ruta a la red de servicios de Horus")
		b.w("ip link add horus0 type xfrm dev %s if_id 42", PlaceholderWANInterface)
		b.w("ip addr add %s/32 dev horus0", tun)
		b.w("ip link set horus0 mtu %d up", v.MTU)
		b.w("ip route replace %s dev horus0 src %s", svc, tun)
		b.w("# 2) strongSwan (swanctl): IKEv2 con PSK, iniciado por el router")
		b.w("cat > /etc/swanctl/conf.d/horus.conf <<'CONF'")
		b.w("connections {")
		b.w("  horus {")
		b.w("    version = 2")
		b.w("    remote_addrs = %s", hub)
		b.w("    proposals = %s", strings.Join(HubIKEProposals, ","))
		b.w("    dpd_delay = 30s")
		b.w("    rekey_time = 4h")
		b.w("    local {")
		b.w("      auth = psk")
		b.w("      id = %s", StrongSwanID(v.RemoteID))
		b.w("    }")
		b.w("    remote {")
		b.w("      auth = psk")
		b.w("      id = %s", StrongSwanID(v.HubID))
		b.w("    }")
		b.w("    children {")
		b.w("      horus {")
		b.w("        local_ts = 0.0.0.0/0")
		b.w("        remote_ts = 0.0.0.0/0")
		b.w("        if_id_in = 42")
		b.w("        if_id_out = 42")
		b.w("        esp_proposals = %s", strings.Join(HubESPProposals, ","))
		b.w("        rekey_time = 1h")
		b.w("        dpd_action = restart")
		b.w("        start_action = start")
		b.w("        close_action = start")
		b.w("      }")
		b.w("    }")
		b.w("  }")
		b.w("}")
		b.w("secrets {")
		b.w("  ike-horus {")
		b.w("    id-router = %s", StrongSwanID(v.RemoteID))
		b.w("    id-hub = %s", StrongSwanID(v.HubID))
		b.w("    secret = \"%s\"", v.PSK)
		b.w("  }")
		b.w("}")
		b.w("CONF")
		b.w("chmod 0600 /etc/swanctl/conf.d/horus.conf")
		b.w("swanctl --load-all")
		b.w("swanctl --initiate --child horus")
	case TransportGRE:
		b.w("# 1) Túnel GRE hacia el hub con la IP de túnel y ruta a la red de servicios de Horus")
		key := ""
		if v.GREKey > 0 {
			key = fmt.Sprintf(" key %d", v.GREKey)
		}
		b.w("ip tunnel add horus0 mode gre local %s remote %s ttl 64%s", v.RemoteAddress, hub, key)
		b.w("ip addr add %s/32 dev horus0", tun)
		b.w("ip link set horus0 mtu %d up", v.MTU)
		b.w("ip route replace %s dev horus0 src %s", svc, tun)
		b.w("iptables -t mangle -A FORWARD -o horus0 -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --set-mss %d", MSS(v.MTU))
	}
	b.w("# 3) Exporte NetFlow v9/IPFIX a %s:%d (IPFIX) o :%d (NetFlow v9) con origen %s y SNMPv3", v.CollectorIP, IPFIXPort, NetStreamPort, tun)
	b.w("#    (usuario %s, SHA-256/AES-128) escuchando en %s solo para %s.", v.SNMPUser, tun, svc)
	c.Config = b.String()
	c.Notes = append(commonNotes(v),
		"Ejemplo de referencia (Debian/Ubuntu con strongswan-swanctl ≥ 5.8 y kernel con XFRM interfaces): adapte nombres de interfaz y servicio. Las mismas propuestas, identidades y PSK sirven para otros fabricantes (Cisco IOS-XE VTI, Juniper st0, FortiGate route-based).",
		"Propuestas aceptadas por el hub: IKE "+strings.Join(HubIKEProposals, ", ")+"; ESP "+strings.Join(HubESPProposals, ", ")+".")
	return c
}
