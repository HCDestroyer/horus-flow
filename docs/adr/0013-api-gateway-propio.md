# 0013 — API Gateway propio (Go) detrás de Traefik, frente a Traefik/Kong/Envoy

- Estado: Propuesta (recomendada)
- Fecha: 2026-10-07

## Contexto

El frontend necesita un punto de entrada único que haga: TLS, autenticación con tokens propios y
**revocación de sesiones**, autorización gruesa por permisos, rate limiting por usuario/IP,
enrutamiento a ~10 servicios, **WebSocket con fan-out de eventos NATS filtrados por permisos**, y
trazas OpenTelemetry. `vision.md` lista "api-gateway" como servicio y "Nginx/Caddy/Traefik" como
reverse proxy.

## Decisión

Dos capas:

1. **Traefik** como proxy de borde: TLS (ACME o certificados internos), HTTP/2, compresión,
   cabeceras de seguridad, servir el SPA estático, redirección HTTP→HTTPS. Sin lógica de negocio.
   Se elige Traefik sobre Nginx/Caddy porque su configuración por labels de Docker se traduce
   directamente a `IngressRoute`/Gateway API en Kubernetes ([ADR-0011](0011-docker-compose-antes-que-kubernetes.md)).
2. **`api-gateway` propio en Go (Chi)**: autenticación (JWT + revocación en Redis/auth), control
   grueso de permisos, rate limiting (Redis GCRA con fallback local), proxy HTTP a los REST de los
   servicios (tabla de rutas declarativa), WebSocket fan-out desde NATS core, circuit breaker por
   servicio, OTel. Responsabilidades detalladas en [architecture.md §7](../architecture.md#7-api-gateway).

## Alternativas consideradas

| Opción | A favor | En contra | Veredicto |
| --- | --- | --- | --- |
| **Solo Traefik** (ForwardAuth + middlewares) | Cero código; rate limit y routing incluidos | ForwardAuth = 1 llamada HTTP extra por request a `auth`; rate limit en memoria por instancia (sin Redis nativo en OSS); **no puede hacer fan-out de NATS a WebSocket**: necesitaría otro servicio de todos modos | Insuficiente |
| **Kong (OSS)** | Plugins de JWT, rate limit con Redis, maduro | Requiere Lua/Go plugins para revocación de sesión y filtrado por permisos; PostgreSQL propio o modo DB-less; WebSocket solo como proxy (sin fan-out); muchas funciones avanzadas son Enterprise | Pesado para lo que aporta |
| **Envoy** (+ ext_authz, ratelimit service) | Muy potente, nativo en K8s/service mesh | Configuración compleja, requiere servicios auxiliares (ratelimit en Go + Redis, ext_authz) — es decir, igualmente se escribe código; curva alta | Sobredimensionado en v1 |
| **KrakenD / Tyk** | Gateway declarativo con agregación | Fan-out NATS→WS y revocación personalizada fuera de su modelo | No encaja |
| **Gateway propio en Go** | Control total de auth, revocación, permisos y WS; mismo stack, mismo OTel; ~1–2k líneas | Código a mantener y asegurar | **Recomendado** |

## Consecuencias

- (+) La funcionalidad más específica (WS fan-out filtrado por permisos, revocación) vive en el
  mismo lenguaje y librerías que el resto; sin plugins en Lua.
- (+) TLS y estáticos en Traefik, que es probado y migra a K8s como Ingress.
- (−) El gateway es código propio en el camino crítico de seguridad: requiere tests exhaustivos y
  revisión de seguridad ([security.md](../security.md)).
- (−) Dos saltos (Traefik → gateway → servicio); latencia añadida ~1 ms, aceptable.
- Revisión: si en K8s se adopta un service mesh o Gateway API con Envoy, el `api-gateway` puede
  reducirse a auth + WebSocket y delegar routing/rate limit.
