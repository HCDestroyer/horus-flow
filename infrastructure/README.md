# infrastructure/ — configuración de infraestructura

- **Propósito:** configuración versionada de los componentes de infraestructura (PostgreSQL,
  ClickHouse, NATS, Valkey, Traefik, observabilidad).
- **Dueño:** PLAT; `infrastructure/clickhouse/` es de FLOW (esquema y migraciones, I0-13). Ruta
  sensible en producción ([`docs/conventions.md`](../docs/conventions.md) §6.2).
- **Documentación:** [`docs/architecture.md`](../docs/architecture.md),
  [`docs/database.md`](../docs/database.md), [`docs/observability.md`](../docs/observability.md).

| Ruta | Qué contiene | Historia |
| --- | --- | --- |
| `nats/nats.dev.conf` | NATS con JetStream para el compose de desarrollo (sin autenticación; monitorización en `:8222`) | I0-02 |
| `traefik/traefik.dev.yml`, `traefik/dynamic/` | Traefik de desarrollo: HTTP `:8000`, ping/dashboard `:8082`, proveedor de archivos (sin socket de Docker) | I0-02 |
| `observability/prometheus/`, `observability/loki/`, `observability/alloy/` | Prometheus (raspa `:8081/metrics` de `horus-*`, Traefik y la propia pila), Loki monolítico y Grafana Alloy (logs de los contenedores del proyecto compose con redacción de respaldo) del perfil `observability` | I0-18 |
| `observability/grafana/` | Datasources y dashboard "Horus · Platform Overview" (filas Servicios, API, Ingesta y Logs) provisionados como código | I0-18 |
