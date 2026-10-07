# 0020 — Valkey en lugar de Redis como caché y almacén efímero

- Estado: Aceptada
- Fecha: 2026-10-07
- Decisores: product owner ([D3](../po-decisions.md)), Agente A (arquitectura, ronda 2)
- Sustituye: [ADR-0009](0009-redis.md)

## Contexto

ADR-0009 eligió Redis "o un fork compatible con licencia OSI, p. ej. Valkey, a decidir en Q11".
Redis cambió de licencia en 2024 (RSALv2/SSPL; desde Redis 8 añade AGPLv3). El PO pidió usar lo
recomendado ([D3](../po-decisions.md)): **Valkey** (BSD-3-Clause, Linux Foundation), compatible a
nivel de protocolo y comandos con Redis 7.2.

## Decisión

- **Valkey 8.x** sustituye a Redis en todos los usos. Clientes Go compatibles con el protocolo
  RESP (p. ej. `valkey-go` o `go-redis`); el código no depende de funciones exclusivas de Redis
  posteriores a 7.2.
- **Las reglas de uso de ADR-0009 se mantienen**: solo almacén **efímero y reconstruible** (caché
  de lectura, rate limiting GCRA, caché de revocación de sesiones con TTL, estados efímeros de UI).
  Prohibido como fuente de verdad, cola de trabajo, bus de eventos o lock de colectores (eso va en
  PostgreSQL o NATS KV).
- Multi-tenant ([ADR-0017](0017-multi-tenant-desde-v1.md)): claves de datos de tenant con prefijo
  `t:<tenant_id>:`; revocación por `sid`; rate limit por usuario/IP más límite global por tenant en
  `analytics`.
- Despliegue: un contenedor, sin persistencia (o AOF opcional), `maxmemory` con `allkeys-lru` en la
  base de cachés y `volatile-ttl` en la base de rate limit/revocación. Se incluye desde el primer
  incremento porque es barato y las rutas de degradación ya están diseñadas.
- Si Valkey cae, el comportamiento es el de [architecture.md §10.7](../architecture.md#107-valkey-se-cae)
  (solo degradación de latencia; revocación vía `auth` con caché en proceso de 30 s; rate limit en
  memoria).

## Alternativas consideradas

- **Redis 8 (AGPLv3)**: aceptable para uso interno, pero añade obligaciones si Horus se distribuye
  o se ofrece como servicio a varios ISP (multi-tenant).
- **KeyDB / DragonflyDB**: compatibles, pero menor respaldo comunitario (KeyDB) o licencia BSL
  (Dragonfly).
- **Sin caché compartida** (caché en proceso): viable con una réplica del gateway; no comparte rate
  limit ni revocación entre réplicas.
- **NATS KV para todo**: posible para revocación, menos adecuado para contadores de alta
  frecuencia.

## Consecuencias

- (+) Licencia permisiva sin riesgo para un despliegue multi-ISP o una distribución futura.
- (+) Sin cambios de diseño: mismo protocolo y mismos usos.
- (−) Si en el futuro se quisiera una función exclusiva de Redis (p. ej. módulos de Redis Stack),
  no estará disponible; no se prevé.
- Impacto: renombrar Redis → Valkey en [security.md](../security.md),
  [observability.md](../observability.md) (`valkey_up`), [conventions.md](../conventions.md) y
  compose.
