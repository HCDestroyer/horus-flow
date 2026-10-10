#!/usr/bin/env bash
# run.sh — `make test-install-debian` (historia I1-22): instalación de punta a punta en un Debian
# "recién instalado" con systemd como PID 1 y Docker dentro (contenedor privilegiado).
#
#   DEBIAN_VERSION=12|13 TEST_MODE=apt|sandbox bash tests/install/debian/run.sh
#
# Pasos (cada uno OK/FAIL en el resumen; logs en bin/test-install-debian/<versión>/):
#   1. Imágenes de prueba: horus A (0.9.0), B (0.9.1) y C rota (0.9.2, no arranca), horus-web y
#      horus-postgres; paquetes con scripts/release/build-release.sh --source local
#      (A completo con todas las imágenes; B y C ligeros, --own-images-only).
#   2. Contenedor Debian con systemd. TEST_MODE=apt (CI): bootstrap-debian.sh instala TODO por apt
#      (Docker oficial, wireguard-tools, age, jq, iptables, NTP). TEST_MODE=sandbox (sin espejos de
#      Debian): binarios equivalentes (Docker estático, iptables nft del host…) y bootstrap --no-apt.
#   3. bootstrap-debian.sh --bundle A --mode ip: todos los servicios sanos, UI (200) y API (401) en
#      https://IP con el certificado autogenerado verificado por su huella; install.sh --check.
#   4. Segunda ejecución: idempotente, secretos idénticos.
#   5. "Reinicio": se reinicia el contenedor entero (systemd arranca docker, horus-tunnel y horus).
#   6. horus-ctl upgrade --bundle B → 0.9.1 sano; horus-ctl upgrade --bundle C (rota) → vuelta
#      atrás automática a 0.9.1 sana (código 3).
#   7. horus-ctl uninstall (conserva datos) y reinstalación con los mismos secretos; uninstall --purge.
#   8. --tls external detrás de un nginx con TLS autofirmado (nginx-proxy.conf): UI/API, e2e de
#      humo (login, cookie Secure, TOTP) por el proxy, WebSocket, X-Forwarded-For falsificado
#      ignorado (rate limit) e IP real en sesiones y auditoría (TEST_PROXY=0 lo salta).
#
# Variables: DEBIAN_VERSION (12), TEST_MODE (apt), TEST_KEEP=1 (no borra el contenedor),
# TEST_IMAGE_MODE (prebuilt|dockerfile para la imagen horus; prebuilt por defecto en sandbox),
# WEB_DIST (SPA generada; por defecto apps/frontend/.output/public, se construye con pnpm si falta).

set -euo pipefail

repo_root="$(cd "$(dirname "$(readlink -f "$0")")/../../.." && pwd)"
here="$repo_root/tests/install/debian"
ver="${DEBIAN_VERSION:-12}"
mode="${TEST_MODE:-apt}"
out="$repo_root/bin/test-install-debian/debian$ver"
work="$repo_root/bin/test-install-debian/work"
name="horus-test-debian$ver"
vol="horus-test-debian$ver-docker"
pass=0 fail=0
mkdir -p "$out" "$work"
: >"$out/summary.txt"

step() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
result() { # result OK|FAIL <descripción>
  if [ "$1" = OK ]; then printf '  \033[32mOK\033[0m   %s\n' "$2"; pass=$((pass + 1))
  else printf '  \033[31mFAIL\033[0m %s\n' "$2"; fail=$((fail + 1)); fi
  printf '%s %s\n' "$1" "$2" >>"$out/summary.txt"
}
check() {
  local d="$1"; shift
  if "$@" >>"$out/checks.log" 2>&1; then result OK "$d"; else
    result FAIL "$d"
    [ "${TEST_FAIL_FAST:-0}" = 0 ] || { echo "TEST_FAIL_FAST=1: se para aquí"; exit 1; }
  fi
}
in_ct() { docker exec "$name" "$@"; }
in_sh() { docker exec "$name" bash -c "$1"; }

cleanup() {
  if [ "${TEST_KEEP:-0}" = 1 ]; then echo "test-install-debian: se conserva $name (TEST_KEEP=1)"; return; fi
  docker rm -f -v "$name" >/dev/null 2>&1 || true
  docker volume rm -f "$vol" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# --- 1. Imágenes y paquetes ------------------------------------------------------------------------
step "1. imágenes y paquetes de prueba (A=0.9.0, B=0.9.1, C=0.9.2 rota)"
img_mode="${TEST_IMAGE_MODE:-$([ "$mode" = sandbox ] && echo prebuilt || echo dockerfile)}"
build_horus() { # build_horus <etiqueta> <versión>
  docker image inspect "$1" >/dev/null 2>&1 && return 0
  ACCEPT_IMAGE="$1" ACCEPT_IMAGE_MODE="$img_mode" VERSION="$2" bash "$repo_root/scripts/accept/image.sh" >>"$out/images.log" 2>&1
}
build_web() {
  docker image inspect horus-web:test-install >/dev/null 2>&1 && return 0
  if [ "$img_mode" = dockerfile ]; then
    docker build -q -t horus-web:test-install -f "$repo_root/apps/frontend/Dockerfile" "$repo_root" >>"$out/images.log" 2>&1
    return
  fi
  local dist="${WEB_DIST:-$repo_root/apps/frontend/.output/public}" ctx="$work/web"
  if [ ! -f "$dist/index.html" ]; then
    (cd "$repo_root/apps/frontend" && pnpm install --frozen-lockfile && pnpm exec nuxt prepare && pnpm build) >>"$out/images.log" 2>&1
    dist="$repo_root/apps/frontend/.output/public"
  fi
  rm -rf "$ctx"; mkdir -p "$ctx"
  cp "$repo_root/apps/frontend/scripts/serve-static.mjs" "$ctx/"; cp -r "$dist" "$ctx/public"
  docker build -q -t horus-web:test-install -f "$here/web-prebuilt.Dockerfile" "$ctx" >>"$out/images.log" 2>&1
}
build_pg() {
  docker image inspect horus-postgres:18.6-pgbackrest2.59.3 >/dev/null 2>&1 \
    || docker build -q -t horus-postgres:18.6-pgbackrest2.59.3 "$repo_root/deployments/images/postgres" >>"$out/images.log" 2>&1
}
images_ok() {
  build_horus horus:test-install-a 0.9.0 && build_horus horus:test-install-b 0.9.1 || return 1
  if ! docker image inspect horus:test-install-c >/dev/null 2>&1; then
    # C: misma imagen que B pero los procesos terminan al arrancar (imprimen la versión y salen).
    printf 'FROM horus:test-install-b\nENTRYPOINT ["/horus", "--version"]\n' \
      | docker build -q -t horus:test-install-c - >>"$out/images.log" 2>&1 || return 1
  fi
  build_web && build_pg
}
check "imágenes horus A/B/C, horus-web y horus-postgres" images_ok
[ "$fail" = 0 ] || { tail -30 "$out/images.log"; exit 1; }

bundles() {
  local v img
  for spec in "0.9.0 a" "0.9.1 b" "0.9.2 c"; do
    v="${spec% *}"; img="horus:test-install-${spec#* }"
    [ -f "$work/dist-$v/horus-$v-linux-amd64.tar.gz" ] && continue
    local extra=(); [ "$v" = 0.9.0 ] || extra=(--own-images-only)
    HORUS_IMAGE="$img" HORUS_WEB_IMAGE=horus-web:test-install HORUS_POSTGRES_IMAGE=horus-postgres:18.6-pgbackrest2.59.3 \
      HORUS_GZIP_LEVEL=1 bash "$repo_root/scripts/release/build-release.sh" --version "$v" --out "$work/dist-$v" \
      --arch amd64 --source local "${extra[@]}" >>"$out/bundles.log" 2>&1 || return 1
  done
}
check "paquetes horus-0.9.{0,1,2}-linux-amd64.tar.gz + SHA256SUMS (build-release.sh)" bundles
[ "$fail" = 0 ] || { tail -30 "$out/bundles.log"; exit 1; }

# --- 2. Contenedor Debian ------------------------------------------------------------------------
step "2. Debian $ver con systemd (TEST_MODE=$mode)"
ct_image="horus-test-debian:$ver-$mode"
if [ "$mode" = sandbox ]; then
  cache="$here/cache"
  mkdir -p "$cache/docker"
  fetch() { [ -s "$2" ] || curl -fsSL --retry 3 "$1" -o "$2"; }
  dv="$(docker version --format '{{.Server.Version}}')"
  [ -x "$cache/docker/dockerd" ] || { fetch "https://download.docker.com/linux/static/stable/x86_64/docker-$dv.tgz" "$work/docker.tgz" && tar -xzf "$work/docker.tgz" -C "$cache/docker" --strip-components=1; }
  [ -s "$cache/docker-compose" ] || cp /usr/libexec/docker/cli-plugins/docker-compose "$cache/docker-compose"
  fetch https://github.com/jqlang/jq/releases/download/jq-1.7.1/jq-linux-amd64 "$cache/jq"
  [ -s "$cache/curl" ] || { fetch https://github.com/stunnel/static-curl/releases/download/8.15.0/curl-linux-x86_64-glibc-8.15.0.tar.xz "$work/curl.tar.xz" && tar -xJf "$work/curl.tar.xz" -C "$cache" curl; }
  [ -s "$cache/wireguard-go" ] || cp "$(command -v wireguard-go)" "$cache/wireguard-go"
  if [ ! -x "$cache/iptables/xtables-nft-multi" ]; then
    mkdir -p "$cache/iptables/lib" "$cache/iptables/xtables"
    cp -L /usr/sbin/xtables-nft-multi "$cache/iptables/"
    cp -a /lib/x86_64-linux-gnu/libxtables.so.12* /lib/x86_64-linux-gnu/libmnl.so.0* /lib/x86_64-linux-gnu/libnftnl.so.11* "$cache/iptables/lib/"
    cp -a /usr/lib/x86_64-linux-gnu/xtables/. "$cache/iptables/xtables/"
  fi
  docker build -q -t "$ct_image" --build-arg "DEBIAN_VERSION=$ver" -f "$here/Dockerfile.sandbox" "$here" >"$out/ct-build.log" 2>&1 \
    || { tail -20 "$out/ct-build.log"; exit 1; }
else
  docker build -q -t "$ct_image" --build-arg "DEBIAN_VERSION=$ver" -f "$here/Dockerfile.apt" "$here" >"$out/ct-build.log" 2>&1 \
    || { tail -20 "$out/ct-build.log"; exit 1; }
fi
docker rm -f -v "$name" >/dev/null 2>&1 || true
docker volume rm -f "$vol" >/dev/null 2>&1 || true
cg=(-v /sys/fs/cgroup:/sys/fs/cgroup:rw --cgroupns=host)
docker run -d --name "$name" --hostname "$name" --privileged "${cg[@]}" \
  --tmpfs /run --tmpfs /run/lock -v "$vol:/var/lib/docker" -v "$work:/artifacts:ro" \
  -e container=docker "$ct_image" >/dev/null
for _ in $(seq 1 60); do in_ct systemctl is-system-running 2>/dev/null | grep -qE 'running|degraded' && break; sleep 1; done
check "systemd como PID 1 ($(in_ct cat /etc/debian_version))" bash -c "docker exec $name systemctl is-system-running | grep -qE 'running|degraded'"
ip="$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$name")"
in_sh "printf 'TestInstall-%s' \$(openssl rand -hex 8) >/root/admin-pw && chmod 600 /root/admin-pw"

# --- 3. Instalación -----------------------------------------------------------------------------
step "3. bootstrap-debian.sh --bundle A --mode ip (IP $ip)"
boot=(bash /artifacts/dist-0.9.0/bootstrap-debian.sh --bundle /artifacts/dist-0.9.0/horus-0.9.0-linux-amd64.tar.gz
  --yes --mode ip --public-ip "$ip" --admin-email admin@horus.test --admin-password-file /root/admin-pw
  --tunnel-cidr 10.230.0.0/16 --tlm-max-bytes 1073741824 --force)
[ "$mode" = apt ] || boot+=(--no-apt)
check "bootstrap + install.sh terminan sin error" bash -c "docker exec $name ${boot[*]@Q} >'$out/install.log' 2>&1"
[ "$mode" = sandbox ] || check "apt instaló Docker oficial, wireguard-tools, age, jq, iptables" \
  docker exec "$name" bash -c 'dpkg -s docker-ce docker-compose-plugin wireguard-tools age jq iptables >/dev/null && grep -q download.docker.com /etc/apt/sources.list.d/docker.sources'
check "sysctl net.core.rmem_max/rmem_default = 32 MiB persistente (/etc/sysctl.d/90-horus.conf)" \
  docker exec "$name" grep -q 'net.core.rmem_max = 33554432' /etc/sysctl.d/90-horus.conf
health() {
  docker exec "$name" horus-ctl status >"$out/status.log" 2>&1 || return 1
  local fp_file="$work/fp-$ver"
  docker exec "$name" cat /etc/horus/tls/public.crt >"$fp_file.crt"
  [ "$(curl -s -o /dev/null -w '%{http_code}' --cacert "$fp_file.crt" "https://$ip/")" = 200 ] || return 1
  [ "$(curl -s -o /dev/null -w '%{http_code}' --cacert "$fp_file.crt" "https://$ip/api/v1/system/status")" = 401 ]
}
check "todos los servicios sanos; UI 200 y API 401 en https://$ip con el certificado autogenerado" health
check "huella SHA-256 del certificado coincide con la mostrada" bash -c \
  "[ \"\$(openssl x509 -in '$work/fp-$ver.crt' -noout -fingerprint -sha256 | cut -d= -f2)\" = \"\$(docker exec $name cat /etc/horus/tls/fingerprint-sha256.txt)\" ]"
# --check falla también por disco ≥ 85 % del HOST (compartido en CI/sandbox): eso se tolera y se anota.
install_check() {
  docker exec "$name" horus-ctl check >"$out/check.log" 2>&1 && return 0
  if grep FALLO "$out/check.log" | grep -qv ' al [0-9]* % '; then return 1; fi
  echo "  (install.sh --check: solo fallos de ocupación del disco del host; ver check.log)"
}
check "install.sh --check (horus-ctl check; se tolera solo el disco del host lleno)" install_check
check "versión instalada 0.9.0" bash -c "[ \"\$(docker exec $name horus-ctl version)\" = 0.9.0 ]"

# --- 4. Idempotencia ----------------------------------------------------------------------------
step "4. segunda ejecución (idempotente)"
sums() { docker exec "$name" bash -c 'find /etc/horus/secrets -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum'; }
before="$(sums)"
check "bootstrap otra vez sin error" bash -c "docker exec $name ${boot[*]@Q} >'$out/install2.log' 2>&1"
check "secretos idénticos" bash -c "[ '$before' = \"\$(docker exec $name bash -c 'find /etc/horus/secrets -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum')\" ]"
check "sigue sano" health

# --- 5. Reinicio ----------------------------------------------------------------------------------
step "5. reinicio del contenedor (systemd levanta docker, horus-tunnel y horus)"
docker restart -t 60 "$name" >/dev/null
ip="$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$name")"
reboot_ok() {
  for _ in $(seq 1 90); do
    in_ct systemctl is-active horus.service 2>/dev/null | grep -qx active && health && return 0
    sleep 5
  done
  return 1
}
check "tras reiniciar: horus.service activo y todo sano" reboot_ok

# --- 6. Actualización y vuelta atrás --------------------------------------------------------------
step "6. horus-ctl upgrade A→B y B→C (rota)"
check "upgrade --bundle 0.9.1 (backup previo, SHA256SUMS, healthcheck)" bash -c \
  "docker exec $name horus-ctl upgrade --bundle /artifacts/dist-0.9.1/horus-0.9.1-linux-amd64.tar.gz --yes --force >'$out/upgrade-b.log' 2>&1"
check "versión 0.9.1 y sana" bash -c "[ \"\$(docker exec $name horus-ctl version)\" = 0.9.1 ]"
check "horus-app corre la imagen de 0.9.1 (horus:test-install-b)" bash -c "[ \"\$(docker exec $name docker inspect -f '{{.Config.Image}}' horus-horus-app-1)\" = horus:test-install-b ]"
check "sana tras actualizar" health
set +e
docker exec "$name" horus-ctl upgrade --bundle /artifacts/dist-0.9.2/horus-0.9.2-linux-amd64.tar.gz --yes --force >"$out/upgrade-c.log" 2>&1
rc=$?
set -e
check "upgrade a 0.9.2 rota falla con código 3 (vuelta atrás)" test "$rc" = 3
check "vuelta atrás: versión 0.9.1" bash -c "[ \"\$(docker exec $name horus-ctl version)\" = 0.9.1 ]"
check "vuelta atrás: horus-app de nuevo con la imagen de 0.9.1" bash -c "[ \"\$(docker exec $name docker inspect -f '{{.Config.Image}}' horus-horus-app-1)\" = horus:test-install-b ]"
# Restauración en el sitio de las copias (la ruta que usa la vuelta atrás cuando el esquema cambia).
check "horus-ctl backup run + restore --yes (PostgreSQL y ClickHouse en el sitio) y sana" bash -c \
  "docker exec $name horus-ctl backup run >'$out/backup-restore.log' 2>&1 && docker exec $name horus-ctl restore --yes >>'$out/backup-restore.log' 2>&1"
check "sana tras restaurar" health
check "vuelta atrás: todo sano" health

# --- 7. Desinstalación ----------------------------------------------------------------------------
step "7. uninstall (conserva datos), reinstalación y uninstall --purge"
check "horus-ctl uninstall --yes" bash -c "docker exec $name horus-ctl uninstall --yes >'$out/uninstall.log' 2>&1"
check "sin contenedores y datos conservados" bash -c \
  "[ -z \"\$(docker exec $name docker ps -q)\" ] && docker exec $name test -s /etc/horus/secrets/postgres_password && docker exec $name test -d /var/lib/horus/postgres"
check "reinstalación con los mismos secretos" bash -c \
  "docker exec $name ${boot[*]@Q} >'$out/install3.log' 2>&1 && [ '$before' = \"\$(docker exec $name bash -c 'find /etc/horus/secrets -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum')\" ]"
check "sana tras reinstalar" health
check "horus-ctl uninstall --purge --yes" bash -c "docker exec $name horus-ctl uninstall --purge --yes >'$out/purge.log' 2>&1"
check "purge: sin /etc/horus, /var/lib/horus ni /opt/horus" bash -c \
  "! docker exec $name test -e /etc/horus && ! docker exec $name test -e /var/lib/horus && ! docker exec $name test -e /opt/horus"

# --- 8. Detrás de un proxy inverso (--tls external) -------------------------------------------------
if [ "${TEST_PROXY:-1}" = 1 ]; then
  step "8. --tls external detrás de nginx con TLS autofirmado"
  nginx_img="${TEST_NGINX_IMAGE:-nginx:1.27.5-alpine}"
  docker image inspect "$nginx_img" >/dev/null 2>&1 || docker pull -q "$nginx_img" >/dev/null
  proxy_setup() {
    docker save "$nginx_img" | docker exec -i "$name" docker load -q >/dev/null || return 1
    docker exec "$name" mkdir -p /etc/horus-test-nginx/tls
    docker cp "$here/nginx-proxy.conf" "$name:/etc/horus-test-nginx/default.conf"
    docker exec "$name" openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 30 \
      -subj "/CN=$ip" -addext "subjectAltName=IP:$ip" -keyout /etc/horus-test-nginx/tls/proxy.key \
      -out /etc/horus-test-nginx/tls/proxy.crt 2>/dev/null || return 1
    docker exec "$name" docker run -d --name horus-test-nginx --network host --restart unless-stopped \
      -v /etc/horus-test-nginx/default.conf:/etc/nginx/conf.d/default.conf:ro \
      -v /etc/horus-test-nginx/tls:/etc/nginx/tls:ro "$nginx_img" >/dev/null
  }
  ext=(bash /artifacts/dist-0.9.0/bootstrap-debian.sh --bundle /artifacts/dist-0.9.0/horus-0.9.0-linux-amd64.tar.gz
    --yes --tls external --public-url "https://$ip" --trusted-proxies 127.0.0.1/32 --http-bind 127.0.0.1:8080
    --admin-email admin@horus.test --admin-password-file /root/admin-pw --tunnel-cidr 10.230.0.0/16
    --tlm-max-bytes 1073741824 --force)
  [ "$mode" = apt ] || ext+=(--no-apt)
  check "instalación --tls external (Traefik solo HTTP en 127.0.0.1:8080)" bash -c "docker exec $name ${ext[*]@Q} >'$out/install-proxy.log' 2>&1"
  check "nginx de prueba con TLS autofirmado delante (proxy_pass a 127.0.0.1:8080)" proxy_setup
  docker exec "$name" cat /etc/horus-test-nginx/tls/proxy.crt >"$work/proxy-$ver.crt"
  pc="$work/proxy-$ver.crt"
  proxy_http() { # proxy_http <ruta> <código esperado> [curl…]
    local p="$1" want="$2"; shift 2
    local got; got="$(curl -s -o /dev/null -w '%{http_code}' --cacert "$pc" "$@" "https://$ip$p")"
    [ "$got" = "$want" ] || { echo "GET $p = $got (quiero $want)"; return 1; }
  }
  proxy_ok() {
    for _ in $(seq 1 30); do proxy_http / 200 && proxy_http /api/v1/system/status 401 && return 0; sleep 2; done
    return 1
  }
  check "por el proxy: UI 200 y API 401" proxy_ok
  check "sin HSTS de Horus (solo el que pone el proxy) y CSP de la SPA" bash -c \
    "h=\$(curl -sI --cacert '$pc' https://$ip/); echo \"\$h\" | grep -qi '^content-security-policy:' && [ \"\$(echo \"\$h\" | grep -ci '^strict-transport-security:')\" = 1 ]"
  check "Traefik HTTP no escucha fuera de 127.0.0.1" bash -c "! curl -s -m 5 -o /dev/null http://$ip:8080/"
  # Login, refresco con la cookie __Secure-…, cambio de contraseña y TOTP, ISP, sitios y routers
  # (e2e de humo de I0) A TRAVÉS del proxy.
  docker exec "$name" cat /root/admin-pw >"$work/admin-pw-$ver"
  check "e2e de humo (login, cookie Secure de refresco, TOTP, ISP, sitios, routers) a través del proxy" env \
    SSL_CERT_FILE="$pc" ACCEPT_BASE_URL="https://$ip" ACCEPT_ADMIN_EMAIL=admin@horus.test \
    ACCEPT_ADMIN_PASSWORD_FILE="$work/admin-pw-$ver" "${GO:-go}" test -count=1 -tags acceptance "$repo_root/tests/acceptance/i0/"
  # WebSocket: el Upgrade llega a horus-app (401/403 por el ticket falso, no 400/502 del proxy).
  ws_ok() {
    local c; c="$(curl -s -o /dev/null -w '%{http_code}' --cacert "$pc" --http1.1 -H 'Connection: Upgrade' -H 'Upgrade: websocket' \
      -H 'Sec-WebSocket-Version: 13' -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' -H "Origin: https://$ip" \
      "https://$ip/api/v1/ws?ticket=falso")"
    case "$c" in 401 | 403) return 0 ;; *) echo "ws = $c"; return 1 ;; esac
  }
  check "WebSocket por el proxy: el Upgrade llega a horus-app (ticket falso → 401/403)" ws_ok
  # Rate limit por IP real: 20 logins por minuto; un X-Forwarded-For falsificado (y distinto cada
  # vez) desde una IP no confiable no lo esquiva.
  spoof_ok() {
    local i c=""
    for i in $(seq 1 22); do
      c="$(curl -s -o /dev/null -w '%{http_code}' --cacert "$pc" -H 'Content-Type: application/json' -H 'X-Requested-With: horus' \
        -H "X-Forwarded-For: 10.66.$i.$i" -d '{"email":"nadie@horus.test","password":"incorrecta-123"}' "https://$ip/api/v1/auth/login")"
      [ "$c" != 429 ] || break
    done
    [ "$c" = 429 ] || { echo "sin 429 tras 22 logins con XFF falsificado (último $c)"; return 1; }
  }
  check "X-Forwarded-For falsificado desde IP no confiable se ignora (rate limit por IP real → 429)" spoof_ok
  real_ip_ok() {
    local gw ips
    gw="$(docker network inspect bridge -f '{{(index .IPAM.Config 0).Gateway}}')"
    ips="$(docker exec "$name" docker compose --project-directory /opt/horus -f /opt/horus/compose.yaml --env-file /opt/horus/.env \
      exec -T postgres psql -U horus -d horus -XAtc "SELECT DISTINCT host(ip) FROM auth.session WHERE ip IS NOT NULL UNION SELECT DISTINCT host(ip) FROM auth.audit_log WHERE ip IS NOT NULL")"
    echo "IPs registradas: $ips (cliente real: $gw)"
    echo "$ips" | grep -qx "$gw" && ! echo "$ips" | grep -qE '^(127\.|172\.31\.250\.)'
  }
  check "sesiones y auditoría con la IP real del cliente (no la del proxy ni la de Traefik)" real_ip_ok
  check "uninstall --purge del modo proxy" bash -c "docker exec $name horus-ctl uninstall --purge --yes >'$out/purge-proxy.log' 2>&1"
  docker exec "$name" docker rm -f horus-test-nginx >/dev/null 2>&1 || true
fi

printf '\n\033[1mtest-install-debian %s (%s):\033[0m %d OK, %d FAIL (bin/test-install-debian/debian%s/)\n' "$ver" "$mode" "$pass" "$fail" "$ver"
[ "$fail" = 0 ]
