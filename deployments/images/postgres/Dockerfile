# syntax=docker/dockerfile:1
#
# horus-postgres — PostgreSQL 18.6 (la misma imagen del compose de desarrollo) + pgBackRest
# (historia I1-23; docs/disaster-recovery.md §3.1, docs/storage.md §2.3).
#
# pgBackRest tiene que vivir en el contenedor de PostgreSQL: `archive_command` (WAL continuo) y
# `pgbackrest backup` leen PGDATA y el socket local. No se instala con apk (la build no depende
# de los mirrors de Alpine): se copia el binario de la imagen woblerr/pgbackrest, también Alpine
# y fijada por digest, con las dos bibliotecas que la base de PostgreSQL no trae (libbz2, libssh2).
# El resto (libpq, OpenSSL, lz4, zstd, libxml2, zlib, liblzma) ya está en la base.
#
#   docker build -t horus-postgres:18.6-pgbackrest2.59.3 deployments/images/postgres
#
# La construyen scripts/install.sh (I1-22) y `make test-backup` (I1-23).

ARG PGBACKREST_IMAGE=woblerr/pgbackrest:2.59.3-alpine@sha256:c1dd7f89fefbbeb1838624ff57375145f7b228d3881e8315e7b8708ef2d98936
ARG POSTGRES_IMAGE=postgres:18.6-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873

FROM ${PGBACKREST_IMAGE} AS pgbackrest

FROM ${POSTGRES_IMAGE}
COPY --from=pgbackrest /usr/bin/pgbackrest /usr/bin/pgbackrest
COPY --from=pgbackrest /usr/lib/libbz2.so.1* /usr/lib/libssh2.so.1* /usr/lib/
SHELL ["/bin/ash", "-eo", "pipefail", "-c"]
# Falla la build si falta alguna biblioteca. Directorios de log/spool/tmp del usuario postgres.
RUN pgbackrest version \
    && ! ldd /usr/bin/pgbackrest | grep -q 'not found' \
    && install -d -o postgres -g postgres -m 0750 /var/log/pgbackrest /var/spool/pgbackrest /tmp/pgbackrest
# Sin root: el instalador crea el directorio de datos con el uid de postgres (70) y el entrypoint
# oficial funciona igual sin privilegios. La restauración de prueba (horus-backup verify) pasa
# --user 0:0 explícitamente para preparar su directorio efímero.
USER 70:70
LABEL org.opencontainers.image.title="horus-postgres" \
      org.opencontainers.image.licenses="PostgreSQL AND MIT" \
      org.opencontainers.image.description="PostgreSQL 18.6 con pgBackRest 2.59.3 (Horus Flow, I1-23)" \
      org.opencontainers.image.source="https://github.com/hcdestroyer/horus-flow"
