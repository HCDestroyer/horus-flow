#!/usr/bin/env bash
# new-module.sh — crea un módulo nuevo de `horus` a partir de services/_example
# y registra su rol en services/cmd/horus/roles.go (I0-04, ADR-0025).
#
# Uso: scripts/new-module.sh <nombre>
#   <nombre>: minúsculas y dígitos, empieza por letra (será paquete Go, rol y
#   prefijo de variables HORUS_<NOMBRE>_*).
#
# Después: go build ./... && go run ./services/cmd/horus --roles=<nombre>
set -euo pipefail

usage() {
  echo "uso: $0 <nombre>   (p. ej. $0 demo)" >&2
  exit 2
}

[[ $# -eq 1 ]] || usage
name="$1"
if [[ ! "$name" =~ ^[a-z][a-z0-9]*$ ]]; then
  echo "new-module: nombre inválido '$name': solo [a-z0-9], empezando por letra" >&2
  exit 2
fi
case "$name" in
  cmd | example | all | internal | api) echo "new-module: '$name' está reservado" >&2; exit 2 ;;
esac

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
template="$root/services/_example"
dest="$root/services/$name"
roles="$root/services/cmd/horus/roles.go"
module_path="$(awk '/^module /{print $2; exit}' "$root/go.mod")"
upper="$(echo "$name" | tr '[:lower:]' '[:upper:]')"

[[ -d "$template" ]] || { echo "new-module: falta la plantilla $template" >&2; exit 1; }
[[ -f "$roles" ]] || { echo "new-module: falta $roles" >&2; exit 1; }
if [[ -e "$dest/module.go" ]]; then
  echo "new-module: ya existe $dest/module.go" >&2
  exit 1
fi
if grep -q "\"$module_path/services/$name\"" "$roles"; then
  echo "new-module: el rol '$name' ya está registrado en $roles" >&2
  exit 1
fi

mkdir -p "$dest"
# Copia la plantilla (sin su README) sin pisar archivos existentes del destino.
(cd "$template" && find . -type f -name '*.go' -print0) | while IFS= read -r -d '' f; do
  mkdir -p "$dest/$(dirname "$f")"
  sed -e "s#services/_example#services/$name#g" \
      -e "s/\bexample\b/$name/g" \
      -e "s/EXAMPLE/$upper/g" \
      "$template/$f" > "$dest/$f"
done
sed -i "1s|.*|// Package $name es el módulo del rol $name de \`horus\` (generado desde services/_example).|" "$dest/module.go"

if [[ ! -e "$dest/README.md" ]]; then
  cat > "$dest/README.md" <<EOF
# services/$name — módulo \`$name\`

- **Propósito:** _(describir)_. Generado con \`scripts/new-module.sh $name\` desde
  [\`services/_example\`](../_example/README.md).
- **Rol:** \`$name\` (\`HORUS_ROLES=$name\` o \`--roles=$name\`).
- **Dueño:** _(agente)_ — añadir la carpeta a \`.github/CODEOWNERS\`.

## Variables de entorno

| Variable | Defecto | Uso |
| --- | --- | --- |
| \`HORUS_${upper}_GREETING\` | \`hola\` | Saludo de la ruta de ejemplo |
| \`HORUS_${upper}_DEPENDENCY_ADDR\` | — | Dependencia opcional (chequeo degradable \`dependency\` en \`/readyz\`) |

## Rutas

- \`GET /api/v1/$name/hello/{name}\`

## Métricas y eventos

Solo las RED HTTP comunes (\`http_server_*{service="$name"}\`).
EOF
fi

# Registro del rol en el binario (antes de los marcadores).
tmp="$(mktemp)"
awk -v imp="	\"$module_path/services/$name\"" \
    -v role="	{name: $name.Role, factory: $name.Register}," '
  /^\t\/\/ new-module.sh:imports$/ { print imp }
  /^\t\/\/ new-module.sh:roles$/ { print role }
  { print }
' "$roles" > "$tmp"
mv "$tmp" "$roles"
gofmt -w "$roles" "$dest"

echo "módulo creado en services/$name y rol '$name' registrado en services/cmd/horus/roles.go"
echo "siguiente: go build ./... && go run ./services/cmd/horus --roles=$name"
