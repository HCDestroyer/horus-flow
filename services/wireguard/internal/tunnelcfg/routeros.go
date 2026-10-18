package tunnelcfg

import "fmt"

// routerOS genera la configuración RouterOS 7 (≥ 7.12). IPsec: RouterOS no
// tiene interfaces VTI, así que el lado del router es una política IKEv2 en
// modo túnel de su /32 hacia la red de servicios (el hub, route-based con
// selectores 0/0, la acepta estrechándola). Referencias:
// https://help.mikrotik.com/docs/spaces/ROS/pages/11993097/IPsec y
// https://help.mikrotik.com/docs/spaces/ROS/pages/24805521/GRE.
func routerOS(v Values) Config {
	title := "MikroTik RouterOS 7 · IPsec IKEv2"
	if v.Transport == TransportGRE {
		title = "MikroTik RouterOS 7 · GRE (sin cifrado)"
	}
	c := newConfig(PlatformRouterOS, "routeros", title)
	svc, tun := v.ServicesCIDR.String(), v.TunnelAddress.String()
	hub := hubOr(v, &c)
	var b builder
	b.w("# ===== Horus Flow · túnel %s · RouterOS v7 (>= 7.12) =====", map[string]string{TransportIPsec: "IPsec IKEv2", TransportGRE: "GRE"}[v.Transport])
	b.w("# Router: %s (%s)", v.RouterName, v.RouterID)
	b.w("# Revise antes de pegar. Todo lo que se crea lleva comment=\"horus\" o el nombre horus/horus-*;")
	b.w("# al final está el bloque para deshacerlo. Horus no escribe en el router: solo lee por el túnel.")
	b.w("")
	if v.NTPServer != "" {
		b.w("# 0) Hora (los timestamps de IPFIX dependen de NTP)")
		b.w("/system ntp client set enabled=yes servers=%s", v.NTPServer)
		b.w("")
	}
	inMatch := "in-interface=horus-gre"
	switch v.Transport {
	case TransportIPsec:
		b.w("# 1) IP de túnel en una interfaz lógica (bridge vacío = loopback)")
		b.w("/interface bridge add name=horus-lo protocol-mode=none comment=\"horus\"")
		b.w("/ip address add address=%s/32 interface=horus-lo comment=\"horus\"", tun)
		b.w("")
		b.w("# 2) IKEv2 con PSK hacia el hub (NAT-T automático, DPD 30 s, rekey IKE 4 h / ESP 1 h, PFS ECP-384)")
		b.w("/ip ipsec profile add name=horus hash-algorithm=sha256 enc-algorithm=aes-256 dh-group=ecp384,modp2048 \\")
		b.w("    lifetime=4h dpd-interval=30s dpd-maximum-failures=5 nat-traversal=yes")
		b.w("/ip ipsec proposal add name=horus auth-algorithms=sha256 enc-algorithms=aes-256-gcm,aes-256-cbc \\")
		b.w("    pfs-group=ecp384 lifetime=1h")
		b.w("/ip ipsec peer add name=horus address=%s/32 exchange-mode=ike2 profile=horus comment=\"horus\"", hub)
		b.w("/ip ipsec identity add peer=horus auth-method=pre-shared-key secret=\"%s\" \\", v.PSK)
		b.w("    my-id=%s remote-id=%s generate-policy=no comment=\"horus\"", routerOSID(v.RemoteID), routerOSID(v.HubID))
		b.w("/ip ipsec policy add peer=horus tunnel=yes src-address=%s/32 dst-address=%s \\", tun, svc)
		b.w("    proposal=horus action=encrypt level=require comment=\"horus\"")
		b.w("")
		b.w("# 3) Sin NAT para el tráfico del túnel: un masquerade en la WAN cambiaría el origen y la")
		b.w("#    política IPsec ya no coincidiría (RouterOS aplica srcnat antes de cifrar).")
		inMatch = "ipsec-policy=in,ipsec"
	case TransportGRE:
		b.w("# 1) Túnel GRE hacia el hub (sin clave: RouterOS no la admite; el hub solo acepta GRE de esta IP pública).")
		b.w("#    Keepalive desactivado: el hub Linux no responde keepalives de GRE.")
		b.w("/interface gre add name=horus-gre local-address=%s remote-address=%s mtu=%d \\", v.RemoteAddress, hub, v.MTU)
		b.w("    keepalive=0s,0 clamp-tcp-mss=yes allow-fast-path=no comment=\"horus\"")
		b.w("/ip address add address=%s/32 network=%s interface=horus-gre comment=\"horus\"", tun, v.CollectorIP)
		b.w("/ip route add dst-address=%s gateway=horus-gre comment=\"horus\"", svc)
		b.w("")
		b.w("# 2) Sin NAT para el tráfico del túnel (origen = IP de túnel).")
	}
	b.w("{")
	b.w("    :local first [:pick [/ip firewall nat find where chain=srcnat] 0]")
	b.w("    /ip firewall nat add chain=srcnat src-address=%s dst-address=%s action=accept comment=\"horus no-nat\"", tun, svc)
	b.w("    :if ([:typeof $first] = \"id\") do={ /ip firewall nat move [find where comment=\"horus no-nat\"] destination=$first }")
	b.w("}")
	b.w("")
	b.w("# 4) Firewall: transporte desde el hub y gestión solo desde Horus por el túnel, delante de la")
	b.w("#    primera regla de input (revise el orden si usa listas de interfaces o reglas propias).")
	b.w("{")
	b.w("    :local first [:pick [/ip firewall filter find where chain=input] 0]")
	if v.Transport == TransportIPsec {
		b.w("    /ip firewall filter add chain=input src-address=%s protocol=udp dst-port=500,4500 action=accept comment=\"horus ike\"", hub)
		b.w("    /ip firewall filter add chain=input src-address=%s protocol=ipsec-esp action=accept comment=\"horus esp\"", hub)
	} else {
		b.w("    /ip firewall filter add chain=input src-address=%s protocol=gre action=accept comment=\"horus gre\"", hub)
	}
	b.w("    /ip firewall filter add chain=input %s src-address=%s dst-address=%s \\", inMatch, svc, tun)
	b.w("        protocol=udp dst-port=161 action=accept comment=\"horus snmp\"")
	b.w("    /ip firewall filter add chain=input %s src-address=%s dst-address=%s \\", inMatch, svc, tun)
	b.w("        protocol=tcp dst-port=443,8729 action=accept comment=\"horus api\"")
	b.w("    /ip firewall filter add chain=input %s src-address=%s dst-address=%s \\", inMatch, svc, tun)
	b.w("        protocol=icmp action=accept comment=\"horus icmp\"")
	b.w("    :if ([:typeof $first] = \"id\") do={")
	b.w("        /ip firewall filter move [find where comment~\"^horus (ike|esp|gre|snmp|api|icmp)\\$\"] destination=$first")
	b.w("    }")
	b.w("}")
	b.w("")
	if v.APIUser != "" {
		b.w("# 5) Usuario de solo lectura para la API REST (sin write, policy ni sensitive)")
		b.w("/user group add name=horus-ro comment=\"horus\" \\")
		b.w("    policy=read,api,rest-api,!write,!policy,!sensitive,!local,!telnet,!ssh,!ftp,!reboot,!test,!winbox,!password,!web,!sniff,!romon")
		b.w("/user add name=%s group=horus-ro password=\"%s\" address=%s comment=\"horus\"", v.APIUser, v.APIPassword, svc)
		b.w("/certificate add name=horus-api common-name=%s subject-alt-name=IP:%s \\", tun, tun)
		b.w("    key-usage=digital-signature,key-encipherment,key-cert-sign,tls-server days-valid=3650")
		b.w("/certificate sign horus-api")
		b.w("/ip service set www-ssl certificate=horus-api disabled=no")
		b.w("/ip service set api-ssl certificate=horus-api disabled=no")
		b.w("")
	}
	b.w("# 6) SNMPv3 authPriv (SHA1 + AES), solo lectura, solo desde Horus")
	b.w("/snmp community add name=%s security=private \\", v.SNMPUser)
	b.w("    authentication-protocol=SHA1 authentication-password=\"%s\" \\", v.SNMPAuthPass)
	b.w("    encryption-protocol=AES encryption-password=\"%s\" \\", v.SNMPPrivPass)
	b.w("    addresses=%s read-access=yes write-access=no comment=\"horus\"", svc)
	b.w("/snmp set enabled=yes")
	b.w("")
	b.w("# 7) Traffic Flow (IPFIX, sin muestreo) hacia el colector de Horus por el túnel, con la IP de túnel de origen")
	b.w(":if ([:len [/ip traffic-flow target find]] = 0) do={")
	b.w("    /ip traffic-flow set enabled=yes interfaces=all active-flow-timeout=1m inactive-flow-timeout=15s packet-sampling=no")
	b.w("} else={")
	b.w("    :put \"HORUS AVISO: Traffic Flow ya exporta a otro colector; no se cambian sus parametros globales.\"")
	b.w("    /ip traffic-flow set enabled=yes")
	b.w("}")
	b.w("/ip traffic-flow target add dst-address=%s port=%d version=ipfix src-address=%s \\", v.CollectorIP, IPFIXPort, tun)
	b.w("    v9-template-refresh=20 v9-template-timeout=1m")
	b.w("/ip traffic-flow ipfix set sys-init-time=yes first-forwarded=yes last-forwarded=yes \\")
	b.w("    nat-events=yes nat-src-address=yes nat-dst-address=yes nat-src-port=yes nat-dst-port=yes")
	b.w("")
	b.w("# ----- Deshacer (pegar para retirar el túnel de Horus) -----")
	b.w("# /ip traffic-flow target remove [find where dst-address=%s]", v.CollectorIP)
	b.w("# /snmp community remove [find where comment=\"horus\"]")
	b.w("# /ip firewall filter remove [find where comment~\"^horus \"]")
	b.w("# /ip firewall nat remove [find where comment=\"horus no-nat\"]")
	if v.APIUser != "" {
		b.w("# /user remove [find where comment=\"horus\"]; /user group remove [find where name=horus-ro]; /certificate remove [find where name=horus-api]")
	}
	if v.Transport == TransportIPsec {
		b.w("# /ip ipsec policy remove [find where comment=\"horus\"]; /ip ipsec identity remove [find where comment=\"horus\"]")
		b.w("# /ip ipsec peer remove [find where name=horus]; /ip ipsec proposal remove [find where name=horus]; /ip ipsec profile remove [find where name=horus]")
		b.w("# /ip address remove [find where comment=\"horus\"]; /interface bridge remove [find where name=horus-lo]")
	} else {
		b.w("# /ip route remove [find where comment=\"horus\"]; /ip address remove [find where comment=\"horus\"]; /interface gre remove [find where name=horus-gre]")
	}
	c.Config = b.String()
	c.Notes = append(commonNotes(v),
		"Sintaxis de RouterOS 7 según help.mikrotik.com; validada en CHR 7.12 y 7.23.8 (docs/tunnels.md §8).",
		fmt.Sprintf("El hub Horus solo acepta, por este túnel, tráfico con origen %s: IPFIX y SNMP salen con esa IP (src-address del destino de Traffic Flow).", tun))
	if v.Transport == TransportIPsec && v.RemoteAddress.IsValid() {
		c.Notes = append(c.Notes, fmt.Sprintf("El hub espera la IKE desde %s; si la IP pública cambia, actualícela en Horus (Transporte del router).", v.RemoteAddress))
	}
	return c
}
