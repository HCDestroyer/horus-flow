#!/usr/bin/env bash
# wait-healthy.sh — espera a que TODOS los contenedores del compose de desarrollo estén
# `healthy` (job de CI `compose-smoke`: `make up && scripts/wait-healthy.sh && make down`).
#
# Un contenedor sin healthcheck cuenta como fallo (criterio 2 de I0-02). Si vence el plazo,
# muestra el estado de cada servicio y las últimas líneas de log de los que no están sanos.
#
# Uso: scripts/wait-healthy.sh [segundos]     (por defecto 120; HORUS_WAIT_TIMEOUT también vale)
# Variables: COMPOSE (comando compose completo; por defecto el del Makefile).
# Historia I0-02 (docs/backlog/increment-0.md).

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
timeout="${1:-${HORUS_WAIT_TIMEOUT:-120}}"
compose_dir="$repo_root/deployments/compose"
read -r -a compose <<<"${COMPOSE:-docker compose --project-directory $compose_dir -f $compose_dir/compose.dev.yaml}"

start="$(date +%s)"
while :; do
  ids="$("${compose[@]}" ps -q)"
  if [ -z "$ids" ]; then
    echo "wait-healthy: no hay contenedores en marcha (¿ejecutaste make up?)" >&2
    exit 1
  fi
  # nombre estado salud
  # $ids sin comillas a propósito: una lista de IDs hexadecimales separados por saltos de línea.
  # shellcheck disable=SC2086
  status="$(docker inspect --format '{{.Name}} {{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{else}}sin-healthcheck{{end}}' $ids | sed 's#^/##' | LC_ALL=C sort)"
  pending="$(printf '%s\n' "$status" | awk '$3 != "healthy"')"
  broken="$(printf '%s\n' "$status" | awk '$3 == "sin-healthcheck" || $2 == "exited" || $2 == "dead" || $3 == "unhealthy"')"
  elapsed=$(($(date +%s) - start))

  if [ -z "$pending" ]; then
    printf '%s\n' "$status" | awk '{ printf "  %-40s %s\n", $1, $3 }'
    echo "wait-healthy: OK — $(printf '%s\n' "$status" | wc -l | tr -d ' ') contenedores healthy en ${elapsed}s"
    exit 0
  fi

  if [ -n "$broken" ] || [ "$elapsed" -ge "$timeout" ]; then
    echo "wait-healthy: KO tras ${elapsed}s (plazo ${timeout}s)" >&2
    printf '%s\n' "$status" | awk '{ printf "  %-40s %-10s %s\n", $1, $2, $3 }' >&2
    for name in $(printf '%s\n' "$pending" | awk '{print $1}'); do
      echo "--- últimas líneas de log de $name ---" >&2
      docker logs --tail 30 "$name" 2>&1 | sed 's/^/  /' >&2 || true
    done
    exit 1
  fi
  sleep 2
done
