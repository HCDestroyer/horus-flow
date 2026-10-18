# ===== Horus Flow · túnel IPsec IKEv2 · RouterOS v7 (>= 7.12) =====
# Router: rt-centro (0192f0d2-0000-7000-8000-000000000001)
# Revise antes de pegar. Todo lo que se crea lleva comment="horus" o el nombre horus/horus-*;
# al final está el bloque para deshacerlo. Horus no escribe en el router: solo lee por el túnel.

# 0) Hora (los timestamps de IPFIX dependen de NTP)
/system ntp client set enabled=yes servers=pool.ntp.org

# 1) IP de túnel en una interfaz lógica (bridge vacío = loopback)
/interface bridge add name=horus-lo protocol-mode=none comment="horus"
/ip address add address=10.255.0.17/32 interface=horus-lo comment="horus"

# 2) IKEv2 con PSK hacia el hub (NAT-T automático, DPD 30 s, rekey IKE 4 h / ESP 1 h, PFS ECP-384)
/ip ipsec profile add name=horus hash-algorithm=sha256 enc-algorithm=aes-256 dh-group=ecp384,modp2048 \
    lifetime=4h dpd-interval=30s dpd-maximum-failures=5 nat-traversal=yes
/ip ipsec proposal add name=horus auth-algorithms=sha256 enc-algorithms=aes-256-gcm,aes-256-cbc \
    pfs-group=ecp384 lifetime=1h
/ip ipsec peer add name=horus address=203.0.113.10/32 exchange-mode=ike2 profile=horus comment="horus"
/ip ipsec identity add peer=horus auth-method=pre-shared-key secret="PruebaPruebaPruebaPruebaPruebaPruebaPruebaPrueba" \
    my-id=fqdn:rt-centro.isp.example remote-id=address:203.0.113.10 generate-policy=no comment="horus"
/ip ipsec policy add peer=horus tunnel=yes src-address=10.255.0.17/32 dst-address=10.255.0.0/24 \
    proposal=horus action=encrypt level=require comment="horus"

# 3) Sin NAT para el tráfico del túnel: un masquerade en la WAN cambiaría el origen y la
#    política IPsec ya no coincidiría (RouterOS aplica srcnat antes de cifrar).
{
    :local first [:pick [/ip firewall nat find where chain=srcnat] 0]
    /ip firewall nat add chain=srcnat src-address=10.255.0.17 dst-address=10.255.0.0/24 action=accept comment="horus no-nat"
    :if ([:typeof $first] = "id") do={ /ip firewall nat move [find where comment="horus no-nat"] destination=$first }
}

# 4) Firewall: transporte desde el hub y gestión solo desde Horus por el túnel, delante de la
#    primera regla de input (revise el orden si usa listas de interfaces o reglas propias).
{
    :local first [:pick [/ip firewall filter find where chain=input] 0]
    /ip firewall filter add chain=input src-address=203.0.113.10 protocol=udp dst-port=500,4500 action=accept comment="horus ike"
    /ip firewall filter add chain=input src-address=203.0.113.10 protocol=ipsec-esp action=accept comment="horus esp"
    /ip firewall filter add chain=input ipsec-policy=in,ipsec src-address=10.255.0.0/24 dst-address=10.255.0.17 \
        protocol=udp dst-port=161 action=accept comment="horus snmp"
    /ip firewall filter add chain=input ipsec-policy=in,ipsec src-address=10.255.0.0/24 dst-address=10.255.0.17 \
        protocol=tcp dst-port=443,8729 action=accept comment="horus api"
    /ip firewall filter add chain=input ipsec-policy=in,ipsec src-address=10.255.0.0/24 dst-address=10.255.0.17 \
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
# /ip ipsec policy remove [find where comment="horus"]; /ip ipsec identity remove [find where comment="horus"]
# /ip ipsec peer remove [find where name=horus]; /ip ipsec proposal remove [find where name=horus]; /ip ipsec profile remove [find where name=horus]
# /ip address remove [find where comment="horus"]; /interface bridge remove [find where name=horus-lo]
