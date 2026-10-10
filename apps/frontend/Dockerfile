# syntax=docker/dockerfile:1
#
# Imagen `horus-web`: la SPA de Horus Flow (salida estática de `nuxt generate`) servida por un
# servidor Node mínimo, sin root y con el sistema de archivos de solo lectura. Traefik enruta
# aquí todo lo que no es `/api` ni `/ws` (que van a horus-app).
#
#   docker build -f apps/frontend/Dockerfile -t horus-web:dev .
#
# Se construye desde la raíz del repositorio porque `pnpm api:generate` y el build leen los
# contratos de packages/schemas. Sin API simulada (HORUS_UI_MOCKS vacío) y con las secciones
# del incremento entregado (NUXT_PUBLIC_NAV_INCREMENT, por defecto I1).
# Las imágenes base van fijadas por versión; PLAT debe fijar además su digest al publicarlas
# (como en el Dockerfile del binario).

ARG NODE_IMAGE=node:22.22.0-bookworm-slim
ARG RUNTIME_IMAGE=gcr.io/distroless/nodejs22-debian12:nonroot

# ---------------------------------------------------------------------------------------------
FROM ${NODE_IMAGE} AS build
WORKDIR /src
RUN corepack enable
COPY packages/schemas/ packages/schemas/
COPY apps/frontend/package.json apps/frontend/pnpm-lock.yaml apps/frontend/
WORKDIR /src/apps/frontend
RUN --mount=type=cache,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile --ignore-scripts
COPY apps/frontend/ ./
ARG NUXT_PUBLIC_NAV_INCREMENT=I1
ENV NUXT_PUBLIC_NAV_INCREMENT=${NUXT_PUBLIC_NAV_INCREMENT} HORUS_UI_MOCKS=
RUN pnpm exec nuxt prepare && pnpm build

# ---------------------------------------------------------------------------------------------
FROM ${RUNTIME_IMAGE}
ARG VERSION=dev
LABEL org.opencontainers.image.title="horus-web" \
      org.opencontainers.image.licenses="LicenseRef-Proprietary" \
      org.opencontainers.image.description="Horus Flow: interfaz web (SPA estática)" \
      org.opencontainers.image.version="${VERSION}"
WORKDIR /app
COPY --from=build /src/apps/frontend/scripts/serve-static.mjs ./serve-static.mjs
COPY --from=build /src/apps/frontend/.output/public ./public
ENV HORUS_WEB_ROOT=/app/public HORUS_WEB_PORT=8081
USER nonroot
EXPOSE 8081
CMD ["serve-static.mjs"]
