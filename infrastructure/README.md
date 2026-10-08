# infrastructure/ — configuración de infraestructura

- **Propósito:** configuración versionada de los componentes de infraestructura (PostgreSQL,
  ClickHouse, NATS, Valkey, Traefik, observabilidad).
- **Dueño:** PLAT. Las migraciones de ClickHouse (FLOW, I0-13) viven en
  `services/ingester/migrations/clickhouse/` porque el binario las embebe; esa ruta es
  sensible en producción ([`docs/conventions.md`](../docs/conventions.md) §6.2).
- **Documentación:** [`docs/architecture.md`](../docs/architecture.md),
  [`docs/database.md`](../docs/database.md), [`docs/observability.md`](../docs/observability.md).

| Ruta | Qué contiene | Historia |
| --- | --- | --- |
| `nats/nats.dev.conf` | NATS con JetStream para el compose de desarrollo (sin autenticación; monitorización en `:8222`) | I0-02 |
| `traefik/traefik.dev.yml`, `traefik/dynamic/` | Traefik de desarrollo: HTTP `:8000`, ping/dashboard `:8082`, proveedor de archivos (sin socket de Docker) | I0-02 |
