#!/usr/bin/env bash
# observability-smoke.sh — prueba de humo del perfil `observability` (historia I0-18).
#
# 1. `make up PROFILE=observability` (o PROFILE=app,observability con OBS_SMOKE_APP=1) y espera
#    a que todo esté healthy.
# 2. Prometheus: los jobs horus, traefik, prometheus, loki, alloy y grafana están configurados y
#    los de la pila responden (up == 1). Con OBS_SMOKE_APP=1, también los tres horus-*.
# 3. Grafana: datasources Prometheus y Loki sanos y dashboard "Horus · Platform Overview" con
#    las filas "API" e "Ingesta".
# 4. Loki: llegan logs de los contenedores (vía Alloy) con los labels service y container.
# 5. `make down` (salvo OBS_SMOKE_KEEP=1).
#
# Uso: scripts/ci/observability-smoke.sh          (o `make observability-smoke`)
# Variables: OBS_SMOKE_APP=1 (incluye el perfil app), OBS_SMOKE_KEEP=1 (no baja el compose),
#            OBS_SMOKE_TIMEOUT (s, por defecto 120) y las de deployments/compose/.env.

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$repo_root"
compose_dir="deployments/compose"
timeout="${OBS_SMOKE_TIMEOUT:-120}"
profile="observability"
[ "${OBS_SMOKE_APP:-0}" = "1" ] && profile="app,observability"

fail() { echo "observability-smoke: KO — $*" >&2; exit 1; }
ok() { echo "observability-smoke: ok — $*"; }

cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then
    COMPOSE_PROFILES="$profile" ${COMPOSE:-docker compose --project-directory $compose_dir -f $compose_dir/compose.dev.yaml -f $compose_dir/compose.observability.yaml} \
      ps -a 2>/dev/null || true
  fi
  if [ "${OBS_SMOKE_KEEP:-0}" != "1" ]; then
    make --no-print-directory down >/dev/null 2>&1 || true
  fi
  exit "$status"
}
trap cleanup EXIT

make --no-print-directory up PROFILE="$profile" COMPOSE_WAIT_TIMEOUT="$timeout"
COMPOSE_PROFILES="$profile" bash scripts/wait-healthy.sh "$timeout"

# Valor de una variable: entorno > .env > valor por defecto.
env_value() {
  local v="${!1:-}"
  if [ -z "$v" ] && [ -f "$compose_dir/.env" ]; then
    v="$(awk -F= -v k="$1" '$1 == k { v = substr($0, index($0, "=") + 1) } END { print v }' "$compose_dir/.env")"
  fi
  printf '%s' "${v:-$2}"
}
bind="$(env_value HORUS_BIND_ADDR 127.0.0.1)"
[ "$bind" = "0.0.0.0" ] && bind="127.0.0.1"
prom="http://$bind:$(env_value HORUS_PROMETHEUS_PORT 9091)"
graf="http://$bind:$(env_value HORUS_GRAFANA_PORT 3001)"
graf_pass_file="$compose_dir/secrets/grafana_admin_password.txt"
[ -s "$graf_pass_file" ] || fail "falta $graf_pass_file"

# Reintenta una comprobación hasta el plazo (los primeros scrapes tardan ~15 s).
retry() {
  local what="$1"; shift
  local start; start="$(date +%s)"
  until "$@"; do
    if [ $(($(date +%s) - start)) -ge "$timeout" ]; then fail "$what (tras ${timeout}s)"; fi
    sleep 3
  done
  ok "$what"
}

# --- Prometheus ---------------------------------------------------------------------------
targets_json() { curl -fsS "$prom/api/v1/targets?state=active"; }

prom_jobs_configured() {
  local jobs
  jobs="$(targets_json | jq -r '[.data.activeTargets[].labels.job] | unique | join(" ")')" || return 1
  for j in horus traefik prometheus loki alloy grafana; do
    [[ " $jobs " == *" $j "* ]] || return 1
  done
}

prom_stack_up() {
  local want="traefik prometheus loki alloy grafana"
  [ "$profile" = "app,observability" ] && want="$want horus"
  local down
  down="$(targets_json | jq -r --arg want "$want" '
    ($want | split(" ")) as $w
    | [.data.activeTargets[] | select(.labels.job as $j | $w | index($j)) | select(.health != "up")
       | "\(.labels.job)/\(.labels.instance)"] | join(" ")')" || return 1
  [ -z "$down" ] || { echo "  aún no up: $down"; return 1; }
}

retry "Prometheus tiene configurados los jobs horus, traefik, prometheus, loki, alloy y grafana" prom_jobs_configured
retry "Prometheus raspa la pila${OBS_SMOKE_APP:+ y los contenedores horus-*} (up == 1)" prom_stack_up

# --- Grafana ------------------------------------------------------------------------------
gcurl() { curl -fsS -u "admin:$(cat "$graf_pass_file")" "$graf$1"; }

grafana_datasources_ok() {
  gcurl /api/datasources/uid/prometheus/health | jq -e '.status == "OK"' >/dev/null &&
    gcurl /api/datasources/uid/loki/health | jq -e '.status == "OK"' >/dev/null
}

grafana_dashboard_ok() {
  gcurl /api/dashboards/uid/horus-platform-overview | jq -e '
    [.dashboard.panels[] | select(.type == "row") | .title] as $rows
    | ($rows | index("API")) != null and ($rows | index("Ingesta")) != null
    and .meta.folderTitle == "Horus Platform"' >/dev/null
}

retry "Grafana: datasources Prometheus y Loki sanos" grafana_datasources_ok
retry "Grafana: dashboard 'Horus · Platform Overview' con paneles API e Ingesta" grafana_dashboard_ok

# --- Loki (vía el proxy de datasource de Grafana; Loki no se publica en el host) -----------
loki_has_logs() {
  local labels
  labels="$(gcurl /api/datasources/proxy/uid/loki/loki/api/v1/labels | jq -r '.data // [] | join(" ")')" || return 1
  [[ " $labels " == *" service "* && " $labels " == *" container "* ]] || return 1
  gcurl "/api/datasources/proxy/uid/loki/loki/api/v1/label/service/values" | jq -e '.data | index("prometheus") != null' >/dev/null
}

retry "Loki recibe logs de los contenedores (labels service y container)" loki_has_logs

echo "observability-smoke: OK"
