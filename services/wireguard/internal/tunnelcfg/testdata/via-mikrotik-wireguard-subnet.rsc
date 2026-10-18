# ===== Horus Flow · switch vía router · RouterOS v7 · túnel WireGuard =====
# Switch: sw-acceso-1 (0192f0d2-0000-7000-8000-0000000000aa) · IP privada 192.168.88.2 <-> dirección Horus 10.255.1.18
# Router: rt-centro
# Revise antes de pegar. Horus nunca escribe en el router. Todo lo que se crea lleva comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"
# (y comment="horus-via-net 0192f0d2-0000-7000-8000-0000000000bb" para la red de gestión); al final está el bloque para deshacerlo.
# Pegarlo otra vez es seguro: cada bloque quita primero sus reglas anteriores.

# 1) NAT 1:1 de la red de gestión 192.168.88.0/28 <-> bloque Horus 10.255.1.16/28, solo con la red de servicios de Horus
/ip firewall nat remove [find where comment="horus-via-net 0192f0d2-0000-7000-8000-0000000000bb"]
/ip firewall nat add chain=dstnat in-interface=wg-horus dst-address=10.255.1.16/28 action=netmap to-addresses=192.168.88.0/28 comment="horus-via-net 0192f0d2-0000-7000-8000-0000000000bb"
/ip firewall nat add chain=srcnat out-interface=wg-horus src-address=192.168.88.0/28 dst-address=10.255.0.0/24 action=netmap to-addresses=10.255.1.16/28 comment="horus-via-net 0192f0d2-0000-7000-8000-0000000000bb"
{
    # La red va después de las reglas por switch (que van primero) y antes de cualquier masquerade.
    :local first [:pick [/ip firewall nat find where chain=srcnat and !(comment~"^horus-via")] 0]
    :if ([:typeof $first] = "id") do={ /ip firewall nat move [find where chain=srcnat and comment="horus-via-net 0192f0d2-0000-7000-8000-0000000000bb"] destination=$first }
}

# 2) Cortafuegos: solo SNMP desde Horus hacia el switch, sus respuestas; el resto se descarta.
#    Van delante de la primera regla de forward ajena (revise el orden si usa listas de interfaces).
/ip firewall filter remove [find where comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"]
/ip firewall filter add chain=forward in-interface=wg-horus src-address=10.255.0.0/24 dst-address=192.168.88.2 protocol=udp dst-port=161 action=accept comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"
/ip firewall filter add chain=forward out-interface=wg-horus src-address=192.168.88.2 dst-address=10.255.0.0/24 protocol=udp src-port=161 action=accept comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"
/ip firewall filter add chain=forward in-interface=wg-horus dst-address=192.168.88.2 action=drop comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"
/ip firewall filter add chain=forward out-interface=wg-horus src-address=192.168.88.2 dst-address=10.255.0.0/24 action=drop comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"
{
    :local first [:pick [/ip firewall filter find where chain=forward and comment!="horus-via 0192f0d2-0000-7000-8000-0000000000aa"] 0]
    :if ([:typeof $first] = "id") do={ /ip firewall filter move [find where comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"] destination=$first }
}

# ----- Deshacer (pegar para retirar este switch del camino de Horus) -----
# /ip firewall filter remove [find where comment="horus-via 0192f0d2-0000-7000-8000-0000000000aa"]
# Y, cuando ya no quede ningún switch de la red de gestión 192.168.88.0/28:
# /ip firewall nat remove [find where comment="horus-via-net 0192f0d2-0000-7000-8000-0000000000bb"]
