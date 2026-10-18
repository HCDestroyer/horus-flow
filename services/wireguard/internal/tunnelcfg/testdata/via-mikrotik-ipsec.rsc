# ===== Horus Flow · switch vía router · RouterOS v7 · túnel IPsec IKEv2 =====
# Switch: sw-acceso-1 (0192f0d2-0000-7000-8000-0000000000aa) · IP privada 192.168.88.2 <-> dirección Horus 10.255.1.40
# Router: rt-centro
# Revise antes de pegar. Horus nunca escribe en el router. Todo lo que se crea lleva comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"
# y al final está el bloque para deshacerlo.
# Pegarlo otra vez es seguro: cada bloque quita primero sus reglas anteriores.

# 1) NAT 1:1 192.168.88.2 <-> 10.255.1.40, solo con la red de servicios de Horus
/ip firewall nat remove [find where comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"]
/ip firewall nat add chain=dstnat ipsec-policy=in,ipsec dst-address=10.255.1.40 action=netmap to-addresses=192.168.88.2 comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"
/ip firewall nat add chain=srcnat src-address=192.168.88.2 dst-address=10.255.0.0/24 action=netmap to-addresses=10.255.1.40 comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"
{
    # Antes de cualquier masquerade: el origen hacia Horus debe ser la dirección Horus.
    :local first [:pick [/ip firewall nat find where chain=srcnat and comment!="horus-via 0192f0d2-0000-7000-8000-0000000000aa"] 0]
    :if ([:typeof $first] = "id") do={ /ip firewall nat move [find where chain=srcnat and comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"] destination=$first }
}

# 2) Política IPsec para la dirección Horus (RouterOS aplica el srcnat antes de cifrar)
/ip ipsec policy remove [find where comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"]
/ip ipsec policy add peer=horus tunnel=yes src-address=10.255.1.40/32 dst-address=10.255.0.0/24 \
    proposal=horus action=encrypt level=require comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"

# 3) Cortafuegos: solo SNMP desde Horus hacia el switch, sus respuestas y el sFlow del switch hacia el colector; el resto se descarta.
#    Van delante de la primera regla de forward ajena (revise el orden si usa listas de interfaces).
/ip firewall filter remove [find where comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"]
/ip firewall filter add chain=forward ipsec-policy=in,ipsec src-address=10.255.0.0/24 dst-address=192.168.88.2 protocol=udp dst-port=161 action=accept comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"
/ip firewall filter add chain=forward src-address=192.168.88.2 dst-address=10.255.0.0/24 protocol=udp src-port=161 action=accept comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"
/ip firewall filter add chain=forward src-address=192.168.88.2 dst-address=10.255.0.1 protocol=udp dst-port=6343 action=accept comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"
/ip firewall filter add chain=forward ipsec-policy=in,ipsec dst-address=192.168.88.2 action=drop comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"
/ip firewall filter add chain=forward src-address=192.168.88.2 dst-address=10.255.0.0/24 action=drop comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"
{
    :local first [:pick [/ip firewall filter find where chain=forward and comment!="horus-via 0192f0d2-0000-7000-8000-0000000000aa"] 0]
    :if ([:typeof $first] = "id") do={ /ip firewall filter move [find where comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"] destination=$first }
}

# ----- Deshacer (pegar para retirar este switch del camino de Horus) -----
# /ip firewall filter remove [find where comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"]
# /ip firewall nat remove [find where comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"]
# /ip ipsec policy remove [find where comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"]
