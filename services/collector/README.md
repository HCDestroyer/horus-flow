# `mod:collector` — services/collector

- **Propósito:** Receptor UDP IPFIX/NetFlow/sFlow; identifica el exportador por IP de túnel.
- **Rol(es) de `horus`:** `collector`
- **Agente dueño:** FLOW ([`docs/backlog/team.md`](../../docs/backlog/team.md) §2, [`.github/CODEOWNERS`](../../.github/CODEOWNERS))
- **Ficha del módulo:** [`docs/services.md`](../../docs/services.md) §3 · Roles y fronteras: [ADR-0025](../../docs/adr/0025-binario-modular-con-roles.md)
- **Layout y reglas:** [`docs/conventions.md`](../../docs/conventions.md) §2.1 — `api/` (contrato público), `internal/` (`config`, `domain`, `app`, `adapters`), `migrations/` (si tiene esquema PostgreSQL). Otros módulos solo importan `services/collector/api`.

## Qué hace (I1-03, I1-09)

1. Recibe UDP (IPFIX y NetFlow v9 en ambos puertos) y **solo acepta exportadores registrados**
   (IP de túnel /32 del inventario); el resto se descarta (`reason="unknown_exporter"`) y se
   emite `horus.flows.exporter.unregistered` (plataforma, cada 10 min por IP).
2. Decodifica leyendo las plantillas de cada exportador (`internal/decode`, independiente de
   flowsim y de goflow2); los datos que llegan antes que su plantilla se retienen 30 s.
3. Mide **saltos de secuencia** (IPFIX: registros; v9: datagramas) y el desfase de `exportTime`.
4. Descarta flujos del túnel (origen o destino = IP de túnel) y de prefijos `excluded`.
5. Publica lotes `FlowBatch` (≤ 500 registros o 1 s) en `horus.telemetry.flows.batch.<router_id>`
   con el sobre en cabeceras (`Horus-Tenant`, `Nats-Msg-Id = batch_id`). La recepción nunca se
   bloquea: búfer en memoria acotado; si se llena, se descarta lo nuevo y al volver el bus se
   publica `horus.flows.collector.data_gap`.
6. Calcula el **estado del exportador** (pending_configuration, exporting, silent > 2 min,
   lossy > 1 % en 5 min, clock_skew > 30 s) en el bucket KV `flow_exporter_state` y publica
   `horus.flows.exporter.{state_changed,silent,recovered}`. Lo sirve el ingester en
   `GET /flow-exporters`.

7. **Decodificación multihilo de un mismo exportador** (`HORUS_COLLECTOR_DECODE_WORKERS`):
   cada carril hace en serie lo que toca estado (plantillas, retenidos, opciones; luego
   secuencia, estado y lotes en orden de llegada) y un pool de N hilos decodifica y filtra los
   conjuntos de datos (`internal/app/parallel.go`). Con 1 hilo, el comportamiento anterior.
   Equivalencia N=1/N=8 probada (mismos registros en el mismo orden, huecos y lotes) y test
   dorado con 1 y 8 hilos.
8. **Spool a disco** (`internal/spool`, docs/architecture.md §10.15): si NATS no acepta lotes (o
   el búfer en memoria pasa de `HORUS_COLLECTOR_SPOOL_AFTER_RATIO`), los lotes van a segmentos
   con CRC32C y se reenvían en orden con el mismo `Nats-Msg-Id`; lleno, descarta lo más antiguo
   y lo cuenta (`data_gap` con `reason=spool_full`).
9. **Estado tras un reinicio** (D23, bucket KV `flow_collector_state`): reinstala las plantillas
   y la secuencia de cada dominio de observación; lo que el router envió con el collector caído
   se mide (`horus_collector_downtime_lost_records_total`, `data_gap` con
   `reason=collector_down`) sin contarlo como pérdida del exportador.

`collector.Replay` reproduce capturas (pcap, pcapng, hfsim) conservando la IP de origen: test
dorado (`tests/flows`), laboratorio y fixtures del simulador.

## Variables

| Variable | Defecto | Uso |
|---|---|---|
| `HORUS_NATS_URL` | — | NATS (sin ella, en dev el rol queda inactivo) |
| `HORUS_COLLECTOR_LISTEN` | `:4739,:2055` | Sockets UDP |
| `HORUS_FLOWS_INVENTORY_FILE` | — | Inventario base (YAML/JSON, `packages/go/flowinv`); con NATS se completa con DEVICES_EVENTS |
| `HORUS_COLLECTOR_BATCH_MAX_RECORDS` / `_BATCH_MAX_AGE` | 500 / 1s | Corte de lotes |
| `HORUS_COLLECTOR_BUFFER_BYTES` | 256 MiB | Búfer ante caída del bus |
| `HORUS_COLLECTOR_UDP_RCVBUF` | 32 MiB | Búfer de recepción de cada socket UDP; el kernel lo limita a `net.core.rmem_max` (avisa en el log) salvo con `CAP_NET_ADMIN` |
| `HORUS_COLLECTOR_PENDING_TTL` | 30s | Retención de datos sin plantilla |
| `HORUS_COLLECTOR_WORKERS` | 4 | Carriles (cada exportador va siempre al mismo) |
| `HORUS_COLLECTOR_DECODE_WORKERS` | 0 (= nº de CPU) | Hilos que decodifican en paralelo un mismo exportador; 1 = un hilo por carril |
| `HORUS_COLLECTOR_QUEUE_DATAGRAMS` | 8192 | Cola de cada carril (llena = `reason="queue_full"`) |
| `HORUS_COLLECTOR_TEMPLATE_TTL` | 30m | Antigüedad máxima de una plantilla guardada para reinstalarla al arrancar |
| `HORUS_COLLECTOR_SPOOL_DIR` | — (compose: `/var/lib/horus/collector-spool`) | Spool a disco; vacío = sin spool |
| `HORUS_COLLECTOR_SPOOL_BYTES` | 8 GiB | Tamaño máximo del spool (≈ 38 min a 20 000/s y ≈ 2 h a 6 500/s con 185 B por flujo; §10.15) |
| `HORUS_COLLECTOR_SPOOL_SEGMENT_BYTES` | 64 MiB | Tamaño de segmento |
| `HORUS_COLLECTOR_SPOOL_FSYNC` | 1s | fsync del spool y su cursor: `0` en cada escritura, `<0` solo al cerrar segmento |
| `HORUS_COLLECTOR_SPOOL_AFTER_RATIO` | 0.5 | Con el bus lento, ocupación del búfer en memoria a partir de la que se usa el spool |
| `HORUS_COLLECTOR_SILENT_AFTER`, `_LOSS_THRESHOLD`, `_LOSS_WINDOW`, `_CLOCK_SKEW` | 2m, 0.01, 5m, 30s | Estado del exportador |
| `HORUS_NATS_ENSURE_STREAMS` | false | Crea TLM_FLOWS/FLOWS_EVENTS si faltan (dev/tests) |

Métricas: `horus_collector_{datagrams,records,received_records,dropped,sequence_gaps,lost_records,batches_published,downtime_lost_records}_total`,
`horus_collector_clock_skew_seconds`, `horus_collector_buffer_bytes`, `horus_collector_bus_connected`; spool:
`horus_collector_spooling`, `horus_collector_spool_{bytes,batches,records,oldest_age_seconds}`,
`horus_collector_spool_{written,replayed,dropped,dropped_records}_total`; autonomía:
`horus_collector_ingress_bytes_per_second` y `horus_collector_autonomy_seconds{buffer="memory|spool"}`
(cuánto aguanta cada búfer sin bus al ritmo actual). `/readyz`: `spool` degradado mientras los
lotes van a disco o si el directorio no se puede usar.

Tests: `make test-collector` (replay de fixtures + fuzzing), `make test-flows-golden`.
