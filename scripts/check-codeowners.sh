#!/usr/bin/env bash
# check-codeowners.sh — verifica que .github/CODEOWNERS asigna dueño a:
#   * toda carpeta de primer nivel del repositorio (incluidas las ocultas, salvo .git), y
#   * cada módulo de services/ (services/<módulo>/, incluido services/cmd/).
#
# "Tener dueño" = existe una regla explícita para esa carpeta (`/ruta/`, `ruta/`, `/ruta/**` o
# `/ruta`) con al menos un dueño (@usuario, @org/equipo o correo), dentro de un bloque precedido
# por un comentario `# Agente: <PLAT|CORE|FLOW|SEC|UI|INT>`. La regla comodín `*` no cuenta.
#
# Uso: scripts/check-codeowners.sh [-v]
#   -v                  muestra la tabla carpeta → agente → dueños
#   CODEOWNERS=<ruta>   usa otro archivo (por defecto .github/CODEOWNERS)
#
# Sale con 0 si todo tiene dueño; 1 si falta alguno; 2 ante errores de uso.
# Historia I0-01 (docs/backlog/increment-0.md).

set -euo pipefail

verbose=0
case "${1:-}" in
  -v | --verbose) verbose=1 ;;
  "") ;;
  -h | --help)
    sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'
    exit 0
    ;;
  *)
    echo "uso: $0 [-v]" >&2
    exit 2
    ;;
esac

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

codeowners="${CODEOWNERS:-.github/CODEOWNERS}"
if [ ! -f "$codeowners" ]; then
  echo "ERROR: no existe $codeowners" >&2
  exit 1
fi

valid_agents="PLAT CORE FLOW SEC UI INT"

# Reglas normalizadas: "ruta<TAB>agente<TAB>dueños" (una por línea).
rules="$(
  awk -v valid="$valid_agents" '
    BEGIN { n = split(valid, v, " "); for (i = 1; i <= n; i++) ok[v[i]] = 1; agent = "" }
    /^[[:space:]]*#/ {
      if (match($0, /Agente:[[:space:]]*[A-Z]+/)) {
        a = substr($0, RSTART, RLENGTH); sub(/Agente:[[:space:]]*/, "", a)
        agent = (a in ok) ? a : ""
      } else if ($0 ~ /Agente:/) {
        agent = ""
      }
      next
    }
    NF == 0 { next }
    {
      path = $1
      owners = ""
      for (i = 2; i <= NF; i++) { if ($i ~ /^#/) break; owners = owners (owners == "" ? "" : " ") $i }
      sub(/^\//, "", path); sub(/\/\*\*$/, "", path); sub(/\/$/, "", path)
      printf "%s\t%s\t%s\n", path, agent, owners
    }
  ' "$codeowners"
)"

# Carpetas que deben tener dueño (las ignoradas por Git se descartan más abajo).
required=()
while IFS= read -r d; do
  required+=("${d#./}")
done < <(find . -mindepth 1 -maxdepth 1 -type d ! -name .git | LC_ALL=C sort)
if [ -d services ]; then
  while IFS= read -r d; do
    required+=("${d#./}")
  done < <(find ./services -mindepth 1 -maxdepth 1 -type d | LC_ALL=C sort)
fi

# Ignora carpetas excluidas por .gitignore (bin/, node_modules/, datos locales...).
is_ignored() {
  git rev-parse --is-inside-work-tree >/dev/null 2>&1 && git check-ignore -q "$1/" 2>/dev/null
}

missing=0
[ "$verbose" -eq 1 ] && printf '%-28s %-6s %s\n' "CARPETA" "AGENTE" "DUEÑOS"
checked=0
for dir in "${required[@]}"; do
  if is_ignored "$dir"; then
    continue
  fi
  checked=$((checked + 1))
  # Última regla que coincide (misma semántica que GitHub).
  line="$(printf '%s\n' "$rules" | awk -F '\t' -v p="$dir" '$1 == p { last = $0 } END { print last }')"
  if [ -z "$line" ]; then
    echo "FALTA: '$dir/' no tiene regla en $codeowners" >&2
    missing=1
    continue
  fi
  agent="$(printf '%s' "$line" | cut -f2)"
  owners="$(printf '%s' "$line" | cut -f3)"
  if ! printf '%s' "$owners" | grep -Eq '(^|[[:space:]])(@[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)?|[^[:space:]@]+@[^[:space:]@]+)([[:space:]]|$)'; then
    echo "FALTA: la regla de '$dir/' no tiene dueño válido" >&2
    missing=1
    continue
  fi
  if [ -z "$agent" ]; then
    echo "FALTA: la regla de '$dir/' no está bajo un comentario '# Agente: <$valid_agents>'" >&2
    missing=1
    continue
  fi
  [ "$verbose" -eq 1 ] && printf '%-28s %-6s %s\n' "$dir/" "$agent" "$owners"
done

if [ "$missing" -ne 0 ]; then
  echo "check-codeowners: KO — añade las reglas que faltan a $codeowners (ver docs/backlog/team.md §2)" >&2
  exit 1
fi
echo "check-codeowners: OK — ${checked} carpetas con dueño en $codeowners"
