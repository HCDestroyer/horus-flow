#!/usr/bin/env bash
# install.sh — instalador de Horus Flow en UN servidor Linux (historia I1-22).
#
# Debian 12/13 o Ubuntu 22.04/24.04 LTS con Docker Engine ≥ 24 y el plugin compose v2.
# Perfil mínimo de ADR-0025 (deployments/compose/compose.prod.yaml). Idempotente: una segunda
# ejecución reutiliza las respuestas guardadas y NUNCA regenera secretos.
#
#   sudo bash scripts/install.sh                         # interactivo: modo de acceso, rango
#                                                        # de túneles y superadmin
#   sudo bash scripts/install.sh --yes --mode ip --admin-email yo@isp.net \
#        --admin-password-file /root/pw                  # desatendido, solo IP
#   sudo bash scripts/install.sh --yes --mode domain --domain horus.isp.net --acme-email noc@isp.net ...
#   sudo bash scripts/install.sh --check                 # puertos, salud de cada rol, disco, TLS, backups
#   sudo bash scripts/install.sh --uninstall [--purge]   # quita contenedores, units y reglas;
#                                                        # --purge borra también datos y secretos
#
# Modos de acceso (D19, docs/po-decisions.md):
#   domain | subdomain   Traefik con TLS automático de Let's Encrypt (reto HTTP-01: el nombre debe
#                        resolver a este servidor y el puerto 80 ser accesible).
#   ip                   (ip_only) sin dominio: certificado autogenerado para la IP; se muestra su
#                        huella SHA-256 para verificarla en el navegador y RouterOS lo importa.
#   --tls external       (cuarto modo, detrás de un proxy inverso propio: Nginx Proxy Manager,
#   | --behind-proxy     nginx, Caddy, HAProxy…) el proxy termina TLS; Traefik sirve solo HTTP en
#                        --http-bind (127.0.0.1:8080), sin ACME, sin certificado y sin HSTS, y solo
#                        acepta X-Forwarded-* de --trusted-proxies (obligatoria), al que se limita
#                        además ese puerto en el cortafuegos. --public-url https://… es la URL que
#                        ve la gente. El UDP de WireGuard NO pasa por el proxy (--wg-endpoint).
#
# Qué hace: comprueba requisitos (CPU, RAM, disco, puertos, Docker, WireGuard), genera secretos
# en <etc>/secrets (0700) y el PAQUETE DE SECRETOS OFFLINE cifrado (ADR-0029,
# disaster-recovery.md §3.6) que la persona debe guardar fuera del servidor, crea la interfaz del
# hub WireGuard (wg0, kernel o wireguard-go), filtra el UDP de IPFIX/NetFlow para que solo entre
# por wg0, construye la imagen de PostgreSQL con pgBackRest, arranca el compose y espera a que
# todo esté healthy, crea el superadmin inicial (lo crea horus-app con su contraseña), inicializa
# pgBackRest, hace el primer backup y programa los timers de backup (I1-23).
#
# Opciones (también en --help):
#   --mode domain|subdomain|ip   --domain FQDN   --acme-email EMAIL   --acme-staging
#   --public-ip IP               IP pública (modo ip y endpoint WireGuard; por defecto la de la ruta por defecto)
#   --tls auto|external          auto: Let's Encrypt (domain/subdomain) o autogenerado (ip); external: proxy propio
#   --public-url URL             (external) URL pública https:// que sirve el proxy
#   --trusted-proxies CIDR[,…]   (external) IP o red del proxy inverso; obligatoria
#   --http-bind IP:PUERTO        (external) dónde escucha Traefik en HTTP (127.0.0.1:8080; la IP de la
#                                LAN si el proxy está en otra máquina)
#   --wg-endpoint HOST           nombre o IP a la que los routers envían el UDP de WireGuard
#   --tunnel-cidr CIDR           rango de IPs de túnel de los routers (10.255.0.0/16; nunca 100.64.0.0/10)
#   --admin-email EMAIL          --admin-password-file FICHERO (si no, se pregunta o se genera)
#   --image-source ghcr|bundle:RUTA|local   de dónde salen las imágenes (por defecto: bundle si
#                                este árbol trae images/, ghcr si trae images.lock, si no local).
#                                ghcr: descarga de GHCR fijada por digest y verifica la firma
#                                cosign; bundle: paquete offline horus-<versión>-linux-<arch>.tar.gz
#                                (o su directorio) verificado con SHA256SUMS; local: imágenes ya
#                                presentes o construidas aquí (repo con Dockerfile)
#   --registry PREFIJO (ghcr.io/hcdestroyer)  --registry-user USUARIO  --registry-token-file F
#                                token de lectura (read:packages) si los paquetes son privados;
#                                se guarda en <etc>/registry-token (0600)
#   --channel stable|beta        canal de actualizaciones (stable)
#   --auto-update on|off         parches automáticos (Z de X.Y.Z) en la ventana; off por defecto
#   --update-window CALENDARIO   ventana de mantenimiento (OnCalendar de systemd; "Sun *-*-* 03:30:00")
#   --update-source URL          fuente de versiones (API de releases de GitHub o latest.json)
#   --no-update-check            no consulta versiones nuevas (servidores sin salida a Internet)
#   --image IMAGEN               imagen horus en modo local (por defecto horus:local, construida con el Dockerfile del repo)
#   --web-image IMAGEN (horus-web:local)  --postgres-image IMAGEN   (modo local)
#   --store-dir DIR              almacén y backups (/var/lib/horus/store; mejor OTRO disco)
#   --data-dir DIR               datos de PostgreSQL/ClickHouse/NATS (/var/lib/horus)
#   --install-dir DIR (/opt/horus)   --etc-dir DIR (/etc/horus)
#   --root DIR                   prefijo para todas las rutas por defecto (pruebas; no activa systemd/cron)
#   --project NOMBRE (horus)     --http-port 80  --https-port 443  --wg-port 51820  --wg-interface wg0
#   --backup-metrics-port 9109 (127.0.0.1)   --docker-subnet CIDR (172.31.250.0/24)    --tlm-max-bytes BYTES (telemetría NATS; según disco)
#   --bundle-recipient CLAVE_AGE  cifra el paquete offline para esa clave pública age (si no: frase
#                                 aleatoria que se muestra UNA vez)
#   --bundle-out DIR             dónde dejar el paquete offline (/root)
#   --smtp-host H --smtp-port 587 --smtp-from EMAIL --smtp-user U --smtp-tls starttls|tls|none
#   --smtp-password-file F       canal mínimo de alertas por correo (D13); opcional
#   --confirm-bundle             marca el paquete offline como guardado fuera del servidor
#   --skip-firewall --skip-tunnel --skip-systemd --skip-backup --force (ignora requisitos no críticos)
#   --yes                        no pregunta (usa opciones, respuestas guardadas o valores por defecto)
#
# Códigos de salida: 0 bien, 1 fallo, 2 uso incorrecto.

# ok/warn/fail siempre devuelven 0, así que `cond && ok … || fail …` es intencionado; los .env y
# /etc/os-release se generan o existen solo en el servidor; $chain va literal a horus-tunnel.
# shellcheck disable=SC2015,SC1090,SC1091,SC2016
set -euo pipefail
umask 022

repo_root="$(cd "$(dirname "$(readlink -f "$0")")/.." && pwd)"

# --- Opciones ------------------------------------------------------------------------------------
action=install
opt_yes=0 opt_force=0 opt_purge=0 skip_fw=0 skip_tunnel=0 skip_systemd=0 skip_backup=0 confirm_bundle=0
declare -A opt=()
usage() { sed -n '2,67p' "$0" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }
need_arg() { [ "$#" -ge 2 ] && [ -n "$2" ] || { echo "install.sh: $1 necesita un valor" >&2; exit 2; }; }
while [ "$#" -gt 0 ]; do
  case "$1" in
    --mode | --domain | --acme-email | --public-ip | --tunnel-cidr | --admin-email | --admin-password-file | \
      --image | --store-dir | --data-dir | --install-dir | --etc-dir | --root | --project | --http-port | \
      --https-port | --wg-port | --wg-interface | --docker-subnet | --tlm-max-bytes | --bundle-recipient | --bundle-out | \
      --smtp-host | --smtp-port | --smtp-from | --smtp-user | --smtp-tls | --smtp-password-file | --backup-metrics-port | \
      --image-source | --registry | --registry-user | --registry-token-file | --channel | --auto-update | --update-window | \
      --update-source | --web-image | --postgres-image | --tls | --public-url | --trusted-proxies | --http-bind | --wg-endpoint)
      need_arg "$@"; opt[${1#--}]="$2"; shift 2 ;;
    --acme-staging) opt[acme-staging]=1; shift ;;
    --no-update-check) opt[update-check]=false; shift ;;
    --behind-proxy) opt[tls]=external; shift ;;
    --check) action=check; shift ;;
    --uninstall) action=uninstall; shift ;;
    --purge) opt_purge=1; shift ;;
    --yes | -y) opt_yes=1; shift ;;
    --force) opt_force=1; shift ;;
    --skip-firewall) skip_fw=1; shift ;;
    --skip-tunnel) skip_tunnel=1; shift ;;
    --skip-systemd) skip_systemd=1; shift ;;
    --skip-backup) skip_backup=1; shift ;;
    --confirm-bundle) confirm_bundle=1; shift ;;
    -h | --help) usage 0 ;;
    *) echo "install.sh: opción desconocida: $1 (--help)" >&2; exit 2 ;;
  esac
done

root="${opt[root]:-}"
root="${root%/}"
[ -z "$root" ] || { mkdir -p "$root"; root="$(readlink -f "$root")"; skip_systemd=1; }
etc_dir="${opt[etc-dir]:-$root/etc/horus}"
conf_file="$etc_dir/install.conf"

# Respuestas guardadas de una instalación anterior (sin secretos).
declare -A saved=()
if [ -f "$conf_file" ]; then
  while IFS='=' read -r k v; do
    [[ "$k" =~ ^[a-z0-9-]+$ ]] && saved[$k]="$v"
  done <"$conf_file"
fi
# get <clave> <defecto>: opción > respuesta guardada > defecto.
get() { if [ -n "${opt[$1]:-}" ]; then printf '%s' "${opt[$1]}"; elif [ -n "${saved[$1]:-}" ]; then printf '%s' "${saved[$1]}"; else printf '%s' "$2"; fi; }

install_dir="$(get install-dir "$root/opt/horus")"
data_dir="$(get data-dir "$root/var/lib/horus")"
store_dir="$(get store-dir "$data_dir/store")"
project="$(get project horus)"
secrets_dir="$etc_dir/secrets"
tls_dir="$etc_dir/tls"
config_dir="$install_dir/config"
env_file="$install_dir/.env"
compose=(docker compose --project-directory "$install_dir" -f "$install_dir/compose.yaml" --env-file "$env_file")

c_ok=$'\033[32m' c_warn=$'\033[33m' c_err=$'\033[31m' c_off=$'\033[0m'
[ -t 1 ] || { c_ok=""; c_warn=""; c_err=""; c_off=""; }
say() { printf '==> %s\n' "$*"; }
ok() { printf '  %sOK%s    %s\n' "$c_ok" "$c_off" "$*"; }
warn() { printf '  %sAVISO%s %s\n' "$c_warn" "$c_off" "$*"; warnings=$((warnings + 1)); }
fail() { printf '  %sFALLO%s %s\n' "$c_err" "$c_off" "$*"; failures=$((failures + 1)); }
die() { printf '%sinstall.sh: %s%s\n' "$c_err" "$*" "$c_off" >&2; exit 1; }
warnings=0 failures=0

[ "$(id -u)" = 0 ] || die "ejecútalo como root (sudo bash scripts/install.sh …)"

interactive=0
[ "$opt_yes" = 0 ] && [ -t 0 ] && interactive=1
ask() { # ask <variable> <pregunta> <defecto>
  local __v="$1" q="$2" d="$3" a=""
  if [ "$interactive" = 1 ]; then
    read -r -p "  $q [${d}]: " a || true
  fi
  printf -v "$__v" '%s' "${a:-$d}"
}

random_alnum() { LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c "${1:-32}" || true; }
sha256_file() { sha256sum "$1" | cut -d' ' -f1; }

# --- Red: CIDR ----------------------------------------------------------------------------------
ip2int() { local a b c d; IFS=. read -r a b c d <<<"$1"; echo $(((a << 24) | (b << 16) | (c << 8) | d)); }
int2ip() { echo "$((($1 >> 24) & 255)).$((($1 >> 16) & 255)).$((($1 >> 8) & 255)).$(($1 & 255))"; }
valid_cidr() { [[ "$1" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}/([0-9]|[12][0-9]|3[0-2])$ ]]; }
cidr_network() { local ip="${1%/*}" len="${1#*/}" n; n="$(ip2int "$ip")"; echo "$(int2ip $((n & (0xFFFFFFFF << (32 - len)) & 0xFFFFFFFF)))/$len"; }
cidr_overlaps_cgnat() { # 100.64.0.0/10
  local net len n lo hi; net="$(cidr_network "$1")"; len="${net#*/}"; n="$(ip2int "${net%/*}")"
  lo="$(ip2int 100.64.0.0)"; hi=$((lo + (1 << 22) - 1))
  local end=$((n + (1 << (32 - len)) - 1))
  [ "$n" -le "$hi" ] && [ "$end" -ge "$lo" ]
}

# --- Requisitos ----------------------------------------------------------------------------------
port_busy() { # port_busy tcp|udp <puerto>
  local flag=-ltnH; [ "$1" = udp ] && flag=-lunH
  command -v ss >/dev/null 2>&1 || return 1
  ss "$flag" "sport = :$2" 2>/dev/null | grep -q .
}

project_running() { docker compose -p "$project" ps -q 2>/dev/null | grep -q .; }

check_requirements() {
  say "Requisitos del servidor"
  local id ver
  if [ -r /etc/os-release ]; then
    id="$(. /etc/os-release; echo "${ID:-}")"; ver="$(. /etc/os-release; echo "${VERSION_ID:-}")"
    case "$id:$ver" in
      debian:12* | debian:13* | ubuntu:22.04 | ubuntu:24.04 | ubuntu:26.04) ok "sistema $id $ver" ;;
      *) warn "sistema $id $ver no probado (soportados: Debian 12/13, Ubuntu 22.04/24.04 LTS)" ;;
    esac
  else
    warn "sin /etc/os-release"
  fi
  local cpus mem_kb mem_gb
  cpus="$(nproc)"
  if [ "$cpus" -ge 4 ]; then ok "CPU: $cpus núcleos"; elif [ "$cpus" -ge 2 ]; then warn "CPU: $cpus núcleos (recomendado ≥ 4)"; else fail "CPU: $cpus núcleo (mínimo 2)"; fi
  mem_kb="$(awk '/MemTotal/ { print $2 }' /proc/meminfo)"; mem_gb=$((mem_kb / 1024 / 1024))
  if [ "$mem_kb" -ge $((7 * 1024 * 1024)) ]; then ok "RAM: ${mem_gb} GiB"
  elif [ "$mem_kb" -ge $((3800 * 1024)) ]; then warn "RAM: ${mem_gb} GiB (recomendado ≥ 8 GiB)"
  else fail "RAM: ${mem_gb} GiB (mínimo 4 GiB)"; fi
  local d free_gb
  for d in "$data_dir" "$store_dir"; do
    local probe="$d"; while [ ! -e "$probe" ]; do probe="$(dirname "$probe")"; done
    free_gb=$(($(df -P -k "$probe" | awk 'NR == 2 { print $4 }') / 1024 / 1024))
    if [ "$free_gb" -ge 100 ]; then ok "disco libre en $d: ${free_gb} GiB"
    elif [ "$free_gb" -ge 20 ]; then warn "disco libre en $d: ${free_gb} GiB (recomendado ≥ 100 GiB; ajusta --tlm-max-bytes)"
    elif [ "$free_gb" -ge 5 ]; then warn "disco libre en $d: ${free_gb} GiB: solo para pruebas"
    else fail "disco libre en $d: ${free_gb} GiB (mínimo 5 GiB)"; fi
  done
  local pd ps
  pd="$(df -P "$(existing_parent "$data_dir")" | awk 'NR == 2 { print $1 }')"
  ps="$(df -P "$(existing_parent "$store_dir")" | awk 'NR == 2 { print $1 }')"
  if [ "$pd" = "$ps" ]; then
    warn "el almacén de backups ($store_dir) está en el mismo disco que las bases: protege de errores lógicos, NO de perder el disco (usa --store-dir en otro disco)"
  else
    ok "backups en otro disco ($ps)"
  fi
  if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    local dv; dv="$(docker version --format '{{.Server.Version}}' 2>/dev/null || echo 0)"
    if [ "${dv%%.*}" -ge 24 ] 2>/dev/null; then ok "Docker Engine $dv"; else fail "Docker Engine $dv (mínimo 24)"; fi
    if docker compose version >/dev/null 2>&1; then ok "$(docker compose version | head -1)"; else fail "falta el plugin docker compose v2"; fi
  else
    fail "Docker no está instalado o el daemon no responde (https://docs.docker.com/engine/install/)"
  fi
  local t
  for t in openssl curl flock ip; do
    command -v "$t" >/dev/null 2>&1 && ok "herramienta $t" || fail "falta la herramienta $t"
  done
  if [ "$skip_tunnel" = 0 ]; then
    if modprobe wireguard 2>/dev/null || [ -d /sys/module/wireguard ]; then ok "WireGuard en el kernel"
    elif command -v wireguard-go >/dev/null 2>&1 && [ -c /dev/net/tun ]; then warn "sin módulo WireGuard del kernel: se usará wireguard-go (más CPU)"
    else fail "sin WireGuard: instala el kernel con el módulo o wireguard-go (apt install wireguard-tools wireguard-go)"; fi
  fi
  if project_running; then
    ok "Horus ya está en marcha (proyecto $project): no se comprueban sus puertos"
  else
    local p
    # 18081/tcp: admin de horus-wg-agent (red del host, solo 127.0.0.1).
    for p in "tcp:$http_port" "tcp:$https_port" "udp:$(get wg-port 51820)" "tcp:18081"; do
      if port_busy "${p%%:*}" "${p#*:}"; then fail "puerto ${p#*:}/${p%%:*} ocupado"; else ok "puerto ${p#*:}/${p%%:*} libre"; fi
    done
  fi
  if [ "$failures" -gt 0 ]; then
    [ "$opt_force" = 1 ] || die "$failures requisito(s) no se cumplen (--force para continuar bajo tu responsabilidad)"
    warn "se continúa con --force pese a $failures requisito(s)"
    failures=0
  fi
}

existing_parent() { local p="$1"; while [ ! -e "$p" ]; do p="$(dirname "$p")"; done; printf '%s' "$p"; }

# --- Configuración (preguntas) --------------------------------------------------------------------
configure() {
  say "Configuración"
  tls_choice="$(get tls auto)"
  case "$tls_choice" in auto | external) ;; *) die "--tls inválido: $tls_choice (auto | external)" ;; esac
  if [ "$tls_choice" = external ]; then configure_external; else configure_traefik_tls; fi
  tunnel_cidr="$(get tunnel-cidr "")"
  [ -n "$tunnel_cidr" ] || ask tunnel_cidr "rango de IPs de túnel de los routers" "10.255.0.0/16"
  valid_cidr "$tunnel_cidr" || die "CIDR inválido: $tunnel_cidr"
  tunnel_cidr="$(cidr_network "$tunnel_cidr")"
  cidr_overlaps_cgnat "$tunnel_cidr" && die "el rango de túneles no puede solapar 100.64.0.0/10 (CGNAT de los ISP)"
  [ "${tunnel_cidr#*/}" -le 24 ] || die "el rango de túneles debe ser /24 o mayor"
  # Red de servicios del hub: la primera /24 del rango; el hub/colector usa su primera IP.
  local base; base="$(ip2int "${tunnel_cidr%/*}")"
  services_cidr="$(int2ip "$base")/24"
  collector_ip="$(int2ip $((base + 1)))"
  admin_email="$(get admin-email "")"
  [ -n "$admin_email" ] || ask admin_email "email del superadministrador inicial" "admin@${domain:-horus.local}"
  [[ "$admin_email" =~ ^[^@[:space:]]+@[^@[:space:]]+$ ]] || die "email inválido: $admin_email"
  http_port="$(get http-port 80)"; https_port="$(get https-port 443)"
  wg_port="$(get wg-port 51820)"; wg_if="$(get wg-interface wg0)"
  docker_subnet="$(get docker-subnet 172.31.250.0/24)"
  valid_cidr "$docker_subnet" || die "CIDR inválido: $docker_subnet"
  grpc_bind="$(int2ip $(($(ip2int "${docker_subnet%/*}") + 1)))"
  configure_images
  configure_updates
  public_base_url="https://$public_host"
  [ "$https_port" = 443 ] || public_base_url="$public_base_url:$https_port"
  access_mode="$mode"; [ "$mode" = ip ] && access_mode=ip_only
  tls_mode=acme; [ "$mode" = ip ] && tls_mode=self_signed
  if [ "$tls_choice" = external ]; then
    tls_mode=external; https_port="$(get https-port 8443)"
    public_base_url="https://$public_host"; [ "$public_url_port" = 443 ] || public_base_url="$public_base_url:$public_url_port"
    ok "detrás de proxy inverso: Traefik en http://$http_bind_addr:$http_port, X-Forwarded-* solo de $trusted_proxies"
  fi
  ok "acceso $access_mode → $public_base_url (TLS $tls_mode)"
  ok "túneles $tunnel_cidr, hub/colector $collector_ip en $wg_if, WireGuard UDP $wg_port en $wg_endpoint"
  ok "superadmin $admin_email; datos en $data_dir, backups en $store_dir"
  ok "versión $horus_version; imágenes: $image_source_desc; canal $channel, actualización automática $auto_update"
}

# Modos de Traefik con TLS propio (D19): domain/subdomain (Let's Encrypt) o ip (autogenerado).
configure_traefik_tls() {
  local default_ip
  default_ip="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{ for (i = 1; i < NF; i++) if ($i == "src") print $(i + 1) }' | head -1)"
  mode="$(get mode "")"
  if [ -z "$mode" ]; then
    echo "  Modo de acceso (D19): domain = dominio propio, subdomain = subdominio, ip = solo la IP del servidor"
    ask mode "modo de acceso (domain|subdomain|ip)" ip
  fi
  case "$mode" in ip | ip_only) mode=ip ;; domain | subdomain) ;; *) die "modo de acceso inválido: $mode" ;; esac
  public_ip="$(get public-ip "$default_ip")"
  domain="" acme_email=""
  if [ "$mode" = ip ]; then
    [ -n "$(get public-ip "")" ] || ask public_ip "IP pública del servidor" "$public_ip"
    [[ "$public_ip" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || die "IP inválida: $public_ip"
    public_host="$public_ip"
  else
    domain="$(get domain "")"
    [ -n "$domain" ] || ask domain "nombre (FQDN) que apunta a este servidor" "horus.example.net"
    [[ "$domain" =~ ^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$ ]] || die "nombre inválido: $domain"
    acme_email="$(get acme-email "")"
    [ -n "$acme_email" ] || ask acme_email "email para Let's Encrypt (avisos de caducidad)" "admin@$domain"
    public_host="$domain"
  fi
  wg_endpoint="$(get wg-endpoint "$public_host")"
  http_bind_addr="${HORUS_PUBLIC_BIND:-0.0.0.0}"; https_bind_addr="$http_bind_addr"
  trusted_proxies=""
}

# Cuarto modo (D19, nota de interpretación): detrás de un proxy inverso que termina TLS.
configure_external() {
  local url hostport
  url="$(get public-url "")"
  [ -n "$url" ] || ask url "URL pública que sirve tu proxy inverso (https://…)" "https://horus.example.net"
  [[ "$url" =~ ^https://([A-Za-z0-9.-]+)(:([0-9]{1,5}))?/?$ ]] || die "--public-url debe ser https://NOMBRE[:PUERTO] (el proxy termina TLS): $url"
  public_host="${BASH_REMATCH[1]}"; public_url_port="${BASH_REMATCH[3]:-443}"
  public_ip="$(get public-ip "")"
  domain="" acme_email=""
  if [[ "$public_host" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; then mode=ip; [ -n "$public_ip" ] || public_ip="$public_host"
  else
    mode="$(get mode domain)"; case "$mode" in domain | subdomain) ;; *) mode=domain ;; esac
    domain="$public_host"
  fi
  trusted_proxies="$(get trusted-proxies "")"
  [ -n "$trusted_proxies" ] || ask trusted_proxies "IP o red (CIDR) de tu proxy inverso" "127.0.0.1/32"
  [ -n "$trusted_proxies" ] || die "--trusted-proxies es obligatoria con --tls external"
  local c norm=()
  IFS=, read -r -a _tp <<<"$trusted_proxies"
  for c in "${_tp[@]}"; do
    c="${c// /}"; [ -n "$c" ] || continue
    [[ "$c" == */* ]] || c="$c/32"
    valid_cidr "$c" || die "CIDR inválido en --trusted-proxies: $c"
    [ "$c" != 0.0.0.0/0 ] || die "--trusted-proxies 0.0.0.0/0 aceptaría cabeceras falsificadas de cualquiera"
    norm+=("$(cidr_network "$c")")
  done
  trusted_proxies="$(IFS=,; echo "${norm[*]}")"
  hostport="$(get http-bind 127.0.0.1:8080)"
  [[ "$hostport" =~ ^([0-9]{1,3}(\.[0-9]{1,3}){3}):([0-9]{1,5})$ ]] || die "--http-bind debe ser IP:PUERTO: $hostport"
  http_bind_addr="${BASH_REMATCH[1]}"; opt[http-port]="${BASH_REMATCH[3]}"
  https_bind_addr=127.0.0.1
  wg_endpoint="$(get wg-endpoint "${public_ip:-$public_host}")"
}

# --- Imágenes: origen (ghcr | bundle:RUTA | local) ------------------------------------------------
# Árbol de esta versión: el repositorio clonado o un paquete de release (VERSION + images.lock;
# el offline trae además images/horus-images.tar). images.lock: VARIABLE ETIQUETA DIGEST por
# línea (HORUS_IMAGE ghcr.io/…/horus:1.2.3 sha256:…).
img_vars=(HORUS_IMAGE HORUS_WEB_IMAGE HORUS_POSTGRES_IMAGE HORUS_CLICKHOUSE_IMAGE HORUS_NATS_IMAGE
  HORUS_VALKEY_IMAGE HORUS_TRAEFIK_IMAGE HORUS_BUSYBOX_IMAGE)
declare -A img=()
tree_version() { if [ -s "$1/VERSION" ]; then tr -d ' \r\n' <"$1/VERSION"; else echo 0.0.0-dev; fi; }

# bundle_tree <ruta>: directorio del paquete offline (extrae el .tar.gz si hace falta y verifica
# SHA256SUMS del archivo y de su contenido).
bundle_tree() {
  local src="$1" dir sums top
  bundle_archive="" bundle_sums_ok=0
  if [ -d "$src" ]; then
    dir="$(readlink -f "$src")"
    [ -f "$dir/SHA256SUMS" ] || die "$dir no es un paquete de Horus (falta SHA256SUMS)"
    if [ ! -f "$dir/.verified" ] || [ "$dir/SHA256SUMS" -nt "$dir/.verified" ]; then
      if [ -f "$dir/images/horus-images.tar" ]; then
        (cd "$dir" && sha256sum -c --quiet SHA256SUMS) || die "el contenido de $dir no coincide con su SHA256SUMS"
      else
        (cd "$dir" && grep -v ' [*]\{0,1\}images/horus-images.tar$' SHA256SUMS | sha256sum -c --quiet -) || die "el contenido de $dir no coincide con su SHA256SUMS"
      fi
      : >"$dir/.verified"
    fi
    bundle_dir="$dir"; return 0
  fi
  [ -f "$src" ] || die "no existe el paquete offline $src"
  src="$(readlink -f "$src")"
  bundle_archive="$src"
  sums="$(dirname "$src")/SHA256SUMS"
  if [ -f "$sums" ] && grep -q " [*]\{0,1\}$(basename "$src")\$" "$sums"; then
    if [ ! -f "$src.verified" ] || [ "$src" -nt "$src.verified" ]; then
      (cd "$(dirname "$src")" && grep " [*]\{0,1\}$(basename "$src")\$" SHA256SUMS | sha256sum -c --quiet -) \
        || die "el SHA256 de $(basename "$src") no coincide con SHA256SUMS: paquete dañado o manipulado"
      : >"$src.verified" 2>/dev/null || true
    fi
    bundle_sums_ok=1
    ok "SHA256 de $(basename "$src") verificado (SHA256SUMS)" >&2
  else
    warn "sin SHA256SUMS junto a $(basename "$src"): se verifica su contenido" >&2
  fi
  # Se extrae todo MENOS images/horus-images.tar (se carga en streaming desde el .tar.gz).
  dir="$(get_bundle_cache)/$(basename "$src" .tar.gz)"
  if [ ! -f "$dir/.extracted" ] || [ "$src" -nt "$dir/.extracted" ]; then
    top="$({ tar -tzf "$src" 2>/dev/null || true; } | head -1)"; top="${top%%/*}"
    rm -rf "$dir"; install -d -m 0755 "$dir"
    tar -xzf "$src" -C "$dir" --strip-components=1 --exclude "$top/images/horus-images.tar" || die "no se pudo extraer $src"
    (cd "$dir" && grep -v ' [*]\{0,1\}images/horus-images.tar$' SHA256SUMS | sha256sum -c --quiet -) \
      || die "el contenido de $src no coincide con su SHA256SUMS"
    printf '%s\n' "$top" >"$dir/.extracted"
  fi
  bundle_dir="$dir"
}

# load_bundle_images: docker load del paquete offline (directorio o streaming desde el .tar.gz,
# comprobando el SHA256 de images/horus-images.tar contra el SHA256SUMS interno).
load_bundle_images() {
  local want got top tmp
  want="$(awk '$2 ~ /^[*]?images\/horus-images.tar$/ { print $1 }' "$bundle_dir/SHA256SUMS")"
  [ -n "$want" ] || die "el SHA256SUMS del paquete no lista images/horus-images.tar"
  if [ -f "$bundle_dir/images/horus-images.tar" ]; then
    docker load -q -i "$bundle_dir/images/horus-images.tar" >/dev/null || die "docker load falló"
    return 0
  fi
  [ -n "$bundle_archive" ] && [ -f "$bundle_archive" ] || die "falta el paquete offline .tar.gz para cargar las imágenes"
  top="$(head -1 "$bundle_dir/.extracted")"
  if [ "$bundle_sums_ok" = 0 ]; then
    got="$(tar -xzOf "$bundle_archive" "$top/images/horus-images.tar" | sha256sum | cut -d' ' -f1)"
    [ "$got" = "$want" ] || die "SHA256 de images/horus-images.tar no coincide: paquete dañado"
  fi
  tmp="$(mktemp)"
  tar -xzOf "$bundle_archive" "$top/images/horus-images.tar" | tee >(sha256sum | cut -d' ' -f1 >"$tmp") | docker load -q >/dev/null \
    || { rm -f "$tmp"; die "docker load falló con $bundle_archive"; }
  sleep 1
  got="$(cat "$tmp")"; rm -f "$tmp"
  [ "$got" = "$want" ] || die "SHA256 de images/horus-images.tar no coincide tras cargar: borra las imágenes cargadas y usa un paquete íntegro"
}

get_bundle_cache() { local d="${root:-}/var/cache/horus/bundles"; install -d -m 0700 "$d"; printf '%s' "$d"; }

read_lock() { # read_lock <images.lock> <modo: digest|tag>
  local var ref dig
  while read -r var ref dig; do
    [[ "$var" =~ ^HORUS_[A-Z_]+_IMAGE$|^HORUS_IMAGE$ ]] || continue
    if [ "$2" = digest ] && [ -n "$dig" ]; then img[$var]="$ref@$dig"; else img[$var]="$ref"; fi
  done <"$1"
}

configure_images() {
  horus_version="$(tree_version "$repo_root")"
  # Origen: opción > paquete offline que es este árbol > respuesta guardada (si es la copia
  # instalada en <instalación>/release) > release con images.lock (GHCR) > local (repo clonado).
  if [ -n "${opt[image-source]:-}" ]; then image_source="${opt[image-source]}"
  elif [ -f "$repo_root/images/horus-images.tar" ]; then image_source="bundle:$repo_root"
  elif [ -f "$repo_root/.installed-copy" ] && [ -n "${saved[image-source]:-}" ]; then image_source="${saved[image-source]}"
  elif [ -f "$repo_root/images.lock" ]; then image_source=ghcr
  else image_source=local; fi
  registry="$(get registry ghcr.io/hcdestroyer)"
  case "$image_source" in
    ghcr)
      [ -f "$repo_root/images.lock" ] || die "--image-source ghcr necesita images.lock (árbol de una release; usa bootstrap-debian.sh o horus-ctl upgrade)"
      read_lock "$repo_root/images.lock" digest
      image_source_desc="GHCR ($registry), fijadas por digest"
      ;;
    bundle:*)
      local bsrc="${image_source#bundle:}"
      bundle_archive="" bundle_sums_ok=0
      if [ ! -e "$bsrc" ] && [ -f "$repo_root/.installed-copy" ] && [ -f "$repo_root/images.lock" ]; then
        # Reejecución desde la copia instalada sin el paquete: las imágenes ya están cargadas.
        bundle_dir="$repo_root"
        warn "el paquete offline $bsrc ya no está: se usan las imágenes cargadas de $(tree_version "$repo_root")"
      else
        bundle_tree "$bsrc"
      fi
      [ -f "$bundle_dir/images.lock" ] || die "$bundle_dir no trae images.lock"
      read_lock "$bundle_dir/images.lock" tag
      horus_version="$(tree_version "$bundle_dir")"
      # Compose, configuración y scripts de la MISMA versión que las imágenes.
      repo_root="$bundle_dir"
      image_source="bundle:$bsrc"
      image_source_desc="paquete offline $bsrc ($horus_version)"
      ;;
    local)
      img[HORUS_IMAGE]="$(get image horus:local)"
      img[HORUS_WEB_IMAGE]="$(get web-image horus-web:local)"
      img[HORUS_POSTGRES_IMAGE]="$(get postgres-image horus-postgres:18.6-pgbackrest2.59.3)"
      image_source_desc="locales (${img[HORUS_IMAGE]}, ${img[HORUS_WEB_IMAGE]})"
      ;;
    *) die "--image-source inválido: $image_source (ghcr | bundle:RUTA | local)" ;;
  esac
  image="${img[HORUS_IMAGE]:?images.lock sin HORUS_IMAGE}"
  [ -n "${img[HORUS_WEB_IMAGE]:-}" ] || die "falta la imagen horus-web (images.lock o --web-image)"
  [ -n "${img[HORUS_POSTGRES_IMAGE]:-}" ] || img[HORUS_POSTGRES_IMAGE]=horus-postgres:18.6-pgbackrest2.59.3
}

configure_updates() {
  channel="$(get channel stable)"
  case "$channel" in stable | beta) ;; *) die "--channel inválido: $channel (stable | beta)" ;; esac
  auto_update="$(get auto-update off)"
  case "$auto_update" in on | off) ;; *) die "--auto-update inválido: $auto_update (on | off)" ;; esac
  update_window="$(get update-window "Sun *-*-* 03:30:00")"
  update_source="$(get update-source "https://api.github.com/repos/hcdestroyer/horus-flow/releases?per_page=50")"
  update_check="$(get update-check true)"
  registry_user="$(get registry-user "")"
  if [ -n "${opt[registry-token-file]:-}" ]; then
    [ -r "${opt[registry-token-file]}" ] || die "no puedo leer ${opt[registry-token-file]}"
    install -d -m 0755 "$etc_dir"
    (umask 077 && head -n1 "${opt[registry-token-file]}" | tr -d '\r\n' >"$etc_dir/registry-token")
    chmod 0600 "$etc_dir/registry-token"
    [ -n "$registry_user" ] || registry_user=token
  fi
}

save_conf() {
  mkdir -p "$etc_dir"; chmod 0755 "$etc_dir"
  {
    echo "# Respuestas de scripts/install.sh (I1-22). Sin secretos. Se reutilizan al volver a ejecutarlo."
    printf '%s=%s\n' mode "$mode" domain "$domain" acme-email "$acme_email" public-ip "$public_ip" \
      tunnel-cidr "$tunnel_cidr" admin-email "$admin_email" image "$image" store-dir "$store_dir" \
      data-dir "$data_dir" install-dir "$install_dir" project "$project" http-port "$http_port" \
      https-port "$https_port" wg-port "$wg_port" wg-interface "$wg_if" docker-subnet "$docker_subnet" \
      tlm-max-bytes "$tlm_max_bytes" smtp-host "$(get smtp-host "")" smtp-port "$(get smtp-port 587)" \
      smtp-from "$(get smtp-from "")" smtp-user "$(get smtp-user "")" smtp-tls "$(get smtp-tls starttls)" \
      backup-metrics-port "$(get backup-metrics-port 9109)" bundle-out "$(get bundle-out "${root:-}/root")" \
      image-source "$image_source" registry "$registry" registry-user "$registry_user" channel "$channel" \
      auto-update "$auto_update" update-window "$update_window" update-source "$update_source" \
      update-check "$update_check" web-image "${img[HORUS_WEB_IMAGE]}" postgres-image "${img[HORUS_POSTGRES_IMAGE]}" \
      version "$horus_version" tls "$tls_choice" public-url "$([ "$tls_choice" = external ] && echo "$public_base_url")" \
      trusted-proxies "$trusted_proxies" http-bind "$([ "$tls_choice" = external ] && echo "$http_bind_addr:$http_port")" \
      wg-endpoint "$(get wg-endpoint "")"
    [ -z "${opt[acme-staging]:-${saved[acme-staging]:-}}" ] || echo "acme-staging=1"
  } >"$conf_file"
  chmod 0644 "$conf_file"
}

# --- Directorios -------------------------------------------------------------------------------
make_dirs() {
  say "Directorios"
  install -d -m 0755 "$install_dir" "$install_dir/bin" "$config_dir" "$data_dir"
  install -d -m 0700 "$secrets_dir" "$secrets_dir/grpc"
  install -d -m 0750 -g 65534 "$tls_dir"
  local d
  install -d -m 0700 -o 70 -g 70 "$data_dir/postgres"
  for d in nats valkey; do install -d -m 0750 "$data_dir/$d"; done
  install -d -m 0750 -o 101 -g 101 "$data_dir/clickhouse" "$data_dir/clickhouse-logs"
  install -d -m 0750 -o 65534 -g 65534 "$data_dir/traefik"
  install -d -m 0755 "$data_dir/metrics"
  install -d -m 0755 "$store_dir" "$store_dir/backups"
  install -d -m 0750 -o 70 -g 70 "$store_dir/backups/postgres"
  install -d -m 0750 -o 101 -g 101 "$store_dir/backups/clickhouse"
  install -d -m 0700 "$store_dir/backups/state"
  for d in archive reports exports audit catalog datasets; do install -d -m 0750 -o 65532 -g 65532 "$store_dir/$d"; done
  ok "$install_dir, $data_dir, $store_dir, $etc_dir"
}

# --- Secretos --------------------------------------------------------------------------------------
# new_secret <archivo> <generador…>: crea el archivo solo si no existe (nunca regenera).
created_secrets=()
new_secret() {
  local f="$secrets_dir/$1"; shift
  [ -s "$f" ] && return 0
  "$@" >"$f.tmp"
  chmod 0644 "$f.tmp"
  mv -f "$f.tmp" "$f"
  created_secrets+=("${f#"$secrets_dir"/}")
}
gen_alnum() { random_alnum 32; }
gen_hex32() { openssl rand -hex 32; }
gen_ed25519() { openssl genpkey -algorithm ed25519 2>/dev/null; }
gen_wg_private() { openssl genpkey -algorithm X25519 -outform DER 2>/dev/null | tail -c 32 | base64; }
wg_public_of() { # clave pública WireGuard (base64) de una privada en base64
  { printf '\x30\x2e\x02\x01\x00\x30\x05\x06\x03\x2b\x65\x6e\x04\x22\x04\x20'; base64 -d <"$1"; } |
    openssl pkey -inform DER -pubout -outform DER 2>/dev/null | tail -c 32 | base64
}

gen_admin_password() {
  local f="${opt[admin-password-file]:-}" p="" p2=""
  if [ -n "$f" ]; then
    [ -r "$f" ] || die "no puedo leer $f"
    p="$(head -n1 "$f" | tr -d '\r\n')"
  elif [ "$interactive" = 1 ]; then
    while :; do
      read -r -s -p "  contraseña del superadmin (≥ 12 caracteres; vacía = generar): " p; echo >&2
      [ -z "$p" ] && break
      read -r -s -p "  repítela: " p2; echo >&2
      [ "$p" = "$p2" ] && [ "${#p}" -ge 12 ] && break
      echo "  no coinciden o es corta" >&2
    done
  fi
  if [ -z "$p" ]; then p="$(random_alnum 20)"; generated_admin_password="$p"; fi
  [ "${#p}" -ge 12 ] || die "la contraseña del superadmin debe tener al menos 12 caracteres"
  printf '%s' "$p"
}

gen_grpc_pki() {
  local g="$secrets_dir/grpc"
  if [ ! -s "$g/ca.crt" ]; then
    openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 3650 -subj "/CN=horus-internal-ca" \
      -keyout "$g/ca.key" -out "$g/ca.crt" -addext "basicConstraints=critical,CA:TRUE" \
      -addext "keyUsage=critical,keyCertSign,cRLSign" 2>/dev/null
    created_secrets+=(grpc/ca.crt)
  fi
  local n
  for n in wireguard wg-agent; do
    [ -s "$g/$n.crt" ] && continue
    openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj "/CN=$n" \
      -keyout "$g/$n.key" -out "$g/$n.csr" 2>/dev/null
    printf 'subjectAltName=DNS:%s\nextendedKeyUsage=serverAuth,clientAuth\nkeyUsage=critical,digitalSignature\nbasicConstraints=CA:FALSE\n' "$n" >"$g/$n.ext"
    openssl x509 -req -in "$g/$n.csr" -CA "$g/ca.crt" -CAkey "$g/ca.key" -CAcreateserial -days 3650 \
      -extfile "$g/$n.ext" -out "$g/$n.crt" 2>/dev/null
    rm -f "$g/$n.csr" "$g/$n.ext"
    created_secrets+=("grpc/$n.crt")
  done
  chmod 0644 "$g"/*.crt "$g"/wireguard.key "$g"/wg-agent.key
  chmod 0600 "$g/ca.key"
}

gen_public_tls() {
  if [ "$tls_mode" = self_signed ]; then
    local cur=""
    [ -s "$tls_dir/public.crt" ] && cur="$(openssl x509 -in "$tls_dir/public.crt" -noout -ext subjectAltName 2>/dev/null | tr -d ' ' | grep -o 'IPAddress:[0-9.]*' | head -1)"
    if [ "$cur" != "IPAddress:$public_ip" ]; then
      openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 825 -subj "/CN=$public_ip/O=Horus Flow" \
        -keyout "$tls_dir/public.key" -out "$tls_dir/public.crt" \
        -addext "subjectAltName=IP:$public_ip" -addext "extendedKeyUsage=serverAuth" \
        -addext "keyUsage=critical,digitalSignature" -addext "basicConstraints=critical,CA:FALSE" 2>/dev/null
      created_secrets+=(tls/public.crt)
    fi
    chgrp 65534 "$tls_dir/public.key"; chmod 0640 "$tls_dir/public.key"; chmod 0644 "$tls_dir/public.crt"
    openssl x509 -in "$tls_dir/public.crt" -noout -fingerprint -sha256 | cut -d= -f2 >"$tls_dir/fingerprint-sha256.txt"
    chmod 0644 "$tls_dir/fingerprint-sha256.txt"
  else
    # ACME (Traefik obtiene el certificado) o external (lo pone el proxy): public.crt vacío.
    [ -e "$tls_dir/public.crt" ] || : >"$tls_dir/public.crt"
    rm -f "$tls_dir/fingerprint-sha256.txt"
  fi
}

make_secrets() {
  say "Secretos ($secrets_dir, nunca se regeneran)"
  local s
  for s in postgres_password clickhouse_password valkey_password nats_password cursor_key pgbackrest_cipher_pass \
    ch_ingester_password ch_analytics_password ch_detection_password ch_alerts_password ch_jobs_password; do
    new_secret "$s" gen_alnum
  done
  new_secret auth_kek gen_hex32
  new_secret devices_kek gen_hex32
  new_secret alerts_kek gen_hex32
  if [ -n "${opt[smtp-password-file]:-}" ]; then
    [ -r "${opt[smtp-password-file]}" ] || die "no puedo leer ${opt[smtp-password-file]}"
    head -n1 "${opt[smtp-password-file]}" | tr -d '\r\n' >"$secrets_dir/smtp_password"
  fi
  [ -e "$secrets_dir/smtp_password" ] || : >"$secrets_dir/smtp_password"
  chmod 0644 "$secrets_dir/smtp_password"
  new_secret auth_signing_key.pem gen_ed25519
  new_secret wg_hub_private_key gen_wg_private
  [ -s "$secrets_dir/seed_admin_password" ] || new_secret seed_admin_password gen_admin_password
  chmod 0600 "$secrets_dir/pgbackrest_cipher_pass" "$secrets_dir/nats_password"
  # Derivados (se reescriben siempre a partir de los anteriores).
  printf 'user default on #%s ~* &* +@all\n' "$(printf '%s' "$(cat "$secrets_dir/valkey_password")" | sha256sum | cut -d' ' -f1)" >"$secrets_dir/valkey_users.acl"
  printf 'authorization {\n  users = [\n    { user: horus, password: "%s" }\n  ]\n}\n' "$(cat "$secrets_dir/nats_password")" >"$secrets_dir/nats_auth.conf"
  printf 'nats://horus:%s@nats:4222\n' "$(cat "$secrets_dir/nats_password")" >"$secrets_dir/nats_url"
  wg_public_of "$secrets_dir/wg_hub_private_key" >"$secrets_dir/wg_hub_public_key"
  [ -n "$(cat "$secrets_dir/wg_hub_public_key")" ] || die "no se pudo derivar la clave pública del hub WireGuard (openssl ≥ 1.1.1)"
  cat >"$secrets_dir/pgbackrest.conf" <<EOF
# pgBackRest de Horus (I1-23; docs/disaster-recovery.md §3.1). Generado por install.sh.
[global]
repo1-type=posix
repo1-path=/var/lib/pgbackrest
repo1-cipher-type=aes-256-cbc
repo1-cipher-pass=$(cat "$secrets_dir/pgbackrest_cipher_pass")
# Sin destino remoto (I1): 3 completos semanales ≥ 14 días de PITR (storage.md §5).
repo1-retention-full-type=count
repo1-retention-full=${HORUS_BACKUP_PG_KEEP_FULL:-3}
repo1-retention-diff=7
compress-type=zst
start-fast=y
archive-async=n
lock-path=/tmp/pgbackrest
log-level-console=info
log-level-file=off

[horus]
pg1-path=/var/lib/postgresql/18/docker
pg1-socket-path=/var/run/postgresql
pg1-user=$(get pg-user horus)
pg1-database=$(get pg-db horus)
EOF
  chmod 0644 "$secrets_dir"/valkey_users.acl "$secrets_dir"/nats_auth.conf "$secrets_dir"/nats_url \
    "$secrets_dir"/pgbackrest.conf "$secrets_dir"/wg_hub_public_key
  gen_grpc_pki
  gen_public_tls
  if [ "${#created_secrets[@]}" -gt 0 ]; then ok "generados: ${created_secrets[*]}"; else ok "ya existían; no se ha regenerado ninguno"; fi
}

# --- Paquete de secretos offline (ADR-0029) ------------------------------------------------------
secrets_digest() { (cd "$etc_dir" && find secrets tls -type f ! -name '*.srl' -print0 | sort -z | xargs -0 sha256sum | sha256sum | cut -d' ' -f1); }

make_bundle() {
  local digest prev out_dir host stamp out tmp
  digest="$(secrets_digest)"
  prev="$(cat "$etc_dir/bundle.sha256" 2>/dev/null || true)"
  if [ "$digest" = "$prev" ] && [ "$confirm_bundle" = 0 ]; then
    if [ -f "$etc_dir/bundle.confirmed" ]; then ok "paquete de secretos offline al día y confirmado"
    else warn "paquete de secretos offline pendiente de confirmar: guárdalo fuera del servidor y ejecuta install.sh --confirm-bundle"; fi
    return 0
  fi
  if [ "$digest" = "$prev" ] && [ "$confirm_bundle" = 1 ]; then
    date -u +%Y-%m-%dT%H:%M:%SZ >"$etc_dir/bundle.confirmed"; ok "paquete de secretos offline confirmado"; return 0
  fi
  say "Paquete de secretos offline (disaster-recovery.md §3.6)"
  out_dir="$(get bundle-out "${root:-}/root")"; install -d -m 0700 "$out_dir"
  host="$(hostname -s 2>/dev/null || hostname)"; stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  tmp="$(mktemp -d)"
  cp -a "$secrets_dir" "$tmp/secrets"; cp -a "$tls_dir" "$tmp/tls"; cp "$conf_file" "$tmp/install.conf"
  [ -f "$env_file" ] && cp "$env_file" "$tmp/horus.env"
  cat >"$tmp/LEEME.txt" <<EOF
Paquete de secretos offline de Horus Flow — servidor $host, $stamp (UTC).
Sin estos secretos NO se pueden leer los backups (pgBackRest cifrado), ni las credenciales
cifradas en PostgreSQL (KEK), ni reconstruir el hub WireGuard sin re-enrolar los routers.
Guárdalo en DOS sitios fuera del servidor (gestor de contraseñas + USB cifrado) y bórralo de aquí.
Restauración: docs/disaster-recovery.md RB-09; copia secrets/ y tls/ a /etc/horus antes de install.sh.
EOF
  local recipient; recipient="$(get bundle-recipient "")"
  if [ -n "$recipient" ]; then
    command -v age >/dev/null 2>&1 || die "--bundle-recipient necesita la herramienta age (apt install age)"
    out="$out_dir/horus-secrets-$host-$stamp.tar.gz.age"
    tar -C "$tmp" -czf - . | age -r "$recipient" -o "$out"
    bundle_msg="cifrado para la clave age $recipient"
  else
    out="$out_dir/horus-secrets-$host-$stamp.tar.gz.enc"
    bundle_passphrase="$(random_alnum 6)-$(random_alnum 6)-$(random_alnum 6)-$(random_alnum 6)"
    tar -C "$tmp" -czf - . | openssl enc -aes-256-cbc -pbkdf2 -iter 600000 -salt -pass fd:3 -out "$out" 3<<<"$bundle_passphrase"
    bundle_msg="cifrado con AES-256 (openssl enc -d -aes-256-cbc -pbkdf2 -iter 600000 -in FICHERO | tar xz)"
  fi
  rm -rf "$tmp"
  chmod 0600 "$out"
  printf '%s\n' "$digest" >"$etc_dir/bundle.sha256"
  rm -f "$etc_dir/bundle.confirmed"
  bundle_file="$out"
  ok "paquete offline: $out ($bundle_msg)"
}

# --- Archivos de la instalación -------------------------------------------------------------------
# Lo que necesita una instalación de su árbol de versión (sin imágenes). Es también el contenido
# de un paquete de release (scripts/release/build-release.sh lee esta lista).
release_paths=(VERSION images.lock deployments/compose/compose.prod.yaml deployments/images/postgres/Dockerfile
  infrastructure/traefik/prod/horus.acme.yml.tmpl infrastructure/traefik/prod/horus.ip.yml.tmpl
  infrastructure/traefik/prod/horus.external.yml.tmpl
  infrastructure/nats/nats.prod.conf infrastructure/nats/kv.yaml infrastructure/backup/clickhouse-backups.xml
  infrastructure/backup/httpd.conf packages/events/streams/streams.yaml scripts/install.sh scripts/horus-ctl.sh
  scripts/bootstrap-debian.sh scripts/backup/horus-backup.sh docs/install-debian.md)

# copy_release: copia el árbol de esta versión a <instalación>/release (lo usan bin/install.sh,
# bin/horus-ctl y la vuelta atrás de horus-ctl upgrade). En un repo sin VERSION se escribe la
# versión de desarrollo.
copy_release() {
  local dst="$install_dir/release" p
  [ "$(readlink -f "$repo_root")" = "$(readlink -f "$dst" 2>/dev/null || true)" ] && return 0
  rm -rf "$dst.new"; install -d -m 0755 "$dst.new"
  for p in "${release_paths[@]}"; do
    [ -e "$repo_root/$p" ] || continue
    install -d -m 0755 "$dst.new/$(dirname "$p")"
    cp -p "$repo_root/$p" "$dst.new/$p"
  done
  [ -f "$dst.new/VERSION" ] || printf '%s\n' "$horus_version" >"$dst.new/VERSION"
  : >"$dst.new/.installed-copy"
  rm -rf "$dst"; mv "$dst.new" "$dst"
}

# IPs de las que Traefik acepta X-Forwarded-*: el proxy externo y, si escucha en loopback (proxy en
# la misma máquina, conexión vía docker-proxy), la puerta de enlace de la red del compose. En los
# modos con TLS propio, ninguna (127.0.0.1/32, que nunca es el origen real).
forwarded_trusted_ips() {
  if [ "$tls_mode" != external ]; then printf '127.0.0.1/32'; return; fi
  local l="$trusted_proxies"
  case "$http_bind_addr" in 127.*) l="$l,$grpc_bind/32" ;; esac
  printf '%s' "$l"
}

render_files() {
  say "Archivos de la instalación ($install_dir)"
  install -m 0644 "$repo_root/deployments/compose/compose.prod.yaml" "$install_dir/compose.yaml"
  install -d -m 0755 "$config_dir/traefik/dynamic" "$config_dir/nats" "$config_dir/backup" "$config_dir/images/postgres"
  local tmpl="$repo_root/infrastructure/traefik/prod/horus.acme.yml.tmpl"
  [ "$tls_mode" = self_signed ] && tmpl="$repo_root/infrastructure/traefik/prod/horus.ip.yml.tmpl"
  [ "$tls_mode" = external ] && tmpl="$repo_root/infrastructure/traefik/prod/horus.external.yml.tmpl"
  sed "s/@HORUS_HOST@/$public_host/g" "$tmpl" >"$config_dir/traefik/dynamic/horus.yml"
  install -m 0644 "$repo_root/infrastructure/nats/nats.prod.conf" "$config_dir/nats/nats.prod.conf"
  install -m 0644 "$repo_root/infrastructure/nats/kv.yaml" "$config_dir/nats/kv.yaml"
  install -m 0644 "$repo_root/packages/events/streams/streams.yaml" "$config_dir/nats/streams.yaml"
  install -m 0644 "$repo_root/infrastructure/backup/clickhouse-backups.xml" "$repo_root/infrastructure/backup/httpd.conf" "$config_dir/backup/"
  install -m 0644 "$repo_root/deployments/images/postgres/Dockerfile" "$config_dir/images/postgres/Dockerfile"
  install -m 0755 "$repo_root/scripts/backup/horus-backup.sh" "$install_dir/bin/horus-backup"
  copy_release
  ln -sfn ../release/scripts/install.sh "$install_dir/bin/install.sh"
  ln -sfn ../release/scripts/horus-ctl.sh "$install_dir/bin/horus-ctl"
  [ -n "$root" ] || ln -sfn "$install_dir/bin/horus-ctl" /usr/local/sbin/horus-ctl
  # .env de compose: SIN secretos (los secretos son archivos en $secrets_dir).
  local acme_ca="https://acme-v02.api.letsencrypt.org/directory"
  [ -n "${opt[acme-staging]:-${saved[acme-staging]:-}}" ] && acme_ca="https://acme-staging-v02.api.letsencrypt.org/directory"
  local hub_id
  hub_id="$(cat "$etc_dir/hub-id" 2>/dev/null || true)"
  if [ -z "$hub_id" ]; then
    hub_id="$(cat /proc/sys/kernel/random/uuid)"; printf '%s\n' "$hub_id" >"$etc_dir/hub-id"
  fi
  cat >"$env_file" <<EOF
# Generado por scripts/install.sh (I1-22) — $(date -u +%Y-%m-%dT%H:%M:%SZ). Sin secretos.
# No lo edites: vuelve a ejecutar $install_dir/bin/install.sh con las opciones nuevas.
COMPOSE_PROJECT_NAME=$project
$(for v in "${img_vars[@]}"; do [ -z "${img[$v]:-}" ] || printf '%s=%s\n' "$v" "${img[$v]}"; done)
HORUS_VERSION=$horus_version
HORUS_UPDATE_CHANNEL=$channel
HORUS_UPDATE_SOURCE=$update_source
HORUS_UPDATE_CHECK=$update_check
HORUS_CONFIG_DIR=$config_dir
HORUS_SECRETS_DIR=$secrets_dir
HORUS_TLS_DIR=$tls_dir
HORUS_DATA_ROOT=$data_dir
HORUS_STORE_DIR=$store_dir
HORUS_PG_DB=horus
HORUS_PG_USER=horus
HORUS_CH_DB=horus
HORUS_CH_USER=horus
HORUS_CH_NOFILE=$ch_nofile
HORUS_ACCESS_MODE=$access_mode
HORUS_TLS_MODE=$tls_mode
HORUS_PUBLIC_BASE_URL=$public_base_url
HORUS_ACME_EMAIL=$acme_email
HORUS_ACME_CA_SERVER=$acme_ca
HORUS_PUBLIC_BIND=0.0.0.0
HORUS_HTTP_BIND_ADDR=$http_bind_addr
HORUS_HTTPS_BIND_ADDR=$https_bind_addr
HORUS_FORWARDED_TRUSTED_IPS=$(forwarded_trusted_ips)
HORUS_TRUSTED_PROXIES=$(printf '%s' "$docker_subnet${trusted_proxies:+,$trusted_proxies}")
HORUS_HTTP_PORT=$http_port
HORUS_HTTPS_PORT=$https_port
HORUS_SEED_ADMIN_EMAIL=$admin_email
HORUS_WG_HUB_ID=$hub_id
HORUS_WG_ENDPOINT=$wg_endpoint
HORUS_WG_PORT=$wg_port
HORUS_WG_INTERFACE=$wg_if
HORUS_WG_HUB_PUBLIC_KEY=$(cat "$secrets_dir/wg_hub_public_key")
HORUS_WG_TUNNEL_CIDRS=$tunnel_cidr
HORUS_WG_SERVICES_CIDR=$services_cidr
HORUS_COLLECTOR_IP=$collector_ip
HORUS_DOCKER_SUBNET=$docker_subnet
HORUS_GRPC_BIND=$grpc_bind
HORUS_TLM_FLOWS_MAX_BYTES=$tlm_max_bytes
HORUS_NATS_MAX_FILE_STORE=$nats_max_store
HORUS_BACKUP_METRICS_PORT=$(get backup-metrics-port 9109)
HORUS_RAW_TTL_DAYS=7
HORUS_SMTP_HOST=$(get smtp-host "")
HORUS_SMTP_PORT=$(get smtp-port 587)
HORUS_SMTP_FROM=$(get smtp-from "")
HORUS_SMTP_USERNAME=$(get smtp-user "")
HORUS_SMTP_TLS=$(get smtp-tls starttls)
HORUS_BACKUP_CH_KEEP_FULL=3
EOF
  chmod 0644 "$env_file"
  # Primer metrics.prom (lo sirve backup-metrics desde el arranque).
  HORUS_ENV_FILE="$env_file" "$install_dir/bin/horus-backup" metrics >/dev/null 2>&1 || true
  ok "compose.yaml, .env, config/, release/ ($horus_version), bin/{horus-backup,horus-ctl,install.sh}"
}

# Tamaños de NATS según el disco: telemetría = 50 GB por defecto (≥ 6 h de autonomía, C-12),
# limitada a un 30 % del disco libre de datos; max_file_store = streams de eventos (≈ 17 GiB) +
# telemetría + margen.
size_nats() {
  local free_b; free_b=$(($(df -P -k "$(existing_parent "$data_dir")" | awk 'NR == 2 { print $4 }') * 1024))
  tlm_max_bytes="$(get tlm-max-bytes "")"
  if [ -z "$tlm_max_bytes" ]; then
    tlm_max_bytes=53687091200
    [ "$tlm_max_bytes" -le $((free_b * 3 / 10)) ] || tlm_max_bytes=$((free_b * 3 / 10))
    [ "$tlm_max_bytes" -ge 1073741824 ] || tlm_max_bytes=1073741824
  fi
  nats_max_store=$(((tlm_max_bytes + 20 * 1073741824) / 1048576))MB
  # nofile de ClickHouse: 262144 si el daemon lo permite (si no, el límite del host).
  ch_nofile="$(get ch-nofile "")"
  if [ -z "$ch_nofile" ]; then
    local hard; hard="$(ulimit -Hn)"
    ch_nofile=262144
    [ "$hard" = unlimited ] || [ "$hard" -ge 262144 ] || ch_nofile="$hard"
  fi
}

# --- Imágenes ------------------------------------------------------------------------------------
ensure_images() {
  say "Imágenes ($image_source_desc)"
  local v ref
  case "$image_source" in
    ghcr)
      registry_login
      for v in HORUS_IMAGE HORUS_WEB_IMAGE HORUS_POSTGRES_IMAGE; do verify_signature "${img[$v]}"; done
      for v in "${img_vars[@]}"; do
        ref="${img[$v]:-}"; [ -n "$ref" ] || continue
        if docker image inspect "$ref" >/dev/null 2>&1; then continue; fi
        docker pull -q "$ref" >/dev/null || die "no se pudo descargar $ref (¿sin salida a $registry? usa el paquete offline: --image-source bundle:RUTA)"
      done
      ok "imágenes descargadas y fijadas por digest"
      ;;
    bundle:*)
      local missing=0
      for v in "${img_vars[@]}"; do
        ref="${img[$v]:-}"; [ -n "$ref" ] || continue
        docker image inspect "$ref" >/dev/null 2>&1 || missing=1
      done
      if [ "$missing" = 1 ]; then
        say "cargando las imágenes del paquete offline (docker load, puede tardar)"
        load_bundle_images
      fi
      for v in "${img_vars[@]}"; do
        ref="${img[$v]:-}"; [ -n "$ref" ] || continue
        docker image inspect "$ref" >/dev/null 2>&1 || die "el paquete offline no contiene $ref"
      done
      ok "imágenes del paquete offline cargadas (SHA256SUMS verificado)"
      ;;
    local)
      image_local "$image" "$repo_root/Dockerfile" "$repo_root"
      image_local "${img[HORUS_WEB_IMAGE]}" "$repo_root/apps/frontend/Dockerfile" "$repo_root"
      image_local "${img[HORUS_POSTGRES_IMAGE]}" "$config_dir/images/postgres/Dockerfile" "$config_dir/images/postgres"
      ;;
  esac
}

# image_local <imagen> <Dockerfile> <contexto>: presente, descargable o construida aquí.
image_local() {
  if docker image inspect "$1" >/dev/null 2>&1; then ok "imagen $1 presente"; return 0; fi
  if docker pull -q "$1" >/dev/null 2>&1; then ok "imagen $1 descargada"; return 0; fi
  [ -f "$2" ] || die "la imagen $1 no existe ni se puede descargar (usa --image-source ghcr o bundle:RUTA)"
  say "construyendo $1 con $2 (puede tardar)"
  docker build -q -t "$1" -f "$2" --build-arg "VERSION=$horus_version" "$3" >/dev/null \
    || die "no se pudo construir $1 (usa --image-source ghcr o bundle:RUTA)"
  ok "imagen $1 construida"
}

# Credenciales de lectura del registro (paquetes privados): <etc>/registry-token (0600).
registry_login() {
  local tok="$etc_dir/registry-token"
  [ -s "$tok" ] || return 0
  docker login "${registry%%/*}" -u "${registry_user:-token}" --password-stdin <"$tok" >/dev/null 2>&1 \
    || die "docker login en ${registry%%/*} falló con el token de $tok (¿caducado o sin read:packages?)"
  ok "sesión de lectura en ${registry%%/*}"
}

# Firma cosign keyless (GitHub OIDC del workflow de release) de las imágenes propias.
cosign_identity() { printf 'https://github.com/%s/.github/workflows/release.yml@refs/tags/v' "${HORUS_REPO:-hcdestroyer/horus-flow}"; }
verify_signature() {
  [ "${HORUS_SKIP_SIGNATURE:-0}" = 1 ] && { warn "HORUS_SKIP_SIGNATURE=1: no se verifica la firma de $1"; return 0; }
  local cosign; cosign="$(command -v cosign || true)"
  [ -n "$cosign" ] || [ ! -x "$install_dir/bin/cosign" ] || cosign="$install_dir/bin/cosign"
  [ -n "$cosign" ] || die "falta cosign para verificar la firma de $1 (bootstrap-debian.sh lo instala; o HORUS_SKIP_SIGNATURE=1 bajo tu responsabilidad)"
  "$cosign" verify --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    --certificate-identity-regexp "^$(cosign_identity)" "$1" >/dev/null 2>&1 \
    || die "la firma cosign de $1 no es válida: no se instala"
  ok "firma cosign de ${1%@*} verificada"
}

# --- Parámetros del kernel ------------------------------------------------------------------------
# net.core.rmem_max/rmem_default: búfer UDP del collector (tests/load/REPORT.md: con 4 MiB se
# perdían flujos en ráfagas; 32 MiB). Son globales del host (no por espacio de red), así que valen
# también dentro del contenedor del collector. ip_forward: el UDP que llega por wg0 se reenvía al
# contenedor del collector (Docker también lo activa; aquí queda persistente). ClickHouse 26.8 no
# exige vm.max_map_count.
sysctl_file() { printf '%s' "${root:-}/etc/sysctl.d/90-horus.conf"; }
setup_sysctl() {
  say "Parámetros del kernel ($(sysctl_file))"
  local rmem="${HORUS_UDP_RMEM_BYTES:-33554432}" kv k v cur
  install -d -m 0755 "$(dirname "$(sysctl_file)")"
  cat >"$(sysctl_file)" <<EOF
# Horus Flow (scripts/install.sh). Búfer UDP del collector de IPFIX/NetFlow y reenvío del túnel.
net.core.rmem_max = $rmem
net.core.rmem_default = $rmem
net.ipv4.ip_forward = 1
EOF
  chmod 0644 "$(sysctl_file)"
  [ -z "$root" ] || { ok "escrito (raíz de pruebas: no se aplica)"; return 0; }
  for kv in "net.core.rmem_max=$rmem" "net.core.rmem_default=$rmem" "net.ipv4.ip_forward=1"; do
    k="${kv%%=*}"; v="${kv#*=}"
    sysctl -q -w "$k=$v" >/dev/null 2>&1 || true
    cur="$(sysctl -n "$k" 2>/dev/null || echo "?")"
    if [ "$cur" = "$v" ]; then ok "$k = $v"
    else warn "$k = $cur (no se pudo poner $v: ¿contenedor sin acceso al kernel del host?)"; fi
  done
}

# --- Túnel WireGuard y cortafuegos ------------------------------------------------------------------
tunnel_script() {
  cat <<EOF
#!/bin/sh
# horus-tunnel — interfaz del hub WireGuard ($wg_if) y filtro de IPFIX/NetFlow (I1-22).
# Generado por install.sh; lo ejecuta horus-tunnel.service al arrancar (antes del compose).
# La clave, el puerto y los peers los gestiona horus-wg-agent; aquí solo interfaz y direcciones.
set -e
case "\${1:-up}" in
  up)
    if ! ip link show $wg_if >/dev/null 2>&1; then
      if ! ip link add $wg_if type wireguard 2>/dev/null; then
        # Sin módulo del kernel: wireguard-go en primer plano y desligado (su modo demonio no
        # sobrevive en algunos entornos); UAPI en /var/run/wireguard/$wg_if.sock.
        mkdir -p /var/run/wireguard
        setsid wireguard-go -f $wg_if </dev/null >>/var/log/horus-wireguard-go.log 2>&1 9>&- &
        i=0
        until [ -S /var/run/wireguard/$wg_if.sock ] && ip link show $wg_if >/dev/null 2>&1; do
          i=\$((i + 1)); [ \$i -lt 50 ] || { echo "horus-tunnel: wireguard-go no creó $wg_if" >&2; exit 1; }; sleep 0.2
        done
      fi
    fi
    ip addr replace $collector_ip/${services_cidr#*/} dev $wg_if
    ip link set $wg_if up
    ip route replace $tunnel_cidr dev $wg_if
EOF
  if [ "$skip_fw" = 0 ]; then
    cat <<EOF
    # IPFIX (4739) y NetFlow v9 (2055) solo desde el túnel: cualquier otra interfaz se descarta
    # antes de llegar al contenedor del colector (DOCKER-USER) y al host (INPUT).
    for chain in DOCKER-USER INPUT; do
      iptables -N \$chain 2>/dev/null || true
      iptables -C \$chain ! -i $wg_if -p udp -m multiport --dports 4739,2055 -m comment --comment horus-ipfix-only-wg -j DROP 2>/dev/null \\
        || iptables -I \$chain ! -i $wg_if -p udp -m multiport --dports 4739,2055 -m comment --comment horus-ipfix-only-wg -j DROP
    done
EOF
  fi
  if [ "$skip_fw" = 0 ] && [ "$tls_mode" = external ]; then
    local c
    printf '    # Modo proxy externo: el HTTP de Traefik (%s/tcp) solo desde --trusted-proxies.\n' "$http_port"
    printf '    for chain in DOCKER-USER INPUT; do\n'
    printf '      iptables -N $chain 2>/dev/null || true\n'
    printf '      while iptables -D $chain -p tcp -m conntrack --ctorigdstport %s -m comment --comment horus-proxy-only -j DROP 2>/dev/null; do :; done\n' "$http_port"
    printf '      iptables -I $chain -p tcp -m conntrack --ctorigdstport %s -m comment --comment horus-proxy-only -j DROP\n' "$http_port"
    IFS=, read -r -a _tp <<<"$trusted_proxies"
    for c in "${_tp[@]}" 127.0.0.0/8; do
      printf '      iptables -C $chain -s %s -p tcp -m conntrack --ctorigdstport %s -m comment --comment horus-proxy-only -j ACCEPT 2>/dev/null || iptables -I $chain -s %s -p tcp -m conntrack --ctorigdstport %s -m comment --comment horus-proxy-only -j ACCEPT\n' "$c" "$http_port" "$c" "$http_port"
    done
    printf '    done\n'
  fi
  cat <<EOF
    ;;
  down)
    for chain in DOCKER-USER INPUT; do
      while iptables -D \$chain ! -i $wg_if -p udp -m multiport --dports 4739,2055 -m comment --comment horus-ipfix-only-wg -j DROP 2>/dev/null; do :; done
      iptables -S \$chain 2>/dev/null | grep -- '--comment horus-proxy-only' | sed 's/^-A /-D /' | while read -r r; do eval "iptables \$r" 2>/dev/null || true; done
    done
    ip link del $wg_if 2>/dev/null || true
    pkill -f "wireguard-go $wg_if" 2>/dev/null || true
    rm -f /var/run/wireguard/$wg_if.sock
    ;;
esac
EOF
}

setup_tunnel() {
  [ "$skip_tunnel" = 0 ] || { warn "--skip-tunnel: la interfaz $wg_if y la IP $collector_ip deben existir ya"; return 0; }
  say "Hub WireGuard ($wg_if) y filtro de IPFIX"
  tunnel_script >"$install_dir/bin/horus-tunnel"
  chmod 0755 "$install_dir/bin/horus-tunnel"
  ip link show "$wg_if" >/dev/null 2>&1 || tunnel_created=1
  "$install_dir/bin/horus-tunnel" up
  ok "$wg_if con $collector_ip/${services_cidr#*/}, ruta $tunnel_cidr"
  [ "$skip_fw" = 1 ] && warn "--skip-firewall: el UDP 4739/2055 no se filtra por interfaz" || ok "UDP 4739/2055 solo por $wg_if (DOCKER-USER e INPUT)"
}

# --- systemd / cron ------------------------------------------------------------------------------
unit_dir() { printf '%s' "${root:-}/etc/systemd/system"; }
have_systemd() { [ -z "$root" ] && [ "$skip_systemd" = 0 ] && [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; }

write_units() {
  local u; u="$(unit_dir)"; install -d -m 0755 "$u"
  local dc="/usr/bin/docker compose --project-directory $install_dir -f $install_dir/compose.yaml --env-file $env_file"
  cat >"$u/horus-tunnel.service" <<EOF
[Unit]
Description=Horus Flow: interfaz del hub WireGuard y filtro de IPFIX
Before=docker.service horus.service
After=network-online.target
Wants=network-online.target
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=$install_dir/bin/horus-tunnel up
ExecStop=$install_dir/bin/horus-tunnel down
[Install]
WantedBy=multi-user.target
EOF
  cat >"$u/horus.service" <<EOF
[Unit]
Description=Horus Flow (docker compose)
Requires=docker.service
After=docker.service horus-tunnel.service
Wants=horus-tunnel.service
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=$dc up -d --wait --wait-timeout 600
ExecStop=$dc stop
TimeoutStartSec=900
[Install]
WantedBy=multi-user.target
EOF
  # Actualización automática de PARCHES (opcional, off por defecto): horus-ctl auto-update solo
  # aplica X.Y.Z → X.Y.Z' del canal, con backup previo, healthcheck y vuelta atrás.
  if [ "$auto_update" = on ]; then
    cat >"$u/horus-autoupdate.service" <<EOF
[Unit]
Description=Horus Flow: actualización automática de parches (horus-ctl auto-update)
After=horus.service
[Service]
Type=oneshot
ExecStart=$install_dir/bin/horus-ctl auto-update
TimeoutStartSec=3600
EOF
    cat >"$u/horus-autoupdate.timer" <<EOF
[Unit]
Description=Horus Flow: ventana de mantenimiento para parches
[Timer]
OnCalendar=$update_window
Persistent=false
RandomizedDelaySec=300
[Install]
WantedBy=timers.target
EOF
  else
    rm -f "$u/horus-autoupdate.service" "$u/horus-autoupdate.timer"
  fi
  local name cal args
  for spec in "run|*-*-* 02:15:00 UTC|run all" "verify|Sun *-*-* 06:00:00 UTC|verify all" \
    "ttl|*-*-* 05:10:00 UTC|check-ttl" "metrics|*:0/5|metrics"; do
    IFS='|' read -r name cal args <<<"$spec"
    cat >"$u/horus-backup-$name.service" <<EOF
[Unit]
Description=Horus Flow: horus-backup $args (I1-23)
After=horus.service
[Service]
Type=oneshot
ExecStart=$install_dir/bin/horus-backup $args
Nice=10
IOSchedulingClass=idle
EOF
    cat >"$u/horus-backup-$name.timer" <<EOF
[Unit]
Description=Horus Flow: horus-backup $args
[Timer]
OnCalendar=$cal
Persistent=true
RandomizedDelaySec=120
[Install]
WantedBy=timers.target
EOF
  done
}

setup_schedules() {
  write_units
  if have_systemd; then
    systemctl daemon-reload
    systemctl enable horus-tunnel.service horus.service >/dev/null 2>&1
    systemctl enable --now horus-backup-run.timer horus-backup-verify.timer horus-backup-ttl.timer horus-backup-metrics.timer >/dev/null 2>&1
    ok "systemd: horus-tunnel, horus y timers horus-backup-{run,verify,ttl,metrics}"
    if [ "$auto_update" = on ]; then
      systemctl enable --now horus-autoupdate.timer >/dev/null 2>&1
      ok "actualización automática de parches: $update_window (horus-autoupdate.timer)"
    else
      systemctl disable --now horus-autoupdate.timer >/dev/null 2>&1 || true
    fi
  elif [ -z "$root" ] && [ -d /etc/cron.d ] && [ "$skip_systemd" = 0 ]; then
    cat >/etc/cron.d/horus-backup <<EOF
# Horus Flow — backups (I1-23). Sin systemd; generado por install.sh.
15 2 * * * root $install_dir/bin/horus-backup run all >>/var/log/horus-backup.log 2>&1
0 6 * * 0 root $install_dir/bin/horus-backup verify all >>/var/log/horus-backup.log 2>&1
10 5 * * * root $install_dir/bin/horus-backup check-ttl >>/var/log/horus-backup.log 2>&1
*/5 * * * * root $install_dir/bin/horus-backup metrics >/dev/null 2>&1
EOF
    warn "sin systemd: backups programados en /etc/cron.d/horus-backup; el arranque tras reiniciar depende de restart: unless-stopped y de ejecutar $install_dir/bin/horus-tunnel up"
  else
    warn "units de systemd escritas en $(unit_dir) pero NO activadas (--root/--skip-systemd o sin systemd)"
  fi
}

# --- Arranque ------------------------------------------------------------------------------------
compose_up() {
  say "Arranque (docker compose up --wait)"
  if ! "${compose[@]}" up -d --wait --wait-timeout "${HORUS_INSTALL_WAIT:-600}"; then
    "${compose[@]}" ps -a || true
    "${compose[@]}" logs --tail 40 || true
    die "el compose no quedó healthy"
  fi
  # Si la interfaz del hub se acaba de (re)crear, el colector (publicado en su IP) y wg-agent
  # (clave, puerto y peers) se recrean para engancharse a ella.
  if [ "${tunnel_created:-0}" = 1 ] && [ "${HORUS_FRESH_COMPOSE:-0}" = 0 ]; then
    "${compose[@]}" up -d --wait --force-recreate --no-deps horus-collector horus-wg-agent >/dev/null 2>&1 || die "no se pudo recrear horus-collector/horus-wg-agent"
  fi
  "${compose[@]}" ps --format 'table {{.Service}}\t{{.Status}}'
}

first_backup() {
  [ "$skip_backup" = 0 ] || { warn "--skip-backup: sin backup inicial"; return 0; }
  say "Backups locales (I1-23): stanza de pgBackRest y primer backup"
  "$install_dir/bin/horus-backup" pg-init >/dev/null || die "pgBackRest stanza-create/check falló"
  ok "pgBackRest: stanza 'horus' y archivo de WAL continuo verificados"
  if [ -z "$(find "$store_dir/backups/postgres/backup" -maxdepth 2 -name '*F' 2>/dev/null | head -1)" ]; then
    "$install_dir/bin/horus-backup" run all >/dev/null || die "el primer backup falló (ver $store_dir/backups/state/alerts.log)"
    ok "primer backup completo de PostgreSQL y ClickHouse"
  else
    ok "ya hay backups completos"
  fi
  "$install_dir/bin/horus-backup" check-ttl >/dev/null && ok "TTL de flows.flows_raw = 7 días (storage.md)" || warn "el TTL de flows.flows_raw no coincide con storage.md"
  "$install_dir/bin/horus-backup" metrics >/dev/null 2>&1 || true
}

summary() {
  echo
  say "Horus Flow instalado"
  echo "  URL:            $public_base_url  (API en /api/v1; modo $access_mode)"
  if [ "$tls_mode" = self_signed ]; then
    echo "  Certificado:    autogenerado para $public_ip — huella SHA-256:"
    echo "                  $(cat "$tls_dir/fingerprint-sha256.txt")"
    echo "                  compruébala en el navegador antes de aceptar el aviso."
  elif [ "$tls_mode" = external ]; then
    echo "  Proxy inverso:  apunta tu proxy (esquema http) a $http_bind_addr:$http_port con WebSockets activado;"
    echo "                  solo se aceptan X-Forwarded-* de $trusted_proxies (docs/install-debian.md)"
  else
    echo "  Certificado:    Let's Encrypt para $domain (se emite en la primera petición HTTPS)"
  fi
  echo "  WireGuard:      UDP $wg_port en $public_host; hub/colector $collector_ip; túneles $tunnel_cidr"
  echo "  Superadmin:     $admin_email (el primer login exige cambiar la contraseña y activar TOTP)"
  [ -z "${generated_admin_password:-}" ] || echo "                  contraseña inicial generada: $generated_admin_password  (anótala; no se volverá a mostrar)"
  if [ -n "${bundle_file:-}" ]; then
    echo "  SECRETOS:       $bundle_file"
    [ -z "${bundle_passphrase:-}" ] || echo "                  frase de descifrado: $bundle_passphrase  (NO se guarda en el servidor)"
    echo "                  cópialo FUERA del servidor (2 sitios), bórralo de aquí y ejecuta: $install_dir/bin/install.sh --confirm-bundle"
  fi
  echo "  Backups:        $store_dir/backups (horus-backup status); métricas en 127.0.0.1:$(get backup-metrics-port 9109)/metrics.prom"
  echo "  Versión:        $horus_version (canal $channel; actualización automática de parches: $auto_update)"
  echo "  Gestión:        horus-ctl status | logs | upgrade | backup | restore | uninstall  (horus-ctl help)"
  echo "  Comprobar:      $install_dir/bin/install.sh --check"
  [ "$warnings" = 0 ] || echo "  Avisos:         $warnings (ver arriba)"
}

do_install() {
  configure
  check_requirements
  size_nats
  save_conf
  make_dirs
  make_secrets
  render_files
  make_bundle
  ensure_images
  setup_sysctl
  setup_tunnel
  compose_up
  first_backup
  setup_schedules
  summary
}

# --- --check -------------------------------------------------------------------------------------
# URL local de Traefik para las comprobaciones (HTTPS propio o, en modo externo, el HTTP del proxy).
traefik_base() {
  if [ "${HORUS_TLS_MODE:-}" = external ]; then
    local a="${HORUS_HTTP_BIND_ADDR:-127.0.0.1}"; [ "$a" != 0.0.0.0 ] || a=127.0.0.1
    printf 'http://%s:%s' "$a" "$HORUS_HTTP_PORT"
  else
    printf 'https://127.0.0.1:%s' "$HORUS_HTTPS_PORT"
  fi
}

do_check() {
  [ -f "$env_file" ] || die "no hay instalación en $install_dir"
  set -a; . "$env_file"; set +a
  say "Contenedores (salud de cada rol)"
  local svc st
  while read -r svc st; do
    [ -n "$svc" ] || continue
    case "$st" in healthy) ok "$svc healthy" ;; exited*) [ "$svc" = nats-init ] && ok "$svc completado" || fail "$svc $st" ;; *) fail "$svc $st" ;; esac
  done < <("${compose[@]}" ps -a --format '{{.Service}} {{if .Health}}{{.Health}}{{else}}{{.State}}{{end}}')
  local cid; cid="$("${compose[@]}" ps -q horus-app 2>/dev/null || true)"
  if [ -n "$cid" ]; then
    local ready; ready="$(docker exec "$cid" /horus healthcheck >/dev/null 2>&1 && echo ok || echo ko)"
    [ "$ready" = ok ] && ok "horus-app /readyz (todos sus roles listos)" || fail "horus-app /readyz no responde 200"
  fi
  say "Puertos"
  local code base host="${HORUS_PUBLIC_BASE_URL#https://}"
  base="$(traefik_base)"
  if [ "${HORUS_TLS_MODE:-}" = external ]; then
    port_busy tcp "$HORUS_HTTP_PORT" && ok "HTTP $HORUS_HTTP_BIND_ADDR:$HORUS_HTTP_PORT/tcp escuchando (para el proxy inverso)" || fail "HTTP $HORUS_HTTP_PORT/tcp no escucha"
    ok "TLS lo termina tu proxy inverso en $HORUS_PUBLIC_BASE_URL (X-Forwarded-* solo de $HORUS_FORWARDED_TRUSTED_IPS)"
  else
    port_busy tcp "$HORUS_HTTPS_PORT" && ok "HTTPS $HORUS_HTTPS_PORT/tcp escuchando" || fail "HTTPS $HORUS_HTTPS_PORT/tcp no escucha"
    port_busy tcp "$HORUS_HTTP_PORT" && ok "HTTP $HORUS_HTTP_PORT/tcp escuchando (redirección y ACME)" || fail "HTTP $HORUS_HTTP_PORT/tcp no escucha"
  fi
  code="$(curl -sk -o /dev/null -m 10 -w '%{http_code}' "$base/api/v1/system/status" -H "Host: $host" || true)"
  [ "$code" = 401 ] && ok "API vía Traefik responde (401 sin token, esperado)" || fail "API vía Traefik: HTTP ${code:-sin respuesta} (se esperaba 401)"
  code="$(curl -sk -o /dev/null -m 10 -w '%{http_code}' "$base/" -H "Host: $host" || true)"
  [ "$code" = 200 ] && ok "interfaz web vía Traefik responde (200)" || fail "interfaz web vía Traefik: HTTP ${code:-sin respuesta} (se esperaba 200)"

  if ip link show "$HORUS_WG_INTERFACE" >/dev/null 2>&1; then
    ok "interfaz $HORUS_WG_INTERFACE presente ($(ip -4 -o addr show "$HORUS_WG_INTERFACE" | awk '{ print $4 }' | head -1))"
    if command -v wg >/dev/null 2>&1; then
      local lp; lp="$(timeout 5 wg show "$HORUS_WG_INTERFACE" listen-port 2>/dev/null || true)"
      [ "$lp" = "$HORUS_WG_PORT" ] && ok "WireGuard escucha en UDP $lp" || fail "WireGuard: puerto '${lp:-?}' (esperado $HORUS_WG_PORT; lo configura horus-wg-agent)"
    fi
  else
    fail "falta la interfaz $HORUS_WG_INTERFACE"
  fi
  iptables -C DOCKER-USER ! -i "$HORUS_WG_INTERFACE" -p udp -m multiport --dports 4739,2055 -m comment --comment horus-ipfix-only-wg -j DROP 2>/dev/null \
    && ok "IPFIX/NetFlow filtrado: solo por $HORUS_WG_INTERFACE" || warn "sin regla DOCKER-USER para IPFIX (¿--skip-firewall?)"
  say "TLS ($HORUS_TLS_MODE)"
  if [ "$HORUS_TLS_MODE" = self_signed ]; then
    local end; end="$(openssl x509 -in "$HORUS_TLS_DIR/public.crt" -noout -enddate | cut -d= -f2)"
    ok "certificado autogenerado, caduca $end"
    ok "huella SHA-256 $(cat "$HORUS_TLS_DIR/fingerprint-sha256.txt")"
    openssl x509 -in "$HORUS_TLS_DIR/public.crt" -noout -checkend $((30 * 86400)) >/dev/null || warn "el certificado caduca en < 30 días: vuelve a ejecutar install.sh tras borrar $HORUS_TLS_DIR/public.crt"
  elif [ "$HORUS_TLS_MODE" = external ]; then
    ok "lo pone tu proxy inverso (sin HSTS en Horus: actívalo en el proxy si quieres)"
  else
    ok "Let's Encrypt (acme.json en $HORUS_DATA_ROOT/traefik)"
  fi
  say "Disco"
  local d pct
  for d in "$HORUS_DATA_ROOT" "$HORUS_STORE_DIR"; do
    pct="$(df -P "$d" | awk 'NR == 2 { sub("%", "", $5); print $5 }')"
    if [ "$pct" -lt 70 ]; then ok "$d al $pct %"; elif [ "$pct" -lt 85 ]; then warn "$d al $pct % (≥ 70 %)"; else fail "$d al $pct % (≥ 85 %)"; fi
  done
  say "Backups"
  "$install_dir/bin/horus-backup" metrics >/dev/null 2>&1 || true
  "$install_dir/bin/horus-backup" status | sed 's/^/  /'
  [ -f "$etc_dir/bundle.confirmed" ] && ok "paquete de secretos offline confirmado" || warn "paquete de secretos offline sin confirmar (install.sh --confirm-bundle)"
  echo
  if [ "$failures" -gt 0 ]; then echo "check: $failures fallo(s), $warnings aviso(s)"; exit 1; fi
  echo "check: OK ($warnings aviso(s))"
}

# --- --uninstall ---------------------------------------------------------------------------------
do_uninstall() {
  say "Desinstalación de Horus Flow ($install_dir)"
  if [ "$opt_purge" = 1 ] && [ "$opt_yes" = 0 ]; then
    local a=""
    read -r -p "  --purge BORRA datos, backups y secretos ($data_dir, $store_dir, $etc_dir). Escribe 'borrar': " a || true
    [ "$a" = borrar ] || die "cancelado"
  fi
  if [ -f "$install_dir/compose.yaml" ] && [ -f "$env_file" ]; then
    "${compose[@]}" down --remove-orphans || true
    ok "contenedores parados y eliminados"
  fi
  local u; u="$(unit_dir)"
  if have_systemd; then
    systemctl disable --now horus-backup-run.timer horus-backup-verify.timer horus-backup-ttl.timer horus-backup-metrics.timer horus-autoupdate.timer horus.service >/dev/null 2>&1 || true
  fi
  [ -x "$install_dir/bin/horus-tunnel" ] && "$install_dir/bin/horus-tunnel" down && ok "interfaz WireGuard y reglas de cortafuegos eliminadas"
  if have_systemd; then systemctl disable horus-tunnel.service >/dev/null 2>&1 || true; fi
  rm -f "$u"/horus.service "$u"/horus-tunnel.service "$u"/horus-backup-*.service "$u"/horus-backup-*.timer \
    "$u"/horus-autoupdate.service "$u"/horus-autoupdate.timer "$(sysctl_file)"
  if [ -z "$root" ]; then
    rm -f /etc/cron.d/horus-backup
    [ "$(readlink /usr/local/sbin/horus-ctl 2>/dev/null)" = "$install_dir/bin/horus-ctl" ] && rm -f /usr/local/sbin/horus-ctl
  fi
  have_systemd && systemctl daemon-reload || true
  rm -rf "$install_dir"
  ok "units, cron y $install_dir eliminados"
  if [ "$opt_purge" = 1 ]; then
    rm -rf "$store_dir" "$data_dir" "$etc_dir"
    ok "datos, backups y secretos eliminados ($data_dir, $store_dir, $etc_dir)"
  else
    ok "se conservan datos ($data_dir), backups ($store_dir) y secretos ($etc_dir): reinstalar los reutiliza"
  fi
}

# --confirm-bundle sin más opciones: solo marca el paquete offline como guardado.
n_opts="${#opt[@]}"
[ -z "${opt[root]:-}" ] || n_opts=$((n_opts - 1))
[ -z "${opt[etc-dir]:-}" ] || n_opts=$((n_opts - 1))
if [ "$action" = install ] && [ "$confirm_bundle" = 1 ] && [ "$n_opts" -eq 0 ] && [ -f "$etc_dir/bundle.sha256" ]; then
  if [ "$(secrets_digest)" = "$(cat "$etc_dir/bundle.sha256")" ]; then
    date -u +%Y-%m-%dT%H:%M:%SZ >"$etc_dir/bundle.confirmed"
    echo "install.sh: paquete de secretos offline confirmado"
    exit 0
  fi
  die "los secretos cambiaron desde el último paquete: ejecuta install.sh para generar uno nuevo"
fi

case "$action" in
  install) do_install ;;
  check) do_check ;;
  uninstall) do_uninstall ;;
esac
