# Contrato de eventos NATS JetStream — Horus Flow

> Estado: **propuesta Sprint 0 — documento crítico** (uno de los tres que [`vision.md`](./vision.md) exige cerrar antes de
> fijar los microservicios) · Dueño: Agente 3 (contratos e integración)
>
> Relacionados: [`api.md`](./api.md) (REST, WebSocket, gRPC), [`architecture.md`](./architecture.md) /
> [`services.md`](./services.md) (Agente 1), [`database.md`](./database.md) / [`traffic-model.md`](./traffic-model.md) /
> [`storage.md`](./storage.md) (Agente 2), [`security.md`](./security.md) / [`observability.md`](./observability.md) /
> [`disaster-recovery.md`](./disaster-recovery.md) (Agente 4), [`open-questions/contracts.md`](./open-questions/contracts.md).

## 0. Resumen de decisiones

| # | Decisión |
| --- | --- |
| E1 | Dos familias de mensajes con reglas distintas: **eventos de dominio** (bajo volumen, durables, contrato estricto, outbox) y **telemetría** (alto volumen, retención corta, publicación directa con buffer local). |
| E2 | Subject de dominio: `horus.<dominio>.<entidad>.<evento>.<entity_id>`. Los 4 primeros tokens son el **tipo** del evento; el 5º es la clave del agregado. Telemetría: `horus.telemetry.<fuente>.<tipo>.<clave>`. |
| E3 | Un stream por dominio productor para eventos (`<DOMINIO>_EVENTS`), un stream por fuente de telemetría (`TLM_*`), un stream `DLQ`. Todos `file`. Dominio: `limits` 30 d. Flujos: `interest` con tope de bytes. |
| E4 | Consumidores **pull durables** por servicio y propósito, `AckExplicit`, `MaxDeliver` + `BackOff`, DLQ implementada por el consumidor (+ vigilante de advisories como red de seguridad). |
| E5 | Sobre estilo CloudEvents en `snake_case` con `id` UUIDv7, `tenant_id`, `actor`, `trace_parent`, `aggregate_version`. |
| E6 | **Protobuf como único lenguaje de esquema**; codificación JSON (protojson) para eventos de dominio y Protobuf binario para telemetría. |
| E7 | Publicación de dominio **siempre vía transactional outbox** en PostgreSQL; `Nats-Msg-Id` = `id` del evento; consumidores idempotentes (inbox o upsert por versión). |
| E8 | Sin token de tenant en subjects en v1: `tenant_id` va en el sobre; multi-tenant futuro con **NATS Accounts** por tenant. |
| E9 | Registro de eventos en `packages/events/catalog.yaml`; payloads en `packages/protobuf/horus/events/<dominio>/v1`. |

---

## 1. Eventos de dominio vs telemetría

| Aspecto | Evento de dominio | Telemetría |
| --- | --- | --- |
| Qué es | Un hecho de negocio ocurrido: "se creó un router", "se revocó un peer", "se abrió una alerta". | Una observación periódica o masiva: resultado de un sondeo SNMP, un lote de flujos, handshakes WireGuard. |
| Volumen | Decenas a cientos por minuto. | Miles a cientos de miles de registros por segundo (flujos). |
| Origen | Servicio con PostgreSQL, dentro de una transacción. | Colectores (`snmp`, `flows`, `wireguard`) o pipelines (`traffic-intelligence`). |
| Publicación | Transactional outbox (§6). Nunca se pierde. | Directa a JetStream con buffer local acotado (§9.3). Puede perderse bajo fallo prolongado, de forma medida. |
| Codificación | JSON (protojson) con sobre completo en el cuerpo. | Protobuf binario; sobre en headers NATS; registros **en lotes**. |
| Retención | 30 días (re-proyección, nuevos consumidores, auditoría técnica). | Horas (buffer ante caídas de consumidores/ClickHouse). |
| Contrato | Estricto, catalogado, `buf breaking` WIRE_JSON. | Estricto en esquema, pero semántica "mejor esfuerzo". |
| Orden | Por agregado vía `aggregate_version`. | Irrelevante salvo por `time`; los consumidores toleran desorden. |
| Visibilidad WebSocket | Canales clase *evento* (reanudables). | Canales clase *estado* (último valor, coalescencia). |

Regla de oro: **un evento de dominio nunca transporta series de telemetría y la telemetría nunca dispara efectos de
negocio directamente**; si una observación cambia el estado de negocio (p. ej. un router deja de responder), el servicio
dueño lo detecta y emite un evento de dominio (`horus.snmp.router.unreachable`).

---

## 2. Convención de subjects

### 2.1 Eventos de dominio

```
horus.<dominio>.<entidad>.<evento>.<entity_id>
  │      │         │         │          └── UUIDv7 del agregado (clave de orden/filtrado)
  │      │         │         └──────────── verbo en pasado, snake_case: created, status_changed, key_rotated
  │      │         └────────────────────── entidad en singular, snake_case: router, peer, alert, session
  │      └──────────────────────────────── dominio (tabla 2.3)
  └─────────────────────────────────────── prefijo fijo del producto
```

Ejemplos: `horus.devices.router.created.0192f0c4-7a1e-7c3a-9b1d-2f6e8a4c1d55`,
`horus.wireguard.peer.revoked.0192f2aa-...`.

- El **`type`** del evento = los 4 primeros tokens (`horus.devices.router.created`). El subject se construye siempre
  como `type + "." + entity_id`, nunca a mano.
- **Por qué el 5º token** (refinamiento de la convención compartida `horus.<dominio>.<entidad>.<evento>`):
  1. Permite consumidores filtrados por entidad sin deserializar (`horus.devices.*.*.<router_id>` para todo lo de un
     router; usado por el gateway y por reanudaciones).
  2. Habilita en el futuro `max_msgs_per_subject` (último estado por entidad) y particionado determinista por clave
     (`subject mapping` con `partition()`) si se necesita orden estricto con paralelismo.
  3. Cardinalidad acotada (miles de routers/peers): sin impacto en el servidor.
- Filtros típicos: `horus.devices.router.>` (todos los eventos de routers), `horus.devices.*.created.*`,
  `horus.auth.session.revoked.*`.
- Tokens: sólo `[a-z0-9_-]`; sin puntos dentro de un token; sin mayúsculas. Tamaño de subject ≤ 128 bytes.

### 2.2 Telemetría

```
horus.telemetry.<fuente>.<tipo>.<clave>
```

| Subject | Clave | Contenido |
| --- | --- | --- |
| `horus.telemetry.snmp.device.<router_id>` | router | Resultado de un sondeo de métricas de dispositivo |
| `horus.telemetry.snmp.interfaces.<router_id>` | router | Lote de contadores de todas las interfaces de un sondeo |
| `horus.telemetry.wireguard.handshake.<server_id>` | servidor WG | Lote de handshakes/contadores de peers en una lectura |
| `horus.telemetry.flows.raw.<router_id>` | router exportador (o `unregistered`) | Lote de flujos normalizados sin enriquecer |
| `horus.telemetry.flows.enriched.<router_id>` | router exportador | Lote de flujos enriquecidos (ASN, servicio, categoría, cliente) |

- Se unifica toda la telemetría bajo `horus.telemetry.>` (en lugar de `horus.flows.raw.*` sueltos) para que una sola
  regla de permisos NATS, de monitoreo y de streams separe el tráfico masivo del de dominio.
- La clave al final permite al gateway suscribirse a la telemetría de un solo router
  (`horus.telemetry.snmp.*.<router_id>`).

### 2.3 Dominios (token 2) y servicios productores

| Token | Servicio | Stream de dominio |
| --- | --- | --- |
| `auth` | `auth` | `AUTH_EVENTS` |
| `devices` | `devices` | `DEVICES_EVENTS` |
| `wireguard` | `wireguard` | `WIREGUARD_EVENTS` |
| `snmp` | `snmp` | `SNMP_EVENTS` |
| `flows` | `flows` | `FLOWS_EVENTS` |
| `traffic` | `traffic-intelligence` | `TRAFFIC_EVENTS` |
| `reputation` | `reputation` | `REPUTATION_EVENTS` |
| `detection` | `detection` | `DETECTION_EVENTS` |
| `alerts` | `alerts` | `ALERTS_EVENTS` |
| `analytics` | `analytics` | `ANALYTICS_EVENTS` (reservado; v1 no publica) |
| `reporting` | `reporting` | `REPORTING_EVENTS` |

`traffic` es el único token que no coincide con el nombre del servicio, para acortar subjects de alto uso.
**Regla de propiedad**: sólo el servicio dueño publica en `horus.<su-dominio>.>` (aplicado con permisos NATS por
usuario de servicio; ver [`security.md`](./security.md)).

Subjects reservados: `horus.dlq.>` (dead-letter), `horus.telemetry.>`, `$JS.*`/`$SYS.*` (NATS).

### 2.4 Multi-tenancy

v1 tiene una sola organización (un ISP). **Decisión: no hay token de tenant en el subject**; `tenant_id` va en el sobre
(y en el header `Horus-Tenant`) con el UUID fijo de la organización.

Justificación: el mecanismo idiomático de NATS para aislar tenants es **una cuenta (Account) por tenant**: cada cuenta
tiene su propio espacio de subjects y sus propios streams, sin compartir datos salvo exports/imports explícitos. Con ese
modelo los subjects no necesitan tenant, así que añadirlo ahora sólo alargaría todos los filtros sin beneficio. Si en
el futuro se prefiriera un único account compartido, se puede insertar el token con *subject mapping* del servidor
(`horus.> → horus.<tenant>.>`) sin cambiar a los productores. Llevar `tenant_id` en el sobre desde el día uno garantiza
que los datos persistidos (outbox, ClickHouse, DLQ) ya sean atribuibles.

---

## 3. Streams

Todos con `storage: file`, `discard: old`, `allow_direct: true` (lecturas directas para reanudación y DLQ),
`max_msg_size` indicado. **Réplicas**: `1` en v1 (un nodo NATS en Docker Compose); el diseño asume `3` cuando haya
clúster (Sprint 14). Los streams se definen **declarativamente** en `packages/events/streams/` y los aplica un job de
aprovisionamiento en `infrastructure/nats/` (los servicios **no** crean streams, para evitar deriva de configuración).

### 3.1 Streams de eventos de dominio

| Stream | Subjects | Retención | `max_age` | `max_bytes` | Dedupe (`duplicate_window`) | `max_msg_size` |
| --- | --- | --- | --- | --- | --- | --- |
| `AUTH_EVENTS` | `horus.auth.>` | limits | 30 d | 1 GiB | 20 min | 64 KiB |
| `DEVICES_EVENTS` | `horus.devices.>` | limits | 30 d | 2 GiB | 20 min | 64 KiB |
| `WIREGUARD_EVENTS` | `horus.wireguard.>` | limits | 30 d | 1 GiB | 20 min | 64 KiB |
| `SNMP_EVENTS` | `horus.snmp.>` | limits | 30 d | 2 GiB | 20 min | 64 KiB |
| `FLOWS_EVENTS` | `horus.flows.>` | limits | 30 d | 1 GiB | 20 min | 64 KiB |
| `TRAFFIC_EVENTS` | `horus.traffic.>` | limits | 30 d | 1 GiB | 20 min | 64 KiB |
| `REPUTATION_EVENTS` | `horus.reputation.>` | limits | 30 d | 1 GiB | 20 min | 64 KiB |
| `DETECTION_EVENTS` | `horus.detection.>` | limits | 30 d | 2 GiB | 20 min | 64 KiB |
| `ALERTS_EVENTS` | `horus.alerts.>` | limits | 30 d | 2 GiB | 20 min | 64 KiB |
| `REPORTING_EVENTS` | `horus.reporting.>` | limits | 30 d | 512 MiB | 20 min | 64 KiB |
| `ANALYTICS_EVENTS` | `horus.analytics.>` | limits | 30 d | 512 MiB | 20 min | 64 KiB |

- **Por qué `limits` y no `interest`/`workqueue`**: varios servicios consumen el mismo evento, y queremos poder crear un
  consumidor nuevo que haga *backfill* o reconstruir una proyección (p. ej. dimensiones de ClickHouse) re-leyendo 30 días.
  `workqueue` prohíbe consumidores solapados; `interest` borra lo ya consumido y rompe el replay.
- **Por qué un stream por dominio** y no uno global: retención y tamaño independientes, aislamiento ante un productor
  desbocado, permisos por stream y recuperación selectiva. Coste: el gateway abre un consumidor ordenado por stream
  (pocos, barato).
- **Ventana de dedupe 20 min**: cubre reinicios del relay de outbox (que republica lo no confirmado con el mismo
  `Nats-Msg-Id`). Coste de memoria despreciable con este volumen.
- La retención de 30 d **no** es el sistema de registro: la verdad está en PostgreSQL. Para reconstrucciones más
  antiguas se usan snapshots desde la base de datos.

### 3.2 Streams de telemetría

| Stream | Subjects | Retención | `max_age` | `max_bytes` | Dedupe | `max_msg_size` | Réplicas futuras |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `TLM_SNMP` | `horus.telemetry.snmp.>` | limits | 24 h | 10 GiB | 2 min | 1 MiB | 1 (o 3 si sobra disco) |
| `TLM_WIREGUARD` | `horus.telemetry.wireguard.>` | limits | 24 h | 1 GiB | 2 min | 256 KiB | 1 |
| `TLM_FLOWS_RAW` | `horus.telemetry.flows.raw.>` | **interest** | 6 h | 100 GiB (dimensionar, C-12) | 2 min | 1 MiB | 1 |
| `TLM_FLOWS_ENRICHED` | `horus.telemetry.flows.enriched.>` | **interest** | 6 h | 100 GiB (dimensionar, C-12) | 2 min | 1 MiB | 1 |
| `DLQ` | `horus.dlq.>` | limits | 30 d | 5 GiB | 2 min | 1 MiB | 3 |

- **SNMP con `limits` 24 h**: volumen modesto (1.000 routers × 1 sondeo/min × ~2 mensajes ≈ 33 msg/s), permite que el
  escritor de series y `alerts` se recuperen de caídas de horas y que el gateway tenga contexto reciente.
- **Flujos con `interest` + tope de bytes**: el mensaje se borra en cuanto **todos** los consumidores durables lo
  confirman, así el disco sólo se usa como buffer cuando un consumidor (o ClickHouse detrás) está caído — exactamente el
  requisito del Sprint 14 ("al volver, Collector → NATS → procesamiento continúa"). `max_age`/`max_bytes` ponen un
  techo: superado, se descartan los más antiguos (`discard: old`) y se emite alerta de capacidad.
  - Advertencia operativa: con `interest`, un mensaje publicado cuando **no existe ningún consumidor** se descarta. Los
    consumidores durables de estos streams se aprovisionan junto con el stream, no al arrancar el servicio.
  - Estimación orientativa: 20.000 flujos/s × ~90 B Protobuf ≈ 1,8 MB/s ≈ 6,5 GB/h → 6 h ≈ 39 GB. Ver C-12.
- **Réplica 1 para flujos incluso en clúster**: la telemetría masiva tolera pérdida ante fallo de disco; replicarla
  triplica I/O. Se revisa en Sprint 14/15.
- **No se usa core NATS sin persistencia para telemetría productiva**: el único uso de core NATS es la lectura en vivo
  del gateway (§api.md 4.8), que lee los mismos subjects sin crear consumidores.

### 3.3 Ajustes del servidor relevantes

- `jetstream.sync_interval`: por defecto NATS hace `fsync` cada 2 min; con un solo nodo, un corte eléctrico podría perder
  mensajes ya confirmados (PubAck). Recomendación: `sync_interval: always` mientras sea un solo nodo **si** el benchmark
  de flujos lo tolera (Sprint 15); si no, aceptar la ventana para telemetría y apoyarse en el outbox para dominio (§9.2).
  Ver C-11.
- `max_payload` del servidor: 1 MiB (por defecto) — los lotes de telemetría se cortan antes (§5.5).
- Límites de cuenta JetStream (`max_file_store`) acordes al disco reservado en [`storage.md`](./storage.md).

---

## 4. Consumers

### 4.1 Reglas generales

- **Pull consumers durables** (recomendado sobre push): control explícito de flujo (`Fetch`/`Consume` con tamaño de
  lote), escalado horizontal natural (N réplicas del servicio comparten el mismo durable → consumidores competidores),
  sin necesidad de *deliver subjects* ni grupos de cola, y backpressure implícito.
- Nombre: `<servicio>-<propósito>` en kebab-case (`alerts-snmp-reachability`). Cada durable usa
  `filter_subjects` (múltiples filtros, NATS ≥ 2.10) para recibir sólo lo que maneja.
- **Los consumidores los crea/actualiza el servicio dueño al arrancar** (`CreateOrUpdateConsumer`, idempotente) con su
  configuración en código, salvo los de streams `interest` (TLM_FLOWS_*), que se aprovisionan junto al stream (§3.2).
- `ack_policy: explicit` siempre. El handler confirma **después** de completar el efecto (commit en BD, insert en
  ClickHouse). Tareas largas envían `InProgress()` para extender `ack_wait`.
- Semántica resultante: **at-least-once** + idempotencia en el consumidor (§7) = efecto *exactly-once*.

### 4.2 Parámetros por familia

| Parámetro | Dominio | Telemetría |
| --- | --- | --- |
| `deliver_policy` | `all` en el primer arranque si el consumidor construye una proyección; `new` si sólo reacciona a hechos actuales (p. ej. notificaciones) | `all` (el stream sólo guarda lo pendiente) |
| `ack_wait` | 30 s | 60 s |
| `max_deliver` | 8 | 5 |
| `backoff` | `1s, 5s, 15s, 30s, 1m, 5m, 15m` | `5s, 30s, 2m, 5m` |
| `max_ack_pending` | 1 (orden estricto) o 64 (tolerante a orden, por defecto) | 2.000–10.000 (lotes en paralelo) |
| Tamaño de `Fetch` | 1–32 | 50–500 lotes |
| `max_waiting` | 512 | 512 |

### 4.3 Orden y paralelismo

JetStream entrega en orden de stream, pero con paralelismo (`max_ack_pending > 1`, varias réplicas) y reintentos el
orden de **procesamiento** se rompe. Estrategia por defecto:

1. **Consumidores tolerantes a desorden** (por defecto): cada evento de agregado lleva `aggregate_version`
   (monotónica, la misma `version` del recurso en PostgreSQL). El consumidor aplica *last-writer-wins por versión*: si
   ya tiene una versión ≥, confirma y descarta.
2. **Orden estricto** (pocos casos: p. ej. una máquina de estados derivada de una secuencia de eventos): `max_ack_pending = 1`.
3. **Orden estricto + paralelismo** (sólo si el volumen lo exige; no en v1): particionado determinista por la clave del
   5º token con *subject mapping* `partition(N, 5)` y un durable por partición.

Telemetría: sin garantías de orden; cada registro trae su `time` y las agregaciones son conmutativas.

### 4.4 Dead-letter (DLQ)

JetStream no tiene DLQ nativa: cuando un mensaje agota `max_deliver` el servidor deja de entregarlo y emite el
advisory `$JS.EVENT.ADVISORY.CONSUMER.MAX_DELIVERIES.<STREAM>.<CONSUMER>`; el mensaje sigue en el stream (hasta su
`max_age`). Implementación en dos capas:

1. **En el consumidor (camino principal)**, dentro de la librería común `packages/events/go`:
   - Error **permanente** (no se puede decodificar, versión de esquema no soportada, violación de invariante) → publica
     una copia en `horus.dlq.<servicio>.<consumer>` y hace `Term()` inmediatamente.
   - Error **transitorio** → `Nak()` (el `backoff` del consumidor decide el retraso).
   - En el último intento (`metadata.NumDelivered == max_deliver`) y aún falla → publica en DLQ y `Term()`.
   - Headers añadidos al mensaje DLQ: `Horus-Dlq-Original-Subject`, `Horus-Dlq-Stream`, `Horus-Dlq-Stream-Seq`,
     `Horus-Dlq-Consumer`, `Horus-Dlq-Attempts`, `Horus-Dlq-Error` (truncado a 1 KiB), `Horus-Dlq-Failed-At`; se
     conservan los headers originales y el cuerpo intacto.
2. **Vigilante (red de seguridad)**: un componente pequeño (en el servicio `alerts` o en un worker de plataforma, a
   decidir con Agente 1) escucha los advisories `MAX_DELIVERIES` y `MSG_TERMINATED`; si el mensaje no llegó ya a la DLQ
   (dedupe por `Nats-Msg-Id` = `dlq:<stream>:<seq>`), lo recupera con *direct get* por secuencia y lo copia.

Operación: métrica y alerta `horus_dlq_messages_total{consumer}` > 0; CLI de plataforma en `scripts/` para inspeccionar
y **re-publicar** un mensaje DLQ a su subject original (los consumidores son idempotentes, así que reprocesar es seguro).

### 4.5 Consumidores iniciales

| Durable | Stream | Filtros | Servicio | Propósito |
| --- | --- | --- | --- | --- |
| *(efímero ordenado)* | `AUTH_`, `DEVICES_`, `WIREGUARD_`, `SNMP_`, `ALERTS_`, `REPORTING_EVENTS` | todos | `api-gateway` | Fan-out WebSocket, cierre de sesiones, invalidación de permisos ([`api.md`](./api.md) §4.8) |
| `snmp-inventory` | `DEVICES_EVENTS` | `horus.devices.router.>`, `horus.devices.interface.>` | `snmp` | Mantener la lista de objetivos de sondeo y refrescar credenciales |
| `devices-snmp-observations` | `SNMP_EVENTS` | `horus.snmp.router.>`, `horus.snmp.interface.>` | `devices` | Actualizar alcanzabilidad/estado operativo y emitir `router.status_changed` |
| `devices-alert-severity` | `ALERTS_EVENTS` | `horus.alerts.alert.>` | `devices` | Derivar `warning`/`critical` del router a partir de alertas abiertas |
| `wireguard-devices` | `DEVICES_EVENTS` | `horus.devices.router.deleted.*` | `wireguard` | Desvincular peers de routers eliminados |
| `snmp-metrics-writer` | `TLM_SNMP` | `horus.telemetry.snmp.>` | `snmp` | Persistir series SNMP (almacén según [`database.md`](./database.md)) |
| `alerts-snmp-thresholds` | `TLM_SNMP` | `horus.telemetry.snmp.>` | `alerts` | Evaluar reglas de umbral (CPU, errores de interfaz) |
| `alerts-domain` | `SNMP_`, `WIREGUARD_`, `DETECTION_`, `DEVICES_EVENTS` | eventos relevantes para reglas | `alerts` | `Alert Rule → Detection → Event → Alert` (un durable por stream) |
| `alerts-reporting` | `REPORTING_EVENTS` | `horus.reporting.report.>` | `alerts` | Notificar al solicitante |
| `traffic-dimensions` | `DEVICES_EVENTS` | `router.>`, `interface.>`, `site.>` | `traffic-intelligence` | Cache exportador → router → interfaz → cliente |
| `traffic-enricher` | `TLM_FLOWS_RAW` | `horus.telemetry.flows.raw.>` | `traffic-intelligence` | Enriquecer y publicar a `TLM_FLOWS_ENRICHED` |
| `flows-raw-writer` | `TLM_FLOWS_RAW` | `horus.telemetry.flows.raw.>` | `flows` | Escribir flujos crudos en ClickHouse (si se conservan crudos, ver [`storage.md`](./storage.md)) |
| `analytics-flows-writer` | `TLM_FLOWS_ENRICHED` | `horus.telemetry.flows.enriched.>` | `analytics` | Insertar flujos enriquecidos en ClickHouse (lotes) |
| `detection-flows` | `TLM_FLOWS_ENRICHED` | `horus.telemetry.flows.enriched.>` | `detection` | Scoring/detección en ventana |
| `detection-reputation` | `REPUTATION_EVENTS` | `horus.reputation.>` | `detection` | Recargar indicadores |
| `analytics-dimensions` | `DEVICES_EVENTS`, `TRAFFIC_EVENTS` | entidades y rulesets | `analytics` | Diccionarios/dimensiones en ClickHouse |

La asignación exacta de quién escribe en ClickHouse (`flows`, `traffic-intelligence` o `analytics`) es de
[`services.md`](./services.md); el contrato de eventos no cambia con esa decisión.

---

## 5. Sobre del evento (envelope) y serialización

### 5.1 Campos

Inspirado en CloudEvents 1.0, adaptado a la convención `snake_case` del proyecto (no pretende ser compatible 1:1 con
el binding NATS de CloudEvents; el mapeo es trivial si se necesitara exportar).

| Campo | Tipo | Obligatorio | Descripción |
| --- | --- | --- | --- |
| `id` | UUIDv7 | sí | Identidad única del evento; = `Nats-Msg-Id`; clave de dedupe. Generado al insertar en el outbox. |
| `type` | string | sí | `horus.<dominio>.<entidad>.<evento>`. |
| `source` | string | sí | `horus/<servicio>` (p. ej. `horus/devices`). La instancia va en `trace_parent`/logs, no aquí. |
| `subject` | string | sí (dominio) | ID del agregado (= 5º token del subject NATS). |
| `time` | RFC 3339 UTC, ms | sí | Cuándo ocurrió el hecho (commit de la transacción), no cuándo se publicó. |
| `schema_version` | entero | sí | Versión **mayor** del esquema del payload (= `vN` del paquete Protobuf). |
| `tenant_id` | UUID | sí | Organización. Fija en v1 (§2.4). |
| `aggregate_type` | string | sí (dominio) | `router`, `peer`… (redundante con `type`, útil para outbox/consultas). |
| `aggregate_version` | entero | sí (dominio) | Versión del agregado tras el cambio (orden e idempotencia, §4.3). |
| `actor` | objeto | sí | `{ "type": "user"\|"service"\|"system", "id": "...", "name": "..." }` — quién causó el hecho. |
| `trace_parent` | string | no | W3C `traceparent` del contexto que originó el evento. |
| `correlation_id` | string | no | `x-request-id` de la petición original. |
| `causation_id` | UUIDv7 | no | `id` del evento que provocó éste (cadenas evento→evento, p. ej. `finding.raised → alert.opened`). |
| `data` | objeto | sí | Payload del tipo, según su esquema. |

Ejemplo (evento de dominio, cuerpo JSON):

```jsonc
{
  "id": "0192f0d1-1b2c-7e44-8a10-6b9c2d1e0f33",
  "type": "horus.devices.router.status_changed",
  "source": "horus/devices",
  "subject": "0192f0c4-7a1e-7c3a-9b1d-2f6e8a4c1d55",
  "time": "2026-10-07T14:03:11.123Z",
  "schema_version": 1,
  "tenant_id": "01920000-0000-7000-8000-000000000001",
  "aggregate_type": "router",
  "aggregate_version": 42,
  "actor": { "type": "service", "id": "devices", "name": "devices" },
  "trace_parent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
  "correlation_id": null,
  "causation_id": "0192f0d0-ff10-7a2b-9c3d-1e2f3a4b5c6d",
  "data": {
    "router_id": "0192f0c4-7a1e-7c3a-9b1d-2f6e8a4c1d55",
    "site_id": "0192e111-2222-7333-8444-555566667777",
    "previous_status": "online",
    "status": "offline",
    "reason": "snmp_unreachable",
    "changed_at": "2026-10-07T14:03:11.000Z"
  }
}
```

### 5.2 Headers NATS (en todos los mensajes)

| Header | Valor |
| --- | --- |
| `Nats-Msg-Id` | `id` del evento (dominio) o `batch_id` (telemetría) — deduplicación JetStream |
| `Content-Type` | `application/json` (dominio) · `application/x-protobuf; proto=horus.events.flows.v1.FlowBatch` (telemetría) |
| `Horus-Type` | `type` |
| `Horus-Schema-Version` | `schema_version` |
| `Horus-Tenant` | `tenant_id` |
| `Horus-Source` | `source` |
| `Horus-Time` | `time` |
| `traceparent` | W3C (en telemetría sólo si la traza está muestreada) |

En telemetría el sobre va **sólo en headers** (modo "binario"); el cuerpo es el lote Protobuf. Así se filtra y enruta
sin deserializar y no se repite el sobre por cada registro.

### 5.3 Serialización — recomendación

**Protobuf como único lenguaje de definición de esquemas (IDL) para todos los payloads; dos codificaciones:**

- **Dominio → JSON (protojson con nombres proto en `snake_case`)**. Motivos: volumen bajo (el coste de JSON es
  irrelevante), depurable con `nats sub`/`nats stream view`, almacenable como `jsonb` en el outbox, y el gateway lo
  reenvía al WebSocket casi sin transformar. El esquema sigue siendo Protobuf, con `buf breaking` en categoría
  `WIRE_JSON`.
- **Telemetría → Protobuf binario**. Motivos: 3–10× menos bytes y CPU en flujos (que dominan el disco de JetStream y el
  ancho de banda), lotes compactos (IPs como `bytes`, contadores `uint64` varint).
- Se descarta JSON Schema/Avro: un segundo lenguaje de esquemas duplicaría tooling; Protobuf ya es obligatorio para gRPC.

Reglas de mapeo protojson: `int64/uint64` se serializan como string en JSON (comportamiento estándar de protojson; el
frontend los trata como string o `bigint`); timestamps como RFC 3339; enums como nombre en minúsculas mediante un
valor `string` en el payload (los payloads de dominio usan `string` para estados, no enums Protobuf, para que añadir un
valor no rompa consumidores JSON).

### 5.4 Versionado y compatibilidad de esquemas

- Cambios **compatibles** (no cambian `schema_version`): añadir campos opcionales, añadir tipos de evento, añadir
  valores a campos `string` de estado documentados como abiertos. Consumidores **deben** ignorar campos desconocidos y
  tratar valores desconocidos como "otro".
- Cambios **incompatibles** (eliminar/renombrar campo, cambiar tipo o semántica): nuevo paquete
  `horus.events.<dominio>.v2`, `schema_version: 2`, **mismo subject**. El productor publica **ambas versiones** (dos
  mensajes, `id` distintos, mismo `causation_id`) durante una ventana ≥ max(30 días, 1 release). La librería de consumo
  declara las versiones soportadas y confirma-y-omite las demás (métrica `horus_events_skipped_version_total`).
- Prohibido reutilizar un `type` con otra semántica. Un tipo obsoleto se marca `deprecated` en el catálogo y se retira
  tras la ventana.
- CI: `buf breaking` (WIRE_JSON para `horus/events/**`) + validación del catálogo (§5.6).

### 5.5 Lotes de telemetría

- Un mensaje = un lote: hasta **1.000 registros o 1 s** (lo que ocurra primero) y siempre < **512 KiB** codificado.
- Cada lote lleva `batch_id` (UUIDv7) usado como `Nats-Msg-Id`: los reintentos del colector desde su buffer local no
  duplican mientras estén dentro de la ventana de dedupe (2 min); fuera de ella, la deduplicación final la hace el
  escritor de ClickHouse con `insert_deduplication_token = batch_id` (ver [`storage.md`](./storage.md)).

### 5.6 Registro de esquemas (`packages/events`)

```
packages/events/
├── catalog.yaml        # fuente de verdad del catálogo (ver ejemplo)
├── streams/            # definiciones de streams/consumers aprovisionados (JSON de la API JetStream)
├── go/                 # librería común: Envelope, Outbox (insert + relay), Publisher de telemetría con buffer,
│                       # Consumer runner (ack/nak/term, backoff, DLQ, inbox, métricas, trazas)
└── docs/               # catálogo navegable generado (no se edita)
```

Los mensajes Protobuf de los payloads viven en `packages/protobuf/horus/events/<dominio>/v<N>/` (un solo árbol buf).
Entrada de catálogo ilustrativa:

```yaml
- type: horus.devices.router.status_changed
  subject: horus.devices.router.status_changed.{router_id}
  stream: DEVICES_EVENTS
  family: domain            # domain | telemetry
  encoding: json
  schema: horus.events.devices.v1.RouterStatusChanged
  schema_version: 1
  producer: devices
  consumers: [api-gateway, alerts, analytics]
  websocket_channels: [routers, "router:{router_id}"]
  pii: false
  status: stable            # draft | stable | deprecated
  since: sprint-3
```

CI valida: cada `type` del catálogo tiene su mensaje Protobuf; cada mensaje en `horus/events` está catalogado; el
subject cumple la convención; el stream cubre el subject; los productores sólo publican en su dominio.
Del catálogo se generan: constantes Go de `type`/subject, tipos TS de los payloads expuestos por WebSocket, y la
documentación. (AsyncAPI 3.0 como formato de salida es opcional; ver C-05.)

### 5.7 Qué **no** va en un evento

- Secretos: contraseñas, hashes, claves privadas WireGuard, comunidades/credenciales SNMP, tokens. Nunca.
- Datos que el consumidor puede pedir por gRPC si los necesita completos (payloads > 64 KiB). Los eventos llevan el
  estado relevante del cambio ("estado transportado" moderado: lo suficiente para la mayoría de consumidores sin
  llamada de vuelta), no el agregado entero con relaciones.
- PII innecesaria: el email del usuario sólo aparece en `user.created/updated`; el resto referencia `user_id`.

---

## 6. Transactional outbox (productores con PostgreSQL)

Problema: escribir en PostgreSQL y publicar en NATS no es atómico; si el servicio cae entre ambas operaciones se pierde
el evento o se publica un hecho que no ocurrió.

### 6.1 Diseño

1. En la **misma transacción** que el cambio de negocio, el servicio inserta el evento en su tabla `outbox`
   (definición física en [`database.md`](./database.md); columnas mínimas propuestas):

```sql
-- ilustrativo
CREATE TABLE outbox (
  seq            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,  -- orden de publicación
  id             uuid        NOT NULL UNIQUE,                       -- id del evento (UUIDv7)
  type           text        NOT NULL,
  subject        text        NOT NULL,                              -- subject NATS completo
  aggregate_id   uuid        NOT NULL,
  headers        jsonb       NOT NULL,
  payload        jsonb       NOT NULL,                              -- sobre completo
  created_at     timestamptz NOT NULL DEFAULT now(),
  published_at   timestamptz,
  attempts       int         NOT NULL DEFAULT 0,
  last_error     text
);
CREATE INDEX outbox_unpublished ON outbox (seq) WHERE published_at IS NULL;
```

2. Un **relay** (goroutine dentro del mismo servicio, código común en `packages/events/go`):
   - Un solo relay activo por servicio mediante `pg_try_advisory_lock` (el resto de réplicas en espera). Así se preserva
     el orden de `seq` sin coordinación adicional; el volumen de dominio no necesita paralelismo.
   - Se despierta con `LISTEN outbox` (el insert hace `NOTIFY`) y por sondeo cada 1 s como respaldo.
   - Lee `WHERE published_at IS NULL ORDER BY seq LIMIT 100`, publica cada uno con `Nats-Msg-Id = id` y **espera el
     PubAck** de JetStream, luego marca `published_at`. Publicación asíncrona con ventana de 100 PubAck pendientes.
   - Fallo de NATS → reintenta con backoff (máx. 30 s), incrementa `attempts`, el negocio sigue funcionando: los eventos
     se acumulan en PostgreSQL (métrica `horus_outbox_pending` y alerta si > 5 min de antigüedad).
   - Si el relay cae tras publicar y antes de marcar, al reanudar republica con el mismo `Nats-Msg-Id` → JetStream lo
     descarta dentro de la ventana de 20 min; fuera de ella, lo resuelve la idempotencia del consumidor.
3. **Limpieza**: filas publicadas se conservan **7 días** (permite re-publicar tras una pérdida de datos en NATS, §9.2) y
   luego se borran por lote (o particionado por día, a criterio de [`database.md`](./database.md)).

Alternativas descartadas: CDC con Debezium (infra adicional: Kafka Connect / Debezium Server) y *listen-to-yourself*
(publicar primero y consumir para escribir) — demasiado complejo para el MVP.

### 6.2 Telemetría

No usa outbox (no hay transacción de negocio). Usa el **publicador con buffer local** de §9.3.

---

## 7. Idempotencia en consumidores (inbox / dedupe)

Cada consumidor elige una de tres estrategias, documentada en su handler:

| Estrategia | Cuándo | Cómo |
| --- | --- | --- |
| **Inbox** | Efectos en PostgreSQL no naturalmente idempotentes (crear una alerta, enviar notificación) | Tabla `inbox(consumer text, event_id uuid, processed_at timestamptz, PRIMARY KEY (consumer, event_id))`. En la **misma transacción** que el efecto: `INSERT ... ON CONFLICT DO NOTHING`; si no insertó, ya se procesó → `Ack()` sin efecto. Limpieza a los 30 días (= retención del stream). |
| **Upsert por versión** | Proyecciones de estado (cache de routers en `snmp`, estado en `devices`) | `UPDATE ... WHERE id = $1 AND version < $2` / `INSERT ... ON CONFLICT DO UPDATE ... WHERE excluded.version > t.version`. |
| **Dedupe en destino** | Inserción masiva en ClickHouse | `insert_deduplication_token = batch_id`, o motores `ReplacingMergeTree` con clave que incluya `batch_id`. |

Efectos externos (email, Telegram): inbox **antes** de llamar al proveedor con estado `sending`, y la clave de
idempotencia del proveedor cuando exista (= `event_id`). Se acepta duplicado excepcional ante caída exacta entre el envío
y el commit; se documenta.

---

## 8. Catálogo inicial de eventos

Convenciones: subject = `type.<entity_id>`; payloads mostrados en JSON (dominio en JSON real; telemetría se muestra en
su representación JSON equivalente aunque viaja en Protobuf). Sólo se muestra `data`. "Sprint" indica cuándo se
necesita.

### 8.1 `auth` (stream `AUTH_EVENTS`, productor `auth`)

| Tipo | Entidad (5º token) | Consumidores | Sprint |
| --- | --- | --- | --- |
| `horus.auth.user.created` | user | api-gateway (no), alerts (destinatarios), analytics (dims) | 2 |
| `horus.auth.user.updated` | user | alerts, analytics | 2 |
| `horus.auth.user.disabled` / `.enabled` / `.deleted` | user | **api-gateway** (cierra WS), alerts | 2 |
| `horus.auth.user.roles_changed` | user | **api-gateway** (recalcula permisos de WS) | 2 |
| `horus.auth.role.created` / `.updated` / `.deleted` | role | **api-gateway** | 2 |
| `horus.auth.session.created` | session | detection (futuro: accesos anómalos) | 2 |
| `horus.auth.session.revoked` | session | **api-gateway** (cierra WS `4409`) | 1–2 |
| `horus.auth.login.failed` | user (o `unknown`) | detection/alerts (fuerza bruta) | 2 |
| `horus.auth.mfa.enabled` / `.disabled` | user | alerts (aviso de seguridad al usuario) | 2 |

```jsonc
// horus.auth.user.created
{ "user_id": "0192...", "email": "noc@isp.example", "display_name": "NOC Turno A",
  "status": "active", "role_ids": ["0192..."], "has_totp": false }

// horus.auth.session.revoked
{ "session_id": "0192...", "user_id": "0192...", "reason": "admin_revoked",
  "revoked_by": "0192..." }
// reason: logout | admin_revoked | password_changed | refresh_token_reuse | user_disabled | expired

// horus.auth.role.updated
{ "role_id": "0192...", "name": "noc-operator",
  "permissions": ["devices.read", "devices.update", "wireguard.read"],
  "added_permissions": ["devices.update"], "removed_permissions": [] }

// horus.auth.login.failed  (sin contraseña ni hash, nunca)
{ "user_id": null, "username": "admin", "source_ip": "203.0.113.7",
  "user_agent": "Mozilla/5.0 ...", "reason": "invalid_credentials", "consecutive_failures": 4 }
```

### 8.2 `devices` (stream `DEVICES_EVENTS`, productor `devices`)

| Tipo | Entidad | Consumidores | Sprint |
| --- | --- | --- | --- |
| `horus.devices.site.created` / `.updated` / `.deleted` | site | api-gateway, traffic-intelligence, analytics | 3 |
| `horus.devices.router.created` / `.updated` / `.deleted` | router | api-gateway, **snmp**, wireguard (`deleted`), traffic-intelligence, analytics | 3 |
| `horus.devices.router.credentials_changed` | router | **snmp** (recarga por gRPC; sin secretos en el evento) | 3/5 |
| `horus.devices.router.status_changed` | router | api-gateway, alerts, analytics | 3/5 |
| `horus.devices.router.maintenance_started` / `_ended` | router | alerts (supresión), api-gateway | 3 |
| `horus.devices.interface.created` / `.updated` / `.deleted` | interface | api-gateway, traffic-intelligence (mapa interfaz→cliente), analytics | 5 |

```jsonc
// horus.devices.router.created
{ "router_id": "0192...", "site_id": "0192...", "name": "rt-core-01",
  "management_ip": "10.0.0.1", "vendor": "mikrotik", "model": "CCR2004-1G-12S+2XS",
  "tags": ["core"], "snmp_enabled": true, "poll_interval_seconds": 60,
  "flow_exporter_ips": ["10.0.0.1"] }

// horus.devices.router.updated  (cambios: lista de campos + valores nuevos de esos campos)
{ "router_id": "0192...", "changed_fields": ["name", "tags"],
  "after": { "name": "rt-core-01a", "tags": ["core", "edge"] } }

// horus.devices.router.status_changed
{ "router_id": "0192...", "site_id": "0192...", "previous_status": "online",
  "status": "offline", "reason": "snmp_unreachable", "changed_at": "2026-10-07T14:03:11.000Z" }
// status: online | offline | warning | critical | unknown | maintenance

// horus.devices.interface.created  (descubierta por SNMP y registrada por devices vía gRPC)
{ "interface_id": "0192...", "router_id": "0192...", "if_index": 7, "name": "sfp-sfpplus1",
  "alias": "UPLINK-TRANSIT-A", "speed_bps": 10000000000, "type": "ethernet", "is_monitored": true }
```

Nota de diseño: el **estado** del router lo posee `devices` (fuente de verdad única), derivado de observaciones de
`snmp` (alcanzabilidad) y de `alerts` (severidad de alertas abiertas). `snmp` no publica `status_changed`; publica
observaciones (§8.4). Confirmar con [`services.md`](./services.md) (C-09).

### 8.3 `wireguard` (stream `WIREGUARD_EVENTS`, productor `wireguard`)

| Tipo | Entidad | Consumidores | Sprint |
| --- | --- | --- | --- |
| `horus.wireguard.server.created` / `.updated` / `.deleted` | server | api-gateway | 4 |
| `horus.wireguard.peer.created` / `.updated` / `.deleted` | peer | api-gateway, devices (vínculo router↔peer) | 4 |
| `horus.wireguard.peer.revoked` | peer | api-gateway, alerts (auditoría/aviso) | 4 |
| `horus.wireguard.peer.key_rotated` | peer | api-gateway, alerts | 4 |
| `horus.wireguard.peer.connection_changed` | peer | api-gateway, alerts, devices | 4 |
| `horus.wireguard.handshake.observed` (telemetría, `horus.telemetry.wireguard.handshake.<server_id>`) | server | api-gateway (canal estado), wireguard (detecta `connection_changed`) | 4 |

```jsonc
// horus.wireguard.peer.created  (sólo clave pública)
{ "peer_id": "0192...", "server_id": "0192...", "router_id": "0192...",
  "public_key": "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=",
  "address": "10.200.0.15/32", "allowed_ips": ["10.200.0.15/32", "192.168.88.0/24"],
  "persistent_keepalive_seconds": 25 }

// horus.wireguard.peer.key_rotated
{ "peer_id": "0192...", "server_id": "0192...",
  "previous_public_key": "xTIB...8Dg=", "public_key": "q2Lk...7Ws=",
  "grace_period_seconds": 0 }

// horus.wireguard.peer.revoked
{ "peer_id": "0192...", "server_id": "0192...", "reason": "router_decommissioned" }

// horus.wireguard.peer.connection_changed
{ "peer_id": "0192...", "previous_state": "connected", "state": "stale",
  "last_handshake_at": "2026-10-07T13:58:02Z" }
// state: never_connected | connected | stale (sin handshake > 3 min)

// horus.wireguard.handshake.observed (telemetría; lote por lectura de `wg show` / netlink)
{ "batch_id": "0192...", "server_id": "0192...", "observed_at": "2026-10-07T14:03:00Z",
  "peers": [ { "peer_id": "0192...", "public_key": "q2Lk...7Ws=",
               "endpoint": "198.51.100.20:51820", "last_handshake_at": "2026-10-07T14:02:41Z",
               "rx_bytes": 1048576, "tx_bytes": 524288 } ] }
```

Se modela `key.rotated` como `peer.key_rotated` (la clave no es una entidad con identidad propia) y
`handshake.observed` como **telemetría** (frecuencia ≈ 1 cada 2 min por peer); el hecho de negocio derivado es
`peer.connection_changed`.

### 8.4 `snmp` (productor `snmp`)

Dominio (stream `SNMP_EVENTS`):

| Tipo | Entidad | Consumidores | Sprint |
| --- | --- | --- | --- |
| `horus.snmp.router.unreachable` | router | **devices**, alerts | 5 |
| `horus.snmp.router.recovered` | router | **devices**, alerts | 5 |
| `horus.snmp.router.auth_failed` | router | devices, alerts | 5 |
| `horus.snmp.router.discovery_completed` | router | devices (sincroniza interfaces por gRPC), api-gateway | 5 |
| `horus.snmp.interface.oper_status_changed` | interface | devices, alerts, api-gateway | 5 |

Telemetría (stream `TLM_SNMP`):

| Tipo | Subject | Consumidores |
| --- | --- | --- |
| `horus.snmp.device.polled` | `horus.telemetry.snmp.device.<router_id>` | snmp-metrics-writer, alerts-snmp-thresholds, api-gateway, analytics |
| `horus.snmp.interfaces.polled` | `horus.telemetry.snmp.interfaces.<router_id>` | ídem |

```jsonc
// horus.snmp.router.unreachable
{ "router_id": "0192...", "management_ip": "10.0.0.1", "reason": "timeout",
  "consecutive_failures": 3, "last_success_at": "2026-10-07T14:00:05Z",
  "detected_at": "2026-10-07T14:03:11Z" }
// reason: timeout | auth_failure | icmp_unreachable | collector_error

// horus.snmp.interface.oper_status_changed
{ "interface_id": "0192...", "router_id": "0192...", "if_index": 7,
  "previous": "up", "current": "down", "observed_at": "2026-10-07T14:03:00Z" }

// horus.snmp.device.polled (telemetría, Protobuf; representación JSON)
{ "batch_id": "0192...", "router_id": "0192...", "polled_at": "2026-10-07T14:03:00Z",
  "poll_duration_ms": 412, "uptime_seconds": 1209600, "cpu_percent": 13.2,
  "memory_used_bytes": 1288490188, "memory_total_bytes": 2147483648,
  "temperature_celsius": 47, "firmware": "7.16.1", "errors": [] }

// horus.snmp.interfaces.polled (telemetría, contadores crudos; el consumidor calcula tasas)
{ "batch_id": "0192...", "router_id": "0192...", "polled_at": "2026-10-07T14:03:00Z",
  "interfaces": [ { "interface_id": "0192...", "if_index": 7, "oper_status": "up", "speed_bps": 10000000000,
                    "in_octets": "918273645512", "out_octets": "123456789012",
                    "in_errors": 0, "out_errors": 0, "in_discards": 12, "out_discards": 0,
                    "counter_bits": 64 } ] }
```

Los contadores se publican **crudos** (no tasas) con `counter_bits` para que el consumidor maneje *wraps* y reinicios
(`uptime` decreciente); calcular tasas en el colector perdería información si se pierde un sondeo.

### 8.5 `flows` (productor `flows`)

Telemetría (stream `TLM_FLOWS_RAW`): `horus.flows.batch.received` en `horus.telemetry.flows.raw.<router_id>`.

```jsonc
// FlowBatch (Protobuf horus.events.flows.v1.FlowBatch; representación JSON)
{ "batch_id": "0192...", "collector_id": "flows-1", "router_id": "0192...",
  "exporter_ip": "10.0.0.1", "protocol": "ipfix", "sampling_rate": 1000,
  "received_from": "2026-10-07T14:03:00.000Z", "received_to": "2026-10-07T14:03:00.998Z",
  "records": [ {
    "flow_start": "2026-10-07T14:02:31.120Z", "flow_end": "2026-10-07T14:02:59.870Z",
    "ip_version": 4, "src_addr": "100.64.12.34", "dst_addr": "157.240.25.35",
    "src_port": 51544, "dst_port": 443, "ip_protocol": 6, "tcp_flags": 27,
    "bytes": 1840221, "packets": 1312,
    "input_if_index": 12, "output_if_index": 7, "direction": "egress",
    "src_as": 0, "dst_as": 32934, "next_hop": "10.0.0.254", "vlan_id": 0 } ] }
```

Supuestos de compatibilidad con el **registro de flujo normalizado** de [`traffic-model.md`](./traffic-model.md)
(Agente 2) — a validar (C-13):

- Un registro por flujo con tiempos de inicio/fin en UTC con ms; `bytes`/`packets` **ya multiplicados** por
  `sampling_rate`? → **Supuesto: no**; se publican tal como llegan y `sampling_rate` va en el lote; la normalización
  (multiplicar) la hace `traffic-intelligence` y queda explícita en el registro enriquecido (`bytes_estimated`).
- IPs: en Protobuf `bytes` de 4 o 16 octetos; en ClickHouse `IPv6` (IPv4 mapeada) según Agente 2.
- Interfaces por `if_index`; la resolución a `interface_id` la hace `traffic-intelligence` con el inventario.
- sFlow (muestras de paquete) se convierte a registros de flujo equivalentes en el colector; no se publican cabeceras
  de paquete.

Dominio (stream `FLOWS_EVENTS`):

| Tipo | Entidad | Consumidores |
| --- | --- | --- |
| `horus.flows.exporter.unregistered_seen` | exportador (UUIDv5 de la IP) | alerts, api-gateway (sugerir alta del router) |
| `horus.flows.exporter.silent` | router | alerts, devices |
| `horus.flows.collector.data_gap` | collector | alerts, analytics (marcar huecos) |

```jsonc
// horus.flows.collector.data_gap
{ "collector_id": "flows-1", "from": "2026-10-07T14:10:00Z", "to": "2026-10-07T14:22:13Z",
  "dropped_batches": 731, "dropped_records_estimated": 702114, "reason": "local_buffer_full" }
```

### 8.6 `traffic` — traffic-intelligence

Telemetría (stream `TLM_FLOWS_ENRICHED`): `horus.traffic.flow.enriched` en `horus.telemetry.flows.enriched.<router_id>`.
Mismo lote que §8.5 más, por registro: `bytes_estimated`, `packets_estimated`, `client_id`, `site_id`,
`interface_id`, `src_asn`, `dst_asn`, `remote_asn`, `remote_org`, `service`, `category`, `remote_country`,
`classification_ruleset_version`, `direction` resuelta (`download`/`upload` desde el punto de vista del cliente).
Pipeline `IP → Prefix → ASN → Organization → Service → Category` según [`traffic-model.md`](./traffic-model.md).

```jsonc
{ "batch_id": "0192...", "source_batch_id": "0192...", "router_id": "0192...",
  "ruleset_version": 17,
  "records": [ { "flow_start": "...", "flow_end": "...", "client_id": "0192...", "site_id": "0192...",
                 "client_addr": "100.64.12.34", "remote_addr": "157.240.25.35", "remote_port": 443,
                 "ip_protocol": 6, "direction": "upload", "bytes_estimated": 1840221000,
                 "packets_estimated": 1312000, "remote_asn": 32934, "remote_org": "Meta Platforms",
                 "service": "instagram", "category": "social", "remote_country": "US" } ] }
```

Dominio (stream `TRAFFIC_EVENTS`):

| Tipo | Entidad | Consumidores |
| --- | --- | --- |
| `horus.traffic.ruleset.published` | ruleset | analytics, detection, reporting (versión de clasificación vigente) |
| `horus.traffic.asn_db.updated` | dataset | analytics |

```jsonc
// horus.traffic.ruleset.published
{ "ruleset_id": "0192...", "version": 17, "services": 214, "categories": 18,
  "prefixes": 98213, "published_by": "0192..." }
```

### 8.7 `reputation` y `detection`

| Tipo | Stream | Productor | Consumidores |
| --- | --- | --- | --- |
| `horus.reputation.feed.refreshed` | `REPUTATION_EVENTS` | reputation | detection, analytics |
| `horus.reputation.indicator.changed` | `REPUTATION_EVENTS` | reputation | detection (sólo cambios relevantes, no la lista entera) |
| `horus.detection.finding.raised` | `DETECTION_EVENTS` | detection | **alerts**, api-gateway, reporting |
| `horus.detection.finding.updated` / `.cleared` | `DETECTION_EVENTS` | detection | alerts, reporting |
| `horus.detection.client.classification_changed` | `DETECTION_EVENTS` | detection | alerts, reporting, analytics (Sprint 10) |

`detection.raised` se nombra `finding.raised` para respetar `<dominio>.<entidad>.<evento>` sin repetir
`detection.detection`.

```jsonc
// horus.reputation.feed.refreshed
{ "feed_id": "0192...", "name": "abuse-ipdb-blocklist", "entries": 182733,
  "added": 1203, "removed": 988, "refreshed_at": "2026-10-07T06:00:00Z" }

// horus.detection.finding.raised  (correlación explicable, no "IP en lista = malware")
{ "finding_id": "0192...", "kind": "suspected_botnet_c2", "severity": "high", "confidence": 0.82,
  "subject_type": "client", "subject_id": "0192...", "router_id": "0192...",
  "window_from": "2026-10-07T13:00:00Z", "window_to": "2026-10-07T14:00:00Z",
  "reasons": [
    { "code": "reputation_hit", "detail": "3 destinos en feed C2", "weight": 0.4 },
    { "code": "periodic_beaconing", "detail": "intervalo 60s ±2s durante 50 min", "weight": 0.3 },
    { "code": "unusual_port", "detail": "TCP/8443 a ASN sin historial", "weight": 0.12 } ],
  "evidence_ref": "clickhouse://findings/0192..." }

// horus.detection.client.classification_changed
{ "client_id": "0192...", "previous": "residential", "current": "commercial", "confidence": 0.87,
  "scores": { "residential": 0.13, "commercial": 0.87, "security": 0.05, "anomaly": 0.21 },
  "reasons": ["31 dispositivos detrás del CPE", "uso sostenido 08:00–19:00 L–S", "upload/download 0.62"] }
```

### 8.8 `alerts` (stream `ALERTS_EVENTS`, productor `alerts`)

| Tipo | Entidad | Consumidores | Sprint |
| --- | --- | --- | --- |
| `horus.alerts.alert.opened` | alert | api-gateway, **devices** (severidad → estado), reporting | 11 (umbral SNMP antes si se adelanta) |
| `horus.alerts.alert.acknowledged` | alert | api-gateway | 11 |
| `horus.alerts.alert.resolved` | alert | api-gateway, devices, reporting | 11 |
| `horus.alerts.alert.escalated` | alert | api-gateway | 11 |
| `horus.alerts.notification.sent` | notification | api-gateway (canal `me`) | 11 |
| `horus.alerts.notification.failed` | notification | api-gateway, (operación) | 11 |

```jsonc
// horus.alerts.alert.opened
{ "alert_id": "0192...", "rule_id": "0192...", "severity": "critical",
  "title": "Router rt-core-01 sin respuesta SNMP",
  "resource": { "type": "router", "id": "0192..." }, "site_id": "0192...",
  "source_event_type": "horus.snmp.router.unreachable", "source_event_id": "0192...",
  "opened_at": "2026-10-07T14:03:12Z", "dedupe_key": "router-unreachable:0192..." }

// horus.alerts.alert.acknowledged
{ "alert_id": "0192...", "acknowledged_by": "0192...", "comment": "Corte de fibra reportado" }

// horus.alerts.alert.resolved
{ "alert_id": "0192...", "resolution": "auto", "resolved_by": null,
  "source_event_type": "horus.snmp.router.recovered", "duration_seconds": 1440 }

// horus.alerts.notification.sent
{ "notification_id": "0192...", "alert_id": "0192...", "channel": "telegram",
  "recipient_user_id": "0192...", "provider_message_id": "8812" }
```

### 8.9 `reporting` (stream `REPORTING_EVENTS`, productor `reporting`)

| Tipo | Entidad | Consumidores |
| --- | --- | --- |
| `horus.reporting.report.generated` | report | alerts (notificar), api-gateway (canal `me`) |
| `horus.reporting.report.failed` | report | alerts, api-gateway |

```jsonc
// horus.reporting.report.generated
{ "report_id": "0192...", "kind": "consumption_by_client", "format": "pdf",
  "period_from": "2026-09-01T00:00:00Z", "period_to": "2026-10-01T00:00:00Z",
  "object_key": "reports/2026/10/0192....pdf", "size_bytes": 482113,
  "sha256": "9f86d0...", "requested_by": "0192..." }
```

El archivo vive en MinIO; el evento lleva la clave, nunca una URL prefirmada (caduca y es un secreto). El frontend la
pide por REST.

### 8.10 `analytics`

Sin eventos en v1 (consumidor puro). Reservado: `horus.analytics.rollup.completed` cuando existan agregaciones
programadas que otros servicios (reporting) deban esperar.

---

## 9. Fallos: ¿qué pasa si NATS se reinicia?

### 9.1 Reinicio ordenado o caída breve (segundos–minutos)

| Componente | Comportamiento |
| --- | --- |
| Streams (todos `file`) | Persisten en disco; al arrancar NATS recupera mensajes, estado de consumidores (ack floor, pendientes) y ventanas de dedupe. |
| Productores de dominio | El relay del outbox no recibe PubAck → reintenta con backoff; los eventos se acumulan en PostgreSQL. **Pérdida: cero.** Las APIs siguen respondiendo (escribir en BD no depende de NATS). |
| Colectores (`snmp`, `flows`, `wireguard`) | Publican al buffer local (§9.3); al volver NATS se drena con los mismos `batch_id`. |
| Consumidores durables | El cliente `nats.go` reconecta automáticamente; el durable continúa desde su ack floor; mensajes no confirmados se reentregan tras `ack_wait`. Duplicados absorbidos por idempotencia (§7). |
| Gateway / WebSocket | Los consumidores ordenados efímeros se recrean desde la última secuencia vista; los clientes WS pueden recibir `resync_required` si hubo hueco. La telemetría en vivo (core NATS) en vuelo durante el corte **se pierde** — aceptable, es estado que se reemplaza en el siguiente sondeo. |
| Readiness | Los servicios no se caen si NATS no está: reportan `degraded` en `/readyz` para NATS pero siguen sirviendo lo que no depende de él (requisito Sprint 14). |

### 9.2 Pérdida de datos de NATS (disco dañado, `sync_interval` > 0 y corte eléctrico)

- **Dominio**: el outbox conserva 7 días de eventos ya publicados. Procedimiento de recuperación
  ([`disaster-recovery.md`](./disaster-recovery.md)): re-publicar desde el outbox los eventos con
  `published_at > <última secuencia confirmada sana>`; los consumidores deduplican por `event_id`/versión. Las
  proyecciones pueden reconstruirse también por snapshot desde gRPC del servicio dueño.
- **Telemetría**: lo que no llegó a escribirse en ClickHouse/almacén de series se pierde; se registra como
  `data_gap`. Es la degradación aceptada para datos de alto volumen.
- Recreación de streams/consumidores: el job de aprovisionamiento es idempotente y se ejecuta en cada arranque del
  stack.

### 9.3 Buffer local en colectores cuando NATS no está disponible

Librería común de publicación de telemetría (`packages/events/go`):

```
lectura (SNMP/UDP flows) ──► cola en memoria acotada ──► publicador async JetStream (ventana de PubAck)
                                     │ si NATS no responde / cola > umbral
                                     ▼
                              spool en disco (segmentos append-only, por tiempo)
                                     │ al reconectar: drena FIFO con los mismos batch_id
```

| Colector | Memoria | Disco (spool) | Política al llenarse |
| --- | --- | --- | --- |
| `snmp` | 10.000 lotes (~10 min a 1.000 routers) | 1 GiB / máx. 24 h | Descartar los más antiguos; contar huecos. Los eventos de dominio de `snmp` (`router.unreachable`) **no** usan este buffer: van por outbox en PostgreSQL. |
| `flows` | 256 MiB | 20 GiB (dimensionar, C-12) / máx. 2 h | Descartar los más antiguos, métrica `horus_flows_dropped_records_total`, emitir `horus.flows.collector.data_gap` al recuperar. |
| `wireguard` (handshakes) | 1.000 lotes | sin spool | Descartar: el siguiente lectura reemplaza el estado. |

Reglas:

- El bucle de lectura UDP de flujos **nunca** se bloquea por NATS (si lo hiciera, el kernel descartaría datagramas sin
  métrica); la presión se absorbe en la cola y el spool.
- El spool vive en un volumen Docker local del colector (no en el NAS), con `fsync` por segmento.
- Al drenar se respeta un límite de tasa (p. ej. 2× la tasa nominal) para no saturar a los consumidores que también
  se están recuperando.
- Lotes reenviados fuera de la ventana de dedupe de JetStream (2 min) se deduplican en destino por `batch_id`.

### 9.4 Otros escenarios relacionados (respuesta corta)

- **ClickHouse caído**: `analytics-flows-writer` y `flows-raw-writer` no confirman → los mensajes se quedan en los
  streams `interest` hasta 6 h / tope de bytes; el resto de consumidores (detection) siguen. Al volver, drenado.
- **SNMP deja de funcionar**: no llegan `device.polled`; `devices` marca `unknown` tras 3 intervalos sin observación
  (temporizador propio, no evento) y `alerts` abre una alerta de colector (`horus_snmp_last_poll_age_seconds`).
- **Un router desaparece**: `snmp` emite `router.unreachable` → `devices` publica `router.status_changed` (`offline`)
  → `alerts` abre alerta; `flows` emite `exporter.silent` si dejaba de exportar.
- **Consumidor caído mucho tiempo**: dominio retiene 30 d; si se excede, el consumidor arranca con `resync` desde
  snapshot gRPC del dueño.

---

## 10. Observabilidad de eventos (contrato mínimo)

Detalle en [`observability.md`](./observability.md). La librería común expone:

- `horus_events_published_total{type}`, `horus_events_publish_errors_total`, `horus_outbox_pending`,
  `horus_outbox_oldest_age_seconds`.
- `horus_events_consumed_total{consumer,type,result=ack|nak|term|dlq|skipped}`,
  `horus_events_processing_seconds{consumer}`, `horus_consumer_lag{stream,consumer}` (num_pending del consumidor).
- `horus_telemetry_buffer_bytes{collector}`, `horus_telemetry_dropped_total{collector}`.
- Trazas: span `publish <type>` y `process <type>` enlazados por `trace_parent` del sobre (link, no hijo, en
  telemetría por lotes).
