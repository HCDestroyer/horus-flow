# packages/events — catálogo de eventos (C4, contrato v0)

> **Estado: v0 aprobado (`stable`) el 2026-10-08 con D17–D21** ([`docs/contracts/G0.md`](../../docs/contracts/G0.md)). Desde aquí, todo cambio incompatible exige versión nueva (`v1`…) conviviendo con `v0`.

- **Dueño:** INT (cambios de `pii`/`tenant_scope` requieren a la persona).
- **Documentación:** [`docs/events.md`](../../docs/events.md), [ADR-0006](../../docs/adr/0006-nats-jetstream-bus-de-eventos.md),
  [ADR-0027](../../docs/adr/0027-tenant-en-cabecera-nats-y-subjects-de-trabajo.md), [`docs/contracts/G0.md`](../../docs/contracts/G0.md).

| Ruta | Contenido |
| --- | --- |
| `catalog/<dominio>.yaml` | Tipos del I0/I1: subject, `tenant_scope`, stream, familia, codificación, mensaje Protobuf, productor, consumidores, topics WS, `pii_fields`, estado |
| `examples/<type>.json` | Golden file por tipo (sobre completo; telemetría como cabeceras + data) |
| `v0/envelope.schema.json` | JSON Schema del sobre para validar golden files (el IDL sigue siendo Protobuf: `horus.events.v1.Envelope`) |
| `streams/streams.yaml` | Streams JetStream y durables de referencia del I1 (los aplica `infrastructure/nats`, PLAT) |

Payloads: `packages/protobuf/horus/events/<dominio>/v1/`. La verificación (`make contracts-check`) comprueba que cada
tipo tiene golden file, que el sobre valida, que los tipos de tenant llevan `tenant_id`, que un stream cubre el subject
y que `data` encaja con su mensaje Protobuf sin campos desconocidos.
