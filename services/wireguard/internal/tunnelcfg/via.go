package tunnelcfg

import (
	"fmt"
	"net/netip"

	"github.com/google/uuid"
)

// Switches vía router del sitio (ADR-0032, docs/tunnels.md §10). Horus
// alcanza un equipo de IP privada a través del túnel del router con una
// DIRECCIÓN HORUS propia (de la IPAM de túneles): el router hace NAT 1:1
// (netmap) entre esa dirección y la IP privada y solo deja pasar SNMP (UDP
// 161) desde la red de servicios del hub hacia el equipo, las respuestas, y
// sFlow (UDP 6343) del equipo hacia el colector. Horus nunca escribe en el
// router: el administrador pega estas reglas, comentadas para deshacerlas.

// Transporte WireGuard (el de los MikroTik por defecto; ipsec y gre arriba).
const TransportWireGuard = "wireguard"

// Interfaces del lado del router (scripts de alta y de túnel).
const (
	routerOSWireGuardIf = "wg-horus"
	routerOSGREIf       = "horus-gre"
	// ACL avanzadas de VRP para el tráfico de los equipos vía router.
	vrpViaACLIn  = 3991
	vrpViaACLOut = 3992
)

// ViaValues son los valores de la configuración del router para un equipo.
type ViaValues struct {
	Vendor     string // mikrotik | huawei
	Transport  string // wireguard | ipsec | gre
	RouterName string
	DeviceID   string
	DeviceName string
	// Target: IP privada del equipo; Mapped: su dirección Horus.
	Target netip.Addr
	Mapped netip.Addr
	// Subnet/Block: red de gestión declarada y su bloque Horus (inválidos =
	// NAT por equipo).
	SubnetID string
	Subnet   netip.Prefix
	Block    netip.Prefix
	// ServicesCIDR: red de servicios del hub (origen del sondeo SNMP);
	// CollectorIP/CollectorPort: destino de sFlow.
	ServicesCIDR  netip.Prefix
	CollectorIP   netip.Addr
	CollectorPort int
	SFlow         bool
}

func (v *ViaValues) validate() error {
	switch v.Transport {
	case TransportWireGuard, TransportIPsec, TransportGRE:
	default:
		return fmt.Errorf("%w: transport", ErrValue)
	}
	if v.Vendor == "huawei" && v.Transport == TransportWireGuard {
		return fmt.Errorf("%w: VRP has no WireGuard", ErrValue)
	}
	id, err := uuid.Parse(v.DeviceID)
	if err != nil {
		return fmt.Errorf("%w: device id", ErrValue)
	}
	v.DeviceID = id.String()
	if !v.Target.Is4() || !v.Mapped.Is4() || !v.CollectorIP.Is4() || !v.ServicesCIDR.IsValid() || !v.ServicesCIDR.Addr().Is4() {
		return fmt.Errorf("%w: addresses", ErrValue)
	}
	if v.ServicesCIDR.Contains(v.Target) || v.ServicesCIDR.Contains(v.Mapped) {
		return fmt.Errorf("%w: services overlap", ErrValue)
	}
	if v.Block.IsValid() {
		sid, err := uuid.Parse(v.SubnetID)
		if err != nil || !v.Subnet.IsValid() || v.Subnet.Bits() != v.Block.Bits() ||
			!v.Subnet.Contains(v.Target) || !v.Block.Contains(v.Mapped) || !v.Subnet.Addr().Is4() || !v.Block.Addr().Is4() {
			return fmt.Errorf("%w: management subnet", ErrValue)
		}
		v.SubnetID = sid.String()
	}
	if !nameRe.MatchString(v.RouterName) {
		v.RouterName = "router"
	}
	if !nameRe.MatchString(v.DeviceName) {
		v.DeviceName = "switch"
	}
	if v.CollectorPort <= 0 || v.CollectorPort > 65535 {
		v.CollectorPort = 6343
	}
	return nil
}

// tag es el comentario de las reglas del equipo: el UUID entero (los UUIDv7
// creados en el mismo minuto comparten los primeros caracteres).
func (v ViaValues) tag() string { return "horus-via " + v.DeviceID }

func (v ViaValues) netTag() string { return "horus-via-net " + v.SubnetID }

// ViaRouter genera la configuración del router del sitio para un equipo vía
// router: RouterOS 7 (WireGuard, IPsec o GRE) o Huawei VRP (IPsec o GRE).
func ViaRouter(v ViaValues) (Config, error) {
	if err := v.validate(); err != nil {
		return Config{}, err
	}
	if v.Vendor == "huawei" {
		return viaVRP(v), nil
	}
	return viaRouterOS(v), nil
}

func transportTitle(t string) string {
	return map[string]string{TransportWireGuard: "WireGuard", TransportIPsec: "IPsec IKEv2", TransportGRE: "GRE (sin cifrado)"}[t]
}

func viaNotes(v ViaValues) []string {
	n := []string{
		fmt.Sprintf("Dirección Horus del switch: %s. El hub la enruta SOLO por el túnel de este router y solo la acepta como origen por él; Horus sondea SNMP y recibe sFlow en esa dirección y nunca usa la IP privada %s como identidad.", v.Mapped, v.Target),
		fmt.Sprintf("NAT 1:1 (netmap) en el router entre %s y %s, solo para el tráfico con la red de servicios de Horus (%s): el resto de su red no cambia.", v.Mapped, v.Target, v.ServicesCIDR),
		fmt.Sprintf("El switch debe poder volver a %s por este router (normalmente su puerta de enlace en la red de gestión). Si su puerta de enlace es otro equipo, añada en el switch una ruta a %s vía este router.", v.ServicesCIDR, v.ServicesCIDR),
		"Cortafuegos mínimo: SNMP (UDP 161) desde Horus hacia el switch y sus respuestas" + map[bool]string{true: ", y sFlow (UDP " + fmt.Sprint(v.CollectorPort) + ") del switch hacia el colector", false: ""}[v.SFlow] + "; todo lo demás entre Horus y el switch se descarta.",
	}
	if v.Block.IsValid() {
		n = append(n, fmt.Sprintf("Red de gestión declarada %s ↔ bloque Horus %s: una sola regla NAT para toda la red; cada switch nuevo de la red solo añade sus reglas de cortafuegos.", v.Subnet, v.Block))
	}
	return n
}

func viaWarnings(v ViaValues) []string {
	w := []string{}
	if v.Transport == TransportGRE {
		w = append(w, "El túnel de este router es GRE (SIN CIFRADO): el sFlow del switch viaja en claro entre el router y Horus; el SNMP va protegido por SNMPv3 authPriv. Nivel de protección heredado del túnel del router.")
	}
	return w
}

func viaRouterOS(v ViaValues) Config {
	c := newConfig(PlatformRouterOS, "routeros", "MikroTik RouterOS 7 · switch vía router · "+transportTitle(v.Transport))
	svc := v.ServicesCIDR.String()
	var in, out string
	switch v.Transport {
	case TransportWireGuard:
		in, out = "in-interface="+routerOSWireGuardIf+" ", "out-interface="+routerOSWireGuardIf+" "
	case TransportGRE:
		in, out = "in-interface="+routerOSGREIf+" ", "out-interface="+routerOSGREIf+" "
	case TransportIPsec:
		// Sin interfaz: lo que entra descifrado lo marca la política IPsec;
		// lo que sale hacia la red de servicios lo cifra la política nueva.
		in = "ipsec-policy=in,ipsec "
	}
	tag := v.tag()
	var b builder
	b.w("# ===== Horus Flow · switch vía router · RouterOS v7 · túnel %s =====", transportTitle(v.Transport))
	b.w("# Switch: %s (%s) · IP privada %s <-> dirección Horus %s", v.DeviceName, v.DeviceID, v.Target, v.Mapped)
	b.w("# Router: %s", v.RouterName)
	b.w("# Revise antes de pegar. Horus nunca escribe en el router. Todo lo que se crea lleva comment=\"%s\"", tag)
	if v.Block.IsValid() {
		b.w("# (y comment=\"%s\" para la red de gestión); al final está el bloque para deshacerlo.", v.netTag())
	} else {
		b.w("# y al final está el bloque para deshacerlo.")
	}
	b.w("# Pegarlo otra vez es seguro: cada bloque quita primero sus reglas anteriores.")
	b.w("")
	natFrom, natTo, natTag := v.Target.String(), v.Mapped.String(), tag
	if v.Block.IsValid() {
		natFrom, natTo, natTag = v.Subnet.String(), v.Block.String(), v.netTag()
		b.w("# 1) NAT 1:1 de la red de gestión %s <-> bloque Horus %s, solo con la red de servicios de Horus", v.Subnet, v.Block)
	} else {
		b.w("# 1) NAT 1:1 %s <-> %s, solo con la red de servicios de Horus", v.Target, v.Mapped)
	}
	b.w("/ip firewall nat remove [find where comment=\"%s\"]", natTag)
	b.w("/ip firewall nat add chain=dstnat %sdst-address=%s action=netmap to-addresses=%s comment=\"%s\"", in, natTo, natFrom, natTag)
	b.w("/ip firewall nat add chain=srcnat %ssrc-address=%s dst-address=%s action=netmap to-addresses=%s comment=\"%s\"", out, natFrom, svc, natTo, natTag)
	b.w("{")
	if v.Block.IsValid() {
		b.w("    # La red va después de las reglas por switch (que van primero) y antes de cualquier masquerade.")
		b.w("    :local first [:pick [/ip firewall nat find where chain=srcnat and !(comment~\"^horus-via\")] 0]")
	} else {
		b.w("    # Antes de cualquier masquerade: el origen hacia Horus debe ser la dirección Horus.")
		b.w("    :local first [:pick [/ip firewall nat find where chain=srcnat and comment!=\"%s\"] 0]", natTag)
	}
	b.w("    :if ([:typeof $first] = \"id\") do={ /ip firewall nat move [find where chain=srcnat and comment=\"%s\"] destination=$first }", natTag)
	b.w("}")
	b.w("")
	step := 2
	if v.Transport == TransportIPsec {
		src := v.Mapped.String() + "/32"
		ptag := tag
		if v.Block.IsValid() {
			src, ptag = v.Block.String(), v.netTag()
		}
		b.w("# %d) Política IPsec para la dirección Horus (RouterOS aplica el srcnat antes de cifrar)", step)
		b.w("/ip ipsec policy remove [find where comment=\"%s\"]", ptag)
		b.w("/ip ipsec policy add peer=horus tunnel=yes src-address=%s dst-address=%s \\", src, svc)
		b.w("    proposal=horus action=encrypt level=require comment=\"%s\"", ptag)
		b.w("")
		step++
	}
	b.w("# %d) Cortafuegos: solo SNMP desde Horus hacia el switch, sus respuestas%s; el resto se descarta.", step,
		map[bool]string{true: " y el sFlow del switch hacia el colector", false: ""}[v.SFlow])
	b.w("#    Van delante de la primera regla de forward ajena (revise el orden si usa listas de interfaces).")
	b.w("/ip firewall filter remove [find where comment=\"%s\"]", tag)
	b.w("/ip firewall filter add chain=forward %ssrc-address=%s dst-address=%s protocol=udp dst-port=161 action=accept comment=\"%s\"",
		in, svc, v.Target, tag)
	b.w("/ip firewall filter add chain=forward %ssrc-address=%s dst-address=%s protocol=udp src-port=161 action=accept comment=\"%s\"",
		out, v.Target, svc, tag)
	if v.SFlow {
		b.w("/ip firewall filter add chain=forward %ssrc-address=%s dst-address=%s protocol=udp dst-port=%d action=accept comment=\"%s\"",
			out, v.Target, v.CollectorIP, v.CollectorPort, tag)
	}
	b.w("/ip firewall filter add chain=forward %sdst-address=%s action=drop comment=\"%s\"", in, v.Target, tag)
	b.w("/ip firewall filter add chain=forward %ssrc-address=%s dst-address=%s action=drop comment=\"%s\"", out, v.Target, svc, tag)
	b.w("{")
	b.w("    :local first [:pick [/ip firewall filter find where chain=forward and comment!=\"%s\"] 0]", tag)
	b.w("    :if ([:typeof $first] = \"id\") do={ /ip firewall filter move [find where comment=\"%s\"] destination=$first }", tag)
	b.w("}")
	b.w("")
	b.w("# ----- Deshacer (pegar para retirar este switch del camino de Horus) -----")
	b.w("# /ip firewall filter remove [find where comment=\"%s\"]", tag)
	if v.Block.IsValid() {
		b.w("# Y, cuando ya no quede ningún switch de la red de gestión %s:", v.Subnet)
		b.w("# /ip firewall nat remove [find where comment=\"%s\"]", v.netTag())
		if v.Transport == TransportIPsec {
			b.w("# /ip ipsec policy remove [find where comment=\"%s\"]", v.netTag())
		}
	} else {
		b.w("# /ip firewall nat remove [find where comment=\"%s\"]", tag)
		if v.Transport == TransportIPsec {
			b.w("# /ip ipsec policy remove [find where comment=\"%s\"]", tag)
		}
	}
	c.Config = b.String()
	c.Notes = append(viaNotes(v), "Sintaxis de RouterOS 7 según help.mikrotik.com (/ip firewall nat action=netmap, /ip ipsec policy, /ip firewall filter).")
	if v.Transport == TransportIPsec {
		c.Notes = append(c.Notes, "IPsec: la política nueva negocia su propia CHILD SA con el hub (route-based, acepta cualquier selector y la ata a la interfaz del router).")
	}
	c.Warnings = viaWarnings(v)
	return c
}

func viaVRP(v ViaValues) Config {
	c := newConfig(PlatformVRP, "vrp", "Huawei VRP · switch vía router · "+transportTitle(v.Transport))
	svc := v.ServicesCIDR
	var b builder
	b.w("system-view")
	// ACL de entrada (Horus → switch) y de salida (switch → Horus) en la
	// interfaz del túnel. Se permite y deniega por la dirección Horus y por la
	// privada: así vale con independencia del orden NAT/filtro del equipo.
	b.w("acl number %d", vrpViaACLIn)
	b.w(" description horus-via-in")
	for _, dst := range []netip.Addr{v.Mapped, v.Target} {
		b.w(" rule permit udp source %s %s destination %s 0 destination-port eq 161", svc.Addr(), wildcard(svc), dst)
	}
	for _, dst := range []netip.Addr{v.Mapped, v.Target} {
		b.w(" rule deny ip destination %s 0", dst)
	}
	b.w(" quit")
	b.w("acl number %d", vrpViaACLOut)
	b.w(" description horus-via-out")
	for _, src := range []netip.Addr{v.Mapped, v.Target} {
		b.w(" rule permit udp source %s 0 source-port eq 161 destination %s %s", src, svc.Addr(), wildcard(svc))
		if v.SFlow {
			b.w(" rule permit udp source %s 0 destination %s 0 destination-port eq %d", src, v.CollectorIP, v.CollectorPort)
		}
	}
	for _, src := range []netip.Addr{v.Mapped, v.Target} {
		b.w(" rule deny ip source %s 0 destination %s %s", src, svc.Addr(), wildcard(svc))
	}
	b.w(" quit")
	b.w("interface Tunnel%d", vrpTunnelNumber)
	if v.Block.IsValid() {
		b.w(" nat static global %s inside %s netmask %s", v.Block.Addr(), v.Subnet.Addr(), mask(v.Subnet))
	} else {
		b.w(" nat static global %s inside %s", v.Mapped, v.Target)
	}
	b.w(" traffic-filter inbound acl %d", vrpViaACLIn)
	b.w(" traffic-filter outbound acl %d", vrpViaACLOut)
	b.w(" quit")
	b.w("return")
	c.Config = b.String()
	undo := []string{
		fmt.Sprintf("Para deshacer: en «interface Tunnel%d», «undo nat static global %s inside %s»%s; en las ACL %d y %d, «display acl» y «undo rule <n>» de las reglas que citan %s o %s; si ya no queda ningún switch vía este router, «undo traffic-filter inbound» y «undo traffic-filter outbound» en la interfaz.",
			vrpTunnelNumber, natGlobal(v), natInside(v), natMask(v), vrpViaACLIn, vrpViaACLOut, v.Mapped, v.Target),
	}
	c.Notes = append(viaNotes(v),
		"PENDIENTE DE VALIDACIÓN EN EQUIPO REAL: NAT estático en la interfaz Tunnel y traffic-filter según la documentación oficial de Huawei (AR/NetEngine «NAT Configuration», «ACL Configuration»); en VRP V5 (AR) la interfaz se llama Tunnel0/0/10.",
		fmt.Sprintf("Las ACL %d (entrada) y %d (salida) se comparten entre los switches vía este router: cada switch añade sus reglas (numeración automática). Si ya usa esos números o la interfaz ya tiene traffic-filter, adapte las líneas.", vrpViaACLIn, vrpViaACLOut),
	)
	c.Notes = append(c.Notes, undo...)
	c.Warnings = viaWarnings(v)
	if v.Block.IsValid() {
		c.Warnings = append(c.Warnings, "VRP no admite un NAT estático por switch dentro de una red con NAT estático de red: si ya había switches de esta red con su propia dirección Horus, regenere su alta.")
	}
	return c
}

func natGlobal(v ViaValues) string {
	if v.Block.IsValid() {
		return v.Block.Addr().String()
	}
	return v.Mapped.String()
}

func natInside(v ViaValues) string {
	if v.Block.IsValid() {
		return v.Subnet.Addr().String()
	}
	return v.Target.String()
}

func natMask(v ViaValues) string {
	if v.Block.IsValid() {
		return " netmask " + mask(v.Subnet)
	}
	return ""
}
