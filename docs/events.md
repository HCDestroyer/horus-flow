# Contrato de eventos NATS JetStream — Horus Flow

> Estado: **propuesta Sprint 0 — documento crítico** (uno de los tres que [`vision.md`](./vision.md) exige cerrar antes de
> fijar los microservicios) · Dueño: Agente 3 (contratos e integración). **Normativo**: si un nombre de evento difiere en
> otro documento, manda éste ([`services.md`](./services.md) lo establece así).
>
> Relacionados: [ADR-0006](./adr/0006-nats-jetstream-bus-de-eventos.md) (NATS), [ADR-0016](./adr/0016-transactional-outbox.md)
> (outbox), [ADR-0015](./adr/0015-enriquecimiento-de-flujos-en-ingesta.md) (clasificación en ingesta),
> [ADR-0014](./adr/0014-granularidad-de-microservicios-en-el-mvp.md) (servicios del MVP), [`architecture.md`](./architecture.md)
> §5, §9, §10, [`services.md`](./services.md), [`database.md`](./database.md), [`traffic-model.md`](./traffic-model.md) §3–§4,
> [`storage.md`](./storage.md), [`security.md`](./security.md) §6–§7, [`observability.md`](./observability.md),
> [`disaster-recovery.md`](./disaster-recovery.md), [`api.md`](./api.md) §4, [`open-questions/contracts.md`](./open-questions/contracts.md).

## 0. Resumen de decisiones

| # | Decisión |
| --- | --- |
| E1 | Tres familias: **eventos de dominio** (bajo volumen, durables, contrato estricto), **telemetría** (alto volumen, buffer acotado, pérdida medida) y **trabajos** (cola `workqueue`). |
| E2 | Subject de dominio `horus.<dominio>.<entidad>.<evento>.<entity_id>`: los 4 primeros tokens son el **tipo**; el 5º, la clave del agregado. Telemetría `horus.telemetry.<fuente>.<tipo>.<clave>`. Trabajos `horus.jobs.<dominio>.<trabajo>`. |
| E3 | `<dominio>` es el **dominio lógico** (carpeta en `services/`), no el proceso: `reputation` y `reporting` conservan su dominio aunque en v1 vivan dentro de `detection` y `analytics`. Para traffic-intelligence el token es **`traffic`** (único y definitivo). |
| E4 | Un stream por dominio (`<DOMINIO>_EVENTS`, `limits`, 30 d), streams de telemetría `TLM_*` (`limits` + `max_bytes`, `discard: old`), `JOBS` (`workqueue`), `DLQ`. Todo `file`. |
| E5 | Consumidores **pull durables** por servicio y propósito, `AckExplicit`, `MaxDeliver` + `BackOff`, DLQ en la librería de consumo + vigilante de advisories. El `api-gateway` usa **NATS core** (sin consumidores). |
| E6 | Sobre estilo CloudEvents en `snake_case`: `id` UUIDv7, `type`, `source`, `subject`, `time`, `schema_version`, `tenant_id`, `actor`, `trace_parent`, `aggregate_version`… |
| E7 | **Protobuf es el único lenguaje de esquema**. Codificación JSON (protojson) para dominio y trabajos; Protobuf binario para telemetría. |
| E8 | Dominio desde servicios con PostgreSQL ⇒ **transactional outbox** (`Nats-Msg-Id` = `event_id`). Sin PostgreSQL (`snmp`, `flows`) ⇒ publicación confirmada + spool local sin descarte + reconciliación por estado. |
| E9 | Auditoría: cada servicio publica `horus.<dominio>.audit.recorded` vía outbox en su propio stream; sólo `auth` la persiste. |
| E10 | Multi-tenant: sin token de tenant en subjects; `tenant_id` en el sobre; aislamiento futuro con **NATS Accounts**. |
| E11 | Registro en `packages/events/catalog.yaml`; payloads en `packages/protobuf/horus/events/<dominio>/v1/`. |

---

## 1. Familias de mensajes

| Aspecto | Evento de dominio | Telemetría | Trabajo (job) |
| --- | --- | --- | --- |
| Qué es | Hecho de negocio ocurrido ("router creado", "peer revocado", "estado cambió a offline") | Observación periódica o masiva (sondeo SNMP, lote de flujos, estado de peers) | Orden de ejecutar algo una vez (generar un reporte) |
| Volumen | Decenas–cientos/min | Miles–cientos de miles de registros/s (en lotes) | Pocos/h |
| Publicación | Outbox (o publicación confirmada + spool en `snmp`/`flows`) | Directa, asíncrona, con buffer local acotado | Outbox del servicio que encola |
| Codificación | JSON, sobre en el cuerpo | Protobuf, sobre en headers | JSON, sobre en el cuerpo |
| Retención | 30 d | Horas, limitada por bytes | Hasta ser confirmado |
| Orden | Por agregado vía `aggregate_version` | Irrelevante (cada registro trae su tiempo) | Irrelevante |
| WebSocket | Topics clase *evento* | Topics clase *estado* | — |

Regla: un evento de dominio nunca transporta series de telemetría y la telemetría nunca dispara efectos de negocio
directamente. Si una observación cambia el negocio, el servicio dueño emite un evento de dominio
(`horus.snmp.router.state_changed`).

---

## 2. Subjects

### 2.1 Eventos de dominio

```
horus.<dominio>.<entidad>.<evento>.<entity_id>
        │          │         │          └── UUIDv7 del agregado
        │          │         └──────────── verbo en pasado, snake_case (created, state_changed, key_rotated)
        │          └────────────────────── entidad en singular, snake_case (router, peer, customer, audit)
        └───────────────────────────────── dominio lógico (§2.3)
```

- `type` = 4 primeros tokens (`horus.devices.router.created`). El subject se construye siempre `type + "." + entity_id`
  con la librería; nunca a mano.
- **5º token** (refinamiento de la convención compartida `horus.<dominio>.<entidad>.<evento>`): permite filtrar por
  entidad sin deserializar (`horus.*.*.*.<router_id>`, usado por el gateway para el topic `router.<id>`), habilita en el
  futuro particionado determinista (`partition(N, 5)`) y `max_msgs_per_subject`. Cardinalidad = nº de entidades (miles):
  sin impacto en el servidor.
- Tokens `[a-z0-9_]`; sin mayúsculas; subject ≤ 128 bytes.
- Filtros típicos: `horus.devices.router.>`, `horus.devices.*.created.*`, `horus.*.audit.recorded.*`.

### 2.2 Telemetría y trabajos

| Subject | Clave | Tipo | Stream |
| --- | --- | --- | --- |
| `horus.telemetry.snmp.device.<router_id>` | router | `horus.snmp.device.sampled` | `TLM_SNMP` |
| `horus.telemetry.snmp.interface.<router_id>` | router | `horus.snmp.interface.sampled` (lote de las interfaces de un sondeo) | `TLM_SNMP` |
| `horus.telemetry.wireguard.peer_status.<server_id>` | hub WG | `horus.wireguard.peer_status.observed` | `TLM_WIREGUARD` |
| `horus.telemetry.flows.batch.<router_id>` | router exportador (o `unregistered`) | `horus.flows.batch.received` | `TLM_FLOWS` |
| `horus.jobs.reporting.generate_report` | — | orden de generar un reporte | `JOBS` |

- Toda la telemetría bajo `horus.telemetry.>` (en lugar de `horus.flows.batch.*` / `horus.snmp.metrics.*` de los
  diagramas de [`architecture.md`](./architecture.md) §5.3, que eran ilustrativos): una sola regla de permisos/monitoreo
  separa lo masivo de lo de dominio, y no solapa con los streams de dominio (`horus.snmp.>` ≠ `horus.telemetry.snmp.>`).
- El `type` de telemetría conserva la forma `horus.<dominio>.<entidad>.<evento>` (va en el header `Horus-Type`).
- La clave final permite al gateway suscribirse a un solo router y, si hace falta, particionar flujos
  (*subject mapping* `horus.telemetry.flows.batch.*` → `horus.telemetry.flows.batch.p<k>.<router_id>`; ver
  [`architecture.md`](./architecture.md) §9.3) sin tocar productores.

### 2.3 Dominios y productores

| Token | Servicio dueño del dominio | Proceso que publica en v1 | Stream |
| --- | --- | --- | --- |
| `auth` | auth | auth | `AUTH_EVENTS` |
| `devices` | devices | devices | `DEVICES_EVENTS` |
| `wireguard` | wireguard | wireguard (control; el agente no publica) | `WIREGUARD_EVENTS` |
| `snmp` | snmp | snmp | `SNMP_EVENTS` |
| `flows` | flows | flows (collector / ingester) | `FLOWS_EVENTS` |
| `traffic` | traffic-intelligence | traffic-intelligence | `TRAFFIC_EVENTS` |
| `reputation` | reputation | **detection** (módulo `reputation`) | `REPUTATION_EVENTS` |
| `detection` | detection | detection | `DETECTION_EVENTS` |
| `alerts` | alerts | alerts | `ALERTS_EVENTS` |
| `analytics` | analytics | analytics | `ANALYTICS_EVENTS` |
| `reporting` | reporting | **analytics** (rol `reporting-worker`) | `REPORTING_EVENTS` |

- **`traffic`** (no `traffic_intel` ni `traffic-intelligence`): corto, coincide con el recurso REST `/traffic/*` y el
  paquete Protobuf `horus.traffic.v1`. El esquema PostgreSQL puede seguir llamándose `traffic_intel`; es independiente.
- Dominio lógico ≠ proceso: cuando `reputation` o `reporting` se separen ([ADR-0014](./adr/0014-granularidad-de-microservicios-en-el-mvp.md)),
  subjects y streams no cambian; sólo los permisos NATS de quién publica.
- **Permisos NATS** ([`security.md`](./security.md)): cada usuario de servicio sólo publica en `horus.<sus-dominios>.>`,
  `horus.telemetry.<su-fuente>.>`, `horus.dlq.<servicio>.>` y, si encola trabajos, `horus.jobs.<su-dominio>.>`.

### 2.4 Multi-tenancy

v1 tiene una organización (un ISP). **Sin token de tenant en subjects**; `tenant_id` en el sobre y en el header
`Horus-Tenant` (UUID fijo; es la columna `tenant_id` de [`database.md`](./database.md); **no** confundir con
`organization_id`, que en el catálogo de tráfico es la organización de red — Meta, Google).

Justificación: el aislamiento idiomático en NATS es **una Account por tenant**, con su propio espacio de subjects y
streams; con ese modelo el tenant no necesita estar en el subject. Si se optara por una cuenta compartida, se insertaría
con *subject mapping* (`horus.> → horus.<tenant>.>`) sin tocar productores. Llevar `tenant_id` en el sobre desde el día
uno deja atribuible todo lo persistido (outbox, ClickHouse, DLQ, auditoría).

### 2.5 Subjects reservados y NATS KV

- `horus.dlq.>` (dead-letter), `horus.jobs.>`, `horus.telemetry.>`.
- **NATS KV** ([ADR-0006](./adr/0006-nats-jetstream-bus-de-eventos.md)) es coordinación, no eventos: buckets `snmp_shards`
  (leases, TTL 30 s), `snmp_router_state` (estado observado actual), `locks` (jobs singleton). Viven en `$KV.<bucket>.>` y
  no forman parte de este contrato salvo `snmp_router_state`, **fuente de reconciliación** de
  `horus.snmp.router.state_changed` (§6.3). Diseño de buckets: [`architecture.md`](./architecture.md).

---

## 3. Streams

Comunes: `storage: file` (SSD local), `discard: old`, `allow_direct: true`. **Réplicas `1` en v1** (nodo único); `3` al
pasar a clúster (Sprint 14) salvo indicación. Los streams se definen **declarativamente** en `packages/events/streams/` y
los aplica un job idempotente de `infrastructure/nats/` en cada arranque; los servicios no crean streams.

### 3.1 Dominio

| Stream | Subjects | Retención | `max_age` | `max_bytes` | `duplicate_window` | `max_msg_size` |
| --- | --- | --- | --- | --- | --- | --- |
| `AUTH_EVENTS` | `horus.auth.>` | limits | 30 d | 1 GiB | 20 min | 64 KiB |
| `DEVICES_EVENTS` | `horus.devices.>` | limits | 30 d | 2 GiB | 20 min | 64 KiB |
| `WIREGUARD_EVENTS` | `horus.wireguard.>` | limits | 30 d | 1 GiB | 20 min | 64 KiB |
| `SNMP_EVENTS` | `horus.snmp.>` | limits | 30 d | 2 GiB | 20 min | 64 KiB |
| `FLOWS_EVENTS` | `horus.flows.>` | limits | 30 d | 1 GiB | 20 min | 64 KiB |
| `TRAFFIC_EVENTS` | `horus.traffic.>` | limits | 30 d | 512 MiB | 20 min | 64 KiB |
| `REPUTATION_EVENTS` | `horus.reputation.>` | limits | 30 d | 512 MiB | 20 min | 64 KiB |
| `DETECTION_EVENTS` | `horus.detection.>` | limits | 30 d | 2 GiB | 20 min | 64 KiB |
| `ALERTS_EVENTS` | `horus.alerts.>` | limits | 30 d | 2 GiB | 20 min | 64 KiB |
| `ANALYTICS_EVENTS` | `horus.analytics.>` | limits | 30 d | 512 MiB | 20 min | 64 KiB |
| `REPORTING_EVENTS` | `horus.reporting.>` | limits | 30 d | 512 MiB | 20 min | 64 KiB |

- **`limits` y no `interest`/`workqueue`**: varios servicios consumen el mismo evento y un consumidor nuevo debe poder
  hacer *backfill* o reconstruir una proyección leyendo hasta 30 días. `workqueue` prohíbe consumidores solapados;
  `interest` borra lo ya consumido.
- **30 d** ([ADR-0006](./adr/0006-nats-jetstream-bus-de-eventos.md) sugería "p. ej. 7 días"): el volumen de dominio es mínimo
  (MB/día) y 30 d cubre caídas largas de un consumidor (incluido `auth` como escritor de auditoría, cuya propuesta mínima
  era 7 d en [`security.md`](./security.md) §7.3) y re-proyecciones sin tocar PostgreSQL. No es sistema de registro.
- **Un stream por dominio**: retención/tamaño/permisos independientes, aislamiento ante un productor desbocado.
- **Auditoría dentro del stream de su dominio** (`horus.<dominio>.audit.recorded.*`), no en un stream aparte: no puede
  haber dos streams con subjects solapados, y la convención acordada pone la auditoría bajo el dominio.
- **Dedupe 20 min**: cubre reinicios del relay del outbox y reintentos desde spool.

### 3.2 Telemetría, trabajos y DLQ

| Stream | Subjects | Retención | `max_age` | `max_bytes` (v1) | Dedupe | `max_msg_size` | Réplicas futuras |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `TLM_SNMP` | `horus.telemetry.snmp.>` | limits | 72 h | 5 GiB | 2 min | 1 MiB | 3 |
| `TLM_WIREGUARD` | `horus.telemetry.wireguard.>` | limits | 24 h | 1 GiB | 2 min | 256 KiB | 1 |
| `TLM_FLOWS` | `horus.telemetry.flows.>` | limits | 24 h | **50 GB** por defecto (dimensionar, C-12) | 2 min | 1 MiB | 1 |
| `JOBS` | `horus.jobs.>` | **workqueue** | 7 d | 256 MiB | 20 min | 64 KiB | 3 |
| `DLQ` | `horus.dlq.>` | limits | 30 d | 5 GiB | 2 min | 1 MiB | 3 |

- **Telemetría con `limits` + `max_bytes` + `discard: old`** ([ADR-0006](./adr/0006-nats-jetstream-bus-de-eventos.md)): el
  stream es el buffer ante caídas de ClickHouse; al llenarse se descartan los lotes más antiguos y se registra el hueco.
- **TLM_FLOWS — objetivo ≥ 6 h a tasa pico** (requisito del Agente 2, [`storage.md`](./storage.md)). Con los 50 GB de
  [`architecture.md`](./architecture.md) §9.4 eso sólo se cumple hasta ~10 routers (1,8 MB/s ⇒ 6 h ≈ 39 GB); a 100 routers
  (18 MB/s) 6 h exigen ~390 GB de SSD. Por eso `max_bytes` es **parámetro de despliegue** (`HORUS_TLM_FLOWS_MAX_BYTES`)
  calculado como `tasa_pico × 6 h × 1,2`; si el disco no alcanza, el runbook documenta la autonomía real. Decisión de
  producto: C-12.
- `max_age` 24 h en TLM_FLOWS: más allá, re-ingerir flujos no aporta (agregados cerrados) y libera disco.
- `TLM_SNMP` 72 h: ~30 MB/h a 1.000 routers ⇒ cubre un fin de semana de caída del writer.
- `JOBS` `workqueue`: cada mensaje lo procesa un solo worker y se borra al confirmarse. La verdad del trabajo sigue en la
  fila `report_runs` de PostgreSQL (`analytics`).

### 3.3 Ajustes del servidor

- `jetstream.sync_interval`: por defecto `fsync` cada 2 min ⇒ con nodo único, un corte eléctrico puede perder mensajes ya
  confirmados. Para dominio y auditoría es recuperable (outbox de 7 días, §6.1); para telemetría es pérdida aceptada
  ([`architecture.md`](./architecture.md) §10.3). Probar `sync_interval: always` en Sprint 15 (C-11).
- `max_payload` 1 MiB (por defecto). Los lotes de telemetría se cortan en 512 KiB (§5.5).
- `max_file_store` de la cuenta JetStream ≥ suma de `max_bytes` + 20 %.

---

## 4. Consumidores

### 4.1 Reglas

- **Pull durables** (no push): control de flujo explícito (`Fetch`/`Consume` por lotes), escalado horizontal natural
  (las N réplicas de un servicio comparten el mismo durable = consumidores competidores; es el "queue group" de
  [`services.md`](./services.md)), backpressure implícito.
- Nombre `<servicio>-<propósito>` en kebab-case; `filter_subjects` múltiples (NATS ≥ 2.10).
- Cada servicio **crea/actualiza sus durables al arrancar** (`CreateOrUpdateConsumer`, configuración en código junto al
  handler). Excepción: `flows-ingester` se aprovisiona con el stream, para que exista antes del primer lote.
- `ack_policy: explicit`; se confirma **después** del efecto (commit en PostgreSQL, insert en ClickHouse). Tareas largas
  envían `InProgress()`.
- Resultado: **at-least-once** + consumidores idempotentes (§7) = efecto *exactly-once*.
- **Excepción `api-gateway`**: sin consumidores; suscripciones NATS core ([`api.md`](./api.md) §4.8), *best effort*.

### 4.2 Parámetros por familia

| Parámetro | Dominio | Telemetría | Jobs |
| --- | --- | --- | --- |
| `deliver_policy` | `all` en el primer arranque si construye proyección; `new` si sólo reacciona (notificar) | `all` | `all` |
| `ack_wait` | 30 s | 60 s | 5 min (+ `InProgress`) |
| `max_deliver` | 8 | 5 | 3 |
| `backoff` | `1s, 5s, 15s, 30s, 1m, 5m, 15m` | `5s, 30s, 2m, 5m` | `1m, 5m` |
| `max_ack_pending` | 64 (tolerante a orden) · 1 (orden estricto) | 1.000–5.000 | 1 por worker |
| Lote de `Fetch` | 1–32 | 10–100 lotes | 1 |

### 4.3 Orden y paralelismo

JetStream entrega en orden del stream, pero con paralelismo y reintentos el **procesamiento** puede desordenarse:

1. **Por defecto, tolerancia a desorden**: cada evento de agregado lleva `aggregate_version` (la columna `version` de la
   entidad). El consumidor aplica *last-writer-wins por versión*: si ya tiene una versión ≥, confirma y descarta.
2. **Orden estricto** (máquinas de estado derivadas de secuencias, cadena de hashes de auditoría): `max_ack_pending = 1`.
3. **Orden + paralelismo** (no en v1): particionado por el 5º token con *subject mapping* y un durable por partición.

Telemetría: sin garantías de orden; agregaciones conmutativas; cada registro trae su tiempo.

### 4.4 Dead-letter (DLQ)

JetStream no tiene DLQ nativa: al agotarse `max_deliver` deja de entregar el mensaje, emite
`$JS.EVENT.ADVISORY.CONSUMER.MAX_DELIVERIES.<STREAM>.<CONSUMER>`, y el mensaje sigue en el stream hasta su retención.

1. **Camino principal, en la librería de consumo** (`packages/go/natsx`, [`conventions.md`](./conventions.md)):
   - Error **permanente** (no decodifica, `schema_version` no soportada, invariante violada) → copia a
     `horus.dlq.<servicio>.<consumer>` y `Term()`.
   - Error **transitorio** → `Nak()` (aplica `backoff`).
   - Último intento (`NumDelivered == max_deliver`) aún fallido → copia a DLQ y `Term()`.
   - Headers añadidos: `Horus-Dlq-Original-Subject`, `Horus-Dlq-Stream`, `Horus-Dlq-Stream-Seq`, `Horus-Dlq-Consumer`,
     `Horus-Dlq-Attempts`, `Horus-Dlq-Error` (≤ 1 KiB, sin datos sensibles), `Horus-Dlq-Failed-At`; cuerpo y headers
     originales intactos.
2. **Red de seguridad**: un vigilante (goroutine en cada servicio para sus propios consumidores; centralizable en `alerts`
   desde el Sprint 11) escucha advisories `MAX_DELIVERIES` y `MSG_TERMINATED`; si el mensaje no está en la DLQ
   (`Nats-Msg-Id = dlq:<stream>:<seq>`), lo recupera con *direct get* y lo copia.

Operación: alerta `horus_dlq_messages_total{consumer} > 0`; CLI en `scripts/` para inspeccionar y **re-publicar** un
mensaje DLQ a su subject original (seguro por idempotencia).

### 4.5 Consumidores iniciales

| Durable | Stream | Filtros | Servicio | Propósito | Sprint |
| --- | --- | --- | --- | --- | --- |
| `auth-audit-<dominio>` (uno por stream) | cada `<DOMINIO>_EVENTS` | `horus.<dominio>.audit.recorded.*` | auth | Único escritor de `audit_log` (cadena de hashes, `max_ack_pending = 1`) | 1–2 |
| `snmp-inventory` | `DEVICES_EVENTS` | `horus.devices.router.>`, `horus.devices.interface.>` | snmp | Objetivos de sondeo; ante `credentials_rotated` refresca vía gRPC `GetPollingTarget` | 5 |
| `snmp-wireguard` | `WIREGUARD_EVENTS` | `horus.wireguard.peer.handshake_stale.*`, `…handshake_recovered.*` | snmp | Tercera señal del estado observado | 5 |
| `snmp-metrics-writer` | `TLM_SNMP` | `horus.telemetry.snmp.>` | snmp (rol metrics-writer) | Persistir en ClickHouse | 5 |
| `devices-snmp-state` | `SNMP_EVENTS` | `horus.snmp.router.>` | devices | Proyección `router_status_cache`; interfaces sugeridas | 5 |
| `wireguard-devices` | `DEVICES_EVENTS` | `horus.devices.router.created.*`, `…deleted.*` | wireguard | Sugerir/limpiar peer asociado (nunca borra solo) | 4 |
| `flows-ingester` | `TLM_FLOWS` | `horus.telemetry.flows.>` | flows (ingester) | Clasificar en ingesta con el snapshot y escribir `flows_raw` (escritor único) | 6 |
| `flows-inventory` | `DEVICES_EVENTS` | `router.>`, `interface.>`, `customer.>` | flows | Mapas exportador→router, ifIndex→interfaz/`flow_role`, IP→cliente (`customer.assigned/unassigned`) | 6 |
| `flows-snapshots` | `TRAFFIC_EVENTS`, `REPUTATION_EVENTS` | `horus.traffic.catalog.published.*`, `horus.reputation.snapshot.published.*` | flows | Recargar snapshots (doble buffer) | 7–8 |
| `detection-signals` | `SNMP_EVENTS`, `FLOWS_EVENTS` | `router.state_changed`, `exporter.*` | detection | Contexto de correlación | 8 |
| `alerts-<dominio>` (uno por stream) | `SNMP_`, `WIREGUARD_`, `FLOWS_`, `DETECTION_`, `REPORTING_EVENTS` | eventos que alimentan reglas, incl. `horus.snmp.poller.heartbeat.*` | alerts | `Alert Rule → Event → Alert → Notification` | 11 |
| `analytics-names` | `DEVICES_EVENTS` | `router.>`, `site.>`, `customer.>` | analytics | Caché de nombres | 9 |
| `analytics-reporting-worker` | `JOBS` | `horus.jobs.reporting.>` | analytics (reporting-worker) | Generar reportes | 12 |

> No existen `traffic-enricher`, `TLM_FLOWS_ENRICHED` ni un escritor de flujos en `analytics`: según
> [ADR-0015](./adr/0015-enriquecimiento-de-flujos-en-ingesta.md) el ingester de `flows` clasifica en ingesta y es el único
> escritor de `flows_raw`.

---

## 5. Sobre (envelope) y serialización

### 5.1 Campos

Inspirado en CloudEvents 1.0, en `snake_case` (no pretende compatibilidad 1:1 con el binding NATS de CloudEvents; el
mapeo es trivial si hiciera falta exportar).

| Campo | Tipo | Oblig. | Descripción |
| --- | --- | --- | --- |
| `id` | UUIDv7 | sí | Identidad del evento = `event_id` = `Nats-Msg-Id` = PK del outbox. |
| `type` | string | sí | `horus.<dominio>.<entidad>.<evento>`. |
| `source` | string | sí | `horus/<servicio>[/<rol>]` (`horus/flows/collector`). |
| `subject` | string | sí (dominio) | ID del agregado = 5º token. |
| `time` | RFC 3339 UTC, ms | sí | Cuándo ocurrió (commit / observación), no cuándo se publicó. |
| `schema_version` | entero | sí | Versión **mayor** del payload (= `vN` del paquete Protobuf). |
| `tenant_id` | UUID | sí | Tenant (fijo en v1). |
| `aggregate_type` | string | sí (dominio) | `router`, `peer`… |
| `aggregate_version` | entero | sí (dominio) | `version` de la entidad tras el cambio (orden + idempotencia). |
| `actor` | objeto | sí | Ver abajo. |
| `trace_parent` | string | no | W3C `traceparent` del contexto origen. |
| `correlation_id` | string | no | `X-Request-Id` de la petición original. |
| `causation_id` | UUIDv7 | no | `id` del evento que provocó éste (`finding.created → alert.opened`). |
| `data` | objeto | sí | Payload según su esquema. |

**`actor`** (requisitos de [`security.md`](./security.md) §6.5):

| Campo | Descripción |
| --- | --- |
| `type` | `user` \| `service` \| `system` |
| `id` | UUIDv7 del usuario, o `svc:<nombre>` para servicios, o `system:<proceso>` |
| `sid` | Opcional, sólo `type=user`: sesión |
| `via` | `session` \| `api_token` — distingue acciones hechas con API token |
| `api_token_id` | Sólo si `via=api_token` |

Sin `name` (dato personal; se resuelve por ID cuando haga falta mostrarlo), sin IP ni User-Agent (sólo en el payload de
auditoría, §8.11). **Nunca** tokens, permisos ni secretos.

```jsonc
{
  "id": "0192f0d1-1b2c-7e44-8a10-6b9c2d1e0f33",
  "type": "horus.devices.router.created",
  "source": "horus/devices",
  "subject": "0192f0c4-7a1e-7c3a-9b1d-2f6e8a4c1d55",
  "time": "2026-10-07T14:03:11.123Z",
  "schema_version": 1,
  "tenant_id": "01920000-0000-7000-8000-000000000001",
  "aggregate_type": "router",
  "aggregate_version": 1,
  "actor": { "type": "user", "id": "0192aaaa-...", "sid": "0192bbbb-...", "via": "session" },
  "trace_parent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
  "correlation_id": "0192cccc-...",
  "causation_id": null,
  "data": { "id": "0192f0c4-...", "version": 1, "site_id": "0192e111-...", "name": "rt-core-01" }
}
```

### 5.2 Headers NATS (todos los mensajes)

| Header | Valor |
| --- | --- |
| `Nats-Msg-Id` | `id` (dominio/jobs) o `batch_id` (telemetría) |
| `Content-Type` | `application/json` · `application/x-protobuf; proto=horus.events.flows.v1.FlowBatch` |
| `Horus-Type`, `Horus-Schema-Version`, `Horus-Tenant`, `Horus-Source`, `Horus-Time` | copia de los campos del sobre |
| `traceparent` | W3C (en telemetría sólo si la traza está muestreada) |

Telemetría: el sobre va **sólo en headers**; el cuerpo es el lote Protobuf (sin repetir el sobre por registro, filtrable
sin deserializar).

### 5.3 Serialización — recomendación

**Protobuf como único IDL; JSON (protojson, nombres proto `snake_case`) para dominio y jobs; Protobuf binario para
telemetría.**

- Dominio en JSON: volumen bajo (coste irrelevante), legible con `nats sub`/`nats stream view`, se guarda tal cual en el
  `jsonb` del outbox y el gateway lo reenvía al WebSocket casi sin transformar. El esquema sigue siendo Protobuf y
  `buf breaking` usa `WIRE_JSON`.
- Telemetría en Protobuf: 3–10× menos bytes/CPU en flujos, que dominan disco y ancho de banda de JetStream
  ([ADR-0005](./adr/0005-grpc-protobuf-interno.md)).
- Se descarta JSON Schema/Avro como segundo lenguaje de esquemas. Los tests de contrato de
  [`conventions.md`](./conventions.md) validan contra el Protobuf (protojson estricto) con *golden files* en
  `packages/events/examples/`.
- Reglas protojson: `int64/uint64` → string en JSON (estándar); timestamps RFC 3339; **estados como `string`** en los
  payloads de dominio (no enums Protobuf), para que añadir un valor no rompa consumidores JSON.

### 5.4 Versionado y compatibilidad

- **Compatible** (sin cambiar `schema_version`): añadir campos opcionales, añadir tipos de evento, añadir valores a estados
  documentados como abiertos. Los consumidores ignoran campos desconocidos y tratan valores desconocidos como "otro".
- **Incompatible** (quitar/renombrar/cambiar tipo o semántica): paquete `horus.events.<dominio>.v2`, `schema_version: 2`,
  **mismo subject**. El productor publica **ambas versiones** (dos mensajes, `id` distintos, mismo `causation_id`) durante
  ≥ max(30 días, 1 release). La librería declara versiones soportadas y confirma-y-omite las demás
  (`horus_events_skipped_version_total`).
- Prohibido reutilizar un `type` con otra semántica; tipos obsoletos → `deprecated` en el catálogo y retirada tras la ventana.

### 5.5 Lotes de telemetría

- Un mensaje = un lote: flujos ≤ 500 registros ([`architecture.md`](./architecture.md) §5.3) o 1 s; SNMP = un sondeo de un
  router. Siempre < **512 KiB** codificado.
- `batch_id` UUIDv7 = `Nats-Msg-Id`: los reenvíos desde spool no duplican dentro de la ventana (2 min); fuera de ella, el
  escritor usa `insert_deduplication_token = batch_id` en ClickHouse ([`database.md`](./database.md)).

### 5.6 Registro (`packages/events`)

```
packages/events/
├── catalog.yaml     # fuente de verdad del catálogo
├── streams/         # definiciones de streams (+ durables aprovisionados) aplicadas por infrastructure/nats
├── examples/        # golden files JSON por tipo (tests de productor y consumidor)
└── docs/            # catálogo navegable generado
```

Payloads Protobuf en `packages/protobuf/horus/events/<dominio>/v<N>/`; librería Go de publicación/consumo en
`packages/go/natsx` ([`conventions.md`](./conventions.md)).

```yaml
- type: horus.snmp.router.state_changed
  subject: horus.snmp.router.state_changed.{router_id}
  stream: SNMP_EVENTS
  family: domain              # domain | telemetry | job
  encoding: json
  schema: horus.events.snmp.v1.RouterStateChanged
  schema_version: 1
  producer: snmp
  consumers: [devices, alerts, detection, api-gateway]
  websocket_topics: [routers.status, "router.{router_id}"]
  pii: false
  status: stable              # draft | stable | deprecated
  since: sprint-5
```

CI valida: cada `type` tiene mensaje Protobuf y golden file; cada mensaje de `horus/events` está catalogado; el subject
cumple §2; un stream cubre el subject; el productor publica sólo en sus dominios.
Del catálogo se generan constantes Go, tipos TS de los payloads expuestos por WebSocket y la documentación.

### 5.7 Qué no va en un evento

- **Secretos**: contraseñas/hashes, claves privadas WireGuard, credenciales SNMP, tokens, URLs prefirmadas. Nunca.
- Más de 64 KiB: los eventos de entidad llevan el **estado completo de la entidad** (sus columnas propias + `version`,
  sin relaciones anidadas ni secretos); lo que no quepa se pide por gRPC/REST.
- PII innecesaria: email sólo en `auth.user.*`; IP/User-Agent del actor sólo en `*.audit.recorded`.

---

## 6. Publicación sin pérdida

### 6.1 Transactional outbox (servicios con PostgreSQL)

Decisión de [ADR-0016](./adr/0016-transactional-outbox.md); aquí el contrato fino.

1. En la **misma transacción** del cambio se inserta el evento en `<esquema>.outbox` (tabla de [`database.md`](./database.md)).
   Columnas que este contrato necesita: `id` (= `event_id`), `subject`, `aggregate_id`, `payload` (sobre completo,
   `jsonb`), `headers` (`jsonb`; **falta en el borrador de `database.md`**), `occurred_at`, `published_at`, `attempts` y
   una columna de orden **`seq bigint generated always as identity`** (también falta).
2. **Relay** (goroutine en el propio servicio, código en `packages/go/natsx`):
   - Activación `LISTEN/NOTIFY` + sondeo cada 1 s.
   - Selección `WHERE published_at IS NULL ORDER BY seq LIMIT 100 FOR UPDATE SKIP LOCKED`.
   - **Orden por `seq`, no por `id`**: UUIDv7 generado en aplicación puede desordenarse entre réplicas por desfase de
     reloj; `seq` refleja el orden de inserción en la base.
   - **Orden por agregado con varias réplicas**: `SKIP LOCKED` solo no lo garantiza (dos réplicas pueden tomar eventos
     consecutivos del mismo agregado). El relay omite filas cuyo `aggregate_id` tenga otra fila anterior pendiente
     (`NOT EXISTS (… o2.aggregate_id = o.aggregate_id AND o2.seq < o.seq AND o2.published_at IS NULL)`). En v1 (1
     réplica) basta un relay único con `pg_try_advisory_lock`.
   - Publica con `Nats-Msg-Id = id`, **espera el PubAck** y marca `published_at`. Ventana de 100 PubAck asíncronos.
   - NATS caído → backoff hasta 30 s; el negocio sigue; métricas `horus_outbox_pending`, `horus_outbox_oldest_age_seconds`
     (alerta > 5 min).
   - Crash entre publish y marcado → republica; JetStream deduplica en 20 min; después, idempotencia del consumidor.
3. **Retención**: filas publicadas 7 días ([ADR-0016](./adr/0016-transactional-outbox.md)) — permiten re-publicar tras una
   pérdida de datos de NATS (§9.2).

La auditoría usa el **mismo outbox**: el servicio inserta, en la misma transacción, el evento de negocio y su
`horus.<dominio>.audit.recorded` ([`security.md`](./security.md) §7.3).

### 6.2 Telemetría

Sin outbox. Publicador asíncrono con buffer local acotado (§9.3) y pérdida medida.

### 6.3 Productores de dominio sin PostgreSQL (`snmp`, `flows`)

`snmp` (estado en NATS KV) y `flows` no tienen PostgreSQL en v1: no hay transacción a la que atar un outbox.

- Publican sus eventos de dominio (y su auditoría, p. ej. `POST /routers/{id}/poll`) con **publicación síncrona
  confirmada** y, si NATS no responde, los guardan en un **spool en disco sin descarte** (pocos eventos: 1 GiB alcanza
  para semanas) que se drena en orden con el mismo `id`.
- **Reconciliación por estado**: `horus.snmp.router.state_changed` describe una transición cuyo resultado vive en el
  bucket KV `snmp_router_state`, con `version` monotónica por router. Si un evento se perdiera (disco del colector
  destruido), `devices` reconcilia al arrancar y cada 10 min comparando `router_status_cache` con
  `PollerService.GetRouterState` (gRPC).
- `horus.snmp.poller.heartbeat` es periódico (15 s) y se publica sin spool: su ausencia **es** la señal.

---

## 7. Idempotencia en consumidores

| Estrategia | Cuándo | Cómo |
| --- | --- | --- |
| **Inbox** | Efectos en PostgreSQL no idempotentes (abrir alerta, encolar notificación) | Tabla `inbox(consumer, event_id, processed_at, PK(consumer, event_id))`; en la **misma transacción** que el efecto `INSERT … ON CONFLICT DO NOTHING`; si no insertó → `Ack()` sin efecto. Limpieza a 30 d. (Definición física: [`database.md`](./database.md).) |
| **Upsert por versión** | Proyecciones (`router_status_cache`, cachés de inventario en `snmp`/`flows`) | `… ON CONFLICT DO UPDATE … WHERE excluded.version > t.version`; en memoria, comparar `aggregate_version`. |
| **Clave natural** | Auditoría | `audit_log.id = data.id` (único); duplicado ⇒ `Ack()` sin efecto. |
| **Dedupe en destino** | ClickHouse | `insert_deduplication_token = batch_id`; `ReplacingMergeTree(version)` donde aplique. |

Efectos externos (email/Telegram): inbox con estado `sending` antes de llamar al proveedor y clave de idempotencia del
proveedor = `event_id` cuando exista; se acepta un duplicado excepcional ante caída exacta entre envío y commit.

---

## 8. Catálogo inicial

Subject = `type.<entity_id>`. Se muestra sólo `data`. Telemetría en su representación JSON equivalente (viaja en
Protobuf). **Regla para entidades** (petición de [`database.md`](./database.md) §10): `*.created` y `*.updated` llevan el
**estado completo** de la entidad + `id` + `version`; `*.updated` añade `changed_fields`; `*.deleted` lleva el último
estado + `version` + `deleted_at`.

### 8.1 `auth` → `AUTH_EVENTS` (productor `auth`)

| Tipo | Consumidores | Sprint |
| --- | --- | --- |
| `horus.auth.user.created` / `.updated` / `.deleted` | alerts (destinatarios), analytics (nombres) | 2 |
| `horus.auth.user.disabled` / `.enabled` | **api-gateway** (cierra WS), alerts | 2 |
| `horus.auth.user.role_assignments_changed` | **api-gateway** (permisos de WS) | 2 |
| `horus.auth.role.created` / `.updated` / `.deleted` | **api-gateway** | 2 |
| `horus.auth.session.created` | detection (futuro: accesos anómalos) | 2 |
| `horus.auth.session.revoked` | **api-gateway** (cierra WS `4409`) | 1–2 |
| `horus.auth.login.failed` | detection/alerts (fuerza bruta) | 2 |
| `horus.auth.mfa.enabled` / `.disabled` | alerts (aviso al usuario) | 2 |
| `horus.auth.audit.recorded` | auth (`auth-audit-auth`) | 1–2 |

```jsonc
// horus.auth.user.created
{ "id": "0192...", "version": 1, "email": "noc@isp.example", "display_name": "NOC Turno A", "status": "active",
  "has_totp": false, "created_at": "2026-10-07T12:00:00Z" }

// horus.auth.user.role_assignments_changed
{ "id": "0192...", "version": 5,
  "assignments": [ { "role_id": "0192...", "scope": "site:0192..." } ],
  "previous_assignments": [ { "role_id": "0192...", "scope": "global" } ] }

// horus.auth.role.updated
{ "id": "0192...", "version": 3, "name": "noc_operator", "is_system": true,
  "permissions": ["devices.read", "snmp.read", "alerts.read", "alerts.ack"], "changed_fields": ["permissions"],
  "added_permissions": ["alerts.ack"], "removed_permissions": [] }

// horus.auth.session.revoked   (entity_id = session id; sin tokens)
{ "session_id": "0192...", "user_id": "0192...", "reason": "admin_revoked", "revoked_by": "0192..." }
// reason: logout | admin_revoked | password_changed | roles_changed | refresh_token_reuse | user_disabled | expired

// horus.auth.login.failed   (entity_id = user id, o UUID nulo si el usuario no existe)
{ "user_id": null, "username": "admin", "source_ip": "203.0.113.7", "reason": "invalid_credentials",
  "consecutive_failures": 4 }
```

### 8.2 `devices` → `DEVICES_EVENTS` (productor `devices`)

| Tipo | Consumidores | Sprint |
| --- | --- | --- |
| `horus.devices.site.created` / `.updated` / `.deleted` | flows, analytics, api-gateway | 3 |
| `horus.devices.router.created` / `.updated` / `.deleted` | **snmp**, flows, wireguard, analytics, api-gateway | 3 |
| `horus.devices.router.credentials_rotated` | **snmp** (recarga por gRPC; sin secretos) | 3 |
| `horus.devices.router.maintenance_started` / `_ended` | alerts (supresión), snmp, api-gateway | 3 |
| `horus.devices.interface.created` / `.updated` / `.deleted` | snmp, flows, analytics, api-gateway | 3/5 |
| `horus.devices.customer.created` / `.updated` / `.deleted` | flows, analytics, detection | 3/6 |
| `horus.devices.customer.assigned` / `.unassigned` (asignación de IP) | **flows** (mapa IP→cliente), detection | 3/6 |
| `horus.devices.audit.recorded` | auth | 3 |

> `customer.assigned/unassigned`: entidad `customer`, entity_id = `customer_id`; el `assignment_id` va en `data`.

```jsonc
// horus.devices.site.created
{ "id": "0192...", "version": 1, "name": "POP Centro", "parent_id": null, "address": "Av. Principal 123",
  "latitude": 19.4326, "longitude": -99.1332, "tags": ["pop"], "created_at": "2026-10-07T12:00:00Z" }

// horus.devices.router.created
{ "id": "0192...", "version": 1, "site_id": "0192...", "name": "rt-core-01", "management_ip": "10.0.0.1",
  "vendor_id": "0192...", "model_id": "0192...", "firmware_version": "7.16.1", "tags": ["core"], "group_ids": [],
  "snmp_enabled": true, "snmp_version": "v3", "poll_interval_seconds": 60, "flow_exporter_ips": ["10.0.0.1"],
  "in_maintenance": false, "created_at": "2026-10-07T12:00:00Z", "updated_at": "2026-10-07T12:00:00Z" }

// horus.devices.router.updated   (estado completo + changed_fields)
{ "id": "0192...", "version": 8, "site_id": "0192...", "name": "rt-core-01a", "management_ip": "10.0.0.1",
  "vendor_id": "0192...", "model_id": "0192...", "firmware_version": "7.16.1", "tags": ["core", "edge"], "group_ids": [],
  "snmp_enabled": true, "snmp_version": "v3", "poll_interval_seconds": 60, "flow_exporter_ips": ["10.0.0.1"],
  "in_maintenance": false, "created_at": "2026-10-07T12:00:00Z", "updated_at": "2026-10-07T15:00:00Z",
  "changed_fields": ["name", "tags"] }

// horus.devices.router.deleted
{ "id": "0192...", "version": 9, "site_id": "0192...", "name": "rt-core-01a", "deleted_at": "2026-10-07T16:00:00Z" }

// horus.devices.router.credentials_rotated   (sin secretos)
{ "id": "0192...", "version": 10, "credential_kind": "snmp_v3", "rotated_at": "2026-10-07T15:10:00Z" }

// horus.devices.interface.created
{ "id": "0192...", "version": 1, "router_id": "0192...", "if_index": 7, "name": "sfp-sfpplus1",
  "alias": "UPLINK-TRANSIT-A", "speed_bps": 10000000000, "flow_role": "upstream", "is_monitored": true }
// flow_role: valores de database.md (p. ej. upstream | customer_edge | core | management | unknown)

// horus.devices.customer.created
{ "id": "0192...", "version": 1, "external_ref": "CRM-88123", "display_name": "Cliente 88123",
  "site_id": "0192...", "plan": "fiber-300m", "status": "active", "created_at": "2026-10-07T12:00:00Z" }

// horus.devices.customer.assigned   (entity_id = customer_id)
{ "assignment_id": "0192...", "customer_id": "0192...", "realm_id": "0192...", "prefix": "100.64.12.34/32",
  "valid_from": "2026-10-07T13:55:02Z", "valid_to": null, "source": "radius", "router_id": "0192..." }

// horus.devices.customer.unassigned
{ "assignment_id": "0192...", "customer_id": "0192...", "realm_id": "0192...", "prefix": "100.64.12.34/32",
  "valid_from": "2026-10-07T13:55:02Z", "valid_to": "2026-10-07T20:10:44Z", "source": "radius", "router_id": "0192..." }
// source: manual | radius | dhcp | import
```

### 8.3 `wireguard` → `WIREGUARD_EVENTS` (productor `wireguard`, control)

| Tipo | Consumidores | Sprint |
| --- | --- | --- |
| `horus.wireguard.server.created` / `.updated` / `.deleted` | api-gateway | 4 |
| `horus.wireguard.peer.created` / `.updated` / `.deleted` | api-gateway | 4 |
| `horus.wireguard.peer.revoked` | api-gateway, alerts | 4 |
| `horus.wireguard.peer.key_rotated` | api-gateway, alerts | 4 |
| `horus.wireguard.peer.handshake_stale` | **snmp** (estado observado), alerts, api-gateway | 4 |
| `horus.wireguard.peer.handshake_recovered` | snmp, alerts, api-gateway | 4 |
| `horus.wireguard.audit.recorded` | auth | 4 |
| `horus.wireguard.peer_status.observed` (telemetría, `TLM_WIREGUARD`) | api-gateway (topic estado) | 4 |

> `handshake_stale` / `handshake_recovered` (nombres de [`services.md`](./services.md) y [`architecture.md`](./architecture.md)
> §10.4) sustituyen a `handshake_lost` / `handshake_restored` pedidos por el Agente 2: misma semántica, un solo par.
> `key.rotated` se modela como `peer.key_rotated` (la clave no tiene identidad propia). Cada *handshake* individual no es
> un evento: el agente reporta por gRPC cada 15 s y el control publica el lote `peer_status.observed`.

```jsonc
// horus.wireguard.peer.created   (sólo clave pública)
{ "id": "0192...", "version": 1, "server_id": "0192...", "router_id": "0192...", "status": "active",
  "public_key": "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=", "address": "10.200.0.15/32",
  "allowed_ips": ["10.200.0.15/32", "192.168.88.0/24"], "persistent_keepalive_seconds": 25,
  "created_at": "2026-10-07T12:00:00Z" }

// horus.wireguard.peer.key_rotated
{ "id": "0192...", "version": 4, "server_id": "0192...", "previous_public_key": "xTIB...8Dg=", "public_key": "q2Lk...7Ws=" }

// horus.wireguard.peer.revoked
{ "id": "0192...", "version": 5, "server_id": "0192...", "router_id": "0192...", "reason": "router_decommissioned" }

// horus.wireguard.peer.handshake_stale
{ "id": "0192...", "server_id": "0192...", "router_id": "0192...",
  "last_handshake_at": "2026-10-07T13:58:02Z", "stale_after_seconds": 180, "detected_at": "2026-10-07T14:01:05Z" }

// horus.wireguard.peer_status.observed   (telemetría; lote por hub cada 15 s)
{ "batch_id": "0192...", "server_id": "0192...", "observed_at": "2026-10-07T14:03:00Z",
  "peers": [ { "peer_id": "0192...", "endpoint": "198.51.100.20:51820", "last_handshake_at": "2026-10-07T14:02:41Z",
               "rx_bytes": "1048576", "tx_bytes": "524288" } ] }
```

### 8.4 `snmp` (productor `snmp`)

Dominio → `SNMP_EVENTS` (publicación §6.3):

| Tipo | Consumidores | Sprint |
| --- | --- | --- |
| `horus.snmp.router.state_changed` | **devices**, alerts, detection, api-gateway | 5 (ICMP quizá 3) |
| `horus.snmp.router.rebooted` | devices, alerts, api-gateway | 5 |
| `horus.snmp.router.interfaces_discovered` | devices (interfaces sugeridas), api-gateway | 5 |
| `horus.snmp.interface.oper_status_changed` | alerts, api-gateway | 5 |
| `horus.snmp.poller.heartbeat` (entity_id = UUIDv5 de la instancia) | alerts, api-gateway (topic `system`) | 5 |
| `horus.snmp.audit.recorded` | auth | 5 |

> `interfaces_discovered` corrige el orden de `discovered_interfaces` de [`services.md`](./services.md) (verbo al final).
> El **estado observado** es de `snmp` ([`services.md`](./services.md)); `devices` sólo lo proyecta y calcula el `status`
> efectivo (8 valores, tabla en [`api.md`](./api.md) §2.6). **No existe `horus.devices.router.status_changed`**: sería un
> segundo evento para el mismo hecho; los consumidores usan `state_changed` + `router.maintenance_started/_ended`. `stale` y `unknown` no
> los emite `snmp`: los deriva `devices`/UI cuando `last_observed_at` supera 3 intervalos o falta el heartbeat
> ([`architecture.md`](./architecture.md) §10.2).

```jsonc
// horus.snmp.router.state_changed
{ "router_id": "0192...", "site_id": "0192...", "version": 42,
  "previous_state": "online", "state": "offline", "reason": "tunnel_down",
  "signals": { "icmp": "fail", "snmp": "fail", "wireguard": "stale" },
  "changed_at": "2026-10-07T14:03:11Z" }
// state: online | warning | critical | degraded | offline
// reason: ok | cpu_high | temperature_high | snmp_unreachable | snmp_auth_failed | tunnel_down | host_unreachable_via_tunnel

// horus.snmp.router.rebooted
{ "router_id": "0192...", "previous_uptime_seconds": 1209600, "uptime_seconds": 95, "detected_at": "2026-10-07T14:20:00Z" }

// horus.snmp.router.interfaces_discovered
{ "router_id": "0192...", "interfaces": [ { "if_index": 7, "name": "sfp-sfpplus1", "alias": "UPLINK-TRANSIT-A",
  "type": "ethernet_csmacd", "speed_bps": 10000000000 } ], "discovered_at": "2026-10-07T14:05:00Z" }

// horus.snmp.interface.oper_status_changed   (entity_id = interface_id)
{ "interface_id": "0192...", "router_id": "0192...", "if_index": 7, "previous": "up", "current": "down",
  "observed_at": "2026-10-07T14:03:00Z" }

// horus.snmp.poller.heartbeat   (cada 15 s, sin spool)
{ "instance_id": "snmp-1", "shards_owned": 64, "targets": 312, "last_cycle_at": "2026-10-07T14:03:00Z",
  "poll_errors_last_cycle": 3 }
```

Telemetría → `TLM_SNMP`, con **deltas y tasas ya calculados** en el poller (contadores crudos incluidos para depuración):

| Tipo | Subject | Consumidores |
| --- | --- | --- |
| `horus.snmp.device.sampled` | `horus.telemetry.snmp.device.<router_id>` | snmp-metrics-writer, api-gateway, alerts (reglas de umbral) |
| `horus.snmp.interface.sampled` | `horus.telemetry.snmp.interface.<router_id>` | ídem |

```jsonc
// horus.snmp.device.sampled
{ "batch_id": "0192...", "router_id": "0192...", "sampled_at": "2026-10-07T14:03:00Z", "poll_duration_ms": 412,
  "uptime_seconds": 1209600, "cpu_percent": 13.2, "memory_used_bytes": "1288490188", "memory_total_bytes": "2147483648",
  "memory_percent": 60.0, "temperature_celsius": 47, "firmware": "7.16.1", "icmp_rtt_ms": 8.4, "icmp_loss_ratio": 0.0 }

// horus.snmp.interface.sampled   (un lote por sondeo de router)
{ "batch_id": "0192...", "router_id": "0192...", "sampled_at": "2026-10-07T14:03:00Z", "interval_seconds": 60,
  "interfaces": [ { "interface_id": "0192...", "if_index": 7, "oper_status": "up", "speed_bps": 10000000000,
    "rx_bps": 4213456789.5, "tx_bps": 812345678.2, "rx_pps": 402113.0, "tx_pps": 98122.0,
    "rx_bytes_delta": "31600925921", "tx_bytes_delta": "6092592586",
    "rx_errors_delta": 0, "tx_errors_delta": 0, "rx_drops_delta": 12, "tx_drops_delta": 0,
    "counter_discontinuity": false, "rx_octets_raw": "918273645512", "tx_octets_raw": "123456789012" } ] }
```

`counter_discontinuity = true` (reinicio, wrap de Counter32, primer sondeo) ⇒ deltas y tasas `null` en ese intervalo
([`architecture.md`](./architecture.md) §10.2).

### 8.5 `flows` (productor `flows`)

Telemetría → `TLM_FLOWS`: `horus.flows.batch.received` en `horus.telemetry.flows.batch.<router_id>`. Publica el
**collector**; consume el **ingester**, que clasifica en ingesta con el snapshot del catálogo y escribe `flows_raw` con
`catalog_version` ([ADR-0015](./adr/0015-enriquecimiento-de-flujos-en-ingesta.md)). No hay salto intermedio por
`traffic-intelligence` ni flujo enriquecido en NATS.

Los registros son **exactamente** el registro canónico de [`traffic-model.md`](./traffic-model.md) §3; el lote añade
`batch_id` y el contexto común del exportador. `bytes`/`packets` viajan **sin escalar** junto con `sampling_rate`; la
corrección `bytes_est = bytes × N` la hace el ingester ([`traffic-model.md`](./traffic-model.md) §4).

```jsonc
// horus.events.flows.v1.FlowBatch (Protobuf; representación JSON)
{ "batch_id": "0192...", "collector_id": "flows-collector-1", "router_id": "0192...",
  "exporter_ip": "10.0.0.1", "sampling_rate": 1000,
  "received_from": "2026-10-07T14:03:00.000Z", "received_to": "2026-10-07T14:03:00.998Z",
  "records": [ {
    "exporter_ip": "10.0.0.1", "observation_domain_id": 0,
    "flow_start": "2026-10-07T14:02:31.120Z", "ts": "2026-10-07T14:02:59.870Z",
    "src_ip": "100.64.12.34", "dst_ip": "157.240.25.35", "src_port": 51544, "dst_port": 443,
    "protocol": 6, "tcp_flags": 27, "bytes": "1840", "packets": "13",
    "input_if_index": 12, "output_if_index": 7, "flow_direction": "egress",
    "src_as": 0, "dst_as": 32934, "next_hop": "10.0.0.254", "vlan_id": 0,
    "post_nat_src_ip": null, "post_nat_src_port": null,
    "sampling_rate": 1000, "flow_source": "ipfix", "batch_id": "0192..." } ] }
```

Notas: `sampling_rate` a nivel de lote es el valor por defecto del exportador; el del registro (si existe) prevalece. En
Protobuf las IPs son `bytes` (4/16), `bytes`/`packets` `uint64`, opcionales con `optional`. `batch_id` =
`insert_deduplication_token`. Registros sin plantilla se descartan y se cuentan (`horus_flows_dropped_total{reason="no_template"}`).

Dominio → `FLOWS_EVENTS` (publicación §6.3):

| Tipo | Entity | Consumidores |
| --- | --- | --- |
| `horus.flows.exporter.discovered` | exportador (UUIDv5 de la IP) | api-gateway (sugerir alta), alerts |
| `horus.flows.exporter.silent` | router | alerts, detection |
| `horus.flows.exporter.recovered` | router | alerts, detection |
| `horus.flows.collector.data_gap` | collector (UUIDv5 del nombre) | alerts, analytics (cobertura) |

```jsonc
// horus.flows.exporter.silent
{ "router_id": "0192...", "exporter_ip": "10.0.0.1", "last_flow_at": "2026-10-07T13:57:40Z", "silent_after_seconds": 300 }

// horus.flows.collector.data_gap
{ "collector_id": "flows-collector-1", "from": "2026-10-07T14:10:00Z", "to": "2026-10-07T14:22:13Z",
  "dropped_batches": 731, "dropped_records_estimated": 365500, "reason": "bus_unavailable" }
// reason: bus_unavailable | local_buffer_full | stream_full
```

### 8.6 `traffic` → `TRAFFIC_EVENTS` (productor `traffic-intelligence`)

| Tipo | Consumidores |
| --- | --- |
| `horus.traffic.catalog.published` (entity = id de la versión de catálogo) | **flows** (ingester recarga), analytics (diccionario `ip_trie`), detection |
| `horus.traffic.rule.updated` | analytics |
| `horus.traffic.audit.recorded` | auth |

```jsonc
// horus.traffic.catalog.published
{ "catalog_version_id": "0192...", "catalog_version": 17,
  "artifact_object_key": "catalog-snapshots/v17/catalog.bin", "sha256": "9f86d081884c7d65...",
  "size_bytes": 48211234, "format_version": 1, "published_at": "2026-10-07T06:00:00Z",
  "stats": { "prefixes": 982113, "asns": 74210, "services": 214, "categories": 18 } }
```

El snapshot se descarga de MinIO y se verifica con `sha256`; el evento nunca lleva URL prefirmada.

### 8.7 `reputation` → `REPUTATION_EVENTS` (productor: módulo `reputation` de `detection`)

| Tipo | Consumidores |
| --- | --- |
| `horus.reputation.snapshot.published` | flows (marcado en ingesta, si se adopta), detection |
| `horus.reputation.source.refreshed` | alerts (fallo de feeds), api-gateway |

```jsonc
// horus.reputation.snapshot.published
{ "snapshot_id": "0192...", "snapshot_version": 233, "artifact_object_key": "reputation-snapshots/v233/rep.bin",
  "sha256": "2c26b46b68ffc68f...", "entries": 182733, "sources": ["abuse_ch_feodo", "spamhaus_drop", "tor_exits"],
  "published_at": "2026-10-07T06:10:00Z" }
```

### 8.8 `detection` → `DETECTION_EVENTS` (productor `detection`)

| Tipo | Consumidores | Sprint |
| --- | --- | --- |
| `horus.detection.finding.created` | **alerts**, api-gateway | 8 |
| `horus.detection.finding.updated` / `.resolved` | alerts, api-gateway | 8 |
| `horus.detection.score.changed` (entity = `customer_id`) | alerts, analytics | 10 |
| `horus.detection.audit.recorded` | auth | 8 |

```jsonc
// horus.detection.finding.created   (correlación explicable, no "IP en lista = malware")
{ "id": "0192...", "version": 1, "kind": "suspected_botnet_c2", "category": "security", "severity": "high",
  "confidence": 0.82, "subject_type": "customer", "customer_id": "0192...", "router_id": "0192...", "site_id": "0192...",
  "window_from": "2026-10-07T13:00:00Z", "window_to": "2026-10-07T14:00:00Z",
  "reasons": [
    { "code": "reputation_hit", "detail": "3 destinos en feed C2", "weight": 0.40 },
    { "code": "periodic_beaconing", "detail": "intervalo 60 s ±2 s durante 50 min", "weight": 0.30 },
    { "code": "unusual_port", "detail": "TCP/8443 a ASN sin historial", "weight": 0.12 } ],
  "min_sampling_rate": 1 }

// horus.detection.score.changed   (sólo al cambiar de clase o superar umbral)
{ "customer_id": "0192...", "model_version": "scoring-v3", "previous_class": "residential", "class": "commercial",
  "confidence": 0.87, "scores": { "residential": 0.13, "commercial": 0.87, "security": 0.05, "anomaly": 0.21 },
  "reasons": ["31 dispositivos detrás del CPE", "uso sostenido 08:00–19:00 L–S", "ratio upload/download 0,62"] }
```

### 8.9 `alerts` → `ALERTS_EVENTS` (productor `alerts`)

| Tipo | Consumidores | Sprint |
| --- | --- | --- |
| `horus.alerts.alert.opened` | api-gateway | 11 |
| `horus.alerts.alert.acknowledged` | api-gateway | 11 |
| `horus.alerts.alert.resolved` | api-gateway | 11 |
| `horus.alerts.notification.sent` / `.failed` | api-gateway (topic `me`) | 11 |
| `horus.alerts.audit.recorded` | auth | 11 |

```jsonc
// horus.alerts.alert.opened
{ "id": "0192...", "version": 1, "rule_id": "0192...", "rule_version": 3, "severity": "critical", "state": "firing",
  "summary": "Router rt-core-01 offline (tunnel_down)", "resource_type": "router", "resource_id": "0192...",
  "site_id": "0192...", "source_event_type": "horus.snmp.router.state_changed", "source_event_id": "0192...",
  "started_at": "2026-10-07T14:03:12Z" }

// horus.alerts.alert.acknowledged
{ "id": "0192...", "version": 2, "state": "acknowledged", "acknowledged_by": "0192...", "comment": "Corte de fibra reportado" }

// horus.alerts.alert.resolved
{ "id": "0192...", "version": 3, "state": "resolved", "resolution": "auto", "resolved_by": null, "duration_seconds": 1440 }

// horus.alerts.notification.sent
{ "id": "0192...", "alert_id": "0192...", "channel": "telegram", "recipient_user_id": "0192..." }
```

### 8.10 `analytics` y `reporting`

| Tipo | Stream | Productor | Consumidores |
| --- | --- | --- | --- |
| `horus.reporting.report.completed` | `REPORTING_EVENTS` | analytics (reporting-worker) | alerts (notificar), api-gateway (topic `me`) |
| `horus.reporting.report.failed` | `REPORTING_EVENTS` | ídem | alerts, api-gateway |
| `horus.reporting.audit.recorded` | `REPORTING_EVENTS` | ídem (exportaciones: quién, filtro, filas) | auth |
| `horus.analytics.archive.completed` | `ANALYTICS_EVENTS` | analytics (archiver) | alerts |
| `horus.analytics.audit.recorded` | `ANALYTICS_EVENTS` | analytics | auth |
| `horus.jobs.reporting.generate_report` (job) | `JOBS` | analytics (API) | analytics (reporting-worker) |

> `report.completed` en el dominio `reporting` (no `analytics.report.completed` de [`services.md`](./services.md)), por la
> regla E3.

```jsonc
// horus.reporting.report.completed   (entity_id = id de la ejecución)
{ "id": "0192...", "version": 3, "report_definition_id": "0192...", "kind": "consumption_by_customer",
  "format": "pdf", "period_from": "2026-09-01T00:00:00Z", "period_to": "2026-10-01T00:00:00Z",
  "object_key": "reports/2026/10/0192....pdf", "size_bytes": 482113, "sha256": "9f86d0...",
  "requested_by": "0192..." }
```

### 8.11 Auditoría: `horus.<dominio>.audit.recorded`

Cada servicio publica su auditoría vía **outbox** (o spool en `snmp`/`flows`) en `horus.<dominio>.audit.recorded.<entry_id>`,
dentro del stream de su dominio; **sólo `auth` la persiste** (`audit_log`, cadena de hashes; [`security.md`](./security.md)
§7.3). Payload = formato de registro de [`security.md`](./security.md) §7.2 **sin** `prev_hash`/`hash` (los calcula `auth`).
Es el único evento que lleva IP y User-Agent.

> Este nombre sustituye a `horus.audit.entry.recorded` (Agente 2) y `horus.audit.record.created` (versión previa de
> `security.md`); la convención final es la de [`architecture.md`](./architecture.md) §6.1 y [`security.md`](./security.md) §7.3.

```jsonc
// horus.devices.audit.recorded
{ "id": "0192...", "occurred_at": "2026-10-07T15:10:00.120Z", "source_service": "devices",
  "actor": { "type": "user", "id": "0192...", "sid": "0192...", "via": "session" },
  "ip": "198.51.100.4", "user_agent": "Mozilla/5.0 ...", "action": "devices.credentials.write",
  "resource_type": "router", "resource_id": "0192...", "scope": "site:0192...",
  "outcome": "success", "reason": null,
  "changes": { "credential_kind": ["snmp_v2c", "snmp_v3"] },
  "request_id": "0192...", "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736" }
```

---

## 9. Fallos

### 9.1 NATS se reinicia o cae (segundos–horas)

| Componente | Comportamiento |
| --- | --- |
| Streams (`file`) | Persisten: mensajes, estado de durables (ack floor, pendientes) y ventanas de dedupe. |
| Servicios con outbox | El relay no recibe PubAck → backoff; eventos y auditoría se acumulan en PostgreSQL. **Pérdida de dominio: cero.** Las APIs siguen. |
| `snmp` / `flows` (dominio) | Spool en disco sin descarte (§6.3); se drena en orden con los mismos `id`. |
| Colectores (telemetría) | Buffer local (§9.3); al volver se drena con los mismos `batch_id`. |
| Durables | `nats.go` reconecta; reanudan desde su ack floor; los no confirmados se reentregan; duplicados absorbidos (§7). |
| Gateway | Pierde lo publicado por core NATS durante el corte (por diseño); envía `realtime_status: degraded` y el frontend hace *polling* REST cada 30 s; al volver, `ok` + resincronización REST. |
| Readiness | NATS se reporta `degraded` en `/readyz`; nadie deja de servir lo que no depende de él. |
| Propagación entre servicios | Se retrasa (un router recién creado no se sondea hasta que NATS vuelve). |

### 9.2 Pérdida de datos de NATS (disco dañado, `sync_interval` + corte eléctrico)

- **Dominio y auditoría**: el outbox conserva 7 días. Runbook ([`disaster-recovery.md`](./disaster-recovery.md)):
  re-publicar desde el outbox de cada servicio los eventos con `published_at` ≥ (último instante sano − margen); los
  consumidores deduplican por `event_id`/versión. `snmp_router_state` (KV) se reconstruye en el siguiente ciclo de sondeo
  y `devices` reconcilia.
- **Telemetría**: lo no escrito aún en ClickHouse se pierde; se registra como hueco de cobertura.
- Streams y durables se recrean con el job idempotente de aprovisionamiento.

### 9.3 Buffer local en colectores

Librería común (`packages/go/natsx`):

```
lectura (sondeo SNMP / socket UDP) ──► cola en memoria acotada ──► publicador async JetStream (ventana de PubAck)
                                              │ NATS no responde o cola > umbral
                                              ▼
                                       spool en disco (segmentos append-only)  ── al reconectar: drena FIFO
```

| Colector | Memoria | Spool en disco | Al llenarse |
| --- | --- | --- | --- |
| `snmp` (telemetría) | 32 MB (horas a 1.000 routers) | 1 GiB / 24 h | descarta lo **más antiguo**; métrica de huecos |
| `snmp` / `flows` (dominio y auditoría) | — | 1 GiB, **sin descarte** | alerta; nunca descarta (§6.3) |
| `flows` collector | 256 MB (~2,3 min a 100 routers pico) | 20 GB / 2 h (C-12) | descarta lo **más nuevo** (`horus_flows_dropped_total{reason="bus_unavailable"}`) y emite `horus.flows.collector.data_gap` al recuperarse |
| `wireguard` (peer_status) | 100 lotes | — | descarta: el siguiente lote reemplaza el estado |

- Descarte en flujos alineado con [`architecture.md`](./architecture.md) §10.3 ("se descartan los lotes nuevos"): conserva
  un periodo contiguo antes del corte en vez de huecos dispersos.
- El bucle de lectura UDP **nunca** se bloquea por NATS.
- El spool vive en un volumen local del colector (no en el NAS), `fsync` por segmento.
- Drenado a ≤ 2× la tasa nominal para no saturar consumidores que también se recuperan.
- Reenvíos fuera de la ventana de dedupe (2 min) se deduplican en ClickHouse por `batch_id`.

### 9.4 Otros escenarios (resumen)

- **ClickHouse cae**: `flows-ingester` y `snmp-metrics-writer` no confirman; `TLM_*` acumulan hasta `max_bytes`
  (autonomía en [`architecture.md`](./architecture.md) §9.4 y §3.2 de este documento); al volver, drenado.
- **SNMP se detiene**: sin `poller.heartbeat` → alerta de infraestructura (Prometheus) y topic `system` en la UI; `devices`
  muestra `stale`, nunca `offline` por ausencia de monitoreo.
- **Un router desaparece**: `snmp` correlaciona ICMP + SNMP + `handshake_stale` → `router.state_changed` (`offline`,
  razón) → `devices` (proyección), `alerts` (Sprint 11), gateway (UI); `flows` publica `exporter.silent`.
- **Consumidor caído > 30 d**: arranca con resincronización por snapshot gRPC del dueño en lugar de replay.

---

## 10. Observabilidad de eventos (contrato mínimo)

Detalle en [`observability.md`](./observability.md). La librería común expone:

- `horus_events_published_total{type}`, `horus_events_publish_errors_total{type}`, `horus_outbox_pending`,
  `horus_outbox_oldest_age_seconds`, `horus_spool_bytes{kind}`.
- `horus_events_consumed_total{consumer,type,result=ack|nak|term|dlq|skipped}`, `horus_events_processing_seconds{consumer}`,
  `horus_consumer_pending{stream,consumer}` (num_pending), `horus_dlq_messages_total{consumer}`.
- `horus_telemetry_buffer_bytes{collector}`, `horus_telemetry_dropped_total{collector,reason}`.
- Logs: `event_id`, `subject`, `actor_id` (nunca nombre ni email del actor).
- Trazas: span `publish <type>` / `process <type>` enlazados por `trace_parent`; en telemetría por lotes se usa *span link*.
