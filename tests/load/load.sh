#!/usr/bin/env bash
# load.sh — `make load-i1` (historia I1-26): prueba de carga del incremento 1.
#
# Compila el simulador y el driver, levanta el compose de tests/load/stack.sh y ejecuta
# tests/load (ver su comentario de paquete). Variables:
#
#   RATE (5000)            tasa del criterio en flujos/s (varias separadas por comas)
#   DURATION (5m)          duración a esa tasa; el nocturno usa 1h
#   RAMP=1                 después, busca el máximo sostenible (RAMP_RATES, RAMP_DURATION=2m)
#   LOAD_REUSE=1           reutiliza un compose ya levantado (no lo crea ni lo destruye)
#   LOAD_KEEP=1            deja el compose levantado al terminar
#   LOAD_SKIP_IMAGE=1      no reconstruye la imagen horus:load si ya existe
#
# Resultados: bin/load/results.{md,json}, logs del simulador en bin/load/, logs del compose en
# bin/load/compose-logs.txt si algo falla. Sale con 1 si la tasa del criterio no se sostiene.

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$repo_root"
GO="${GO:-go}"
export GOTOOLCHAIN="${GOTOOLCHAIN:-local}"
mkdir -p bin/load
chmod 755 bin/load

"$GO" build -o bin/load/flowsim ./tools/flowsim/cmd/flowsim
"$GO" build -o bin/load/horus-load ./tests/load

started=0
cleanup() {
  rc=$?
  if [ "$started" = 1 ]; then
    if [ "$rc" != 0 ]; then
      bash tests/load/stack.sh compose ps -a >bin/load/compose-logs.txt 2>&1 || true
      bash tests/load/stack.sh compose logs --no-color --tail 300 >>bin/load/compose-logs.txt 2>&1 || true
    fi
    if [ "${LOAD_KEEP:-0}" != 1 ]; then
      bash tests/load/stack.sh down >/dev/null 2>&1 || true
    fi
  fi
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

if [ "${LOAD_REUSE:-0}" != 1 ]; then
  rm -f bin/load/state.json
  started=1
  bash tests/load/stack.sh up
fi

# Variables del compose (puertos, rutas) sin pasar por eval.
while IFS= read -r line; do
  line="${line#export }"
  key="${line%%=*}"
  val="${line#*=}"
  val="${val#\'}"
  val="${val%\'}"
  export "$key=$val"
done < <(bash tests/load/stack.sh env)

./bin/load/horus-load "$@"
