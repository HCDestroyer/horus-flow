#!/usr/bin/env bash
# sim-router.sh — "routers" simulados para la aceptación de I1 (historia I1-24).
#
# Cada router simulado es un namespace de red (hfsim-<nombre>) con una interfaz WireGuard
# (wireguard-go o módulo del kernel) configurada con los valores del SCRIPT DE ONBOARDING que
# genera Horus: su IP de túnel /32, la clave pública y el endpoint del hub y la red de servicios
# (IP del colector). La clave privada se genera aquí y nunca sale (como en el MikroTik); la
# pública se registra con POST /api/v1/enroll/wireguard. Así los flujos del simulador llegan al
# colector por el MISMO camino que los de un router real: túnel WireGuard → wg del hub → filtro
# DOCKER-USER → horus-collector, con la IP de túnel como origen (identidad del exportador).
#
#   sim-router.sh up <nombre> <índice> <ip_túnel> <clave_pública_hub> <endpoint> <puerto> <cidr_servicios>
#        → imprime PUBKEY=<clave pública del router simulado>
#   sim-router.sh exec <nombre> <orden…>      ejecuta la orden dentro del namespace
#   sim-router.sh handshake <nombre>          segundos desde el último handshake (o "never")
#   sim-router.sh down-all                    borra todos los routers simulados
#
# Necesita root, iproute2, wireguard-tools (wg) y el módulo wireguard o wireguard-go.
# Variables: SIM_ROUTER_DIR (claves; bin/accept-i1/sim). Para convivir con otra batería en el mismo host
# (p. ej. make chaos-restart-core mientras corre accept-i1): SIM_ROUTER_NS_PREFIX (hfsim-),
# SIM_ROUTER_IF_PREFIX (hfsim; ≤ 6 caracteres) y SIM_ROUTER_LINK_NET (169.254.77).

set -euo pipefail

prefix="${SIM_ROUTER_NS_PREFIX:-hfsim-}"
ifp="${SIM_ROUTER_IF_PREFIX:-hfsim}"
linknet="${SIM_ROUTER_LINK_NET:-169.254.77}"
dir="${SIM_ROUTER_DIR:-$(cd "$(dirname "$0")/../.." && pwd)/bin/accept-i1/sim}"
mkdir -p "$dir"
chmod 0700 "$dir"

die() { echo "sim-router: $*" >&2; exit 1; }
[ "$(id -u)" = 0 ] || die "necesita root (namespaces de red y WireGuard)"

ns_of() { printf '%s%s' "$prefix" "$1"; }

up() {
  [ "$#" = 7 ] || die "uso: up <nombre> <índice> <ip_túnel> <clave_hub> <endpoint> <puerto> <cidr_servicios>"
  local name="$1" idx="$2" tip="$3" hub="$4" ep="$5" port="$6" svc="$7"
  local ns veth_h veth_n base host_ip ns_ip
  ns="$(ns_of "$name")"
  [[ "$idx" =~ ^[0-9]+$ ]] && [ "$idx" -lt 60 ] || die "índice inválido: $idx"
  veth_h="${ifp}h$idx" veth_n="${ifp}n$idx"
  # Nombre de interfaz único en el host: el socket UAPI de wireguard-go
  # (/var/run/wireguard/<nombre>.sock) no está aislado por namespace.
  local wgi="${ifp}wg$idx"
  echo "$wgi" >"$dir/$name.if"
  # Enlace host ↔ namespace por un /30 de 169.254.77.0/24 (el host es el servidor de Horus).
  base=$((idx * 4))
  host_ip="$linknet.$((base + 1))" ns_ip="$linknet.$((base + 2))"
  ip netns del "$ns" 2>/dev/null || true
  ip link del "$veth_h" 2>/dev/null || true
  ip netns add "$ns"
  ip -n "$ns" link set lo up
  ip link add "$veth_h" type veth peer name "$veth_n"
  ip link set "$veth_n" netns "$ns"
  ip addr add "$host_ip/30" dev "$veth_h"
  ip link set "$veth_h" up
  ip -n "$ns" addr add "$ns_ip/30" dev "$veth_n"
  ip -n "$ns" link set "$veth_n" up
  ip -n "$ns" route add default via "$host_ip"
  # El endpoint del script es la IP pública del servidor; si es el loopback (instalación de
  # prueba con --public-ip 127.0.0.1) desde el namespace se llega por la IP del host en el veth.
  case "$ep" in 127.* | localhost) ep="$host_ip" ;; esac
  # Clave privada generada aquí (como /interface wireguard add en RouterOS); nunca sale.
  umask 077
  wg genkey >"$dir/$name.key"
  wg pubkey <"$dir/$name.key" >"$dir/$name.pub"
  rm -f "/var/run/wireguard/$wgi.sock"
  if ! ip -n "$ns" link add "$wgi" type wireguard 2>/dev/null; then
    command -v wireguard-go >/dev/null 2>&1 || die "sin módulo wireguard ni wireguard-go"
    ip netns exec "$ns" env WG_I_PREFER_BUGGY_USERSPACE_TO_POLISHED_KMOD=1 setsid wireguard-go -f "$wgi" \
      </dev/null >>"$dir/$name.wireguard-go.log" 2>&1 &
    local i=0
    until ip -n "$ns" link show "$wgi" >/dev/null 2>&1; do
      i=$((i + 1)); [ "$i" -lt 50 ] || die "wireguard-go no creó $wgi en $ns"; sleep 0.2
    done
  fi
  ip netns exec "$ns" wg set "$wgi" private-key "$dir/$name.key" \
    peer "$hub" endpoint "$ep:$port" allowed-ips "$svc" persistent-keepalive 5
  ip -n "$ns" addr add "$tip/32" dev "$wgi"
  ip -n "$ns" link set "$wgi" mtu 1420 up
  ip -n "$ns" route add "$svc" dev "$wgi"
  echo "PUBKEY=$(cat "$dir/$name.pub")"
}

handshake() {
  local ns t
  ns="$(ns_of "$1")"
  t="$(ip netns exec "$ns" wg show "$(cat "$dir/$1.if" 2>/dev/null)" latest-handshakes 2>/dev/null | awk '{ print $2 }' | head -1)"
  if [ -z "$t" ] || [ "$t" = 0 ]; then echo never; else echo $(($(date +%s) - t)); fi
}

down_all() {
  local ns
  for ns in $(ip netns list 2>/dev/null | awk '{ print $1 }' | grep "^$prefix" || true); do
    ip netns pids "$ns" 2>/dev/null | xargs -r kill 2>/dev/null || true
    ip netns del "$ns" 2>/dev/null || true
  done
  for l in $(ip -o link show 2>/dev/null | awk -F': ' '{ print $2 }' | cut -d@ -f1 | grep "^${ifp}h[0-9]" || true); do
    ip link del "$l" 2>/dev/null || true
  done
  rm -f "$dir"/*.key "$dir"/*.if /var/run/wireguard/"$ifp"wg*.sock
}

cmd="${1:-}"
[ -n "$cmd" ] && shift
case "$cmd" in
  up) up "$@" ;;
  exec)
    [ "$#" -ge 2 ] || die "uso: exec <nombre> <orden…>"
    ns="$(ns_of "$1")"; shift
    exec ip netns exec "$ns" "$@"
    ;;
  handshake) [ "$#" = 1 ] || die "uso: handshake <nombre>"; handshake "$1" ;;
  down-all) down_all ;;
  *) sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; exit 2 ;;
esac
