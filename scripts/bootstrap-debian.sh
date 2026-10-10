#!/usr/bin/env bash
# bootstrap-debian.sh — prepara un Debian recién instalado y ejecuta el instalador de Horus Flow
# (historia I1-22; guía para personas no programadoras: docs/install-debian.md).
#
# Comando único (como root, en Debian 12 bookworm o 13 trixie, amd64 o arm64):
#
#   apt-get install -y curl && curl -fsSLO https://github.com/hcdestroyer/horus-flow/releases/latest/download/bootstrap-debian.sh && bash bootstrap-debian.sh
#
# Qué hace (se puede repetir sin romper nada):
#   1. Comprueba que es Debian 12/13 (amd64/arm64) y que se ejecuta como root.
#   2. Instala por apt: Docker Engine y el plugin compose DESDE EL REPOSITORIO OFICIAL DE DOCKER
#      (clave GPG verificada por huella; nunca get.docker.com), wireguard-tools, age, curl, jq,
#      ca-certificates, iptables, iproute2, openssl y la hora por NTP (systemd-timesyncd, o chrony
#      si ya está). Opcional: unattended-upgrades (--unattended-upgrades).
#   3. Instala cosign (versión fijada y verificada por SHA-256) para comprobar las firmas.
#   4. Comprueba el módulo WireGuard del kernel.
#   5. Obtiene la versión de Horus: la release publicada (instalador verificado con cosign y
#      SHA256SUMS; imágenes de GHCR fijadas por digest), el paquete offline (--bundle) o el árbol
#      en el que está este script.
#   6. Ejecuta scripts/install.sh de esa versión con el resto de opciones (los sysctl del búfer UDP
#      e ip_forward los aplica install.sh en /etc/sysctl.d/90-horus.conf).
#
# Opciones propias (el resto se pasa tal cual a install.sh: --mode, --domain, --admin-email…):
#   --version X.Y.Z        versión a instalar (por defecto la última del canal)
#   --channel stable|beta  canal (stable); también se guarda para las actualizaciones
#   --bundle FICHERO       paquete offline horus-<versión>-linux-<arch>.tar.gz (sin GHCR ni GitHub)
#   --from DIR             árbol ya descargado o repositorio clonado (pruebas)
#   --registry-token-file F  token de lectura (read:packages) si el repo o GHCR son privados
#   --unattended-upgrades  activa las actualizaciones automáticas de seguridad de Debian
#   --no-apt               no instala paquetes (ya gestionados aparte); solo comprueba que están
#   --prepare-only         prepara el sistema y no ejecuta install.sh
#   -h | --help
#
# Variables: HORUS_REPO (hcdestroyer/horus-flow), HORUS_RELEASE_BASE_URL, HORUS_SKIP_SIGNATURE=1
# (solo pruebas), DOCKER_APT_URL (https://download.docker.com/linux/debian; un espejo propio).
# Códigos de salida: 0 bien, 1 fallo, 2 uso incorrecto.

# /etc/os-release existe solo en el servidor.
# shellcheck disable=SC1091
set -euo pipefail
umask 022

self="$(readlink -f "$0")"
repo="${HORUS_REPO:-hcdestroyer/horus-flow}"
docker_apt="${DOCKER_APT_URL:-https://download.docker.com/linux/debian}"
# Clave de firma del repositorio de Docker (https://docs.docker.com/engine/install/debian/).
docker_key_fpr="9DC858229FC7DD38854AE2D88D81803C0EBFCD88"
cosign_version="v2.6.1"
declare -A cosign_sha256=(
  [amd64]=064954c5d8c7e3b28188eee5b1727b31c411550bc5fefd41aa672d3c761d103a
  [arm64]=56a16480bdd56ec789abaa65924402f6b92c0041f06885995853c05567b76f34
)

say() { printf '==> %s\n' "$*"; }
ok() { printf '  OK    %s\n' "$*"; }
warn() { printf '  AVISO %s\n' "$*"; }
die() { printf 'bootstrap-debian.sh: %s\n' "$*" >&2; exit 1; }
usage() { sed -n '2,36p' "$self" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }

version="" channel="" bundle="" from="" token_file="" unattended=0 no_apt=0 prepare_only=0
pass=()
while [ "$#" -gt 0 ]; do
  case "$1" in
    --version) version="${2#v}"; shift 2 ;;
    --channel) channel="$2"; pass+=(--channel "$2"); shift 2 ;;
    --bundle) bundle="$2"; shift 2 ;;
    --from) from="$2"; shift 2 ;;
    --registry-token-file) token_file="$2"; pass+=(--registry-token-file "$2"); shift 2 ;;
    --unattended-upgrades) unattended=1; shift ;;
    --no-apt) no_apt=1; shift ;;
    --prepare-only) prepare_only=1; shift ;;
    -h | --help) usage 0 ;;
    *) pass+=("$1"); shift ;;
  esac
done

# --- 1. Sistema ---------------------------------------------------------------------------------
say "Sistema"
[ "$(id -u)" = 0 ] || die "ejecútalo como root (su - o sudo bash bootstrap-debian.sh)"
[ -r /etc/os-release ] || die "no encuentro /etc/os-release: ¿es Debian?"
os_id="$(. /etc/os-release && echo "${ID:-}")"
os_ver="$(. /etc/os-release && echo "${VERSION_ID:-}")"
codename="$(. /etc/os-release && echo "${VERSION_CODENAME:-}")"
case "$os_id:$os_ver" in
  debian:12 | debian:13) ok "Debian $os_ver ($codename)" ;;
  *) die "sistema no soportado: $os_id $os_ver (este script es para Debian 12 o 13; en otros, instala Docker y usa scripts/install.sh)" ;;
esac
arch="$(dpkg --print-architecture)"
case "$arch" in amd64 | arm64) ok "arquitectura $arch" ;; *) die "arquitectura no soportada: $arch (amd64 o arm64)" ;; esac
exec 9>/run/lock/horus-bootstrap.lock 2>/dev/null || exec 9>/tmp/horus-bootstrap.lock
flock -n 9 || die "ya hay otro bootstrap-debian.sh en marcha"

# --- 2. Paquetes ----------------------------------------------------------------------------------
export DEBIAN_FRONTEND=noninteractive
apt_get() { apt-get -q -y -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold "$@"; }
base_pkgs=(ca-certificates curl gpg jq age wireguard-tools iptables iproute2 openssl util-linux procps kmod tar gzip)
docker_pkgs=(docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin)

docker_repo() {
  install -d -m 0755 /etc/apt/keyrings
  local tmp; tmp="$(mktemp)"
  curl -fsSL --retry 3 "$docker_apt/gpg" -o "$tmp" || die "no se pudo descargar la clave de $docker_apt (¿salida a Internet o proxy?)"
  gpg --show-keys --with-colons "$tmp" 2>/dev/null | awk -F: '$1 == "fpr" { print $10 }' | grep -qx "$docker_key_fpr" \
    || { rm -f "$tmp"; die "la clave de $docker_apt no tiene la huella esperada $docker_key_fpr"; }
  install -m 0644 "$tmp" /etc/apt/keyrings/docker.asc; rm -f "$tmp"
  cat >/etc/apt/sources.list.d/docker.sources.new <<EOF
# Repositorio oficial de Docker (bootstrap-debian.sh de Horus Flow).
Types: deb
URIs: $docker_apt
Suites: $codename
Components: stable
Architectures: $arch
Signed-By: /etc/apt/keyrings/docker.asc
EOF
  mv -f /etc/apt/sources.list.d/docker.sources.new /etc/apt/sources.list.d/docker.sources
  rm -f /etc/apt/sources.list.d/docker.list
  ok "repositorio de Docker ($docker_apt $codename, clave $docker_key_fpr)"
}

if [ "$no_apt" = 1 ]; then
  say "Paquetes (--no-apt: solo se comprueban)"
  for t in docker curl jq openssl ip flock sysctl; do command -v "$t" >/dev/null 2>&1 || die "falta $t (quita --no-apt para instalarlo)"; done
  docker compose version >/dev/null 2>&1 || die "falta el plugin docker compose"
  ok "herramientas presentes"
else
  say "Paquetes de Debian"
  apt_get update >/dev/null || die "apt-get update falló (¿sin red, proxy o espejo de Debian?)"
  apt_get install --no-install-recommends "${base_pkgs[@]}" >/dev/null || die "no se pudieron instalar: ${base_pkgs[*]}"
  ok "${base_pkgs[*]}"
  # Hora: los flujos llevan marcas de tiempo; sin NTP los informes y la detección se desvían.
  if dpkg -s chrony >/dev/null 2>&1; then
    systemctl enable --now chrony >/dev/null 2>&1 || true; ok "hora: chrony"
  else
    apt_get install --no-install-recommends systemd-timesyncd >/dev/null || die "no se pudo instalar systemd-timesyncd"
    timedatectl set-ntp true >/dev/null 2>&1 || systemctl enable --now systemd-timesyncd >/dev/null 2>&1 || true
    ok "hora: systemd-timesyncd (NTP)"
  fi
  say "Docker Engine (repositorio oficial)"
  for p in docker.io docker-doc docker-compose podman-docker containerd runc; do
    if dpkg -s "$p" >/dev/null 2>&1; then apt_get remove "$p" >/dev/null || true; warn "retirado el paquete $p de Debian (conflicto con Docker oficial)"; fi
  done
  docker_repo
  apt_get update >/dev/null || die "apt-get update falló tras añadir el repositorio de Docker"
  apt_get install "${docker_pkgs[@]}" >/dev/null || die "no se pudo instalar Docker: ${docker_pkgs[*]}"
  ok "$(dpkg-query -W -f '${Package} ${Version}' docker-ce), $(dpkg-query -W -f '${Package} ${Version}' docker-compose-plugin)"
  if [ "$unattended" = 1 ]; then
    apt_get install --no-install-recommends unattended-upgrades >/dev/null || die "no se pudo instalar unattended-upgrades"
    printf 'APT::Periodic::Update-Package-Lists "1";\nAPT::Periodic::Unattended-Upgrade "1";\n' >/etc/apt/apt.conf.d/20auto-upgrades
    ok "unattended-upgrades: parches de seguridad de Debian automáticos"
  fi
fi
if [ -d /run/systemd/system ]; then
  systemctl enable --now docker.service >/dev/null 2>&1 || die "no se pudo arrancar docker.service (journalctl -u docker)"
fi
for _ in $(seq 1 30); do docker info >/dev/null 2>&1 && break; sleep 1; done
docker info >/dev/null 2>&1 || die "el daemon de Docker no responde"
ok "Docker $(docker version --format '{{.Server.Version}}') en marcha; $(docker compose version --short 2>/dev/null || echo compose)"

# --- 3. cosign (firmas de la release) -----------------------------------------------------------
install_cosign() {
  if command -v cosign >/dev/null 2>&1; then ok "cosign $(cosign version 2>/dev/null | awk '/GitVersion/ { print $2 }')"; return 0; fi
  local tmp; tmp="$(mktemp)"
  if ! curl -fsSL --retry 3 "https://github.com/sigstore/cosign/releases/download/$cosign_version/cosign-linux-$arch" -o "$tmp"; then
    rm -f "$tmp"; return 1
  fi
  echo "${cosign_sha256[$arch]}  $tmp" | sha256sum -c --quiet - || { rm -f "$tmp"; die "SHA-256 de cosign $cosign_version no coincide"; }
  install -m 0755 "$tmp" /usr/local/bin/cosign; rm -f "$tmp"
  ok "cosign $cosign_version (/usr/local/bin/cosign, SHA-256 verificado)"
}

# --- 4. WireGuard -------------------------------------------------------------------------------
say "WireGuard"
if modprobe wireguard 2>/dev/null || [ -d /sys/module/wireguard ]; then
  ok "módulo wireguard del kernel"
  echo wireguard >/etc/modules-load.d/horus-wireguard.conf
elif command -v wireguard-go >/dev/null 2>&1; then
  warn "sin módulo wireguard en el kernel: se usará wireguard-go (más CPU)"
else
  [ "$no_apt" = 1 ] || apt_get install --no-install-recommends wireguard-go >/dev/null 2>&1 || true
  if command -v wireguard-go >/dev/null 2>&1; then warn "sin módulo wireguard en el kernel: instalado wireguard-go (más CPU)"
  else die "sin WireGuard: el kernel de Debian lo incluye; ¿es un contenedor o un kernel recortado? (apt install linux-image-$arch)"; fi
fi

[ "$prepare_only" = 0 ] || { say "Sistema preparado (--prepare-only): ejecuta scripts/install.sh de la versión que quieras"; exit 0; }

# --- 5. Versión de Horus --------------------------------------------------------------------------
cache=/var/cache/horus
auth=()
[ -z "$token_file" ] || { [ -r "$token_file" ] || die "no puedo leer $token_file"; auth=(-H "Authorization: Bearer $(head -n1 "$token_file" | tr -d '\r\n')"); }
here="$(cd "$(dirname "$self")/.." && pwd)"
tree=""
if [ -n "$from" ]; then
  tree="$(readlink -f "$from")"
elif [ -n "$bundle" ]; then
  say "Paquete offline $bundle"
  f="$(readlink -f "$bundle")"; [ -f "$f" ] || die "no existe $bundle"
  if [ -f "$(dirname "$f")/SHA256SUMS" ]; then
    (cd "$(dirname "$f")" && grep " [*]\{0,1\}$(basename "$f")\$" SHA256SUMS | sha256sum -c --quiet -) || die "SHA-256 de $(basename "$f") no coincide con SHA256SUMS"
    ok "SHA-256 de $(basename "$f") verificado"
  else
    warn "sin SHA256SUMS junto al paquete: se verifica solo su contenido"
  fi
  # Solo los scripts (install.sh verifica y extrae el resto y carga las imágenes en streaming).
  tree="$cache/bootstrap/$(basename "$f" .tar.gz)"
  install -d -m 0700 "$cache/bootstrap"; rm -rf "$tree"; install -d -m 0755 "$tree"
  top="$({ tar -tzf "$f" 2>/dev/null || true; } | head -1)"; top="${top%%/*}"
  tar -xzf "$f" -C "$tree" --strip-components=1 "$top/scripts" "$top/VERSION" || die "$(basename "$f") no es un paquete de Horus"
  ok "paquete $(cat "$tree/VERSION")"
  pass=(--image-source "bundle:$f" "${pass[@]}")
elif [ -f "$here/VERSION" ] && [ -f "$here/scripts/install.sh" ] && [ -f "$here/images.lock" ]; then
  tree="$here"   # este script viene dentro de un paquete ya extraído
else
  install_cosign || warn "no se pudo instalar cosign: no se podrán verificar las firmas"
  say "Release de Horus Flow"
  base="${HORUS_RELEASE_BASE_URL:-https://github.com/$repo/releases/download}"
  if [ -z "$version" ]; then
    want="${channel:-stable}"
    version="$(curl -fsSL --retry 2 "${auth[@]}" -H 'Accept: application/vnd.github+json' "https://api.github.com/repos/$repo/releases?per_page=50" \
      | jq -r --arg ch "$want" '[.[] | select(.draft | not) | select($ch == "beta" or (.prerelease | not)) | .tag_name | ltrimstr("v")
          | select(test("^[0-9]+\\.[0-9]+\\.[0-9]+(-[0-9A-Za-z.-]+)?$"))]
          | sort_by([(split("-")[0] | split(".") | map(tonumber)), (if test("-") then 0 else 1 end)]) | last // empty')" \
      || die "no se pudo consultar las releases de $repo (¿sin Internet? usa --bundle)"
    [ -n "$version" ] || die "no hay releases publicadas en el canal $want"
  fi
  tree="$cache/releases/$version"; dl="$tree.dl"
  rm -rf "$dl"; install -d -m 0700 "$dl"
  for a in SHA256SUMS SHA256SUMS.sigstore.json "horus-$version-installer.tar.gz"; do
    if [ "${#auth[@]}" -gt 0 ] && [ -z "${HORUS_RELEASE_BASE_URL:-}" ]; then
      id="$(curl -fsSL "${auth[@]}" "https://api.github.com/repos/$repo/releases/tags/v$version" | jq -r --arg n "$a" '.assets[] | select(.name == $n) | .id')"
      curl -fsSL "${auth[@]}" -H 'Accept: application/octet-stream' "https://api.github.com/repos/$repo/releases/assets/$id" -o "$dl/$a"
    else
      curl -fsSL --retry 3 "$base/v$version/$a" -o "$dl/$a"
    fi || die "no se pudo descargar $a de v$version"
  done
  if [ "${HORUS_SKIP_SIGNATURE:-0}" = 1 ]; then
    warn "HORUS_SKIP_SIGNATURE=1: no se verifica la firma"
  else
    command -v cosign >/dev/null 2>&1 || die "falta cosign para verificar la firma de la release"
    cosign verify-blob --bundle "$dl/SHA256SUMS.sigstore.json" \
      --certificate-oidc-issuer https://token.actions.githubusercontent.com \
      --certificate-identity-regexp "^https://github.com/$repo/.github/workflows/release.yml@refs/tags/v" \
      "$dl/SHA256SUMS" >/dev/null 2>&1 || die "la firma de SHA256SUMS de v$version no es válida"
    ok "firma cosign de la release v$version verificada"
  fi
  (cd "$dl" && grep " [*]\{0,1\}horus-$version-installer.tar.gz\$" SHA256SUMS | sha256sum -c --quiet -) || die "SHA-256 del instalador no coincide"
  rm -rf "$tree"; install -d -m 0755 "$tree"
  tar -xzf "$dl/horus-$version-installer.tar.gz" -C "$tree" --strip-components=1
  rm -rf "$dl"
  ok "Horus Flow $version (instalador verificado) en $tree"
  pass=(--image-source ghcr "${pass[@]}")
fi
[ -x "$tree/scripts/install.sh" ] || [ -f "$tree/scripts/install.sh" ] || die "$tree no contiene scripts/install.sh"

# --- 6. Instalador --------------------------------------------------------------------------------
say "Instalador de Horus Flow ($tree)"
exec bash "$tree/scripts/install.sh" "${pass[@]}"
