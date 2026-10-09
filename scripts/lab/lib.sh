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
