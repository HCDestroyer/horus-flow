#!/usr/bin/env bash
# tunnels.sh — valida en un CHR limpio la configuración IPsec IKEv2 y GRE que genera Horus para RouterOS
# (docs/tunnels.md §8): importa ipsec.rsc, comprueba que el CHR establece la IKE SA con un hub charon
# (imagen horus-ipsec, red del host, configurado SOLO por el adaptador vici de tunnel-agent), deshace la
# configuración con su propio bloque «Deshacer», importa gre.rsc y comprueba la interfaz GRE.
#
#   sudo ROS=7.12 LAB_ACCEL=tcg LAB_BOOT_TIMEOUT=900 bash scripts/lab/tunnels.sh
#
# Requisitos: los del laboratorio (infrastructure/lab/chr/README.md), Docker y la imagen horus-ipsec
# (docker build -t horus-ipsec:local deployments/images/strongswan). Sin módulo esp4 en el kernel del host
# la CHILD SA no se instala (se valida IKE y la negociación ESP); sin ip_gre el hub no levanta su lado GRE
# (se valida la sintaxis RouterOS y la interfaz en el CHR).
set -euo pipefail
# shellcheck source-path=SCRIPTDIR source=lib.sh
source "$(dirname "$0")/lib.sh"
cd "$repo_root"

image="${HORUS_IPSEC_IMAGE:-horus-ipsec:local}"
hub=hlab-ipsec-hub
psk="$(printf 'Prueba%.0s' 1 2 3 4 5 6 7 8)" # PSK de prueba de baja entropía (no es un secreto)
rsc="$STATE/rsc" # import_rsc (lib.sh) sirve y lee desde aquí
http_pid=""

cleanup() {
  [ -z "$http_pid" ] || kill "$http_pid" 2>/dev/null || true
  docker rm -f "$hub" >/dev/null 2>&1 || true
  if vm_running; then $SUDO kill "$(cat "$STATE/qemu.pid")" 2>/dev/null || true; fi
  for l in "$TAP_WAN" "$TAP_LAN" "$BR_WAN" "$BR_LAN"; do
    if link_exists "$l"; then $SUDO ip link del "$l" || true; fi
  done
  rm -rf "$rsc"
}
[ -n "${KEEP:-}" ] || trap cleanup EXIT

need qemu-system-x86_64; need qemu-img; need curl; need jq; need python3; need docker
docker image inspect "$image" >/dev/null 2>&1 || die "falta la imagen $image (docker build -t $image deployments/images/strongswan)"
mkdir -p "$STATE" "$rsc"; chmod 700 "$STATE" "$rsc"
load_secrets
vm_running && die "ya hay un CHR en marcha (make lab-down primero)"

# Red: solo los bridges (el host es 198.18.0.1 en la WAN del CHR = IP pública del hub).
for br in "$BR_WAN" "$BR_LAN"; do
  link_exists "$br" || $SUDO ip link add "$br" type bridge
  $SUDO ip link set "$br" up
done
$SUDO ip addr replace "$LAB_WAN_HOST_IP/24" dev "$BR_WAN"

# Hub: charon en la red del host, socket vici en el estado del laboratorio.
mkdir -p "$STATE/ipsec-hub"
docker rm -f "$hub" >/dev/null 2>&1 || true
docker run -d --name "$hub" --network host --cap-drop ALL --cap-add NET_ADMIN --cap-add NET_BIND_SERVICE \
  --cap-add SETUID --cap-add SETGID --cap-add CHOWN --cap-add SETPCAP -v "$STATE/ipsec-hub:/run/horus-ipsec" "$image" >/dev/null
for _ in $(seq 1 50); do [ -S "$STATE/ipsec-hub/charon.vici" ] && break; sleep 0.2; done

vm_up
wait_rest

# Configuración base del laboratorio (WAN 198.18.0.2 con masquerade y drop final en input) y las del túnel.
export ROUTER_NAME="$LAB_ROUTER_NAME" LAB_WAN_CHR_IP LAB_LAN_GW
python3 -I "$repo_root/scripts/lab/render-rsc.py" base "$rsc/base.rsc"
HORUS_TUNNELCFG_EMIT_DIR="$rsc" HORUS_TUNNELCFG_PSK="$psk" go test -count=1 -run TestEmitLab ./services/wireguard/internal/tunnelcfg/ >/dev/null
python3 -I -m http.server "$LAB_FILE_PORT" --bind 127.0.0.1 --directory "$rsc" >/dev/null 2>&1 &
http_pid=$!
sleep 1
import_rsc base

# IPsec: el test carga la conexión del hub por vici y espera la IKE SA que inicia el CHR al importar.
expect_child=1
if ! $SUDO ip xfrm state add src 192.0.2.1 dst 192.0.2.2 proto esp spi 0x100 mode tunnel \
    auth 'hmac(sha256)' 0x3030303030303030303030303030303030303030303030303030303030303030 \
    enc 'cbc(aes)' 0x3030303030303030303030303030303030303030303030303030303030303030 2>/dev/null; then
  expect_child=0
  log "aviso: el kernel del host no admite SA ESP (esp4): se valida IKEv2 y la negociación ESP, no la CHILD SA"
else
  $SUDO ip xfrm state delete src 192.0.2.1 dst 192.0.2.2 proto esp spi 0x100
fi
HORUS_TEST_VICI_SOCKET="$STATE/ipsec-hub/charon.vici" HORUS_TEST_ROUTER_IP="$LAB_WAN_CHR_IP" HORUS_TEST_HUB_ID="$LAB_WAN_HOST_IP" \
  HORUS_TEST_ROUTER_ID="fqdn:horus-lab-chr.lab" HORUS_TEST_PSK="$psk" HORUS_TEST_EXPECT_CHILD="$expect_child" HORUS_TEST_SA_TIMEOUT=300s \
  go test -count=1 -v -tags=ipsec -run TestIKEv2AgainstStrongSwan ./services/tunnelagent/internal/adapters/vici/ >"$STATE/tunnels-ipsec-test.log" 2>&1 &
test_pid=$!
sleep 3
import_rsc ipsec
ros_get ip/ipsec/peer | jq -c '.[] | {name, address, "exchange-mode"}' >&2 || true
if wait "$test_pid"; then log "IPsec: la IKE SA con el CHR se estableció ($(grep -o 'selected proposal: [A-Z]*:[^ ]*' "$STATE/tunnels-ipsec-test.log" | tr '\n' ' '))"
else tail -n 30 "$STATE/tunnels-ipsec-test.log" >&2; die "IPsec: sin IKE SA con el CHR (log en $STATE/tunnels-ipsec-test.log)"; fi
import_rsc ipsec-undo
[ "$(ros_get ip/ipsec/peer | jq 'length')" = 0 ] || die "el bloque «Deshacer» de IPsec dejó peers"
[ "$(ros_get 'ip/firewall/filter?comment=horus%20ike' | jq 'length')" = 0 ] || die "el bloque «Deshacer» dejó reglas"

# GRE: sintaxis y estado de la interfaz en el CHR (el hub sin ip_gre no responde).
import_rsc gre
g="$(ros_get 'interface/gre?name=horus-gre')"
[ "$(printf '%s' "$g" | jq -r '.[0]["remote-address"]')" = "$LAB_WAN_HOST_IP" ] || die "GRE: interfaz sin crear: $g"
log "GRE: interfaz $(printf '%s' "$g" | jq -c '.[0] | {name, "local-address", "remote-address", mtu, keepalive, "clamp-tcp-mss"}')"
import_rsc gre-undo
[ "$(ros_get 'interface/gre?name=horus-gre' | jq 'length')" = 0 ] || die "el bloque «Deshacer» de GRE dejó la interfaz"
log "tunnels.sh: RouterOS $(cat "$STATE/ros-version"): IPsec IKEv2 y GRE validados (imports sin errores, deshacer incluido)"
