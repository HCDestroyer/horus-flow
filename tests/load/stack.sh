#!/usr/bin/env bash
# stack.sh — compose propio de las pruebas de carga y de fallo (historia I1-26).
#
# Uso: tests/load/stack.sh up|down|env|compose <args…>
#
#   up       imagen horus (scripts/accept/image.sh), secretos de desarrollo y compose con el perfil
#            app + tests/load/compose.load.yaml; espera a que todo esté healthy
#   down     destruye el proyecto y sus volúmenes
#   env      imprime las variables (puertos, rutas) para `eval` desde otros scripts
#   compose  ejecuta `docker compose` con los ficheros y el proyecto de la prueba
#
# Es un proyecto APARTE (LOAD_PROJECT=horus-load, puertos +LOAD_PORT_OFFSET=23000) con volúmenes
# nuevos: no toca el `make up` de desarrollo ni el compose de accept-i0/accept-i1. Sin el perfil
# de observabilidad (Prometheus/Grafana): las métricas se leen directamente de /metrics.
#
# Variables: LOAD_PROJECT, LOAD_PORT_OFFSET, LOAD_IMAGE (horus:load), ACCEPT_IMAGE_MODE
# (auto|dockerfile|prebuilt; en el sandbox `docker build` falla por la CA del proxy y auto cae en
# prebuilt), LOAD_SKIP_IMAGE=1 (reutiliza la imagen existente), LOAD_WAIT_TIMEOUT (300),
# HORUS_CH_NOFILE (16384 si `ulimit -n` es menor que 262144).

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$repo_root"

compose_dir="$repo_root/deployments/compose"
export COMPOSE_PROJECT_NAME="${LOAD_PROJECT:-horus-load}"
export COMPOSE_PROFILES=app
export HORUS_IMAGE="${LOAD_IMAGE:-horus:load}"
offset="${LOAD_PORT_OFFSET:-23000}"
for pv in HORUS_PG_PORT:5432 HORUS_CH_HTTP_PORT:8123 HORUS_CH_NATIVE_PORT:9000 HORUS_NATS_PORT:4222 \
  HORUS_NATS_MONITOR_PORT:8222 HORUS_VALKEY_PORT:6379 HORUS_HTTP_PORT:8000 HORUS_TRAEFIK_ADMIN_PORT:8082 \
  HORUS_COLLECTOR_IPFIX_PORT:4739 HORUS_COLLECTOR_V9_PORT:2055 HORUS_APP_ADMIN_PORT:8081 \
  HORUS_LOAD_COLLECTOR_ADMIN_PORT:8083; do
  export "${pv%%:*}=$((${pv##*:} + offset))"
done
if [ -z "${HORUS_CH_NOFILE:-}" ] && [ "$(ulimit -n)" != unlimited ] && [ "$(ulimit -n)" -lt 262144 ]; then
  export HORUS_CH_NOFILE=16384
fi
export HORUS_LOAD_DIR="${HORUS_LOAD_DIR:-$repo_root/bin/load}"
export HORUS_LOAD_INV_DIR="$HORUS_LOAD_DIR/inventory"
export HORUS_LOAD_REPO="$repo_root"
compose=(docker compose --project-directory "$compose_dir" --env-file "$compose_dir/.env.example"
  -f "$compose_dir/compose.dev.yaml" -f "$repo_root/tests/load/compose.load.yaml")

print_env() {
  cat <<EOF
export COMPOSE_PROJECT_NAME='$COMPOSE_PROJECT_NAME'
export LOAD_API_URL='http://127.0.0.1:$HORUS_HTTP_PORT'
export LOAD_APP_METRICS='http://127.0.0.1:$HORUS_APP_ADMIN_PORT/metrics'
export LOAD_COLLECTOR_METRICS='http://127.0.0.1:$HORUS_LOAD_COLLECTOR_ADMIN_PORT/metrics'
export LOAD_NATS_URL='nats://127.0.0.1:$HORUS_NATS_PORT'
export LOAD_NATS_MONITOR='http://127.0.0.1:$HORUS_NATS_MONITOR_PORT'
export LOAD_CH_HTTP='http://127.0.0.1:$HORUS_CH_HTTP_PORT'
export LOAD_CH_USER='horus'
export LOAD_CH_PASSWORD_FILE='$compose_dir/secrets/clickhouse_password.txt'
export LOAD_ADMIN_EMAIL='admin@horus.localhost'
export LOAD_ADMIN_PASSWORD_FILE='$compose_dir/secrets/seed_admin_password.txt'
export LOAD_COLLECTOR_TARGET='127.0.0.1:$HORUS_COLLECTOR_IPFIX_PORT'
export LOAD_INVENTORY='$HORUS_LOAD_INV_DIR/inventory.json'
export LOAD_COMPOSE='${compose[*]}'
EOF
}

case "${1:-}" in
  up)
    mkdir -p "$HORUS_LOAD_INV_DIR"
    chmod 755 "$HORUS_LOAD_DIR" "$HORUS_LOAD_INV_DIR"
    # Inventario base vacío: el driver escribe el exportador real tras dar de alta el ISP y
    # reinicia collector y horus-app (con NATS, el fichero base solo se lee al arrancar).
    [ -f "$HORUS_LOAD_INV_DIR/inventory.json" ] || echo '{}' >"$HORUS_LOAD_INV_DIR/inventory.json"
    chmod 644 "$HORUS_LOAD_INV_DIR/inventory.json"
    bash scripts/dev-secrets.sh >/dev/null
    if [ "${LOAD_SKIP_IMAGE:-0}" != 1 ] || ! docker image inspect "$HORUS_IMAGE" >/dev/null 2>&1; then
      ACCEPT_IMAGE="$HORUS_IMAGE" bash scripts/accept/image.sh
    fi
    "${compose[@]}" --profile "*" down --volumes --remove-orphans >/dev/null 2>&1 || true
    "${compose[@]}" up -d --no-build --wait --wait-timeout "${LOAD_WAIT_TIMEOUT:-300}"
    "${compose[@]}" ps --format 'table {{.Service}}\t{{.Status}}'
    ;;
  down)
    "${compose[@]}" --profile "*" down --volumes --remove-orphans
    ;;
  env)
    print_env
    ;;
  compose)
    shift
    "${compose[@]}" "$@"
    ;;
  *)
    echo "uso: $0 up|down|env|compose <args>" >&2
    exit 2
    ;;
esac
