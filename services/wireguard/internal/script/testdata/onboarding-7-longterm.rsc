# ===== Horus Flow · onboarding de router · RouterOS v7 long-term (>= 7.18) · plantilla 7-longterm =====
# Router: rt-centro _1__x (0192e333-0000-7000-8000-000000000033)
# Revise el script antes de pegarlo. Todo lo que crea lleva comment="horus" (salvo el destino de
# Traffic Flow, que no admite comment) y se deshace con el script inverso. Contraseñas y token
# de enrolamiento se muestran UNA SOLA VEZ: guarde este archivo en un lugar seguro o bórrelo tras
# pegarlo. El token caduca a las 24 h y sirve una sola vez.
# Horus no escribe en el router: solo lee por el túnel con el usuario de solo lectura.

# 0) Hora (los timestamps de IPFIX dependen de NTP)
/system ntp client set enabled=yes servers=pool.ntp.org

# 1) Túnel WireGuard hacia Horus (la clave privada se genera aquí y nunca sale del router)
/interface wireguard add name=wg-horus mtu=1420 comment="horus"
/ip address add address=10.255.3.17/32 interface=wg-horus comment="horus"
/interface wireguard peers add interface=wg-horus name=horus-hub \
    public-key="xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=" \
    endpoint-address=horus.example.net endpoint-port=51820 \
    allowed-address=10.255.0.0/24 persistent-keepalive=25s comment="horus"
/ip route add dst-address=10.255.0.0/24 gateway=wg-horus comment="horus"

# 2) Firewall: gestión solo desde Horus por el túnel (al principio de input; revise el orden si usa
#    listas de interfaces o reglas propias)
/ip firewall filter add chain=input in-interface=wg-horus src-address=10.255.0.0/24 \
    protocol=udp dst-port=161 action=accept comment="horus snmp" place-before=0
/ip firewall filter add chain=input in-interface=wg-horus src-address=10.255.0.0/24 \
    protocol=tcp dst-port=443,8729 action=accept comment="horus api" place-before=0
/ip firewall filter add chain=input in-interface=wg-horus src-address=10.255.0.0/24 \
    protocol=icmp action=accept comment="horus icmp" place-before=0

# 3) Usuario de solo lectura para la API (sin write, policy ni sensitive)
/user group add name=horus-ro comment="horus" \
    policy=read,api,rest-api,!write,!policy,!sensitive,!local,!telnet,!ssh,!ftp,!reboot,!test,!winbox,!password,!web,!sniff,!romon
/user add name=horus group=horus-ro password="Api9pass9word9Api9pass9w" \
    address=10.255.0.0/24 comment="horus"

# 4) Certificado autofirmado para REST (www-ssl) y API-SSL (Horus fija su huella en el primer contacto)
/certificate add name=horus-api common-name=10.255.3.17 subject-alt-name=IP:10.255.3.17 \
    key-usage=digital-signature,key-encipherment,tls-server days-valid=3650
/certificate sign horus-api
/ip service set www-ssl certificate=horus-api disabled=no
/ip service set api-ssl certificate=horus-api disabled=no
# Restringir orígenes: AÑADA 10.255.0.0/24 a la lista 'address' existente de cada servicio
# (no la sustituya si ya gestiona por esos servicios). Si no hay restricción previa:
# /ip service set www-ssl address=10.255.0.0/24
# /ip service set api-ssl address=10.255.0.0/24

# 5) SNMPv3 authPriv (SHA1 + AES), solo lectura, solo desde Horus
/snmp community add name=horus-00000033 security=private \
    authentication-protocol=SHA1 authentication-password="Auth9pass9word9Auth9pass" \
    encryption-protocol=AES encryption-password="Priv9pass9word9Priv9pass" \
    addresses=10.255.0.0/24 read-access=yes write-access=no comment="horus"
/snmp set enabled=yes
# Recomendado (decisión del ISP): deshabilitar la comunidad 'public' por defecto
# /snmp community set [find default=yes] disabled=yes

# 6) Traffic Flow (IPFIX, sin muestreo) hacia el colector de Horus por el túnel.
#    Los parámetros globales son comunes a todos los destinos: si ya exporta a otro colector no se
#    cambian (revise active-flow-timeout=1m e inactive-flow-timeout=15s con el asistente).
:if ([:len [/ip traffic-flow target find]] = 0) do={
    /ip traffic-flow set enabled=yes interfaces=all cache-entries=256k \
        active-flow-timeout=1m inactive-flow-timeout=15s packet-sampling=no
} else={
    :put "HORUS AVISO: Traffic Flow ya exporta a otro colector; no se cambian sus parametros globales."
    /ip traffic-flow set enabled=yes
}
/ip traffic-flow target add dst-address=10.255.0.1 port=4739 version=ipfix \
    src-address=10.255.3.17 v9-template-refresh=20 v9-template-timeout=1m
# Campos IPFIX, incluidos los NAT: con NAT/CGNAT en este router la bajada solo se atribuye al cliente
# por postNATDestinationIPv4Address (verificado con un router real, docs/traffic-model.md §4.4.3).
/ip traffic-flow ipfix set sys-init-time=yes first-forwarded=yes last-forwarded=yes \
    src-mac-address=yes dst-mac-address=yes \
    nat-events=yes nat-src-address=yes nat-dst-address=yes nat-src-port=yes nat-dst-port=yes

# 7) Enrolamiento: envía SOLO la clave pública del router a Horus con el token de un uso
{
    :local pk [/interface wireguard get [find name=wg-horus] public-key]
    :put ("HORUS_ROUTER_PUBKEY=" . $pk)
    /tool fetch url="https://horus.example.net/api/v1/enroll/wireguard" http-method=post check-certificate=yes \
        http-header-field="Content-Type: application/json" \
        http-data=("{\"token\":\"q1w2e3r4t5y6u7i8o9p0a1s2d3f4g5h6j7k8l9z0x1c\",\"public_key\":\"" . $pk . "\"}") output=none
    :put "HORUS: clave publica enviada; el tunel se activa en segundos."
}

# 8) IPv6 (opcional, comentado). Traffic Flow exporta IPv4 e IPv6 con el mismo target.
# /ipv6 settings print
# /ipv6 pool print
# /ppp profile print where dhcpv6-pd-pool!="" || remote-ipv6-prefix-pool!=""
# /ipv6 dhcp-server binding print count-only
# ¿Hay NAT66? (Horus no lo soporta en v1: la bajada traducida no se atribuye)
# /ipv6 firewall nat print where disabled=no
# /ip traffic-flow ipfix set ipv6-flow-label=yes icmp-type=yes icmp-code=yes \
#     src-address-mask=yes dst-address-mask=yes
# (en la rama long-term icmp-code puede no existir: compruebe con /ip traffic-flow ipfix print)
