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
    # C: misma imagen que B pero el proceso no arranca (healthcheck nunca sano).
    printf 'FROM horus:test-install-b\nENTRYPOINT ["/horus", "no-existe-este-subcomando"]\n' \
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
check "install.sh --check (horus-ctl check)" docker exec "$name" horus-ctl check
check "versión instalada 0.9.0" bash -c "[ \"\$(docker exec $name horus-ctl version)\" = 0.9.0 ]"

# --- 4. Idempotencia ----------------------------------------------------------------------------
step "4. segunda ejecución (idempotente)"
sums() { docker exec "$name" bash -c 'sha256sum /etc/horus/secrets/* /etc/horus/secrets/grpc/* | sha256sum'; }
before="$(sums)"
check "bootstrap otra vez sin error" bash -c "docker exec $name ${boot[*]@Q} >'$out/install2.log' 2>&1"
check "secretos idénticos" bash -c "[ '$before' = \"\$(docker exec $name bash -c 'sha256sum /etc/horus/secrets/* /etc/horus/secrets/grpc/* | sha256sum')\" ]"
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
  "docker exec $name horus-ctl upgrade --bundle /artifacts/dist-0.9.1/horus-0.9.1-linux-amd64.tar.gz --yes >'$out/upgrade-b.log' 2>&1"
check "versión 0.9.1 y sana" bash -c "[ \"\$(docker exec $name horus-ctl version)\" = 0.9.1 ]"
check "sana tras actualizar" health
set +e
docker exec "$name" horus-ctl upgrade --bundle /artifacts/dist-0.9.2/horus-0.9.2-linux-amd64.tar.gz --yes >"$out/upgrade-c.log" 2>&1
rc=$?
set -e
check "upgrade a 0.9.2 rota falla con código 3 (vuelta atrás)" test "$rc" = 3
check "vuelta atrás: versión 0.9.1" bash -c "[ \"\$(docker exec $name horus-ctl version)\" = 0.9.1 ]"
check "vuelta atrás: todo sano" health

# --- 7. Desinstalación ----------------------------------------------------------------------------
step "7. uninstall (conserva datos), reinstalación y uninstall --purge"
check "horus-ctl uninstall --yes" bash -c "docker exec $name horus-ctl uninstall --yes >'$out/uninstall.log' 2>&1"
check "sin contenedores y datos conservados" bash -c \
  "[ -z \"\$(docker exec $name docker ps -q)\" ] && docker exec $name test -s /etc/horus/secrets/postgres_password && docker exec $name test -d /var/lib/horus/postgres"
check "reinstalación con los mismos secretos" bash -c \
  "docker exec $name ${boot[*]@Q} >'$out/install3.log' 2>&1 && [ '$before' = \"\$(docker exec $name bash -c 'sha256sum /etc/horus/secrets/* /etc/horus/secrets/grpc/* | sha256sum')\" ]"
check "sana tras reinstalar" health
check "horus-ctl uninstall --purge --yes" bash -c "docker exec $name horus-ctl uninstall --purge --yes >'$out/purge.log' 2>&1"
check "purge: sin /etc/horus, /var/lib/horus ni /opt/horus" bash -c \
  "! docker exec $name test -e /etc/horus && ! docker exec $name test -e /var/lib/horus && ! docker exec $name test -e /opt/horus"

printf '\n\033[1mtest-install-debian %s (%s):\033[0m %d OK, %d FAIL (bin/test-install-debian/debian%s/)\n' "$ver" "$mode" "$pass" "$fail" "$ver"
[ "$fail" = 0 ]
