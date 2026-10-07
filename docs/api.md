# Convenciones de API — Horus Flow

> Estado: **propuesta Sprint 0** · Dueño: Agente 3 (contratos e integración) · Fuente: [`vision.md`](./vision.md)
>
> Documentos relacionados: [`events.md`](./events.md) (contrato NATS), [`architecture.md`](./architecture.md) y
> [`services.md`](./services.md) (Agente 1), [`database.md`](./database.md) (Agente 2),
> [`security.md`](./security.md) y [`conventions.md`](./conventions.md) (Agente 4),
> [`open-questions/contracts.md`](./open-questions/contracts.md).

Este documento fija tres contratos:

1. **REST público** que expone el `api-gateway` al frontend Nuxt (§1–§3).
2. **WebSocket** de tiempo real, también en el `api-gateway` (§4).
3. **gRPC interno** entre el `api-gateway` y los servicios, y entre servicios (§5).

Principio rector: el frontend sólo conoce el gateway (`Frontend → HTTPS/WSS → api-gateway → servicios`).
Ningún servicio interno se expone fuera de la red de Docker.

---

## 1. REST público

### 1.1 Base, versionado y formato

| Tema | Decisión |
| --- | --- |
| Prefijo | `/api/v1/...`. La versión **mayor** va en la ruta; sólo cambia ante cambios incompatibles. |
| Compatibilidad dentro de `v1` | Se permite **agregar** campos de respuesta, endpoints, parámetros opcionales y valores de enum documentados como "abiertos". Prohibido: quitar/renombrar campos, cambiar tipos, volver obligatorio un parámetro, cambiar semántica. El cliente debe ignorar campos desconocidos. |
| Deprecación | Header `Deprecation` (RFC 9745) + `Sunset` (RFC 8594) + `Link: <...>; rel="deprecation"`. Mínimo un release menor de convivencia. |
| Formato | `application/json; charset=utf-8`. Claves en `snake_case`. |
| IDs | UUIDv7 en texto canónico (`0192f0c4-7a1e-7c3a-9b1d-2f6e8a4c1d55`). Nunca IDs autoincrementales expuestos. |
| Tiempos | UTC, RFC 3339 con `Z` y milisegundos: `2026-10-07T14:03:11.123Z`. Campos con sufijo `_at` (instantes) o `_until`/`_from`. |
| Duraciones | Enteros con unidad en el nombre: `poll_interval_seconds`, `uptime_seconds`. Nada de `"5m"` en JSON. |
| Tráfico | Bytes y paquetes como enteros (`bytes`, `packets`); tasas como `*_bps` / `*_pps` (números). Contadores que pueden pasar de 2^53 se serializan como **string** (`"rx_octets": "18446744073709551615"`) — documentado por campo en OpenAPI. |
| Nulos | Un campo opcional sin valor se envía como `null` (no se omite) en respuestas, para que el tipo TS sea estable. En `PATCH`, `null` significa "borrar el valor". |
| Enums | Strings en `snake_case` minúsculas: `"online"`, `"offline"`, `"warning"`, `"critical"`, `"unknown"`, `"maintenance"`. |
| Booleanos | Prefijo `is_`/`has_` sólo cuando ayude a la lectura (`is_enabled`, `has_totp`). |

Rutas fuera de `/api/v1` (no versionadas, no públicas a internet salvo `/healthz`): `/healthz` (liveness), `/readyz`
(readiness), `/metrics` (Prometheus, sólo red interna).

### 1.2 Recursos y nombres

- Recursos en **plural, kebab-case** en la ruta: `/sites`, `/routers`, `/wireguard/servers`, `/audit-events`.
- Anidamiento máximo de **un nivel** y sólo cuando el hijo no tiene sentido sin el padre o para listar por padre:
  `GET /routers/{router_id}/interfaces`. El recurso hijo también es direccionable de forma plana:
  `GET /interfaces/{interface_id}`.
- **Acciones** que no encajan en CRUD: `POST /{recurso}/{id}/{verbo-kebab}` — p. ej.
  `POST /wireguard/peers/{id}/rotate-key`, `POST /users/{id}/disable`. Siempre `POST`, siempre idempotentes con
  `Idempotency-Key` (§1.8).
- Singletons del usuario actual bajo `/me`: `GET /me`, `GET /me/sessions`.
- Sub-recursos de sólo lectura derivados (métricas, estado): `GET /routers/{id}/metrics`, `GET /routers/{id}/status`.

### 1.3 Métodos y códigos HTTP

| Método | Uso | Éxito |
| --- | --- | --- |
| `GET` | Leer recurso o colección. Seguro e idempotente. | `200` |
| `POST` (colección) | Crear. Devuelve el recurso creado + `Location` + `ETag`. | `201` |
| `POST` (acción) | Acción síncrona → `200` con resultado; acción asíncrona (reportes, sondeo forzado) → `202` con `Location` al recurso/operación. | `200` / `202` |
| `PATCH` | Actualización parcial con **JSON Merge Patch** (RFC 7396). Acepta `application/merge-patch+json` y, por comodidad, `application/json` con la misma semántica. Requiere `If-Match`. | `200` (devuelve el recurso) |
| `PUT` | Sólo para reemplazo completo de colecciones de asociación (`PUT /users/{id}/roles`) y secretos write-only (`PUT /routers/{id}/snmp-credentials`). Requiere `If-Match` cuando el recurso padre tiene versión. | `200` / `204` |
| `DELETE` | Borrado (lógico o físico según [`database.md`](./database.md)). Idempotente: borrar algo ya borrado → `404`. Requiere `If-Match`. | `204` |

Códigos de error usados (y **sólo** estos, para que el frontend tenga un mapa cerrado):

| Código | Cuándo |
| --- | --- |
| `400` | JSON mal formado, parámetro de query inválido (tipo), cursor corrupto. |
| `401` | Sin token, token expirado o inválido. Header `WWW-Authenticate: Bearer error="invalid_token"`. |
| `403` | Autenticado sin permiso (`recurso.accion`) o fuera de su ACL. |
| `404` | No existe **o** el usuario no puede saber que existe (ACL ocultando recursos). |
| `405` | Método no soportado (con `Allow`). |
| `409` | Conflicto de estado/unicidad (`name` duplicado, peer ya revocado, `Idempotency-Key` en vuelo). |
| `412` | `If-Match` no coincide (concurrencia optimista). |
| `413` | Cuerpo mayor que el límite (1 MiB por defecto). |
| `415` | `Content-Type` no soportado. |
| `422` | Validación semántica de campos (con lista `errors[]`). |
| `428` | Falta `If-Match` en una mutación que lo exige, o falta `Idempotency-Key` donde es obligatorio. |
| `429` | Rate limit (con `Retry-After`). |
| `500` | Error no controlado (siempre con `trace_id`). |
| `502` / `503` / `504` | Servicio interno caído / degradado / timeout (p. ej. ClickHouse caído → los endpoints analíticos responden `503` con `code: "ANALYTICS_UNAVAILABLE"` mientras el inventario sigue funcionando). |

### 1.4 Formato de error — RFC 9457 `application/problem+json`

Todas las respuestas de error (4xx/5xx) usan Problem Details con extensiones fijas:

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

Reglas:

- **`code` es el contrato** (UPPER_SNAKE_CASE, estable, en inglés). `title`/`detail` son texto humano (español) y pueden
  cambiar; el frontend traduce por `code`, nunca por `detail`.
- `code` es **idéntico** al `ErrorInfo.reason` de gRPC (§5.6); el gateway no inventa códigos, los propaga.
- `type` es una URL de documentación derivada del `code` (`/errors/<code-kebab>`); no tiene que resolverse en v1.
- `errors[].field` usa notación JSON Pointer simplificada con puntos para anidados: `allowed_ips.0`.
- `500` nunca filtra mensajes internos, stack traces ni SQL: `detail` genérico + `trace_id`.
- Catálogo inicial de `code` transversales: `VALIDATION_FAILED`, `UNAUTHENTICATED`, `TOKEN_EXPIRED`,
  `PERMISSION_DENIED`, `NOT_FOUND`, `ALREADY_EXISTS`, `CONFLICT`, `PRECONDITION_FAILED` (ETag),
  `PRECONDITION_REQUIRED`, `IDEMPOTENCY_KEY_REUSED`, `IDEMPOTENCY_IN_PROGRESS`, `RATE_LIMITED`, `INTERNAL`,
  `SERVICE_UNAVAILABLE`, `ANALYTICS_UNAVAILABLE`, `TIMEOUT`, `MFA_REQUIRED`, `INVALID_CREDENTIALS`.
  Cada servicio añade códigos de dominio con prefijo de entidad: `ROUTER_NOT_FOUND`, `PEER_ALREADY_REVOKED`,
  `WIREGUARD_IP_POOL_EXHAUSTED`. El registro vive en la spec OpenAPI (`components/schemas/ErrorCode`, enum abierto).

### 1.5 Paginación — **cursor (keyset) por defecto**

**Recomendación: cursor opaco basado en keyset para todas las colecciones; sin offset en v1.**

Justificación: las colecciones grandes (auditoría, alertas, peers, interfaces de 1.000 routers) cambian mientras se
paginan; offset produce duplicados/saltos y degrada en PostgreSQL (`OFFSET 50000`). Los IDs UUIDv7 son ordenables por
tiempo, lo que hace el keyset trivial con `(sort_key, id)`. Las tablas de Nuxt UI funcionan bien con "cargar más" /
"siguiente / anterior". Si el producto exige "ir a la página 37", ver la pregunta abierta C-03.

```
GET /api/v1/routers?limit=50&status=online,warning&sort=-last_seen_at
```

```json
{
  "data": [ { "id": "0192...", "name": "rt-core-01", "status": "online" } ],
  "page": {
    "next_cursor": "eyJrIjpbIjIwMjYtMTAtMDdUMTQ6MDM6MTEuMTIzWiIsIjAxOTIuLi4iXSwiZCI6Im4ifQ",
    "prev_cursor": null,
    "has_more": true,
    "limit": 50
  }
}
```

- `limit`: por defecto 50, máximo 200 (500 en endpoints de exportación interna). Fuera de rango → `400`.
- El cursor es **opaco** (base64url de JSON firmado con HMAC para evitar manipulación) y codifica filtros + orden;
  si el cliente cambia filtros u orden debe descartar el cursor. Cursor de otra consulta → `400 INVALID_CURSOR`.
- `total` **no** se devuelve por defecto. Con `?include_total=true` se devuelve `page.total` (count exacto) sólo en
  colecciones administrativas pequeñas (users, roles, sites, routers ≤ ~10⁴ filas). En colecciones grandes se ignora.
- Respuesta de recurso individual: el objeto **sin envoltorio** (`{ "id": ..., ... }`). Colecciones: siempre
  `{ "data": [...], "page": {...} }`. Endpoints analíticos: `{ "data": ..., "meta": {...} }` (§1.7).

### 1.6 Filtros, búsqueda y ordenamiento

- **Filtros por igualdad** como parámetros con el nombre del campo: `?site_id=...&status=online`.
- **Multivalor** separado por comas (OR dentro del campo, AND entre campos): `?status=online,warning`.
- **Rangos** con sufijos `_gte`, `_lte`, `_gt`, `_lt`: `?created_at_gte=2026-10-01T00:00:00Z`.
- **Tags**: `?tag=core,edge` (el recurso tiene **todas**) — `?tag_any=core,edge` (alguna).
- **Búsqueda libre**: `?q=texto` (prefijo/ILIKE sobre campos documentados por endpoint: nombre, IP, descripción).
- **Orden**: `?sort=campo` asc, `?sort=-campo` desc, múltiples separados por coma: `?sort=-status,name`. El servidor
  agrega `id` como desempate implícito. Campos ordenables/filtrables: **lista blanca por endpoint** en OpenAPI; un campo
  no permitido → `400 INVALID_SORT_FIELD` / `INVALID_FILTER`.
- Nada de DSL tipo `filter[status][in]=...` ni RSQL en v1: más simple de validar y generar.

### 1.7 Consultas analíticas y rangos de tiempo

Aplica a métricas SNMP (MVP) y después a tráfico/analytics (ClickHouse).

```
GET /api/v1/routers/{id}/metrics?metrics=cpu_percent,memory_percent&from=2026-10-07T00:00:00Z&to=2026-10-07T12:00:00Z&step=300
GET /api/v1/routers/{id}/metrics?metrics=cpu_percent&range=24h
```

| Parámetro | Regla |
| --- | --- |
| `from`, `to` | RFC 3339 UTC. Intervalo **semiabierto** `[from, to)`. `to` por defecto = ahora. |
| `range` | Atajo relativo exclusivo con `from`: `15m, 1h, 6h, 24h, 7d, 30d`. El servidor resuelve a `from/to` absolutos. |
| `step` | Segundos por bucket. Si se omite, el servidor elige para devolver ≤ ~500 puntos por serie. Mínimo = resolución de la fuente (intervalo de sondeo SNMP; 60 s por defecto). |
| `tz` | IANA (`America/Mexico_City`) sólo para alinear buckets diarios/mensuales; los timestamps devueltos siguen en UTC. |
| `agg` | `avg` (por defecto), `max`, `min`, `sum`, `last`, `p95` según la métrica. |
| Límites | `(to - from) / step ≤ 2.000` puntos por serie; rango máximo por resolución (p. ej. datos crudos ≤ 7 d, agregados horarios ≤ 1 año). Violación → `422 TIME_RANGE_TOO_LARGE`. |

Respuesta (formato orientado a ECharts, series columnar):

```json
{
  "data": {
    "series": [
      { "metric": "cpu_percent", "unit": "percent", "points": [["2026-10-07T00:00:00Z", 12.5], ["2026-10-07T00:05:00Z", null]] }
    ]
  },
  "meta": {
    "from": "2026-10-07T00:00:00Z",
    "to": "2026-10-07T12:00:00Z",
    "step": 300,
    "agg": "avg",
    "source": "snmp",
    "partial": false
  }
}
```

- Huecos de datos se devuelven como `null` (el router no respondió), nunca interpolados en el servidor.
- `meta.partial = true` cuando parte del rango no está disponible (p. ej. retención expirada o backend degradado).
- Consultas analíticas pesadas tienen timeout de 30 s en el gateway; si una consulta excede, `504 TIMEOUT`.

### 1.8 Idempotencia — `Idempotency-Key`

- Header `Idempotency-Key: <UUIDv7 o UUIDv4 generado por el cliente>` (draft IETF `httpapi-idempotency-key-header`).
- **Obligatorio** (→ `428` si falta) en: creación de peers WireGuard, `rotate-key`, `revoke`, generación de reportes,
  envío de notificaciones de prueba, y cualquier acción con efecto externo. **Recomendado** en el resto de `POST`.
  `PUT`, `PATCH` y `DELETE` ya son idempotentes/condicionales por `If-Match`.
- Implementación en el gateway con Redis: clave `idem:{user_id}:{method}:{route}:{key}` → `{request_hash, status, headers, body}`, TTL 24 h.
  - Primera vez: se reserva la clave con estado `in_progress` (SET NX, TTL 60 s) y se ejecuta.
  - Repetición con mismo hash y respuesta guardada → se devuelve la misma respuesta + `Idempotency-Replayed: true`.
  - Repetición mientras está en vuelo → `409 IDEMPOTENCY_IN_PROGRESS` + `Retry-After: 1`.
  - Misma clave con cuerpo distinto → `422 IDEMPOTENCY_KEY_REUSED`.
  - Sólo se guardan respuestas `2xx` y `4xx` deterministas (no `5xx`, no `429`), para permitir reintentos.
- El gateway propaga la clave en metadata gRPC (`x-idempotency-key`) para que el servicio la use como
  `Nats-Msg-Id`/clave natural si lo necesita.

### 1.9 Concurrencia optimista — `ETag` / `If-Match`

- Todo recurso mutable tiene un entero `version` (columna en PostgreSQL, incrementada en cada `UPDATE`; ver
  [`database.md`](./database.md)). Se expone como `ETag: "v7"` (fuerte) y también en el cuerpo como `"version": 7`.
- `PATCH`, `PUT` (sobre recursos versionados) y `DELETE` **requieren** `If-Match: "v7"` → si falta `428`; si no
  coincide `412 PRECONDITION_FAILED` con el recurso actual en `current` (extensión del problem) para que la UI
  muestre el diff.
- `GET` admite `If-None-Match` → `304` (útil para polling barato del dashboard).
- La versión viaja a gRPC (`etag`/`version` en el request, AIP-154) y en los eventos (`aggregate_version`), lo que
  permite al frontend descartar eventos WebSocket antiguos (§4.6).

### 1.10 Rate limiting

- Algoritmo token bucket (GCRA) en Redis, en el gateway. Claves por usuario autenticado y, para endpoints anónimos,
  por IP.
- Límites iniciales (configurables):

| Ámbito | Límite |
| --- | --- |
| `POST /auth/login`, `/auth/mfa/verify`, `/auth/password/*` | 5/min por IP+usuario y 20/min por IP; backoff exponencial tras fallos (lo define [`security.md`](./security.md)). |
| API autenticada general | 600 req/min por usuario (ráfaga 100). |
| Endpoints analíticos (`/metrics`, futuros `/traffic/*`) | 60 req/min por usuario. |
| Exportaciones / reportes | 10/hora por usuario. |
| WebSocket | ver §4.9. |

- Headers en **todas** las respuestas de rutas limitadas (draft IETF `ratelimit-headers`):
  `RateLimit-Policy: "default";q=600;w=60` y `RateLimit: "default";r=412;t=23`. En `429`: además `Retry-After: <s>`.
- Si Redis no está disponible el limitador **falla abierto** para la API general y **cerrado** para login
  (degradación segura; ver [`security.md`](./security.md)).

### 1.11 CORS, cabeceras y autenticación HTTP

- **Recomendación: mismo origen.** El reverse proxy (Caddy/Traefik) sirve Nuxt en `/` y el gateway en `/api/` bajo el
  mismo host → CORS no se habilita. Si se despliega el frontend en otro dominio, CORS con lista blanca explícita de
  orígenes (sin `*`), `Access-Control-Allow-Credentials: true`, métodos `GET, POST, PATCH, PUT, DELETE`, headers
  `Authorization, Content-Type, Idempotency-Key, If-Match, If-None-Match, X-Request-Id, traceparent`, expuestos
  `ETag, Location, RateLimit, RateLimit-Policy, Retry-After, X-Request-Id, Idempotency-Replayed`, `max-age` 600.
- **Autenticación** (detalle en [`security.md`](./security.md); aquí sólo el contrato HTTP): access token JWT corto
  (≈10 min) en `Authorization: Bearer`, mantenido en memoria por el frontend; refresh token rotativo en cookie
  `HttpOnly; Secure; SameSite=Strict; Path=/api/v1/auth`. `POST /auth/refresh` exige además el header
  `X-Requested-With: horus` como defensa CSRF.
- Cabeceras de correlación: el gateway acepta `X-Request-Id` (o genera un UUIDv7) y `traceparent` (W3C); devuelve
  `X-Request-Id` siempre.
- Respuestas con secretos (configuración WireGuard, claves privadas mostradas una vez) llevan
  `Cache-Control: no-store`. Por defecto la API responde `Cache-Control: no-store` salvo endpoints marcados.

### 1.12 OpenAPI como fuente de verdad — **spec-first**

**Decisión: spec-first con OpenAPI 3.1.**

Justificación:

- El contrato lo consumen dos lenguajes (Go en el gateway, TypeScript en Nuxt); escribir la spec primero permite que
  frontend y backend trabajen en paralelo desde el sprint de planning con mocks.
- Chi no tiene anotaciones/reflexión fuertes; las herramientas code-first en Go (swag) producen specs pobres
  (sin `oneOf`, problem+json, enums abiertos) y desincronizadas.
- Revisión de cambios de contrato en PR como diff de YAML, con detección automática de breaking changes.

Organización y herramientas:

```
packages/schemas/openapi/
├── openapi.yaml            # raíz: info, servers, security, tags, $ref a paths
├── paths/                  # un archivo por recurso (routers.yaml, wireguard-peers.yaml, ...)
├── components/             # schemas, parameters (limit, cursor, sort...), responses (Problem), headers
└── dist/openapi.bundled.yaml  # generado (redocly bundle), no se edita
```

| Paso | Herramienta |
| --- | --- |
| Lint de estilo (snake_case, `operationId` en camelCase, todo error referencia `Problem`, todo `POST` de acción declara `Idempotency-Key`, paginación estándar) | Redocly CLI o Spectral con reglas propias en `packages/schemas/openapi/.spectral.yaml` |
| Detección de breaking changes contra `main` | `oasdiff breaking` en CI (falla el PR si rompe `v1`) |
| Servidor Go (tipos + interfaz de handlers para Chi) | `oapi-codegen` con `strict-server` + `chi-server` → `services/api-gateway/internal/http/gen/` |
| Validación en runtime (dev/test) | middleware `kin-openapi` que valida request/response contra la spec en tests de integración |
| Cliente TypeScript | `openapi-typescript` (tipos, cero runtime) + `openapi-fetch`, envuelto en un composable `useApi()` de Nuxt que inyecta token, `X-Request-Id`, `traceparent` y mapea `problem+json` a un error tipado por `code`. Tipos generados en `apps/frontend/app/types/api.gen.ts` (o `packages/schemas/ts/` si se comparte). |
| Mocks para el frontend | Prism (`stoplight/prism`) levantado desde la spec en `docker compose --profile mock`. |
| Documentación | Redoc/Scalar servido en `/api/docs` sólo en entornos no productivos. |

El código generado se **commitea** y CI verifica `generate && git diff --exit-code` (builds reproducibles sin
toolchain extra en cada Dockerfile).

---

## 2. Endpoints iniciales del MVP

MVP técnico: `Nuxt → api-gateway → {auth, devices, wireguard} → NATS → snmp → PostgreSQL`. Todas las rutas bajo
`/api/v1`. "Servicio" indica el servicio gRPC que atiende detrás del gateway. Permisos con la convención
`recurso.accion` de [`vision.md` §7](./vision.md#7-seguridad); los marcados con **(nuevo)** no están en la lista base y
deben ratificarse con [`security.md`](./security.md) (ver C-07).

### 2.1 Autenticación y sesión (servicio `auth`)

| Método y ruta | Descripción | Permiso |
| --- | --- | --- |
| `POST /auth/login` | Usuario + contraseña. Si tiene TOTP devuelve `200 {"mfa_required": true, "mfa_token": "..."}` (token de 5 min, un uso). Si no, emite tokens. | público (rate limited) |
| `POST /auth/mfa/verify` | `mfa_token` + código TOTP → emite access token + cookie refresh. | público (rate limited) |
| `POST /auth/refresh` | Rota refresh token (cookie) → nuevo access token. Reuso de refresh revocado ⇒ revoca la familia de sesión. | cookie de refresh |
| `POST /auth/logout` | Revoca la sesión actual. | autenticado |
| `POST /auth/password/forgot` · `POST /auth/password/reset` | Recuperación (Sprint 2). | público (rate limited) |
| `GET /me` | Usuario actual + roles + **permisos efectivos** (el frontend los usa para ocultar UI; el backend los vuelve a verificar). | autenticado |
| `PATCH /me` | Nombre, idioma, zona horaria. | autenticado |
| `POST /me/password` | Cambio de contraseña (requiere la actual). | autenticado |
| `POST /me/totp/enroll` · `POST /me/totp/confirm` · `DELETE /me/totp` | Gestión TOTP. | autenticado |
| `GET /me/sessions` · `DELETE /me/sessions/{session_id}` | Ver/revocar mis sesiones. | autenticado |
| `POST /realtime/tickets` | Ticket de un uso para abrir el WebSocket (§4.2). | autenticado |

### 2.2 Usuarios, roles, permisos y auditoría (servicio `auth`)

| Método y ruta | Descripción | Permiso |
| --- | --- | --- |
| `GET /users` | Lista (filtros `status`, `role_id`, `q`). | `users.read` (nuevo) |
| `POST /users` | Crear usuario (invitación o contraseña inicial). | `users.manage` (nuevo) |
| `GET /users/{id}` | Detalle. | `users.read` |
| `PATCH /users/{id}` | Editar. `If-Match`. | `users.manage` |
| `DELETE /users/{id}` | Baja lógica. `If-Match`. | `users.manage` |
| `POST /users/{id}/disable` · `/enable` | Bloquear/desbloquear (revoca sesiones al deshabilitar). | `users.manage` |
| `PUT /users/{id}/roles` | Reemplaza el conjunto de roles `{"role_ids": [...]}`. | `roles.manage` (nuevo) |
| `GET /users/{id}/sessions` · `DELETE /users/{id}/sessions/{session_id}` | Sesiones de otro usuario. | `users.manage` |
| `GET /roles` · `GET /roles/{id}` | Roles con sus permisos. | `roles.read` (nuevo) |
| `POST /roles` · `PATCH /roles/{id}` · `DELETE /roles/{id}` | Gestión de roles (roles de sistema no borrables). | `roles.manage` |
| `GET /permissions` | Catálogo de permisos disponibles (estático, versionado). | `roles.read` |
| `GET /audit-events` | Auditoría (filtros `actor_id`, `action`, `resource_type`, `resource_id`, `created_at_gte/lte`). | `audit.read` (nuevo) |

### 2.3 Inventario: sitios, routers, interfaces (servicio `devices`)

| Método y ruta | Descripción | Permiso |
| --- | --- | --- |
| `GET /sites` · `GET /sites/{id}` | Sitios (filtros `q`, `tag`, `parent_id`). | `devices.read` |
| `POST /sites` · `PATCH /sites/{id}` · `DELETE /sites/{id}` | Gestión de sitios (borrar sitio con routers → `409 SITE_NOT_EMPTY`). | `devices.create` / `devices.update` / `devices.delete` |
| `GET /routers` | Lista (filtros `site_id`, `status`, `vendor`, `tag`, `q`; orden `name`, `status`, `last_seen_at`). | `devices.read` |
| `GET /routers/status-summary` | Conteo por estado para el dashboard (`online/offline/warning/critical/unknown/maintenance`), opcional `site_id`. | `devices.read` |
| `POST /routers` | Registrar router (nombre, sitio, IP de gestión, vendor, modelo, tags, perfil SNMP). | `devices.create` |
| `GET /routers/{id}` · `PATCH /routers/{id}` · `DELETE /routers/{id}` | Detalle / editar / baja. | `devices.read` / `devices.update` / `devices.delete` |
| `PUT /routers/{id}/snmp-credentials` | Credenciales SNMP v2c/v3 **write-only**; la respuesta nunca devuelve secretos (sólo `configured: true`, `version`, `updated_at`). | `devices.update` (ver C-07: ¿`credentials.manage`?) |
| `POST /routers/{id}/poll` | Fuerza un sondeo SNMP inmediato (asíncrono, `202`; resultado llega por WebSocket). | `devices.update` |
| `POST /routers/{id}/maintenance` · `DELETE /routers/{id}/maintenance` | Ventana de mantenimiento (suprime alertas). | `devices.update` |
| `GET /routers/{id}/interfaces` | Interfaces del router (descubiertas por SNMP; filtros `oper_status`, `is_monitored`). | `devices.read` |
| `GET /interfaces/{id}` · `PATCH /interfaces/{id}` | Detalle; editar sólo campos administrativos (`alias`, `is_monitored`, `role`: `uplink/client/mgmt`). | `devices.read` / `devices.update` |

### 2.4 WireGuard (servicio `wireguard`)

| Método y ruta | Descripción | Permiso |
| --- | --- | --- |
| `GET /wireguard/servers` · `GET /wireguard/servers/{id}` | Servidores (interfaz, endpoint público, puerto, pool de IPs, estado). | `wireguard.read` |
| `POST /wireguard/servers` · `PATCH /wireguard/servers/{id}` · `DELETE /wireguard/servers/{id}` | Gestión (borrar con peers activos → `409`). | `wireguard.write` |
| `GET /wireguard/servers/{id}/peers` | Peers de un servidor (filtros `status`, `router_id`, `q`; orden `last_handshake_at`). | `wireguard.read` |
| `POST /wireguard/servers/{id}/peers` | Crear peer: asigna IP del pool, genera claves (o acepta `public_key` propia), `allowed_ips`, `router_id` opcional. **Requiere `Idempotency-Key`.** Si el servidor genera la clave privada, se devuelve **una sola vez** en `private_key` con `Cache-Control: no-store`. | `wireguard.write` |
| `GET /wireguard/peers/{id}` · `PATCH /wireguard/peers/{id}` | Detalle (incluye `last_handshake_at`, `rx_bytes`, `tx_bytes`, `connection_status`); editar `allowed_ips`, `description`, `persistent_keepalive_seconds`. | `wireguard.read` / `wireguard.write` |
| `POST /wireguard/peers/{id}/rotate-key` | Rotación de claves. **Requiere `Idempotency-Key`.** | `wireguard.write` |
| `POST /wireguard/peers/{id}/revoke` | Revocación (deja de aceptarse inmediatamente; registro permanece). **Requiere `Idempotency-Key`.** | `wireguard.write` |
| `DELETE /wireguard/peers/{id}` | Borrado definitivo de un peer ya revocado. | `wireguard.write` |
| `GET /wireguard/peers/{id}/config` | Archivo de configuración del lado del peer (`text/plain`), sin clave privada si no se custodia (ver C-08). | `wireguard.write` |

### 2.5 Métricas SNMP de un router (servicio `snmp`, lectura)

| Método y ruta | Descripción | Permiso |
| --- | --- | --- |
| `GET /routers/{id}/status` | Snapshot actual: alcanzabilidad, `last_poll_at`, `uptime_seconds`, `cpu_percent`, `memory_percent`, `temperature_celsius`, firmware, último error de sondeo. | `devices.read` |
| `GET /routers/{id}/metrics` | Series de métricas de dispositivo (`cpu_percent`, `memory_percent`, `temperature_celsius`, `uptime_seconds`, `poll_latency_ms`) con rango de tiempo (§1.7). | `devices.read` (ver C-07: ¿`monitoring.read`?) |
| `GET /routers/{id}/interfaces/metrics` | Series agregadas por interfaz (`rx_bps`, `tx_bps`, `rx_errors`, `tx_errors`, `rx_drops`, `tx_drops`, `oper_status`) con `interface_id=a,b` y rango. | `devices.read` |
| `GET /interfaces/{id}/metrics` | Igual que el anterior para una interfaz. | `devices.read` |

> El backend de almacenamiento de series SNMP (PostgreSQL/ClickHouse) lo decide [`database.md`](./database.md)
> / [`storage.md`](./storage.md); el contrato REST no cambia con esa decisión.

---

## 3. Patrones transversales REST ↔ servicios

- El gateway es un **traductor delgado**: autentica, autoriza a nivel de permiso grueso (`recurso.accion`), valida contra
  la spec, aplica rate limit/idempotencia y traduce a gRPC. La autorización fina (ACL por sitio, por ejemplo) se
  vuelve a verificar en el servicio dueño (defensa en profundidad).
- Mapeo gRPC → HTTP en §5.6.
- Operaciones largas (reportes, sondeos forzados): `202 Accepted` + `Location: /api/v1/reports/{id}`; el recurso tiene
  `status: pending|running|succeeded|failed`; el cambio de estado llega también por WebSocket.
- Health: `/readyz` del gateway reporta degradación por dependencia (`auth`, `devices`, `redis`, `nats`) pero
  **no** falla la readiness si cae un servicio no crítico (p. ej. `snmp` o ClickHouse): el gateway sigue sirviendo
  login, inventario y WireGuard (requisito Sprint 14).

---

## 4. WebSocket de tiempo real

### 4.1 Endpoint único

- `GET wss://<host>/api/v1/realtime` — un solo endpoint en el `api-gateway`, una conexión por pestaña.
- Subprotocolo obligatorio `Sec-WebSocket-Protocol: horus.realtime.v1` (permite versionar el protocolo sin cambiar la
  ruta). Mensajes de texto JSON (UTF-8, `snake_case`). Compresión `permessage-deflate` habilitada.
- El canal es **server → client** para datos; el cliente sólo envía mensajes de control.

### 4.2 Autenticación en el handshake y renovación

Los navegadores no permiten headers arbitrarios en el handshake WebSocket. Opciones: token en query (queda en logs),
token en `Sec-WebSocket-Protocol` (hack), cookie (CSRF/cross-site WebSocket hijacking) o **ticket de un uso**.

**Decisión: ticket de un uso.**

1. El frontend llama `POST /api/v1/realtime/tickets` con su access token → `{"ticket": "<256 bits aleatorios>", "expires_at": "..."}`.
   El gateway guarda en Redis `ws_ticket:{ticket} → {user_id, session_id, permissions_hash}` con TTL 30 s.
2. Abre `wss://host/api/v1/realtime?ticket=...`. El gateway hace `GETDEL` (un uso), valida `Origin` contra la lista
   blanca y acepta. El ticket en la URL es inútil tras el primer uso y expira en 30 s, así que su aparición en logs no
   es explotable (y el proxy debe redactar el query param igualmente).
3. **Renovación**: la conexión hereda la expiración del access token. Antes de que expire, el cliente envía
   `{"type": "auth", "access_token": "..."}` con el token renovado; el gateway lo valida, verifica que
   `session_id` coincide y extiende. Si el token expira sin renovación → `auth_expiring` 60 s antes y cierre `4401`.
4. **Revocación**: el gateway consume `horus.auth.session.revoked` / `horus.auth.user.disabled` (ver
   [`events.md`](./events.md)) y cierra con `4409` todas las conexiones de esa sesión/usuario en ≤ 1 s.
5. **Cambio de permisos**: con `horus.auth.role.updated` / `horus.auth.user.roles_changed` el gateway recalcula los
   permisos de las conexiones afectadas y cancela las suscripciones que ya no proceden (`unsubscribed` con
   `reason: "forbidden"`).

### 4.3 Modelo de suscripción por canales

Los canales son **nombres lógicos del protocolo WebSocket**, desacoplados de los subjects NATS (el gateway mapea). Así
se puede reorganizar NATS sin romper el frontend y nunca se exponen subjects internos.

| Canal | Contenido | Clase | Permiso | Fuente NATS |
| --- | --- | --- | --- | --- |
| `routers` | Altas/bajas/cambios de routers y `status_changed` (todas las del inventario visible por ACL) | evento | `devices.read` | `horus.devices.router.*` |
| `sites` | Cambios de sitios | evento | `devices.read` | `horus.devices.site.*` |
| `router:{id}` | Cambios del router concreto + interfaces (`interface.*`, `oper_status_changed`) | evento | `devices.read` + ACL del router | `horus.devices.*`, `horus.snmp.interface.*` filtrado por `router_id` |
| `router:{id}:metrics` | Resultado de cada sondeo SNMP (dispositivo + interfaces) | estado | `devices.read` + ACL | `horus.telemetry.snmp.*.{id}` |
| `wireguard` | Peers creados/revocados/rotados, `connection_changed` | evento | `wireguard.read` | `horus.wireguard.>` |
| `wireguard:server:{id}:handshakes` | Handshakes y contadores en vivo | estado | `wireguard.read` | `horus.telemetry.wireguard.handshake.{id}` |
| `alerts` | `alert.opened/acknowledged/resolved` (Sprint 11) | evento | `security.read` (ver C-07: ¿`alerts.read`?) | `horus.alerts.alert.*` |
| `me` | Notificaciones para el usuario, reportes listos, avisos de sesión | evento | autenticado (filtrado por `user_id`) | `horus.alerts.notification.*`, `horus.reporting.report.*` |

- **Clase evento**: cada mensaje importa; soporta reanudación (§4.6).
- **Clase estado**: sólo importa el último valor por clave; al suscribirse se envía el último snapshot conocido y
  luego valores nuevos; sin replay; ante presión se **coalesce** (§4.7).
- La verificación de permiso se hace al suscribirse **y** por mensaje para canales colectivos (`routers`), filtrando
  por la ACL del usuario (en v1 la ACL por sitio puede no existir aún; el filtro queda preparado).

### 4.4 Formato de mensajes

Cliente → servidor:

```json
{ "type": "subscribe",   "id": "c-17", "channel": "router:0192f0c4-7a1e-7c3a-9b1d-2f6e8a4c1d55:metrics" }
{ "type": "subscribe",   "id": "c-18", "channel": "routers", "resume_from": "DEVICES_EVENTS:48211" }
{ "type": "unsubscribe", "id": "c-19", "channel": "routers" }
{ "type": "auth",        "id": "c-20", "access_token": "eyJ..." }
{ "type": "ping",        "id": "c-21" }
```

Servidor → cliente:

```json
{ "type": "ack",        "id": "c-17", "channel": "router:0192...:metrics" }
{ "type": "error",      "id": "c-18", "code": "PERMISSION_DENIED", "message": "falta devices.read" }
{ "type": "event",      "channel": "routers", "cursor": "DEVICES_EVENTS:48212",
  "event": {
    "id": "0192f0d1-1b2c-7e44-8a10-6b9c2d1e0f33",
    "type": "horus.devices.router.status_changed",
    "time": "2026-10-07T14:03:11.123Z",
    "subject": "0192f0c4-7a1e-7c3a-9b1d-2f6e8a4c1d55",
    "aggregate_version": 42,
    "data": { "router_id": "0192f0c4-...", "previous_status": "online", "status": "offline", "reason": "snmp_unreachable" }
  } }
{ "type": "state",      "channel": "router:0192...:metrics", "key": "device", "time": "2026-10-07T14:03:00Z",
  "data": { "cpu_percent": 13.2, "memory_percent": 61.0, "uptime_seconds": 1209600, "temperature_celsius": 47 } }
{ "type": "resync_required", "channel": "routers", "reason": "cursor_expired" }
{ "type": "unsubscribed", "channel": "alerts", "reason": "forbidden" }
{ "type": "heartbeat",  "time": "2026-10-07T14:03:25Z" }
{ "type": "pong",       "id": "c-21" }
{ "type": "auth_expiring", "expires_at": "2026-10-07T14:10:00Z" }
```

- `event.event` es una **proyección pública** del sobre NATS ([`events.md`](./events.md) §4): se eliminan `actor`
  detallado, `trace_parent`, `tenant_id` y campos internos; `data` sigue el mismo esquema (los tipos TS se generan del
  mismo Protobuf).
- Tamaño máximo de mensaje servidor → cliente: 64 KiB (los payloads grandes se referencian por ID y se piden por REST).

### 4.5 Heartbeats

- **Servidor → cliente (detección de cliente muerto)**: frames de control `ping` del protocolo cada 20 s; sin `pong`
  en 10 s → cierre. El navegador responde automáticamente.
- **Cliente → servidor (detección de servidor muerto)**: el navegador no expone pings de protocolo, así que el servidor
  envía `{"type":"heartbeat"}` cada 25 s si no hubo otro tráfico; si el cliente no recibe nada en 60 s, reconecta.
  El cliente puede enviar `ping` de aplicación para medir latencia.

### 4.6 Reconexión y reanudación

- Backoff exponencial con jitter completo: 1 s, 2 s, 4 s… máximo 30 s. Cierre `1012` (reinicio del servidor) o `1001`
  (going away) → reconectar inmediatamente con jitter 0–5 s para evitar estampida.
- Cada mensaje de un canal de clase **evento** trae `cursor = <STREAM>:<stream_sequence>` de JetStream. Como todas
  las instancias del gateway leen el mismo stream, el cursor es **válido en cualquier instancia** (no hace falta sticky
  session).
- Al reconectar el cliente envía `resume_from` por canal. El gateway:
  1. Si el cursor está en su buffer circular en memoria (últimos 10 min o 10.000 eventos por stream) → reenvía los
     eventos posteriores filtrados por permisos, luego en vivo.
  2. Si no, y el cursor tiene < 1 h, crea un consumidor **ordenado efímero** desde `stream_sequence + 1` con tope de
     5.000 mensajes.
  3. Si no es posible → `resync_required`; el cliente vuelve a pedir el estado por REST.
- Patrón recomendado en el cliente: **suscribirse primero, luego hacer el GET REST**, y aplicar eventos sólo si
  `aggregate_version > version` local (evita la carrera snapshot/evento sin coordinar cursores con REST).
- Canales de clase **estado**: no se reanudan; tras reconectar llega el último snapshot.

### 4.7 Backpressure

- Cola de salida por conexión acotada: 256 mensajes o 1 MiB. Escritura con deadline de 10 s.
- Canales **estado**: coalescencia por `(channel, key)` — si hay un valor pendiente sin enviar se reemplaza por el nuevo.
- Canales **evento**: si la cola se llena, se descartan los pendientes de ese canal y se envía `resync_required`; si
  ocurre 3 veces en 5 min o la escritura excede el deadline → cierre `4408 slow_consumer`.
- El gateway nunca bloquea el hilo de lectura de NATS por un cliente lento (hub con fan-out no bloqueante).

### 4.8 Fan-out desde NATS en el gateway

```
NATS JetStream ──(1 consumidor ordenado efímero por stream de dominio y por instancia)──┐
NATS core      ──(suscripciones dinámicas a horus.telemetry.* con refcount)─────────────┤
                                                                                         ▼
                                                      Hub en memoria: canal → {conexiones}
                                                       ├─ filtro de permisos/ACL por mensaje
                                                       ├─ buffer circular (sólo canales evento)
                                                       └─ cola acotada por conexión → writer goroutine
```

- **Eventos de dominio**: cada instancia del gateway crea un consumidor **ordenado** (efímero, sin ack, `DeliverNew`)
  por stream de dominio relevante (`DEVICES_EVENTS`, `WIREGUARD_EVENTS`, `AUTH_EVENTS`, `ALERTS_EVENTS`,
  `REPORTING_EVENTS`, `SNMP_EVENTS`). Barato, ordenado, aporta `stream_sequence` para el cursor. No usa durables:
  el gateway no "procesa" eventos, sólo los retransmite; las pérdidas se cubren con `resync_required`.
- **Telemetría**: suscripción **core NATS** (no JetStream) al subject concreto sólo mientras haya ≥ 1 cliente suscrito
  (p. ej. `horus.telemetry.snmp.*.{router_id}`); al llegar a 0 suscriptores se cancela. Los mensajes publicados a
  subjects capturados por un stream también se entregan a suscriptores core, sin coste de consumidor.
- El último snapshot de canales estado se cachea en memoria del gateway (por `router_id`), con fallback a
  `GET /routers/{id}/status` vía gRPC al servicio `snmp` cuando no hay valor caliente.
- Escala horizontal: N instancias independientes, sin estado compartido salvo Redis (tickets). Cada instancia recibe
  todos los eventos de dominio (bajo volumen) y sólo la telemetría que sus clientes piden.

### 4.9 Límites por conexión

| Límite | Valor inicial |
| --- | --- |
| Conexiones simultáneas por usuario | 10 (pestañas/dispositivos); la 11ª cierra la más antigua con `4429`. |
| Suscripciones por conexión | 50 |
| Canales `router:{id}:metrics` por conexión | 20 |
| Mensaje entrante máximo | 16 KiB |
| Tasa de mensajes entrantes | 20/s (ráfaga 50) → `4429` |
| Tiempo para enviar el primer `subscribe` | 30 s |
| Vida máxima de una conexión | 24 h (cierre `1001` para rebalancear y forzar re-autenticación completa) |
| Conexiones por instancia de gateway | 10.000 (objetivo de carga, validar en Sprint 15) |

Códigos de cierre propios: `4400` mensaje inválido, `4401` no autenticado/token expirado, `4403` origen no permitido,
`4408` cliente lento, `4409` sesión revocada/usuario deshabilitado, `4429` límite excedido. Estándar: `1001`, `1011`,
`1012`.

---

## 5. gRPC interno

### 5.1 Organización de `packages/protobuf`

```
packages/protobuf/
├── buf.yaml                 # módulo, lint, breaking
├── buf.gen.yaml             # plugins: protoc-gen-go, protoc-gen-go-grpc, (es) protoc-gen-es para TS
├── horus/
│   ├── common/v1/           # tipos compartidos: PageRequest/PageInfo, Actor, Money?, IPAddress, TimeRange, ErrorReason
│   ├── auth/v1/             # AuthService, UserService, RoleService, SessionService, TokenService (introspección/JWKS)
│   ├── devices/v1/          # SiteService, RouterService, InterfaceService, CredentialService
│   ├── wireguard/v1/        # ServerService, PeerService
│   ├── snmp/v1/             # MetricsService (consultas), PollService (sondeo forzado)
│   ├── flows/v1/  traffic/v1/  reputation/v1/  detection/v1/  alerts/v1/  analytics/v1/  reporting/v1/
│   └── events/              # payloads de eventos — ver events.md §5
│       ├── devices/v1/  auth/v1/  wireguard/v1/  snmp/v1/  flows/v1/ ...
└── gen/
    ├── go/                  # código Go generado (commiteado) — módulo Go propio
    └── ts/                  # tipos TS para payloads de eventos expuestos por WebSocket
```

- Paquete Protobuf = `horus.<dominio>.v<N>` (p. ej. `horus.devices.v1`); eventos en `horus.events.<dominio>.v<N>`.
- `go_package = "github.com/<org>/horus-flow/packages/protobuf/gen/go/horus/devices/v1;devicesv1"` (vía *managed mode*
  de buf, no a mano).
- Estilo de API orientado a recursos tipo Google AIP: `GetRouter`, `ListRouters` (`page_size`, `page_token`,
  `filter`, `order_by`), `CreateRouter`, `UpdateRouter` (con `google.protobuf.FieldMask update_mask` y `int64 version`
  para concurrencia optimista), `DeleteRouter`, acciones como RPC propios (`RotatePeerKey`, `RevokePeer`).
- Tipos: IDs `string` (UUID), instantes `google.protobuf.Timestamp`, duraciones `google.protobuf.Duration`, IPs como
  `string` canónico en APIs de control y `bytes` (16) en telemetría. Enums con `<ENUM>_UNSPECIFIED = 0` y prefijo
  del enum en cada valor (`ROUTER_STATUS_ONLINE`).
- Dirección de dependencias: los servicios **no** dependen del paquete del gateway; el gateway depende de todos.
  Dependencias entre dominios se limitan a `common` y a lecturas puntuales (`snmp` → `devices.v1.CredentialService`).

Ejemplo ilustrativo (no es un archivo del repo):

```protobuf
syntax = "proto3";
package horus.devices.v1;

import "google/protobuf/field_mask.proto";
import "google/protobuf/timestamp.proto";

service RouterService {
  rpc GetRouter(GetRouterRequest) returns (Router);
  rpc ListRouters(ListRoutersRequest) returns (ListRoutersResponse);
  rpc CreateRouter(CreateRouterRequest) returns (Router);
  rpc UpdateRouter(UpdateRouterRequest) returns (Router);
  rpc DeleteRouter(DeleteRouterRequest) returns (DeleteRouterResponse);
}

message Router {
  string id = 1;
  string site_id = 2;
  string name = 3;
  string management_ip = 4;
  RouterStatus status = 5;
  repeated string tags = 6;
  int64 version = 7;
  google.protobuf.Timestamp created_at = 8;
  google.protobuf.Timestamp updated_at = 9;
  reserved 10; reserved "snmp_community"; // los secretos nunca viajan en Router
}

message UpdateRouterRequest {
  Router router = 1;
  google.protobuf.FieldMask update_mask = 2;
  int64 expected_version = 3; // 0 = sin control (sólo llamadas internas de sistema)
}
```

### 5.2 Versionado y compatibilidad (buf)

- `buf lint` con reglas `STANDARD` (+ `COMMENTS` en servicios públicos internos) y excepciones documentadas en `buf.yaml`.
- `buf breaking --against '.git#branch=main'` en CI:
  - Paquetes RPC (`horus.<dominio>.v1`): categoría `FILE`.
  - Paquetes de eventos (`horus.events.*`): categoría `WIRE_JSON` (los eventos de dominio se serializan en JSON;
    renombrar un campo rompe aunque el número de campo no cambie).
- Reglas dentro de una versión: sólo añadir campos/RPCs/valores de enum; nunca reutilizar números (usar `reserved`
  número **y** nombre); no cambiar tipos ni cardinalidad; no mover campos dentro/fuera de `oneof`.
- Cambio incompatible ⇒ nuevo paquete `v2` conviviendo con `v1` (el servidor implementa ambos durante la migración);
  `v1` se marca `option deprecated = true`.
- Generación: `buf generate` → `packages/protobuf/gen/go`, commiteado; CI verifica diff vacío.

### 5.3 Deadlines y reintentos

- **Toda llamada lleva deadline.** Un servidor que recibe una llamada sin deadline aplica uno por defecto (10 s) y
  emite métrica `grpc_missing_deadline_total`.
- El gateway fija el presupuesto total por request HTTP: 5 s CRUD, 30 s analítica, 15 s acciones (rotate-key); las
  llamadas aguas abajo **heredan** el deadline restante (propagación nativa de `context.Context`), nunca lo amplían.
- Reintentos sólo vía *service config* de grpc-go, para métodos marcados idempotentes (`Get*`, `List*`, y mutaciones
  con `request_id`), códigos `UNAVAILABLE` (y `RESOURCE_EXHAUSTED` con `RetryInfo`), máx. 3 intentos, backoff
  100 ms→1 s. Sin hedging en v1.
- Streams de larga duración (si los hay, p. ej. `snmp` ↔ `devices` sync) usan keepalive y se re-establecen con backoff.

### 5.4 Propagación de contexto (metadata)

| Metadata | Contenido | Quién lo pone |
| --- | --- | --- |
| `traceparent`, `tracestate` | W3C Trace Context | interceptores `otelgrpc` (automático) |
| `x-request-id` | ID de la petición HTTP original (UUIDv7) | gateway; servicios lo propagan y lo registran en logs y en `correlation_id` de eventos |
| `authorization` | `Bearer <access token del usuario>` (JWT original) | gateway en llamadas en nombre de un usuario |
| `x-horus-service` | Nombre del servicio llamante | todos (interceptor) |
| `x-idempotency-key` | Clave de idempotencia del cliente | gateway (cuando aplica) |

- **Identidad**: recomendación v1 = **reenviar el JWT del usuario**. Cada servicio lo valida localmente con la JWKS de
  `auth` (cacheada) y verifica permisos de nuevo (defensa en profundidad; un servicio no confía ciegamente en un header
  `x-user-id`). Llamadas de sistema (workers, consumidores de eventos) usan un token de servicio (client credentials
  emitido por `auth`, `actor.type = "service"`). Detalles de emisión en [`security.md`](./security.md).
- Un interceptor común (en una librería `packages/` de Go, ver [`conventions.md`](./conventions.md)) extrae todo esto a
  un `Actor` en el `context.Context` que reutilizan logs, auditoría y el envelope de eventos.

### 5.5 Seguridad del transporte interno

- **Recomendación v1: sin mTLS**, tráfico gRPC en texto claro **sólo dentro de una red Docker interna** (no publicada al
  host), con autenticación a nivel de aplicación (JWT de usuario/servicio) en cada llamada. Motivos: un solo host en
  v1, menor complejidad operativa (CA, rotación de certificados) en el MVP.
- El código queda **listo para TLS/mTLS** por configuración (`GRPC_TLS_MODE=disabled|tls|mtls`) y se activa en cuanto
  haya más de un host o al migrar a Kubernetes (malla de servicios o SPIFFE/SPIRE). Ver C-10 y
  [`security.md`](./security.md).
- Excepción a considerar: las llamadas que transportan secretos (`CredentialService.GetSnmpCredentials`) deberían ir
  sobre TLS desde v1 si `security.md` lo exige.

### 5.6 Errores: `google.rpc.Status` y mapeo a HTTP

- Los servicios devuelven códigos gRPC estándar con detalles `google.rpc.Status`:
  - `google.rpc.ErrorInfo{reason: "ROUTER_NOT_FOUND", domain: "devices.horus", metadata: {...}}` — **obligatorio** en
    todo error de negocio; `reason` = `code` del problem+json.
  - `google.rpc.BadRequest{field_violations}` → `errors[]` de 422.
  - `google.rpc.PreconditionFailure` → 412 (versión) o 409 (estado).
  - `google.rpc.RetryInfo` → `Retry-After`.
- Mapeo en el gateway:

| gRPC | HTTP | `code` por defecto |
| --- | --- | --- |
| `INVALID_ARGUMENT` | 422 (400 si es de sintaxis) | `VALIDATION_FAILED` |
| `UNAUTHENTICATED` | 401 | `UNAUTHENTICATED` |
| `PERMISSION_DENIED` | 403 | `PERMISSION_DENIED` |
| `NOT_FOUND` | 404 | `NOT_FOUND` |
| `ALREADY_EXISTS` | 409 | `ALREADY_EXISTS` |
| `ABORTED` | 409 | `CONFLICT` |
| `FAILED_PRECONDITION` | 412 si `PreconditionFailure.type == "VERSION"`, si no 409 | `PRECONDITION_FAILED` / `CONFLICT` |
| `RESOURCE_EXHAUSTED` | 429 | `RATE_LIMITED` |
| `UNAVAILABLE` | 503 | `SERVICE_UNAVAILABLE` |
| `DEADLINE_EXCEEDED` | 504 | `TIMEOUT` |
| `UNIMPLEMENTED` | 501→ se expone como 404 | `NOT_FOUND` |
| `INTERNAL`, `UNKNOWN`, `DATA_LOSS` | 500 | `INTERNAL` |

- Mensajes de error gRPC (`status.message`) son para logs, no para el usuario; el gateway sólo propaga `reason` y
  violaciones de campo.

### 5.7 Otros

- Health checks `grpc.health.v1` en todos los servicios (usados por Docker healthcheck y por el gateway).
- Server reflection sólo en desarrollo.
- Tamaño máximo de mensaje 4 MiB (por defecto); respuestas grandes se paginan.
- Keepalive cliente 30 s, `MaxConnectionAge` servidor 30 min (rebalanceo futuro detrás de un LB).
- Descubrimiento: DNS de Docker Compose (`devices:9090`); puerto gRPC convencional `9090`, HTTP `8080`, métricas
  `9100` (a confirmar en [`conventions.md`](./conventions.md)).
