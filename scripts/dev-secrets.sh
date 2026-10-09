#!/usr/bin/env bash
# dev-secrets.sh — genera los secretos de DESARROLLO del compose (docs/conventions.md §10).
#
# Crea en deployments/compose/secrets/ (ignorado por Git) los archivos que faltan:
#   postgres_password.txt, clickhouse_password.txt, valkey_password.txt  valores aleatorios
#   grafana_admin_password.txt (perfil observability, I0-18)              valor aleatorio
#   seed_admin_password.txt (superadmin semilla de horus-app, I0-06)      valor aleatorio
#   valkey_users.acl                                                      ACL de Valkey con el
#                                                                         hash SHA-256 de la contraseña
# Nunca sobrescribe un secreto existente ni imprime valores. Para regenerarlos: `make reset`
# y borra la carpeta secrets/ (los volúmenes se inicializaron con las contraseñas antiguas).
#
# Uso: scripts/dev-secrets.sh            (lo llama `make up`)
# Historia I0-02 (docs/backlog/increment-0.md).

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
dir="${HORUS_SECRETS_DIR:-$repo_root/deployments/compose/secrets}"

umask 077
mkdir -p "$dir"
chmod 700 "$dir"

random_secret() {
  # 32 caracteres alfanuméricos (sin símbolos: evitan problemas de escape en DSN).
  LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 32 || true
}

sha256_hex() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | cut -d' ' -f1
  else
    shasum -a 256 | cut -d' ' -f1
  fi
}

created=()
for name in postgres_password clickhouse_password valkey_password grafana_admin_password seed_admin_password; do
  file="$dir/$name.txt"
  if [ ! -s "$file" ]; then
    random_secret >"$file"
    created+=("$name.txt")
  fi
  # Los contenedores leen el archivo con usuarios sin privilegios (valkey, postgres...): el
  # archivo es legible, la carpeta no (700) para el resto de usuarios del host.
  chmod 644 "$file"
done

acl="$dir/valkey_users.acl"
if [ ! -s "$acl" ] || [ "$dir/valkey_password.txt" -nt "$acl" ]; then
  hash="$(printf '%s' "$(cat "$dir/valkey_password.txt")" | sha256_hex)"
  printf 'user default on #%s ~* &* +@all\n' "$hash" >"$acl"
  created+=("valkey_users.acl")
fi
chmod 644 "$acl"

if [ "${#created[@]}" -gt 0 ]; then
  echo "dev-secrets: generados en ${dir#"$repo_root"/}: ${created[*]}"
else
  echo "dev-secrets: secretos ya presentes en ${dir#"$repo_root"/}"
fi
