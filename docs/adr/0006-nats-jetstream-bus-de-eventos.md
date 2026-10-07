# 0006 — NATS JetStream como bus de eventos

- Estado: Aceptada
- Fecha: 2026-10-07

## Contexto

Los colectores (SNMP, flujos) no deben esperar a los consumidores (P3 en
[architecture.md](../architecture.md)); los servicios de dominio necesitan propagar hechos
(`router.created`, `peer.revoked`) sin acoplarse; la UI necesita eventos en tiempo real; y ante una
caída de ClickHouse el sistema debe amortiguar telemetría. `vision.md` elige NATS JetStream.

## Decisión

**NATS con JetStream** (servidor 2.10+), con estos usos:

| Uso | Mecanismo |
| --- | --- |
| Eventos de dominio | Streams JetStream con retención `limits` (p. ej. 7 días), consumers durables por servicio, entrega *at-least-once*, dedupe por `Nats-Msg-Id` = `event_id` en la ventana del stream + idempotencia en el consumidor. |
| Telemetría (flujos, métricas SNMP) | Streams separados, retención por `max_bytes` (buffer ante caídas de ClickHouse), `discard: old`, almacenamiento `file` en SSD local. |
| Colas de trabajo (reportes) | Stream con retención `workqueue`. |
| UI en tiempo real | NATS **core** (sin persistencia), consumido por el api-gateway. |
| Coordinación | **NATS KV** para leases de shards de colectores, locks de jobs singleton y estado actual de routers. Evita depender de Redis para coordinación. |

Topología: **nodo único** en v1 (Compose); **clúster de 3** con réplicas R3 al pedir HA
(Sprint 14) o migrar a Kubernetes. Subjects `horus.<dominio>.<entidad>.<evento>`; el contrato
detallado (streams, retenciones, esquemas) es del Agente 3 en [events.md](../events.md).

## Alternativas consideradas

- **Apache Kafka / Redpanda**: más throughput y ecosistema de stream processing, pero operación
  más pesada (Kafka) y sin KV/request-reply/core pub-sub ligero; sobredimensionado para ≤ 300
  routers. Redpanda sería la alternativa si el volumen de flujos exige particionado masivo
  (> 1M flujos/s sostenidos).
- **RabbitMQ**: bueno para colas, peor para replay/retención por bytes y para telemetría masiva.
- **Redis Streams**: mezclaría caché con durabilidad; Redis se define como desechable
  ([ADR-0009](0009-redis.md)).
- **Inserción directa colector → ClickHouse**: menos piezas, pero sin buffer ante caídas y acopla
  colectores a ClickHouse.

## Consecuencias

- (+) Un solo binario (~20 MB) cubre pub/sub, streams persistentes, colas, KV y request/reply.
- (+) Amortiguación cuantificable ante caídas de ClickHouse (ver [architecture.md §9.4](../architecture.md#94-capacidad-de-buffer-ante-caída-de-clickhouse)).
- (−) Nodo único = SPOF de la mensajería en v1; mitigado con outbox ([ADR-0016](0016-transactional-outbox.md)) y buffers en colectores.
- (−) A > 300 routers sin muestreo, persistir todos los flujos en JetStream es exigente; requiere
  clúster y particionado de subjects, o pasar flujos a NATS core.
