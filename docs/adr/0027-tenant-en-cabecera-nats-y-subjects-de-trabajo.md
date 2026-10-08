# 0027 — Tenant en la cabecera `Horus-Tenant` y órdenes de trabajo en `horus.work.>`

- Estado: Aceptada
- Fecha: 2026-10-08
- Decisores: coordinador, a partir de la propuesta del Agente C (ronda 2)
- Relacionado: [ADR-0017](0017-multi-tenant-desde-v1.md), [ADR-0006](0006-nats-jetstream-bus-de-eventos.md),
  [`events.md`](../events.md)

## Contexto

ADR-0017 decidió no poner el tenant en los subjects de NATS. Hace falta que cada consumidor sepa
a qué tenant pertenece un mensaje sin parsear el cuerpo, y que un mensaje sin tenant no llegue a
procesarse. Además, el módulo `jobs` del binario modular ([ADR-0025](0025-binario-modular-con-roles.md))
publica eventos de dominio (`horus.jobs.coverage.low`…); un stream `workqueue` sobre
`horus.jobs.>` se los habría consumido.

## Decisión

- Todo mensaje lleva la cabecera **`Horus-Tenant`** (`<tenant_id>`, o `platform` para eventos de
  plataforma) además de `tenant_id` en el sobre. La librería común (`natsx`) la exige al publicar
  y la valida al consumir: si falta o no coincide con el sobre, el mensaje va a la DLQ y se
  dispara la alerta `TenantIsolationViolation`.
- Las órdenes de trabajo (`workqueue`) usan **`horus.work.>`** (stream `WORK`). `horus.jobs.>`
  queda para los eventos del dominio `jobs` (stream `JOBS_EVENTS`).
- Una sola cuenta NATS en v1.

## Alternativas consideradas

- **Tenant como token del subject:** permite filtrar y purgar por tenant en el broker, pero
  cambia todos los subjects y filtros. Se puede adoptar más adelante con cuentas NATS por
  tenant si hace falta aislamiento físico.

## Consecuencias

- Al dar de baja un ISP, sus eventos no se pueden purgar selectivamente del bus: caducan con la
  retención del stream (30 días). Pregunta abierta C-22.
