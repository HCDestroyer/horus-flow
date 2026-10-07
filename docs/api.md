# Convenciones de API — Horus Flow

> Estado: **propuesta Sprint 0** · Dueño: Agente 3 (contratos e integración) · Fuente: [`vision.md`](./vision.md)
>
> Relacionados: [`events.md`](./events.md) (contrato NATS), [`architecture.md`](./architecture.md) §7 y
> [ADR-0013](./adr/0013-api-gateway-propio.md) (gateway), [`services.md`](./services.md) (rutas por servicio),
> [ADR-0005](./adr/0005-grpc-protobuf-interno.md) (gRPC), [`security.md`](./security.md) §5–§6 (sesiones, permisos),
> [`database.md`](./database.md) (columna `version`), [`conventions.md`](./conventions.md),
> [`open-questions/contracts.md`](./open-questions/contracts.md).

Este documento fija tres contratos:

1. **REST público** `/api/v1` que el frontend Nuxt consume a través del `api-gateway` (§1–§3).
2. **WebSocket** de tiempo real en el `api-gateway` (§4).
3. **gRPC interno** servicio↔servicio (§5).

Topología que este contrato asume (decidida en [ADR-0013](./adr/0013-api-gateway-propio.md)):

```
Nuxt ──HTTPS/WSS──► Traefik (TLS, estáticos) ──► api-gateway (Go/Chi: JWT + revocación, permiso grueso,
                                                   rate limit, idempotencia, WS fan-out)
                                                        │ HTTP (proxy, sin traducir)        │ NATS core
                                                        ▼                                   ▼
                                              REST propio de cada servicio (Chi)     eventos → WS
                                                        │ gRPC (servicio↔servicio, mTLS)
```

- El gateway **no traduce REST↔gRPC**: cada servicio expone su propio REST (Chi) y el gateway hace proxy por una
  tabla declarativa de rutas. Todo lo de §1 aplica **a cada servicio**; el gateway sólo añade lo transversal
  (validación del JWT y revocación, permiso grueso, rate limit, idempotencia, request id).
- gRPC sólo entre servicios (y gateway → `auth.SessionService/CheckSession`). Nunca hacia el frontend.

---

## 1. REST público

### 1.1 Base, versionado y formato

| Tema | Decisión |
| --- | --- |
| Prefijo | `/api/v1/...`. La versión **mayor** va en la ruta y es **global** (no por servicio): el frontend ve una sola API. |
| Compatibilidad en `v1` | Permitido: añadir campos de respuesta, endpoints, parámetros opcionales, valores en enums declarados "abiertos". Prohibido: quitar/renombrar campos, cambiar tipos, volver obligatorio un parámetro, cambiar semántica. El cliente ignora campos desconocidos. |
| Deprecación | Headers `Deprecation` (RFC 9745) + `Sunset` (RFC 8594) + `Link: <...>; rel="deprecation"`; mínimo un release menor de convivencia. |
| Formato | `application/json; charset=utf-8`; claves `snake_case`. |
| IDs | UUIDv7 canónico (`0192f0c4-7a1e-7c3a-9b1d-2f6e8a4c1d55`). Nunca IDs autoincrementales. |
| Tiempos | UTC, RFC 3339 con `Z` y milisegundos (`2026-10-07T14:03:11.123Z`). Instantes con sufijo `_at`. |
| Duraciones | Enteros con unidad en el nombre: `poll_interval_seconds`, `uptime_seconds`. |
| Tráfico | `bytes`, `packets` enteros; tasas `*_bps`/`*_pps`. Contadores que pueden superar 2^53 (contadores SNMP de 64 bits) se serializan como **string** decimal, documentado por campo. |
| Nulos | Un opcional sin valor se envía `null` (no se omite) para que el tipo TS sea estable. En `PATCH`, `null` = borrar el valor. |
| Enums | strings `snake_case`: `"online"`, `"degraded"`, `"offline"`. |

Rutas fuera de `/api/v1` (no versionadas): `/healthz`, `/readyz` (por servicio y gateway), `/metrics` (puerto `9100`,
sólo red interna). Puertos según [`services.md`](./services.md) §2: HTTP `8080`, gRPC `9090`, métricas `9100`.

### 1.2 Recursos y nombres

- Colecciones en **plural, kebab-case**: `/sites`, `/routers`, `/wireguard/servers`, `/audit-events`.
- Anidamiento de **un nivel** como máximo, para listar por padre: `GET /routers/{router_id}/interfaces`; el hijo
  también es direccionable plano: `GET /interfaces/{interface_id}`.
- **Acciones** no CRUD: `POST /{coleccion}/{id}/{verbo-kebab}` (`/wireguard/peers/{id}/rotate-key`,
  `/users/{id}/disable`, `/alerts/{id}/acknowledge`). Siempre `POST`.
- Recursos del usuario actual bajo `/me`.
- **Prefijo de ruta ⇒ servicio dueño** (tabla del gateway, §3.1). Un recurso no se reparte entre servicios salvo
  sub-rutas explícitas (p. ej. `/routers/{id}/metrics/live` → `snmp`).

### 1.3 Métodos y códigos HTTP

| Método | Uso | Éxito |
| --- | --- | --- |
| `GET` | Leer recurso o colección. | `200` (`304` con `If-None-Match`) |
| `POST` (colección) | Crear. Devuelve el recurso + `Location` + `ETag`. | `201` |
| `POST` (acción) | Síncrona → `200`; asíncrona (reportes, sondeo forzado) → `202` + `Location` del recurso/ejecución. | `200` / `202` |
| `PATCH` | Parcial con **JSON Merge Patch** (RFC 7396); acepta `application/merge-patch+json` y `application/json` con la misma semántica. Requiere `If-Match`. | `200` (recurso actualizado) |
| `PUT` | Sólo reemplazo de asociaciones (`PUT /users/{id}/role-assignments`) y secretos write-only (`PUT /routers/{id}/snmp-credentials`). | `200` / `204` |
| `DELETE` | Borrado (lógico o físico según [`database.md`](./database.md)). Requiere `If-Match`. Ya borrado → `404`. | `204` |

Conjunto **cerrado** de códigos de error:

| Código | Cuándo |
| --- | --- |
| `400` | JSON mal formado, parámetro con tipo inválido, cursor corrupto. |
| `401` | Sin token, token expirado (`TOKEN_EXPIRED`: el cliente hace refresh y reintenta) o sesión revocada. Header `WWW-Authenticate: Bearer error="invalid_token"`. |
| `403` | Sin permiso `recurso.accion`, fuera de alcance ACL, `Origin` no permitido, 2FA o re-autenticación reciente requerida (`code` lo distingue). |
| `404` | No existe **o** el usuario no puede saber que existe (filtro por alcance). |
| `405` | Método no soportado (`Allow`). |
| `409` | Conflicto de estado o unicidad; `Idempotency-Key` en vuelo. |
| `412` | `If-Match` no coincide. |
| `413` | Cuerpo > 1 MiB (salvo subidas explícitas). |
| `415` | `Content-Type` no soportado. |
| `422` | Validación semántica (lista `errors[]`). |
| `428` | Falta `If-Match` o `Idempotency-Key` obligatorio. |
| `429` | Rate limit (`Retry-After`). |
| `500` | Error no controlado (con `trace_id`, sin detalles internos). |
| `502` / `503` / `504` | Servicio caído / degradado / timeout. Ej.: ClickHouse caído → endpoints analíticos `503` `ANALYTICS_UNAVAILABLE`, mientras login, inventario y WireGuard siguen. El gateway devuelve `503 SERVICE_UNAVAILABLE` si el circuit breaker del servicio está abierto. |

### 1.4 Formato de error — RFC 9457 `application/problem+json`

Lo producen **los servicios** (librería común, ver [`conventions.md`](./conventions.md)) y el gateway para sus propios
rechazos (401, 403 grueso, 429, 502–504). Mismo formato en ambos:

```json
{
  "type": "https://docs.horus-flow.local/errors/validation-failed",
  "title": "La solicitud contiene campos inválidos",
  "status": 422,
  "detail": "2 campos no pasaron la validación",
  "instance": "/api/v1/routers",
  "code": "VALIDATION_FAILED",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "request_id": "0192f0c4-7a1e-7c3a-9b1d-2f6e8a4c1d55",
  "errors": [
    { "field": "management_ip", "code": "INVALID_IP", "message": "no es una dirección IPv4/IPv6 válida" },
    { "field": "site_id", "code": "NOT_FOUND", "message": "el sitio no existe" }
  ]
}
```

- **`code` es el contrato**: UPPER_SNAKE_CASE, inglés, estable. `title`/`detail` son texto humano en español y pueden
  cambiar; el frontend traduce por `code`.
- `code` es idéntico al `ErrorInfo.reason` de gRPC (§5.6): si un servicio falla por un error gRPC de otro servicio,
  propaga el `reason` tal cual.
- `errors[].field` con notación de puntos para anidados/arrays: `allowed_ips.0`.
- `412` incluye la extensión `current` con la representación actual del recurso (para que la UI muestre el conflicto).
- Códigos transversales: `VALIDATION_FAILED`, `UNAUTHENTICATED`, `TOKEN_EXPIRED`, `SESSION_REVOKED`,
  `PERMISSION_DENIED`, `ORIGIN_NOT_ALLOWED`, `MFA_REQUIRED`, `REAUTH_REQUIRED`, `NOT_FOUND`, `ALREADY_EXISTS`, `CONFLICT`,
  `PRECONDITION_FAILED`, `PRECONDITION_REQUIRED`, `IDEMPOTENCY_KEY_REUSED`, `IDEMPOTENCY_IN_PROGRESS`,
  `RATE_LIMITED`, `INTERNAL`, `SERVICE_UNAVAILABLE`, `ANALYTICS_UNAVAILABLE`, `TIMEOUT`, `INVALID_CREDENTIALS`,
  `INVALID_CURSOR`, `INVALID_FILTER`, `INVALID_SORT_FIELD`, `TIME_RANGE_TOO_LARGE`.
  De dominio, con prefijo de entidad: `ROUTER_NOT_FOUND`, `SITE_NOT_EMPTY`, `PEER_ALREADY_REVOKED`,
  `WIREGUARD_IP_POOL_EXHAUSTED`. Catálogo en la spec OpenAPI (`components/schemas/ErrorCode`, enum abierto).

### 1.5 Paginación — cursor (keyset) por defecto

**Recomendación: cursor opaco keyset en todas las colecciones; sin offset en v1.** Offset duplica/salta filas en
colecciones que cambian mientras se paginan (alertas, auditoría) y degrada en PostgreSQL con offsets grandes; UUIDv7 es
ordenable por tiempo y hace el keyset trivial con `(clave_orden, id)`.

```
GET /api/v1/routers?limit=50&status=online,degraded&sort=-last_observed_at
```

```json
{
  "data": [ { "id": "0192...", "name": "rt-core-01", "observed_state": "online" } ],
  "page": { "next_cursor": "eyJrIjpbIjIwMjYtMTAtMDdUMTQ6MDM6MTEuMTIzWiIsIjAxOTIuLi4iXX0", "prev_cursor": null, "has_more": true, "limit": 50 }
}
```

- `limit`: por defecto 50, máximo 200. Fuera de rango → `400`.
- Cursor opaco (base64url de JSON con HMAC) que codifica filtros + orden + última clave; si el cliente cambia filtros u
  orden, descarta el cursor. Cursor ajeno → `400 INVALID_CURSOR`.
- `?include_total=true` → `page.total` sólo en colecciones administrativas pequeñas (users, roles, sites, routers);
  ignorado en colecciones grandes. Ver C-03.
- Recurso individual: objeto **sin envoltorio**. Colección: `{ "data": [...], "page": {...} }`. Analítica:
  `{ "data": ..., "meta": {...} }` (§1.7).

### 1.6 Filtros, búsqueda y ordenamiento

- Igualdad: `?site_id=...&observed_state=online`. Multivalor por comas (OR en el campo, AND entre campos):
  `?observed_state=offline,degraded`.
- Rangos con sufijos `_gte`, `_lte`, `_gt`, `_lt`: `?created_at_gte=2026-10-01T00:00:00Z`.
- Tags: `?tag=core,edge` (todas) · `?tag_any=core,edge` (alguna).
- Búsqueda libre: `?q=` (prefijo/trigram sobre campos documentados: nombre, IP, descripción).
- Orden: `?sort=name`, `?sort=-created_at,name`. Desempate implícito por `id`.
- **Lista blanca por endpoint** en OpenAPI; campo no permitido → `400 INVALID_FILTER` / `INVALID_SORT_FIELD`.
- Sin DSL (`filter[x][in]`, RSQL) en v1.

### 1.7 Consultas analíticas y rangos de tiempo

Aplica a series SNMP (MVP) y luego a tráfico/analytics.

```
GET /api/v1/analytics/routers/{id}/metrics?metrics=cpu_percent,memory_percent&from=2026-10-07T00:00:00Z&to=2026-10-07T12:00:00Z&step=300
GET /api/v1/analytics/routers/{id}/metrics?metrics=cpu_percent&range=24h
```

| Parámetro | Regla |
| --- | --- |
| `from`, `to` | RFC 3339 UTC; intervalo **semiabierto** `[from, to)`; `to` por defecto = ahora. |
| `range` | Atajo relativo, excluyente con `from`: `15m, 1h, 6h, 24h, 7d, 30d, 90d`. El servidor lo resuelve a absolutos y los devuelve en `meta`. |
| `step` | Segundos por bucket. Omitido → el servidor elige para ≤ ~500 puntos/serie. Mínimo = resolución de la fuente (intervalo de sondeo, 60 s por defecto). |
| `tz` | IANA, sólo para alinear buckets diarios/mensuales; los timestamps devueltos siguen en UTC. |
| `agg` | `avg` (defecto), `max`, `min`, `sum`, `last`, `p95` según métrica. |
| Límites | ≤ 2.000 puntos por serie; rango máximo según la tabla/resolución disponible ([`storage.md`](./storage.md)). Violación → `422 TIME_RANGE_TOO_LARGE`. |

```json
{
  "data": { "series": [ { "metric": "cpu_percent", "unit": "percent",
                          "points": [["2026-10-07T00:00:00Z", 12.5], ["2026-10-07T00:05:00Z", null]] } ] },
  "meta": { "from": "2026-10-07T00:00:00Z", "to": "2026-10-07T12:00:00Z", "step": 300, "agg": "avg",
            "source": "snmp", "partial": false, "coverage": 0.98 }
}
```

- Huecos como `null` (nunca ceros ni interpolación en servidor): coherente con "huecos, no ceros" de
  [`architecture.md`](./architecture.md) §10.4.
- `meta.partial = true` si parte del rango no está disponible (retención, backend degradado); `coverage` fracción con datos.
- Timeout de consultas analíticas: 30 s → `504 TIMEOUT`.

### 1.8 Idempotencia — `Idempotency-Key`

- Header `Idempotency-Key: <UUID generado por el cliente>` (draft IETF `httpapi-idempotency-key-header`).
- **Obligatorio** (`428` si falta) en acciones con efecto externo o no repetible: crear peer WireGuard, `rotate-key`,
  `revoke`, generar reporte, notificación de prueba, `poll`. **Recomendado** en el resto de `POST`. `PATCH`/`PUT`/`DELETE`
  ya son condicionales por `If-Match`.
- **Implementación genérica en el gateway** (middleware sobre el proxy, Redis):
  `idem:{session_user}:{method}:{route}:{key}` → `{request_hash, status, headers, body}`, TTL 24 h.
  - Primera vez: reserva `in_progress` (SET NX, TTL 60 s) y hace proxy.
  - Repetición con mismo hash y respuesta guardada → misma respuesta + `Idempotency-Replayed: true`.
  - En vuelo → `409 IDEMPOTENCY_IN_PROGRESS` + `Retry-After: 1`. Mismo key con otro cuerpo → `422 IDEMPOTENCY_KEY_REUSED`.
  - Sólo se guardan `2xx` y `4xx` deterministas (no `5xx`, `429`).
  - Si Redis no está: el gateway reenvía la cabecera y el servicio aplica su defensa (unicidad natural / clave en BD).
- El gateway reenvía `Idempotency-Key` al servicio; éste puede usarla como clave natural (p. ej. columna única en la
  creación del peer) — segunda línea de defensa.

### 1.9 Concurrencia optimista — `ETag` / `If-Match`

- Todo recurso mutable tiene `version integer` (columna estándar de [`database.md`](./database.md)), expuesta como
  `ETag: "7"` y en el cuerpo `"version": 7`.
- `PATCH`, `DELETE` y `PUT` sobre recursos versionados **requieren** `If-Match` → falta `428`, no coincide `412`
  (con `current`).
- `GET` admite `If-None-Match` → `304`.
- `version` viaja también en los eventos (`aggregate_version`, [`events.md`](./events.md) §5) para que el frontend
  descarte eventos WebSocket viejos.

### 1.10 Rate limiting

Implementado en el gateway (Redis GCRA; si Redis cae, limitador en memoria por réplica — [`architecture.md`](./architecture.md) §7.1).
Valores alineados con [`security.md`](./security.md):

| Ámbito | Límite inicial |
| --- | --- |
| `POST /auth/login`, `/auth/mfa/verify`, `/auth/password/*` | 5/min por cuenta + 20/min por IP; bloqueo progresivo según `security.md`. |
| API autenticada | 600 req/min por sesión (`sid`; ráfaga 100). |
| Analítica (`/analytics/*`) | 60 req/min por sesión. |
| Exportaciones / reportes | 10/h por usuario. |
| WebSocket | §4.9. |

Headers (draft IETF `ratelimit-headers`) en rutas limitadas: `RateLimit-Policy: "default";q=600;w=60` y
`RateLimit: "default";r=412;t=23`; en `429` además `Retry-After`.

### 1.11 Autenticación HTTP, CSRF, CORS y cabeceras

Modelo de tokens decidido en [`security.md`](./security.md) §5.1 (S4/S5); aquí su efecto en el contrato:

- **Access token**: JWT EdDSA de 10 min emitido por `auth`, guardado **sólo en memoria** de la SPA y enviado como
  `Authorization: Bearer <jwt>` en cada petición. `401 TOKEN_EXPIRED` ⇒ el composable `useApi()` hace un único refresh y
  reintenta (*single-flight*, para no disparar refresh concurrentes).
- **Refresh token**: opaco, rotativo, en cookie `__Secure-hf_rt` (`HttpOnly; Secure; SameSite=Strict; Path=/api/v1/auth`).
  Sólo lo usan `POST /auth/refresh` y `POST /auth/logout`. Al recargar la página la SPA llama a `/auth/refresh`.
- **CSRF**: la API no se autentica con cookies ⇒ no aplica, salvo en `/auth/refresh` y `/auth/logout`, que exigen
  `SameSite=Strict` + cabecera `X-Requested-With: horus` + `Origin` permitido (fallo → `403 ORIGIN_NOT_ALLOWED`).
- Integraciones no navegador: `Authorization: Bearer hf_pat_...`; el gateway lo canjea por un access JWT de corta vida
  antes de reenviar.
- El gateway valida firma/`exp`/`aud` y revocación (`sid`) y **reenvía el mismo JWT** al servicio
  (`Authorization: Bearer`); el servicio lo revalida (JWKS) y aplica permiso fino + alcance.
- **CORS: mismo origen** (Traefik sirve SPA y `/api` en el mismo host) ⇒ CORS deshabilitado. Si algún día cambian los
  dominios, lista blanca explícita (ver C-01).
- Correlación: el gateway acepta o genera `X-Request-Id` (UUIDv7) y `traceparent`; los reenvía y devuelve `X-Request-Id`.
- Respuestas con secretos (configuración WireGuard con clave privada, credenciales reveladas) llevan
  `Cache-Control: no-store`. Por defecto toda la API responde `Cache-Control: no-store` salvo endpoints marcados.

### 1.12 OpenAPI como fuente de verdad — spec-first

**Decisión: spec-first, OpenAPI 3.1, una spec por servicio + un bundle público único.**

Por qué spec-first:

- Dos lenguajes consumen el contrato (Go en servicios, TS en Nuxt); con la spec primero, frontend y backend trabajan en
  paralelo desde el planning con mocks.
- Las herramientas code-first para Chi (swag) generan specs pobres (sin `oneOf`, problem+json, enums abiertos) y se
  desincronizan.
- El cambio de contrato se revisa en el PR como diff de YAML con detección automática de rupturas.

Por qué una spec por servicio: cada servicio expone su REST ([ADR-0013](./adr/0013-api-gateway-propio.md)); su spec vive
con su dominio y se valida en su CI, pero el frontend necesita **una** API.

```
packages/schemas/openapi/
├── common/                    # componentes compartidos: Problem, ErrorCode, PageInfo, parámetros
│                              # (limit, cursor, sort, from/to/range/step), headers (ETag, RateLimit, Idempotency-Key)
├── auth.yaml  devices.yaml  wireguard.yaml  snmp.yaml  analytics.yaml  ...   # una por servicio
├── gateway-routes.yaml        # tabla ruta → servicio → permiso grueso (la consume el gateway; ver §3.1)
└── dist/horus-api.v1.yaml     # bundle generado (redocly join/bundle); no se edita
```

| Paso | Herramienta |
| --- | --- |
| Lint (snake_case, `operationId`, todo error referencia `Problem`, acciones declaran `Idempotency-Key`, paginación estándar, cada operación declara `x-permission`) | Redocly CLI o Spectral con reglas propias |
| Rupturas contra `main` | `oasdiff breaking` en CI (falla el PR) |
| Consistencia gateway | Test de CI: toda operación del bundle tiene entrada en `gateway-routes.yaml` con permiso o `public: true` (deny by default, [`security.md`](./security.md)) |
| Servidor Go por servicio | `oapi-codegen` (`chi-server` + `strict-server`) → `services/<svc>/internal/http/gen/` |
| Validación runtime (dev/test) | middleware `kin-openapi` request/response en tests de integración |
| Cliente TS | `openapi-typescript` (sólo tipos) + `openapi-fetch` desde el bundle → `apps/frontend/app/types/api.gen.ts`, envuelto en el composable `useApi()` (añade `Authorization`, `X-Request-Id`, `traceparent`; refresh transparente ante `TOKEN_EXPIRED`; mapea `problem+json` a error tipado por `code`) |
| Mocks | Prism desde el bundle (`docker compose --profile mock`) |
| Docs | Scalar/Redoc en `/api/docs` sólo fuera de producción |

Código generado **commiteado**; CI verifica `generate && git diff --exit-code`.

---

## 2. Endpoints iniciales del MVP

MVP técnico: `api-gateway`, `auth`, `devices`, `wireguard` (+agent), `snmp` ([ADR-0014](./adr/0014-granularidad-de-microservicios-en-el-mvp.md)).
Rutas bajo `/api/v1`. Permisos del catálogo de [`security.md`](./security.md) §6.1. "Servicio" = destino en la tabla del
gateway. 🔒 = requiere re-autenticación reciente (`REAUTH_REQUIRED`, `security.md` §5.1).

### 2.1 Sesión y cuenta propia (`auth`)

| Método y ruta | Descripción | Permiso |
| --- | --- | --- |
| `POST /auth/login` | Usuario + contraseña. Sin 2FA → `200 {"access_token", "expires_at"}` + cookie de refresh. Con 2FA → `200 {"mfa_required": true, "mfa_token": "..."}` (5 min, un uso). | público, rate limited |
| `POST /auth/mfa/verify` | `mfa_token` + código TOTP (o de recuperación) → access token + cookie de refresh. | público, rate limited |
| `POST /auth/refresh` | Rota el refresh (cookie) → nuevo access token. Reutilización ⇒ revoca la sesión. Requiere `X-Requested-With: horus`. | cookie de refresh |
| `POST /auth/reauth` | Contraseña (+TOTP) para marcar `auth_time` reciente. | autenticado |
| `POST /auth/logout` | Revoca la sesión actual y borra la cookie. Requiere `X-Requested-With: horus`. | autenticado |
| `POST /auth/password/forgot` · `POST /auth/password/reset` | Recuperación (Sprint 2). | público, rate limited |
| `GET /me` | Usuario, roles, **permisos efectivos con alcance**. | autenticado |
| `POST /ws/tickets` | Ticket de un uso (30 s) para abrir el WebSocket (§4.2). | autenticado |
| `PATCH /me` | Nombre, idioma, zona horaria. | autenticado |
| `POST /me/password` | Cambio de contraseña (revoca otras sesiones). | autenticado |
| `POST /me/totp/enroll` · `POST /me/totp/confirm` · `DELETE /me/totp` 🔒 | Gestión TOTP. | autenticado |
| `GET /me/sessions` · `DELETE /me/sessions/{session_id}` | Mis sesiones. | autenticado |

### 2.2 Usuarios, roles, sesiones, auditoría (`auth`)

| Método y ruta | Descripción | Permiso |
| --- | --- | --- |
| `GET /users` · `GET /users/{id}` | Lista (filtros `status`, `role_id`, `q`) / detalle. | `users.read` |
| `POST /users` · `PATCH /users/{id}` · `DELETE /users/{id}` | Alta / edición / baja lógica. | `users.manage` |
| `POST /users/{id}/disable` · `/enable` | Bloquear (revoca sesiones y API tokens) / desbloquear. | `users.manage` |
| `GET /users/{id}/role-assignments` | Asignaciones `(rol, alcance)`. | `roles.read` |
| `PUT /users/{id}/role-assignments` 🔒 | Reemplaza asignaciones `[{"role_id", "scope": "global" \| "site:<id>" \| "router_group:<id>"}]`. | `roles.assign` |
| `GET /roles` · `GET /roles/{id}` | Roles y permisos. | `roles.read` |
| `POST /roles` · `PATCH /roles/{id}` · `DELETE /roles/{id}` | Roles personalizados (los de sistema no se borran). | `roles.manage` |
| `GET /permissions` | Catálogo de permisos. | `roles.read` |
| `GET /sessions` · `DELETE /sessions/{id}` | Sesiones de cualquier usuario (`?user_id=`). | `sessions.read` / `sessions.manage` |
| `GET /audit-events` | Auditoría (filtros `actor_id`, `action`, `resource_type`, `resource_id`, `outcome`, `occurred_at_gte/lte`). | `audit.read` |
| `POST /audit-events/exports` | Exportación (asíncrona, `202`). | `audit.export` |

### 2.3 Inventario (`devices`)

| Método y ruta | Descripción | Permiso |
| --- | --- | --- |
| `GET /sites` · `GET /sites/{id}` | Sitios (filtros `q`, `tag`, `parent_id`). | `sites.read` |
| `POST /sites` · `PATCH /sites/{id}` · `DELETE /sites/{id}` | Gestión (con routers → `409 SITE_NOT_EMPTY`). | `sites.create` / `sites.update` / `sites.delete` |
| `GET /routers` | Lista (filtros `site_id`, `observed_state`, `vendor_id`, `tag`, `group_id`, `q`; orden `name`, `observed_state`, `last_observed_at`). Incluye el estado observado proyectado (`router_status_cache`). | `devices.read` |
| `GET /routers/state-summary` | Conteo por estado observado (`online/warning/critical/degraded/offline/stale/unknown`) y `maintenance`, opcional `site_id`. | `devices.read` |
| `POST /routers` · `GET /routers/{id}` · `PATCH /routers/{id}` · `DELETE /routers/{id}` | Registrar / detalle / editar / baja. | `devices.create` / `devices.read` / `devices.update` / `devices.delete` |
| `PUT /routers/{id}/snmp-credentials` 🔒 | SNMP v2c/v3 **write-only**; respuesta sin secretos (`configured`, `snmp_version`, `updated_at`). | `devices.credentials.write` |
| `POST /routers/{id}/snmp-credentials/reveal` 🔒 | Revela en claro (auditado, 2FA). | `devices.credentials.reveal` |
| `POST /routers/{id}/maintenance` · `DELETE /routers/{id}/maintenance` | Ventana de mantenimiento. | `devices.update` |
| `GET /routers/{id}/interfaces` | Interfaces (filtros `oper_status`, `flow_role`, `is_monitored`). | `devices.read` |
| `GET /interfaces/{id}` · `PATCH /interfaces/{id}` | Detalle; editar `alias`, `is_monitored`, `flow_role`. | `devices.read` / `devices.update` |
| `GET /routers/{id}/discovered-interfaces` · `POST /routers/{id}/discovered-interfaces/accept` | Interfaces sugeridas por SNMP y su aceptación al inventario. | `devices.read` / `devices.update` |

### 2.4 WireGuard (`wireguard`)

| Método y ruta | Descripción | Permiso |
| --- | --- | --- |
| `GET /wireguard/servers` · `GET /wireguard/servers/{id}` | Servidores/hubs (endpoint, puerto, pool, estado del agente). | `wireguard.read` |
| `POST /wireguard/servers` · `PATCH /wireguard/servers/{id}` · `DELETE /wireguard/servers/{id}` | Gestión (con peers activos → `409`). | `wireguard.write` |
| `GET /wireguard/pools` · `POST /wireguard/pools` · `PATCH /wireguard/pools/{id}` | Pools IPAM de túnel. | `wireguard.read` / `wireguard.write` |
| `GET /wireguard/peers` | Peers (filtros `server_id`, `status`, `router_id`, `handshake_state`, `q`; orden `last_handshake_at`). | `wireguard.read` |
| `POST /wireguard/peers` 🔒 | Crear peer: IP del pool, `allowed_ips`, `router_id` opcional; `public_key` propia **o** generación en servidor (ver C-08). **`Idempotency-Key` obligatorio.** | `wireguard.write` |
| `GET /wireguard/peers/{id}` · `PATCH /wireguard/peers/{id}` | Detalle (incluye `last_handshake_at`, `rx_bytes`, `tx_bytes`, `endpoint`, `handshake_state`) / editar `allowed_ips`, `description`, `persistent_keepalive_seconds`. | `wireguard.read` / `wireguard.write` |
| `POST /wireguard/peers/{id}/rotate-key` 🔒 | Rotación de claves. `Idempotency-Key` obligatorio. | `wireguard.keys.rotate` |
| `POST /wireguard/peers/{id}/revoke` | Revocación inmediata (el registro permanece). `Idempotency-Key` obligatorio. | `wireguard.write` |
| `DELETE /wireguard/peers/{id}` | Borrado de un peer ya revocado. | `wireguard.write` |
| `GET /wireguard/peers/{id}/config` 🔒 | Configuración del lado del router (`text/plain`, `no-store`). | `wireguard.write` |

### 2.5 Métricas SNMP de un router

| Método y ruta | Servicio | Descripción | Permiso |
| --- | --- | --- | --- |
| `GET /routers/{id}/metrics/live` | `snmp` | Último valor: estado observado + razón, `last_poll_at`, `uptime_seconds`, `cpu_percent`, `memory_percent`, `temperature_celsius`, firmware, y por interfaz `oper_status`, `rx_bps`, `tx_bps`, errores, drops. | `snmp.read` |
| `POST /routers/{id}/poll` | `snmp` | Sondeo inmediato (`202`; el resultado llega por WebSocket). `Idempotency-Key` obligatorio. | `devices.update` |
| `GET /analytics/routers/{id}/metrics` | `analytics` (**`snmp` hasta el Sprint 9**) | Series de dispositivo con rango (§1.7): `cpu_percent`, `memory_percent`, `temperature_celsius`, `uptime_seconds`, `poll_latency_ms`, `availability_ratio`. | `snmp.read` |
| `GET /analytics/routers/{id}/interfaces/metrics` | ídem | Series por interfaz (`interface_id=a,b`): `rx_bps`, `tx_bps`, `rx_errors`, `tx_errors`, `rx_drops`, `tx_drops`, `oper_status`. | `snmp.read` |

> Las series históricas las sirve `analytics` ([`services.md`](./services.md)), que nace en el Sprint 9. Para que el MVP
> (Sprint 5) tenga gráficas sin cambiar el contrato, la tabla del gateway enruta `/analytics/routers/*` a `snmp` hasta
> entonces; el frontend no nota el cambio. Ver C-17.

---

## 3. El gateway como proxy: reglas transversales

### 3.1 Tabla de rutas

`packages/schemas/openapi/gateway-routes.yaml` (declarativa, versionada; ilustrativo):

```yaml
- prefix: /api/v1/routers/{id}/metrics/live
  service: snmp
  methods: { GET: snmp.read }
- prefix: /api/v1/routers
  service: devices
  methods: { GET: devices.read, POST: devices.create, PATCH: devices.update, DELETE: devices.delete }
- prefix: /api/v1/auth/login
  service: auth
  public: true
  rate_limit: login
```

- Coincidencia por prefijo más específico. Permiso **grueso**: "¿tiene X en algún alcance?" ([`security.md`](./security.md) §6.4).
- Timeouts por ruta: 5 s CRUD, 15 s acciones, 30 s analítica. Circuit breaker por servicio.
- El gateway **no** agrega respuestas de varios servicios (sin BFF de composición en v1).
- Operaciones largas: `202 Accepted` + `Location` al recurso de ejecución (`/reports/{id}`, `/audit-events/exports/{id}`)
  con `status: pending|running|succeeded|failed`; el cambio de estado también llega por WebSocket.
- `/readyz` del gateway no falla por la caída de un servicio no crítico (`snmp`, `analytics`): sigue sirviendo login,
  inventario y WireGuard (Sprint 14).

### 3.2 Cabeceras que el gateway envía al servicio

| Cabecera | Contenido |
| --- | --- |
| `Authorization: Bearer <access JWT>` | El mismo JWT del cliente, ya validado (identidad, `sid`, permisos con alcance; [`security.md`](./security.md) §5.1) |
| `X-Request-Id` | ID de correlación |
| `traceparent`, `tracestate` | W3C Trace Context |
| `Idempotency-Key`, `If-Match`, `If-None-Match` | Tal cual |
| `X-Forwarded-For`, `X-Forwarded-Proto` | Normalizados por el gateway (IP real para auditoría) |

El gateway **elimina** `Cookie` y cualquier `X-Horus-*` entrante; los tickets WebSocket nunca se reenvían.

---

## 4. WebSocket de tiempo real

### 4.1 Endpoint único y garantías

- `GET wss://<host>/api/v1/ws` en el `api-gateway`; una conexión por pestaña.
- Subprotocolo `Sec-WebSocket-Protocol: horus.ws.v1` (versiona el protocolo sin cambiar la ruta). Mensajes de texto JSON,
  `snake_case`. `permessage-deflate` habilitado.
- **Best effort** ([`architecture.md`](./architecture.md) §7.2): el WebSocket no garantiza entrega. Tras reconectar el
  cliente **resincroniza por REST** (patrón *snapshot + deltas*). No hay replay de eventos perdidos en v1.

### 4.2 Autenticación y vida de la conexión — ticket de un uso

Los navegadores no permiten cabeceras arbitrarias en el handshake WebSocket y el access token vive en memoria (no en
cookie). Decisión alineada con [`security.md`](./security.md) §5.1: **ticket de un solo uso**.

1. La SPA llama `POST /api/v1/ws/tickets` con su access token → `{"ticket": "<256 bits base64url>", "expires_at": "..."}`.
   El gateway guarda en Redis `ws_ticket:<sha256(ticket)> → {user_id, sid, exp_del_token}` con TTL 30 s.
2. Abre `wss://<host>/api/v1/ws?ticket=<ticket>` con subprotocolo `horus.ws.v1`. El gateway hace `GETDEL` (un uso), valida
   `Origin` contra la lista permitida (defensa contra *cross-site WebSocket hijacking*) y acepta. El ticket en la URL es
   inútil tras el primer uso y caduca en 30 s; aun así Traefik y el gateway **redactan** el parámetro `ticket` en logs
   ([`observability.md`](./observability.md)). Ningún token de larga vida viaja en la URL.
3. **Renovación en banda**: la conexión hereda la expiración del access token. Antes de que expire, el cliente envía
   `{"type": "auth", "access_token": "<nuevo>"}`; el gateway lo valida, comprueba que el `sid` coincide y extiende. El
   gateway avisa con `auth_expiring` 60 s antes; si vence sin renovar → cierre `4401`.
4. Si Redis no está disponible no se pueden emitir tickets: `POST /ws/tickets` → `503` y el frontend usa *polling* REST.
5. Integraciones no navegador: `Authorization: Bearer hf_pat_...` en el upgrade (sin ticket).
6. **Revocación**: con `horus.auth.session.revoked` / `horus.auth.user.disabled` ([`events.md`](./events.md)) el gateway
   cierra con `4409` las conexiones de ese `sid`/usuario en < 5 s.
7. **Cambio de permisos**: el gateway aplica los permisos del siguiente token renovado (≤ 10 min) y, si
   `horus.auth.role.updated` / `horus.auth.user.role_assignments_changed` quita permisos, cancela de inmediato las
   suscripciones afectadas (`unsubscribed`, `reason: "forbidden"`).

### 4.3 Topics

Los topics son nombres lógicos del protocolo, **desacoplados de los subjects NATS** (el gateway mapea; el frontend nunca ve
subjects internos).

| Topic | Contenido | Clase | Permiso (+ alcance) | Subjects NATS de origen |
| --- | --- | --- | --- | --- |
| `routers.status` | Cambios de estado observado de routers visibles | evento | `devices.read` | `horus.snmp.router.state_changed.*` |
| `routers` | Altas/bajas/cambios de inventario de routers y sitios | evento | `devices.read` | `horus.devices.router.*.*`, `horus.devices.site.*.*` |
| `router.<id>` | Todo lo del router: inventario, interfaces, estado, `rebooted`, `interfaces_discovered`, `oper_status_changed` | evento | `devices.read` en el alcance del router | `horus.devices.*.*.<id>`, `horus.snmp.*.*.<id>` (+ interfaces filtradas por `router_id`) |
| `router.<id>.metrics` | Cada sondeo (dispositivo + interfaces) | estado | `snmp.read` en el alcance | `horus.telemetry.snmp.*.<id>` |
| `wireguard.peers` | Peers creados/revocados/rotados, `handshake_stale/recovered` | evento | `wireguard.read` | `horus.wireguard.peer.*.*` |
| `wireguard.server.<id>.status` | Handshakes/contadores en vivo del hub | estado | `wireguard.read` | `horus.telemetry.wireguard.peer_status.<id>` |
| `alerts` | `alert.opened/acknowledged/resolved` (Sprint 11) | evento | `alerts.read` | `horus.alerts.alert.*.*` |
| `me` | Notificaciones al usuario, reportes listos, exportaciones listas, aviso de cierre de sesión | evento | autenticado (filtro por `user_id`) | `horus.alerts.notification.*.*`, `horus.reporting.report.*.*`, `horus.auth.session.revoked.*` |
| `system` | Estado del tiempo real (`realtime_status`) y del pipeline SNMP (heartbeat ausente) | estado | autenticado | `horus.snmp.poller.heartbeat.*` |

- **Evento**: cada mensaje cuenta; se entrega en orden de llegada; puede perderse ante desconexión.
- **Estado**: sólo importa el último valor por clave; al suscribirse se envía el último snapshot conocido; luego como
  máximo **1 actualización/s por topic y cliente** (coalescencia, [`architecture.md`](./architecture.md) §9.3).
- Permiso al suscribirse **y** filtro por alcance por mensaje en topics colectivos (`routers.status`): un operador con
  alcance `site:X` sólo recibe routers de X. El evento trae `site_id` para filtrar sin consultas.

### 4.4 Mensajes

Cliente → servidor:

```json
{ "type": "subscribe",   "id": "c-17", "topic": "router.0192f0c4-7a1e-7c3a-9b1d-2f6e8a4c1d55.metrics" }
{ "type": "unsubscribe", "id": "c-18", "topic": "routers.status" }
{ "type": "auth",        "id": "c-20", "access_token": "eyJ..." }
{ "type": "ping",        "id": "c-19" }
```

Servidor → cliente:

```json
{ "type": "ack",   "id": "c-17", "topic": "router.0192...metrics" }
{ "type": "error", "id": "c-17", "code": "PERMISSION_DENIED", "message": "falta snmp.read en el alcance del router" }
{ "type": "event", "topic": "routers.status",
  "event": { "id": "0192f0d1-1b2c-7e44-8a10-6b9c2d1e0f33", "type": "horus.snmp.router.state_changed",
             "time": "2026-10-07T14:03:11.123Z", "subject": "0192f0c4-...", "aggregate_version": 42,
             "data": { "router_id": "0192f0c4-...", "site_id": "0192e111-...", "previous_state": "online",
                       "state": "offline", "reason": "tunnel_down" } } }
{ "type": "state", "topic": "router.0192...metrics", "key": "device", "time": "2026-10-07T14:03:00Z",
  "data": { "cpu_percent": 13.2, "memory_percent": 61.0, "uptime_seconds": 1209600, "temperature_celsius": 47 } }
{ "type": "unsubscribed", "topic": "alerts", "reason": "forbidden" }
{ "type": "realtime_status", "status": "degraded", "reason": "event_bus_unavailable" }
{ "type": "heartbeat", "time": "2026-10-07T14:03:25Z" }
{ "type": "pong", "id": "c-19" }
{ "type": "auth_expiring", "expires_at": "2026-10-07T14:10:00Z" }
```

- `event.event` es una **proyección pública** del sobre NATS ([`events.md`](./events.md) §5): `id`, `type`, `time`,
  `subject`, `aggregate_version`, `data`. Se eliminan `actor` (salvo `actor.type`/`actor.id` si el topic lo requiere),
  `trace_parent`, `tenant_id`, `correlation_id`. `data` mantiene el esquema del evento (los tipos TS se generan del mismo
  Protobuf, `packages/protobuf/gen/ts`).
- El frontend deduplica por `event.id` y descarta si `aggregate_version` ≤ la versión local del recurso
  ([`conventions.md`](./conventions.md)).
- Tamaño máximo servidor → cliente: 64 KiB; payloads mayores se referencian por ID y se piden por REST.
- `realtime_status: degraded` cuando el gateway pierde NATS: el frontend pasa a *polling* REST cada 30 s hasta recibir
  `realtime_status: ok` ([`architecture.md`](./architecture.md) §10.3).

### 4.5 Heartbeats

- Servidor → cliente: frames `ping` de protocolo cada 20 s; sin `pong` en 10 s → cierre (detecta clientes muertos; el
  navegador responde solo).
- Para que el cliente detecte un servidor muerto: `{"type":"heartbeat"}` cada 25 s si no hubo otro tráfico; sin nada en
  60 s el cliente reconecta. `ping` de aplicación opcional para medir latencia.

### 4.6 Reconexión

- Backoff exponencial con jitter completo: 1 s, 2 s, 4 s… máx. 30 s. Cierres `1001`/`1012` (reinicio o despliegue del
  gateway) → reconectar con jitter 0–5 s.
- Al reconectar: re-suscribir **primero** y luego recargar por REST lo visible; aplicar eventos sólo si
  `aggregate_version` > versión local (evita la carrera snapshot/evento sin cursores).
- Sin sticky sessions: cualquier réplica del gateway sirve cualquier conexión.
- Evolución (no v1): reanudación con cursor usando la secuencia JetStream si la resincronización REST resulta cara
  (ver C-18).

### 4.7 Backpressure

- Cola de salida por conexión acotada: 256 mensajes o 1 MiB; escritura con deadline 10 s.
- Topics **estado**: coalescencia por `(topic, key)` + tope 1/s.
- Topics **evento**: si la cola se llena, se vacía la del topic y se envía `{"type":"resync_required","topic":...}`
  (el cliente recarga por REST); si ocurre 3 veces en 5 min o vence el deadline de escritura → cierre `4408`.
- El hilo de lectura de NATS nunca se bloquea por un cliente lento (hub con envío no bloqueante).

### 4.8 Fan-out desde NATS core

```
NATS core ─ suscripciones estáticas (dominio, bajo volumen) ─┐
          ─ suscripciones dinámicas a horus.telemetry.* ─────┤ (refcount: sólo mientras haya clientes)
                                                             ▼
                                     Hub en memoria: topic → {conexiones}
                                       ├─ filtro permiso + alcance por mensaje
                                       ├─ último valor por clave (topics estado)
                                       └─ cola acotada por conexión → goroutine escritora
```

- El gateway usa **suscripciones core** (sin consumidores JetStream): los mensajes publicados en subjects capturados por
  un stream también se entregan a suscriptores core. Sin estado en NATS, sin acks, coste mínimo.
- Suscripciones estáticas: `horus.snmp.router.>`, `horus.snmp.interface.>`, `horus.snmp.poller.heartbeat.>`,
  `horus.devices.>`, `horus.wireguard.>`, `horus.auth.session.>`, `horus.auth.user.>`, `horus.auth.role.>`,
  `horus.alerts.>`, `horus.reporting.>`. Dinámicas: `horus.telemetry.snmp.*.<router_id>`,
  `horus.telemetry.wireguard.peer_status.<server_id>`.
- El último snapshot de topics estado se cachea en memoria; si no hay valor caliente, el gateway lo pide al servicio por
  REST interno (`GET /routers/{id}/metrics/live`).
- Cada réplica se suscribe de forma independiente ⇒ escalado horizontal sin estado compartido (salvo Redis de sesiones).
- Permiso NATS del usuario del gateway: sólo **subscribe** a esos subjects; ningún publish ([`security.md`](./security.md)).

### 4.9 Límites por conexión

| Límite | Valor |
| --- | --- |
| Conexiones por sesión | 5 ([`security.md`](./security.md)); la 6ª cierra la más antigua con `4429` |
| Suscripciones por conexión | 50 |
| Topics `router.<id>.metrics` por conexión | 20 |
| Mensaje entrante máximo | 16 KiB |
| Mensajes entrantes | 20/s (ráfaga 50) → `4429` |
| Tiempo hasta el primer `subscribe` | 30 s |
| Vida máxima | 12 h (cierre `1001` para rebalancear) |
| Objetivo por réplica | 10.000 conexiones (v1 espera ~50; validar en Sprint 15) |

Cierres propios: `4400` mensaje inválido · `4401` ticket inválido o token vencido sin renovar · `4403` `Origin` no permitido ·
`4408` cliente lento · `4409` sesión revocada/usuario deshabilitado · `4429` límite. Estándar: `1001`, `1011`, `1012`.

---

## 5. gRPC interno

### 5.1 Organización de `packages/protobuf`

```
packages/protobuf/
├── buf.yaml                 # módulo, reglas lint/breaking
├── buf.gen.yaml             # protoc-gen-go, protoc-gen-go-grpc; protoc-gen-es (TS de payloads de eventos)
├── horus/
│   ├── common/v1/           # PageRequest/PageInfo, Actor, TimeRange, IpAddress
│   ├── auth/v1/             # SessionService (CheckSession), UserService (GetUsers)
│   ├── devices/v1/          # InventoryService (ListPollingTargets, GetPollingTarget, GetRouters, ListCustomerAddressMap)
│   ├── wireguard/v1/        # WireGuardControl (ReportStatus), WireGuardAgent (ApplyDesiredState)
│   ├── snmp/v1/             # PollerService (PollNow, GetRouterState)
│   ├── flows/v1/  traffic/v1/  detection/v1/  alerts/v1/  analytics/v1/
│   └── events/<dominio>/v1/ # payloads de eventos (events.md §5.6)
└── gen/
    ├── go/                  # Go generado (commiteado), módulo propio
    └── ts/                  # TS de payloads expuestos por WebSocket
```

- Paquete `horus.<servicio>.v<N>` ([ADR-0005](./adr/0005-grpc-protobuf-interno.md)); eventos en `horus.events.<dominio>.v<N>`.
- `go_package` por *managed mode* de buf (no a mano).
- Estilo orientado a recursos (Google AIP): `Get*`, `List*` (`page_size`, `page_token`, `filter`, `order_by`),
  `Update*` con `google.protobuf.FieldMask update_mask` + `int64 expected_version`; acciones como RPC propios.
- Tipos: IDs `string` (UUID); `google.protobuf.Timestamp`/`Duration`; IPs `string` en control y `bytes` (4/16) en
  telemetría; enums con `<ENUM>_UNSPECIFIED = 0` y prefijo en cada valor.
- Regla de uso ([ADR-0005](./adr/0005-grpc-protobuf-interno.md)): gRPC sólo si el llamador necesita la respuesta para
  continuar; máximo 2 saltos síncronos; para notificar hechos, NATS.

```protobuf
// ilustrativo
syntax = "proto3";
package horus.devices.v1;

import "google/protobuf/timestamp.proto";

service InventoryService {
  // Snapshot paginado de objetivos de sondeo con credenciales descifradas.
  // Sólo identidades de servicio autorizadas (svc:snmp), sobre mTLS.
  rpc ListPollingTargets(ListPollingTargetsRequest) returns (ListPollingTargetsResponse);
  rpc GetRouters(GetRoutersRequest) returns (GetRoutersResponse); // batch de nombres
}

message PollingTarget {
  string router_id = 1;
  string management_ip = 2;
  int64 version = 3;
  google.protobuf.Duration poll_interval = 4;
  SnmpCredentials credentials = 5; // nunca aparece en eventos ni en logs
  reserved 6; reserved "community";
}
```

### 5.2 Versionado y compatibilidad (buf)

- `buf lint` (`STANDARD`, excepciones documentadas en `buf.yaml`).
- `buf breaking --against '.git#branch=main'` en CI: categoría `FILE` para `horus/<servicio>/**`, **`WIRE_JSON`** para
  `horus/events/**` (los eventos de dominio viajan en JSON: renombrar un campo rompe aunque el número no cambie).
- Dentro de una versión: sólo añadir campos/RPCs/valores; nunca reutilizar números (`reserved` número **y** nombre); no
  cambiar tipos ni cardinalidad; no mover campos dentro/fuera de `oneof`.
- Ruptura ⇒ paquete `v2` conviviendo con `v1` (el servidor implementa ambos durante la migración; `v1` con
  `option deprecated = true`).
- `buf generate` → `gen/`, commiteado; CI verifica diff vacío.

### 5.3 Deadlines y reintentos

- **Toda llamada lleva deadline.** Por defecto 2 s ([ADR-0005](./adr/0005-grpc-protobuf-interno.md)); snapshots
  paginados (`ListPollingTargets`) 10 s por página. Un servidor que recibe una llamada sin deadline aplica 2 s y emite
  `grpc_missing_deadline_total`.
- El deadline se **propaga** (contexto Go) y nunca se amplía aguas abajo; el REST del servicio deriva su contexto del
  timeout de la ruta del gateway.
- Reintentos sólo por *service config* de grpc-go, en métodos idempotentes (`Get*`, `List*`, `CheckSession`), ante
  `UNAVAILABLE` (y `RESOURCE_EXHAUSTED` con `RetryInfo`), máx. 3 intentos, backoff 100 ms→1 s. Sin hedging.
- `wireguard` ↔ `wireguard-agent`: `ApplyDesiredState` es idempotente (estado completo + versión), por lo que se reintenta
  libremente; el agente es *fail-static*.

### 5.4 Propagación de contexto (metadata)

| Metadata | Contenido |
| --- | --- |
| `authorization` | `Bearer <access JWT del usuario>` si se actúa en su nombre; si no, identidad del certificado mTLS (+ JWT de servicio `sub=svc:<nombre>` si hace falta) ([`security.md`](./security.md) §6.5) |
| `traceparent`, `tracestate` | W3C (interceptores `otelgrpc`) |
| `x-request-id` | Correlación; termina también en `correlation_id` de los eventos |
| `x-horus-caller` | Nombre del servicio llamante (informativo; la identidad real es el certificado mTLS + JWT) |

Interceptor común (`packages/go/authz`, [`conventions.md`](./conventions.md)) que valida el JWT, extrae un `Actor`
(`type`, `id`, `sid`) al `context.Context` y lo reutilizan logs, auditoría y el sobre de eventos.

### 5.5 Seguridad del transporte

**mTLS en gRPC interno desde el Sprint 1** (decisión S5 de [`security.md`](./security.md)), además del JWT por llamada.
Obligatorio sin excepción para `wireguard-agent` (privilegiado) y para RPCs que transportan secretos
(`ListPollingTargets`). Configuración `GRPC_TLS_MODE=mtls` por defecto; `disabled` sólo en tests locales.

### 5.6 Errores: `google.rpc.Status`

- Códigos gRPC estándar + detalles:
  - `google.rpc.ErrorInfo{reason: "ROUTER_NOT_FOUND", domain: "devices.horus"}` — **obligatorio** en errores de negocio;
    `reason` = `code` del problem+json.
  - `google.rpc.BadRequest{field_violations}` → `errors[]` de 422.
  - `google.rpc.PreconditionFailure` (`type: "VERSION"`) → 412; otro tipo → 409.
  - `google.rpc.RetryInfo` → `Retry-After`.
- Cuando un servicio expone por REST un fallo de otra llamada gRPC, mapea así:

| gRPC | HTTP | `code` por defecto |
| --- | --- | --- |
| `INVALID_ARGUMENT` | 422 | `VALIDATION_FAILED` |
| `UNAUTHENTICATED` | 401 | `UNAUTHENTICATED` |
| `PERMISSION_DENIED` | 403 | `PERMISSION_DENIED` |
| `NOT_FOUND` | 404 | `NOT_FOUND` |
| `ALREADY_EXISTS` | 409 | `ALREADY_EXISTS` |
| `ABORTED` | 409 | `CONFLICT` |
| `FAILED_PRECONDITION` | 412 / 409 | `PRECONDITION_FAILED` / `CONFLICT` |
| `RESOURCE_EXHAUSTED` | 429 | `RATE_LIMITED` |
| `UNAVAILABLE` | 503 | `SERVICE_UNAVAILABLE` |
| `DEADLINE_EXCEEDED` | 504 | `TIMEOUT` |
| `INTERNAL`, `UNKNOWN`, `DATA_LOSS`, `UNIMPLEMENTED` | 500 | `INTERNAL` |

- `status.message` es para logs, nunca para el usuario.

### 5.7 Otros

- `grpc.health.v1` en todos los servicios; reflexión sólo en desarrollo.
- Mensaje máximo 4 MiB; respuestas grandes paginadas o en *server streaming*.
- Keepalive cliente 30 s; `MaxConnectionAge` servidor 30 min.
- Descubrimiento por DNS de Compose (`devices:9090`); en Kubernetes, Services headless.
