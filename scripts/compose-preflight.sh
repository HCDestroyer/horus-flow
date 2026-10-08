#!/usr/bin/env bash
# compose-preflight.sh — prepara y valida el entorno del compose de desarrollo antes de `up`.
#
#   1. Si falta deployments/compose/.env, lo crea copiando .env.example.
#   2. Genera los secretos que falten (scripts/dev-secrets.sh).
#   3. Comprueba que cada variable obligatoria del compose (`${VAR:?...}`) tiene valor en el
#      entorno o en .env, y que cada archivo de secreto existe y no está vacío.
#
# Ante un error nombra la variable o el archivo, NUNCA imprime valores. Sale con 1 si falta algo.
# Uso: scripts/compose-preflight.sh [archivo-compose]   (lo llama `make up`)
# Historia I0-02 (docs/backlog/increment-0.md).

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
compose_file="${1:-$repo_root/deployments/compose/compose.dev.yaml}"
compose_dir="$(cd "$(dirname "$compose_file")" && pwd)"
env_file="$compose_dir/.env"
example="$compose_dir/.env.example"

if [ ! -f "$env_file" ]; then
  cp "$example" "$env_file"
  echo "preflight: creado ${env_file#"$repo_root"/} a partir de .env.example"
fi

HORUS_SECRETS_DIR="$compose_dir/secrets" bash "$repo_root/scripts/dev-secrets.sh"

# Valor de una variable en .env (última asignación gana, como en compose). No se imprime.
env_file_value() {
  awk -v k="$1" '
    /^[[:space:]]*#/ || !/=/ { next }
    {
      line = $0; sub(/^[[:space:]]*(export[[:space:]]+)?/, "", line)
      key = substr(line, 1, index(line, "=") - 1); gsub(/[[:space:]]/, "", key)
      if (key == k) { val = substr(line, index(line, "=") + 1); found = 1 }
    }
    END { if (found) print val }
  ' "$env_file"
}

errors=0

# Variables obligatorias: las que el compose declara con ${VAR:?...}.
required="$(grep -oE '\$\{[A-Z0-9_]+:\?' "$compose_file" | sed -E 's/^\$\{//; s/:\?$//' | LC_ALL=C sort -u)"
for var in $required; do
  value="${!var:-}"
  if [ -z "$value" ]; then
    value="$(env_file_value "$var")"
    value="${value%\"}"; value="${value#\"}"; value="${value%\'}"; value="${value#\'}"
  fi
  if [ -z "$value" ]; then
    echo "preflight: ERROR — falta la variable obligatoria $var (defínela en ${env_file#"$repo_root"/}; ver .env.example)" >&2
    errors=1
  fi
done

# Secretos declarados en el bloque `secrets:` de primer nivel (file: ./secrets/...).
secret_files="$(awk '
  /^secrets:/ { in_s = 1; next }
  /^[^[:space:]#]/ { in_s = 0 }
  in_s && $1 == "file:" { print $2 }
' "$compose_file")"
for rel in $secret_files; do
  path="$compose_dir/${rel#./}"
  if [ ! -s "$path" ]; then
    echo "preflight: ERROR — falta o está vacío el secreto ${path#"$repo_root"/} (ejecuta scripts/dev-secrets.sh)" >&2
    errors=1
  fi
done

if [ "$errors" -ne 0 ]; then
  exit 1
fi
echo "preflight: OK — variables obligatorias y secretos presentes"
