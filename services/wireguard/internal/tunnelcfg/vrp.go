package tunnelcfg

import (
	"fmt"
	"net/netip"
)

// vrpMIBViews son los subárboles SNMP que lee Horus en VRP (los mismos que
// el alta directa, services/devices/internal/domain/vrp.go).
var vrpMIBViews = []string{
	"1.3.6.1.2.1.1", "1.3.6.1.2.1.2", "1.3.6.1.2.1.31", "1.3.6.1.2.1.47", "1.3.6.1.2.1.80",
	"1.3.6.1.4.1.2011.5.25.31", "1.3.6.1.4.1.2011.5.25.111",
}

func wildcard(p netip.Prefix) string {
	bits := p.Bits()
	var m [4]byte
	for i := range 4 {
		n := min(max(bits-8*i, 0), 8)
		m[i] = ^byte(0xff << (8 - n))
	}
	return netip.AddrFrom4(m).String()
}

func mask(p netip.Prefix) string {
	bits := p.Bits()
	var m [4]byte
	for i := range 4 {
		n := min(max(bits-8*i, 0), 8)
		m[i] = byte(0xff << (8 - n))
	}
	return netip.AddrFrom4(m).String()
}

// vrp genera la configuración Huawei VRP. Sintaxis de VRP V8 (NE40E /
// NetEngine 8000) y V5 (AR) según la documentación oficial de Huawei
// («IPsec Configuration» → «Configuring an IPsec Tunnel Interface» /
// «Configuring IKEv2»; «GRE Configuration»), PENDIENTE DE VALIDACIÓN EN
// EQUIPO REAL (docs/tunnels.md §8). La IP de túnel vive en una LoopBack y
// la interfaz Tunnel es unnumbered: NetStream y SNMP salen con esa IP.
// Suite IKE conservadora (AES-256, SHA2-256, grupo 14 = MODP-2048) que
// casa con la propuesta de respaldo del hub en VRP antiguos.
func vrp(v Values) Config {
	title := "Huawei VRP · IPsec IKEv2 (Tunnel tunnel-protocol ipsec)"
	if v.Transport == TransportGRE {
		title = "Huawei VRP · GRE (sin cifrado)"
	}
	c := newConfig(PlatformVRP, "vrp", title)
	hub := hubOr(v, &c)
	tun, svc := v.TunnelAddress.String(), v.ServicesCIDR
	c.Placeholders = append(c.Placeholders,
		Placeholder{Key: PlaceholderWANInterface, Description: "Interfaz WAN del router (origen del túnel), p. ej. GigabitEthernet0/1/0."},
		Placeholder{Key: PlaceholderNetStream, Description: "Interfaz donde activar NetStream (subida al proveedor o lado clientes). Repita el bloque por cada una."})
	var b builder
	b.w("system-view")
	// 1) IP de túnel (identidad del exportador) en una LoopBack.
	b.w("interface LoopBack%d", vrpLoopbackNumber)
	b.w(" description horus-tunnel-ip")
	b.w(" ip address %s 255.255.255.255", tun)
	b.w(" quit")
	switch v.Transport {
	case TransportIPsec:
		// 2) IKEv2: propuesta, par, perfil IPsec y la interfaz Tunnel route-based.
		b.w("ipsec proposal horus")
		b.w(" esp authentication-algorithm sha2-256")
		b.w(" esp encryption-algorithm aes-256")
		b.w(" quit")
		b.w("ike proposal 10")
		b.w(" encryption-algorithm aes-256")
		b.w(" dh group14")
		b.w(" authentication-algorithm sha2-256")
		b.w(" integrity-algorithm hmac-sha2-256")
		b.w(" prf hmac-sha2-256")
		b.w(" authentication-method pre-share")
		b.w(" sa duration 14400")
		b.w(" quit")
		b.w("ike peer horus")
		b.w(" undo version 1")
		b.w(" version 2")
		b.w(" pre-shared-key cipher %s", v.PSK)
		b.w(" ike-proposal 10")
		b.w(" local-id-type %s", vrpIDType(v.RemoteID))
		if IDKind(v.RemoteID) != "ip" {
			b.w(" local-id %s", idValue(v.RemoteID))
		}
		b.w(" remote-id-type %s", vrpIDType(v.HubID))
		b.w(" remote-id %s", idValue(v.HubID))
		b.w(" remote-address %s", hub)
		b.w(" dpd type periodic")
		b.w(" dpd idle-time 30")
		b.w(" quit")
		b.w("ipsec profile horus")
		b.w(" ike-peer horus")
		b.w(" proposal horus")
		b.w(" pfs dh-group14")
		b.w(" sa duration time-based 3600")
		b.w(" quit")
		b.w("interface Tunnel%d", vrpTunnelNumber)
		b.w(" description horus-ipsec")
		b.w(" ip address unnumbered interface LoopBack%d", vrpLoopbackNumber)
		b.w(" tunnel-protocol ipsec")
		b.w(" source %s", PlaceholderWANInterface)
		b.w(" destination %s", hub)
		b.w(" ipsec profile horus")
		b.w(" mtu %d", v.MTU)
		b.w(" tcp adjust-mss %d", MSS(v.MTU))
		b.w(" quit")
	case TransportGRE:
		b.w("interface Tunnel%d", vrpTunnelNumber)
		b.w(" description horus-gre")
		b.w(" ip address unnumbered interface LoopBack%d", vrpLoopbackNumber)
		b.w(" tunnel-protocol gre")
		b.w(" source %s", v.RemoteAddress)
		b.w(" destination %s", hub)
		if v.GREKey > 0 {
			b.w(" gre key %d", v.GREKey)
		}
		b.w(" mtu %d", v.MTU)
		b.w(" tcp adjust-mss %d", MSS(v.MTU))
		b.w(" quit")
	}
	// 3) Ruta hacia la red de servicios de Horus por el túnel.
	b.w("ip route-static %s %s Tunnel%d description horus", svc.Addr(), mask(svc), vrpTunnelNumber)
	// 4) SNMPv3 authPriv solo desde la red de servicios de Horus.
	b.w("acl number %d", vrpACL)
	b.w(" description horus-snmp-ro")
	b.w(" rule 5 permit source %s %s", svc.Addr(), wildcard(svc))
	b.w(" rule 10 deny")
	b.w(" quit")
	b.w("snmp-agent")
	b.w("snmp-agent sys-info version v3")
	for _, oid := range vrpMIBViews {
		b.w("snmp-agent mib-view included horus-ro %s", oid)
	}
	b.w("snmp-agent group v3 horus-ro privacy read-view horus-ro acl %d", vrpACL)
	b.w("snmp-agent usm-user v3 %s group horus-ro", v.SNMPUser)
	b.w("snmp-agent usm-user v3 %s authentication-mode sha2-256", v.SNMPUser)
	b.w("%s", v.SNMPAuthPass)
	b.w("%s", v.SNMPAuthPass)
	b.w("snmp-agent usm-user v3 %s privacy-mode aes128", v.SNMPUser)
	b.w("%s", v.SNMPPrivPass)
	b.w("%s", v.SNMPPrivPass)
	b.w("snmp-agent usm-user v3 %s acl %d", v.SNMPUser, vrpACL)
	// 5) NetStream v9 por el túnel con la IP de túnel de origen.
	b.w("ip netstream export version 9")
	b.w("ip netstream export template timeout-rate 1")
	b.w("ip netstream export template option sampler")
	b.w("ip netstream export source %s", tun)
	b.w("ip netstream export host %s %d", v.CollectorIP, NetStreamPort)
	b.w("ip netstream timeout active 1")
	b.w("ip netstream timeout inactive 15")
	b.w("ip netstream sampler fix-packets %d inbound", v.SamplingRate)
	b.w("ip netstream sampler fix-packets %d outbound", v.SamplingRate)
	b.w("interface %s", PlaceholderNetStream)
	b.w(" ip netstream inbound")
	b.w(" ip netstream outbound")
	b.w(" quit")
	b.w("return")
	c.Config = b.String()
	c.Notes = append(commonNotes(v),
		"PENDIENTE DE VALIDACIÓN EN EQUIPO REAL: sintaxis de VRP V8 (NE40E/NetEngine 8000) tomada de la documentación oficial de Huawei; en VRP V5 (AR) la interfaz se llama Tunnel0/0/10 y algunos comandos cambian (docs/tunnels.md §5.2).",
		"Las contraseñas SNMP aparecen una sola vez: tras «authentication-mode» y «privacy-mode» VRP pide la contraseña dos veces; las líneas siguientes son esas respuestas.",
		fmt.Sprintf("La ACL %d limita SNMP a la red de servicios de Horus (%s); si ya usa ese número, cámbielo en las tres líneas que lo citan.", vrpACL, svc),
		fmt.Sprintf("NetStream y SNMP salen con la IP de la LoopBack%d (%s): Horus solo acepta esa IP por este túnel.", vrpLoopbackNumber, tun))
	if v.Transport == TransportIPsec {
		c.Notes = append(c.Notes,
			"IPsec en NE40E/NetEngine 8000 suele requerir una tarjeta de servicio (licencia): si el equipo no la tiene, use GRE por un camino privado o el modo directo.",
			"Suite IKEv2: AES-256 + SHA2-256 + grupo 14 (MODP-2048) con PFS grupo 14, la propuesta de respaldo del hub para VRP; si su VRP admite «dh group20» (ECP-384) y AES-GCM, puede usarlos (el hub los prefiere).")
	} else if v.GREKey > 0 {
		c.Notes = append(c.Notes, fmt.Sprintf("Clave GRE %d: identifica el túnel en el hub (no es un secreto). Keepalive GRE desactivado (no lo configure: el hub no lo responde).", v.GREKey))
	}
	return c
}
