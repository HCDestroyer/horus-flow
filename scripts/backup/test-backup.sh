#!/usr/bin/env bash
# test-backup.sh — `make test-backup` (historia I1-23, "Hecho cuando").
#
# Prueba de punta a punta sobre una instalación REAL hecha con scripts/install.sh en un
# directorio raíz temporal (no toca /opt, /etc ni /var del host; sí crea una interfaz WireGuard
# de prueba y reglas iptables etiquetadas, que se quitan al final):
#
#   1. install.sh --root <tmp> --mode ip (puertos y proyecto propios).
#   2. Datos: el e2e de humo de I0 (tests/acceptance/i0) contra la API real por HTTPS con el
#      certificado autogenerado: 2 ISP, nodos, routers, prefijos, usuarios y TOTP.
#   3. horus-backup run all (pgBackRest full + BACKUP de ClickHouse) y un segundo run
#      (diferencial / incremental).
#   4. horus-backup verify all: restauración en bases VACÍAS y efímeras; PostgreSQL debe
#      coincidir tabla a tabla (filas y md5) con el origen — ISP, nodos, routers, peers,
#      clientes y hallazgos incluidos — y ClickHouse con las filas medidas.
#   5. check-ttl (flows_raw = 7 d), métricas horus_backup_* servidas en 127.0.0.1, alerta de
#      disco forzando el umbral, y que un backup roto alerta y deja la métrica a 0.
#   6. install.sh --uninstall --purge.
#
# Variables: TEST_BACKUP_IMAGE (imagen horus ya construida; si no, scripts/accept/image.sh),
# TEST_BACKUP_KEEP=1 (no desinstala), TEST_BACKUP_PORT_OFFSET (30000), GO.
# Logs: bin/test-backup/.

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$repo_root"
out="$repo_root/bin/test-backup"
mkdir -p "$out"
root="$(mktemp -d /tmp/horus-test-backup.XXXXXX)"
offset="${TEST_BACKUP_PORT_OFFSET:-30000}"
https_port=$((offset + 443)); http_port=$((offset + 80)); wg_port=$((51820 + offset / 1000))
metrics_port=$((offset + 9109))
project=horus-testbackup
GO="${GO:-go}"
image="${TEST_BACKUP_IMAGE:-}"
pass=0 fail=0

step() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
check() { # check <descripción> <orden…>
  local d="$1"; shift
  if "$@" >>"$out/checks.log" 2>&1; then printf '  \033[32mOK\033[0m   %s\n' "$d"; pass=$((pass + 1))
  else printf '  \033[31mFAIL\033[0m %s (bin/test-backup/checks.log)\n' "$d"; fail=$((fail + 1)); fi
}

cleanup() {
  if [ "${TEST_BACKUP_KEEP:-0}" = 1 ]; then
    echo "test-backup: instalación conservada en $root (TEST_BACKUP_KEEP=1)"
    return
  fi
  bash scripts/install.sh --uninstall --purge --yes --root "$root" >"$out/uninstall.log" 2>&1 || true
  rm -rf "$root"
}
trap cleanup EXIT

if [ -z "$image" ]; then
  step "imagen horus"
  image=horus:test-backup
  ACCEPT_IMAGE="$image" ACCEPT_IMAGE_MODE="${ACCEPT_IMAGE_MODE:-auto}" GO="$GO" bash scripts/accept/image.sh >"$out/image.log" 2>&1 \
    || { tail -20 "$out/image.log"; exit 1; }
fi

step "1. instalación en $root"
pw="$root/admin-password"
printf 'TestBackup-%s' "$(openssl rand -hex 8)" >"$pw"
if ! bash scripts/install.sh --yes --force --root "$root" --mode ip --public-ip 127.0.0.1 \
  --admin-email admin@horus.test --admin-password-file "$pw" --tunnel-cidr 10.232.0.0/16 \
  --image "$image" --project "$project" --http-port "$http_port" --https-port "$https_port" \
  --wg-port "$wg_port" --wg-interface hfwgtestbk --docker-subnet 172.31.252.0/24 \
  --tlm-max-bytes 1073741824 --backup-metrics-port "$metrics_port" >"$out/install.log" 2>&1; then
  tail -40 "$out/install.log"; exit 1
fi
hb="$root/opt/horus/bin/horus-backup"
check "install.sh --check" bash scripts/install.sh --check --root "$root"

step "2. datos de prueba por la API real (e2e de humo de I0, HTTPS con certificado autogenerado)"
check "e2e: 2 ISP, nodos, routers, prefijos, usuarios y TOTP" env SSL_CERT_FILE="$root/etc/horus/tls/public.crt" \
  ACCEPT_BASE_URL="https://127.0.0.1:$https_port" ACCEPT_ADMIN_EMAIL=admin@horus.test ACCEPT_ADMIN_PASSWORD_FILE="$pw" \
  "$GO" test -count=1 -tags acceptance ./tests/acceptance/i0/
printf "INSERT INTO dim.tenant (tenant_id, name, timezone, version) VALUES (generateUUIDv4(), 'isp-backup-test', 'UTC', 1)\n" | "$hb" ch-sql >>"$out/checks.log" 2>&1

step "3. backups"
check "horus-backup run all (full)" "$hb" run all
check "horus-backup run all (diferencial / incremental)" "$hb" run all
check "repositorio pgBackRest con full y diff" bash -c "docker compose --project-directory '$root/opt/horus' -f '$root/opt/horus/compose.yaml' --env-file '$root/opt/horus/.env' exec -T -u postgres postgres pgbackrest --config=/run/secrets/pgbackrest_conf --stanza=horus info | grep -q 'diff backup'"
check "ClickHouse: un completo y un incremental" bash -c "ls '$root/var/lib/horus/store/backups/clickhouse' | grep -q '^full_' && ls '$root/var/lib/horus/store/backups/clickhouse' | grep -q '^incr_'"

step "4. restauración de prueba en bases vacías"
"$hb" verify all >"$out/verify.log" 2>&1 && verify_rc=0 || verify_rc=$?
check "horus-backup verify all" test "$verify_rc" = 0
check "PostgreSQL restaurado: ISP (auth.tenant ≥ 2), nodos y routers presentes e idénticos" \
  bash -c "grep -Eq 'auth\.tenant +([2-9]|[1-9][0-9]+) filas' '$out/verify.log' && grep -Eq 'devices\.site +[1-9]' '$out/verify.log' && grep -Eq 'devices\.router +[1-9]' '$out/verify.log' && grep -q 'tablas idénticas' '$out/verify.log'"
check "ClickHouse restaurado con las filas esperadas" grep -q 'ClickHouse: restauración OK' "$out/verify.log"

step "5. TTL, métricas y alertas"
check "TTL de flows.flows_raw = 7 días" "$hb" check-ttl
check "métricas horus_backup_* en 127.0.0.1:$metrics_port" bash -c \
  "curl -fsS http://127.0.0.1:$metrics_port/metrics.prom | grep -q 'horus_backup_last_status{component=\"postgres\"} 1' && curl -fsS http://127.0.0.1:$metrics_port/metrics.prom | grep -q 'horus_backup_restore_test_last_status{component=\"clickhouse\"} 1'"
check "disco por encima del umbral → alerta y métrica" bash -c \
  "HORUS_STORE_ALERT_RATIO=0.01 '$hb' metrics; grep -q 'disco store' '$root/var/lib/horus/store/backups/state/alerts.log' && grep -q 'horus_store_disk_used_ratio{volume=\"store\"} 0\\.[0-9]' '$root/var/lib/horus/metrics/metrics.prom'"
docker compose --project-directory "$root/opt/horus" -f "$root/opt/horus/compose.yaml" --env-file "$root/opt/horus/.env" stop clickhouse >>"$out/checks.log" 2>&1
check "backup con ClickHouse caído → falla, alerta y horus_backup_last_status 0" bash -c \
  "! '$hb' ch && grep -q 'backup de clickhouse FALLIDO' '$root/var/lib/horus/store/backups/state/alerts.log' && grep -q 'horus_backup_last_status{component=\"clickhouse\"} 0' '$root/var/lib/horus/metrics/metrics.prom'"
docker compose --project-directory "$root/opt/horus" -f "$root/opt/horus/compose.yaml" --env-file "$root/opt/horus/.env" start clickhouse >>"$out/checks.log" 2>&1

echo
echo "test-backup: $pass OK, $fail FAIL (logs en bin/test-backup/)"
[ "$fail" = 0 ]
