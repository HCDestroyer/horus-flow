#!/usr/bin/env bash
# build-release.sh — artefactos de una release de Horus Flow (historia I1-22).
#
#   scripts/release/build-release.sh --version 1.2.3 --out dist [--arch amd64,arm64] \
#       [--registry ghcr.io/hcdestroyer] [--source registry|local] [--no-bundle] [--notes-url URL]
#
# Produce en --out:
#   horus-<v>-installer.tar.gz       árbol de la versión (compose, configuración, scripts, docs,
#                                    VERSION, images.lock, SHA256SUMS) para --image-source ghcr
#   horus-<v>-linux-<arch>.tar.gz    lo mismo + images/horus-images.tar (docker save de TODAS las
#                                    imágenes del compose, fijadas por digest) para servidores sin
#                                    salida a GHCR (--image-source bundle:…)
#   bootstrap-debian.sh              el comando único de docs/install-debian.md
#   latest.json                      manifiesto de versiones (canal stable o beta) para espejos
#   SHA256SUMS                       sumas de todo lo anterior (el workflow lo firma con cosign)
#
# --source registry (CI): las imágenes propias ya están en el registro (<registry>/horus,
#   horus-web, horus-postgres con la etiqueta <v>); se fijan por el digest del índice multiarch.
# --source local (pruebas): imágenes locales HORUS_IMAGE, HORUS_WEB_IMAGE, HORUS_POSTGRES_IMAGE
#   (variables de entorno) y las de terceros ya presentes; el digest es el ID local.
#
# Las imágenes de terceros salen de compose.prod.yaml (${HORUS_X_IMAGE:-ref@sha256:…}).

set -euo pipefail

repo_root="$(cd "$(dirname "$(readlink -f "$0")")/../.." && pwd)"
version="" out="" archs="amd64,arm64" registry="ghcr.io/hcdestroyer" source=registry bundle=1 notes_url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --version) version="${2#v}"; shift 2 ;;
    --out) out="$2"; shift 2 ;;
    --arch) archs="$2"; shift 2 ;;
    --registry) registry="$2"; shift 2 ;;
    --source) source="$2"; shift 2 ;;
    --no-bundle) bundle=0; shift ;;
    --notes-url) notes_url="$2"; shift 2 ;;
    -h | --help) sed -n '2,24p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "build-release.sh: opción desconocida $1" >&2; exit 2 ;;
  esac
done
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || { echo "build-release.sh: --version X.Y.Z[-pre] obligatorio" >&2; exit 2; }
[ -n "$out" ] || { echo "build-release.sh: --out obligatorio" >&2; exit 2; }
mkdir -p "$out"; out="$(readlink -f "$out")"
log() { printf 'build-release: %s\n' "$*" >&2; }

# Lista de archivos del árbol: la misma que copia install.sh (release_paths).
mapfile -t paths < <(sed -n '/^release_paths=(/,/)$/p' "$repo_root/scripts/install.sh" | tr -s ' ()' '\n' | grep -E '^[A-Za-z]' | grep -v '^release_paths=' | grep -v '^VERSION$' | grep -v '^images.lock$')
[ "${#paths[@]}" -gt 5 ] || { echo "build-release.sh: no pude leer release_paths de install.sh" >&2; exit 1; }

compose="$repo_root/deployments/compose/compose.prod.yaml"
third_party() { # VARIABLE ref@digest de las imágenes de terceros del compose
  grep -oE '\$\{HORUS_[A-Z]+_IMAGE:-[^}]+@sha256:[0-9a-f]{64}\}' "$compose" | sed -E 's/^\$\{([A-Z_]+):-(.+)\}$/\1 \2/' | sort -u
}

# --- images.lock -----------------------------------------------------------------------------------
lock="$out/images.lock"
{
  echo "# images.lock de Horus Flow $version: VARIABLE ETIQUETA DIGEST (install.sh --image-source ghcr|bundle)."
  for pair in "HORUS_IMAGE horus" "HORUS_WEB_IMAGE horus-web" "HORUS_POSTGRES_IMAGE horus-postgres"; do
    read -r var name <<<"$pair"; set -- "$var" "$name"
    if [ "$source" = registry ]; then
      ref="$registry/$2:$version"
      dig="$(docker buildx imagetools inspect "$ref" --format '{{json .Manifest}}' | jq -r .digest)"
      [ -n "$dig" ] && [ "$dig" != null ] || { echo "build-release.sh: sin digest para $ref" >&2; exit 1; }
    else
      ref="${!1:?falta la variable $1 (imagen local)}"
      dig="$(docker image inspect "$ref" --format '{{.Id}}')"
    fi
    printf '%s %s %s\n' "$1" "$ref" "$dig"
  done
  third_party | while read -r var ref; do printf '%s %s %s\n' "$var" "${ref%@*}" "${ref#*@}"; done
} >"$lock"
log "images.lock: $(grep -c '^HORUS' "$lock") imágenes"

# --- Árbol ----------------------------------------------------------------------------------------
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
tree="$stage/horus-$version"
mkdir -p "$tree"
for p in "${paths[@]}"; do
  [ -e "$repo_root/$p" ] || { echo "build-release.sh: falta $p" >&2; exit 1; }
  mkdir -p "$tree/$(dirname "$p")"
  cp -p "$repo_root/$p" "$tree/$p"
done
printf '%s\n' "$version" >"$tree/VERSION"
cp "$lock" "$tree/images.lock"
sums_tree() { (cd "$tree" && find . -type f ! -name SHA256SUMS -printf '%P\n' | LC_ALL=C sort | xargs sha256sum) >"$tree/SHA256SUMS"; }
sums_tree
tar -C "$stage" -czf "$out/horus-$version-installer.tar.gz" "horus-$version"
log "horus-$version-installer.tar.gz"

# --- Paquetes offline por arquitectura -------------------------------------------------------------
if [ "$bundle" = 1 ]; then
  save_platform=()
  # Con el almacén de imágenes de containerd, save necesita --platform (guarda solo esa arquitectura).
  docker info -f '{{json .DriverStatus}}' 2>/dev/null | grep -q 'io.containerd.snapshotter' && save_platform=(--platform)
  IFS=, read -r -a arch_list <<<"$archs"
  for arch in "${arch_list[@]}"; do
    mkdir -p "$tree/images"
    tags=()
    while read -r var ref dig; do
      [[ "$var" =~ ^HORUS ]] || continue
      if [ "$source" = registry ] || [[ "$var" != HORUS_IMAGE && "$var" != HORUS_WEB_IMAGE && "$var" != HORUS_POSTGRES_IMAGE ]]; then
        if [ "$source" = local ] && docker image inspect "$ref" >/dev/null 2>&1; then
          :
        else
          docker pull -q --platform "linux/$arch" "$ref@$dig" >/dev/null
          docker tag "$ref@$dig" "$ref"
        fi
      fi
      tags+=("$ref")
    done <"$lock"
    log "docker save ($arch): ${#tags[@]} imágenes"
    if [ "${#save_platform[@]}" -gt 0 ] && [ "$source" = registry ]; then
      docker save --platform "linux/$arch" -o "$tree/images/horus-images.tar" "${tags[@]}"
    else
      docker save -o "$tree/images/horus-images.tar" "${tags[@]}"
    fi
    sums_tree
    tar -C "$stage" -czf "$out/horus-$version-linux-$arch.tar.gz" "horus-$version"
    rm -rf "$tree/images"
    sums_tree
    log "horus-$version-linux-$arch.tar.gz"
  done
fi

# --- bootstrap, latest.json y SHA256SUMS -----------------------------------------------------------
install -m 0755 "$repo_root/scripts/bootstrap-debian.sh" "$out/bootstrap-debian.sh"
channel=stable; [ "$version" = "${version%%-*}" ] || channel=beta
jq -n --arg v "$version" --arg ch "$channel" --arg at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg notes "${notes_url:-https://github.com/hcdestroyer/horus-flow/releases/tag/v$version}" \
  '{schema: 1, channels: {($ch): {version: $v, published_at: $at, notes_url: $notes}}}' >"$out/latest.json"
rm -f "$out/images.lock"
(cd "$out" && find . -maxdepth 1 -type f ! -name 'SHA256SUMS*' ! -name '*.verified' -printf '%P\n' | LC_ALL=C sort | xargs sha256sum) >"$out/SHA256SUMS"
log "listo en $out"
ls -la "$out" >&2
