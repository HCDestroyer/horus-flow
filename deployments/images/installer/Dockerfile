# syntax=docker/dockerfile:1
#
# Imagen horus-installer: instalación de Horus Flow solo con Docker (docs/install-docker.md).
#
# Lleva el árbol de la versión (la misma lista release_paths de scripts/install.sh, con VERSION e
# images.lock) y ejecuta ese install.sh con --runtime docker; también es la imagen del servicio
# horus-host (túnel WireGuard, cortafuegos y backups programados). El contexto NO es el repositorio
# sino el árbol preparado por scripts/release/build-installer-image.sh:
#
#   bash scripts/release/build-installer-image.sh --tag horus-installer:local
#
# Base fijada por versión y digest; cosign fijado por versión y SHA-256 (como bootstrap-debian.sh).
# Paquetes de Alpine sin versión exacta (el repositorio de una rama solo conserva la última): los
# fija la rama 3.22 de la base y la release los congela en la imagen firmada.

ARG BASE_IMAGE=alpine:3.22.6@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8

FROM ${BASE_IMAGE}

ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED=unknown
ARG TARGETARCH=amd64
ARG COSIGN_VERSION=v2.6.1
ARG COSIGN_SHA256_AMD64=064954c5d8c7e3b28188eee5b1727b31c411550bc5fefd41aa672d3c761d103a
ARG COSIGN_SHA256_ARM64=56a16480bdd56ec789abaa65924402f6b92c0041f06885995853c05567b76f34

LABEL org.opencontainers.image.title="horus-installer" \
      org.opencontainers.image.description="Instalador de Horus Flow solo con Docker y contenedor horus-host" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${CREATED}" \
      org.opencontainers.image.source="https://github.com/hcdestroyer/horus-flow" \
      org.opencontainers.image.documentation="https://github.com/hcdestroyer/horus-flow/blob/HEAD/docs/install-docker.md"

SHELL ["/bin/ash", "-eo", "pipefail", "-c"]

# Herramientas que usan install.sh, horus-backup y horus-ctl (GNU coreutils/findutils, flock, ss),
# el cliente de Docker con compose v2, WireGuard (wg y wireguard-go), iptables nft y legacy, ipset
# (conjuntos de los túneles IPsec/GRE, docs/tunnels.md), age y
# supercronic (backups programados en horus-host).
# hadolint ignore=DL3018
RUN apk add --no-cache \
      age bash ca-certificates coreutils curl diffutils docker-cli docker-cli-compose findutils \
      gawk iproute2 ipset iptables iptables-legacy jq openssl procps-ng sed supercronic tar tini \
      tzdata util-linux wireguard-go wireguard-tools-wg \
    && case "${TARGETARCH}" in \
         amd64) sum="${COSIGN_SHA256_AMD64}" ;; \
         arm64) sum="${COSIGN_SHA256_ARM64}" ;; \
         *) echo "arquitectura no soportada: ${TARGETARCH}" >&2; exit 1 ;; \
       esac \
    && curl -fsSL --retry 3 -o /usr/local/bin/cosign \
         "https://github.com/sigstore/cosign/releases/download/${COSIGN_VERSION}/cosign-linux-${TARGETARCH}" \
    && echo "${sum}  /usr/local/bin/cosign" | sha256sum -c - \
    && chmod 0755 /usr/local/bin/cosign

COPY . /usr/share/horus/release

RUN install -m 0755 /usr/share/horus/release/deployments/images/installer/horus-installer.sh /usr/local/bin/horus-installer \
    && install -D -m 0755 /usr/share/horus/release/deployments/images/installer/iptables-auto.sh /usr/local/sbin/iptables \
    && test -s /usr/share/horus/release/VERSION \
    && bash -n /usr/share/horus/release/scripts/install.sh

# Sin USER: crear wg0, poner reglas de iptables y escribir datos de uid 70/101/65532 exige root.
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/horus-installer"]
CMD ["help"]
