#!/usr/bin/env bash
# image.sh — construye la imagen única `horus` para la aceptación (historia I0-19).
#
# Modo (ACCEPT_IMAGE_MODE):
#   auto        (defecto) `docker build` del Dockerfile raíz; si falla, imagen equivalente
#               desde un binario compilado en el host (tests/acceptance/i0/Dockerfile.prebuilt)
#   dockerfile  solo el Dockerfile raíz (falla si no construye)
#   prebuilt    solo la imagen desde el binario del host
#
# ¿Por qué el modo prebuilt? En entornos con un proxy HTTPS que reemplaza certificados (sandboxes
# de agentes, redes corporativas) el `go mod download` dentro del contenedor de build no conoce
# la CA del proxy y falla con "x509: certificate signed by unknown authority". El host sí la
# conoce, así que se compila allí con los mismos flags (CGO_ENABLED=0, -trimpath, -s -w) y se
# copia el binario sobre la MISMA base runtime fijada por digest del Dockerfile raíz.
#
# Variables: ACCEPT_IMAGE (horus:accept), ACCEPT_IMAGE_MODE, VERSION, GO, ACCEPT_NOTE_FILE
# (si existe, aquí se deja una nota para el resumen de accept-i0.sh).

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$repo_root"

image="${ACCEPT_IMAGE:-horus:accept}"
mode="${ACCEPT_IMAGE_MODE:-auto}"
version="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
revision="$(git rev-parse HEAD 2>/dev/null || echo unknown)"
go_bin="${GO:-go}"
work="$repo_root/bin/accept-i0/image"
mkdir -p "$work"

note() {
  echo "image: $*"
  if [ -n "${ACCEPT_NOTE_FILE:-}" ]; then printf '%s' "$*" >"$ACCEPT_NOTE_FILE"; fi
}

build_dockerfile() {
  echo "image: docker build -t $image (Dockerfile raíz)"
  docker build -t "$image" --build-arg "VERSION=$version" --build-arg "REVISION=$revision" \
    --build-arg "CREATED=$(date -u +%Y-%m-%dT%H:%M:%SZ)" . 2>&1 | tee "$work/docker-build.log"
}

build_prebuilt() {
  local ctx="$work/context" runtime
  rm -rf "$ctx"
  mkdir -p "$ctx/var/lib/horus"
  echo "image: compilando el binario en el host (CGO_ENABLED=0, linux/amd64)"
  CGO_ENABLED=0 GOOS=linux GOARCH="${GOARCH:-amd64}" "$go_bin" build -trimpath \
    -ldflags "-s -w -X main.version=$version" -o "$ctx/horus" ./services/cmd/horus
  # Misma base runtime que el Dockerfile raíz (fuente única: su ARG RUNTIME_IMAGE).
  runtime="$(sed -nE 's/^ARG RUNTIME_IMAGE=(.+)$/\1/p' Dockerfile | head -1)"
  if [ -z "$runtime" ]; then
    echo "image: no encuentro ARG RUNTIME_IMAGE en el Dockerfile raíz" >&2
    return 1
  fi
  echo "image: docker build -t $image (prebuilt sobre $runtime)"
  docker build -t "$image" -f tests/acceptance/i0/Dockerfile.prebuilt \
    --build-arg "RUNTIME_IMAGE=$runtime" --build-arg "VERSION=$version" --build-arg "REVISION=$revision" "$ctx"
}

case "$mode" in
  dockerfile)
    build_dockerfile
    note "Dockerfile raíz"
    ;;
  prebuilt)
    build_prebuilt
    note "prebuilt (ACCEPT_IMAGE_MODE=prebuilt)"
    ;;
  auto)
    if build_dockerfile; then
      note "Dockerfile raíz"
    else
      reason="docker build falló"
      if grep -q 'x509: certificate signed by unknown authority' "$work/docker-build.log"; then
        reason="docker build sin CA del proxy (x509)"
      fi
      echo "image: AVISO — $reason; se usa la imagen equivalente desde el binario del host" >&2
      build_prebuilt
      note "prebuilt: $reason"
    fi
    ;;
  *)
    echo "image: ACCEPT_IMAGE_MODE desconocido: $mode (auto|dockerfile|prebuilt)" >&2
    exit 2
    ;;
esac

# Comprobaciones mínimas de la imagen (las mismas que el job `build` de CI).
user="$(docker image inspect "$image" --format '{{.Config.User}}')"
if [ "$user" != "65532:65532" ]; then
  echo "image: KO — la imagen corre como '$user', se esperaba 65532:65532" >&2
  exit 1
fi
docker run --rm "$image" --version
echo "image: OK — $image"

# Imagen horus-web (opcional): ACCEPT_WEB_IMAGE. Mismo criterio de modo que `horus`: el Dockerfile
# del frontend o, si no puede descargar dependencias (CA del proxy), la SPA generada en el host
# sobre la misma base runtime (tests/install/debian/web-prebuilt.Dockerfile).
[ -n "${ACCEPT_WEB_IMAGE:-}" ] || exit 0
web="$ACCEPT_WEB_IMAGE"
build_web_dockerfile() {
  docker build -t "$web" --build-arg "VERSION=$version" -f apps/frontend/Dockerfile . >"$work/web-build.log" 2>&1
}
build_web_prebuilt() {
  local ctx="$work/web-context" runtime
  echo "image: generando la SPA en el host (pnpm build, sin API simulada)"
  (cd apps/frontend && pnpm install --frozen-lockfile >/dev/null && pnpm build >"$work/web-generate.log" 2>&1)
  runtime="$(sed -nE 's/^ARG RUNTIME_IMAGE=(.+)$/\1/p' apps/frontend/Dockerfile | head -1)"
  rm -rf "$ctx"; mkdir -p "$ctx"
  cp apps/frontend/scripts/serve-static.mjs "$ctx/"; cp -r apps/frontend/.output/public "$ctx/public"
  docker build -t "$web" -f tests/install/debian/web-prebuilt.Dockerfile ${runtime:+--build-arg "RUNTIME_IMAGE=$runtime"} \
    --build-arg "VERSION=$version" "$ctx" >"$work/web-build.log" 2>&1
}
case "$mode" in
  dockerfile) build_web_dockerfile ;;
  prebuilt) build_web_prebuilt ;;
  *) build_web_dockerfile || { echo "image: AVISO — docker build de horus-web falló; SPA generada en el host" >&2; build_web_prebuilt; } ;;
esac
echo "image: OK — $web"
