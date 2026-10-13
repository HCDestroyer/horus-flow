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
`/analytics/customers/{customer_id}/traffic`, `/sites/{site_id}/prefix-proposals` y `GET /asn/{asn}`
(nombre del ASN en el dataset, catálogo y regla propia del ISP). Las etiquetas de los tops usan las
reglas ASN propias del tenant (`horus.devices.asn_rule.*`, proyectadas en el inventario `flowinv`). Consultan los
agregados de ClickHouse (5 min / 1 h / 1 d según el rango) como `horus_analytics` con
`SQL_horus_tenant` (row policy) y filtro `tenant_id`; ClickHouse caído ⇒ `503 ANALYTICS_UNAVAILABLE`.

Datos de widgets resueltos en servidor: proveedor en proceso
`services/analytics/api/trafficwidgets` (`module.Services` → `analytics.TrafficWidgetData`) para el
catálogo de dashboards de CORE: `traffic_now`, `customers_active`, `exporters_status`,
`traffic_timeseries`, `top_customers`, `top_services`, `top_categories`, `top_organizations` y
`routers_status_grid` (formas de `apps/frontend/app/widgets/shapes.ts`), caché por
tenant/tipo/config/rango/alcance con *singleflight* e IPs enmascaradas sin `show_personal_data`.
Escribe `dim.category`, `dim.service`, `dim.organization` y `dim.asn` desde el catálogo.

## Routers (EP-08: series históricas y widget de estado)

Rutas: `GET /analytics/routers/{router_id}/metrics` (series de dispositivo: CPU, memoria,
temperatura, latencia ICMP, `availability_ratio` = sondeos respondidos / total por bucket, uptime,
duración del sondeo) y `GET /analytics/routers/{router_id}/interfaces/metrics` (una serie por
métrica e interfaz, `group` = nombre; `interface_id` csv de hasta 8 UUID; sin él, los uplinks del
estado vivo del rol `snmp` o las 4 interfaces con más tráfico). Leen `snmp.device_metrics` y
`snmp.interface_metrics` (escritor: rol `snmp`) con `tenant_id` + `router_id`; crudo con paso ≥ 1 min
hasta 24 h y los agregados `snmp.device_5m/1h` y `snmp.interface_5m/1h` (SimpleAggregateFunction:
media = `_sum / _count`, nunca 0 sin muestras) para rangos mayores (5 min hasta 2 d, 1 h después). Huecos como `null` (nunca 0), `meta.source:
snmp`, `partial`/`coverage` como en tráfico. El router se valida contra el inventario `flowinv`, el
estado del rol `snmp` o `dim.router`: de otro ISP o fuera del alcance por nodo ⇒ 404. Permiso
`snmp.read`; el JWT de kiosco entra sin permisos (lista blanca de `permissions.yaml`).

Widget `routers_status_grid` (kind `table`, tema `routers.status`): filas de
`snmpapi.RouterStatusReader.List` (publicado por el rol `snmp` en `module.Services` como
`snmp.RouterStatus`) filtradas por `config.show` y ordenadas por `config.sort` (`status` = problemas
primero como ordena `List`, `name`, `cpu` desc, `traffic` desc por rx+tx del uplink); columnas
`router, site, router_id, status, reason, since, last_observed_at, cpu_percent, memory_percent,
temperature_celsius, uptime_seconds, rx_bps, tx_bps, uplink, exporter_state, tunnel_state,
interfaces_up, interfaces_total`; `meta.freshness_seconds` desde `PollerHealth.LastCycleAt` y
`partial` si el sondeador no está sano. Sin el rol `snmp` en el proceso el widget responde
`ErrUnavailable` (503 `ANALYTICS_UNAVAILABLE`, "no disponible"), nunca una lista vacía. Sin datos
personales.

Variables: `HORUS_CLICKHOUSE_DSN`, `HORUS_CLICKHOUSE_ANALYTICS_PASSWORD`,
`HORUS_ANALYTICS_QUERY_TIMEOUT` (10s), `HORUS_ANALYTICS_CACHE_TTL` (10s),
`HORUS_FLOWS_INVENTORY_FILE`, `HORUS_NATS_URL` (estado de exportadores), `HORUS_CATALOG_SNAPSHOT_DIR`.
Tests: `make test-analytics`.
