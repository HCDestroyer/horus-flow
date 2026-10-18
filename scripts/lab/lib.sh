# shellcheck shell=bash disable=SC2034
# lib.sh — funciones comunes del laboratorio CHR (historia I0-11). Lo cargan lab.sh y
# selftest.sh; no se ejecuta solo.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../infrastructure/lab/chr/lab.env
source "$repo_root/infrastructure/lab/chr/lab.env"

case "$LAB_STATE_DIR" in
  /*) STATE="$LAB_STATE_DIR" ;;
  *) STATE="$repo_root/$LAB_STATE_DIR" ;;
esac

P="$LAB_PREFIX"
BR_WAN="$P-wan"; BR_LAN="$P-lan"
TAP_WAN="$P-tap-wan"; TAP_LAN="$P-tap-lan"
WG_IF="$P-wg"
NS_INET="$P-inet"; NS_RTR="$P-rtr"
REST="http://127.0.0.1:$LAB_REST_PORT/rest"

SUDO=""
[ "$(id -u)" -eq 0 ] || SUDO="sudo"

log() { echo "lab: $*" >&2; }
die() { echo "lab: ERROR — $*" >&2; exit 1; }

need() {
  local missing=()
  for c in "$@"; do command -v "$c" >/dev/null 2>&1 || missing+=("$c"); done
  [ "${#missing[@]}" -eq 0 ] || die "faltan herramientas: ${missing[*]} (Debian/Ubuntu: sudo apt install \
iproute2 wireguard-tools qemu-system-x86 qemu-utils curl jq unzip python3 socat snmp)"
}

client_ns() { echo "$P-cli-$1"; }
client_ip() {
  local i=11
  for c in $LAB_CLIENTS; do
    if [ "$c" = "$1" ]; then echo "$LAB_LAN_NET.$i"; return; fi
    i=$((i + 1))
  done
  die "cliente '$1' no existe (LAB_CLIENTS='$LAB_CLIENTS')"
}

random_secret() { LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 24 || true; }

# Secretos del laboratorio (nunca en Git ni en la salida).
load_secrets() {
  mkdir -p "$STATE"; chmod 700 "$STATE"
  local f="$STATE/secrets.env"
  if [ ! -s "$f" ]; then
    (umask 077; {
      echo "LAB_ADMIN_PASSWORD=$(random_secret)"
      echo "HORUS_API_PASSWORD=$(random_secret)"
      echo "SNMP_AUTH_PASS=$(random_secret)"
      echo "SNMP_PRIV_PASS=$(random_secret)"
    } >"$f")
  fi
  # shellcheck disable=SC1090
  source "$f"
  export SNMP_USER="horus-lab"
  export HORUS_API_PASSWORD SNMP_AUTH_PASS SNMP_PRIV_PASS LAB_ADMIN_PASSWORD
}

# --- REST de RouterOS por la NIC de gestión (admin del laboratorio) -------------------------
ros_get() { curl -fsS --max-time 20 -u "admin:${ROS_PASS-$LAB_ADMIN_PASSWORD}" "$REST/$1"; }
ros_post() {
  curl -fsS --max-time "${3:-60}" -u "admin:${ROS_PASS-$LAB_ADMIN_PASSWORD}" \
    -H 'Content-Type: application/json' -X POST --data "$2" "$REST/$1"
}
# Ejecuta un script RouterOS y devuelve su salida como texto (POST /rest/execute).
ros_exec() {
  local body
  body="$(jq -cn --arg s "$1" '{script: $s, "as-string": true}')"
  ros_post execute "$body" "${2:-120}" | jq -r '.ret // empty'
}

# Compara versiones "7.12.1" >= "7.12".
version_ge() { [ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -n1)" = "$2" ]; }

vm_running() { [ -f "$STATE/qemu.pid" ] && $SUDO kill -0 "$(cat "$STATE/qemu.pid")" 2>/dev/null; }

link_exists() { ip link show "$1" >/dev/null 2>&1; }

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
#
# Validado contra RouterOS 7.12 y 7.23.8 reales: la salida de `/import verbose=yes` por
# `POST /rest/execute` NO incluye "Script file loaded and executed successfully", y un error corta
# el import con un mensaje libre ("input does not match any value of interface", "expected end of
# command"...). Por eso se añade al final del archivo un centinela `:put` que solo se imprime si
# TODAS las líneas anteriores se ejecutaron.
import_sentinel() { echo "HORUS-LAB-IMPORT-OK-$1"; }
import_rsc() { # nombre (base|onboarding)
  local name="$1" out sentinel
  sentinel="$(import_sentinel "$name")"
  printf '\n:put "%s"\n' "$sentinel" >>"$STATE/rsc/$name.rsc"
  ros_exec "/tool fetch url=\"http://10.0.2.2:$LAB_FILE_PORT/$name.rsc\" dst-path=\"horus-$name.rsc\"" >/dev/null \
    || die "el CHR no pudo descargar $name.rsc del host"
  out="$(ros_exec "/import file-name=horus-$name.rsc verbose=yes" 300 || true)"
  printf '%s\n' "$out" >"$STATE/import-$name.log"
  ros_exec "/file remove [find name=\"horus-$name.rsc\"]" >/dev/null || true
  if ! grep -qx "$sentinel" "$STATE/import-$name.log" \
      || grep -qiwE "failure|syntax error|bad command|expected|no such item|invalid value|does not match" "$STATE/import-$name.log"; then
    tail -n 4 "$STATE/import-$name.log" | grep -v '^#line' | sed 's/^/lab:   /' >&2 || true
    die "el import de $name.rsc tuvo errores (§8.3.1): revisa $LAB_STATE_DIR/import-$name.log"
  fi
  log "import de $name.rsc sin errores"
}
