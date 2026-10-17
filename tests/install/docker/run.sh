#!/usr/bin/env bash
# run.sh — `make test-install-docker`: instalación SOLO con Docker de punta a punta en este anfitrión
# (imagen horus-installer, docs/install-docker.md), modo ip con certificado autogenerado.
#
#   sudo INSTALLER_IMAGE=horus-installer:local HORUS_IMAGE=horus:local HORUS_WEB_IMAGE=horus-web:local \
#     bash tests/install/docker/run.sh
#
# Pasos (OK/FAIL en el resumen; logs en bin/test-install-docker/):
#   1. docker run … horus-installer install --yes --mode ip (imágenes locales): todo healthy.
#   2. horus-installer check; horus-host healthy con wg0, regla DOCKER-USER y crontab de backups;
#      el UDP de IPFIX/NetFlow publicado SOLO en la IP del hub (nunca 0.0.0.0).
#   3. Segunda ejecución: idempotente, secretos idénticos.
#   4. HTTPS: API 401 y la huella del certificado servido = fingerprint-sha256.txt.
#   5. "Reinicio" de horus-host: vuelve a crear wg0 (wireguard-go si no hay módulo) y reinicia el
#      colector y wg-agent; todo vuelve a healthy.
#   6. horus-installer backup run all / verify (restauración de prueba) / status; ctl status.
#   7. horus-installer uninstall --purge --yes: sin contenedores, sin wg0, sin reglas, directorios vacíos.
#
# Variables: INSTALLER_IMAGE (horus-installer:local), HORUS_IMAGE (horus:local), HORUS_WEB_IMAGE
# (horus-web:local), HORUS_POSTGRES_IMAGE (vacía = la del instalador), TEST_ROOT (vacío = rutas por
# defecto /opt/horus, /etc/horus, /var/lib/horus; si no, <TEST_ROOT>/{install,etc,data}), STORE_DIR
# (/mnt/horus-store o <TEST_ROOT>/store), TUNNEL_CIDR (10.200.0.0/16), TEST_KEEP=1 (no desinstala),
# EXTRA_INSTALL_ARGS (p. ej. --force en una máquina pequeña).
set -euo pipefail

repo_root="$(cd "$(dirname "$(readlink -f "$0")")/../../.." && pwd)"
out="$repo_root/bin/test-install-docker"
installer="${INSTALLER_IMAGE:-horus-installer:local}"
horus_image="${HORUS_IMAGE:-horus:local}"
web_image="${HORUS_WEB_IMAGE:-horus-web:local}"
tunnel_cidr="${TUNNEL_CIDR:-10.200.0.0/16}"
if [ -n "${TEST_ROOT:-}" ]; then
  opt="$TEST_ROOT/install" etc="$TEST_ROOT/etc" data="$TEST_ROOT/data" store="${STORE_DIR:-$TEST_ROOT/store}"
else
  opt=/opt/horus etc=/etc/horus data=/var/lib/horus store="${STORE_DIR:-/mnt/horus-store}"
fi
project=horus wg_if=wg0
pass=0 fail=0
mkdir -p "$out"
: >"$out/summary.txt"; : >"$out/checks.log"
in_dir="$(mktemp -d)"
printf '%s' "Docker-$(openssl rand -hex 12)" >"$in_dir/admin-password"
chmod 0644 "$in_dir/admin-password"

step() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
result() {
  if [ "$1" = OK ]; then printf '  \033[32mOK\033[0m   %s\n' "$2"; pass=$((pass + 1))
  else printf '  \033[31mFAIL\033[0m %s\n' "$2"; fail=$((fail + 1)); fi
  printf '%s %s\n' "$1" "$2" >>"$out/summary.txt"
}
check() { local d="$1"; shift; if "$@" >>"$out/checks.log" 2>&1; then result OK "$d"; else result FAIL "$d"; fi; }

# horus-installer ORDEN …: el mismo docker run que documenta docs/install-docker.md.
hi() {
  docker run --rm -i --network host \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -v "$opt:$opt" -v "$etc:$etc" -v "$data:$data" -v "$store:$store" -v "$in_dir:/in:ro" \
    -e "HORUS_OPT_ETC_DIR=$etc" "$installer" "$@"
}
host_cid() { docker ps -q --filter "label=com.docker.compose.project=$project" --filter "label=com.docker.compose.service=horus-host"; }
svc_cid() { docker ps -aq --filter "label=com.docker.compose.project=$project" --filter "label=com.docker.compose.service=$1"; }
# iptables del anfitrión con la imagen del instalador (mismo backend que horus-host).
host_ipt() { docker run --rm --network host --cap-add NET_ADMIN --entrypoint iptables "$installer" "$@"; }
secrets_hash() { find "$etc/secrets" -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum; }
wait_healthy() { # wait_healthy <cid> <segundos>
  local i
  for ((i = 0; i < $2; i++)); do
    [ "$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$1" 2>/dev/null)" = healthy ] && return 0
    sleep 1
  done
  return 1
}

collect_logs() {
  docker ps -a >"$out/docker-ps.txt" 2>&1 || true
  [ ! -f "$opt/compose.yaml" ] || docker compose --project-directory "$opt" -f "$opt/compose.yaml" --env-file "$opt/.env" \
    logs --no-color --tail 200 >"$out/compose-logs.txt" 2>&1 || true
}
cleanup() {
  local rc=$?
  collect_logs
  if [ "${TEST_KEEP:-0}" = 1 ]; then echo "TEST_KEEP=1: la instalación queda en marcha"; else
    [ ! -f "$etc/install.conf" ] || hi uninstall --purge --yes >>"$out/uninstall-final.log" 2>&1 || true
  fi
  rm -rf "$in_dir"
  exit "$rc"
}
trap cleanup EXIT

for i in "$installer" "$horus_image" "$web_image"; do
  docker image inspect "$i" >/dev/null 2>&1 || { echo "falta la imagen $i" >&2; exit 2; }
done
install_args=(install --yes --mode ip --public-ip 127.0.0.1 --admin-email admin@horus.test
  --admin-password-file /in/admin-password --tunnel-cidr "$tunnel_cidr" --image-source local
  --image "$horus_image" --web-image "$web_image" --install-dir "$opt" --data-dir "$data" --store-dir "$store")
[ -z "${HORUS_POSTGRES_IMAGE:-}" ] || install_args+=(--postgres-image "$HORUS_POSTGRES_IMAGE")
# shellcheck disable=SC2206 # opciones extra separadas por espacios, a propósito
[ -z "${EXTRA_INSTALL_ARGS:-}" ] || install_args+=($EXTRA_INSTALL_ARGS)

# Pasos con varias órdenes (check los ejecuta y guarda la salida en checks.log).
do_install() { hi "${install_args[@]}" 2>&1 | tee "$out/$1"; return "${PIPESTATUS[0]}"; }
wg_has_hub_ip() { ip -4 -o addr show "$wg_if" | grep -q "$(sed -n 's/^HORUS_COLLECTOR_IP=//p' "$opt/.env")/24"; }
crontab_loaded() { docker exec "$hc" grep -q 'horus-backup run all' "$opt/config/horus-host.crontab" && docker exec "$hc" pgrep -x supercronic; }
collector_ports_private() {
  local p; p="$(docker port "$(svc_cid horus-collector)")"; printf '%s\n' "$p"
  grep -q '4739/udp' <<<"$p" && ! grep -Eq '0\.0\.0\.0|\[::\]' <<<"$p"
}
api_401() { [ "$(curl -sk -o /dev/null -w '%{http_code}' https://127.0.0.1/api/v1/system/status)" = 401 ]; }
started_at() { docker inspect -f '{{.State.StartedAt}}' "$(svc_cid "$1")"; }
no_project_containers() { [ -z "$(docker ps -aq --filter "label=com.docker.compose.project=$project")" ]; }
no_ipfix_rules() { ! host_ipt -S DOCKER-USER | grep -q horus-ipfix-only-wg; }
dirs_empty() { local d; for d in "$opt" "$etc" "$data" "$store"; do [ ! -e "$d" ] || [ -z "$(ls -A "$d")" ] || return 1; done; }
not_cmd() { ! "$@"; }

step "1. Instalación con docker run … horus-installer install"
check "install (docker run, modo ip)" do_install install.log
grep -E 'huella|SECRETOS|frase|Superadmin|contraseña' "$out/install.log" | sed 's/^/    /' || true

step "2. check, horus-host y aislamiento del colector"
check "horus-installer check" hi check
hc="$(host_cid)"
check "horus-host healthy" wait_healthy "$hc" 60
check "wg0 en el anfitrión con la IP del hub" wg_has_hub_ip
check "regla DOCKER-USER: IPFIX/NetFlow solo por $wg_if" host_ipt -C DOCKER-USER ! -i "$wg_if" -p udp -m multiport --dports 4739,2055 -m comment --comment horus-ipfix-only-wg -j DROP
check "regla INPUT: IPFIX/NetFlow solo por $wg_if" host_ipt -C INPUT ! -i "$wg_if" -p udp -m multiport --dports 4739,2055 -m comment --comment horus-ipfix-only-wg -j DROP
check "crontab de backups cargado en horus-host" crontab_loaded
check "UDP 4739/2055 publicados solo en la IP del hub (no 0.0.0.0)" collector_ports_private

step "3. Segunda ejecución (idempotente)"
before="$(secrets_hash)"
check "segunda ejecución" do_install install2.log
check "los secretos no cambian" test "$before" = "$(secrets_hash)"

step "4. HTTPS con el certificado autogenerado"
check "API vía HTTPS responde 401" api_401
served="$(openssl s_client -connect 127.0.0.1:443 </dev/null 2>/dev/null | openssl x509 -noout -fingerprint -sha256 | cut -d= -f2)"
check "huella servida = fingerprint-sha256.txt ($served)" test "$served" = "$(cat "$etc/tls/fingerprint-sha256.txt")"

step "5. Reinicio de horus-host (wg0 de nuevo y reenganche del colector)"
col_before="$(started_at horus-collector)"
docker restart -t 20 "$hc" >/dev/null
check "horus-host healthy tras reiniciar" wait_healthy "$hc" 90
check "wg0 presente" ip link show "$wg_if"
if [ -d /sys/module/wireguard ]; then
  result OK "wg0 del kernel: sobrevive al contenedor (no hace falta reiniciar el colector)"
else
  sleep 5
  check "colector reiniciado tras recrear wg0 (wireguard-go)" not_cmd test "$(started_at horus-collector)" = "$col_before"
fi
check "colector healthy" wait_healthy "$(svc_cid horus-collector)" 90
check "wg-agent healthy" wait_healthy "$(svc_cid horus-wg-agent)" 90

step "6. Backups y gestión"
check "backup run all" hi backup run all
check "backup verify (restauración de prueba)" hi backup verify
check "backup status" hi backup status
check "ctl status" hi ctl status
check "ctl upgrade rechazado (se actualiza con la imagen nueva)" not_cmd hi ctl upgrade

if [ "${TEST_KEEP:-0}" != 1 ]; then
  step "7. Desinstalación --purge"
  check "uninstall --purge --yes" hi uninstall --purge --yes
  check "sin contenedores del proyecto" no_project_containers
  check "sin wg0" not_cmd ip link show "$wg_if"
  check "sin reglas horus-ipfix-only-wg" no_ipfix_rules
  check "directorios vacíos" dirs_empty
fi

echo
echo "test-install-docker: $pass OK, $fail FAIL (bin/test-install-docker/summary.txt)"
[ "$fail" = 0 ]
