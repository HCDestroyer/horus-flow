# ===== Horus Flow · script inverso (desinstalación) · RouterOS v7 =====
# Router: rt-centro (0192e333-0000-7000-8000-000000000033)
# Elimina todo lo que el script de alta creó con comment="horus" y SOLO el destino de Traffic Flow de
# Horus. No toca otros destinos de Traffic Flow ni sus parámetros globales (Horus no conoce los
# valores previos: revise active-flow-timeout y cache-entries si los cambió el alta).

# 1) Destino de Traffic Flow de Horus (solo el suyo)
/ip traffic-flow target remove [find where dst-address=10.255.0.1 && port=4739 && src-address=10.255.3.17]
:if ([:len [/ip traffic-flow target find]] = 0) do={ /ip traffic-flow set enabled=no }

# 2) SNMPv3 y usuario de solo lectura
/snmp community remove [find where comment="horus" || name="horus-00000033"]
/user remove [find where comment="horus"]
/user group remove [find where comment="horus" && name="horus-ro"]

# 3) Certificados de Horus (primero se liberan los servicios que usan horus-api)
/ip service set [find where certificate="horus-api"] certificate=none
/certificate remove [find where name="horus-api" || name~"^horus-ca"]

# 4) Firewall, ruta, dirección y túnel WireGuard
/ip firewall filter remove [find where comment~"^horus"]
/ip route remove [find where comment="horus"]
/ip address remove [find where comment="horus"]
/interface wireguard peers remove [find where comment="horus"]
/interface wireguard remove [find where name="wg-horus" && comment="horus"]
:put "HORUS: configuracion de Horus eliminada."
