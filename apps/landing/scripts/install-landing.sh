#!/usr/bin/env bash
# install-landing.sh — instala y mantiene la landing comercial de Horus Flow con su panel /admin.
#
# Debian 12/13 (o Ubuntu 22.04+), como root. Instala Docker si falta (repositorio oficial), prepara
# los secretos, construye la imagen, arranca el contenedor detrás de Nginx Proxy Manager (o en un
# puerto) y crea el primer administrador.
#
#   sudo bash install-landing.sh                       # instalación interactiva
#   sudo bash install-landing.sh install --yes --domain horusflow.kns.gt --admin-email info@kns.gt
#   sudo bash install-landing.sh update                # nueva versión (copia previa de la base de datos)
#   sudo bash install-landing.sh status | logs [-f] | backup | restore ARCHIVO
#   sudo bash install-landing.sh admin list | admin reset-totp --email correo
#   sudo bash install-landing.sh uninstall [--purge]
#
# Opciones de install:
#   --domain DOMINIO            dominio público (horusflow.kns.gt)
#   --admin-email CORREO        primer administrador (info@kns.gt)
#   --admin-password-file F     su contraseña (≥ 12 caracteres); sin ella se genera y se muestra una vez
#   --mode npm|port             npm: red de Docker de Nginx Proxy Manager, sin puerto publicado (defecto)
#                               port: publica un puerto para otro proxy (Caddy, nginx, Traefik…)
#   --proxy-network RED         red de NPM (npm_default; se detecta si hay un contenedor de NPM)
#   --port [IP:]PUERTO          modo port (127.0.0.1:3000)
#   --trusted-proxies CIDR,…    proxies de confianza para la IP real (se calcula según el modo)
#   --smtp-host H --smtp-port P --smtp-user U --smtp-pass-file F --smtp-secure true|false
#   --mail-from "Nombre <correo>"   --sales-email CORREO
#   --source DIR                código de la landing (apps/landing); por defecto, el de este script
#   --repo URL --branch RAMA    o clona el repositorio (https://github.com/hcdestroyer/horus-flow,
#                               claude/horus-landing) en DIR_INSTALACION/src
#   --token-file F              token de GitHub de solo lectura si el repositorio es privado
#   --image IMAGEN              usa una imagen ya construida en lugar de construirla
#   --install-dir DIR           (/opt/horus-landing)
#   --backup-cron               copia diaria de la base de datos a DIR/backups (se guardan 14)
#   --skip-docker-install       no instala Docker (debe estar ya)
#   --yes                       no pregunta nada (usa los valores por defecto y las opciones)
#
# Todo queda en DIR_INSTALACION: compose.yaml, .env (sin secretos), secrets/ (solo legibles por el
# usuario del contenedor, 65532) y backups/. La base de datos vive en el volumen
# horus-landing_landing-data. GUARDA secrets/data.key FUERA DEL SERVIDOR: sin ella no se pueden leer
# las credenciales de PayPal ni los TOTP de los administradores.

set -euo pipefail
umask 027

# --- Utilidades -----------------------------------------------------------------------------------
if [ -t 1 ]; then c_ok=$'\e[32m' c_warn=$'\e[33m' c_err=$'\e[31m' c_b=$'\e[1m' c_off=$'\e[0m'; else c_ok='' c_warn='' c_err='' c_b='' c_off=''; fi
say() { printf '%s==>%s %s\n' "$c_b" "$c_off" "$*"; }
ok() { printf '  %sOK%s   %s\n' "$c_ok" "$c_off" "$*"; }
warn() { printf '  %sAVISO%s %s\n' "$c_warn" "$c_off" "$*" >&2; }
die() { printf '%sinstall-landing: %s%s\n' "$c_err" "$*" "$c_off" >&2; exit 1; }

self="$(readlink -f "$0")"
script_dir="$(dirname "$self")"

# --- Argumentos -----------------------------------------------------------------------------------
cmd="install"
case "${1:-}" in
  install | update | status | logs | backup | restore | admin | uninstall | help | -h | --help) cmd="$1"; shift ;;
esac
[ "$cmd" = "-h" ] || [ "$cmd" = "--help" ] && cmd=help

install_dir="${HORUS_LANDING_DIR:-/opt/horus-landing}"
domain="" admin_email="" admin_pw_file="" mode="" proxy_network="" port="" trusted=""
smtp_host="" smtp_port="" smtp_user="" smtp_pass_file="" smtp_secure="" mail_from="" sales_email=""
source_dir="" repo="https://github.com/hcdestroyer/horus-flow" branch="claude/horus-landing" token_file=""
image="" backup_cron=0 skip_docker=0 assume_yes=0 purge=0 follow=0
rest=()

need() { [ "$#" -ge 2 ] || die "falta el valor de $1"; }
while [ "$#" -gt 0 ]; do
  case "$1" in
    --domain) need "$@"; domain="$2"; shift 2 ;;
    --admin-email) need "$@"; admin_email="$2"; shift 2 ;;
    --admin-password-file) need "$@"; admin_pw_file="$2"; shift 2 ;;
    --mode) need "$@"; mode="$2"; shift 2 ;;
    --proxy-network) need "$@"; proxy_network="$2"; shift 2 ;;
    --port) need "$@"; port="$2"; shift 2 ;;
    --trusted-proxies) need "$@"; trusted="$2"; shift 2 ;;
    --smtp-host) need "$@"; smtp_host="$2"; shift 2 ;;
    --smtp-port) need "$@"; smtp_port="$2"; shift 2 ;;
    --smtp-user) need "$@"; smtp_user="$2"; shift 2 ;;
    --smtp-pass-file) need "$@"; smtp_pass_file="$2"; shift 2 ;;
    --smtp-secure) need "$@"; smtp_secure="$2"; shift 2 ;;
    --mail-from) need "$@"; mail_from="$2"; shift 2 ;;
    --sales-email) need "$@"; sales_email="$2"; shift 2 ;;
    --source) need "$@"; source_dir="$2"; shift 2 ;;
    --repo) need "$@"; repo="$2"; shift 2 ;;
    --branch) need "$@"; branch="$2"; shift 2 ;;
    --token-file) need "$@"; token_file="$2"; shift 2 ;;
    --image) need "$@"; image="$2"; shift 2 ;;
    --install-dir) need "$@"; install_dir="$2"; shift 2 ;;
    --backup-cron) backup_cron=1; shift ;;
    --skip-docker-install) skip_docker=1; shift ;;
    --yes | -y) assume_yes=1; shift ;;
    --purge) purge=1; shift ;;
    -f | --follow) follow=1; shift ;;
    -h | --help) cmd=help; shift ;;
    *) rest+=("$1"); shift ;;
  esac
done

usage() { sed -n '2,/^set -euo/p' "$self" | sed '$d' | sed 's/^# \{0,1\}//'; }
[ "$cmd" = help ] && { usage; exit 0; }
[ "$(id -u)" = 0 ] || die "ejecútalo como root (sudo bash $0 $cmd …)"

project="horus-landing"
container="horus-landing"
volume="${project}_landing-data"
compose_file="$install_dir/compose.yaml"
env_file="$install_dir/.env"
secrets_dir="$install_dir/secrets"
backup_dir="$install_dir/backups"
state_file="$install_dir/install.conf"
uid=65532

compose() { docker compose -p "$project" -f "$compose_file" --env-file "$env_file" "$@"; }

ask() { # ask VARIABLE "pregunta" "defecto"
  local __v="$1" q="$2" def="$3" a=""
  if [ "$assume_yes" = 1 ] || [ ! -t 0 ]; then printf -v "$__v" '%s' "$def"; return; fi
  read -r -p "  $q [$def]: " a || true
  printf -v "$__v" '%s' "${a:-$def}"
}

load_state() { # valores de una instalación anterior (no se piden otra vez)
  [ -f "$state_file" ] || return 0
  # shellcheck disable=SC1090
  . "$state_file"
}

save_state() {
  cat >"$state_file" <<EOF
# install-landing.sh — valores de esta instalación (sin secretos). No editar a mano.
st_domain='$domain'
st_admin_email='$admin_email'
st_mode='$mode'
st_proxy_network='$proxy_network'
st_port='$port'
st_trusted='$trusted'
st_source_dir='$source_dir'
st_repo='$repo'
st_branch='$branch'
st_image='$image'
EOF
  chmod 0600 "$state_file"
}

# --- Docker ----------------------------------------------------------------------------------------
install_docker() {
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    ok "Docker $(docker version --format '{{.Server.Version}}' 2>/dev/null || echo '?') y compose presentes"
    return
  fi
  [ "$skip_docker" = 1 ] && die "Docker o el plugin compose no están instalados (quita --skip-docker-install)"
  [ -r /etc/os-release ] || die "sin /etc/os-release: instala Docker a mano y usa --skip-docker-install"
  local id codename arch
  id="$(. /etc/os-release; echo "${ID:-}")"
  codename="$(. /etc/os-release; echo "${VERSION_CODENAME:-}")"
  case "$id" in debian | ubuntu) ;; *) die "sistema $id no soportado por este script: instala Docker a mano y usa --skip-docker-install" ;; esac
  arch="$(dpkg --print-architecture)"
  say "Instalando Docker desde el repositorio oficial ($id $codename, $arch)"
  export DEBIAN_FRONTEND=noninteractive
  apt-get -q update >/dev/null
  apt-get -q -y install ca-certificates curl gpg >/dev/null
  install -d -m 0755 /etc/apt/keyrings
  local tmp fpr="9DC858229FC7DD38854AE2D88D81803C0EBFCD88"
  tmp="$(mktemp)"
  curl -fsSL --retry 3 "https://download.docker.com/linux/$id/gpg" -o "$tmp" || die "no se pudo descargar la clave de Docker"
  gpg --show-keys --with-colons "$tmp" 2>/dev/null | awk -F: '$1=="fpr"{print $10}' | grep -qx "$fpr" \
    || { rm -f "$tmp"; die "la clave de Docker no tiene la huella esperada $fpr"; }
  install -m 0644 "$tmp" /etc/apt/keyrings/docker.asc
  rm -f "$tmp"
  cat >/etc/apt/sources.list.d/docker.sources <<EOF
Types: deb
URIs: https://download.docker.com/linux/$id
Suites: $codename
Components: stable
Architectures: $arch
Signed-By: /etc/apt/keyrings/docker.asc
EOF
  apt-get -q update >/dev/null
  apt-get -q -y install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin >/dev/null
  systemctl enable --now docker >/dev/null 2>&1 || true
  docker compose version >/dev/null 2>&1 || die "Docker instalado pero sin el plugin compose"
  ok "Docker $(docker version --format '{{.Server.Version}}') instalado"
}

# --- Código ----------------------------------------------------------------------------------------
git_auth() { # cabecera de autenticación solo para esta orden (el token no se guarda en .git/config)
  if [ -n "$token_file" ]; then
    [ -r "$token_file" ] || die "no puedo leer $token_file"
    local b64
    b64="$(printf 'x-access-token:%s' "$(tr -d '\r\n' <"$token_file")" | base64 -w0)"
    printf '%s\n' "-c" "http.extraHeader=Authorization: Basic $b64"
  fi
}

resolve_source() {
  if [ -z "$source_dir" ] && [ -f "$script_dir/../Dockerfile" ] && [ -f "$script_dir/../nuxt.config.ts" ]; then
    source_dir="$(readlink -f "$script_dir/..")"
  fi
  if [ -n "$source_dir" ]; then
    [ -f "$source_dir/Dockerfile" ] || die "$source_dir no parece apps/landing (falta Dockerfile)"
    source_dir="$(readlink -f "$source_dir")"
    ok "código de la landing: $source_dir"
    return
  fi
  command -v git >/dev/null 2>&1 || { apt-get -q update >/dev/null; apt-get -q -y install git >/dev/null; }
  local src="$install_dir/src" auth=()
  mapfile -t auth < <(git_auth)
  if [ -d "$src/.git" ]; then
    git "${auth[@]}" -C "$src" fetch -q --depth 1 origin "$branch" || die "no se pudo actualizar $repo ($branch)"
    git -C "$src" checkout -q -B "$branch" FETCH_HEAD
  else
    say "Descargando $repo ($branch)"
    git "${auth[@]}" clone -q --depth 1 --branch "$branch" --filter=blob:none --sparse "$repo" "$src" \
      || die "no se pudo clonar $repo (si es privado, usa --token-file con un token de solo lectura)"
    git -C "$src" sparse-checkout set apps/landing
  fi
  source_dir="$src/apps/landing"
  ok "código de la landing: $source_dir ($(git -C "$src" rev-parse --short HEAD))"
}

# --- Configuración ---------------------------------------------------------------------------------
detect_npm_network() {
  local c net
  c="$(docker ps --format '{{.Names}} {{.Image}}' | awk '/jc21\/nginx-proxy-manager|nginx-proxy-manager/{print $1; exit}')"
  [ -n "$c" ] || return 1
  net="$(docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' "$c" | awk '{print $1}')"
  [ -n "$net" ] && printf '%s' "$net"
}

network_subnets() {
  docker network inspect -f '{{range .IPAM.Config}}{{.Subnet}},{{end}}' "$1" 2>/dev/null | sed 's/,$//'
}

write_secret() { # write_secret ARCHIVO (contenido por stdin): solo lo lee el usuario del contenedor
  local f="$1" tmp
  tmp="$(mktemp "$secrets_dir/.tmp.XXXXXX")"
  cat >"$tmp"
  chown "$uid:$uid" "$tmp"
  chmod 0400 "$tmp"
  mv -f "$tmp" "$f"
}

prepare_secrets() {
  install -d -m 0750 "$secrets_dir"
  if [ -s "$secrets_dir/data.key" ]; then
    ok "clave de cifrado existente (secrets/data.key): no se toca"
  else
    openssl rand -base64 32 | write_secret "$secrets_dir/data.key"
    generated_key=1
    ok "clave de cifrado generada (secrets/data.key)"
  fi
  if [ -n "$admin_pw_file" ]; then
    [ -r "$admin_pw_file" ] || die "no puedo leer $admin_pw_file"
    local pw; pw="$(tr -d '\r\n' <"$admin_pw_file")"
    [ "${#pw}" -ge 12 ] || die "la contraseña del administrador debe tener al menos 12 caracteres"
    printf '%s' "$pw" | write_secret "$secrets_dir/admin_password"
  elif [ ! -s "$secrets_dir/admin_password" ]; then
    generated_pw="$(openssl rand -base64 24 | tr -d '/+=' | cut -c1-20)"
    printf '%s' "$generated_pw" | write_secret "$secrets_dir/admin_password"
  fi
  if [ -n "$smtp_pass_file" ]; then
    [ -r "$smtp_pass_file" ] || die "no puedo leer $smtp_pass_file"
    tr -d '\r\n' <"$smtp_pass_file" | write_secret "$secrets_dir/smtp_pass"
  elif [ ! -e "$secrets_dir/smtp_pass" ]; then
    : | write_secret "$secrets_dir/smtp_pass"
  fi
  ok "secretos en $secrets_dir (propietario $uid, modo 0400)"
}

write_env() {
  local old_smtp_host="" old_smtp_port="" old_smtp_user="" old_smtp_secure="" old_from="" old_sales=""
  if [ -f "$env_file" ]; then
    old_smtp_host="$(sed -n 's/^SMTP_HOST=//p' "$env_file")"; old_smtp_port="$(sed -n 's/^SMTP_PORT=//p' "$env_file")"
    old_smtp_user="$(sed -n 's/^SMTP_USER=//p' "$env_file")"; old_smtp_secure="$(sed -n 's/^SMTP_SECURE=//p' "$env_file")"
    old_from="$(sed -n 's/^MAIL_FROM=//p' "$env_file" | sed 's/^"//; s/"$//')"; old_sales="$(sed -n 's/^SALES_EMAIL=//p' "$env_file")"
  fi
  smtp_host="${smtp_host:-$old_smtp_host}"
  smtp_port="${smtp_port:-${old_smtp_port:-587}}"
  smtp_user="${smtp_user:-$old_smtp_user}"
  smtp_secure="${smtp_secure:-${old_smtp_secure:-$([ "$smtp_port" = 465 ] && echo true || echo false)}}"
  sales_email="${sales_email:-${old_sales:-info@kns.gt}}"
  mail_from="${mail_from:-${old_from:-Horus Flow <$sales_email>}}"
  cat >"$env_file" <<EOF
# Landing de Horus Flow — generado por install-landing.sh. Sin secretos (están en secrets/).
# Variables: apps/landing/README.md › Variables. Tras editarlo: install-landing.sh update
NUXT_PUBLIC_SITE_URL=https://$domain
SALES_EMAIL=$sales_email
MAIL_FROM="$mail_from"
MAIL_CONFIRM_CUSTOMER=true
SMTP_HOST=$smtp_host
SMTP_PORT=$smtp_port
SMTP_SECURE=$smtp_secure
SMTP_USER=$smtp_user
TRUSTED_PROXIES=$trusted
RATE_LIMIT_MAX=8
RATE_LIMIT_WINDOW_SECONDS=600
FORM_MIN_FILL_SECONDS=3
ADMIN_EMAIL=$admin_email
EOF
  chmod 0640 "$env_file"
  ok ".env escrito (dominio https://$domain, SMTP ${smtp_host:-sin configurar})"
}

write_compose() {
  local build_block net_block ports_block="" networks_line
  if [ -n "$image" ]; then
    build_block="    image: $image"
  else
    build_block="    build:
      context: $source_dir
    image: horus-landing:latest"
  fi
  if [ "$mode" = npm ]; then
    networks_line="    networks: [proxy]"
    net_block="networks:
  proxy:
    name: $proxy_network
    external: true"
  else
    networks_line="    networks: [default]"
    ports_block="    ports:
      - \"$port:3000\""
    net_block=""
  fi
  cat >"$compose_file" <<EOF
# Landing de Horus Flow — generado por install-landing.sh (modo $mode). No editar a mano: se
# regenera en cada install/update.
name: $project

services:
  landing:
$build_block
    container_name: $container
    restart: unless-stopped
    env_file: .env
    environment:
      NODE_ENV: production
      DATA_DIR: /data
      DATA_KEY_FILE: /run/secrets/data_key
      ADMIN_PASSWORD_FILE: /run/secrets/admin_password
      SMTP_PASS_FILE: /run/secrets/smtp_pass
    secrets: [data_key, admin_password, smtp_pass]
    volumes:
      - landing-data:/data
    read_only: true
    cap_drop: [ALL]
    security_opt: ['no-new-privileges:true']
    mem_limit: 384m
$networks_line
$ports_block
    healthcheck:
      test:
        - CMD
        - /nodejs/bin/node
        - -e
        - "fetch('http://127.0.0.1:3000/robots.txt').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 20s

secrets:
  data_key:
    file: $secrets_dir/data.key
  admin_password:
    file: $secrets_dir/admin_password
  smtp_pass:
    file: $secrets_dir/smtp_pass

volumes:
  landing-data:

$net_block
EOF
  ok "compose.yaml escrito"
}

wait_healthy() {
  local i st=""
  for i in $(seq 1 60); do
    st="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$container" 2>/dev/null || echo missing)"
    [ "$st" = healthy ] && { ok "contenedor sano"; return 0; }
    [ "$st" = exited ] || [ "$st" = dead ] && break
    sleep 3
  done
  docker logs --tail 40 "$container" >&2 || true
  die "el contenedor no quedó sano (estado: $st). Revisa los registros de arriba: install-landing.sh logs"
}

quiet() { # ejecuta sin ruido; si falla, muestra lo que dijo
  local out
  if ! out="$("$@" 2>&1)"; then printf '%s\n' "$out" >&2; return 1; fi
}

cli() { docker exec -i "$container" /nodejs/bin/node .output/server/index.mjs "$@"; }

ensure_admin() {
  local list
  list="$(cli admin list 2>/dev/null || true)"
  if printf '%s' "$list" | grep -qi -- "$admin_email"; then
    ok "administrador $admin_email presente"
  else
    cli admin create --email "$admin_email" --name "C&S Company" <"$secrets_dir/admin_password" >/dev/null \
      || die "no se pudo crear el administrador $admin_email"
    ok "administrador $admin_email creado"
  fi
}

do_backup() {
  install -d -m 0750 "$backup_dir"
  local name tmp="/data/.backup-$$.sqlite"
  name="horus-landing-$(date +%Y%m%d-%H%M%S).sqlite"
  [ "$(docker inspect -f '{{.State.Running}}' "$container" 2>/dev/null)" = true ] \
    || die "el contenedor $container no está en marcha (install-landing.sh status)"
  # Copia consistente en caliente con la propia base de datos (VACUUM INTO), sin imágenes extra.
  docker exec "$container" /nodejs/bin/node --no-warnings -e "
    const { DatabaseSync } = require('node:sqlite');
    const db = new DatabaseSync('/data/horus-landing.sqlite');
    db.exec(\"VACUUM INTO '$tmp'\"); db.close();" 2>/dev/null || die "no se pudo copiar la base de datos"
  docker cp "$container:$tmp" "$backup_dir/$name" >/dev/null
  docker exec "$container" /nodejs/bin/node -e "require('fs').rmSync('$tmp',{force:true})" || true
  chmod 0600 "$backup_dir/$name"
  ls -1t "$backup_dir"/horus-landing-*.sqlite 2>/dev/null | tail -n +15 | xargs -r rm -f
  ok "copia: $backup_dir/$name (se guardan las 14 últimas; la clave secrets/data.key va aparte)"
}

do_restore() {
  local file="${rest[0]:-}" img
  [ -n "$file" ] && [ -r "$file" ] || die "uso: install-landing.sh restore ARCHIVO.sqlite"
  file="$(readlink -f "$file")"
  img="$(docker inspect -f '{{.Config.Image}}' "$container" 2>/dev/null)" || die "el contenedor $container no existe"
  if [ "$assume_yes" != 1 ]; then
    local a=""; read -r -p "  Esto sustituye la base de datos actual por $file. ¿Seguir? [s/N] " a || true
    [[ "$a" =~ ^[sSyY] ]] || die "cancelado"
  fi
  do_backup
  compose stop landing >/dev/null 2>&1
  # Pase lo que pase, la landing vuelve a arrancar (con la base restaurada o con la anterior).
  trap 'compose start landing >/dev/null 2>&1 || true' EXIT
  # Como root dentro del contenedor auxiliar para leer la copia (0600, de root) y dejar el
  # archivo restaurado a nombre del usuario de la landing (65532).
  docker run --rm --user 0:0 --entrypoint /nodejs/bin/node -v "$volume:/data" -v "$file:/restore.sqlite:ro" "$img" --no-warnings -e "
    const fs = require('fs');
    const { DatabaseSync } = require('node:sqlite');
    const probe = new DatabaseSync('/restore.sqlite', { readOnly: true });
    if (probe.prepare('PRAGMA integrity_check').get().integrity_check !== 'ok') throw new Error('copia dañada');
    probe.close();
    for (const f of ['-wal', '-shm']) fs.rmSync('/data/horus-landing.sqlite' + f, { force: true });
    fs.copyFileSync('/restore.sqlite', '/data/horus-landing.sqlite');
    fs.chownSync('/data/horus-landing.sqlite', $uid, $uid);" || die "no se pudo restaurar (la landing sigue con la base anterior)"
  trap - EXIT
  compose start landing >/dev/null 2>&1
  wait_healthy
  ok "base de datos restaurada desde $file"
}

install_cron() {
  cat >/etc/cron.d/horus-landing-backup <<EOF
# Copia diaria de la base de datos de la landing de Horus Flow (install-landing.sh --backup-cron).
SHELL=/bin/bash
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
17 3 * * * root bash $install_dir/install-landing.sh backup --install-dir $install_dir >>/var/log/horus-landing-backup.log 2>&1
EOF
  chmod 0644 /etc/cron.d/horus-landing-backup
  ok "copia diaria programada (03:17, /etc/cron.d/horus-landing-backup)"
}

print_next_steps() {
  echo
  say "${c_ok}Landing instalada${c_off}"
  echo "  Web:    https://$domain"
  echo "  Panel:  https://$domain/admin   (usuario: $admin_email)"
  if [ -n "${generated_pw:-}" ]; then
    echo
    echo "  ${c_b}Contraseña inicial del panel (se muestra solo ahora):${c_off} $generated_pw"
    echo "  Al entrar te pedirá registrar el código de verificación (TOTP) con tu app de autenticación."
  fi
  if [ "${generated_key:-0}" = 1 ]; then
    echo
    echo "  ${c_warn}IMPORTANTE:${c_off} copia $secrets_dir/data.key a un lugar seguro FUERA de este servidor."
    echo "  Sin ella no se pueden leer las credenciales de PayPal ni los TOTP guardados."
  fi
  echo
  if [ "$mode" = npm ]; then
    cat <<EOF
  En Nginx Proxy Manager → Proxy Hosts → Add Proxy Host:
    Domain Names:     $domain
    Scheme:           http
    Forward Hostname: $container        Forward Port: 3000
    Block Common Exploits: activado
    SSL: Request a new SSL Certificate (Let's Encrypt), Force SSL y HTTP/2
    Advanced (para que los vídeos se reproduzcan en Safari):
      proxy_force_ranges on;
EOF
  else
    cat <<EOF
  La landing escucha en http://$port. Configura tu proxy para que sirva https://$domain y
  reenvíe a esa dirección (y añade tu proxy a --trusted-proxies si no está en esta máquina).
EOF
  fi
  echo
  echo "  DNS: registro A de $domain → IP pública de este servidor."
  [ -z "$smtp_host" ] && echo "  Correo: sin SMTP las solicitudes se guardan en el panel pero no salen correos (install-landing.sh install --smtp-host …)."
  echo "  Mantenimiento: install-landing.sh status | logs | update | backup | restore | admin list"
}

# --- Órdenes ---------------------------------------------------------------------------------------
do_install() {
  say "Landing de Horus Flow — instalación en $install_dir"
  load_state
  install -d -m 0750 "$install_dir"
  install_docker
  command -v openssl >/dev/null 2>&1 || { apt-get -q update >/dev/null; apt-get -q -y install openssl >/dev/null; }

  domain="${domain:-${st_domain:-}}"; [ -n "$domain" ] || ask domain "Dominio público de la landing" "horusflow.kns.gt"
  admin_email="${admin_email:-${st_admin_email:-}}"; [ -n "$admin_email" ] || ask admin_email "Correo del primer administrador" "info@kns.gt"
  [[ "$admin_email" == *@*.* ]] || die "correo de administrador no válido: $admin_email"
  [[ "$domain" =~ ^[A-Za-z0-9.-]+\.[A-Za-z]{2,}$ ]] || die "dominio no válido: $domain"

  mode="${mode:-${st_mode:-}}"
  if [ -z "$mode" ]; then
    if detect_npm_network >/dev/null; then mode=npm; else ask mode "No veo Nginx Proxy Manager. Modo (npm|port)" "port"; fi
  fi
  case "$mode" in
    npm)
      proxy_network="${proxy_network:-${st_proxy_network:-$(detect_npm_network || echo npm_default)}}"
      docker network inspect "$proxy_network" >/dev/null 2>&1 \
        || die "no existe la red de Docker '$proxy_network' de Nginx Proxy Manager (docker network ls; usa --proxy-network o --mode port)"
      trusted="${trusted:-${st_trusted:-$(network_subnets "$proxy_network")}}"
      ok "modo npm: red $proxy_network, proxies de confianza ${trusted:-ninguno}"
      ;;
    port)
      port="${port:-${st_port:-127.0.0.1:3000}}"
      [[ "$port" == *:* ]] || port="127.0.0.1:$port"
      trusted="${trusted:-${st_trusted:-127.0.0.1/32,172.16.0.0/12}}"
      ok "modo port: $port, proxies de confianza $trusted"
      ;;
    *) die "--mode debe ser npm o port" ;;
  esac

  source_dir="${source_dir:-${st_source_dir:-}}"; repo="${st_repo:-$repo}"; branch="${st_branch:-$branch}"
  image="${image:-${st_image:-}}"
  [ -n "$image" ] || resolve_source

  prepare_secrets
  write_env
  write_compose
  cp -f "$self" "$install_dir/install-landing.sh" 2>/dev/null || true
  chmod 0750 "$install_dir/install-landing.sh"
  save_state

  if [ -n "$image" ]; then
    docker image inspect "$image" >/dev/null 2>&1 || docker pull -q "$image" >/dev/null || die "no existe la imagen $image"
    say "Arrancando con la imagen $image"
  else
    say "Construyendo la imagen (primera vez: unos minutos)"
  fi
  if [ -z "$image" ]; then quiet compose build --pull || die "falló la construcción de la imagen (install-landing.sh logs / docker compose build)"; fi
  quiet compose up -d || die "no se pudo arrancar el contenedor"
  wait_healthy
  ensure_admin
  [ "$backup_cron" = 1 ] && install_cron
  print_next_steps
}

do_update() {
  [ -f "$state_file" ] || die "no hay instalación en $install_dir (ejecuta install)"
  load_state
  domain="$st_domain" admin_email="$st_admin_email" mode="$st_mode" proxy_network="$st_proxy_network"
  port="$st_port" trusted="$st_trusted" source_dir="${source_dir:-$st_source_dir}" repo="$st_repo" branch="$st_branch"
  image="${image:-$st_image}"
  say "Actualizando la landing"
  if [ "$(docker inspect -f '{{.State.Running}}' "$container" 2>/dev/null)" = true ]; then
    do_backup
  else
    warn "la landing no está en marcha: se actualiza sin copia previa"
  fi
  if [ -z "$image" ]; then
    if [ -d "$install_dir/src/.git" ] && [ -z "$st_source_dir" ]; then source_dir=""; fi
    resolve_source
  fi
  write_env
  write_compose
  save_state
  if [ -z "$image" ]; then quiet compose build --pull || die "falló la construcción; la versión anterior sigue funcionando"; fi
  quiet compose up -d || die "no se pudo arrancar la nueva versión"
  wait_healthy
  ok "actualizada (las migraciones de la base de datos se aplican solas al arrancar)"
}

do_status() {
  [ -f "$compose_file" ] || die "no hay instalación en $install_dir"
  load_state
  echo "  Dominio: https://${st_domain:-?}   Modo: ${st_mode:-?}   Instalación: $install_dir"
  compose ps
  docker inspect -f '  Salud: {{if .State.Health}}{{.State.Health.Status}}{{else}}sin healthcheck{{end}} · arrancado {{.State.StartedAt}}' "$container" 2>/dev/null || true
  local last; last="$(ls -1t "$backup_dir"/horus-landing-*.sqlite 2>/dev/null | head -1)"
  echo "  Última copia: ${last:-ninguna}"
}

do_uninstall() {
  [ -f "$compose_file" ] || die "no hay instalación en $install_dir"
  if [ "$assume_yes" != 1 ]; then
    local a="" what="el contenedor (los datos y secretos se conservan)"
    [ "$purge" = 1 ] && what="el contenedor, la base de datos, los secretos y las copias"
    read -r -p "  Se eliminará $what. ¿Seguir? [s/N] " a || true
    [[ "$a" =~ ^[sSyY] ]] || die "cancelado"
  fi
  if [ "$purge" = 1 ]; then
    compose down -v --remove-orphans >/dev/null 2>&1 || true
    rm -f /etc/cron.d/horus-landing-backup
    rm -rf "$install_dir"
    ok "landing eliminada por completo"
  else
    compose down --remove-orphans >/dev/null 2>&1 || true
    ok "contenedor eliminado; datos en el volumen $volume y secretos en $secrets_dir (install para volver)"
  fi
}

case "$cmd" in
  install) do_install ;;
  update) do_update ;;
  status) do_status ;;
  logs) if [ "$follow" = 1 ]; then docker logs -f --tail 200 "$container"; else docker logs --tail 200 "$container"; fi ;;
  backup) do_backup ;;
  restore) do_restore ;;
  admin) [ "${#rest[@]}" -gt 0 ] || die "uso: install-landing.sh admin list | create --email c | reset-totp --email c | enable|disable --email c"
    a=("${rest[@]}")
    [ -n "$admin_email" ] && a+=(--email "$admin_email")
    cli admin "${a[@]}" ;;
  uninstall) do_uninstall ;;
esac
