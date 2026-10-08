# Contrato de eventos NATS JetStream — Horus Flow

> Estado: **propuesta ronda 2 — documento crítico** (aplica [`po-decisions.md`](./po-decisions.md) D1–D10, que prevalecen
> sobre cualquier texto anterior) · Dueño: Agente C (contratos, seguridad y operaciones). **Normativo**: si un nombre de
> evento difiere en otro documento, manda éste ([`services.md`](./services.md) lo establece así).
>
> "Sprint N" en este documento = el incremento que entrega esa capacidad (D9, [`roadmap.md`](./roadmap.md)). Si el
> Agente A agrupa servicios en un binario modular, los **dominios lógicos, subjects y streams no cambian**: sólo quién
> publica (E3). "api-gateway" en las tablas de consumidores = el rol `gateway` del binario `horus`.
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
| E2 | Subject de dominio `horus.<dominio>.<entidad>.<evento>.<entity_id>`: los 4 primeros tokens son el **tipo**; el 5º, la clave del agregado. **Sin token de tenant** ([ADR-0017](./adr/0017-multi-tenant-desde-v1.md) §6). Telemetría `horus.telemetry.<fuente>.<tipo>.<clave>`. Órdenes de trabajo `horus.work.<dominio>.<trabajo>` (antes `horus.jobs.*`, que ahora es el dominio del módulo `jobs`). |
| E3 | `<dominio>` es el **dominio lógico** (carpeta en `services/`), no el proceso: `reputation` y `reporting` conservan su dominio aunque en v1 vivan dentro de `detection` y `analytics`. Para traffic-intelligence el token es **`traffic`** (único y definitivo). |
| E4 | Un stream por dominio (`<DOMINIO>_EVENTS`, `limits`, 30 d) **compartido por todos los tenants**, streams de telemetría `TLM_*` (`limits` + `max_bytes`, `discard: old`), `WORK` (`workqueue`), `DLQ`. Todo `file`. |
| E5 | Consumidores **pull durables** por servicio y propósito, `AckExplicit`, `MaxDeliver` + `BackOff`, DLQ en la librería de consumo + vigilante de advisories. El `api-gateway` usa **NATS core** (sin consumidores). |
| E6 | Sobre estilo CloudEvents en `snake_case`: `id` UUIDv7, `type`, `source`, `subject`, `time`, `schema_version`, `tenant_id`, `actor`, `trace_parent`, `aggregate_version`… |
| E7 | **Protobuf es el único lenguaje de esquema**. Codificación JSON (protojson) para dominio y trabajos; Protobuf binario para telemetría. |
| E8 | Dominio desde servicios con PostgreSQL ⇒ **transactional outbox** (`Nats-Msg-Id` = `event_id`). Sin PostgreSQL (`snmp`, `flows`) ⇒ publicación confirmada + spool local sin descarte + reconciliación por estado. |
| E9 | Auditoría: cada servicio publica `horus.<dominio>.audit.recorded` vía outbox en su propio stream; sólo `auth` la persiste. |
| E10 | Multi-tenant desde v1 (D6): **`tenant_id` obligatorio en el sobre y cabecera `Horus-Tenant` obligatoria** en todo mensaje (dominio y telemetría). La librería rechaza publicar sin tenant (salvo tipos de plataforma) y el consumidor rechaza (DLQ + alerta) un mensaje sin cabecera o con cabecera ≠ sobre. Una sola cuenta NATS (§2.4). |
| E11 | Registro en `packages/events/catalog/<dominio>.yaml` (un archivo por dominio, para que varios agentes no choquen); payloads en `packages/protobuf/horus/events/<dominio>/v1/`. |
| E12 | Cliente = IP (D1, [ADR-0018](./adr/0018-la-ip-es-el-cliente.md)): el ingester publica `flows.client.first_seen` y `flows.client.activity_summary`; `devices` crea el cliente y publica `customer.discovered`, `updated`, `kind_changed`, `inactivated`, `reactivated`, `reset`, `purged`. Se eliminan `customer.created/deleted/assigned/unassigned` (§8.2, §8.5). |
| E13 | Seguridad de red como propósito (D5, [ADR-0024](./adr/0024-deteccion-de-botnets-como-objetivo-principal.md)): `detection` publica `finding.opened/updated/resolved`, `customer.kind_suggested`, `customer.security_state_changed` y `reputation.snapshot_published`; la mitigación activa queda fuera de v1. |
| E14 | MikroTik primero (D10, [ADR-0022](./adr/0022-mikrotik-routeros-v7-primer-fabricante.md)): métricas por SNMP (sin stream propio de RouterOS); la API de RouterOS alimenta hechos del alta (`devices.router.capabilities_detected`); `wireguard.peer.enrolled/activated` y `wireguard.hub.status_changed`. |
| E15 | Binario modular ([ADR-0025](./adr/0025-binario-modular-con-roles.md)): los eventos entre módulos del mismo proceso **también** pasan por NATS con outbox; dominio = módulo. |

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
        │          │         │          └── UUIDv7 del agregado (o la clave indicada en el catálogo, p. ej. realm_id)
        │          │         └──────────── verbo en pasado, snake_case (created, state_changed, key_rotated)
        │          └────────────────────── entidad en singular, snake_case (router, peer, customer, audit)
        └───────────────────────────────── dominio lógico = módulo (§2.3)
```

- `type` = 4 primeros tokens (`horus.devices.router.created`). El subject se construye siempre con la librería
  (`natsx.Subject(type, entity_id)`); nunca a mano.
- **5º token**: permite filtrar por entidad sin deserializar (`horus.*.*.*.<router_id>`, usado por el gateway para el
  topic `router.<id>`), habilita en el futuro particionado determinista (`partition(N, 5)`) y `max_msgs_per_subject`.
- **El tenant no va en el subject** (§2.4): va en el sobre (`tenant_id`) y en la cabecera `Horus-Tenant`, ambos
  obligatorios.
- Tokens `[a-z0-9_-]` (el guion es necesario para los UUID); sin mayúsculas; subject ≤ 128 bytes.
- Filtros típicos: `horus.devices.router.>`, `horus.devices.*.created.*`, `horus.*.audit.recorded.*`.

### 2.2 Telemetría y órdenes de trabajo

| Subject | Clave | Tipo | Stream |
| --- | --- | --- | --- |
| `horus.telemetry.snmp.device.<router_id>` | router | `horus.snmp.device.sampled` (el `snmp.metrics.batch` de [`services.md`](./services.md)) | `TLM_SNMP` |
| `horus.telemetry.snmp.interface.<router_id>` | router | `horus.snmp.interface.sampled` (lote de las interfaces de un sondeo) | `TLM_SNMP` |
| `horus.telemetry.wireguard.peer_status.<server_id>` | hub WG | `horus.wireguard.peer_status.observed` (un lote **por tenant** y hub: el hub es de plataforma, los peers de cada tenant) | `TLM_WIREGUARD` |
| `horus.telemetry.flows.batch.<router_id>` | router exportador | `horus.flows.batch.received` (un lote = un router = un tenant) | `TLM_FLOWS` |
| `horus.telemetry.flows.client_activity.<realm_id>` | realm | `horus.flows.client.activity_summary` (resumen horario de IPs con tráfico; §8.5) | `TLM_FLOWS` |
| `horus.work.reporting.generate_report` | — | orden de generar un reporte | `WORK` |

- Toda la telemetría bajo `horus.telemetry.>`: una sola regla de permisos/monitoreo separa lo masivo de lo de dominio,
  y no solapa con los streams de dominio (`horus.snmp.>` ≠ `horus.telemetry.snmp.>`).
- El `type` de telemetría conserva la forma `horus.<dominio>.<entidad>.<evento>` (va en el header `Horus-Type`).
- `horus.flows.client.first_seen.<realm_id>` (IPs nuevas, lotes cada 10 s) tiene forma de evento de dominio y vive en
  `FLOWS_EVENTS` (volumen bajo, se publica sin spool: perder uno es inocuo, el siguiente flujo lo repite; §8.5).
- Exportadores **no registrados**: el collector descarta sus flujos (nunca publica flujos sin tenant) y emite
  `horus.flows.exporter.unregistered` (evento de plataforma).
- **Órdenes de trabajo** (`workqueue`) pasan de `horus.jobs.>` a **`horus.work.>`** (stream `WORK`): con el binario
  modular `jobs` es un **dominio** que publica eventos (`horus.jobs.coverage.low`, `horus.jobs.remote_sync.lagging`…),
  y un stream `workqueue` sobre `horus.jobs.>` se los tragaría.

### 2.3 Dominios y productores

Con [ADR-0025](./adr/0025-binario-modular-con-roles.md) "servicio" = **módulo** del binario `horus`; el proceso que
publica es el que ejecute ese rol.

| Token | Módulo dueño | Stream |
| --- | --- | --- |
| `auth` | auth (incluye tenants, membresías y kioscos) | `AUTH_EVENTS` |
| `devices` | devices (incluye clientes y prefijos de clientes) | `DEVICES_EVENTS` |
| `wireguard` | wireguard (control; `wg-agent` no publica) | `WIREGUARD_EVENTS` |
| `snmp` | snmp | `SNMP_EVENTS` |
| `flows` | collector y ingester | `FLOWS_EVENTS` |
| `traffic` | traffic | `TRAFFIC_EVENTS` |
| `detection` | detection (incluye reputación y scoring) | `DETECTION_EVENTS` |
| `alerts` | alerts | `ALERTS_EVENTS` |
| `analytics` | analytics (incluye dashboards, widgets y kioscos-vista) | `ANALYTICS_EVENTS` |
| `reporting` | reporting | `REPORTING_EVENTS` |
| `jobs` | jobs (archivado, copia remota, cobertura) | `JOBS_EVENTS` |

- El token `reputation` desaparece como dominio: los snapshots de reputación los publica `detection`
  (`horus.detection.reputation.snapshot_published`), alineado con [`services.md`](./services.md).
- **Permisos NATS**: una sola cuenta ([ADR-0017](./adr/0017-multi-tenant-desde-v1.md) §6); como todos los módulos
  del perfil mínimo corren en `horus-app`, la separación de permisos por subject se aplica por **proceso**:
  `horus-collector` sólo publica `horus.telemetry.flows.>`, `horus.flows.exporter.>` y `horus.dlq.collector.>`;
  `horus-wg-agent` no publica; `horus-app` publica en sus dominios. Al separar roles en más contenedores, cada uno
  recibe un usuario NATS con sólo sus dominios.

### 2.4 Multi-tenancy (D6)

Decisión del Agente A en [ADR-0017](./adr/0017-multi-tenant-desde-v1.md) §6; aquí el contrato:

1. **Todo mensaje lleva `tenant_id` en el sobre y la cabecera `Horus-Tenant`** (dominio, telemetría y órdenes de
   trabajo). En telemetría, cuyo sobre va sólo en cabeceras, `Horus-Tenant` es la única fuente y es obligatoria.
2. **Tipos de plataforma** (catálogo publicado, snapshots de reputación, heartbeat del poller, exportador no
   registrado, hub WireGuard, copias): `tenant_id: null` y `Horus-Tenant: platform`. El catálogo declara por tipo
   `tenant_scope: tenant | platform`; CI rechaza un tipo de negocio declarado `platform`.
3. **Validación en la librería** (`packages/go/natsx`):
   - Al publicar: el tenant sale del contexto (`tid` de la petición o el tenant de la entidad); sin tenant en un tipo
     `tenant` → error de programación (no se publica).
   - Al consumir: falta `Horus-Tenant`, o no coincide con el `tenant_id` del sobre, o un tipo `tenant` trae `null` ⇒
     DLQ con `reason=tenant_invalid`, métrica `horus_events_tenant_invalid_total{consumer}` y alerta de seguridad.
   - El handler recibe el tenant ya validado en el contexto; los repositorios hacen `SET LOCAL horus.tenant_id`
     antes de escribir (RLS).
4. **Una sola cuenta NATS**; sin token de tenant en subjects. Los consumidores son multi-tenant.
5. **Equidad**: el collector limita flujos/s por exportador y por tenant antes de publicar (exceso descartado y
   contado por tenant; evento `horus.flows.exporter.rate_limited`).
6. **Fan-out WebSocket**: el gateway enruta por `Horus-Tenant` sin deserializar ([`api.md`](./api.md) §4.3).
7. **Baja de un tenant**: como no hay token de tenant en el subject, no se puede purgar un stream por tenant; los
   eventos de dominio caducan a los 30 días (`max_age`) y la telemetría en ≤ 72 h. El certificado de baja lo
   documenta ([`security.md`](./security.md) §13.5). Si algún día hace falta purga inmediata o permisos por tenant,
   se añade el token con *subject mapping* sin tocar productores (lo prevé el ADR).

> Esta sección sustituye a la propuesta intermedia de esta ronda (tenant como 5º token del subject). Se acepta el ADR:
> los beneficios (purga por subject, filtros de replay por tenant) no compensan multiplicar subjects con consumidores
> multi-tenant y una sola cuenta.

### 2.5 Subjects reservados y NATS KV

- `horus.dlq.>` (dead-letter), `horus.work.>` (órdenes de trabajo), `horus.telemetry.>`.
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
| `DETECTION_EVENTS` | `horus.detection.>` | limits | 30 d | 2 GiB | 20 min | 64 KiB |
| `ALERTS_EVENTS` | `horus.alerts.>` | limits | 30 d | 2 GiB | 20 min | 64 KiB |
| `ANALYTICS_EVENTS` | `horus.analytics.>` | limits | 30 d | 512 MiB | 20 min | 64 KiB |
| `REPORTING_EVENTS` | `horus.reporting.>` | limits | 30 d | 512 MiB | 20 min | 64 KiB |
| `JOBS_EVENTS` | `horus.jobs.>` | limits | 30 d | 256 MiB | 20 min | 64 KiB |

- **`limits` y no `interest`/`workqueue`**: varios servicios consumen el mismo evento y un consumidor nuevo debe poder
  hacer *backfill* o reconstruir una proyección leyendo hasta 30 días. `workqueue` prohíbe consumidores solapados;
  `interest` borra lo ya consumido.
- **30 d** ([ADR-0006](./adr/0006-nats-jetstream-bus-de-eventos.md) sugería "p. ej. 7 días"): el volumen de dominio es mínimo
  (MB/día) y 30 d cubre caídas largas de un consumidor (incluido `auth` como escritor de auditoría, cuya propuesta mínima
  era 7 d en [`security.md`](./security.md) §7.3) y re-proyecciones sin tocar PostgreSQL. No es sistema de registro.
- **Un stream por dominio**: retención/tamaño/permisos independientes, aislamiento ante un productor desbocado.
- **Auditoría dentro del stream de su dominio** (`horus.<dominio>.audit.recorded.>`), no en un stream aparte: no puede
  haber dos streams con subjects solapados, y la convención acordada pone la auditoría bajo el dominio.
- **Dedupe 20 min**: cubre reinicios del relay del outbox y reintentos desde spool.
- **Streams compartidos por tenant** (E4): un stream por tenant multiplicaría streams × ISP sin beneficio a este
  volumen; el tenant viaja en sobre y cabecera (§2.4).
- `FLOWS_EVENTS` recibe además `horus.flows.client.first_seen.*` (lotes de IPs nuevas): 1 GiB cubre de sobra 30 días
  salvo avalancha, que el ingester ya limita por realm ([ADR-0018](./adr/0018-la-ip-es-el-cliente.md) §2).

### 3.2 Telemetría, trabajos y DLQ

| Stream | Subjects | Retención | `max_age` | `max_bytes` (v1) | Dedupe | `max_msg_size` | Réplicas futuras |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `TLM_SNMP` | `horus.telemetry.snmp.>` | limits | 72 h | 5 GiB | 2 min | 1 MiB | 3 |
| `TLM_WIREGUARD` | `horus.telemetry.wireguard.>` | limits | 24 h | 1 GiB | 2 min | 256 KiB | 1 |
| `TLM_FLOWS` | `horus.telemetry.flows.>` | limits | 24 h | **50 GB** por defecto (dimensionar, C-12) | 2 min | 1 MiB | 1 |
| `WORK` | `horus.work.>` | **workqueue** | 7 d | 256 MiB | 20 min | 64 KiB | 3 |
| `DLQ` | `horus.dlq.>` | limits | 30 d | 5 GiB | 2 min | 1 MiB | 3 |

- **Telemetría con `limits` + `max_bytes` + `discard: old`** ([ADR-0006](./adr/0006-nats-jetstream-bus-de-eventos.md)): el
  stream es el buffer ante caídas de ClickHouse; al llenarse se descartan los lotes más antiguos y se registra el hueco.
- **TLM_FLOWS — objetivo ≥ 6 h a tasa pico** (requisito del Agente 2, [`storage.md`](./storage.md)). Con varios ISP
  la tasa pico es la **suma** de los tenants; la cuota `max_flows_per_second` de cada tenant ([`api.md`](./api.md) §2.8)
  impide que uno solo agote la autonomía de todos (el collector descarta lo que exceda la cuota del tenant y lo cuenta). Con los 50 GB de
  [`architecture.md`](./architecture.md) §9.4 eso sólo se cumple hasta ~10 routers (1,8 MB/s ⇒ 6 h ≈ 39 GB); a 100 routers
  (18 MB/s) 6 h exigen ~390 GB de SSD. Por eso `max_bytes` es **parámetro de despliegue** (`HORUS_TLM_FLOWS_MAX_BYTES`)
  calculado como `tasa_pico × 6 h × 1,2`; si el disco no alcanza, el runbook documenta la autonomía real. Decisión de
  producto: C-12.
- `max_age` 24 h en TLM_FLOWS: más allá, re-ingerir flujos no aporta (agregados cerrados) y libera disco.
- `TLM_SNMP` 72 h: ~30 MB/h a 1.000 routers ⇒ cubre un fin de semana de caída del writer.
- `WORK` `workqueue`: cada mensaje lo procesa un solo worker y se borra al confirmarse. La verdad del trabajo sigue en la
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
3. **Orden + paralelismo** (no en v1): particionado por el 6º token (agregado) con *subject mapping* y un durable por partición.

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
| `auth-audit-<dominio>` (uno por stream) | cada `<DOMINIO>_EVENTS` | `horus.<dominio>.audit.recorded.>` | auth | Único escritor de `audit_log` (cadena de hashes, `max_ack_pending = 1`) | 1–2 |
| `snmp-inventory` | `DEVICES_EVENTS` | `horus.devices.router.>`, `horus.devices.interface.>` | snmp | Objetivos de sondeo; ante `credentials_rotated` refresca vía gRPC `GetPollingTarget` | 5 |
| `snmp-wireguard` | `WIREGUARD_EVENTS` | `horus.wireguard.peer.handshake_stale.>`, `…handshake_recovered.>` | snmp | Tercera señal del estado observado | 5 |
| `snmp-metrics-writer` | `TLM_SNMP` | `horus.telemetry.snmp.>` | snmp (rol metrics-writer) | Persistir en ClickHouse | 5 |
| `devices-snmp-state` | `SNMP_EVENTS` | `horus.snmp.router.>` | devices | Proyección `router_status_cache`; interfaces sugeridas | 5 |
| `wireguard-devices` | `DEVICES_EVENTS` | `horus.devices.router.created.>`, `…deleted.>` | wireguard | Sugerir/limpiar peer asociado (nunca borra solo) | 4 |
| `flows-ingester` | `TLM_FLOWS` | `horus.telemetry.flows.>` | flows (ingester) | Clasificar en ingesta con el snapshot y escribir `flows_raw` (escritor único) | 6 |
| `flows-inventory` | `DEVICES_EVENTS` | `router.>`, `interface.>` | flows (collector e ingester) | Mapas exportador→(tenant, router) e ifIndex→interfaz/`flow_role` (ClickHouse no guarda `customer_id`: [ADR-0018](./adr/0018-la-ip-es-el-cliente.md) §1) | 6 |
| `devices-client-first-seen` | `FLOWS_EVENTS` | `horus.flows.client.first_seen.>` | devices | Alta idempotente por `(tenant, realm, address)` → `customer.discovered` (queue group) | 6 |
| `devices-client-activity` | `TLM_FLOWS` | `horus.telemetry.flows.client_activity.>` | devices | `last_seen` (resolución 1 h) y `inactive → active` (`customer.reactivated`) | 6 |
| `devices-customer-detection` | `DETECTION_EVENTS` | `horus.detection.customer.kind_suggested.>`, `horus.detection.customer.security_state_changed.>` | devices | Aplica el tipo sugerido si `kind_locked = false` y la confianza ≥ umbral del tenant → `customer.kind_changed` (`source=scoring`); proyecta `security_state` | 10 |
| `ingester-known-clients` | `DEVICES_EVENTS` | `horus.devices.customer.>`, `horus.devices.client_prefix.>`, `horus.devices.realm.>` | flows (ingester) | Conjunto de IPs conocidas por realm y prefijos de clientes | 6 |
| `detection-customers` | `DEVICES_EVENTS` | `horus.devices.customer.>` | detection | Universo de clientes a puntuar; respeta `kind_locked` para no proponer cambios ya decididos a mano | 8–10 |
| `alerts-jobs` | `JOBS_EVENTS` | `horus.jobs.coverage.low.>`, `horus.jobs.remote_sync.lagging.>`, `horus.jobs.remote_sync.failed.>` | alerts | Alertas de cobertura de flujos y de copia remota | backups |
| `flows-snapshots` | `TRAFFIC_EVENTS`, `DETECTION_EVENTS` | `horus.traffic.catalog.published.>`, `horus.detection.reputation.snapshot_published.>` | flows (ingester) | Recargar snapshots desde NATS Object Store (doble buffer) | 7–8 |
| `detection-signals` | `SNMP_EVENTS`, `FLOWS_EVENTS` | `router.state_changed`, `exporter.*` | detection | Contexto de correlación | 8 |
| `alerts-<dominio>` (uno por stream) | `SNMP_`, `WIREGUARD_`, `FLOWS_`, `DETECTION_`, `REPORTING_EVENTS` | eventos que alimentan reglas, incl. `horus.snmp.poller.heartbeat.>` | alerts | `Alert Rule → Event → Alert → Notification` | 11 |
| `analytics-names` | `DEVICES_EVENTS` | `router.>`, `site.>`, `customer.>` | analytics | Caché de nombres y tipo de cliente por tenant | 9 |
| `auth-tenant-lifecycle` (y uno por servicio con datos) | `AUTH_EVENTS` | `horus.auth.tenant.>` | todos los dueños de datos | Suspender/pausar ingesta, ejecutar la purga de un tenant dado de baja | 1 |
| `reporting-worker` | `WORK` | `horus.work.reporting.>` | reporting | Generar reportes | 12 |

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
| `subject` | string | sí (dominio) | ID del agregado = 6º token. |
| `time` | RFC 3339 UTC, ms | sí | Cuándo ocurrió (commit / observación), no cuándo se publicó. |
| `schema_version` | entero | sí | Versión **mayor** del payload (= `vN` del paquete Protobuf). |
| `tenant_id` | UUID \| null | sí (presente siempre) | Tenant (ISP) dueño del hecho; copia obligatoria en la cabecera `Horus-Tenant`. `null` (cabecera `platform`) sólo en tipos declarados `tenant_scope: platform`. |
| `aggregate_type` | string | sí (dominio) | `router`, `peer`… |
| `aggregate_version` | entero | sí (dominio) | `version` de la entidad tras el cambio (orden + idempotencia). |
| `actor` | objeto | sí | Ver abajo. |
| `trace_parent` | string | no | W3C `traceparent` del contexto origen. |
| `correlation_id` | string | no | `X-Request-Id` de la petición original. |
| `causation_id` | UUIDv7 | no | `id` del evento que provocó éste (`finding.opened → alert.opened`). |
| `data` | objeto | sí | Payload según su esquema. |

**`actor`** (requisitos de [`security.md`](./security.md) §6.5):

| Campo | Descripción |
| --- | --- |
| `type` | `user` \| `service` \| `system` \| `kiosk` |
| `id` | UUIDv7 del usuario o del kiosco, o `svc:<nombre>` para servicios, o `system:<proceso>` |
| `via_platform` | `true` si un usuario de plataforma actuó dentro del tenant con acceso de soporte ([ADR-0017](./adr/0017-multi-tenant-desde-v1.md) §2, [`security.md`](./security.md) §6.6) |
| `platform_role` | Opcional: rol de plataforma con el que actuó (`platform_admin`…) |
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
  "tenant_id": "0192e000-0000-7000-8000-000000000001",
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
| `Horus-Type`, `Horus-Schema-Version`, `Horus-Source`, `Horus-Time` | copia de los campos del sobre |
| `Horus-Tenant` | **obligatoria**: `tenant_id` del sobre, o `platform` en tipos de plataforma (§2.4). En telemetría es la única fuente del tenant |
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
  tenant_scope: tenant        # tenant | platform
  stream: SNMP_EVENTS
  family: domain              # domain | telemetry | job
  encoding: json
  schema: horus.events.snmp.v1.RouterStateChanged
  schema_version: 1
  producer: snmp
  consumers: [devices, alerts, detection, api-gateway]
  websocket_topics: [routers.status, "router.{router_id}"]
  pii: false                  # true ⇒ campos personales listados en pii_fields (p. ej. [ip]); se ocultan a kioscos
  status: stable              # draft | stable | deprecated
  since: sprint-5
```

CI valida: cada `type` tiene mensaje Protobuf y golden file; cada mensaje de `horus/events` está catalogado; el subject
cumple §2; un stream cubre el subject; el productor publica sólo en sus dominios; los
golden files de tipos `tenant_scope: tenant` llevan `tenant_id` no nulo. El catálogo se divide en
`packages/events/catalog/<dominio>.yaml` para que dos agentes que añaden eventos de dominios distintos no editen el
mismo archivo ([`conventions.md`](./conventions.md) §6.3).
Del catálogo se generan constantes Go, tipos TS de los payloads expuestos por WebSocket y la documentación.

### 5.7 Qué no va en un evento

- **Secretos**: contraseñas/hashes, claves privadas WireGuard, credenciales SNMP/API RouterOS/SSH, credenciales de
  destinos remotos de almacenamiento, tokens, códigos de enrolamiento de kioscos, URLs prefirmadas. Nunca.
- Más de 64 KiB: los eventos de entidad llevan el **estado completo de la entidad** (sus columnas propias + `version`,
  sin relaciones anidadas ni secretos); lo que no quepa se pide por gRPC/REST.
- PII innecesaria: email sólo en `auth.user.*`; IP/User-Agent del actor sólo en `*.audit.recorded`. La **IP del
  cliente** (D1: es su identidad) sólo en `devices.customer.*`, `flows.client.first_seen`, `flows.client.activity_summary` y en el lote de flujos;
  los eventos de `detection` y `alerts` referencian `customer_id` y no repiten la IP.

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

Subject = `type.<entity_id>`; todo mensaje lleva `tenant_id` en el sobre y `Horus-Tenant` (`platform`/`null` en los tipos de plataforma). Se muestra sólo `data`. Telemetría en su representación JSON equivalente (viaja en
Protobuf). **Regla para entidades** (petición de [`database.md`](./database.md) §10): `*.created` y `*.updated` llevan el
**estado completo** de la entidad + `id` + `version`; `*.updated` añade `changed_fields`; `*.deleted` lleva el último
estado + `version` + `deleted_at`.

### 8.1 `auth` → `AUTH_EVENTS` (productor `auth`)

| Tipo | Ámbito | Consumidores | Sprint |
| --- | --- | --- | --- |
| `horus.auth.tenant.created` / `.updated` | platform¹ | todos los dueños de datos (crear particiones/KEK del tenant), api-gateway | 1 |
| `horus.auth.tenant.suspended` / `.resumed` | platform¹ | **api-gateway** (corta acceso y WS), flows (pausa de ingesta si `pause_ingest`), alerts | 1 |
| `horus.auth.tenant.offboarded` / `.purged` | platform¹ | todos los dueños de datos (purga, crypto-shredding), jobs | 1 |
| `horus.auth.user.created` / `.updated` / `.deleted` | platform | alerts (destinatarios), analytics (nombres) | 2 |
| `horus.auth.user.disabled` / `.enabled` | platform | **api-gateway** (cierra WS), alerts | 2 |
| `horus.auth.membership.granted` / `.updated` / `.revoked` (sustituye a `user.role_assignments_changed`) | tenant | **api-gateway** (permisos de WS y caché de membresías revocadas) | 2 |
| `horus.auth.role.created` / `.updated` / `.deleted` | tenant (roles propios) / platform (plantillas) | **api-gateway** | 2 |
| `horus.auth.support_access.granted` / `.ended` | tenant | api-gateway, alerts (aviso al `tenant_admin`) | 2 |
| `horus.auth.kiosk.enrolled` / `.revoked` | tenant | **api-gateway** (cierra WS `4409`), alerts | dashboards |
| `horus.auth.session.created` | platform | detection (futuro: accesos anómalos) | 2 |
| `horus.auth.session.revoked` | platform | **api-gateway** (cierra WS `4409`) | 1–2 |
| `horus.auth.login.failed` | platform | detection/alerts (fuerza bruta) | 2 |
| `horus.auth.mfa.enabled` / `.disabled` | platform | alerts (aviso al usuario) | 2 |
| `horus.auth.audit.recorded` | tenant o platform según la acción | auth (`auth-audit-auth`) | 1–2 |

¹ Los eventos del ciclo de vida de un tenant llevan `tenant_id` = ese tenant (y `Horus-Tenant` igual) y entity_id = el
tenant; se listan como "platform" porque sólo los emite un rol de plataforma.

```jsonc
// horus.auth.tenant.created   (subject horus.auth.tenant.created.<tenant_id>)
{ "id": "0192e000-...", "version": 1, "slug": "isp-norte", "name": "ISP Norte", "status": "active",
  "country": "MX", "quotas": { "max_routers": 50, "max_flows_per_second": 20000, "max_customers": 200000 },
  "created_at": "2026-10-07T12:00:00Z" }

// horus.auth.user.created   (subject …user.created.<user_id>; tenant_id null)
{ "id": "0192...", "version": 1, "email": "noc@isp.example", "display_name": "NOC Turno A", "status": "active",
  "has_totp": false, "created_at": "2026-10-07T12:00:00Z" }

// horus.auth.membership.updated   (subject …membership.updated.<membership_id>; tenant_id = el del ISP)
{ "user_id": "0192...", "version": 5,
  "assignments": [ { "role_id": "0192...", "scope": "site:0192..." } ],
  "previous_assignments": [ { "role_id": "0192...", "scope": "tenant" } ] }

// horus.auth.membership.revoked
{ "user_id": "0192...", "version": 6, "reason": "removed_by_tenant_admin" }

// horus.auth.role.updated
{ "id": "0192...", "version": 3, "name": "noc", "is_system": true,
  "permissions": ["devices.read", "snmp.read", "alerts.read", "alerts.ack"], "changed_fields": ["permissions"],
  "added_permissions": ["alerts.ack"], "removed_permissions": [] }

// horus.auth.kiosk.revoked   (sin credenciales)
{ "id": "0192...", "version": 4, "name": "Videowall NOC 1", "reason": "device_lost" }

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
| `horus.devices.router.capabilities_detected` | snmp, flows, api-gateway | 5 |
| `horus.devices.realm.updated` | **flows** (ingester: realms del router) | 6 |
| `horus.devices.client_prefix.created` / `.updated` / `.deleted` | **flows** (ingester: qué IP es cliente), detection | 6 |
| `horus.devices.customer.discovered` | **flows** (conjunto de IPs conocidas), detection, analytics, api-gateway | 6 |
| `horus.devices.customer.updated` (alias/notas, desbloqueo de tipo) | analytics, api-gateway | 6 |
| `horus.devices.customer.kind_changed` | detection, analytics, alerts, api-gateway | 6/10 |
| `horus.devices.customer.inactivated` / `.reactivated` | flows, detection, analytics, api-gateway | 6 |
| `horus.devices.customer.reset` | detection (reinicia líneas base), analytics, api-gateway | 6 |
| `horus.devices.customer.purged` | **flows**, detection, analytics (borrar lo derivado) | 6 |
| `horus.devices.audit.recorded` | auth | 3 |

> **Eliminados** (D1, cliente = IP): `horus.devices.customer.created`, `.deleted`, `.assigned` y `.unassigned`. Ya no
> hay altas manuales ni asignaciones IP↔cliente con vigencia; la identidad es `(tenant, realm, address)`. Coordinado con
> el Agente B ([`database.md`](./database.md) §2.3, [`traffic-model.md`](./traffic-model.md)). `customer.created` no se
> reutiliza con otra semántica (§5.4): el alta se llama `discovered` porque la origina la observación.
>
> **Ciclo de vida**: `discovered` (al procesar `flows.client.first_seen`) → `kind_changed` (0..n; scoring o manual) →
> `inactivated` (sin tráfico ≥ N días, **30** por defecto, o fuera de todo prefijo: `reason=prefix_removed`) →
> `reactivated` (vuelve a verse; mismo `id`) … `reset` (manual: la IP pasó a otra persona) … → `purged` (sin tráfico
> más allá de la mayor retención por cliente, 25 meses por defecto; o privacidad; o baja del tenant). Tras `purged`, si
> la IP reaparece es un cliente nuevo.
>
> **Nombres**: `inactivated` (no `inactive`, como proponía el borrador de [`database.md`](./database.md)) por la regla
> de verbo en pasado (§2.1); el cambio de tipo tiene evento propio `kind_changed` (no va dentro de `updated`) porque
> lo consumen alertas e historial con su origen y razones. Todos los eventos de cliente llevan estado completo +
> `version`.

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

// horus.devices.router.capabilities_detected   (D10; tras el primer sondeo y al cambiar firmware)
{ "id": "0192...", "version": 11, "vendor": "mikrotik", "os": "routeros", "os_version": "7.16.1",
  "board": "CCR2116-12G-4S+", "api": { "rest": true, "rest_tls": true, "ssh": true },
  "traffic_flow": { "enabled": true, "version": "ipfix", "targets_ok": true, "active_flow_timeout_seconds": 60,
                    "inactive_flow_timeout_seconds": 15, "packet_sampling": false },
  "snmp": { "v3": true }, "wireguard": true, "detected_at": "2026-10-07T14:05:00Z" }
// targets_ok=false si Traffic Flow no apunta al collector esperado (deriva de configuración): alerta, no corrección.

// horus.devices.client_prefix.created   (estado completo)
{ "id": "0192...", "version": 1, "realm_id": "0192...", "site_id": "0192...", "prefix": "100.64.0.0/16",
  "assignment_mode": "dynamic", "source": "suggested", "confirmed": true, "created_at": "2026-10-07T12:00:00Z" }

// horus.devices.customer.discovered   (entity_id = customer_id; pii: [address])
{ "id": "0192...", "version": 1, "address": "100.64.12.34", "realm_id": "0192...", "site_id": "0192...",
  "client_prefix_id": "0192...", "kind": "residential", "kind_source": "default", "kind_locked": false,
  "commercial_use_suspected": false, "status": "active", "alias": null,
  "first_seen": "2026-10-07T13:55:00Z", "last_seen": "2026-10-07T13:55:00Z" }

// horus.devices.customer.kind_changed   (estado completo del cliente + detalle del cambio)
{ "id": "0192...", "version": 4, "address": "100.64.12.34", "realm_id": "0192...", "status": "active",
  "kind": "commercial", "kind_source": "scoring", "kind_locked": false, "kind_confidence": 87,
  "change": { "from_kind": "residential", "to_kind": "commercial", "source": "scoring",
              "model_ref": "scoring-v3@2026-10-07",
              "reasons": [ { "code": "many_inbound_services", "detail": "servicios entrantes en 443 y 8080", "weight": 0.35 },
                           { "code": "business_hours_pattern", "detail": "uso sostenido 08:00–19:00 L–S", "weight": 0.30 } ],
              "manual_reason": null, "causation_event_id": "0192...", "changed_at": "2026-10-07T15:00:00Z" } }
// change.source: default | scoring | manual | reset. Con manual: model_ref/reasons = null, manual_reason obligatorio,
// actor = el usuario, kind_locked = true. Desbloquear emite customer.updated con changed_fields=["kind_locked"].

// horus.devices.customer.inactivated
{ "id": "0192...", "version": 7, "address": "100.64.12.34", "status": "inactive",
  "last_seen": "2026-09-01T10:00:00Z", "reason": "no_traffic" }
// reason: no_traffic | prefix_removed

// horus.devices.customer.reset
{ "id": "0192...", "version": 8, "address": "100.64.12.34", "kind": "residential", "kind_source": "default",
  "kind_locked": false, "alias": null, "reset_at": "2026-10-07T16:00:00Z" }

// horus.devices.customer.purged   (sin IP: el objetivo es olvidarla)
{ "id": "0192...", "version": 9, "reason": "retention" }
// reason: privacy_request | tenant_offboarding | retention
```

### 8.3 `wireguard` → `WIREGUARD_EVENTS` (productor `wireguard`, control)

| Tipo | Consumidores | Sprint |
| --- | --- | --- |
| `horus.wireguard.hub.status_changed` (plataforma) | alerts (página: todos los routers de todos los ISP dependen del hub), api-gateway (`platform.overview`, `system`) | 4 |
| `horus.wireguard.peer.created` / `.updated` / `.deleted` | api-gateway | 4 |
| `horus.wireguard.peer.enrolled` (clave pública recibida por `POST /enroll/wireguard`) | api-gateway, alerts (aviso al admin del tenant) | 4 |
| `horus.wireguard.peer.activated` (primer handshake) | api-gateway, devices, snmp | 4 |
| `horus.wireguard.peer.revoked` | api-gateway, alerts | 4 |
| `horus.wireguard.peer.key_rotated` | api-gateway, alerts | 4 |
| `horus.wireguard.peer.handshake_stale` | **snmp** (estado observado), alerts, api-gateway | 4 |
| `horus.wireguard.peer.handshake_recovered` | snmp, alerts, api-gateway | 4 |
| `horus.wireguard.audit.recorded` | auth | 4 |
| `horus.wireguard.peer_status.observed` (telemetría, `TLM_WIREGUARD`) | api-gateway (topic estado) | 4 |

> El hub y la IPAM de túneles son **de plataforma** ([ADR-0017](./adr/0017-multi-tenant-desde-v1.md) §8): los eventos
> del hub llevan `tenant_id: null`; cada peer pertenece a un tenant y sus eventos llevan ese tenant;
> `peer_status.observed` se publica **un lote por tenant** y hub, para que el gateway nunca reenvíe a un ISP el estado
> de peers de otro.

```jsonc
// horus.wireguard.peer.enrolled   (sin token ni secretos)
{ "id": "0192...", "version": 2, "router_id": "0192...", "status": "pending_handshake",
  "public_key": "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=", "address": "10.255.3.17/32",
  "enrolled_from_ip": "198.51.100.20", "enrolled_at": "2026-10-07T12:10:00Z" }

// horus.wireguard.hub.status_changed
{ "hub_id": "0192...", "previous": "up", "status": "down", "reason": "interface_missing",
  "peers_affected": 143, "tenants_affected": 4, "changed_at": "2026-10-07T14:00:00Z" }
```

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
| `horus.snmp.poller.heartbeat` (tenant `platform`; entity_id = UUIDv5 de la instancia) | alerts, api-gateway (topic `system`) | 5 |
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

**MikroTik (D10)**: las métricas salen de SNMP (MIB-II, IF-MIB, HOST-RESOURCES-MIB y MIKROTIK-MIB para
temperatura, voltaje, PSU y ventiladores) con los mismos tipos de arriba; la API de RouterOS (8729 TLS / REST) no es
fuente de series ([ADR-0022](./adr/0022-mikrotik-routeros-v7-primer-fabricante.md)) y sólo alimenta los hechos del alta
(`devices.router.capabilities_detected`, §8.2). Los sensores propios de MikroTik viajan en `horus.snmp.device.sampled`
como `"sensors": [{"name", "value", "unit"}]` (campo opcional, compatible).

Eventos de dominio derivados (por `snmp`, §6.3): `horus.snmp.router.state_changed` ya cubre umbrales (`reason`
admite `psu_failed`, `fan_failed` como valores nuevos del enum abierto); `horus.devices.router.capabilities_detected`
(§8.2) avisa de la deriva de configuración de Traffic Flow; la cobertura de flujos frente a contadores SNMP la vigila
`jobs` (`horus.jobs.coverage.low`, §8.13).

### 8.5 `flows` (productor `flows`)

Telemetría → `TLM_FLOWS`: `horus.flows.batch.received` en `horus.telemetry.flows.batch.<router_id>`, con
`Horus-Tenant` = tenant del router. El collector resuelve `(tenant, router)` a partir de la IP de origen del exportador,
única en la plataforma porque los MikroTik exportan por su túnel con `src-address` = IP de túnel de la IPAM de
plataforma ([ADR-0017](./adr/0017-multi-tenant-desde-v1.md) §8). Publica el
**collector**; consume el **ingester**, que clasifica en ingesta con el snapshot del catálogo y escribe `flows_raw` con
`catalog_version` ([ADR-0015](./adr/0015-enriquecimiento-de-flujos-en-ingesta.md)). No hay salto intermedio por
`traffic-intelligence` ni flujo enriquecido en NATS.

Los registros son **exactamente** el registro canónico de [`traffic-model.md`](./traffic-model.md) §3; el lote añade
`batch_id` y el contexto común del exportador. `bytes`/`packets` viajan **sin escalar** junto con `sampling_rate`; la
corrección `bytes_est = bytes × N` la hace el ingester ([`traffic-model.md`](./traffic-model.md) §4).

```jsonc
// horus.events.flows.v1.FlowBatch (Protobuf; representación JSON; tenant en la cabecera Horus-Tenant y en el lote)
{ "batch_id": "0192...", "collector_id": "flows-collector-1", "tenant_id": "0192e000-...", "router_id": "0192...",
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
// flow_source (MikroTik Traffic Flow, D10): netflow_v5 | netflow_v9 | ipfix
```

Notas: `sampling_rate` a nivel de lote es el valor por defecto del exportador; el del registro (si existe) prevalece. En
Protobuf las IPs son `bytes` (4/16), `bytes`/`packets` `uint64`, opcionales con `optional`. `batch_id` =
`insert_deduplication_token`. Registros sin plantilla se descartan y se cuentan (`horus_flows_dropped_total{reason="no_template"}`).

**Descubrimiento de clientes (D1, [ADR-0018](./adr/0018-la-ip-es-el-cliente.md) §3)**. Los publica el **ingester**,
que sabe qué lado del flujo es el cliente (prefijos de clientes del realm) y mantiene el conjunto de IPs conocidas por
realm. Nunca escribe en PostgreSQL.

| Tipo | Subject | Stream | Cuándo |
| --- | --- | --- | --- |
| `horus.flows.client.first_seen` | `horus.flows.client.first_seen.<realm_id>` | `FLOWS_EVENTS` | Lotes cada 10 s con las IPs no vistas del realm, deduplicadas (TTL) y con límite por realm/minuto (excesos: `horus_customers_discovery_throttled_total`). Sin spool: si se pierde, el siguiente flujo lo repite. |
| `horus.flows.client.activity_summary` | `horus.telemetry.flows.client_activity.<realm_id>` | `TLM_FLOWS` | Cada hora: IPs con tráfico en la hora (troceado < 512 KiB), para `last_seen`, reactivación e inactividad. |

**Nombre único**: `activity_summary` (de [`services.md`](./services.md) y [`database.md`](./database.md)); se descarta
`seen_summary`. Consumidor de ambos: `devices` (§4.5), que hace *upsert* idempotente por `(tenant, realm, address)` y
emite `customer.discovered` sólo cuando inserta.

```jsonc
// horus.flows.client.first_seen   (pii: [clients.address]; Horus-Tenant = tenant del realm)
{ "batch_id": "0192...", "realm_id": "0192...", "router_id": "0192...",
  "window_from": "2026-10-07T14:02:50Z", "window_to": "2026-10-07T14:03:00Z",
  "clients": [ { "address": "100.64.12.34", "client_prefix_id": "0192...", "first_seen": "2026-10-07T14:02:53Z" } ] }

// horus.flows.client.activity_summary   (pii: [clients.address])
{ "batch_id": "0192...", "realm_id": "0192...", "hour": "2026-10-07T13:00:00Z", "part": 1, "parts": 3,
  "clients": [ { "address": "100.64.12.34", "last_seen": "2026-10-07T13:59:41Z", "bytes_est": "184000000", "flows": 1320 } ] }
```

Dominio → `FLOWS_EVENTS` (publicación §6.3):

| Tipo | Entity | Consumidores |
| --- | --- | --- |
| `horus.flows.exporter.unregistered` (plataforma, `tenant_id` nulo) | exportador (UUIDv5 de la IP) | api-gateway (`platform.overview`: sugerir alta), alerts |
| `horus.flows.exporter.rate_limited` | router | alerts, api-gateway (`platform.overview`) |
| `horus.flows.exporter.silent` | router | alerts, detection |
| `horus.flows.exporter.recovered` | router | alerts, detection |
| `horus.flows.collector.data_gap` | collector (UUIDv5 del nombre) | alerts, analytics (cobertura) |

```jsonc
// horus.flows.exporter.silent
{ "router_id": "0192...", "exporter_ip": "10.0.0.1", "last_flow_at": "2026-10-07T13:57:40Z", "silent_after_seconds": 300 }

// horus.flows.exporter.rate_limited   (el collector descartó flujos por el límite del exportador o del tenant)
{ "router_id": "0192...", "quota_flows_per_second": 20000, "observed_flows_per_second": 31250,
  "dropped_records": 675000, "window_from": "2026-10-07T14:00:00Z", "window_to": "2026-10-07T14:01:00Z" }

// horus.flows.collector.data_gap
{ "collector_id": "flows-collector-1", "from": "2026-10-07T14:10:00Z", "to": "2026-10-07T14:22:13Z",
  "dropped_batches": 731, "dropped_records_estimated": 365500, "reason": "bus_unavailable" }
// reason: bus_unavailable | local_buffer_full | stream_full
```

### 8.6 `traffic` → `TRAFFIC_EVENTS` (productor `traffic-intelligence`)

| Tipo | Consumidores |
| --- | --- |
| `horus.traffic.catalog.published` (plataforma, `tenant_id` nulo: el catálogo es común a todos los ISP; entity = id de la versión) | **flows** (ingester recarga), analytics (diccionario `ip_trie`), detection |
| `horus.traffic.rule.updated` | analytics |
| `horus.traffic.audit.recorded` | auth |

```jsonc
// horus.traffic.catalog.published
{ "catalog_version_id": "0192...", "catalog_version": 17,
  "object_store": "catalog-snapshots", "object_name": "v17/catalog.bin", "sha256": "9f86d081884c7d65...",
  "size_bytes": 48211234, "format_version": 1, "published_at": "2026-10-07T06:00:00Z",
  "stats": { "prefixes": 982113, "asns": 74210, "services": 214, "categories": 18 } }
```

El snapshot se publica en **NATS Object Store** (bucket `catalog-snapshots`, [ADR-0019](./adr/0019-almacenamiento-local-y-destino-remoto.md)) y se verifica con `sha256`; el evento nunca lleva URL.

### 8.7 Reputación (dentro de `detection` → `DETECTION_EVENTS`)

Los feeds son **de plataforma**; la allowlist es **por tenant** y la aplica `detection` al correlacionar (no cambia el
snapshot que usa el ingester).

| Tipo | Consumidores |
| --- | --- |
| `horus.detection.reputation.snapshot_published` (plataforma) | **flows** (ingester: marca `reputation_hit` en ingesta), detection |
| `horus.detection.reputation.source_refreshed` (plataforma) | alerts (fallo de feeds), api-gateway |

```jsonc
// horus.detection.reputation.snapshot_published   (artefacto en NATS Object Store, bucket reputation-snapshots)
{ "snapshot_id": "0192...", "snapshot_version": 233, "object_store": "reputation-snapshots", "object_name": "v233/rep.bin",
  "sha256": "2c26b46b68ffc68f...", "entries": 182733, "sources": ["abuse_ch_feodo", "spamhaus_drop", "tor_exits"],
  "published_at": "2026-10-07T06:10:00Z" }
```

### 8.8 `detection` → `DETECTION_EVENTS` (productor `detection`)

Con D5 el propósito principal es **detectar clientes infectados o participando en botnets** y, en segundo lugar, el uso
comercial de IPs residenciales. Tres niveles: el **hallazgo** (una correlación explicable en una ventana), el **estado
de seguridad del cliente** (resumen que consume el NOC) y el **scoring de tipo** (residencial/comercial).

| Tipo | Consumidores | Sprint |
| --- | --- | --- |
| `horus.detection.finding.opened` | **alerts**, api-gateway | 8 |
| `horus.detection.finding.updated` / `.resolved` (incluye `resolution: false_positive`) | alerts, api-gateway | 8 |
| `horus.detection.customer.security_state_changed` (entity = `customer_id`) | **devices** (proyecta `security_state`), alerts, analytics, api-gateway | 8 |
| `horus.detection.customer.kind_suggested` (entity = `customer_id`) | **devices** (aplica `kind` si `kind_locked = false` y confianza ≥ umbral) | 10 |
| `horus.detection.score.changed` (entity = `customer_id`) | analytics (series de score) | 10 |
| `horus.detection.mitigation.*` | — (**reservado**; mitigación fuera de v1, [ADR-0024](./adr/0024-deteccion-de-botnets-como-objetivo-principal.md) §4) | — |
| `horus.detection.audit.recorded` | auth | 8 |

`finding.kind` (enum abierto, alineado con [`api.md`](./api.md) §2.10): `botnet_c2_communication`,
`ddos_participation`, `outbound_scanning`, `spam_smtp_outbound`, `open_proxy_abuse`, `cryptomining`, `beaconing`,
`reputation_hit`. Los hallazgos referencian `customer_id` (y `realm_id`), no la IP (§5.7). **Nombre**: `opened` (no
`created`, como en el borrador de [`services.md`](./services.md)) por simetría con `alerts.alert.opened` y con el estado
`open` del hallazgo.

```jsonc
// horus.detection.finding.opened   (correlación explicable, no "IP en lista = malware")
{ "id": "0192...", "version": 1, "state": "open", "kind": "botnet_c2_communication", "category": "security", "severity": "high",
  "confidence": 0.82, "subject_type": "customer", "customer_id": "0192...", "router_id": "0192...", "site_id": "0192...",
  "window_from": "2026-10-07T13:00:00Z", "window_to": "2026-10-07T14:00:00Z",
  "reasons": [
    { "code": "reputation_hit", "detail": "3 destinos en feed C2 (abuse_ch_feodo)", "weight": 0.40 },
    { "code": "periodic_beaconing", "detail": "intervalo 60 s ±2 s durante 50 min", "weight": 0.30 },
    { "code": "unusual_port", "detail": "TCP/8443 a ASN sin historial", "weight": 0.12 } ],
  "evidence": { "distinct_destinations": 3, "destination_asns": [64512], "flows": 51, "bytes_est": "48200" },
  "rule_version": "c2-contact@4", "reputation_snapshot_version": 233, "min_sampling_rate": 1 }
// D11: además "summary", "primary_target", "occurrences" y "recommended_actions": [{ "code", "title", "explanation",
//   "priority", "risk", "audience", "execution": "manual", "customer_message",
//   "routeros": { "min_version": "7.12", "commands": ["/ip firewall filter add … src-address={{customer_address}} …"],
//                 "undo_commands": [...], "placeholders": [...], "rendered_commands": null, "rendered_undo_commands": null } }]
//   Plantillas con placeholders: el evento NUNCA lleva la IP del cliente (§5.7); Horus no ejecuta los comandos.
//   Forma completa: packages/schemas/finding/v0/finding.schema.json (C8) y horus.events.detection.v1.Finding.
// ddos_participation: evidence { "target_asn", "target_prefix", "pps_peak", "protocol", "spoofing_suspected" }
// outbound_scanning:  evidence { "distinct_destinations", "destination_ports", "syn_ratio" }

// horus.detection.customer.security_state_changed
{ "customer_id": "0192...", "version": 3, "previous_state": "clean", "state": "suspected",
  "open_findings": 1, "top_kind": "botnet_c2_communication", "max_severity": "high",
  "since": "2026-10-07T14:00:00Z", "model_version": "security-v1" }
// state: clean | suspected (≥1 hallazgo abierto) | infected (hallazgos de alta confianza sostenidos o confirmados) |
//        mitigated (marcado por un operador tras actuar; vuelve a clean si no hay hallazgos en N días)

// horus.detection.customer.kind_suggested   (sólo al cambiar la sugerencia; histéresis de 7 días para no oscilar)
{ "customer_id": "0192...", "model_version": "scoring-v3", "previous_kind": "residential", "kind": "commercial",
  "confidence": 0.87, "scores": { "residential": 0.13, "commercial": 0.87 },
  "reasons": [ { "code": "many_devices_behind_cpe", "detail": "31 dispositivos distintos detrás del CPE", "weight": 0.35 },
               { "code": "business_hours_pattern", "detail": "uso sostenido 08:00–19:00 L–S", "weight": 0.30 },
               { "code": "upload_ratio_high", "detail": "ratio subida/bajada 0,62", "weight": 0.22 } ],
  "window_from": "2026-09-30T00:00:00Z", "window_to": "2026-10-07T00:00:00Z" }
```

> `reasons` pasa de lista de textos a objetos `{code, detail, weight}` (igual que en los hallazgos): `code` es estable y
> traducible, `detail` es texto humano. Es un cambio de forma antes de que exista ningún consumidor, así que se hace en
> `v1` sin `v2`. `devices` **copia** las razones en `customer.kind_changed` para que el historial de tipo no dependa
> de la retención de `detection`. La regla de aplicación (umbral de confianza, persistencia mínima antes de cambiar el
> tipo) vive en `devices` y es configurable por tenant (C-20).

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

// horus.alerts.notification.sent   (D13: campos añadidos de forma compatible; en I1 alert_id = null porque el
// canal mínimo reacciona directamente al evento de origen, sin motor de reglas)
{ "id": "0192...", "alert_id": "0192...", "channel_id": "0192...", "channel": "telegram", "recipient_user_id": "0192...",
  "event_type": "finding_opened", "source_event_type": "horus.detection.finding.opened", "source_event_id": "0192...",
  "is_test": false, "error": null, "occurred_at": "2026-10-07T14:03:12Z" }
// channel: email | telegram | librenms (previsto, D13: syslog / SNMP trap / API)
```

> **Contrato v0 (I0-05)**: catálogo por dominio en `packages/events/catalog/`, golden files en `packages/events/examples/`,
> payloads en `packages/protobuf/horus/events/`. Añade para el I1: `horus.flows.exporter.state_changed` (estados del
> exportador de I1-09; `silent`/`recovered` se siguen emitiendo) y la telemetría `horus.flows.traffic_summary.observed`
> en `horus.telemetry.flows.summary.<site_id>` (topic WebSocket `traffic.summary`). Ver `docs/contracts/G0.md`.

### 8.10 `analytics` y `reporting`

| Tipo | Stream | Productor | Consumidores |
| --- | --- | --- | --- |
| `horus.reporting.report.completed` | `REPORTING_EVENTS` | analytics (reporting-worker) | alerts (notificar), api-gateway (topic `me`) |
| `horus.reporting.report.failed` | `REPORTING_EVENTS` | ídem | alerts, api-gateway |
| `horus.reporting.audit.recorded` | `REPORTING_EVENTS` | ídem (exportaciones: quién, filtro, filas) | auth |
| `horus.analytics.archive.completed` | `ANALYTICS_EVENTS` | analytics (archiver) | alerts |
| `horus.analytics.audit.recorded` | `ANALYTICS_EVENTS` | analytics | auth |
| `horus.work.reporting.generate_report` (orden) | `WORK` | analytics (API) | reporting |

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

Cada servicio publica su auditoría vía **outbox** (o spool en `snmp`/`flows`) en `horus.<dominio>.audit.recorded.<entry_id>` (`tenant_id` nulo para acciones de plataforma),
dentro del stream de su dominio; **sólo `auth` la persiste** (`audit_log`, cadena de hashes; [`security.md`](./security.md)
§7.3). Payload = formato de registro de [`security.md`](./security.md) §7.2 **sin** `prev_hash`/`hash` (los calcula `auth`).
Es el único evento que lleva IP y User-Agent.

> Este nombre sustituye a `horus.audit.entry.recorded` (Agente 2) y `horus.audit.record.created` (versión previa de
> `security.md`); la convención final es la de [`architecture.md`](./architecture.md) §6.1 y [`security.md`](./security.md) §7.3.

```jsonc
// horus.devices.audit.recorded
{ "id": "0192...", "tenant_id": "0192e000-...", "occurred_at": "2026-10-07T15:10:00.120Z", "source_service": "devices",
  "actor": { "type": "user", "id": "0192...", "sid": "0192...", "via": "session" },
  "ip": "198.51.100.4", "user_agent": "Mozilla/5.0 ...", "action": "devices.credentials.write",
  "resource_type": "router", "resource_id": "0192...", "scope": "site:0192...",
  "outcome": "success", "reason": null,
  "changes": { "credential_kind": ["snmp_v2c", "snmp_v3"] },
  "request_id": "0192...", "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736" }
```

### 8.12 Dashboards y kioscos (`analytics` → `ANALYTICS_EVENTS`; D8)

| Tipo | Consumidores |
| --- | --- |
| `horus.analytics.dashboard.created` / `.updated` / `.deleted` | api-gateway (topic `dashboard.<id>`: las pantallas recargan), analytics (invalidar caché de widgets) |
| `horus.analytics.playlist.updated` / `.deleted` | api-gateway (kioscos que la usan) |
| `horus.analytics.audit.recorded` | auth |

```jsonc
// horus.analytics.dashboard.updated   (estado completo salvo la config de widgets si supera 64 KiB → se pide por REST)
{ "id": "0192...", "version": 13, "name": "NOC — Nodos norte", "visibility": "tenant", "owner_id": "0192...",
  "widget_count": 8, "changed_fields": ["widgets"], "updated_at": "2026-10-07T15:00:00Z" }
```

Los datos de los widgets **no** son eventos: se piden por REST o llegan por los topics de estado existentes. Los
kioscos (identidad) son de `auth` (`horus.auth.kiosk.*`, §8.1).

### 8.13 `jobs` → `JOBS_EVENTS` (archivado, copia remota, cobertura; D2)

Módulo `jobs` ([ADR-0025](./adr/0025-binario-modular-con-roles.md)); nombres de [`services.md`](./services.md).

| Tipo | Ámbito | Consumidores |
| --- | --- | --- |
| `horus.jobs.archive.completed` / `.failed` | tenant (partición de un tenant) o plataforma | alerts, api-gateway |
| `horus.jobs.backup.completed` / `.failed` (pgBackRest / clickhouse-backup locales) | plataforma | alerts, api-gateway (`system`, `platform.overview`) |
| `horus.jobs.remote_sync.completed` | plataforma | api-gateway |
| `horus.jobs.remote_sync.lagging` (retraso > umbral) / `.failed` | plataforma | **alerts** (`remote_copy_lagging`, `remote_destination_unhealthy`), api-gateway |
| `horus.jobs.coverage.low` (cobertura de flujos < 80 % frente a contadores SNMP) | tenant | **alerts** (`flow_coverage_low`), api-gateway, analytics (marca periodo incompleto) |
| `horus.jobs.audit.recorded` | plataforma | auth |

```jsonc
// horus.jobs.remote_sync.lagging   (entity = destino; sin credenciales)
{ "destination_id": "0192...", "kind": "sftp", "paths": ["backups/", "audit/"], "lag_seconds": 93600,
  "threshold_seconds": 86400, "pending_bytes": "18200000000", "last_success_at": "2026-10-06T04:12:31Z",
  "last_error": "dial tcp: i/o timeout" }

// horus.jobs.coverage.low   (entity = router; Horus-Tenant = tenant del router)
{ "router_id": "0192...", "site_id": "0192...", "hour": "2026-10-07T13:00:00Z", "coverage_ratio": 0.62,
  "threshold": 0.8, "likely_cause": "hardware_offload" }
```

Si **no hay** destino remoto configurado no se emite `remote_sync.*`: el estado `remote_storage: not_configured` lo
publica `GET /system/status` y lo vigila una alerta de baja severidad ([`disaster-recovery.md`](./disaster-recovery.md)
§3.0).

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

- Sin label de tenant en las métricas por `type`/`consumer` (cardinalidad); el tenant va en logs y trazas. Excepción:
  `horus_events_tenant_invalid_total{consumer}` (> 0 ⇒ alerta de seguridad, §2.4).
- `horus_events_published_total{type}`, `horus_events_publish_errors_total{type}`, `horus_outbox_pending`,
  `horus_outbox_oldest_age_seconds`, `horus_spool_bytes{kind}`.
- `horus_events_consumed_total{consumer,type,result=ack|nak|term|dlq|skipped}`, `horus_events_processing_seconds{consumer}`,
  `horus_consumer_pending{stream,consumer}` (num_pending), `horus_dlq_messages_total{consumer}`.
- `horus_telemetry_buffer_bytes{collector}`, `horus_telemetry_dropped_total{collector,reason}`.
- Logs: `event_id`, `subject`, `tenant_id`, `actor_id` (nunca nombre ni email del actor, ni la IP del cliente).
- Trazas: span `publish <type>` / `process <type>` enlazados por `trace_parent`; en telemetría por lotes se usa *span link*.
