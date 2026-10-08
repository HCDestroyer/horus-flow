# infrastructure/ — configuración de infraestructura

- **Propósito:** configuración versionada de los componentes de infraestructura (PostgreSQL,
  ClickHouse, NATS, Valkey, Traefik, observabilidad).
- **Dueño:** PLAT; `infrastructure/clickhouse/` es de FLOW (esquema y migraciones, I0-13). Ruta
  sensible en producción ([`docs/conventions.md`](../docs/conventions.md) §6.2).
- **Documentación:** [`docs/architecture.md`](../docs/architecture.md),
  [`docs/database.md`](../docs/database.md), [`docs/observability.md`](../docs/observability.md).
