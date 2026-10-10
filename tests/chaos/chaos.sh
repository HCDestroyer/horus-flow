#!/usr/bin/env bash
# chaos.sh — `make chaos-i1` (historia I1-26): pruebas de fallo del incremento 1.
#
# Mismo compose y arnés que la prueba de carga (tests/load/load.sh, tests/loadkit); el driver es
# tests/chaos (ver su comentario de paquete). Variables propias:
#
#   CHAOS_SCENARIOS (clickhouse,nats,app,collector)   escenarios, en orden
#   CHAOS_RATE (5000)                                   flujos/s durante cada escenario
#   CHAOS_DOWN (30s)                                    caída de ClickHouse, NATS y horus-app
#   CHAOS_COLLECTOR_DOWN (1m)                           caída del collector
#
# y las de load.sh (LOAD_REUSE, LOAD_KEEP, LOAD_SKIP_IMAGE). Resultados: bin/load/chaos.{md,json}.
set -euo pipefail
LOAD_DRIVER=chaos exec bash "$(dirname "$0")/../load/load.sh" "$@"
