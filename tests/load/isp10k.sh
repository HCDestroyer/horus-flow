#!/usr/bin/env bash
# isp10k.sh — `make load-isp10k`: carga y caída de ClickHouse de un ISP de 10 000 clientes.
#
# Mismo arnés que `make load-i1` (tests/load/load.sh, tests/loadkit) con el escenario isp10k del
# simulador (10 000 clientes tras NAT con IE 225-228, ~3 000 /64 delegados, infectados) a tasa fija:
#
#   1. rampa ISP10K_RATES (10000,20000,40000,60000) de ISP10K_STEP (3m) cada escalón; para en el
#      primero que no cumple los criterios de load-i1 (sin pérdida, lag acotado, drenaje < 90 s,
#      p95 de la API < 500 ms) y publica el máximo sostenible;
#   2. ClickHouse caído ISP10K_CHAOS_DOWN (5m) a ISP10K_CHAOS_RATE (20000) flujos/s: 0 pérdida, 0
#      duplicados y drenaje del backlog de TLM_FLOWS (ISP10K_SKIP_CHAOS=1 lo omite).
#
# Proyecto y puertos propios (horus-load-isp10k, +24000) e imagen horus:load-isp10k para no chocar
# con otras pruebas. LOAD_KEEP=1 conserva el compose. Resultados: bin/load/results.{md,json} y
# bin/load/chaos.{md,json}. Sale con 1 si 10 000/s o el caos fallan.
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$repo_root"
export SCENARIO=isp10k
export LOAD_PROJECT="${LOAD_PROJECT:-horus-load-isp10k}"
export LOAD_PORT_OFFSET="${LOAD_PORT_OFFSET:-24000}"
export LOAD_IMAGE="${LOAD_IMAGE:-horus:load-isp10k}"
rates="${ISP10K_RATES:-10000,20000,40000,60000}"
first="${rates%%,*}"
rest="${rates#*,}"
[ "$rest" = "$rates" ] && rest=""
keep="${LOAD_KEEP:-0}"

rc=0
LOAD_KEEP=1 RATE="$first" DURATION="${ISP10K_STEP:-3m}" RAMP=$([ -n "$rest" ] && echo 1 || echo 0) \
  RAMP_RATES="$rest" RAMP_DURATION="${ISP10K_STEP:-3m}" bash tests/load/load.sh || rc=$?
cp bin/load/results.md bin/load/results-isp10k.md 2>/dev/null || true
cp bin/load/results.json bin/load/results-isp10k.json 2>/dev/null || true

if [ "${ISP10K_SKIP_CHAOS:-0}" != 1 ]; then
  LOAD_REUSE=1 LOAD_KEEP=1 LOAD_DRIVER=chaos CHAOS_SCENARIOS=clickhouse CHAOS_RATE="${ISP10K_CHAOS_RATE:-20000}" \
    CHAOS_DOWN="${ISP10K_CHAOS_DOWN:-5m}" bash tests/load/load.sh || rc=$?
fi

if [ "$keep" != 1 ]; then
  bash tests/load/stack.sh down >/dev/null 2>&1 || true
fi
exit "$rc"
