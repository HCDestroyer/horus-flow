# syntax=docker/dockerfile:1
#
# Imagen única `horus` (ADR-0025, docs/conventions.md §9): un binario, todos los roles.
# Qué ejecuta cada contenedor lo decide HORUS_ROLES (horus-app, horus-collector,
# horus-wg-agent salen de esta misma imagen).
#
#   docker build -t horus:dev --build-arg VERSION=$(git describe --tags --always) .
#
# Imágenes base fijadas por versión y digest. El toolchain es una versión de Go con soporte
# (go.mod declara go 1.26 con toolchain go1.26.8): igual que GO_VERSION en CI.

ARG GO_IMAGE=golang:1.26.8-bookworm@sha256:dc9ad6c05acc7a88e5b71bde60a5fe3bd4b9f0db209011711b464107438a8107
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

# ---------------------------------------------------------------------------------------------
FROM ${GO_IMAGE} AS build

WORKDIR /src

# Dependencias primero (capa cacheable); go.sum aún puede no existir (sin dependencias).
COPY go.mod go.su[m] ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY services/ services/
COPY packages/ packages/

ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/horus ./services/cmd/horus \
    && mkdir -p /out/var/lib/horus

# ---------------------------------------------------------------------------------------------
FROM ${RUNTIME_IMAGE}

ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED=unknown
LABEL org.opencontainers.image.title="horus" \
      org.opencontainers.image.licenses="LicenseRef-Proprietary" \
      org.opencontainers.image.description="Horus Flow: binario modular con roles (HORUS_ROLES)" \
      org.opencontainers.image.source="https://github.com/hcdestroyer/horus-flow" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${CREATED}"

COPY --from=build /out/horus /horus
# Almacén local (HORUS_DATA_DIR, ADR-0019): el volumen nombrado hereda este propietario.
COPY --from=build --chown=65532:65532 /out/var/lib/horus /var/lib/horus

ENV HORUS_DATA_DIR=/var/lib/horus
USER 65532:65532
# HTTP (gateway), gRPC interno, administración (/healthz, /readyz, /metrics), colector UDP.
EXPOSE 8080 9090 8081 4739/udp 2055/udp
ENTRYPOINT ["/horus"]
