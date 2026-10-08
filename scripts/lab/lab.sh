#!/usr/bin/env bash
# lab.sh — laboratorio MikroTik CHR reproducible (historia I0-11, vendors/mikrotik.md §8).
#
#   lab.sh up        red + hub WireGuard + CHR (QEMU) + configuración base + onboarding (§7)
#   lab.sh down      apaga la VM y borra la red del laboratorio (conserva la caché de imágenes)
#   lab.sh status    estado de la VM, el túnel y las netns
#   lab.sh traffic   genera tráfico desde un cliente: PROFILE=beacon|scan|smtp|volume|dns CLIENT=a
#   lab.sh console   consola serie del CHR (salir con Ctrl-])
#
# Topología (§8.2, mapa de NICs en infrastructure/lab/chr/base.rsc.tmpl):
#
#   netns <P>-cli-{a,b,c} ── bridge <P>-lan ── ether3-lan  CHR  ether2-wan ── bridge <P>-wan ─┬─ netns <P>-inet
#   10.20.0.11-13                               10.20.0.1  (NAT) 198.18.0.2                  ├─ host 198.18.0.1:51820
#                                                          ether1-mgmt: QEMU user-net         │  (hub WG <P>-wg 10.255.0.1)
#                                                          REST/SSH en 127.0.0.1:1808x        └─ (IPFIX llega por el túnel
#                                                                                                 a 10.255.0.1:4739/udp)
#
# Requisitos: Linux, root o sudo, /dev/kvm (o LAB_ACCEL=tcg), iproute2, wireguard-tools,
# qemu-system-x86, qemu-utils, curl, jq, unzip, python3; socat para `console`.
# Variables: infrastructure/lab/chr/lab.env (ROS, LAB_ACCEL, puertos y redes).
# LAB_ROUTER=netns sustituye el CHR por una netns Linux con NAT: SOLO para probar la red y los
# generadores de tráfico en máquinas sin KVM ni imagen; no valida nada de RouterOS.

set -euo pipefail
# shellcheck source-path=SCRIPTDIR source=lib.sh
source "$(dirname "$0")/lib.sh"
cd "$repo_root"

LAB_ROUTER="${LAB_ROUTER:-chr}"

# --------------------------------------------------------------------------------------------
preflight() {
  [ "$(uname -s)" = "Linux" ] || die "el laboratorio necesita Linux (netns, bridges, taps)"
  need ip python3 curl jq
  if [ "$LAB_ROUTER" = "chr" ]; then
    need qemu-system-x86_64 qemu-img wg unzip
    if [ "$LAB_ACCEL" = "kvm" ] && { [ ! -e /dev/kvm ] || ! $SUDO test -w /dev/kvm; }; then
      cat >&2 <<EOF
lab: ERROR — esta máquina no tiene /dev/kvm utilizable: el CHR no arranca con KVM.
  - Sin virtualización, prueba el decodificador con los fixtures grabados:
      make sim-verify            (selftest de tools/flowsim y fixtures; luego los de I0-12)
  - Si aun así quieres arrancar el CHR emulado (muy lento, minutos de arranque):
      make lab-up LAB_ACCEL=tcg
  - Para habilitar KVM: virtualización activada en la BIOS, módulo kvm_intel/kvm_amd cargado y
    tu usuario en el grupo 'kvm' (o ejecuta con sudo).
EOF
      exit 2
    fi
  fi
  [ -z "$SUDO" ] || sudo -n true 2>/dev/null || log "se pedirá la contraseña de sudo (red y QEMU)"
}

# --- Red ------------------------------------------------------------------------------------
link_exists() { ip link show "$1" >/dev/null 2>&1; }
ns_exists() { ip netns list 2>/dev/null | awk '{print $1}' | grep -qx "$1"; }

add_ns_on_bridge() { # netns bridge ip/len [gw]
  local ns="$1" br="$2" addr="$3" gw="${4:-}" short="${1#"$P"-}"
  ns_exists "$ns" || $SUDO ip netns add "$ns"
  if ! link_exists "$P-v-$short"; then
    $SUDO ip link add "$P-v-$short" type veth peer name eth0 netns "$ns"
    $SUDO ip link set "$P-v-$short" master "$br" up
  fi
  $SUDO ip -n "$ns" link set lo up
  $SUDO ip -n "$ns" link set eth0 up
  $SUDO ip -n "$ns" addr replace "$addr" dev eth0
  [ -z "$gw" ] || $SUDO ip -n "$ns" route replace default via "$gw"
}

# Con Docker instalado, FORWARD tiene política DROP y br_netfilter pasa por iptables también el
# tráfico puenteado: sin estas reglas los clientes no llegan al router dentro del mismo bridge.
bridge_fw_allow() {
  command -v iptables >/dev/null 2>&1 || return 0
  for br in "$BR_WAN" "$BR_LAN"; do
    $SUDO iptables -C FORWARD -i "$br" -o "$br" -m comment --comment horus-lab -j ACCEPT 2>/dev/null \
      || $SUDO iptables -I FORWARD 1 -i "$br" -o "$br" -m comment --comment horus-lab -j ACCEPT
  done
}

bridge_fw_remove() {
  command -v iptables >/dev/null 2>&1 || return 0
  for br in "$BR_WAN" "$BR_LAN"; do
    while $SUDO iptables -D FORWARD -i "$br" -o "$br" -m comment --comment horus-lab -j ACCEPT 2>/dev/null; do :; done
  done
}

net_up() {
  for br in "$BR_WAN" "$BR_LAN"; do
    link_exists "$br" || $SUDO ip link add "$br" type bridge
    $SUDO ip link set "$br" up
  done
  $SUDO ip addr replace "$LAB_WAN_HOST_IP/24" dev "$BR_WAN"
  bridge_fw_allow


  # internet-sim: la respuesta vuelve a la IP de la WAN del router (NAT masquerade).
  add_ns_on_bridge "$NS_INET" "$BR_WAN" "$LAB_INET_IP/24"
  if [ -z "$($SUDO ip netns pids "$NS_INET" 2>/dev/null)" ]; then
    $SUDO ip netns exec "$NS_INET" nohup python3 -I "$repo_root/scripts/lab/internet-sim.py" \
      >"$STATE/internet-sim.log" 2>&1 &
  fi

  for c in $LAB_CLIENTS; do
    add_ns_on_bridge "$(client_ns "$c")" "$BR_LAN" "$(client_ip "$c")/24" "$LAB_LAN_GW"
  done
  log "red lista: $BR_WAN ($LAB_WAN_NET.0/24), $BR_LAN ($LAB_LAN_NET.0/24), clientes: $LAB_CLIENTS"
}

net_down() {
  for ns in $(ip netns list 2>/dev/null | awk '{print $1}' | grep "^$P-" || true); do
    for pid in $($SUDO ip netns pids "$ns" 2>/dev/null); do $SUDO kill "$pid" 2>/dev/null || true; done
    $SUDO ip netns del "$ns" || true
  done
  for l in "$TAP_WAN" "$TAP_LAN" "$WG_IF" "$BR_WAN" "$BR_LAN"; do
    if link_exists "$l"; then $SUDO ip link del "$l" || true; fi
  done
  bridge_fw_remove
}

# --- Router de sustitución (LAB_ROUTER=netns): solo fontanería ------------------------------
stub_router_up() {
  add_ns_on_bridge "$NS_RTR" "$BR_WAN" "$LAB_WAN_CHR_IP/24"
  if ! $SUDO ip -n "$NS_RTR" link show lan >/dev/null 2>&1; then
    $SUDO ip link add "$P-v-rtrlan" type veth peer name lan netns "$NS_RTR"
    $SUDO ip link set "$P-v-rtrlan" master "$BR_LAN" up
  fi
  $SUDO ip -n "$NS_RTR" link set lan up
  $SUDO ip -n "$NS_RTR" addr replace "$LAB_LAN_GW/24" dev lan
  $SUDO ip netns exec "$NS_RTR" sysctl -qw net.ipv4.ip_forward=1
  if command -v nft >/dev/null 2>&1; then
    $SUDO ip netns exec "$NS_RTR" nft add table ip horuslab
    $SUDO ip netns exec "$NS_RTR" nft add chain ip horuslab post '{ type nat hook postrouting priority 100 ; }'
    $SUDO ip netns exec "$NS_RTR" nft add rule ip horuslab post oifname eth0 masquerade
  elif command -v iptables >/dev/null 2>&1; then
    $SUDO ip netns exec "$NS_RTR" iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE
  else
    log "aviso: sin nft ni iptables, el router de sustitución enruta SIN NAT"
  fi
  log "router de sustitución (netns $NS_RTR) listo — no es RouterOS: no valida §8.3"
}

# --- Hub WireGuard (hace de despliegue de Horus, D16) ---------------------------------------
wg_up() {
  mkdir -p "$STATE/wg"
  if [ ! -s "$STATE/wg/hub.key" ]; then
    (umask 077; wg genkey >"$STATE/wg/hub.key")
  fi
  wg pubkey <"$STATE/wg/hub.key" >"$STATE/wg/hub.pub"
  if ! link_exists "$WG_IF" && ! $SUDO ip link add "$WG_IF" type wireguard 2>/dev/null; then
    # Sin módulo del kernel (contenedores, WSL...): implementación en espacio de usuario.
    command -v wireguard-go >/dev/null 2>&1 \
      || die "el kernel no soporta WireGuard y falta wireguard-go (apt install wireguard-go)"
    $SUDO wireguard-go "$WG_IF" || die "wireguard-go no pudo crear $WG_IF"
    log "aviso: WireGuard en espacio de usuario (wireguard-go)"
  fi
  $SUDO wg set "$WG_IF" listen-port "$LAB_WG_PORT" private-key "$STATE/wg/hub.key"
  $SUDO ip addr replace "$LAB_HUB_IP/${LAB_SERVICES_CIDR#*/}" dev "$WG_IF"
  $SUDO ip link set "$WG_IF" up
  $SUDO ip route replace "$LAB_ROUTER_WG_IP/32" dev "$WG_IF"
  log "hub WireGuard $WG_IF en $LAB_WAN_HOST_IP:$LAB_WG_PORT ($LAB_HUB_IP)"
}

# --- VM CHR ---------------------------------------------------------------------------------
vm_up() {
  local img disk accel_args=()
  img="$(bash "$repo_root/scripts/lab/fetch-chr.sh" "$ROS" | tail -n1)"
  disk="$STATE/chr-$ROS.qcow2"
  # Disco de capa sobre la imagen verificada: cada lab-up arranca un CHR LIMPIO (§8.3.1).
  rm -f "$disk"
  qemu-img create -q -f qcow2 -F raw -b "$img" "$disk"

  for t in "$TAP_WAN:$BR_WAN" "$TAP_LAN:$BR_LAN"; do
    link_exists "${t%%:*}" || $SUDO ip tuntap add dev "${t%%:*}" mode tap
    $SUDO ip link set "${t%%:*}" master "${t#*:}" up
  done

  if [ "$LAB_ACCEL" = "kvm" ]; then accel_args=(-accel kvm -cpu host); else
    accel_args=(-accel tcg); log "aviso: QEMU TCG (sin KVM): el arranque puede tardar varios minutos"
  fi
  rm -f "$STATE/console.sock" "$STATE/monitor.sock"
  $SUDO qemu-system-x86_64 -name "horus-lab-chr-$ROS" -machine pc "${accel_args[@]}" \
    -m "$LAB_VM_MEM" -smp "$LAB_VM_CPUS" \
    -drive "file=$disk,format=qcow2,if=ide" \
    -netdev "user,id=mgmt,net=10.0.2.0/24,hostfwd=tcp:127.0.0.1:$LAB_REST_PORT-:80,hostfwd=tcp:127.0.0.1:$LAB_HTTPS_PORT-:443,hostfwd=tcp:127.0.0.1:$LAB_SSH_PORT-:22" \
    -device virtio-net-pci,netdev=mgmt,mac=52:54:00:48:46:01 \
    -netdev "tap,id=wan,ifname=$TAP_WAN,script=no,downscript=no" \
    -device virtio-net-pci,netdev=wan,mac=52:54:00:48:46:02 \
    -netdev "tap,id=lan,ifname=$TAP_LAN,script=no,downscript=no" \
    -device virtio-net-pci,netdev=lan,mac=52:54:00:48:46:03 \
    -display none -serial "unix:$STATE/console.sock,server=on,wait=off" \
    -monitor "unix:$STATE/monitor.sock,server=on,wait=off" \
    -pidfile "$STATE/qemu.pid" -daemonize
  $SUDO chown "$(id -u):$(id -g)" "$STATE/qemu.pid" "$STATE/console.sock" "$STATE/monitor.sock" 2>/dev/null || true
  log "CHR $ROS arrancado (pid $(cat "$STATE/qemu.pid"), accel $LAB_ACCEL)"
}

# Espera a la REST del CHR limpio (admin sin contraseña) y fija la contraseña del laboratorio.
wait_rest() {
  local start t0 res version
  start="$(date +%s)"
  log "esperando la REST de RouterOS en 127.0.0.1:$LAB_REST_PORT (hasta ${LAB_BOOT_TIMEOUT}s)..."
  until res="$(ROS_PASS="" ros_get system/resource 2>/dev/null || ros_get system/resource 2>/dev/null)"; do
    vm_running || die "QEMU terminó durante el arranque (mira 'make lab-console' o el log de QEMU)"
    if [ $(($(date +%s) - start)) -ge "$LAB_BOOT_TIMEOUT" ]; then
      die "la REST no respondió en ${LAB_BOOT_TIMEOUT}s. Entra con 'make lab-console' (admin, sin
  contraseña) y comprueba: /ip dhcp-client print (ether1 con 10.0.2.15) y /ip service print (www)."
    fi
    sleep 3
  done
  t0=$(($(date +%s) - start))
  version="$(printf '%s' "$res" | jq -r '.version' | awk '{print $1}')"
  echo "$version" >"$STATE/ros-version"
  log "RouterOS $version responde por REST tras ${t0}s"
  version_ge "$version" "7.12" || die "RouterOS $version < 7.12 (D15)"
  if ROS_PASS="" ros_get system/resource >/dev/null 2>&1; then
    ROS_PASS="" ros_post user/set "$(jq -cn --arg p "$LAB_ADMIN_PASSWORD" '{numbers: "admin", password: $p}')" >/dev/null \
      || die "no se pudo fijar la contraseña de admin por REST"
    log "contraseña de admin del laboratorio fijada (en $LAB_STATE_DIR/secrets.env)"
  fi
}

# Sirve un .rsc al CHR por la NIC de gestión, lo importa con /import verbose y revisa errores.
import_rsc() { # nombre (base|onboarding)
  local name="$1" out
  ros_exec "/tool fetch url=\"http://10.0.2.2:$LAB_FILE_PORT/$name.rsc\" dst-path=\"horus-$name.rsc\"" >/dev/null \
    || die "el CHR no pudo descargar $name.rsc del host"
  out="$(ros_exec "/import file-name=horus-$name.rsc verbose=yes" 300 || true)"
  printf '%s\n' "$out" >"$STATE/import-$name.log"
  ros_exec "/file remove [find name=\"horus-$name.rsc\"]" >/dev/null || true
  if ! grep -qi "executed successfully" "$STATE/import-$name.log" \
      || grep -qiE "failure|syntax error|bad command|expected|no such item|invalid value" "$STATE/import-$name.log"; then
    die "el import de $name.rsc tuvo errores (§8.3.1): revisa $LAB_STATE_DIR/import-$name.log"
  fi
  log "import de $name.rsc sin errores"
}

configure() {
  export ROUTER_NAME="$LAB_ROUTER_NAME" NTP_SERVER="$LAB_NTP_SERVER" ROUTER_WG_IP="$LAB_ROUTER_WG_IP" \
    HORUS_WG_PUBKEY HORUS_WG_ENDPOINT="$LAB_WAN_HOST_IP" HORUS_WG_PORT="$LAB_WG_PORT" \
    HORUS_SERVICES_CIDR="$LAB_SERVICES_CIDR" HORUS_COLLECTOR_IP="$LAB_HUB_IP" \
    LAB_WAN_CHR_IP LAB_LAN_GW
  HORUS_WG_PUBKEY="$(cat "$STATE/wg/hub.pub")"
  local rsc="$STATE/rsc"; mkdir -p "$rsc"; chmod 700 "$rsc"
  python3 -I "$repo_root/scripts/lab/render-rsc.py" base "$rsc/base.rsc"
  python3 -I "$repo_root/scripts/lab/render-rsc.py" onboarding "$rsc/onboarding.rsc"

  # Servidor temporal solo en el loopback (QEMU user-net lo expone al CHR como 10.0.2.2).
  python3 -I -m http.server "$LAB_FILE_PORT" --bind 127.0.0.1 --directory "$rsc" >/dev/null 2>&1 &
  local http_pid=$!
  # shellcheck disable=SC2064
  trap "kill $http_pid 2>/dev/null || true" EXIT
  sleep 1
  import_rsc base
  import_rsc onboarding
  kill "$http_pid" 2>/dev/null || true
  trap - EXIT
  rm -f "$rsc/onboarding.rsc"   # contiene secretos del laboratorio

  local pub
  pub="$(ros_get "interface/wireguard?name=wg-horus" | jq -r '.[0]["public-key"] // empty')"
  [ -n "$pub" ] || die "no se pudo leer la clave pública de wg-horus del CHR"
  $SUDO wg set "$WG_IF" peer "$pub" allowed-ips "$LAB_ROUTER_WG_IP/32"
  echo "HORUS_ROUTER_PUBKEY=$pub" >"$STATE/router.env"
  date +%s >"$STATE/configured-at"
  log "peer del router añadido al hub (HORUS_ROUTER_PUBKEY=$pub)"
}

# --------------------------------------------------------------------------------------------
cmd_up() {
  preflight
  mkdir -p "$STATE"; chmod 700 "$STATE"
  load_secrets
  net_up
  if [ "$LAB_ROUTER" = "netns" ]; then
    stub_router_up
  else
    vm_running && die "ya hay un laboratorio en marcha (make lab-down primero)"
    wg_up
    vm_up
    wait_rest
    configure
  fi
  cat <<EOF
lab: listo ($LAB_ROUTER${LAB_ROUTER/chr/ $ROS}).
  consola serie:  make lab-console           REST admin: $REST (admin / $LAB_STATE_DIR/secrets.env)
  tráfico:        make lab-traffic PROFILE=scan CLIENT=a
  IPFIX:          llega por el túnel a $LAB_HUB_IP:$LAB_IPFIX_PORT/udp (p. ej. make sim-verify SIM_LISTEN=$LAB_HUB_IP:$LAB_IPFIX_PORT ...)
  validación:     make lab-selftest           apagar: make lab-down
EOF
}

cmd_down() {
  if vm_running; then
    $SUDO kill "$(cat "$STATE/qemu.pid")" || true
    log "CHR detenido"
  fi
  rm -f "$STATE/qemu.pid" "$STATE/console.sock" "$STATE/monitor.sock" "$STATE"/chr-*.qcow2 "$STATE/configured-at"
  net_down
  log "red del laboratorio eliminada (la caché de imágenes en $CHR_CACHE_DIR se conserva)"
}

cmd_status() {
  if vm_running; then echo "CHR: en marcha (pid $(cat "$STATE/qemu.pid"), RouterOS $(cat "$STATE/ros-version" 2>/dev/null || echo '?'))"
  else echo "CHR: parado"; fi
  ip netns list 2>/dev/null | grep "^$P-" | sed 's/^/netns: /' || echo "netns: ninguna"
  if link_exists "$WG_IF"; then $SUDO wg show "$WG_IF"; else echo "hub WireGuard: no existe"; fi
}

cmd_traffic() {
  local profile="${PROFILE:-beacon}" client="${CLIENT:-a}" ns
  local extra=()
  read -r -a extra <<<"${TRAFFIC_ARGS:-}"
  ns="$(client_ns "$client")"
  ns_exists "$ns" || die "la netns $ns no existe (¿make lab-up?)"
  log "perfil $profile desde $ns ($(client_ip "$client")) hacia internet-sim $LAB_INET_IP"
  $SUDO ip netns exec "$ns" python3 -I "$repo_root/scripts/lab/traffic.py" --profile "$profile" \
    --target "$LAB_INET_IP" --duration "${DURATION:-60}" --interval "${INTERVAL:-5}" "${extra[@]}"
}

cmd_console() {
  need socat
  [ -S "$STATE/console.sock" ] || die "no hay consola (¿make lab-up?)"
  echo "consola serie del CHR (usuario admin; salir con Ctrl-])" >&2
  $SUDO socat -,raw,echo=0,escape=0x1d "UNIX-CONNECT:$STATE/console.sock"
}

case "${1:-}" in
  up) cmd_up ;;
  down) cmd_down ;;
  status) cmd_status ;;
  traffic) cmd_traffic ;;
  console) cmd_console ;;
  *) sed -n '2,9p' "$0" | sed 's/^# \{0,1\}//'; exit 2 ;;
esac
