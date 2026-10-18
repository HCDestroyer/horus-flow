#!/bin/sh
# Horus Flow · túnel GRE · ejemplo genérico Linux (router rt-centro)
set -eu
# 1) Túnel GRE hacia el hub con la IP de túnel y ruta a la red de servicios de Horus
ip tunnel add horus0 mode gre local 198.51.100.7 remote 203.0.113.10 ttl 64 key 7
ip addr add 10.255.0.17/32 dev horus0
ip link set horus0 mtu 1400 up
ip route replace 10.255.0.0/24 dev horus0 src 10.255.0.17
iptables -t mangle -A FORWARD -o horus0 -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --set-mss 1360
# 3) Exporte NetFlow v9/IPFIX a 10.255.0.1:4739 (IPFIX) o :2055 (NetFlow v9) con origen 10.255.0.17 y SNMPv3
#    (usuario horus-00000001, SHA-256/AES-128) escuchando en 10.255.0.17 solo para 10.255.0.0/24.
