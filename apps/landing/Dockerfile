# syntax=docker/dockerfile:1
#
# Imagen `horus-landing`: la landing comercial de Horus Flow (Nuxt 4 SSR + rutas Nitro de los
# formularios). Servidor Node sin root, sistema de archivos de solo lectura posible (no escribe
# nada salvo con MAIL_TRANSPORT=file).
#
#   docker build -t horus-landing:dev apps/landing
#
# El contexto es apps/landing (autocontenido). Variables de entorno: README.md › Variables.

ARG NODE_IMAGE=node:22.22.0-bookworm-slim
ARG RUNTIME_IMAGE=gcr.io/distroless/nodejs22-debian12:nonroot

# ---------------------------------------------------------------------------------------------
FROM ${NODE_IMAGE} AS build
WORKDIR /src
RUN corepack enable
COPY package.json pnpm-lock.yaml .npmrc ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile --ignore-scripts
COPY . .
ENV NODE_ENV=production
RUN pnpm exec nuxt prepare && pnpm build

# ---------------------------------------------------------------------------------------------
FROM ${RUNTIME_IMAGE}
ARG VERSION=dev
LABEL org.opencontainers.image.title="horus-landing" \
      org.opencontainers.image.licenses="LicenseRef-Proprietary" \
      org.opencontainers.image.description="Horus Flow: landing comercial" \
      org.opencontainers.image.vendor="Connection And Solutions Company, S.A. (C&S Company)" \
      org.opencontainers.image.version="${VERSION}"
WORKDIR /app
COPY --from=build --chown=nonroot:nonroot /src/.output ./.output
ENV NODE_ENV=production HOST=0.0.0.0 PORT=3000
USER nonroot
EXPOSE 3000
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/nodejs/bin/node", "-e", "fetch('http://127.0.0.1:3000/robots.txt').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"]
CMD [".output/server/index.mjs"]
