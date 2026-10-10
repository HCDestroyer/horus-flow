# syntax=docker/dockerfile:1
# horus-web EQUIVALENTE a apps/frontend/Dockerfile montada con la SPA ya generada en el host
# (`pnpm build` en apps/frontend). La usa `make test-install-debian` cuando `docker build` del
# Dockerfile del frontend no puede descargar dependencias (CA de un proxy). Misma base runtime,
# mismo servidor estático, mismo usuario y puerto.
# Contexto: un directorio con serve-static.mjs y public/.
ARG RUNTIME_IMAGE=gcr.io/distroless/nodejs22-debian12:nonroot
FROM ${RUNTIME_IMAGE}
ARG VERSION=dev
LABEL org.opencontainers.image.title="horus-web" \
      org.opencontainers.image.description="Horus Flow: interfaz web (SPA estática) — build prebuilt de prueba" \
      org.opencontainers.image.version="${VERSION}"
WORKDIR /app
COPY serve-static.mjs ./serve-static.mjs
COPY public ./public
ENV HORUS_WEB_ROOT=/app/public HORUS_WEB_PORT=8081
USER nonroot
EXPOSE 8081
CMD ["serve-static.mjs"]
