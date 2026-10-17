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

## Rol `reporting`: reporte semanal de seguridad (I3)

Paquete `services/analytics/reporting` (el rol vive en este módulo: mismo esquema PostgreSQL
`analytics` y mismos agregados de ClickHouse; no es un módulo propio, `docs/services.md`).

- **Contenido** (una semana ISO, lunes 00:00 → lunes 00:00 en la zona del ISP, o un rango a petición
  de hasta 31 días): hallazgos abiertos/resueltos/falsos positivos por tipo y severidad, MTTR,
  clientes con más hallazgos activos (alias o IP), críticos/altos vigentes, estado de seguridad de los
  clientes y contactos con listas de reputación (contrato en proceso
  `services/detection/api/securityreport`); disponibilidad ICMP/SNMP por router (`snmp.device_1h` +
  `dim.router`/`dim.site`); bajada/subida frente al periodo anterior y por día local (`flows.site_1h`);
  avisos enviados por canal y evento (`services/alerts/api` `NotificationStats`). Una fuente que no
  está en el proceso o falla deja su sección con una nota (nunca ceros).
- **Formatos:** PDF A4 imprimible (go-pdf/fpdf, Helvetica estándar en cp1252, identidad Horus en
  claro, salida reproducible) y zip de CSV (uno por sección, UTF-8 con BOM, `;`, decimales con coma,
  fórmulas neutralizadas). Archivos en `$HORUS_DATA_DIR/reports/<tenant_id>/<run_id>.pdf|-csv.zip`.
- **Datos personales:** cada formato se genera también enmascarado (`<run_id>-masked.pdf|-masked-csv.zip`,
  `mask.go`: IPv4 `a.b.c.x`, IPv6 /48 + `…`, alias → `Cliente N` estable en el archivo, IPs del resumen
  de los hallazgos enmascaradas). La descarga sirve esa variante a quien no tiene `customers.read` de
  todo el ISP (usuarios y API tokens), la anota en la auditoría (`changes.variant`) y en la cabecera
  `X-Horus-Report-Variant`; `GET /reports*` devuelve `personal_data_masked` y los archivos de esa variante.
- **Cola y programación:** `analytics.report_definition` (una por ISP y tipo; por defecto habilitada,
  lunes 07:00 en la zona del ISP, PDF y CSV) y `analytics.report_run` (verdad de la cola; el worker
  reclama con `FOR UPDATE SKIP LOCKED`, plazo de 15 min y 3 intentos). El planificador encola una
  ejecución por definición y periodo (`ux_report_run__scheduled`: idempotente con varias réplicas),
  recupera la última ejecución perdida y no genera reportes anteriores al alta de la definición. Con
  auth en el proceso crea la definición por defecto de cada ISP activo y copia su nombre, slug y zona.
- **API:** `GET/POST /reports`, `GET /reports/{id}`, `GET /reports/{id}/download` (auditada como
  `reports.download`), `GET /report-definitions`, `PATCH /report-definitions/{id}`.
- **Eventos:** `horus.reporting.report.completed|failed` por el outbox de `analytics` (topic WebSocket `me`).
- **Aviso por correo:** con `HORUS_SMTP_*` y destinatarios en la definición, cada reporte programado
  envía un aviso con el enlace a la página de reportes (`HORUS_PUBLIC_BASE_URL`), sin adjunto.

Variables: `HORUS_DATA_DIR`, `HORUS_REPORTS_RETENTION_DAYS` (365), `HORUS_REPORTS_WORKER` (true; false =
solo API), `HORUS_REPORTS_QUERY_TIMEOUT` (60s), `HORUS_CLICKHOUSE_DSN`,
`HORUS_CLICKHOUSE_ANALYTICS_PASSWORD`, `HORUS_SMTP_*`, `HORUS_PUBLIC_BASE_URL`. Revisión visual del PDF:
`HORUS_REPORTING_PDF_OUT=/tmp/muestra.pdf go test -run TestRenderPDF ./services/analytics/reporting/`.
Tests de integración: `go test -tags integration ./services/analytics/reporting/` (PostgreSQL y ClickHouse
con Docker o `HORUS_TEST_POSTGRES_DSN` / `HORUS_CH_TEST_DSN`).
