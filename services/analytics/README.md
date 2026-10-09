# `mod:analytics` — services/analytics

- **Propósito:** Consultas analíticas, widgets, dashboards y kiosco; el rol `reporting` genera reportes.
- **Rol(es) de `horus`:** `analytics`, `reporting`
- **Agente dueño:** FLOW (salvo `dashboards`, que es de CORE) ([`docs/backlog/team.md`](../../docs/backlog/team.md) §2, [`.github/CODEOWNERS`](../../.github/CODEOWNERS))
- **Submódulo `dashboards`:** dueño CORE (persistencia de dashboards, team.md §2).
- **Ficha del módulo:** [`docs/services.md`](../../docs/services.md) §3 · Roles y fronteras: [ADR-0025](../../docs/adr/0025-binario-modular-con-roles.md)
- **Layout y reglas:** [`docs/conventions.md`](../../docs/conventions.md) §2.1 — `api/` (contrato público), `internal/` (`config`, `domain`, `app`, `adapters`), `migrations/` (si tiene esquema PostgreSQL). Otros módulos solo importan `services/analytics/api`.

Esqueleto creado en I0-01: todavía sin código. Este README se ampliará con variables de entorno,
métricas y eventos publicados/consumidos cuando el módulo tenga implementación.

## Tráfico y datos de widgets (FLOW: I1-08, I1-29)

Rutas (OpenAPI `packages/schemas/openapi/v0/analytics.yaml`): `GET /analytics/traffic/top`,
`/analytics/traffic/timeseries`, `/analytics/traffic/attribution`,
`/analytics/customers/{customer_id}/traffic` y `/sites/{site_id}/prefix-proposals`. Consultan los
agregados de ClickHouse (5 min / 1 h / 1 d según el rango) como `horus_analytics` con
`SQL_horus_tenant` (row policy) y filtro `tenant_id`; ClickHouse caído ⇒ `503 ANALYTICS_UNAVAILABLE`.

Datos de widgets resueltos en servidor: proveedor en proceso
`services/analytics/api/trafficwidgets` (`module.Services` → `analytics.TrafficWidgetData`) para el
catálogo de dashboards de CORE: `traffic_now`, `customers_active`, `exporters_status`,
`traffic_timeseries`, `top_customers`, `top_services`, `top_categories`, `top_organizations`
(formas de `apps/frontend/app/widgets/shapes.ts`), caché por tenant/tipo/config/rango/alcance con
*singleflight* e IPs enmascaradas sin `show_personal_data`. Escribe `dim.category`,
`dim.service`, `dim.organization` y `dim.asn` desde el catálogo.

Variables: `HORUS_CLICKHOUSE_DSN`, `HORUS_CLICKHOUSE_ANALYTICS_PASSWORD`,
`HORUS_ANALYTICS_QUERY_TIMEOUT` (10s), `HORUS_ANALYTICS_CACHE_TTL` (10s),
`HORUS_FLOWS_INVENTORY_FILE`, `HORUS_NATS_URL` (estado de exportadores), `HORUS_CATALOG_SNAPSHOT_DIR`.
Tests: `make test-analytics`.
