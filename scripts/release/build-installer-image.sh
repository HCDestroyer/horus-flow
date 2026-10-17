#!/usr/bin/env bash
# build-installer-image.sh — imagen horus-installer (instalación solo con Docker, docs/install-docker.md).
#
#   scripts/release/build-installer-image.sh [--tag horus-installer:local] [--version X.Y.Z]
#       [--lock images.lock] [--stage DIR] [-- ARGUMENTOS EXTRA DE docker build]
#
# Prepara el contexto con el árbol de la versión (la lista release_paths de scripts/install.sh, la
# misma que el paquete de release), VERSION e images.lock (si se da; la release lo pasa con los
# digests de horus, horus-web, horus-postgres y las imágenes de terceros) y construye
# deployments/images/installer/Dockerfile.
#
#   --stage DIR   solo prepara el contexto en DIR (vacío o inexistente) y no construye: la release lo
#                 construye con docker buildx (multiarch, push, firma cosign).
#
# Ejemplos: make installer-image (local); en CI release.yml usa --stage.
set -euo pipefail

repo_root="$(cd "$(dirname "$(readlink -f "$0")")/../.." && pwd)"
tag=horus-installer:local version="" lock="" stage=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --tag) tag="$2"; shift 2 ;;
    --version) version="${2#v}"; shift 2 ;;
    --lock) lock="$2"; shift 2 ;;
    --stage) stage="$2"; shift 2 ;;
    --) shift; break ;;
    -h | --help) sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "build-installer-image.sh: opción desconocida $1" >&2; exit 2 ;;
  esac
done
if [ -z "$version" ]; then
  if [ -s "$repo_root/VERSION" ]; then version="$(tr -d ' \r\n' <"$repo_root/VERSION")"; else version=0.0.0-dev; fi
fi
[ -z "$lock" ] || [ -f "$lock" ] || { echo "build-installer-image.sh: no existe $lock" >&2; exit 2; }

# Lista de archivos: la misma que copia install.sh (release_paths) y empaqueta build-release.sh.
mapfile -t paths < <(sed -n '/^release_paths=(/,/)$/p' "$repo_root/scripts/install.sh" | tr -s ' ()' '\n' | grep -E '^[A-Za-z]' | grep -v '^release_paths=' | grep -v '^VERSION$' | grep -v '^images.lock$')
[ "${#paths[@]}" -gt 5 ] || { echo "build-installer-image.sh: no pude leer release_paths de install.sh" >&2; exit 1; }

only_stage=1
if [ -z "$stage" ]; then
  only_stage=0
  stage="$(mktemp -d)"
  trap 'rm -rf "$stage"' EXIT
else
  [ ! -e "$stage" ] || [ -z "$(ls -A "$stage")" ] || { echo "build-installer-image.sh: $stage no está vacío" >&2; exit 2; }
  mkdir -p "$stage"
fi
for p in "${paths[@]}"; do
  [ -e "$repo_root/$p" ] || { echo "build-installer-image.sh: falta $p" >&2; exit 1; }
  mkdir -p "$stage/$(dirname "$p")"
  cp -p "$repo_root/$p" "$stage/$p"
done
printf '%s\n' "$version" >"$stage/VERSION"
[ -z "$lock" ] || cp "$lock" "$stage/images.lock"

if [ "$only_stage" = 1 ]; then
  echo "build-installer-image: contexto preparado en $stage (versión $version)" >&2
  exit 0
fi
docker build -t "$tag" -f "$stage/deployments/images/installer/Dockerfile" \
  --build-arg "VERSION=$version" --build-arg "REVISION=$(git -C "$repo_root" rev-parse HEAD 2>/dev/null || echo unknown)" \
  --build-arg "CREATED=$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$@" "$stage"
echo "build-installer-image: $tag ($version)" >&2
