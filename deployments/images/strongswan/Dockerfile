# syntax=docker/dockerfile:1
#
# Imagen horus-ipsec: demonio IKEv2 de strongSwan (charon) del hub de túneles IPsec (docs/tunnels.md,
# ADR-0031). Solo el demonio: sin configuración de conexiones ni secretos en la imagen ni en disco.
# tunnel-agent (contenedor aparte, con el certificado gRPC) le carga cada conexión y su PSK en memoria
# por el socket vici compartido (/run/horus-ipsec/charon.vici). Este contenedor es el único expuesto a
# Internet en UDP 500/4500 y no tiene acceso a PostgreSQL, a la KEK ni a los certificados gRPC.
#
#   docker build -t horus-ipsec:local deployments/images/strongswan
#
# Base fijada por versión y digest (la misma que horus-installer). Paquete de Alpine sin versión exacta
# (el repositorio de una rama solo conserva la última): lo fija la rama 3.22 y la release lo congela.

ARG BASE_IMAGE=alpine:3.22.6@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8

FROM ${BASE_IMAGE}

ARG VERSION=dev
ARG REVISION=unknown

LABEL org.opencontainers.image.title="horus-ipsec" \
      org.opencontainers.image.description="Demonio IKEv2 (strongSwan charon) del hub de túneles IPsec de Horus Flow" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.source="https://github.com/hcdestroyer/horus-flow" \
      org.opencontainers.image.documentation="https://github.com/hcdestroyer/horus-flow/blob/main/docs/tunnels.md" \
      org.opencontainers.image.licenses="GPL-2.0-or-later"

SHELL ["/bin/ash", "-eo", "pipefail", "-c"]

# Grupo fijo del socket vici (GID 1430): tunnel-agent lo recibe con group_add y es el único que puede
# hablar con charon. charon abre UDP 500/4500 como root y cambia al usuario ipsec del paquete
# conservando solo CAP_NET_ADMIN (SA y políticas XFRM).
# hadolint ignore=DL3018
RUN apk add --no-cache strongswan tini \
    && addgroup -S -g 1430 horusvici \
    && addgroup ipsec horusvici \
    && rm -f /etc/swanctl/conf.d/* /etc/ipsec.conf /etc/ipsec.secrets \
    && test -x /usr/lib/strongswan/charon && test -e /etc/strongswan.d/charon/vici.conf

COPY charon-horus.conf /etc/strongswan.d/charon-horus.conf
COPY --chmod=0755 entrypoint.sh /usr/local/bin/horus-ipsec

# El socket vici vive en un volumen compartido con tunnel-agent (tmpfs del anfitrión).
VOLUME ["/run/horus-ipsec"]
EXPOSE 500/udp 4500/udp

HEALTHCHECK --interval=15s --timeout=5s --start-period=10s --retries=3 CMD ["/bin/sh", "-c", "test -S /run/horus-ipsec/charon.vici && pgrep -x charon >/dev/null"]

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/horus-ipsec"]
