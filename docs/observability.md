# Horus Flow — Observabilidad

> Estado: **propuesta ronda 2** (aplica [`po-decisions.md`](po-decisions.md) D1–D10) · Responsable: Agente C.
> "Sprint N" = el incremento que entrega esa capacidad (D9). Con el binario modular
> ([ADR-0025](adr/0025-binario-modular-con-roles.md)) "servicio" = **rol/módulo**: el label `service` toma el nombre
> del módulo (`devices`, `ingester`…) y se añade `process` (`horus-app`, `horus-collector`, `horus-wg-agent`).
> Fuente: [`vision.md`](vision.md) §6 ("desde el primer sprint"). Relacionados:
> [`architecture.md`](architecture.md), [`services.md`](services.md), [`events.md`](events.md),
> [`security.md`](security.md), [`disaster-recovery.md`](disaster-recovery.md),
> [`conventions.md`](conventions.md).

Objetivo: poder responder en minutos "¿qué falló, dónde y desde cuándo?" para la propia
plataforma (vision §6: *Frontend → API → Device Service → PostgreSQL, saber dónde falló*).

## 0. Principio rector: telemetría de plataforma ≠ datos de producto

| | Telemetría de plataforma | Datos de producto |
|---|---|---|
| Qué | Salud de los servicios de Horus: latencias, errores, colas, recursos | Lo que Horus mide de la red del ISP: CPU de routers, tráfico de interfaces, flujos, consumo por cliente |
| Dónde | Prometheus / Loki / Tempo | PostgreSQL / ClickHouse (ver [`database.md`](database.md), [`storage.md`](storage.md)) |
| Cardinalidad | Baja y acotada | Alta (millones de series/filas) |
| Usuario | Quien opera la plataforma (IA + 1 persona, D7) | NOC y analistas de cada ISP |

Las métricas SNMP **por interfaz** de cada router son datos de producto: van a ClickHouse
(Agente 2), no a Prometheus. Prometheus solo recibe agregados sobre el funcionamiento del
colector. Mezclarlos es la causa nº 1 de explosión de cardinalidad.

## 1. Stack

| Señal | Instrumentación | Transporte | Backend | Retención v1 |
|-------|-----------------|------------|---------|--------------|
| Métricas | `prometheus/client_golang` (con *exemplars* de `trace_id`) | Prometheus *scrape* `/metrics` cada 15 s | Prometheus (→ VictoriaMetrics, §9) | 30 días |
| Logs | `log/slog` JSON a stdout (Go); consola JSON en Nitro/navegador vía endpoint | Grafana **Alloy** lee logs de contenedores | Loki | 30 días (14 días `debug`) |
| Trazas | OpenTelemetry SDK (Go: `go.opentelemetry.io/otel`; web: `@opentelemetry/sdk-trace-web`) | OTLP gRPC `:4317` → Alloy (componentes `otelcol`) | Grafana **Tempo** | 7 días |
| Dashboards / alertas | — | — | Grafana + Alertmanager | Dashboards como código |

Decisiones:

- **Grafana Alloy** como agente único (logs + receptor OTLP + tail sampling). Promtail está
  deprecado (EOL 2026). Alloy embebe componentes del OpenTelemetry Collector, así que los SDK
  hablan OTLP estándar y se puede cambiar a `otelcol-contrib` sin tocar código.
- **Tempo** como backend de trazas (integra con Loki/Prometheus en Grafana: de un log o un
  exemplar se salta a la traza). Jaeger es alternativa válida si se prefiere.
- Métricas con `client_golang` y no con OTel Metrics: más maduro, exemplars nativos, sin capa
  de traducción. Se revisa si OTel Metrics se vuelve el estándar del equipo.
- Configuraciones en `infrastructure/observability/` (las escribe quien implemente el Sprint 1;
  este documento no las define).
- **Grafana, Prometheus, Loki y Tempo son de plataforma**: contienen datos de todos los tenants y **nunca** se exponen
  a usuarios de un ISP. Lo que un ISP ve de "su" salud (ingesta, cobertura, routers) se lo da Horus por la API
  ([`api.md`](api.md)), filtrado por tenant.
- Puerto de administración separado en cada servicio Go (`HORUS_ADMIN_ADDR`, por defecto
  `:8081`): `/metrics`, `/healthz`, `/readyz`, `/debug/pprof/*` (este último solo si
  `HORUS_PPROF_ENABLED=true`). Nunca publicado al exterior. Convive con los puertos de
  [`architecture.md`](architecture.md) (HTTP `8080`, gRPC `9090`). Motivo del puerto aparte: en
  `api-gateway` el `8080` es la API pública detrás de Traefik y `/metrics`/pprof no deben quedar
  accesibles por esa ruta; para uniformidad se aplica a todos los servicios. Traefik puede
  publicar `/healthz` del gateway si se necesita un chequeo externo.

## 2. Métricas

### 2.1 Convenciones de nombres

- Formato `horus_<servicio>_<subsistema>_<nombre>_<unidad>[_total]`, en `snake_case`.
  `<servicio>` es el nombre canónico con `_` (p. ej. `traffic_intelligence`).
  Ejemplos: `horus_snmp_poll_duration_seconds`, `horus_flows_records_received_total`.
- Métricas transversales (HTTP, gRPC, NATS, BD) **sin** prefijo de servicio y con label
  `service`, generadas por la librería común `packages/go/observability`:
  `http_server_request_duration_seconds`, `grpc_server_handled_total`, etc.
- Unidades base: segundos, bytes, ratio 0–1. Nunca milisegundos ni porcentajes en el nombre.
- Contadores terminan en `_total`; *gauges* sin sufijo; histogramas en `_seconds`/`_bytes`.
- Histogramas de latencia con buckets comunes:
  `0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10` s. Se evalúa usar histogramas
  nativos de Prometheus cuando estén estables en la versión desplegada.
- Toda métrica nueva lleva `Help` claro y se documenta en el README del servicio.
- Métrica `horus_build_info{service, version, commit, go_version} 1` en todos los servicios.

### 2.2 Labels: permitidos, condicionados y prohibidos

| Categoría | Labels | Regla |
|-----------|--------|-------|
| **Permitidos** (cardinalidad < 20 por label) | `service`, `env`, `instance`, `method`, `route` (plantilla, p. ej. `/api/v1/devices/{id}`), `status_class` (`2xx`…`5xx`), `code` (HTTP o gRPC), `grpc_method`, `stream`, `consumer`, `subject_prefix` (hasta `horus.<dominio>.<entidad>`), `protocol` (`netflow_v5`, `netflow_v9`, `ipfix`, `sflow`), `vendor`, `result` (`ok`, `timeout`, `error`, `auth_error`), `reason` (enumerado cerrado), `db` (`postgres`, `clickhouse`, `valkey`), `operation` (enumerado) | Libres |
| **Por tenant** (D6) | `tenant` (slug inmutable del ISP; < 100 por instalación) | Permitido **sólo** en la familia de métricas por tenant de §2.6 y en métricas de colectores por router (donde no añade series: cada router pertenece a un tenant). **Prohibido** en métricas RED HTTP/gRPC, de NATS por consumidor y de BD (multiplicaría `route × code × tenant`). |
| **Condicionados** (requieren análisis en el PR) | `site_id` (nodo; estimado < 500 entre todos los ISP), `router_id`/`exporter_id` (hasta 1.000–5.000) | Solo en métricas de la familia de salud de colectores, **máx. 3 series por router**, y nunca combinados con otro label de cardinalidad media. Ej. permitido: `horus_snmp_router_last_success_timestamp_seconds{router_id}`. Alternativa preferida: agregados por `vendor`/`site_id` en Prometheus y el detalle por router en PostgreSQL/ClickHouse. |
| **Prohibidos** | IP de cliente o de destino, `customer_id`, `realm_id`, `finding_id`, `kiosk_id`, `user_id`, `session_id`, `trace_id`/`request_id` (van en exemplars/logs), `interface_id`/`ifIndex`, ASN, puerto, path HTTP sin plantilla, mensajes de error, email, nombre de categoría/servicio de tráfico de forma libre | Bloquear en revisión; test de la librería rechaza labels con nombre de la lista negra |

**Presupuesto de cardinalidad:** objetivo < 200k series activas en v1 **para toda la instalación** (todos los ISP) (un Prometheus de 4 GB
de RAM lo soporta con holgura); máx. 15k series por servicio en el despliegue de 1.000 routers.
Alerta cuando `prometheus_tsdb_head_series` > 70 % del presupuesto. Cálculo de referencia que
justifica la prohibición: 1.000 routers × 50 interfaces × 8 contadores = 400k series solo de
interfaces.

### 2.3 RED (servicios síncronos) — todos los servicios con HTTP/gRPC

| Métrica | Tipo | Labels |
|---------|------|--------|
| `http_server_requests_total` | counter | `service, method, route, code` |
| `http_server_request_duration_seconds` | histogram | `service, method, route, status_class` |
| `http_server_requests_in_flight` | gauge | `service` |
| `grpc_server_handled_total` / `grpc_server_handling_seconds` | counter / histogram | `service, grpc_method, code` (interceptor `go-grpc-middleware/providers/prometheus`) |
| `grpc_client_handled_total` / `grpc_client_handling_seconds` | counter / histogram | `service, grpc_method, code` |
| `horus_api_gateway_ws_connections` | gauge | — |
| `horus_api_gateway_ws_messages_sent_total` | counter | `topic` (enumerado) |
| `horus_api_gateway_ratelimit_rejected_total` | counter | `policy` |
| `horus_auth_login_attempts_total` | counter | `result` (`ok`, `bad_credentials`, `mfa_required`, `mfa_failed`, `throttled`) |

### 2.4 USE (recursos y dependencias)

- Proceso y runtime Go: colectores por defecto de `client_golang` (`go_*`, `process_*`).
- Pools: `db_pool_connections{service, db, state}` (`pgxpool` stats), `db_query_duration_seconds{service, db, operation}`.
- Host y contenedores: `node_exporter` y `cAdvisor`.
- Infraestructura: `postgres_exporter`, métricas nativas de ClickHouse (`/metrics` puerto 9363),
  `redis_exporter` (compatible con Valkey), `prometheus-nats-exporter` (o endpoint de NATS con *surveyor*),
  `node_exporter` con *filesystem* del volumen local de datos (`HORUS_DATA_DIR`), `blackbox_exporter` para el destino
  remoto SFTP si existe (TCP) y para la URL pública. (MinIO desaparece, D2/D3.)

### 2.5 Métricas de negocio / pipeline (las más importantes para operar)

| Métrica | Tipo | Labels | Responde a |
|---------|------|--------|------------|
| `horus_devices_routers{status}` | gauge | `status` (`online`, `degraded`, `warning`, `critical`, `offline`, `stale`, `unknown` — estados de [`architecture.md`](architecture.md) §10.2/§10.4) | ¿Cuántos routers están online? |
| `horus_snmp_polls_total` | counter | `vendor, result` | Tasa de éxito del polling |
| `horus_snmp_poll_duration_seconds` | histogram | `vendor` | Lentitud del polling |
| `horus_snmp_poll_lag_seconds` | histogram | — | Retraso entre la hora programada y la real (lag del colector) |
| `horus_snmp_router_last_success_timestamp_seconds` | gauge | `router_id` (condicionado) | Frescura por router (SLO de §6) |
| `horus_snmp_targets_stale` | gauge | — | Routers sin datos en > 3 intervalos (estado `stale`) |
| `horus_snmp_poller_heartbeat_timestamp_seconds` | gauge | — | Complementa el evento `horus.snmp.poller.heartbeat` (15 s); Prometheus alerta sin depender de NATS |
| `horus_clickhouse_up` | gauge | `service` | Visto desde cada servicio que escribe/lee ClickHouse |
| `horus_flows_packets_received_total` | counter | `protocol` | Paquetes/s de entrada |
| `horus_flows_records_received_total` | counter | `protocol` | **Flujos/s** |
| `horus_flows_dropped_total` | counter | `reason` (`unknown_exporter`, `rate_limited`, `decode_error`, `template_missing`, `bus_unavailable`, `stream_full`) | Pérdidas en ingesta (nombres de razón alineados con [`architecture.md`](architecture.md) §10.1/§10.3) |
| `horus_flows_exporters_active` | gauge | `protocol` | Exportadores vistos en los últimos 5 min |
| `horus_flows_ingest_lag_seconds` | histogram | — | Desde recepción del datagrama hasta *commit* en ClickHouse (SLO) |
| `horus_flows_clickhouse_insert_rows_total` / `_batch_duration_seconds` | counter / histogram | `table` | Rendimiento de inserción |
| `jetstream_consumer_num_pending` | gauge (exporter NATS) | `stream, consumer` | **Eventos pendientes por consumer** |
| `jetstream_consumer_num_ack_pending`, `_num_redelivered` | gauge | `stream, consumer` | Consumidores atascados / mensajes venenosos |
| `horus_events_published_total` / `horus_events_consumed_total` | counter | `service, subject_prefix, result` | Tráfico de eventos |
| `horus_events_processing_duration_seconds` | histogram | `service, consumer` | Coste de procesamiento |
| `horus_events_dlq_total` | counter | `service, consumer` | Mensajes enviados a *dead letter* |
| `horus_outbox_pending` / `horus_outbox_oldest_age_seconds` | gauge | `service` | Outbox atascado (NATS caído). [`architecture.md`](architecture.md) lo llama `outbox_pending_count`; se adopta este nombre por la convención §2.1 |
| `horus_wireguard_agent_reconcile_total` / `_last_success_timestamp_seconds` | counter / gauge | `result` | Reconciliación de `wireguard-agent` (cada 15 s) |
| `horus_wireguard_peers{state}` | gauge | `state` (`handshake_recent` < 3 min, `stale`, `never`) | Salud de túneles (sin label por peer) |
| `horus_alerts_notifications_total` | counter | `channel, result` | Entrega de notificaciones |
| `horus_alerts_pipeline_latency_seconds` | histogram | — | Desde evento detectado a notificación |
| `horus_backup_last_success_timestamp_seconds` | gauge | `component` (`postgres`, `clickhouse`, `secrets_bundle`) | Copia **local**; ver [`disaster-recovery.md`](disaster-recovery.md) |
| `horus_backup_restore_test_last_success_timestamp_seconds` | gauge | `component` | Última restauración de prueba correcta |
| `horus_remote_sync_configured` | gauge | — | Nº de destinos remotos activos (0 es válido: aviso, no fallo) |
| `horus_remote_sync_lag_seconds` | gauge | `destination` (nombre corto, < 5) | Edad de lo más antiguo pendiente de copiar al destino ([ADR-0019](adr/0019-almacenamiento-local-y-destino-remoto.md)) |
| `horus_remote_sync_pending_bytes` | gauge | `destination` | Bytes por copiar |
| `horus_remote_sync_last_success_timestamp_seconds` | gauge | `destination` | Última copia completa verificada |
| `horus_remote_sync_failures_total` | counter | `destination, reason` | Errores (auth, red, cuota del proveedor, verificación) |
| `horus_local_store_used_ratio` | gauge | `volume` (`data`, `backups`) | Ocupación del almacén local (umbrales 70/85/95 %) |
| `horus_audit_chain_verified_timestamp_seconds` | gauge | — | Integridad de auditoría ([`security.md`](security.md) §7) |

### 2.6 Métricas por tenant (ISP) y por nodo (D6)

Con varios ISP la pregunta "¿qué ISP está afectado?" debe responderse sin bajar a logs. Se añade una **familia
acotada** de métricas con label `tenant` (slug); el resto no lo lleva.

| Métrica | Tipo | Labels | Responde a |
|---------|------|--------|------------|
| `horus_tenant_routers` | gauge | `tenant, status` | Routers por estado y por ISP |
| `horus_tenant_flows_records_total` | counter | `tenant` | Flujos/s por ISP (base de cuotas y capacidad) |
| `horus_tenant_flows_dropped_total` | counter | `tenant, reason` (`rate_limited`, `unknown_exporter`→ sin tenant, `bus_unavailable`) | Descartes por ISP; vecino ruidoso |
| `horus_tenant_flows_ingest_lag_seconds` | histogram (buckets reducidos: 5, 15, 30, 60, 120, 300) | `tenant` | Lag por ISP (un ISP no queda tapado por la media) |
| `horus_tenant_flow_coverage_ratio` | gauge | `tenant` | Cobertura de flujos vs. contadores SNMP (umbral 0,8; [ADR-0022](adr/0022-mikrotik-routeros-v7-primer-fabricante.md)) |
| `horus_tenant_exporters_silent` | gauge | `tenant` | Routers principales sin flujos > 5 min |
| `horus_tenant_customers` | gauge | `tenant, status` (`active`, `inactive`) | Clientes descubiertos |
| `horus_tenant_customers_discovered_total` / `horus_customers_discovery_throttled_total` | counter | `tenant` | Ritmo de descubrimiento; avalanchas por escaneo/IP falsificada ([ADR-0018](adr/0018-la-ip-es-el-cliente.md)) |
| `horus_tenant_findings_open` | gauge | `tenant, severity` | Carga de seguridad por ISP (sin `kind` para acotar) |
| `horus_tenant_api_requests_total` | counter | `tenant, status_class` | Uso de API por ISP (sustituye a añadir `tenant` a las RED) |
| `horus_tenant_rate_limited_total` | counter | `tenant, policy` | Cuotas por tenant alcanzadas |
| `horus_tenant_analytics_query_seconds` | histogram (reducido) | `tenant` | Coste de consultas por ISP |
| `horus_tenant_ws_connections` | gauge | `tenant, principal` (`user`, `kiosk`) | Pantallas NOC y usuarios conectados |
| `horus_events_tenant_invalid_total` / `horus_gateway_ws_tenant_mismatch_total` | counter | `consumer` / — | **Seguridad**: mensajes sin `Horus-Tenant` o incoherentes; debe ser 0 |

Por **nodo**: sólo `horus_snmp_router_last_success_timestamp_seconds{router_id}` y
`horus_flows_exporter_last_flow_timestamp_seconds{router_id}` (máx. 2 series por router, con `tenant` como label
redundante sin coste). Cualquier otra vista por nodo (tráfico, clientes, hallazgos) es **dato de producto** y vive en
ClickHouse/PostgreSQL, visible para el ISP en Horus.

Cálculo de cardinalidad (referencia: 20 ISP, 1.000 routers): familia por tenant ≈ 20 × ~40 series = 800; por router
2 × 1.000 = 2.000. Despreciable frente al presupuesto.

Reglas:

- El valor del label es el **slug** inmutable del tenant (legible en alertas), no el UUID; un tenant renombrado
  conserva el slug.
- *Recording rules* por tenant para los SLO de frescura e ingesta (§6), de modo que un ISP pequeño con todos sus
  routers caídos no quede oculto en el 99 % global.
- Al dar de baja un tenant sus series desaparecen solas (staleness); no hay limpieza manual.

## 3. Logs

### 3.1 Formato

JSON de una línea por evento a **stdout** (12-factor; nunca a archivos dentro del contenedor).
Go usa `log/slog` con `JSONHandler` y un *handler* envoltorio que inyecta campos del contexto.

Campos **obligatorios**:

| Campo | Tipo | Ejemplo |
|-------|------|---------|
| `ts` | RFC 3339 con ms, UTC | `2026-10-07T22:31:04.512Z` |
| `level` | `debug` \| `info` \| `warn` \| `error` | `info` |
| `service` | nombre canónico | `devices` |
| `trace_id` | 32 hex (vacío si no hay traza) | `4bf92f3577b34da6a3ce929d0e0e4736` |
| `span_id` | 16 hex | `00f067aa0ba902b7` |
| `request_id` | UUIDv7 asignado por el gateway (`X-Request-Id`), o por el consumidor NATS | `0192…` |
| `msg` | texto corto en inglés, estable (no interpolar valores) | `router created` |

Campo **obligatorio cuando aplica**: `tenant_id` (UUID; en toda línea emitida dentro de una petición o mensaje de un
tenant; vacío en acciones de plataforma). Campos recomendados: `version`, `env`, `component` (p. ej. `http`, `grpc`, `nats`, `poller`),
`actor_id` (UUID de usuario, no email), `event_id`, `subject`, `router_id`, `site_id`,
`duration_ms`, `error` (mensaje), `error_kind`. Claves en `snake_case`.

Ejemplo:

```json
{"ts":"2026-10-07T22:31:04.512Z","level":"warn","service":"snmp","version":"0.3.0","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7","request_id":"0192f1c2-7d1e-7c3a-9e55-1f2a3b4c5d6e","component":"poller","router_id":"0192f1c2-0000-7000-8000-000000000001","vendor":"mikrotik","msg":"snmp poll timeout","duration_ms":2004,"attempt":2}
```

### 3.2 Niveles

| Nivel | Uso | Producción |
|-------|-----|------------|
| `debug` | Detalle para desarrollo | Desactivado; activable por servicio en caliente (`HORUS_LOG_LEVEL` o endpoint admin) con caducidad de 1 h |
| `info` | Hechos de ciclo de vida y negocio relevantes (arranque, configuración efectiva sin secretos, router creado) | Activo. **No** un log por petición exitosa (para eso están métricas y trazas); el gateway escribe un *access log* por petición con muestreo 10 % para 2xx y 100 % para 4xx/5xx |
| `warn` | Situación anómala recuperada (reintento, degradación, dependencia lenta) | Activo |
| `error` | Fallo que afecta a una operación y requiere atención | Activo; un error se loguea **una vez**, donde se maneja, no en cada capa |

No existe `fatal` fuera de `main` (fallo de arranque → log `error` + `os.Exit(1)`).
Los logs en bucle (p. ej. paquete UDP inválido) usan muestreo/rate limit (1 por exportador por
minuto + contador en métricas).

### 3.3 Qué NUNCA se loguea

- Contraseñas, hashes, secretos TOTP, códigos 2FA o de recuperación, tokens de reset.
- Cookies, cabecera `Authorization`, JWT internos, API tokens, refresh tokens, tickets de WebSocket (el reverse proxy redacta el query param `ticket`).
- Credenciales de routers (communities SNMP, usuarios/claves v3, SSH/API), claves WireGuard
  privadas o *preshared*, KEK/DEK, credenciales de destinos remotos (SFTP, OAuth, MEGA), clave de `rclone crypt`,
  tokens de enrolamiento WireGuard, códigos y credenciales de kiosco.
- **IPs de clientes (D1: son su identidad), alias de clientes, destinos de flujos** (dato personal). `customer_id`
  y `tenant_id` sí se pueden loguear (son UUID internos).
  Para depurar ingesta se loguean exportador, plantilla y contadores, no registros.
- Cuerpos completos de peticiones/respuestas de la API (solo en `debug` local y con filtrado).
- Emails completos de usuarios en `info`+ (usar `actor_id`).

Mecanismo: tipos `Secret`/`Redacted` en `packages/go/observability` que implementan
`slog.LogValuer` devolviendo `"[REDACTED]"`; test de lint (regla `forbidigo`/`sloglint`) que
prohíbe pasar campos con nombres sensibles; Alloy aplica un *stage* de redacción de respaldo
(regex para `Authorization`, `password=`, `community`).

### 3.4 Loki

- **Labels** (índice, baja cardinalidad): `service`, `env`, `level`, `host`, `container`.
- `tenant_id`, `trace_id`, `request_id`, `router_id` **no** son labels: van como *structured metadata* o
  dentro del JSON (consultables con `| json`).
- Derived field en Grafana: `trace_id` → enlace a Tempo.
- Retención 30 días (`debug` 14 días si se activa); límites por *tenant* de ingesta
  (p. ej. 10 MB/s) para que un bucle de logs no tumbe Loki.

## 4. Trazas (OpenTelemetry)

### 4.1 SDK

- **Go:** `go.opentelemetry.io/otel` + exportador OTLP gRPC; instrumentación con
  `otelhttp` (Chi), `otelgrpc` (stats handler), `otelpgx` (PostgreSQL), instrumentación manual
  para ClickHouse, Valkey (`redisotel`, compatible) y NATS (ver §4.2). Recurso:
  `service.name`, `service.version`, `deployment.environment`, `service.instance.id`.
  Inicialización en `packages/go/observability.Setup(ctx, cfg)`, con *shutdown* en el cierre
  ordenado.
- **Nuxt 4 (navegador):** `@opentelemetry/sdk-trace-web` con instrumentaciones `fetch` y
  `document-load`; propaga `traceparent` solo a `/api/*` (mismo origen,
  `propagateTraceHeaderCorsUrls` acotado); exporta OTLP/HTTP a `/otel/v1/traces` publicado por el
  reverse proxy hacia Alloy con rate limit y tamaño máximo. Sampling en el navegador 5 %
  (100 % en dev). Sin atributos con datos personales.
- **Nitro (si se usa SSR/servidor):** `@opentelemetry/sdk-node` con `http` instrumentado.
  Con la recomendación de SPA estática ([`conventions.md`](conventions.md) §3) no aplica en v1.
- Variables estándar OTel: `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`,
  `OTEL_TRACES_SAMPLER`, `OTEL_TRACES_SAMPLER_ARG`, `OTEL_RESOURCE_ATTRIBUTES`.

### 4.2 Propagación W3C Trace Context

| Canal | Cómo |
|-------|------|
| HTTP | Cabeceras `traceparent` / `tracestate` (propagador `TraceContext` + `Baggage`). El gateway **no confía** en `traceparent` de clientes externos salvo el de la propia SPA (se acepta para unir trazas; el sampling decide el backend). |
| gRPC | Metadata `traceparent` vía `otelgrpc`. |
| NATS (JetStream) | Cabeceras NATS `traceparent`/`tracestate` (NATS soporta headers) **y** campo `trace_parent` del envelope (Agente 3). El campo del envelope es la fuente de verdad (sobrevive a re-procesos y a herramientas que no copian headers); el header es para instrumentación genérica. Si difieren, prevalece el envelope. |
| Outbox | Se guarda `trace_parent` en la fila del outbox al crear el evento; el *relay* lo publica tal cual. |

Semántica de spans en NATS: productor crea span `PRODUCER` (`messaging.system=nats`,
`messaging.destination.name=<subject>`); consumidor crea span `CONSUMER`:
- consumo 1:1 → hijo del contexto del mensaje;
- procesamiento por lotes (flows, analytics) → span propio del lote con **links** a los
  contextos de los mensajes (máx. 128 links), nunca un span por flujo.

**Dependencia con el Agente 3:** el envelope debe incluir `trace_parent` (formato W3C completo
`00-<trace_id>-<span_id>-<flags>`), `trace_state` opcional, `event_id` (UUIDv7, se loguea),
`occurred_at`, `producer` (servicio) y `actor` (ver [`security.md`](security.md) §6.5).

### 4.3 Sampling

- **Head:** `parentbased_traceidratio` con 10 % en producción (100 % en dev/CI) para trazas
  iniciadas en la plataforma.
- **Tail (Alloy, `otelcol.processor.tail_sampling`):** conservar 100 % de trazas con error o
  duración > 1 s, 100 % de rutas de autenticación con fallo, y 10 % del resto. Decisión tras
  10 s de espera.
- **Pipelines de alto volumen** (colector de flujos, `traffic-intelligence`): sin trazas por
  registro; un span por lote con atributos de conteo; sampling 1 % de lotes.
- Polling SNMP: un span por ciclo de polling de un router con sampling 1 %; 100 % si falla.

### 4.4 Atributos prohibidos en spans

Los mismos que en logs (§3.3). Las sentencias SQL se registran **parametrizadas**
(`db.statement` sin valores). Las URLs se registran con la ruta plantilla.

## 5. Dashboards iniciales de Grafana

Provisionados como código (JSON en `infrastructure/observability/grafana/`), carpeta
"Horus Platform". Variables comunes: `env`, `service`, `instance`.

| # | Dashboard | Contenido | Sprint |
|---|-----------|-----------|--------|
| 1 | **Platform Overview** | Estado de SLOs y error budget, servicios up/down, tasa de errores 5xx, latencia p95 por servicio, alertas activas, últimas versiones desplegadas (`horus_build_info`) | 1 |
| 2 | **Service RED** (plantilla por servicio) | Peticiones/s, errores por código, latencia p50/p95/p99 por ruta y método gRPC, enlaces a logs y trazas (exemplars) | 1 |
| 3 | **Go Runtime** | Goroutines, heap, GC, CPU, FDs por instancia | 1 |
| 4 | **Datastores** | PostgreSQL (conexiones, TPS, replicación/WAL, bloat, consultas lentas), Valkey (memoria, hits, evictions), ClickHouse (inserts/s, merges, partes por tabla, consultas en curso, memoria), almacén local (ocupación por ruta: `backups/`, `archive/`, `reports/`) | 1 / 6 |
| 5 | **NATS JetStream** | Mensajes/s por stream, bytes, pending y ack-pending por consumer, redeliveries, DLQ, uso de almacenamiento vs límites | 1 |
| 6 | **Auth & Security** | Logins por resultado, 2FA, throttling, sesiones activas, rate limit, 403 por ruta | 2 |
| 7 | **SNMP Collector** | Polls/s por vendor, tasa de éxito, duración, lag, routers obsoletos, top 10 routers con más fallos (consulta a PostgreSQL/ClickHouse vía datasource, no Prometheus) | 5 |
| 8 | **Flow Ingestion** | Paquetes y flujos/s por protocolo, descartes por motivo, exportadores activos, lag de ingesta, inserción en ClickHouse, pending de NATS | 6 |
| 9 | **Pipeline de eventos** | Publicados/consumidos por dominio, latencia de procesamiento, outbox pendiente | 3–8 |
| 10 | **Backups & DR** | Antigüedad del último backup local por componente, estado de cada destino remoto (lag, pendiente, errores) o aviso "sin copia fuera del servidor", último test de restauración | backups |
| 11 | **Host & Containers** | CPU, RAM, disco (con predicción de llenado) por volumen (`data`, `backups`), red, I/O | 1 |
| 12 | **Tenants** (D6) | Tabla por ISP: routers por estado, flujos/s y descartes, lag de ingesta, cobertura, exportadores silenciosos, clientes, hallazgos abiertos, uso de API y cuotas; variable `tenant` | multi-tenant |

## 6. SLOs y alertas de la plataforma

### 6.1 SLOs propuestos (ventana de 30 días)

| SLO | SLI | Objetivo | Error budget |
|-----|-----|----------|--------------|
| Disponibilidad API | Peticiones `/api/v1/*` no-5xx / total (excluye 4xx y 503 por mantenimiento anunciado) | **99,5 %** (despliegue de un nodo) → 99,9 % con HA (Sprint 14+) | 3 h 36 min/mes |
| Latencia API | Peticiones de lectura con duración < 300 ms | 95 % | — |
| Latencia API analítica | Consultas de `analytics` < 2 s | 95 % | — |
| Frescura SNMP | Routers activos con último poll exitoso < 2× intervalo (intervalo base 60 s → < 120 s), medido cada minuto | **99 %** de los routers-minuto | — |
| Lag de ingesta de flujos | Lotes con `horus_flows_ingest_lag_seconds` < 60 s | **99 %** | — |
| Entrega de eventos en tiempo real | Evento → WebSocket del navegador < 5 s | 99 % | — |
| Notificación de alertas | Detección → notificación enviada < 60 s | 99 % | — |

Los SLOs son objetivos internos de ingeniería, no compromisos contractuales. Se revisan con datos
reales en el incremento de endurecimiento.

**Por tenant:** frescura SNMP e ingesta de flujos se evalúan además **por tenant** (recording rules con `tenant`): el
SLO global puede cumplirse mientras un ISP pequeño está a ciegas. La alerta por tenant distingue "problema de la
plataforma" (varios tenants a la vez) de "problema del ISP" (uno solo: su enlace, su router, su túnel).

### 6.2 Alertas

Reglas como código (Prometheus rules + Alertmanager). Principio: alertar por **síntomas** que
afectan al usuario (burn rate del SLO) y por pocas causas inminentes (disco, backups).

| Alerta | Condición | Severidad |
|--------|-----------|-----------|
| `APIErrorBudgetBurnFast` | Burn rate > 14,4× en 1 h **y** en 5 min | page |
| `APIErrorBudgetBurnSlow` | Burn rate > 6× en 6 h **y** en 30 min | ticket |
| `SNMPFreshnessBurn` | < 99 % routers frescos en 15 min | page |
| `SNMPCollectorDown` | `up{service="snmp"} == 0` 2 min | page |
| `FlowIngestLagHigh` | p99 lag > 60 s durante 10 min | page |
| `FlowDropsHigh` | descartes (excepto `unknown_exporter`) > 1 % durante 10 min | ticket |
| `NATSConsumerLagging` | `num_pending` creciente 15 min y > umbral por consumer | ticket (page si es `alerts`) |
| `NATSStreamNearLimit` | uso de stream > 80 % de `max_bytes` | page |
| `OutboxStuck` | `horus_outbox_oldest_age_seconds` > 300 | page |
| `ServiceDown` | `up == 0` 2 min (servicios críticos: gateway, auth, devices) | page |
| `PostgresDown` / `ClickHouseDown` / `ValkeyDown` / `NATSDown` | exporter fallando 2 min | page |
| `PostgresWALArchiveFailing` | `pg_stat_archiver` con fallos continuos > 15 min | page |
| `PostgresWALTooLarge` | tamaño de `pg_wal` > 20 GB **o** > 50 % del volumen (riesgo de [`architecture.md`](architecture.md) §10.5) | page |
| `FlowsBufferHigh` | stream de flujos > 70 % de `max_bytes` (autonomía ante caída de ClickHouse) | page |
| `RouterMassOffline` / `WireGuardHubDown` | > 50 % de routers de **varios tenants** `offline` con `tunnel_down`, o `horus.wireguard.hub.status_changed` a `down` (caída del hub de plataforma) | page |
| `DiskWillFillIn24h` | `predict_linear` del FS de datos | page |
| `BackupTooOld` | `time() - horus_backup_last_success_timestamp_seconds > 26h` (copia **local** de PG) | page |
| `RestoreTestTooOld` | última restauración de prueba > 35 días (mensual + margen) | ticket |
| `RemoteCopyNotConfigured` | `horus_remote_sync_configured == 0` durante 7 días (y luego cada 7 días) | ticket (aviso; nunca page: D2 permite no tener destino) |
| `RemoteCopyLagging` (`remote_copy_lagging`) | `horus_remote_sync_lag_seconds > 36h` | ticket; page si > 72 h |
| `RemoteDestinationUnhealthy` (`remote_destination_unhealthy`) | `increase(horus_remote_sync_failures_total[6h]) > 3` y sin éxito en 24 h | ticket |
| `LocalStoreFilling` | `horus_local_store_used_ratio` > 0,70 (ticket) / > 0,85 (page) / > 0,95 (page: se pausan archivado y reportes) | ticket / page |
| `AuditChainBroken` | verificación falla | page (seguridad) |
| `CertificateExpiringSoon` | < 14 días (blackbox) | ticket |
| `PrometheusCardinalityHigh` | series > 70 % del presupuesto | ticket |
| `TenantIsolationViolation` | `horus_events_tenant_invalid_total` o `horus_gateway_ws_tenant_mismatch_total` > 0 | page (seguridad) |
| `TenantFlowsSilent` | todos los exportadores de **un** tenant silenciosos > 10 min mientras otros tenants reciben | ticket (problema del ISP; se avisa además al ISP por su módulo `alerts`) |
| `TenantIngestLagHigh` | p99 lag de un tenant > 120 s durante 10 min | ticket |
| `TenantFlowCoverageLow` (`flow_coverage_low`) | `horus_tenant_flow_coverage_ratio < 0.8` durante 2 h | ticket (lo ve también el ISP) |
| `TenantNoisyNeighbor` | un tenant > 60 % de los flujos/s totales **y** descartes por cuota > 0 | ticket |
| `CustomerDiscoveryAvalanche` | `rate(horus_customers_discovery_throttled_total[10m]) > 0` | ticket (posible escaneo o IPs falsificadas) |
| `Watchdog` | Siempre activa → *dead man's switch* externo | — |

**Plataforma ≠ red de cada ISP.** Alertmanager avisa a quien **opera la plataforma** (la persona, D7, y los agentes
de operación). Las alertas de la red de un ISP (router caído, hallazgo de botnet) son **producto**: las genera el
módulo `alerts` de Horus por tenant y van a los canales que configure cada ISP; nunca salen por Alertmanager (que
mezclaría ISPs). Las alertas con `tenant` agrupan por `tenant` (`group_by: [alertname, tenant]`) para no generar una
tormenta cuando cae un ISP entero, e inhiben las por router de ese tenant. Con **una sola persona** de guardia
(D7): `page` sólo para lo que afecta a varios tenants, a la plataforma entera o a la seguridad; lo de un solo ISP es
`ticket` en horario laboral.

**Independencia:** las alertas de la plataforma van por **Alertmanager** directamente (email +
Telegram) y **no** dependen del servicio `alerts` de Horus (que puede estar caído justo cuando
se necesita). La alerta `Watchdog` se envía a un servicio externo de heartbeat
(p. ej. Healthchecks.io autoalojado en otra máquina o SaaS) que avisa si deja de llegar.
Cada alerta `page` tiene enlace a su runbook en [`disaster-recovery.md`](disaster-recovery.md).

## 7. Health checks

Servidos en el puerto de administración (`HORUS_ADMIN_ADDR`), sin autenticación, solo en la
red interna. Compatibles con Kubernetes (`livenessProbe`, `readinessProbe`, `startupProbe`)
y con `healthcheck` de docker compose.

| Endpoint | Pregunta | Comprueba | **No** comprueba | Fallo implica |
|----------|----------|-----------|------------------|---------------|
| `GET /healthz` (liveness) | ¿El proceso está vivo y no bloqueado? | El servidor HTTP responde; opcionalmente un *heartbeat* de los bucles principales (poller, consumidores) actualizado en los últimos 60 s | **Ninguna dependencia externa** (si PostgreSQL cae, reiniciar el servicio no ayuda y provoca cascadas) | Reiniciar el contenedor |
| `GET /readyz` (readiness) | ¿Puede atender tráfico útil ahora? | Configuración cargada, migraciones verificadas (versión esperada), dependencias **imprescindibles** para su función con *timeout* de 1 s y resultado cacheado 5 s | Dependencias opcionales/degradables | Sacarlo del balanceo (no reiniciar) |

Respuesta JSON (200 o 503):

```json
{"status":"degraded","service":"analytics","version":"0.6.0","checks":{"postgres":{"status":"ok","latency_ms":2},"clickhouse":{"status":"fail","error":"dial timeout"}}}
```

Con el binario modular ([ADR-0025](adr/0025-binario-modular-con-roles.md)) **readiness es por rol**: cada proceso
agrega los chequeos de los roles de su `HORUS_ROLES`, y `/readyz?role=<rol>` permite consultar uno. El proceso está
*ready* si **todos sus roles** lo están; un rol degradable no saca al proceso del balanceo. Así, ClickHouse caído no
deja a `horus-app` fuera de servicio (login e inventario siguen) aunque `analytics` responda 503 en sus rutas.

| Rol | `/readyz` requiere | Degradable (reporta `degraded`, sigue *ready*) |
|-----|--------------------|-----------------------------------------------|
| `gateway` | JWKS de `auth` cargado (caché) | Valkey (revocación con fallback a `auth`, rate limit local); roles remotos (503 por ruta con circuit breaker) |
| `auth` | PostgreSQL | Valkey, NATS (auditoría queda en outbox) |
| `devices` | PostgreSQL | NATS (outbox) |
| `wireguard` | PostgreSQL | NATS, `wg-agent` (cambios en cola de reconciliación) |
| `wg-agent` | Interfaz WG presente en el kernel | `wireguard` (mantiene el último estado aplicado; nunca borra peers por no poder leer el deseado) |
| `snmp` | Lista de objetivos cargada (snapshot o caché local) | NATS (buffer en memoria), `devices` |
| `collector` | Socket UDP abierto, mapa exportador→(tenant, router) cargado | NATS (buffer acotado; al llenarse descarta con `reason=bus_unavailable`) |
| `ingester` | NATS, ClickHouse, snapshots de catálogo y reputación cargados | — |
| `traffic`, `detection` | PostgreSQL | ClickHouse (detección pausada y marcada), NATS |
| `analytics` | PostgreSQL | ClickHouse (503 `ANALYTICS_UNAVAILABLE` en sus rutas) |
| `reporting` | PostgreSQL | ClickHouse, almacén local lleno (reportes en cola) |
| `alerts` | PostgreSQL, NATS | Canales de notificación externos |
| `jobs` | PostgreSQL | Destino remoto (nunca afecta a readiness: sólo `remote_storage: degraded/not_configured`), ClickHouse |

Además: `startupProbe` usa `/healthz` con margen amplio (migraciones/caches iniciales); el
cierre ordenado pone `/readyz` en 503 inmediatamente al recibir `SIGTERM`, espera 5 s y drena.

## 8. Correlación entre señales

- `request_id` (gateway) y `trace_id` presentes en logs y en respuestas de error
  (`instance`/`trace_id` en el Problem Details de [`api.md`](api.md)) para que soporte pueda
  buscar.
- Exemplars en histogramas de latencia → salto de un pico en Grafana a la traza exacta.
- Logs ↔ trazas por `trace_id` (derived field en Loki, "logs for this span" en Tempo).
- Auditoría guarda `trace_id` y `request_id` ([`security.md`](security.md) §7.2).

## 9. ¿Cuándo migrar a VictoriaMetrics?

Prometheus de un nodo es suficiente para v1. Migrar (primero como `remote_write` a
VictoriaMetrics single-node, manteniendo Prometheus/vmagent como scraper) si se cumple
**cualquiera**:

1. Series activas > 1 M de forma sostenida o RAM de Prometheus > 16 GB.
2. Retención necesaria > 90 días a resolución completa (capacity planning, comparativas
   interanuales de la plataforma).
3. Necesidad de HA de métricas (dos réplicas con deduplicación) tras el Sprint 14.
4. Consultas de dashboards con p95 > 5 s pese a *recording rules*.
5. Más de una instalación/entorno que se quiera centralizar.

VictoriaMetrics single-node es compatible con PromQL (MetricsQL) y con los dashboards
existentes; el cambio es de infraestructura, no de instrumentación. Alternativa considerada:
Grafana Mimir (más pesado de operar; solo si se adopta el stack LGTM completo a escala).

## 10. Checklist de observabilidad para la Definición de Terminado

- [ ] El servicio usa `packages/go/observability` (logs JSON con campos obligatorios, trazas,
      métricas RED automáticas, `horus_build_info`).
- [ ] Métricas de negocio nuevas documentadas, con labels de la lista permitida o análisis de
      cardinalidad en el PR.
- [ ] Ningún dato prohibido (§3.3) en logs, spans ni labels.
- [ ] Contexto de traza propagado por HTTP/gRPC/NATS (test de integración que verifica
      `trace_parent` en el evento publicado).
- [ ] `/healthz` y `/readyz` implementados según §7, con dependencias clasificadas.
- [ ] Alertas y panel de dashboard añadidos o actualizados si la funcionalidad crea un nuevo
      modo de fallo; runbook enlazado.
