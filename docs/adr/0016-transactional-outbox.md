# 0016 — Publicación de eventos de dominio con transactional outbox

- Estado: Propuesta (recomendada)
- Fecha: 2026-10-07

## Contexto

Los servicios con PostgreSQL (auth, devices, wireguard, traffic-intelligence, detection, alerts,
analytics) deben publicar eventos cuando cambia su estado. Escribir en PostgreSQL y luego publicar
en NATS no es atómico: si NATS está caído o el proceso muere entre ambos pasos, el evento se pierde
o se publica un cambio que no se confirmó. El requisito de que NATS pueda reiniciarse sin pérdida
de hechos de dominio lo exige ([architecture.md §10.3](../architecture.md#103-nats-se-reinicia-o-cae)).

## Decisión

- Cada esquema tiene una tabla `outbox` (`id` UUIDv7 = `event_id`, `subject`, `payload`,
  `headers`, `aggregate_id`, `created_at`, `published_at`). El cambio de estado y el `INSERT` en
  outbox van en **la misma transacción**.
- Un **relay** dentro de cada servicio (goroutine) lee filas no publicadas
  (`FOR UPDATE SKIP LOCKED`, orden por `id`), publica en JetStream con `Nats-Msg-Id = event_id`
  (dedupe del lado del servidor) y marca `published_at`. Con varias réplicas, `SKIP LOCKED` evita
  dobles envíos concurrentes; el orden por agregado se preserva procesando en serie por
  `aggregate_id`.
- Activación: `LISTEN/NOTIFY` para baja latencia + sondeo cada 1 s como respaldo.
- Limpieza: filas publicadas se borran tras 7 días.
- Consumidores **idempotentes** por `event_id` (entrega at-least-once). El formato del evento y la
  política de dedupe son del Agente 3 ([events.md](../events.md)).
- Telemetría (`snmp`, `flows`) **no** usa outbox: publica directo con buffer acotado y pérdida
  medida.

## Alternativas consideradas

- **Publicar después del commit, sin outbox**: simple, pierde eventos ante fallos.
- **CDC con Debezium (WAL → NATS)**: sin código de relay, pero añade Kafka Connect/Debezium Server
  y acopla eventos al esquema físico de tablas.
- **Transacciones distribuidas (2PC)**: no soportado por NATS y no deseable.

## Consecuencias

- (+) Ningún evento de dominio se pierde por caídas de NATS o del proceso.
- (+) Cuando NATS cae, las operaciones REST siguen funcionando.
- (−) Latencia extra de publicación (ms a 1 s) y una tabla más por servicio.
- (−) Duplicados posibles (tras crash entre publish y marcar): resueltos por dedupe en NATS e
  idempotencia en consumidores.
