#!/usr/bin/env bash
# restart-flows.sh — `make chaos-restart-flows` (D23): matriz de reinicio brusco de la cadena de
# flujos (collector, horus-app, NATS, ClickHouse × kill -9 y docker restart, varias veces y en
# momentos aleatorios) con ingesta a tasa constante. Driver: tests/chaos/restart.go.
#
# Variables:
#
#   CHAOS_RATE (10000)                                   flujos/s constantes del simulador
#   SCENARIO (isp10k)                                    escenario de flowsim (normal | isp10k)
#   CHAOS_TARGETS (horus-collector,horus-app,nats,clickhouse)
#   CHAOS_METHODS (kill9,restart)
#   CHAOS_REPEAT (3)                                     reinicios por escenario
#   CHAOS_SEED                                           semilla de los momentos (por defecto, la hora)
#
# Proyecto horus-chaos-flows (puertos +25000), imagen horus:chaos-flows, collector con CAP_NET_ADMIN
# (32 MiB de búfer UDP) y spool a disco. kill -9 se envía al PID del contenedor desde el host: hace
# falta poder señalar procesos de Docker (root). LOAD_KEEP=1 conserva el compose, LOAD_SKIP_IMAGE=1
# reutiliza la imagen. Resultados: bin/load/chaos-restart.{md,json}.
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$repo_root"
export SCENARIO="${SCENARIO:-isp10k}"
export LOAD_PROJECT="${LOAD_PROJECT:-horus-chaos-flows}"
export LOAD_PORT_OFFSET="${LOAD_PORT_OFFSET:-25000}"
export LOAD_IMAGE="${LOAD_IMAGE:-horus:chaos-flows}"
export LOAD_TLM_MAX_BYTES="${LOAD_TLM_MAX_BYTES:-1610612736}"
export LOAD_COMPOSE_EXTRA="${LOAD_COMPOSE_EXTRA:-tests/load/compose.isp10k.yaml}"
export CHAOS_RATE="${CHAOS_RATE:-10000}"
LOAD_DRIVER=chaos CHAOS_SUITE=restart-flows exec bash tests/load/load.sh "$@"
