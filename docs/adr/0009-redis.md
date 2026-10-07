# 0009 — Redis como caché y almacén efímero

- Estado: Sustituido por ADR-0020
- Fecha: 2026-10-07

## Contexto

`vision.md` asigna a Redis: caché, sesiones temporales, rate limiting, estados rápidos y locks
distribuidos. Si Redis se usa como fuente de verdad de algo, su caída se vuelve un incidente del
plano de administración.

## Decisión

**Redis** (o un fork compatible con licencia OSI, p. ej. Valkey, a decidir en
[Q11](../open-questions/architecture.md#q11)) estrictamente como **almacén efímero y reconstruible**:

| Uso permitido | Ejemplo |
| --- | --- |
| Caché de lectura | Resultados de analytics (TTL 30–300 s), nombres de routers. |
| Rate limiting | Contadores GCRA del gateway. |
| Caché de revocación de sesiones | `session_revoked:<sid>` con TTL = vida del access token. La fuente de verdad es `auth.sessions` en PostgreSQL. |
| Estados efímeros de UI | Flags temporales (p. ej. "sondeo en curso"). |

**No permitido**: fuente de verdad de sesiones, colas de trabajo, eventos, leases/locks de
colectores (van en NATS KV, [ADR-0006](0006-nats-jetstream-bus-de-eventos.md)). Locks en Redis
solo para optimizaciones donde una doble ejecución es inocua.

Persistencia desactivada o AOF opcional; `maxmemory` con política `allkeys-lru` salvo para claves
de rate limit/revocación (base lógica separada con `volatile-ttl`).

## Alternativas consideradas

- **Sin Redis (caché en proceso + PostgreSQL)**: viable para 1 réplica, pero el rate limit y la
  revocación no se comparten entre réplicas del gateway.
- **NATS KV para todo**: posible, pero Redis es más adecuado para contadores de alta frecuencia.
- **Memcached**: sin estructuras ni TTL por clave tan flexibles.

## Consecuencias

- (+) Caída de Redis = solo degradación de latencia ([architecture.md §10.7](../architecture.md#107-redis-se-cae)).
- (−) Ventana de hasta 30 s en la que una sesión revocada puede seguir aceptada si Redis cae
  (fallback a `auth` con caché en proceso).
