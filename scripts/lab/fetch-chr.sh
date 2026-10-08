#!/usr/bin/env bash
# fetch-chr.sh — descarga y VERIFICA la imagen MikroTik CHR (historia I0-11, criterio 4).
#
# Deja la imagen raw descomprimida en $CHR_CACHE_DIR/chr-<ROS>.img (fuera del repo: la imagen
# nunca entra en Git) e imprime su ruta en la última línea de stdout. Reutiliza la caché si el
# zip ya verificado está presente.
#
# Checksum (infrastructure/lab/chr/checksums.sha256 explica el orden): línea fijada en Git >
# checksum publicado por MikroTik junto a la imagen > CHR_SHA256 del entorno. Sin ninguno: falla.
#
# Uso: scripts/lab/fetch-chr.sh [versión]     (por defecto ROS de lab.env)

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
# shellcheck source=../../infrastructure/lab/chr/lab.env
source "$repo_root/infrastructure/lab/chr/lab.env"
ROS="${1:-$ROS}"
pinned_file="$repo_root/infrastructure/lab/chr/checksums.sha256"

log() { echo "fetch-chr: $*" >&2; }
die() { log "ERROR — $*"; exit 1; }

case "$ROS" in
  7.*) ;;
  *) die "versión '$ROS' no soportada: solo RouterOS 7.x (D15, mínimo 7.12)" ;;
esac
major_minor="${ROS#7.}"; minor="${major_minor%%.*}"
if ! [[ "$minor" =~ ^[0-9]+$ ]] || [ "$minor" -lt 12 ]; then
  die "versión '$ROS' por debajo del mínimo 7.12 (D15)"
fi

zip_name="chr-$ROS.img.zip"
img_name="chr-$ROS.img"
url="$CHR_BASE_URL/$ROS/$zip_name"
mkdir -p "$CHR_CACHE_DIR"
zip_path="$CHR_CACHE_DIR/$zip_name"
img_path="$CHR_CACHE_DIR/$img_name"

sha256_of() { sha256sum "$1" | cut -d' ' -f1; }

# 1) Checksum fijado en Git.
expected="$(awk -v f="$zip_name" '!/^#/ && $2 == f { print tolower($1) }' "$pinned_file" | tail -n1)"
source_desc="infrastructure/lab/chr/checksums.sha256"

# 2) Checksum publicado por MikroTik junto a la imagen (ruta a verificar en la primera ejecución).
if [ -z "$expected" ]; then
  for sums in "$CHR_BASE_URL/$ROS/SHA256SUMS" "$CHR_BASE_URL/$ROS/$zip_name.sha256"; do
    if body="$(curl -fsSL --max-time 30 "$sums" 2>/dev/null)"; then
      expected="$(printf '%s\n' "$body" | awk -v f="$zip_name" '
        { for (i = 1; i <= NF; i++) if ($i ~ /^[0-9a-fA-F]{64}$/) h = $i }
        index($0, f) || NF == 1 { if (h) { print tolower(h); exit } }')"
      if [ -n "$expected" ]; then source_desc="$sums"; break; fi
    fi
  done
fi

# 3) Valor copiado a mano de mikrotik.com/download.
if [ -z "$expected" ] && [ -n "${CHR_SHA256:-}" ]; then
  expected="$(printf '%s' "$CHR_SHA256" | tr 'A-F' 'a-f')"
  source_desc="CHR_SHA256 (entorno)"
fi

if [ -z "$expected" ]; then
  die "no hay checksum para $zip_name. Cópialo de https://mikrotik.com/download (Cloud Hosted Router,
  Raw disk image, botón SHA256) y vuelve a ejecutar con CHR_SHA256=<hash>, o añádelo a
  infrastructure/lab/chr/checksums.sha256. Nunca se arranca una imagen sin verificar."
fi
[[ "$expected" =~ ^[0-9a-f]{64}$ ]] || die "checksum mal formado en $source_desc"

if [ -f "$zip_path" ] && [ "$(sha256_of "$zip_path")" = "$expected" ]; then
  log "usando $zip_path de la caché (SHA-256 verificado contra $source_desc)"
else
  log "descargando $url"
  tmp="$zip_path.part"
  if ! curl -fSL --retry 3 --max-time 600 -o "$tmp" "$url"; then
    rm -f "$tmp"
    die "no se pudo descargar $url (¿sin acceso a download.mikrotik.com? Descárgalo a mano en
  $zip_path y vuelve a ejecutar: se verificará igual)"
  fi
  got="$(sha256_of "$tmp")"
  if [ "$got" != "$expected" ]; then
    rm -f "$tmp"
    die "SHA-256 NO coincide para $zip_name (esperado $expected según $source_desc, obtenido $got)"
  fi
  mv "$tmp" "$zip_path"
  log "SHA-256 verificado contra $source_desc"
fi

if [ ! -f "$img_path" ] || [ "$zip_path" -nt "$img_path" ]; then
  command -v unzip >/dev/null 2>&1 || die "falta 'unzip'"
  unzip -p "$zip_path" "$img_name" >"$img_path.part" || die "el zip no contiene $img_name"
  mv "$img_path.part" "$img_path"
fi
echo "$img_path"
