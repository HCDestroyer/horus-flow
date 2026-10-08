#!/usr/bin/env bash
# selftest.sh — validación técnica del laboratorio CHR (historia I0-11, "hecho cuando";
# lista de vendors/mikrotik.md §8.3). Necesita una máquina con KVM (o LAB_ACCEL=tcg).
#
# Arranca un laboratorio LIMPIO (make lab-up), comprueba y guarda la salida en
# .lab/selftest-<ROS>.log para adjuntarla al PR:
#   §8.3.1  versión >= 7.12 y base + onboarding (§7) importados sin errores
#   §8.3.2  handshake WireGuard < 30 s; ICMP, REST (www-ssl), API-SSL y SNMPv3 responden POR el
#           túnel; REST con el usuario horus NO funciona fuera del túnel
#   §8.3.8  el usuario horus no puede escribir
#   §8.3.3/4/6  llega IPFIX con plantilla y registros, y con NAT activo aparecen IPs privadas de
#           los clientes (pre-NAT) tras generar tráfico beacon + scan
# No cubre (a mano o en I0-12): IPv6, ±2 s de NTP, flujos vs ifHCOutOctets 1 h (§8.3.5),
# PPPoE con segundo CHR (§8.3.7) ni la grabación de fixtures (§8.3.9).
#
# Uso: scripts/lab/selftest.sh   (make lab-selftest ROS=7.12)   SELFTEST_KEEP=1 no apaga al final.

set -euo pipefail
# shellcheck source-path=SCRIPTDIR source=lib.sh
source "$(dirname "$0")/lib.sh"
cd "$repo_root"

mkdir -p "$STATE"
out="$STATE/selftest-$ROS.log"
exec > >(tee "$out") 2>&1

pass=0; fail=0; skip=0
ok() { echo "PASS  $*"; pass=$((pass + 1)); }
ko() { echo "FAIL  $*"; fail=$((fail + 1)); }
sk() { echo "SKIP  $*"; skip=$((skip + 1)); }
check() { local d="$1"; shift; if "$@" >/dev/null 2>&1; then ok "$d"; else ko "$d"; fi; }

echo "== Horus Flow · selftest del laboratorio CHR · RouterOS $ROS · $(date -u +%FT%TZ) · $(uname -srm)"
[ "${SELFTEST_KEEP:-0}" = "1" ] || trap 'bash "$repo_root/scripts/lab/lab.sh" down >/dev/null 2>&1 || true' EXIT

bash "$repo_root/scripts/lab/lab.sh" down >/dev/null 2>&1 || true
if ! bash "$repo_root/scripts/lab/lab.sh" up; then
  ko "make lab-up (CHR arranca y se configura)"; echo "== RESULTADO: KO"; exit 1
fi
load_secrets
ok "lab-up: CHR arrancado, configuración base y onboarding importados sin errores (§8.3.1)"

version="$(cat "$STATE/ros-version")"
if version_ge "$version" 7.12; then ok "RouterOS $version >= 7.12 (D15)"; else ko "RouterOS $version < 7.12"; fi

# --- §8.3.2 túnel -----------------------------------------------------------------------------
start="$(cat "$STATE/configured-at")"; hs=0
while [ $(($(date +%s) - start)) -lt 30 ]; do
  hs="$($SUDO wg show "$WG_IF" latest-handshakes | awk '{print $2}' | head -n1)"
  [ "${hs:-0}" -gt 0 ] && break
  sleep 1
done
if [ "${hs:-0}" -gt 0 ]; then ok "handshake WireGuard en $(($(date +%s) - start))s (< 30 s)"; else ko "sin handshake WireGuard en 30 s"; fi

R="$LAB_ROUTER_WG_IP"
check "ICMP al router por el túnel ($R)" ping -c 3 -W 2 "$R"
code="$(curl -sk -o /dev/null -w '%{http_code}' --max-time 10 -u "horus:$HORUS_API_PASSWORD" "https://$R/rest/system/resource" || true)"
if [ "$code" = "200" ]; then ok "REST (www-ssl) por el túnel con el usuario horus: 200"; else ko "REST por el túnel: HTTP $code"; fi
check "API-SSL :8729 abierto por el túnel" timeout 5 bash -c "exec 3<>/dev/tcp/$R/8729"
if command -v snmpget >/dev/null 2>&1; then
  if snmpget -v3 -l authPriv -u "$SNMP_USER" -a SHA -A "$SNMP_AUTH_PASS" -x AES -X "$SNMP_PRIV_PASS" \
      -t 3 -r 1 "$R" 1.3.6.1.2.1.1.5.0 2>/dev/null | grep -q "$LAB_ROUTER_NAME"; then
    ok "SNMPv3 authPriv por el túnel (sysName = $LAB_ROUTER_NAME)"
  else ko "SNMPv3 por el túnel"; fi
else sk "SNMPv3: falta snmpget (apt install snmp)"; fi

code="$(curl -sk -o /dev/null -w '%{http_code}' --max-time 5 -u "horus:$HORUS_API_PASSWORD" "https://$LAB_WAN_CHR_IP/rest/system/resource" || true)"
if [ "$code" != "200" ]; then ok "REST por la WAN ($LAB_WAN_CHR_IP) fuera del túnel rechazada (HTTP $code)"; else ko "REST por la WAN responde 200"; fi
code="$(curl -sk -o /dev/null -w '%{http_code}' --max-time 5 -u "horus:$HORUS_API_PASSWORD" "https://127.0.0.1:$LAB_HTTPS_PORT/rest/system/resource" || true)"
if [ "$code" != "200" ]; then ok "REST con horus desde fuera de $LAB_SERVICES_CIDR rechazada (HTTP $code)"; else ko "horus entra desde fuera del túnel"; fi

# --- §8.3.8 solo lectura ----------------------------------------------------------------------
code="$(curl -sk -o /dev/null -w '%{http_code}' --max-time 10 -u "horus:$HORUS_API_PASSWORD" \
  -H 'Content-Type: application/json' -X POST --data '{"name":"pwned"}' "https://$R/rest/system/identity/set" || true)"
if [ "${code:0:1}" != "2" ]; then ok "el usuario horus no puede escribir (POST identity/set: HTTP $code)"; else ko "el usuario horus PUEDE escribir"; fi

# --- §8.3.3 / 8.3.4 / 8.3.6 IPFIX ---------------------------------------------------------------
probe_out="$STATE/ipfix-probe.json"
python3 -I "$repo_root/scripts/lab/ipfix-probe.py" --listen "$LAB_HUB_IP:$LAB_IPFIX_PORT" --seconds 150 \
  --exporter "$R" --expect-src "$LAB_LAN_NET.0/24" >"$probe_out" &
probe=$!
first="${LAB_CLIENTS%% *}"
PROFILE=beacon CLIENT="$first" DURATION=40 INTERVAL=2 bash "$repo_root/scripts/lab/lab.sh" traffic || true
PROFILE=scan CLIENT="$first" TRAFFIC_ARGS="--ports 200" bash "$repo_root/scripts/lab/lab.sh" traffic || true
if wait "$probe"; then
  ok "IPFIX por el túnel con plantilla, registros e IP privada del cliente pre-NAT: $(jq -c '{packets,templates,records,client_addresses_seen}' "$probe_out")"
else
  ko "IPFIX: $(cat "$probe_out" 2>/dev/null || echo 'sin salida')"
fi

echo "== $pass PASS · $fail FAIL · $skip SKIP — salida en ${out#"$repo_root"/}"
if [ "$fail" -ne 0 ]; then echo "== RESULTADO: KO"; exit 1; fi
echo "== RESULTADO: OK"
