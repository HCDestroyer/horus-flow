#!/usr/bin/env bash
# accept-i0.sh — batería de aceptación del incremento 0 (`make accept-i0`, historia I0-19).
#
# Ejecuta, en orden, cada paso con su propio log y termina con un resumen OK/FAIL/SKIP por paso
# (qué historia lo cubre y dónde está la salida). Sale con 1 si algún paso falla.
#
#   go-build → go-test (-race) → lint → contracts → sim-verify → image → compose-up (perfil
#   app) → healthy → migrations → e2e-api → go-integration → frontend-build → frontend-e2e →
#   lab-chr
#
# El compose de aceptación es un proyecto APARTE (ACCEPT_PROJECT=horus-accept) con volúmenes
# nuevos y puertos desplazados (ACCEPT_PORT_OFFSET=20000): no toca los datos ni los puertos del
# `make up` de desarrollo. Se destruye al terminar salvo ACCEPT_KEEP=1.
#
# Variables:
#   ACCEPT_STEPS=a,b       ejecuta solo esos pasos (el resto, SKIP)
#   ACCEPT_SKIP=a,b        omite esos pasos
#   ACCEPT_KEEP=1          deja el compose de aceptación levantado al terminar
#   ACCEPT_PROJECT         nombre del proyecto compose (horus-accept)
#   ACCEPT_PORT_OFFSET     desplazamiento de los puertos publicados (20000)
#   ACCEPT_IMAGE           imagen horus a construir y usar (horus:accept)
#   ACCEPT_IMAGE_MODE      auto|dockerfile|prebuilt (scripts/accept/image.sh)
#   ACCEPT_WAIT_TIMEOUT    segundos para que el compose quede healthy (240)
#   ACCEPT_UI_PORT         puerto del servidor estático de Playwright (ACCEPT_PORT_OFFSET+4173)
#   GOLANGCI_LINT, BUF_BIN, PLAYWRIGHT_BROWSERS_PATH, GO   herramientas (como en el Makefile)
#
# Logs: bin/accept-i0/<paso>.log (y compose-logs.txt si falla algo con el compose levantado).

set -uo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$repo_root"

out_dir="$repo_root/bin/accept-i0"
rm -rf "$out_dir"/*.log "$out_dir"/notes "$out_dir"/compose-logs.txt 2>/dev/null || true
mkdir -p "$out_dir/notes"

GO="${GO:-go}"
MAKE="${MAKE:-make}"
export GOTOOLCHAIN="${GOTOOLCHAIN:-local}"

# --- Compose de aceptación ---------------------------------------------------------------------
compose_dir="$repo_root/deployments/compose"
export COMPOSE_PROJECT_NAME="${ACCEPT_PROJECT:-horus-accept}"
export COMPOSE_PROFILES=app
export HORUS_IMAGE="${ACCEPT_IMAGE:-horus:accept}"
export ACCEPT_IMAGE="$HORUS_IMAGE"
offset="${ACCEPT_PORT_OFFSET:-20000}"
port_vars=(HORUS_PG_PORT:5432 HORUS_CH_HTTP_PORT:8123 HORUS_CH_NATIVE_PORT:9000 HORUS_NATS_PORT:4222
  HORUS_NATS_MONITOR_PORT:8222 HORUS_VALKEY_PORT:6379 HORUS_HTTP_PORT:8000 HORUS_TRAEFIK_ADMIN_PORT:8082
  HORUS_COLLECTOR_IPFIX_PORT:4739 HORUS_COLLECTOR_V9_PORT:2055 HORUS_APP_ADMIN_PORT:8081
  HORUS_PROMETHEUS_PORT:9091 HORUS_GRAFANA_PORT:3001)
for pv in "${port_vars[@]}"; do
  export "${pv%%:*}=$((${pv##*:} + offset))"
done
COMPOSE="docker compose --project-directory $compose_dir -f $compose_dir/compose.dev.yaml -f $compose_dir/compose.observability.yaml"
export COMPOSE
read -r -a compose <<<"$COMPOSE"
api_url="http://127.0.0.1:${HORUS_HTTP_PORT}"
admin_url="http://127.0.0.1:${HORUS_APP_ADMIN_PORT}"
wait_timeout="${ACCEPT_WAIT_TIMEOUT:-240}"
compose_started=0

# Valor de una variable: entorno, si no .env, si no .env.example (sin imprimir secretos).
env_value() {
  local key="$1" f v
  if [ -n "${!key:-}" ]; then printf '%s' "${!key}"; return; fi
  for f in "$compose_dir/.env" "$compose_dir/.env.example"; do
    [ -f "$f" ] || continue
    v="$(sed -nE "s/^[[:space:]]*(export[[:space:]]+)?${key}=(.*)$/\2/p" "$f" | tail -1)"
    v="${v%\"}"; v="${v#\"}"
    if [ -n "$v" ]; then printf '%s' "$v"; return; fi
  done
}

# --- Registro de pasos -------------------------------------------------------------------------
declare -a S_ID S_STORY S_DESC S_RES S_TIME S_NOTE
failed=0

contains() { case ",$1," in *",$2,"*) return 0 ;; *) return 1 ;; esac; }

result_of() {
  local i
  for i in "${!S_ID[@]}"; do
    if [ "${S_ID[$i]}" = "$1" ]; then printf '%s' "${S_RES[$i]}"; return; fi
  done
}

record() { S_ID+=("$1"); S_STORY+=("$2"); S_DESC+=("$3"); S_RES+=("$4"); S_TIME+=("$5"); S_NOTE+=("$6"); }

# step <id> <historias> <descripción> <dependencias,coma> <función>
step() {
  local id="$1" story="$2" desc="$3" deps="$4" fn="$5" dep note_file="$out_dir/notes/$1" start rc note
  if [ -n "${ACCEPT_STEPS:-}" ] && ! contains "$ACCEPT_STEPS" "$id"; then
    record "$id" "$story" "$desc" SKIP 0 "no está en ACCEPT_STEPS"; return
  fi
  if [ -n "${ACCEPT_SKIP:-}" ] && contains "$ACCEPT_SKIP" "$id"; then
    record "$id" "$story" "$desc" SKIP 0 "ACCEPT_SKIP"; return
  fi
  for dep in ${deps//,/ }; do
    if [ "$(result_of "$dep")" = "FAIL" ]; then
      record "$id" "$story" "$desc" SKIP 0 "depende de '$dep', que falló"; return
    fi
  done
  printf '\n\033[1m==> [%s] %s (%s)\033[0m\n' "$id" "$desc" "$story"
  start="$(date +%s)"
  ( export ACCEPT_NOTE_FILE="$note_file"; "$fn" ) >"$out_dir/$id.log" 2>&1
  rc=$?
  note="$(cat "$note_file" 2>/dev/null || true)"
  local elapsed=$(($(date +%s) - start))
  if [ "$rc" -eq 0 ]; then
    record "$id" "$story" "$desc" OK "$elapsed" "$note"
    printf '    OK en %ss%s\n' "$elapsed" "${note:+ — $note}"
  elif [ "$rc" -eq 77 ]; then
    record "$id" "$story" "$desc" SKIP "$elapsed" "$note"
    printf '    SKIP — %s\n' "$note"
  else
    failed=1
    record "$id" "$story" "$desc" FAIL "$elapsed" "${note:-código $rc}"
    printf '    \033[31mFAIL\033[0m en %ss (código %s) — log: %s\n' "$elapsed" "$rc" "${out_dir#"$repo_root"/}/$id.log"
    tail -n 25 "$out_dir/$id.log" | sed 's/^/    | /'
  fi
}

skip() { printf '%s' "$1" >"$ACCEPT_NOTE_FILE"; exit 77; }
note() { printf '%s' "$1" >"$ACCEPT_NOTE_FILE"; }

# --- Pasos -------------------------------------------------------------------------------------
s_go_build() {
  $MAKE --no-print-directory build && ./bin/horus --version
}

s_go_test() {
  $MAKE --no-print-directory test
}

s_go_integration() {
  # Tests con testcontainers (PostgreSQL real): migraciones, RLS y matriz "A no ve B" (I0-08).
  if ! grep -rqE --include='*.go' '^//go:build .*integration' services packages tests; then
    skip "no hay tests con la etiqueta integration"
  fi
  # Los tests del esquema ClickHouse (I0-13) usan un servidor real: el del compose de
  # aceptación (BORRAN las bases flows/dim y los usuarios horus_*; por eso va tras e2e-api).
  local ch_user ch_db
  ch_user="$(env_value HORUS_CH_USER)"; ch_db="$(env_value HORUS_CH_DB)"
  HORUS_CH_TEST_DSN="clickhouse://${ch_user}@127.0.0.1:${HORUS_CH_NATIVE_PORT}/${ch_db}" \
    HORUS_CH_TEST_PASSWORD_FILE="$compose_dir/secrets/clickhouse_password.txt" \
    $GO test -tags=integration -race -shuffle=on -count=1 ./...
}

s_lint() {
  # Caché propia del repositorio: la caché global de golangci-lint guarda rutas absolutas y, con
  # varios clones o worktrees del mismo código, puede devolver avisos de OTRO árbol.
  export GOLANGCI_LINT_CACHE="${GOLANGCI_LINT_CACHE:-$out_dir/golangci-cache}"
  $MAKE --no-print-directory lint || return 1
  # El e2e de humo lleva la etiqueta `acceptance` y `make lint` no lo ve.
  "${GOLANGCI_LINT:-golangci-lint}" run --build-tags acceptance ./tests/acceptance/...
}

s_contracts() {
  $MAKE --no-print-directory contracts-check
}

s_sim_verify() {
  $MAKE --no-print-directory sim-verify
}

s_image() {
  bash scripts/accept/image.sh
}

s_compose_up() {
  bash scripts/compose-preflight.sh "$compose_dir/compose.dev.yaml" "$compose_dir/compose.observability.yaml"
  echo "compose: proyecto $COMPOSE_PROJECT_NAME, perfil app, imagen $HORUS_IMAGE, API en $api_url"
  "${compose[@]}" --profile "*" down --volumes --remove-orphans
  "${compose[@]}" up -d --no-build --wait --wait-timeout "$wait_timeout"
  local rc=$?
  "${compose[@]}" ps --format 'table {{.Service}}\t{{.Status}}\t{{.Ports}}'
  note "proyecto $COMPOSE_PROJECT_NAME, puertos +$offset"
  return $rc
}

s_healthy() {
  bash scripts/wait-healthy.sh "$wait_timeout" || return 1
  echo "--- $admin_url/readyz (horus-app)"
  curl -fsS --max-time 5 "$admin_url/readyz" || { echo "readyz de horus-app no responde 200"; return 1; }
  echo
  # Sin token, el gateway (detrás de Traefik) debe responder 401 problem+json: prueba que la
  # ruta Traefik → horus-app → borde del gateway está viva. Con token lo comprueba e2e-api.
  echo "--- $api_url/api/v1/system/status sin token (vía Traefik; se espera 401)"
  local code
  code="$(curl -sS --max-time 5 -o "$out_dir/system-status.json" -w '%{http_code}' "$api_url/api/v1/system/status")"
  cat "$out_dir/system-status.json"; echo
  [ "$code" = 401 ] || { echo "KO: HTTP $code, se esperaba 401 del gateway"; return 1; }
}

s_migrations() {
  local pg_user pg_db ch_user ch_db rc=0
  pg_user="$(env_value HORUS_PG_USER)"; pg_db="$(env_value HORUS_PG_DB)"
  ch_user="$(env_value HORUS_CH_USER)"; ch_db="$(env_value HORUS_CH_DB)"

  # PostgreSQL: los roles auth y devices aplican sus migraciones goose al arrancar (I0-06/I0-09).
  # Toda tabla con tenant_id tiene RLS salvo las outbox (docs/database.md §1.2); las particiones
  # heredan la política de su tabla padre.
  echo "--- PostgreSQL: versión de migraciones por esquema"
  local q="SELECT 'auth', max(version_id) FROM auth.goose_db_version UNION ALL SELECT 'devices', max(version_id) FROM devices.goose_db_version"
  "${compose[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -U "$pg_user" -d "$pg_db" -Atc "$q" || rc=1
  local rls
  rls="$("${compose[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -U "$pg_user" -d "$pg_db" -Atc \
    "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace JOIN pg_attribute a ON a.attrelid = c.oid
     WHERE n.nspname IN ('auth','devices') AND c.relkind IN ('r','p') AND NOT c.relispartition
       AND a.attname = 'tenant_id' AND NOT c.relrowsecurity
       AND n.nspname || '.' || c.relname NOT IN ('auth.outbox', 'devices.outbox')")" || rc=1
  echo "tablas con tenant_id sin RLS: ${rls:-?}"
  [ "${rls:-1}" = "0" ] || { echo "KO: hay tablas con tenant_id sin RLS"; rc=1; }

  # ClickHouse: `make migrate-ch` (idempotente; el rol ingester ya lo aplicó al arrancar).
  echo "--- make migrate-ch"
  HORUS_CLICKHOUSE_DSN="clickhouse://${ch_user}@127.0.0.1:${HORUS_CH_NATIVE_PORT}/${ch_db}" \
    $MAKE --no-print-directory migrate-ch || rc=1
  echo "--- ClickHouse: tablas del esquema v0"
  local tables
  tables="$("${compose[@]}" exec -T clickhouse sh -c \
    'clickhouse-client --user "$CLICKHOUSE_USER" --password "$(cat /run/secrets/clickhouse_password)" -q "SELECT database || '"'"'.'"'"' || name FROM system.tables WHERE database IN ('"'"'flows'"'"','"'"'dim'"'"') ORDER BY 1"')" || rc=1
  printf '%s\n' "$tables"
  local t
  for t in flows.flows_raw dim.tenant dim.site dim.router dim.customer; do
    grep -qx "$t" <<<"$tables" || { echo "KO: falta la tabla $t"; rc=1; }
  done
  note "PG auth+devices, RLS, CH $(grep -c . <<<"$tables") tablas"
  return $rc
}

s_e2e_api() {
  local email
  email="$(env_value HORUS_SEED_ADMIN_EMAIL)"
  ACCEPT_BASE_URL="$api_url" ACCEPT_ADMIN_EMAIL="${email:-admin@horus.localhost}" \
    ACCEPT_ADMIN_PASSWORD_FILE="$compose_dir/secrets/seed_admin_password.txt" \
    $GO test -tags acceptance -count=1 -v ./tests/acceptance/i0/
}

s_frontend_build() {
  command -v pnpm >/dev/null 2>&1 || { echo "falta pnpm"; return 1; }
  (cd apps/frontend && pnpm install --frozen-lockfile && pnpm build:mocks)
}

s_frontend_e2e() {
  local port="${ACCEPT_UI_PORT:-$((offset + 4173))}"
  (cd apps/frontend && E2E_NO_BUILD=1 PORT="$port" pnpm exec playwright test --grep @i0 --reporter=list)
}

s_lab_chr() {
  if [ ! -e /dev/kvm ]; then
    skip "sin /dev/kvm: el laboratorio CHR (make lab-selftest) necesita KVM; ejecútalo en un host con virtualización"
  fi
  $MAKE --no-print-directory lab-selftest
}

# --- Al salir: logs del compose si algo falló y limpieza ---------------------------------------
cleanup() {
  if [ "$compose_started" = 1 ]; then
    if [ "$failed" = 1 ]; then
      "${compose[@]}" ps -a >"$out_dir/compose-logs.txt" 2>&1 || true
      "${compose[@]}" logs --no-color --tail 200 >>"$out_dir/compose-logs.txt" 2>&1 || true
    fi
    if [ "${ACCEPT_KEEP:-0}" = 1 ]; then
      echo "accept-i0: compose '$COMPOSE_PROJECT_NAME' sigue levantado (ACCEPT_KEEP=1); API en $api_url"
    else
      "${compose[@]}" --profile "*" down --volumes --remove-orphans >/dev/null 2>&1 || true
    fi
  fi
}
trap cleanup EXIT
trap 'exit 130' INT TERM

# --- Batería -----------------------------------------------------------------------------------
step go-build       "I0-04"             "compilar el binario horus"                    ""                 s_go_build
step go-test        "I0-04..I0-17"      "tests Go con -race"                            ""                 s_go_test
step lint           "I0-01,I0-03"       "golangci-lint y CODEOWNERS"                     ""                 s_lint
step contracts      "I0-05"             "contracts-check (OpenAPI, eventos, proto, DDL)" ""                 s_contracts
step sim-verify     "I0-10,I0-12"       "simulador: seis escenarios y fixtures"          ""                 s_sim_verify
step image          "I0-04"             "imagen única horus"                            ""                 s_image
step compose-up     "I0-02"             "compose con perfil app (imagen local)"         "image"            s_compose_up
case "$(result_of compose-up)" in OK | FAIL) compose_started=1 ;; esac
step healthy        "I0-02,I0-04,I0-18" "contenedores healthy, /readyz y system/status" "compose-up"       s_healthy
step migrations     "I0-06,I0-09,I0-13" "migraciones PostgreSQL y ClickHouse"           "compose-up"       s_migrations
step e2e-api        "I0-06..I0-09"      "e2e de humo contra el backend real"            "compose-up"       s_e2e_api
step go-integration "I0-06..I0-09,I0-13" "tests de integración (testcontainers y CH)"  "compose-up"       s_go_integration
step frontend-build "I0-14..I0-16"      "build del frontend con mocks"                  ""                 s_frontend_build
step frontend-e2e   "I0-14..I0-16"      "Playwright @i0 contra mocks"                   "frontend-build"   s_frontend_e2e
step lab-chr        "I0-11"             "laboratorio MikroTik CHR"                      ""                 s_lab_chr

# --- Resumen -----------------------------------------------------------------------------------
printf '\n\033[1mResumen de accept-i0\033[0m (%s)\n' "$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
printf '%-15s %-5s %6s  %-19s %s\n' PASO RES. TIEMPO HISTORIAS NOTA
n_ok=0; n_fail=0; n_skip=0
for i in "${!S_ID[@]}"; do
  case "${S_RES[$i]}" in
    OK) color=32; n_ok=$((n_ok + 1)) ;;
    FAIL) color=31; n_fail=$((n_fail + 1)) ;;
    *) color=33; n_skip=$((n_skip + 1)) ;;
  esac
  note="${S_NOTE[$i]}"
  [ "${S_RES[$i]}" = FAIL ] && note="${note:+$note — }log: bin/accept-i0/${S_ID[$i]}.log"
  printf '%-15s \033[%sm%-5s\033[0m %5ss  %-19s %s\n' "${S_ID[$i]}" "$color" "${S_RES[$i]}" "${S_TIME[$i]}" "${S_STORY[$i]}" "$note"
done
printf '\nOK %d · FAIL %d · SKIP %d\n' "$n_ok" "$n_fail" "$n_skip"
if [ "$failed" = 1 ]; then
  [ "$compose_started" = 1 ] && echo "Logs del compose: bin/accept-i0/compose-logs.txt"
  echo "accept-i0: KO"
  exit 1
fi
echo "accept-i0: OK"
