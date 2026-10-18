# ===== Horus Flow · túnel GRE · RouterOS v7 (>= 7.12) =====
# Router: rt-centro (0192f0d2-0000-7000-8000-000000000001)
# Revise antes de pegar. Todo lo que se crea lleva comment="horus" o el nombre horus/horus-*;
# al final está el bloque para deshacerlo. Horus no escribe en el router: solo lee por el túnel.

# 0) Hora (los timestamps de IPFIX dependen de NTP)
/system ntp client set enabled=yes servers=pool.ntp.org

# 1) Túnel GRE hacia el hub (sin clave: RouterOS no la admite; el hub solo acepta GRE de esta IP pública).
#    Keepalive desactivado: el hub Linux no responde keepalives de GRE.
/interface gre add name=horus-gre local-address=198.51.100.7 remote-address=203.0.113.10 mtu=1400 \
    keepalive=0s,0 clamp-tcp-mss=yes allow-fast-path=no comment="horus"
/ip address add address=10.255.0.17/32 network=10.255.0.1 interface=horus-gre comment="horus"
/ip route add dst-address=10.255.0.0/24 gateway=horus-gre comment="horus"

# 2) Sin NAT para el tráfico del túnel (origen = IP de túnel).
{
    :local first [:pick [/ip firewall nat find where chain=srcnat] 0]
    /ip firewall nat add chain=srcnat src-address=10.255.0.17 dst-address=10.255.0.0/24 action=accept comment="horus no-nat"
    :if ([:typeof $first] = "id") do={ /ip firewall nat move [find where comment="horus no-nat"] destination=$first }
}

# 4) Firewall: transporte desde el hub y gestión solo desde Horus por el túnel, delante de la
#    primera regla de input (revise el orden si usa listas de interfaces o reglas propias).
{
    :local first [:pick [/ip firewall filter find where chain=input] 0]
    /ip firewall filter add chain=input src-address=203.0.113.10 protocol=gre action=accept comment="horus gre"
    /ip firewall filter add chain=input in-interface=horus-gre src-address=10.255.0.0/24 dst-address=10.255.0.17 \
        protocol=udp dst-port=161 action=accept comment="horus snmp"
    /ip firewall filter add chain=input in-interface=horus-gre src-address=10.255.0.0/24 dst-address=10.255.0.17 \
        protocol=tcp dst-port=443,8729 action=accept comment="horus api"
    /ip firewall filter add chain=input in-interface=horus-gre src-address=10.255.0.0/24 dst-address=10.255.0.17 \
        protocol=icmp action=accept comment="horus icmp"
    :if ([:typeof $first] = "id") do={
        /ip firewall filter move [find where comment~"^horus (ike|esp|gre|snmp|api|icmp)\$"] destination=$first
    }
}

# 5) Usuario de solo lectura para la API REST (sin write, policy ni sensitive)
/user group add name=horus-ro comment="horus" \
    policy=read,api,rest-api,!write,!policy,!sensitive,!local,!telnet,!ssh,!ftp,!reboot,!test,!winbox,!password,!web,!sniff,!romon
/user add name=horus group=horus-ro password="PruebaApi12345" address=10.255.0.0/24 comment="horus"
/certificate add name=horus-api common-name=10.255.0.17 subject-alt-name=IP:10.255.0.17 \
    key-usage=digital-signature,key-encipherment,key-cert-sign,tls-server days-valid=3650
/certificate sign horus-api
/ip service set www-ssl certificate=horus-api disabled=no
/ip service set api-ssl certificate=horus-api disabled=no

# 6) SNMPv3 authPriv (SHA1 + AES), solo lectura, solo desde Horus
/snmp community add name=horus-00000001 security=private \
    authentication-protocol=SHA1 authentication-password="PruebaSnmp1234" \
    encryption-protocol=AES encryption-password="PruebaSnmp1234" \
    addresses=10.255.0.0/24 read-access=yes write-access=no comment="horus"
/snmp set enabled=yes

# 7) Traffic Flow (IPFIX, sin muestreo) hacia el colector de Horus por el túnel, con la IP de túnel de origen
:if ([:len [/ip traffic-flow target find]] = 0) do={
    /ip traffic-flow set enabled=yes interfaces=all active-flow-timeout=1m inactive-flow-timeout=15s packet-sampling=no
} else={
    :put "HORUS AVISO: Traffic Flow ya exporta a otro colector; no se cambian sus parametros globales."
    /ip traffic-flow set enabled=yes
}
/ip traffic-flow target add dst-address=10.255.0.1 port=4739 version=ipfix src-address=10.255.0.17 \
    v9-template-refresh=20 v9-template-timeout=1m
/ip traffic-flow ipfix set sys-init-time=yes first-forwarded=yes last-forwarded=yes \
    nat-events=yes nat-src-address=yes nat-dst-address=yes nat-src-port=yes nat-dst-port=yes

# ----- Deshacer (pegar para retirar el túnel de Horus) -----
# /ip traffic-flow target remove [find where dst-address=10.255.0.1]
# /snmp community remove [find where comment="horus"]
# /ip firewall filter remove [find where comment~"^horus "]
# /ip firewall nat remove [find where comment="horus no-nat"]
# /user remove [find where comment="horus"]; /user group remove [find where name=horus-ro]; /certificate remove [find where name=horus-api]
# /ip route remove [find where comment="horus"]; /ip address remove [find where comment="horus"]; /interface gre remove [find where name=horus-gre]
