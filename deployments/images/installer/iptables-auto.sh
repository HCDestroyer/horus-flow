#!/bin/sh
# iptables-auto — iptables del ANFITRIÓN desde horus-host (red del anfitrión + NET_ADMIN).
#
# Se instala como /usr/local/sbin/iptables en la imagen horus-installer. Las reglas deben ir al mismo
# backend que usan el sistema y Docker (cadena DOCKER-USER): iptables-nft (Debian 11+, Ubuntu 20.04+,
# Proxmox 8) o iptables-legacy. Se elige el que ya tiene más reglas y se recuerda en
# /run/horus-iptables-backend; HORUS_IPTABLES_BACKEND=nft|legacy lo fuerza.
set -e
f=/run/horus-iptables-backend
b="${HORUS_IPTABLES_BACKEND:-}"
[ -n "$b" ] || b="$(cat "$f" 2>/dev/null || true)"
if [ -z "$b" ]; then
  n="$( (xtables-nft-multi iptables-save 2>/dev/null || true) | grep -c '^-A' || true)"
  l="$( (xtables-legacy-multi iptables-save 2>/dev/null || true) | grep -c '^-A' || true)"
  if [ "${l:-0}" -gt "${n:-0}" ]; then b=legacy; else b=nft; fi
  echo "$b" >"$f" 2>/dev/null || true
fi
case "$b" in nft | legacy) ;; *) echo "iptables-auto: backend inválido: $b (nft | legacy)" >&2; exit 2 ;; esac
exec "xtables-$b-multi" iptables "$@"
