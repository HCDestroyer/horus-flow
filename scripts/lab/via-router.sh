#!/usr/bin/env bash
# via-router.sh — valida en el laboratorio CHR la configuración que genera Horus para un switch VÍA ROUTER
# (ADR-0032, docs/tunnels.md §10): un "switch" simulado (netns hlab-sw, IP PRIVADA 10.20.0.50 en la LAN del
# CHR, respondedor UDP en 161 y 9161) detrás del CHR; el hub enruta la dirección Horus 10.255.3.40 por el peer
# WireGuard del CHR (lo mismo que aplica wg-agent: allowed-ips del peer + ruta /32); se importa via.rsc
# (NAT 1:1 netmap + cortafuegos mínimo) y se comprueba:
#   1. SNMP: el hub consulta 10.255.3.40:161 y responde el switch (que ve como origen la IP de servicios del
#      hub, no una IP traducida) con origen 10.255.3.40 (nunca 10.20.0.50).
#   2. sFlow: el switch envía a 10.255.0.1:6343 y el hub lo recibe con origen 10.255.3.40.
#   3. Cortafuegos: otro puerto UDP (9161) hacia el switch NO pasa.
#   4. Reimportar no duplica reglas; el bloque «Deshacer» deja el router sin reglas horus-via.
#
#   make lab-up LAB_ACCEL=tcg ROS=7.23.8 LAB_BOOT_TIMEOUT=900   # laboratorio con el túnel WireGuard arriba
#   sudo bash scripts/lab/via-router.sh
#
# No toca nada fuera del laboratorio; al terminar quita el netns del switch y la dirección del peer.
set -euo pipefail
# shellcheck source-path=SCRIPTDIR source=lib.sh
source "$(dirname "$0")/lib.sh"
cd "$repo_root"

SW_NS="$P-sw"
SW_IP="${HORUS_VIA_TARGET:-10.20.0.50}"
HORUS_ADDR="${HORUS_VIA_MAPPED:-10.255.3.40}"
rsc="$STATE/rsc"
http_pid=""
out="$STATE/via-router.log"

# shellcheck disable=SC2329 # se invoca por trap EXIT
cleanup() {
  [ -z "$http_pid" ] || kill "$http_pid" 2>/dev/null || true
  if ip netns list 2>/dev/null | awk '{print $1}' | grep -qx "$SW_NS"; then
    for pid in $($SUDO ip netns pids "$SW_NS" 2>/dev/null); do $SUDO kill "$pid" 2>/dev/null || true; done
    $SUDO ip netns del "$SW_NS" || true
  fi
  if link_exists "$P-v-sw"; then $SUDO ip link del "$P-v-sw" || true; fi
  if [ -n "${pub:-}" ]; then $SUDO wg set "$WG_IF" peer "$pub" allowed-ips "$LAB_ROUTER_WG_IP/32" || true; fi
  $SUDO ip route del "$HORUS_ADDR/32" dev "$WG_IF" 2>/dev/null || true
  rm -f "$rsc/via.rsc" "$rsc/via-undo.rsc"
}
trap cleanup EXIT

need curl; need jq; need python3; need wg
vm_running || die "no hay un CHR en marcha (make lab-up primero)"
load_secrets
# shellcheck source=/dev/null
source "$STATE/router.env"
pub="$HORUS_ROUTER_PUBKEY"
mkdir -p "$rsc"; chmod 700 "$rsc"
: >"$out"

# 1) "Switch": netns en la LAN del CHR con su IP privada y puerta de enlace = CHR.
$SUDO ip netns add "$SW_NS" 2>/dev/null || true
if ! link_exists "$P-v-sw"; then
  $SUDO ip link add "$P-v-sw" type veth peer name eth0 netns "$SW_NS"
  $SUDO ip link set "$P-v-sw" master "$BR_LAN" up
fi
$SUDO ip -n "$SW_NS" link set lo up
# MAC fija, como la de un switch real: con una MAC nueva en cada ejecución el CHR conserva la entrada ARP
# vieja (failed) y no entrega al switch hasta que caduca.
$SUDO ip -n "$SW_NS" link set eth0 address 52:54:00:48:46:50
$SUDO ip -n "$SW_NS" link set eth0 up
$SUDO ip -n "$SW_NS" addr replace "$SW_IP/24" dev eth0
$SUDO ip -n "$SW_NS" route replace default via "$LAB_LAN_GW"
# Respondedor: devuelve "<origen visto> <destino local>" a quien pregunte (161 = SNMP, 9161 = puerto no permitido).
$SUDO ip netns exec "$SW_NS" python3 -I -c '
import socket, threading
def serve(port):
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    s.bind(("0.0.0.0", port))
    while True:
        data, addr = s.recvfrom(2048)
        s.sendto(("%s %d" % (addr[0], port)).encode(), addr)
for p in (161, 9161):
    threading.Thread(target=serve, args=(p,), daemon=True).start()
threading.Event().wait()
' >/dev/null 2>&1 &

ros_exec "/ip arp remove [find where address=$SW_IP and dynamic]" >/dev/null 2>&1 || true

# 2) Hub: la dirección Horus viaja con el peer del CHR (lo que hace wg-agent con el estado deseado).
$SUDO wg set "$WG_IF" peer "$pub" allowed-ips "$LAB_ROUTER_WG_IP/32,$HORUS_ADDR/32"
$SUDO ip route replace "$HORUS_ADDR/32" dev "$WG_IF"

# 3) Configuración del router generada por Horus (tunnelcfg.ViaRouter) e import.
HORUS_TUNNELCFG_EMIT_DIR="$rsc" HORUS_VIA_TARGET="$SW_IP" HORUS_VIA_MAPPED="$HORUS_ADDR" \
  go test -count=1 -run TestEmitViaLab ./services/wireguard/internal/tunnelcfg/ >/dev/null
cp "$rsc/via.rsc" "$STATE/via-router.rsc"
# Validado en CHR 7.23.8 (TCG): justo después del import las reglas nuevas aparecen un momento con la marca
# I (inválida) y todavía no filtran; se espera a que el router las active.
settle() {
  local n
  for _ in $(seq 1 60); do
    n="$(ros_exec ":put [:len [/ip firewall filter find where comment~\"^horus-via\" and invalid]]" | tr -dc '0-9')"
    [ "${n:-1}" = 0 ] && { sleep 2; return 0; }
    sleep 1
  done
  log "aviso: reglas horus-via todavía inválidas tras 60 s"
}
python3 -I -m http.server "$LAB_FILE_PORT" --bind 127.0.0.1 --directory "$rsc" >/dev/null 2>&1 &
http_pid=$!
sleep 1
import_rsc via
settle

probe() { # puerto → respuesta "origen_visto puerto|origen_respuesta" o vacío
  python3 -I - "$HORUS_ADDR" "$1" "$LAB_HUB_IP" <<'PY'
import socket, sys
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.bind((sys.argv[3], 0))
s.settimeout(4)
# Tres intentos: el primer paquete espera la resolución ARP del switch en un CHR emulado (TCG).
for _ in range(3):
    s.sendto(b"horus", (sys.argv[1], int(sys.argv[2])))
    try:
        data, addr = s.recvfrom(2048)
        print(data.decode() + "|" + addr[0])
        break
    except socket.timeout:
        continue
else:
    print("")
PY
}
check() { # descripción condición
  if eval "$2"; then log "OK   $1"; echo "OK   $1" >>"$out"; else log "FALLO $1"; echo "FALLO $1" >>"$out"; fail=1; fi
}
fail=0

r="$(probe 161)"
check "SNMP por la dirección Horus llega al switch con origen la IP de servicios del hub ($r)" "[ \"\${r%% *}\" = \"$LAB_HUB_IP\" ]"
check "la respuesta vuelve con origen la dirección Horus, nunca la IP privada" "[ \"\${r##*|}\" = \"$HORUS_ADDR\" ]"
if [ "$fail" != 0 ]; then
  { ros_exec "/ip firewall filter print stats without-paging where chain=forward"
    ros_exec "/ip firewall nat print stats without-paging"
    ros_exec "/ip firewall connection print without-paging where dst-address~\"$HORUS_ADDR\""; } >>"$out" 2>&1 || true
fi
r2="$(probe 9161)"
check "otro puerto UDP hacia el switch se descarta en el router" "[ -z \"$r2\" ]"

# sFlow: el switch envía al colector; el hub debe verlo con origen la dirección Horus.
got="$(python3 -I - "$LAB_HUB_IP" "$SW_NS" "$SW_IP" <<'PY'
import socket, subprocess, sys
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.bind((sys.argv[1], 6343))
s.settimeout(6)
subprocess.run(["ip", "netns", "exec", sys.argv[2], "python3", "-I", "-c",
    "import socket; x=socket.socket(socket.AF_INET, socket.SOCK_DGRAM); x.bind(('%s', 0)); x.sendto(b'sflow', ('%s', 6343))" % (sys.argv[3], sys.argv[1])],
    check=True)
try:
    data, addr = s.recvfrom(2048)
    print(addr[0])
except socket.timeout:
    print("")
PY
)"
check "sFlow del switch llega al colector con origen la dirección Horus ($got)" "[ \"$got\" = \"$HORUS_ADDR\" ]"

# Reimportar: mismas reglas (cada bloque quita las suyas antes de crearlas).
count() { ros_exec ":put [:len [/ip firewall $1 find where comment~\"^horus-via\"]]" | tr -dc '0-9'; }
nat1="$(count nat)"; flt1="$(count filter)"
HORUS_TUNNELCFG_EMIT_DIR="$rsc" HORUS_VIA_TARGET="$SW_IP" HORUS_VIA_MAPPED="$HORUS_ADDR" \
  go test -count=1 -run TestEmitViaLab ./services/wireguard/internal/tunnelcfg/ >/dev/null
import_rsc via
settle
check "reimportar no duplica reglas (nat $nat1, filter $flt1)" "[ \"$(count nat)\" = \"$nat1\" ] && [ \"$(count filter)\" = \"$flt1\" ] && [ \"$nat1\" = 2 ]"

# Deshacer.
import_rsc via-undo
check "el bloque «Deshacer» deja el router sin reglas horus-via" "[ \"$(count nat)\" = 0 ] && [ \"$(count filter)\" = 0 ]"
r3="$(probe 161)"
check "sin las reglas, la dirección Horus ya no alcanza al switch" "[ -z \"$r3\" ]"

# 5) Sintaxis de las variantes IPsec y GRE (y de una red de gestión declarada) en el mismo CHR: sin túnel IPsec/GRE
#    real con el hub (eso lo cubre scripts/lab/tunnels.sh), se crean los objetos del túnel con los nombres que usa
#    la configuración de Horus (peer/proposal «horus», interfaz horus-gre), se importa y se deshace.
ros_exec "/interface gre add name=horus-gre local-address=$LAB_WAN_CHR_IP remote-address=$LAB_WAN_HOST_IP !keepalive comment=\"horus-lab-via\"" >/dev/null
ros_exec "/ip ipsec proposal add name=horus" >/dev/null
ros_exec "/ip ipsec peer add name=horus address=$LAB_WAN_HOST_IP/32 exchange-mode=ike2 comment=\"horus-lab-via\"" >/dev/null
for variant in "ipsec:" "gre:" "gre:subnet" "wireguard:subnet"; do
  transport="${variant%%:*}"; subnet="${variant#*:}"
  HORUS_TUNNELCFG_EMIT_DIR="$rsc" HORUS_VIA_TARGET="$SW_IP" HORUS_VIA_MAPPED="$HORUS_ADDR" HORUS_VIA_TRANSPORT="$transport" \
    HORUS_VIA_SUBNET="$subnet" go test -count=1 -run TestEmitViaLab ./services/wireguard/internal/tunnelcfg/ >/dev/null
  cp "$rsc/via.rsc" "$STATE/via-router-$transport${subnet:+-$subnet}.rsc"
  import_rsc via
  settle
  pol="$(ros_exec ":put [:len [/ip ipsec policy find where comment~\"^horus-via\"]]" | tr -dc '0-9')"
  want_pol=0; [ "$transport" = ipsec ] && want_pol=1
  check "variante $transport${subnet:+ con red declarada}: importa sin errores (nat $(count nat), filter $(count filter), políticas IPsec $pol)" \
    "[ \"$(count nat)\" = 2 ] && [ \"$(count filter)\" = 5 ] && [ \"$pol\" = $want_pol ]"
  import_rsc via-undo
  pol="$(ros_exec ":put [:len [/ip ipsec policy find where comment~\"^horus-via\"]]" | tr -dc '0-9')"
  check "variante $transport${subnet:+ con red declarada}: el bloque «Deshacer» la retira" \
    "[ \"$(count nat)\" = 0 ] && [ \"$(count filter)\" = 0 ] && [ \"$pol\" = 0 ]"
done
ros_exec "/ip ipsec peer remove [find where name=horus]" >/dev/null || true
ros_exec "/ip ipsec proposal remove [find where name=horus]" >/dev/null || true
ros_exec "/interface gre remove [find where name=horus-gre]" >/dev/null || true

log "resultado en $out"
exit "$fail"
