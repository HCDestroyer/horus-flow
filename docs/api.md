# Convenciones de API — Horus Flow

> Estado: **propuesta ronda 2** (aplica las decisiones del PO [`po-decisions.md`](./po-decisions.md) D1–D10, que
> prevalecen sobre cualquier texto anterior) · Dueño: Agente C (contratos, seguridad y operaciones) · Fuente:
> [`vision.md`](./vision.md)
>
> Las referencias a "Sprint N" que quedan en este documento se leen como "el incremento que entrega esa capacidad"
> (D9; orden en [`roadmap.md`](./roadmap.md)). Donde decía Redis se lee **Valkey** (D3; protocolo compatible).
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
- Si el Agente A adopta un despliegue más simple (p. ej. un binario modular, ADRs 0017–0029), este contrato **no
  cambia**: "servicio" pasa a ser "módulo dueño" y la tabla del gateway enruta a un handler interno en vez de a un
  proxy HTTP. Las reglas de tenant (§0), permisos y formato son idénticas.

### Cambios de la ronda 2 (resumen)

| Tema | Cambio | Decisión |
| --- | --- | --- |
| Multi-tenant | Tenant **explícito en la ruta**: `/api/v1/tenants/{tenant_id}/...`; recursos de plataforma bajo `/api/v1/platform/...` (§0) | D6 |
| Usuarios con varios ISP | El usuario es de plataforma; pertenece a tenants por **membresías** con roles por tenant; `GET /me` lista sus tenants (§0.3, §2.2) | D6 |
| Clientes | Cliente = IP descubierta. Sin altas manuales, sin asignaciones IP↔cliente. Búsqueda por IP en el **cuerpo** (`POST .../customers/lookup`), nunca en la URL; cambio manual de tipo e historial con razones (§2.9) | D1 |
| Seguridad/botnets | Hallazgos y estado de seguridad por cliente (§2.10) | D5 |
| Dashboards | CRUD de dashboards, widgets, layouts y rotaciones; datos por widget resueltos en servidor; **modo kiosco** con credencial de dispositivo de solo lectura (§2.11–§2.12) | D8 |
| WebSocket | Topics con tenant: `tenants.<tenant_id>.<topic>` (§4.3) | D6 |
| Series históricas | Las sirve siempre su dueño analítico sobre ClickHouse desde el primer incremento (desaparece el enrutado temporal a `snmp`) | D4 |
| Almacén clave-valor | Valkey | D3 |

---

## 0. Tenant en la API (D6)

### 0.1 Modelo

- **Tenant = ISP.** Jerarquía: tenant → nodo (`site`, sitio físico del ISP) → router principal (exportador de flujos,
  MikroTik en v1, D10) → IPs de clientes (D1). En la API REST el nodo se sigue llamando `sites` (la UI lo rotula
  "Nodo"); si el Agente B renombra la entidad, se añade el alias sin romper `v1`.
- **Usuario de plataforma**: un usuario existe una sola vez (email único en la plataforma) y accede a uno o varios
  tenants mediante **membresías** `(user, tenant, rol, alcance)`. Hay además **roles de plataforma**
  (`platform_admin`, `platform_operator`, `platform_auditor`; [`security.md`](./security.md) §6.2).
- Todo recurso de negocio pertenece a **exactamente un** tenant. Los IDs son UUIDv7 globalmente únicos, pero la
  pertenencia nunca se infiere del ID: se comprueba siempre contra el tenant de la ruta.

### 0.2 Decisión: tenant en la ruta

| Opción | A favor | En contra |
| --- | --- | --- |
| **A. Tenant en la ruta** `/api/v1/tenants/{tenant_id}/routers/{id}` | Explícito en cada petición, logs y trazas; el gateway hace el chequeo grueso de pertenencia sin leer el cuerpo; varias pestañas con ISP distintos a la vez; URLs compartibles y estables (pantallas NOC, enlaces de alertas); claves de caché/idempotencia/rate limit por tenant de forma natural; el repositorio filtra `WHERE tenant_id = $ruta AND id = $id` (defensa contra IDOR entre tenants) | Rutas más largas; el tenant se repite en recursos cuyo ID ya es único |
| B. Tenant implícito (cabecera `X-Horus-Tenant` o claim "tenant activo" en el JWT) + selector | Rutas cortas | Estado ambiental: un error en el frontend (cabecera de otra pestaña, token con el tenant equivocado) envía datos a la vista equivocada; el claim exige re-emitir token al cambiar de ISP y choca entre pestañas; la cabecera no aparece en enlaces ni en access logs; más difícil de probar exhaustivamente |

**Recomendación y decisión de contrato: A.** El selector de ISP de la UI sólo cambia el prefijo de ruta (y el topic
WebSocket); no hay "tenant activo" en servidor. Proponer ADR (numera el Agente A): *"Tenant explícito en rutas REST y
topics WebSocket"*.

Reglas:

1. Rutas de negocio: `/api/v1/tenants/{tenant_id}/<colección>[/{id}[/...]]`. `tenant_id` es el UUID (no el slug: el
   slug es editable). El frontend puede mostrar el slug en sus propias URLs y resolverlo con `GET /me`.
2. Rutas sin tenant: `/api/v1/auth/*`, `/api/v1/me*`, `/api/v1/ws*`, `/api/v1/kiosk/*` (§2.12), `/api/v1/system/status`,
   `/api/v1/widget-types`, `/api/v1/permissions` y todo `/api/v1/platform/*` (gestión de plataforma).
3. **Pertenencia**: si el usuario no es miembro del tenant (y no tiene acceso de soporte vigente, [`security.md`](./security.md)
   §6.6) → `404 TENANT_NOT_FOUND` (no se revela si el tenant existe). Si es miembro pero le falta el permiso →
   `403 PERMISSION_DENIED`.
4. **Recurso de otro tenant**: `GET /tenants/A/routers/{id_de_B}` → `404 ROUTER_NOT_FOUND`, nunca `403` (no confirma
   existencia). Lo garantiza el repositorio (cláusula `tenant_id`) y lo prueba la batería de aislamiento
   ([`security.md`](./security.md) §3.9, [`conventions.md`](./conventions.md) §11).
5. Referencias en el cuerpo (`site_id`, `router_id`…) deben pertenecer al mismo tenant de la ruta → `422` con
   `errors[].code = NOT_FOUND` (no se distingue "no existe" de "es de otro tenant").
6. Las respuestas incluyen `tenant_id` en cada recurso (redundante pero útil para clientes y para detectar errores).
7. Cursores, ETags de colección e `Idempotency-Key` quedan ligados al tenant: el HMAC del cursor incluye `tenant_id`
   (cursor de otro tenant → `400 INVALID_CURSOR`); la clave de idempotencia incluye la ruta, y por tanto el tenant.
8. Operaciones multi-tenant (vista NOC de un usuario con varios ISP): **no** hay endpoints que mezclen tenants salvo
   los de plataforma. El frontend compone N llamadas, una por tenant; `GET /me/tenants/summary` (§2.1) devuelve el
   resumen mínimo (conteos de estado) de todos los tenants del usuario en una sola llamada.

### 0.3 Identidad y token

- El access JWT ([`security.md`](./security.md) §5.1) lleva `tnt` (lista de tenant IDs de las membresías del usuario,
  máx. 50; si hay más, `tnt_ver` y el gateway resuelve con `auth`), `pla` (roles de plataforma, si los hay) y `perms`
  **por tenant** (`{"<tenant_id>": {"devices.read": ["*"], ...}}`) mientras quepa en 4 KiB; si no, `perms_ver` y los
  servicios resuelven con caché de 60 s.
- Cambiar de ISP en la UI **no** emite un token nuevo: el token ya cubre todos los tenants del usuario.
- El gateway rechaza pronto si `tenant_id` de la ruta ∉ `tnt` (y no hay acceso de soporte); el servicio dueño vuelve
  a comprobar pertenencia y permiso fino con alcance dentro del tenant.

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

- Colecciones en **plural, kebab-case**: `/sites`, `/routers`, `/wireguard/servers`, `/audit-events`, siempre bajo
  `/tenants/{tenant_id}` si el recurso es de negocio (§0.2). En las tablas de este documento las rutas de negocio se
  escriben **relativas a `/api/v1/tenants/{tenant_id}`** para abreviar.
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
| `PUT` | Sólo reemplazo de asociaciones (`PUT /members/{user_id}/role-assignments`, `PUT /dashboards/{id}/layout`) y secretos write-only (`PUT /routers/{id}/credentials/{kind}`). | `200` / `204` |
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
  `RATE_LIMITED`, `TENANT_RATE_LIMITED`, `INTERNAL`, `SERVICE_UNAVAILABLE`, `ANALYTICS_UNAVAILABLE`, `TIMEOUT`,
  `INVALID_CREDENTIALS`, `INVALID_CURSOR`, `INVALID_FILTER`, `INVALID_SORT_FIELD`, `TIME_RANGE_TOO_LARGE`,
  `TENANT_NOT_FOUND`, `TENANT_SUSPENDED`, `KIOSK_FORBIDDEN`.
  De dominio, con prefijo de entidad: `ROUTER_NOT_FOUND`, `SITE_NOT_EMPTY`, `PEER_ALREADY_REVOKED`,
  `WIREGUARD_IP_POOL_EXHAUSTED`, `CUSTOMER_NOT_FOUND`, `CUSTOMER_TYPE_LOCKED`, `DASHBOARD_NOT_FOUND`,
  `WIDGET_TYPE_NOT_ALLOWED`, `KIOSK_ENROLLMENT_CODE_INVALID`, `STORAGE_TARGET_UNREACHABLE`. Catálogo en la spec OpenAPI (`components/schemas/ErrorCode`, enum abierto).

### 1.5 Paginación — cursor (keyset) por defecto

**Recomendación: cursor opaco keyset en todas las colecciones; sin offset en v1.** Offset duplica/salta filas en
colecciones que cambian mientras se paginan (alertas, auditoría) y degrada en PostgreSQL con offsets grandes; UUIDv7 es
ordenable por tiempo y hace el keyset trivial con `(clave_orden, id)`.

```
GET /api/v1/routers?limit=50&status=online,degraded&sort=-last_observed_at
```

```json
{
  "data": [ { "id": "0192...", "name": "rt-core-01", "status": "online" } ],
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

- Igualdad: `?site_id=...&status=online`. Multivalor por comas (OR en el campo, AND entre campos):
  `?status=offline,degraded`.
- Rangos con sufijos `_gte`, `_lte`, `_gt`, `_lt`: `?created_at_gte=2026-10-01T00:00:00Z`.
- Tags: `?tag=core,edge` (todas) · `?tag_any=core,edge` (alguna).
- Búsqueda libre: `?q=` (prefijo/trigram sobre campos documentados: nombre, IP de gestión, descripción).
  **Excepción**: las IPs de clientes (D1: la IP *es* el cliente, dato personal) **nunca** viajan en la URL (quedarían en
  access logs, historial del navegador y `Referer`): se buscan con `POST .../customers/lookup` (§2.9).
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
- **Implementación genérica en el gateway** (middleware sobre el proxy, Valkey):
  `idem:{session_user}:{method}:{route}:{key}` → `{request_hash, status, headers, body}`, TTL 24 h.
  - Primera vez: reserva `in_progress` (SET NX, TTL 60 s) y hace proxy.
  - Repetición con mismo hash y respuesta guardada → misma respuesta + `Idempotency-Replayed: true`.
  - En vuelo → `409 IDEMPOTENCY_IN_PROGRESS` + `Retry-After: 1`. Mismo key con otro cuerpo → `422 IDEMPOTENCY_KEY_REUSED`.
  - Sólo se guardan `2xx` y `4xx` deterministas (no `5xx`, `429`).
  - Si Valkey no está: el gateway reenvía la cabecera y el servicio aplica su defensa (unicidad natural / clave en BD).
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

Implementado en el gateway (Valkey GCRA; si Valkey cae, limitador en memoria por réplica — [`architecture.md`](./architecture.md) §7.1).
Claves con prefijo de tenant (`rl:t:<tenant_id>:...`) para que un ISP no consuma el cupo de otro.
Valores alineados con [`security.md`](./security.md):

| Ámbito | Límite inicial |
| --- | --- |
| `POST /auth/login`, `/auth/mfa/verify`, `/auth/password/*` | 5/min por cuenta + 20/min por IP; bloqueo progresivo según `security.md`. |
| API autenticada | 600 req/min por sesión (`sid`; ráfaga 100). |
| Analítica (`/analytics/*`) | 60 req/min por sesión. |
| Exportaciones / reportes | 10/h por usuario. |
| **Por tenant** (vecino ruidoso) | API 3.000 req/min y analítica 300 req/min por tenant, sumando usuarios y kioscos; superarlo → `429 TENANT_RATE_LIMITED`. Ajustable por tenant (`platform.tenants.manage`). |
| Kioscos (§2.12) | 120 req/min por kiosco; sólo `GET` de datos de widgets y configuración. |
| `POST /kiosk/enroll` | 5/min por IP y 10 intentos fallidos por código (el código se invalida). |
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
  antes de reenviar. Un API token pertenece a **un solo tenant** (o a la plataforma) y sus rutas deben coincidir.
- Pantallas NOC: credencial de **dispositivo kiosco** propia (cookie `__Secure-hf_kiosk`, `Path=/api/v1/kiosk`), que
  se canjea por un access JWT de solo lectura (§2.12).
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

## 2. Endpoints iniciales

Módulos dueños según [`services.md`](./services.md) (si el Agente A los agrupa en un binario modular, la columna
"servicio" es el módulo). Rutas bajo `/api/v1`; **las de negocio son relativas a `/api/v1/tenants/{tenant_id}`** (§0.2)
salvo que la tabla diga "plataforma" o empiecen por `/me`, `/auth`, `/kiosk`, `/platform`. Permisos del catálogo de
[`security.md`](./security.md) §6.1, evaluados **dentro del tenant de la ruta**. 🔒 = requiere re-autenticación reciente
(`REAUTH_REQUIRED`, `security.md` §5.1).

### 2.1 Sesión y cuenta propia (`auth`)

| Método y ruta | Descripción | Permiso |
| --- | --- | --- |
| `POST /auth/login` | Usuario + contraseña. Sin 2FA → `200 {"access_token", "expires_at"}` + cookie de refresh. Con 2FA → `200 {"mfa_required": true, "mfa_token": "..."}` (5 min, un uso). | público, rate limited |
| `POST /auth/mfa/verify` | `mfa_token` + código TOTP (o de recuperación) → access token + cookie de refresh. | público, rate limited |
| `POST /auth/refresh` | Rota el refresh (cookie) → nuevo access token. Reutilización ⇒ revoca la sesión. Requiere `X-Requested-With: horus`. | cookie de refresh |
| `POST /auth/reauth` | Contraseña (+TOTP) para marcar `auth_time` reciente. | autenticado |
| `POST /auth/logout` | Revoca la sesión actual y borra la cookie. Requiere `X-Requested-With: horus`. | autenticado |
| `POST /auth/password/forgot` · `POST /auth/password/reset` | Recuperación. | público, rate limited |
| `POST /auth/invitations/accept` | Acepta una invitación a un tenant (token de un uso, 72 h); crea el usuario si no existe. | público, rate limited |
| `GET /me` | Usuario, roles de plataforma, **membresías** (`[{tenant_id, tenant_slug, tenant_name, roles, permissions_with_scope}]`). | autenticado |
| `GET /me/tenants/summary` | Resumen mínimo por tenant del usuario (routers por `status`, clientes con hallazgos abiertos, alertas abiertas). Único endpoint que cruza tenants; sólo devuelve conteos de tenants donde el usuario tiene `devices.read`. | autenticado |
| `POST /ws/tickets` | Ticket de un uso (30 s) para abrir el WebSocket (§4.2). | autenticado |
| `PATCH /me` | Nombre, idioma, zona horaria, tenant por defecto en la UI. | autenticado |
| `POST /me/password` | Cambio de contraseña (revoca otras sesiones). | autenticado |
| `POST /me/totp/enroll` · `POST /me/totp/confirm` · `DELETE /me/totp` 🔒 | Gestión TOTP. | autenticado |
| `GET /me/sessions` · `DELETE /me/sessions/{session_id}` | Mis sesiones. | autenticado |

### 2.2 Miembros, roles, auditoría del tenant (`auth`)

El usuario es de plataforma; lo que gestiona un administrador de ISP es la **membresía** en su tenant. Quitar una
membresía corta el acceso a ese tenant en ≤ 5 s (evento `horus.auth.membership.revoked` → el gateway cancela las
suscripciones WebSocket del tenant; los tokens vigentes dejan de servir para ese tenant porque el gateway consulta la
caché de membresías revocadas, igual que la de sesiones).

| Método y ruta (relativa al tenant) | Descripción | Permiso |
| --- | --- | --- |
| `GET /members` · `GET /members/{user_id}` | Miembros del tenant (filtros `status`, `role_id`, `q`). Sólo datos del usuario necesarios (nombre, email, estado, 2FA activo); nunca sus otras membresías. | `users.read` |
| `POST /members` | Invitar por email con roles iniciales. Si el email ya existe en la plataforma se añade la membresía (sin revelar al invitador si existía: misma respuesta `202`). | `users.manage` |
| `DELETE /members/{user_id}` | Quitar del tenant (no borra al usuario de la plataforma). | `users.manage` |
| `GET /members/{user_id}/role-assignments` | Asignaciones `(rol, alcance)` en este tenant. | `roles.read` |
| `PUT /members/{user_id}/role-assignments` 🔒 | Reemplaza `[{"role_id", "scope": "tenant" \| "site:<id>" \| "router_group:<id>"}]`. | `roles.assign` |
| `GET /roles` · `GET /roles/{id}` | Roles del tenant (plantillas de sistema + propios). | `roles.read` |
| `POST /roles` · `PATCH /roles/{id}` · `DELETE /roles/{id}` | Roles propios del tenant (los de sistema no se editan). | `roles.manage` |
| `GET /audit-events` | Auditoría **del tenant** (sólo registros con ese `tenant_id`, incluidos los accesos de soporte de plataforma). Filtros `actor_id`, `action`, `resource_type`, `resource_id`, `outcome`, `occurred_at_gte/lte`. | `audit.read` |
| `POST /audit-events/exports` | Exportación (asíncrona, `202`). | `audit.export` |
| `GET /api-tokens` · `POST /api-tokens` 🔒 · `DELETE /api-tokens/{id}` | API tokens de integración ligados a este tenant (alcance ⊆ permisos del creador en el tenant). | `api_tokens.manage` |

`GET /permissions` (sin tenant) devuelve el catálogo. Las sesiones son de la persona, no del tenant: un administrador
de ISP **no** puede revocar sesiones (afectarían a otros ISP del mismo usuario); quita la membresía. Revocar sesiones
de cualquiera es de plataforma (§2.8).

### 2.3 Inventario (`devices`)

| Método y ruta | Descripción | Permiso |
| --- | --- | --- |
| `GET /sites` · `GET /sites/{id}` | Sitios (filtros `q`, `tag`, `parent_id`). | `sites.read` |
| `POST /sites` · `PATCH /sites/{id}` · `DELETE /sites/{id}` | Gestión (con routers → `409 SITE_NOT_EMPTY`). | `sites.create` / `sites.update` / `sites.delete` |
| `GET /routers` | Lista (filtros `site_id`, `status`, `observed_state`, `vendor_id`, `tag`, `group_id`, `q`; orden `name`, `status`, `last_observed_at`). Incluye `status`, `observed_state`, `status_reason`, `last_observed_at` (§2.6). | `devices.read` |
| `GET /routers/status-summary` | Conteo por `status` efectivo (los 8 valores de §2.6), opcional `site_id`/`group_id`. Base del dashboard online/offline/warning/critical. | `devices.read` |
| `POST /routers` · `GET /routers/{id}` · `PATCH /routers/{id}` · `DELETE /routers/{id}` | Registrar / detalle / editar / baja. | `devices.create` / `devices.read` / `devices.update` / `devices.delete` |
| `PUT /routers/{id}/credentials/{kind}` 🔒 | Credencial **write-only** por tipo: `snmp` (v3 authPriv preferente; v2c sólo dentro del túnel), `routeros_api` (usuario + contraseña o certificado para la API REST de RouterOS v7 sobre HTTPS, D10), `ssh` (usuario + llave ed25519 generada por Horus; se devuelve sólo la **pública** para instalarla en el router). Respuesta sin secretos (`configured`, `kind`, `updated_at`, `last_used_at`, `last_result`). Sustituye a `PUT /routers/{id}/snmp-credentials`. | `devices.credentials.write` |
| `POST /routers/{id}/credentials/{kind}/test` | Prueba de conectividad/autenticación con la credencial guardada (`202`; resultado por WebSocket). `Idempotency-Key` obligatorio. | `devices.update` |
| `POST /routers/{id}/credentials/{kind}/reveal` 🔒 | Revela en claro (auditado, 2FA). Nadie lo tiene por defecto salvo `tenant_admin`. | `devices.credentials.reveal` |
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
| `GET /analytics/routers/{id}/metrics` | `analytics` | Series de dispositivo con rango (§1.7): `cpu_percent`, `memory_percent`, `temperature_celsius`, `uptime_seconds`, `poll_latency_ms`, `availability_ratio`. | `snmp.read` |
| `GET /analytics/routers/{id}/interfaces/metrics` | ídem | Series por interfaz (`interface_id=a,b`): `rx_bps`, `tx_bps`, `rx_errors`, `tx_errors`, `rx_drops`, `tx_drops`, `oper_status`. | `snmp.read` |

> Con D4 ClickHouse existe desde el primer incremento que guarda series, así que las históricas las sirve siempre su
> dueño analítico ([`services.md`](./services.md)); desaparece el enrutado temporal a `snmp` (C-17 resuelta). Las
> métricas de MikroTik obtenidas por la API REST de RouterOS (D10, [`vendors/mikrotik.md`](./vendors/mikrotik.md))
> se exponen por los mismos endpoints: el campo `meta.source` indica `snmp` o `routeros_api`.

### 2.6 Estado del router: un solo conjunto de valores

Se unifican los estados de [`architecture.md`](./architecture.md) §10.2–§10.4 (`online/degraded/offline/stale`) y los del
dashboard de [`vision.md`](./vision.md) Sprint 3 (`online/offline/warning/critical`) en **8 valores**, todos con semántica
distinta. Hay dos campos:

- `observed_state`: lo que **observa** `snmp` (ICMP + SNMP + handshake WG). Sólo 5 valores. Viaja en
  `horus.snmp.router.state_changed` ([`events.md`](./events.md) §8.4). Dueño: `snmp`.
- `status`: estado **efectivo** que muestra la UI y por el que se filtra. Lo calcula `devices` (proyección
  `router_status_cache`) a partir de `observed_state` + frescura de la observación + configuración administrativa.
  **No se publica un evento propio**: no existe `horus.devices.router.status_changed`. Los consumidores que necesitan el
  estado reaccionan a `horus.snmp.router.state_changed` (observación) y a `horus.devices.router.maintenance_started/_ended`
  (administración); la UI recalcula `status` con la misma tabla (función compartida en `packages/`).

| `status` | `observed_state` | Significado | Cuándo |
| --- | --- | --- | --- |
| `online` | `online` | Responde y está dentro de umbrales | ICMP ✓, SNMP ✓, WG OK |
| `warning` | `warning` | Responde pero un umbral de severidad media está superado | CPU/RAM/temperatura/errores sobre umbral *warning* |
| `critical` | `critical` | Responde pero con un umbral crítico superado | Umbral *critical* (p. ej. temperatura, CPU sostenida 95 %) |
| `degraded` | `degraded` | Alcanzable pero sin monitoreo completo | ICMP ✓ + SNMP ✗ (`snmp_unreachable`, `snmp_auth_failed`) |
| `offline` | `offline` | No alcanzable (con razón) | ICMP ✗ + SNMP ✗ (`tunnel_down`, `host_unreachable_via_tunnel`) |
| `stale` | (el último conocido) | **No sabemos**: la última observación tiene > 3 intervalos o falta el heartbeat del poller | `snmp` caído o atrasado; nunca se marca `offline` por ausencia de monitoreo |
| `unknown` | — | Nunca observado | Router recién creado, SNMP/ICMP deshabilitado, o antes del Sprint 5 |
| `maintenance` | (cualquiera) | Ventana de mantenimiento activa; suprime alertas | Flag administrativo de `devices` |

Precedencia al calcular `status`: `maintenance` > `unknown` > `stale` > `observed_state`. La respuesta REST incluye
siempre `observed_state` (puede ser `null`), `status_reason` y `last_observed_at`, para que la UI explique, p. ej.,
"en mantenimiento — último estado observado: offline".

### 2.7 Estado del sistema (modos degradados)

`GET /api/v1/system/status` — lo sirve **el propio gateway** (no hace proxy), a partir de sus health checks de
dependencias (cada 10 s, cacheado), del estado de su conexión NATS y del heartbeat de `snmp`. Permite a la UI mostrar
banners como "Analítica no disponible" sin esperar a que falle una petición.

Permiso: **cualquier usuario autenticado** obtiene la vista resumida (lo que necesita la UI); el detalle por componente
(`components[].detail`, latencias, versiones) sólo con el permiso de plataforma `platform.status.read` (el detalle es
de infraestructura compartida y no debe verlo un ISP). Kioscos: vista resumida. Sin autenticación → `401` (no revela
topología).

```json
{
  "status": "degraded",
  "checked_at": "2026-10-07T14:03:20Z",
  "capabilities": {
    "auth": "ok", "inventory": "ok", "wireguard": "ok",
    "monitoring": "stale", "realtime": "ok", "analytics": "unavailable", "reports": "unavailable"
  },
  "components": [
    { "name": "analytics", "status": "down", "since": "2026-10-07T13:50:02Z", "detail": "clickhouse: connection refused" },
    { "name": "snmp", "status": "degraded", "since": "2026-10-07T14:01:00Z", "detail": "heartbeat ausente 75 s" }
  ]
}
```

- `status` global: `ok` | `degraded` | `down` (sólo `down` si `auth` o el propio gateway no funcionan).
- `capabilities` es lo que consume la UI (vocabulario estable, `ok` | `degraded` | `stale` | `unavailable`); `components`
  usa los nombres de servicio y de infraestructura (`postgres`, `valkey`, `nats`, `clickhouse`, `local_storage`, `remote_storage`) y puede crecer. `remote_storage` vale `not_configured` si no hay destino remoto de copias (D2): la UI lo muestra como aviso a `platform_admin`, nunca como fallo.
- Cambios de `capabilities` se empujan también por WebSocket en el topic `system` (`{"type":"state","topic":"system",
  "key":"status","data":{...}}`).
- Rate limit propio: la UI lo consulta como máximo cada 30 s (o sólo al reconectar el WebSocket).

### 2.8 Plataforma: tenants, nodos y usuarios (`auth` + `devices`; sólo roles de plataforma)

Rutas **absolutas** bajo `/api/v1/platform`. Un `platform_admin` gestiona la plataforma, **no** ve datos de negocio de
los ISP (clientes, tráfico, hallazgos) salvo con acceso de soporte explícito y temporal ([`security.md`](./security.md)
§6.6).

| Método y ruta | Servicio | Descripción | Permiso |
| --- | --- | --- | --- |
| `GET /platform/tenants` · `GET /platform/tenants/{id}` | auth | ISP: `slug`, `name`, `status` (`active`/`suspended`/`offboarding`), país (para la ley aplicable), cuotas, conteos (nodos, routers, clientes, flujos/s), `support_access_policy`. | `platform.tenants.read` |
| `POST /platform/tenants` 🔒 | auth | Alta de ISP + invitación al primer `tenant_admin`. `Idempotency-Key` obligatorio. | `platform.tenants.manage` |
| `PATCH /platform/tenants/{id}` | auth | Nombre, cuotas (`max_routers`, `max_flows_per_second`, `max_customers`), retenciones dentro de los límites de plataforma. | `platform.tenants.manage` |
| `POST /platform/tenants/{id}/suspend` · `/resume` 🔒 | auth | Suspender: sus usuarios reciben `403 TENANT_SUSPENDED`; la ingesta sigue o se pausa según `pause_ingest`. | `platform.tenants.manage` |
| `POST /platform/tenants/{id}/offboard` 🔒 | auth | Baja: exportación final + borrado programado (crypto-shredding de la KEK del tenant y purga de datos, [`security.md`](./security.md) §13.5). Requiere confirmación con el `slug`. | `platform.tenants.manage` |
| `GET /platform/tenants/{id}/nodes` · `POST ...` · `PATCH /platform/nodes/{id}` | devices | Alta asistida de nodos y su router principal (*onboarding*): crea `site` + `router` + peer WireGuard + exportador esperado en el tenant. Equivale a las rutas de tenant, pero sin necesitar membresía. | `platform.nodes.manage` |
| `GET /platform/exporters/unassigned` | flows | Exportadores que envían flujos sin estar asociados a ningún router/tenant (descartados). Sólo plataforma: aún no tienen tenant. | `platform.nodes.manage` |
| `GET /platform/users` · `GET /platform/users/{id}` | auth | Usuarios de la plataforma con sus membresías. | `platform.users.read` |
| `POST /platform/users/{id}/disable` · `/enable` 🔒 | auth | Bloquear (revoca sesiones y tokens en todos los tenants). | `platform.users.manage` |
| `PUT /platform/users/{id}/platform-roles` 🔒 | auth | Roles de plataforma. Nadie se asigna a sí mismo. | `platform.users.manage` |
| `GET /platform/sessions` · `DELETE /platform/sessions/{id}` | auth | Sesiones de cualquier usuario. | `platform.users.manage` |
| `POST /platform/tenants/{id}/support-access` 🔒 | auth | Acceso de soporte temporal (motivo obligatorio, ≤ 4 h, notificado al `tenant_admin`, auditado en ambos registros). | `platform.support_access` |
| `GET /platform/audit-events` | auth | Auditoría de plataforma (acciones sin tenant y de roles de plataforma). | `platform.audit.read` |
| `GET /platform/storage-targets` · `POST` · `PATCH /platform/storage-targets/{id}` · `DELETE` | ops (dueño lo fija el Agente A) | Destinos remotos de copias (D2): `kind` `sftp` \| `gdrive` \| `dropbox` \| `mega` \| `s3`, parámetros no secretos, `schedule`, `encryption: client_side` (obligatorio). | `platform.storage.manage` |
| `PUT /platform/storage-targets/{id}/credentials` 🔒 | ídem | Secretos write-only (llave SFTP, token OAuth, contraseña); nunca se devuelven. | `platform.storage.manage` |
| `POST /platform/storage-targets/{id}/test` | ídem | Prueba escritura/lectura/borrado de un objeto de prueba (`202`). | `platform.storage.manage` |
| `GET /platform/backups` | ídem | Últimas copias locales y remotas por componente, verificación y antigüedad ([`disaster-recovery.md`](./disaster-recovery.md)). | `platform.status.read` |

### 2.9 Clientes descubiertos (`devices`; D1)

Un **cliente es una IP** vista en los flujos del router principal de un nodo. Identidad: `(tenant_id, realm_id, ip)`
(el *realm* distingue espacios de direcciones que se repiten, p. ej. 100.64.0.0/10 en dos nodos con CGNAT; modelo del
Agente B en [`database.md`](./database.md)). No hay alta manual, ni CRM, ni asignaciones IP↔cliente: los antiguos
`customer.assigned/unassigned` y `subscribers.*` desaparecen.

Representación:

```json
{
  "id": "0192f0c4-7a1e-7c3a-9b1d-2f6e8a4c1d55",
  "tenant_id": "0192e000-0000-7000-8000-000000000001",
  "ip": "100.64.12.34",
  "realm_id": "0192e111-...", "site_id": "0192e222-...", "router_id": "0192e333-...",
  "status": "active",
  "type": "commercial",
  "type_source": "scoring",
  "type_locked": false,
  "type_confidence": 0.87,
  "type_changed_at": "2026-10-06T09:00:00.000Z",
  "security_state": "suspected",
  "alias": null,
  "first_seen_at": "2026-09-01T10:12:00.000Z",
  "last_seen_at": "2026-10-07T14:00:00.000Z",
  "version": 6
}
```

- `type`: `residential` (por defecto al descubrir) | `commercial` (enum abierto). `type_source`: `default` | `scoring` |
  `manual`. `type_locked = true` cuando el tipo es manual: el scoring sigue calculando y mostrando su sugerencia
  (`suggested_type` en el detalle), pero **no** lo cambia hasta que alguien lo desbloquee.
- `status`: `active` | `expired` (sin tráfico durante `customer_inactivity_days`, por defecto 90, configurable por tenant;
  C-18). Una IP expirada que vuelve a verse se reactiva **con el mismo `id`** y su historial (D1: la IP es el cliente).
- `security_state` (D5): `clean` | `suspected` | `infected` | `mitigated`; lo calcula `detection` (§2.10).
- `last_seen_at` tiene resolución de 1 h (no se escribe por cada flujo).

| Método y ruta (relativa al tenant) | Servicio | Descripción | Permiso |
| --- | --- | --- | --- |
| `GET /customers` | devices | Lista (filtros `site_id`, `router_id`, `realm_id`, `status`, `type`, `type_source`, `type_locked`, `security_state`, `last_seen_at_gte/lte`; orden `last_seen_at`, `first_seen_at`, `type_changed_at`). **Sin** filtro por IP en la URL. | `customers.read` |
| `POST /customers/lookup` | devices | Búsqueda por IP o prefijo en el **cuerpo**: `{"ip": "100.64.12.34"}` o `{"prefix": "100.64.12.0/24", "realm_id": "..."}` → misma forma que la colección, con cursor. `200` (no crea nada; `POST` sólo para sacar la IP de la URL). | `customers.read` |
| `GET /customers/{id}` | devices | Detalle + `suggested_type` y `suggested_confidence` del último scoring. | `customers.read` |
| `PATCH /customers/{id}` | devices | `alias`, `notes` (opcionales; pueden ser PII: se registran en auditoría sin valor). | `customers.update` |
| `POST /customers/{id}/set-type` | devices | Cambio **manual**: `{"type": "commercial", "reason": "Contrato empresarial verificado", "lock": true}`. `reason` obligatorio (≥ 10 caracteres). Emite `horus.devices.customer.type_changed` con `source=manual`. | `customers.type.write` |
| `POST /customers/{id}/unlock-type` | devices | Devuelve el control al scoring (`type_locked=false`); el siguiente scoring puede cambiar el tipo. | `customers.type.write` |
| `GET /customers/{id}/type-history` | devices | Historial de tipo: `[{changed_at, previous_type, type, source, actor, reason, model_version, confidence, reasons[]}]`. Las razones del scoring se copian al historial en el momento del cambio (no dependen de la retención de `detection`). | `customers.read` |
| `GET /customers/{id}/scoring` | detection | Último scoring y su explicación: `model_version`, `scores` por clase, `features` (con valor y peso), `reasons[]` en lenguaje natural, ventana evaluada. Serie de scorings con `?range=90d`. | `customers.read` |
| `GET /customers/{id}/findings` | detection | Hallazgos de seguridad del cliente (§2.10). | `security.read` |
| `GET /analytics/customers/{id}/traffic` | analytics | Series de tráfico del cliente (§1.7): `rx_bps`, `tx_bps`, por servicio/categoría/ASN. Acceso a detalle por cliente auditado. | `traffic.customer.read` |
| `GET /customers/stats` | devices | Conteos por `type`, `type_source`, `status`, `security_state` y `site_id` (widgets de dashboard). | `customers.read` |
| `POST /customers/exports` | devices | Exportación CSV (asíncrona, auditada, con IPs). | `customers.export` |

Notas:

- **Concurrencia**: `set-type` y `unlock-type` requieren `If-Match` (la versión del cliente): si un scoring cambió el
  tipo mientras el operador miraba la ficha → `412` con `current`.
- `set-type` sobre un cliente con `type_locked=true` y el mismo tipo → `200` sin cambio (idempotente); con otro tipo →
  permitido (manual sobre manual), queda en el historial.
- El borrado de un cliente sólo existe como **purga** por privacidad o por baja del tenant (plataforma), no en la API
  del tenant.

### 2.10 Seguridad: hallazgos y botnets (`detection`; D5)

| Método y ruta (relativa al tenant) | Descripción | Permiso |
| --- | --- | --- |
| `GET /findings` | Hallazgos (filtros `kind`, `severity`, `state` `open`/`acknowledged`/`resolved`/`false_positive`, `customer_id`, `site_id`, `created_at_gte`). | `security.read` |
| `GET /findings/{id}` | Detalle con `reasons[]` (código, detalle, peso), evidencias agregadas (destinos por ASN/puerto, periodicidad, feeds de reputación coincidentes) y ventana. Nunca payloads. | `security.read` |
| `POST /findings/{id}/acknowledge` · `/resolve` · `/mark-false-positive` | Gestión; `comment` obligatorio en `mark-false-positive` (alimenta el ajuste del modelo). | `security.manage` |
| `GET /security/summary` | Conteos por `security_state` y `kind` por nodo (widgets NOC). | `security.read` |
| `GET /reputation/sources` | Estado de los feeds de reputación (última actualización, entradas). | `security.read` |

`kind` (enum abierto): `botnet_c2_communication`, `ddos_participation`, `outbound_scanning`, `spam_smtp_outbound`,
`open_proxy_abuse`, `cryptomining`, `reputation_hit`. La **mitigación activa** (p. ej. añadir la IP a un
`address-list` del MikroTik) no forma parte de v1: requiere decisión del PO (C-21) y escribiría en routers.

### 2.11 Dashboards modulares (D8)

Dueño: módulo `dashboards` (servicio que fije el Agente A; candidato natural: `analytics`, porque resuelve datos de
widgets). Un dashboard es **un documento** con su layout y sus widgets; widgets y layout también tienen rutas propias
para que el editor no reescriba el documento entero, pero todas incrementan la `version` del dashboard (un solo ETag).

```json
{
  "id": "0192...", "tenant_id": "0192...", "version": 12,
  "name": "NOC — Nodos norte", "visibility": "tenant", "owner_id": "0192...",
  "layout": { "grid": "12-col", "row_height_px": 80, "breakpoints": { "lg": 1600, "md": 1200 } },
  "default_range": "6h", "refresh_seconds": 30,
  "widgets": [
    { "id": "w-routers", "type": "routers_status_grid", "title": "Routers",
      "position": { "x": 0, "y": 0, "w": 6, "h": 4 },
      "config": { "site_ids": ["0192..."], "show": ["offline", "critical", "warning"] }, "refresh_seconds": 15 },
    { "id": "w-bw", "type": "site_bandwidth_timeseries", "title": "Ancho de banda",
      "position": { "x": 6, "y": 0, "w": 6, "h": 4 }, "config": { "site_ids": ["0192..."], "range": "6h" } }
  ]
}
```

| Método y ruta (relativa al tenant salvo indicación) | Descripción | Permiso |
| --- | --- | --- |
| `GET /widget-types` (sin tenant) | Catálogo: `type`, `title`, `config_schema` (JSON Schema), `required_permission`, `data_endpoint_kind` (`state`/`series`/`table`), `realtime_topic` (si se actualiza por WebSocket), `contains_personal_data` (bool), `kiosk_allowed` (bool). | autenticado |
| `GET /dashboards` · `GET /dashboards/{id}` | Propios + compartidos con el tenant (`visibility = tenant`). | `dashboards.read` |
| `POST /dashboards` | Crear (privado por defecto). Validación de cada `config` contra su `config_schema`. | `dashboards.read` (privados) · `dashboards.manage` (`visibility=tenant`) |
| `PATCH /dashboards/{id}` · `DELETE /dashboards/{id}` | Editar/borrar (dueño, o `dashboards.manage` si es del tenant). `If-Match`. | ídem |
| `POST /dashboards/{id}/duplicate` | Copia privada. | `dashboards.read` |
| `PUT /dashboards/{id}/layout` | Reemplaza `layout` y las `position` de todos los widgets (arrastrar y soltar). `If-Match`. | dueño / `dashboards.manage` |
| `POST /dashboards/{id}/widgets` · `PATCH /dashboards/{id}/widgets/{widget_id}` · `DELETE ...` | CRUD de un widget; `If-Match` con la versión **del dashboard**. | dueño / `dashboards.manage` |
| `GET /dashboards/{id}/widgets/{widget_id}/data` | **Datos del widget** resueltos en servidor a partir de su `config` guardada (+ `range`/`from`/`to` opcionales, §1.7). El cliente no envía consultas arbitrarias. Respuesta `{ "data": ..., "meta": {..., "widget_type", "generated_at"} }`. | `required_permission` del tipo **y** acceso al dashboard |
| `POST /widget-data/preview` | Datos de un widget **no guardado** (editor): cuerpo `{type, config, range}`. No disponible para kioscos. | `required_permission` del tipo |
| `GET /playlists` · `POST` · `PATCH /playlists/{id}` · `DELETE` | Rotaciones para pantallas: `[{dashboard_id, duration_seconds}]`, `transition`. | `dashboards.manage` |

Reglas de datos de widgets:

- El servidor ejecuta la consulta con los permisos y el **alcance** de quien mira (usuario o kiosco), no del autor del
  dashboard: un dashboard compartido no amplía permisos. Si el espectador no tiene el permiso del widget, ese widget
  responde `403 WIDGET_TYPE_NOT_ALLOWED` y la UI lo muestra bloqueado; el resto del dashboard funciona.
- Widgets con `contains_personal_data = true` (p. ej. "top clientes por consumo", "clientes con hallazgos") muestran IPs
  sólo a usuarios con `customers.read` y **nunca** a kioscos salvo política explícita del tenant (§2.12).
- Cada respuesta lleva `Cache-Control: private, max-age=<refresh_seconds/2>` y `ETag`; el servidor cachea por
  `(tenant, widget_type, config_hash, range_bucket, alcance)` en Valkey para que diez pantallas iguales no hagan diez
  consultas a ClickHouse.
- Timeout por widget 10 s; un widget lento no bloquea a los demás (peticiones independientes).

### 2.12 Modo kiosco para pantallas NOC (D8)

Problema: una pantalla mural debe mostrar dashboards 24/7 sin que nadie inicie sesión, mientras que la sesión de un
usuario caduca (12 h de inactividad, 7 días absoluta) y exige 2FA. Un "token de solo lectura en la URL" sería lo más
simple, pero un token largo en la URL acaba en historial, logs del proxy, capturas de pantalla y cabecera `Referer`, y
quien lo copie tiene acceso indefinido desde cualquier lugar. Se descarta.

**Diseño: el kiosco es un dispositivo registrado, no un usuario.**

1. Un administrador del tenant crea un kiosco: `POST /kiosks` con `name`, `playlist_id` o `dashboard_ids`,
   `allowed_cidrs` (opcional pero recomendado: red del NOC), `show_personal_data` (por defecto `false`), `expires_at`
   (por defecto 180 días).
2. Genera un **código de enrolamiento**: `POST /kiosks/{id}/enrollment-codes` → código de 8 caracteres alfanuméricos +
   QR, un solo uso, **10 min**. El QR codifica `https://<host>/kiosk/enroll#code=...` (fragmento: no viaja al servidor
   ni a logs).
3. En la pantalla se abre `/kiosk` y se introduce o escanea el código → `POST /api/v1/kiosk/enroll {code}` (público,
   rate limited) → el servidor emite una **credencial de dispositivo** opaca (256 bits) en cookie
   `__Secure-hf_kiosk` (`HttpOnly; Secure; SameSite=Strict; Path=/api/v1/kiosk`), rotativa en cada uso y con detección
   de reutilización (como el refresh de usuario). En BD sólo su SHA-256.
4. La SPA en modo kiosco llama `POST /api/v1/kiosk/token` (cookie + `X-Requested-With: horus`) → access JWT de
   **10 min** con `sub = kiosk:<id>`, `typ = kiosk`, `tnt = [tenant]`, sin `perms` de escritura. Renovación igual que un
   usuario; WebSocket con ticket (§4.2).
5. `GET /api/v1/kiosk/config` devuelve la playlist/dashboards asignados; la UI entra en pantalla completa, rota y se
   autorrefresca.

| Método y ruta | Descripción | Permiso |
| --- | --- | --- |
| `GET /kiosks` · `GET /kiosks/{id}` (tenant) | Kioscos, `last_seen_at`, `last_ip`, estado. | `kiosks.manage` |
| `POST /kiosks` · `PATCH /kiosks/{id}` (tenant) | Alta/edición (dashboards, CIDR, política de datos personales, caducidad). | `kiosks.manage` |
| `POST /kiosks/{id}/enrollment-codes` 🔒 (tenant) | Código de un uso (10 min). `Idempotency-Key` obligatorio. | `kiosks.manage` |
| `POST /kiosks/{id}/revoke` (tenant) | Revocación inmediata: invalida la credencial y cierra su WebSocket (`4409`). | `kiosks.manage` |
| `POST /kiosk/enroll` | Canje del código → cookie de dispositivo. | público, rate limited |
| `POST /kiosk/token` | Cookie de dispositivo → access JWT de kiosco. | cookie de kiosco |
| `GET /kiosk/config` | Dashboards/playlist asignados y parámetros de rotación. | JWT de kiosco |

Qué puede hacer un JWT de kiosco (lista blanca en la tabla del gateway, `principal: kiosk`):

- `GET /tenants/{su_tenant}/dashboards/{id}` y `.../widgets/{wid}/data` **sólo** de los dashboards asignados y de
  widgets con `kiosk_allowed = true`; `GET /api/v1/system/status` (resumen); WebSocket a los topics de esos widgets.
- Nada más: ni `/me`, ni listas, ni exportaciones, ni `preview`, ni otros tenants. Cualquier otra ruta →
  `403 KIOSK_FORBIDDEN`.
- Si `allowed_cidrs` está definido, el gateway rechaza peticiones desde otras IPs (también el canje del código).

Análisis de seguridad (detalle en [`security.md`](./security.md) §5.5): el código es de un uso y corto; la credencial
de larga vida es HttpOnly (no la lee un XSS), rotativa (robo detectable), limitada por CIDR y revocable al instante; el
alcance es de solo lectura sobre dashboards concretos; por defecto sin IPs de clientes (una pantalla de NOC la ven
visitas y cámaras). Un kiosco de plataforma (todos los ISP) sólo puede mostrar salud agregada de la plataforma y lo
crea un `platform_admin`.

---

## 3. El gateway como proxy: reglas transversales

### 3.1 Tabla de rutas

`packages/schemas/openapi/gateway-routes.yaml` (declarativa, versionada; ilustrativo):

```yaml
- prefix: /api/v1/tenants/{tenant_id}/routers/{id}/metrics/live
  service: snmp
  tenant: path                 # path | none | platform
  methods: { GET: snmp.read }
- prefix: /api/v1/tenants/{tenant_id}/routers
  service: devices
  tenant: path
  methods: { GET: devices.read, POST: devices.create, PATCH: devices.update, DELETE: devices.delete }
- prefix: /api/v1/tenants/{tenant_id}/dashboards/{id}/widgets/{widget_id}/data
  service: analytics
  tenant: path
  principals: [user, api_token, kiosk]   # por defecto sólo [user, api_token]
  methods: { GET: widget }               # permiso según el tipo de widget (lo evalúa el dueño)
- prefix: /api/v1/platform/tenants
  service: auth
  tenant: platform
  methods: { GET: platform.tenants.read, POST: platform.tenants.manage, PATCH: platform.tenants.manage }
- prefix: /api/v1/auth/login
  service: auth
  tenant: none
  public: true
  rate_limit: login
```

- Coincidencia por prefijo más específico. Permiso **grueso**: "¿es miembro del tenant de la ruta y tiene X en algún
  alcance **de ese tenant**?" ([`security.md`](./security.md) §6.4). `tenant: platform` exige un rol de plataforma;
  `tenant: none` sólo autenticación (o `public`).
- `principals` limita qué tipo de identidad puede usar la ruta; un kiosco sólo alcanza las rutas que lo declaran.
- Test de CI: toda ruta bajo `/tenants/{tenant_id}` declara `tenant: path`; ninguna ruta `tenant: none` devuelve datos
  de negocio (revisión de la tabla en cada PR que la toque).
- Timeouts por ruta: 5 s CRUD, 15 s acciones, 30 s analítica. Circuit breaker por servicio.
- El gateway **no** agrega respuestas de varios servicios (sin BFF de composición en v1).
- Operaciones largas: `202 Accepted` + `Location` al recurso de ejecución (`/reports/{id}`, `/audit-events/exports/{id}`)
  con `status: pending|running|succeeded|failed`; el cambio de estado también llega por WebSocket.
- `/readyz` del gateway no falla por la caída de un servicio no crítico (`snmp`, `analytics`): sigue sirviendo login,
  inventario y WireGuard (Sprint 14).

### 3.2 Cabeceras que el gateway envía al servicio

| Cabecera | Contenido |
| --- | --- |
| `Authorization: Bearer <access JWT>` | El mismo JWT del cliente, ya validado (identidad, `sid`, tenants, permisos con alcance; [`security.md`](./security.md) §5.1) |
| `X-Horus-Tenant` | Tenant de la ruta, añadido por el gateway **sólo** como ayuda de observabilidad (logs/trazas). El servicio decide por la ruta y el JWT, nunca por esta cabecera. |
| `X-Request-Id` | ID de correlación |
| `traceparent`, `tracestate` | W3C Trace Context |
| `Idempotency-Key`, `If-Match`, `If-None-Match` | Tal cual |
| `X-Forwarded-For`, `X-Forwarded-Proto` | Normalizados por el gateway (IP real para auditoría) |

El gateway **elimina** `Cookie` y cualquier `X-Horus-*` entrante (un cliente no puede fijar `X-Horus-Tenant`); los
tickets WebSocket y las credenciales de kiosco nunca se reenvían.

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
   El gateway guarda en Valkey `ws_ticket:<sha256(ticket)> → {principal, sid, tnt, exp_del_token}` con TTL 30 s
   (`principal` = usuario o `kiosk:<id>`).
2. Abre `wss://<host>/api/v1/ws?ticket=<ticket>` con subprotocolo `horus.ws.v1`. El gateway hace `GETDEL` (un uso), valida
   `Origin` contra la lista permitida (defensa contra *cross-site WebSocket hijacking*) y acepta. El ticket en la URL es
   inútil tras el primer uso y caduca en 30 s; aun así Traefik y el gateway **redactan** el parámetro `ticket` en logs
   ([`observability.md`](./observability.md)). Ningún token de larga vida viaja en la URL.
3. **Renovación en banda**: la conexión hereda la expiración del access token. Antes de que expire, el cliente envía
   `{"type": "auth", "access_token": "<nuevo>"}`; el gateway lo valida, comprueba que el `sid` coincide y extiende. El
   gateway avisa con `auth_expiring` 60 s antes; si vence sin renovar → cierre `4401`.
4. Si Valkey no está disponible no se pueden emitir tickets: `POST /ws/tickets` → `503` y el frontend usa *polling* REST.
5. Integraciones no navegador: `Authorization: Bearer hf_pat_...` en el upgrade (sin ticket).
6. **Revocación**: con `horus.auth.session.revoked` / `horus.auth.user.disabled` / `horus.auth.kiosk.revoked`
   ([`events.md`](./events.md)) el gateway cierra con `4409` las conexiones de ese `sid`/usuario/kiosco en < 5 s.
7. **Cambio de permisos**: el gateway aplica los permisos del siguiente token renovado (≤ 10 min) y, si
   `horus.auth.role.updated` / `horus.auth.membership.updated` / `horus.auth.membership.revoked` quita permisos o el
   acceso a un tenant, cancela de inmediato las suscripciones afectadas (`unsubscribed`, `reason: "forbidden"`).
   `horus.auth.tenant.suspended` cancela todas las suscripciones de ese tenant.

### 4.3 Topics

Los topics son nombres lógicos del protocolo, **desacoplados de los subjects NATS** (el gateway mapea; el frontend nunca ve
subjects internos). **Todo topic de negocio lleva el tenant** como prefijo: `tenants.<tenant_id>.<topic>`. Una misma
conexión puede suscribirse a topics de varios tenants (usuario con varios ISP, vista NOC combinada); cada suscripción se
autoriza contra la membresía y los permisos **de ese tenant**. Sin prefijo sólo quedan `me`, `system` y los de
plataforma (`platform.*`).

En la tabla, `T.` abrevia `tenants.<tenant_id>.` y `<t>` el tenant en el subject ([`events.md`](./events.md) §2.4).

| Topic | Contenido | Clase | Permiso (+ alcance) | Subjects NATS de origen |
| --- | --- | --- | --- | --- |
| `T.routers.status` | Cambios de estado observado de routers visibles | evento | `devices.read` | `horus.snmp.router.state_changed.<t>.*` |
| `T.routers` | Altas/bajas/cambios de inventario de routers y nodos | evento | `devices.read` | `horus.devices.router.*.<t>.*`, `horus.devices.site.*.<t>.*` |
| `T.router.<id>` | Todo lo del router: inventario, interfaces, estado, `rebooted`, `interfaces_discovered`, `oper_status_changed`, capacidades RouterOS | evento | `devices.read` en el alcance del router | `horus.devices.*.*.<t>.<id>`, `horus.snmp.*.*.<t>.<id>` (+ interfaces filtradas por `router_id`) |
| `T.router.<id>.metrics` | Cada sondeo (dispositivo + interfaces; SNMP o API RouterOS) | estado | `snmp.read` en el alcance | `horus.telemetry.snmp.*.<t>.<id>`, `horus.telemetry.routeros.*.<t>.<id>` |
| `T.customers` | Clientes descubiertos, cambios de tipo, expirados/reactivados | evento | `customers.read` | `horus.devices.customer.*.<t>.*` |
| `T.security` | Hallazgos abiertos/actualizados/resueltos y cambios de `security_state` | evento | `security.read` | `horus.detection.finding.*.<t>.*`, `horus.detection.customer_security.*.<t>.*` |
| `T.wireguard.peers` | Peers creados/revocados/rotados, `handshake_stale/recovered` | evento | `wireguard.read` | `horus.wireguard.peer.*.<t>.*` |
| `T.wireguard.server.<id>.status` | Handshakes/contadores en vivo del hub (sólo peers del tenant) | estado | `wireguard.read` | `horus.telemetry.wireguard.peer_status.<t>.<id>` |
| `T.alerts` | `alert.opened/acknowledged/resolved` | evento | `alerts.read` | `horus.alerts.alert.*.<t>.*` |
| `T.dashboard.<id>` | Cambios del dashboard (la pantalla recarga layout/widgets) | evento | acceso al dashboard (usuario o kiosco asignado) | `horus.dashboards.dashboard.*.<t>.<id>`, `horus.dashboards.playlist.*.<t>.*` |
| `T.summary` | Conteos para la cabecera NOC (routers por estado, alertas abiertas, clientes con hallazgos) | estado | `devices.read` | calculado por el gateway desde los eventos anteriores + snapshot REST |
| `me` | Notificaciones al usuario, reportes listos, exportaciones listas, aviso de cierre de sesión, membresías cambiadas | evento | autenticado (filtro por `user_id`) | `horus.alerts.notification.*.*.*`, `horus.reporting.report.*.*.*`, `horus.auth.session.revoked.*.*`, `horus.auth.membership.*.*.*` |
| `system` | `capabilities` de `GET /system/status` (§2.7), `realtime_status` y heartbeat del poller | estado | autenticado o kiosco | health checks del gateway + `horus.snmp.poller.heartbeat.platform.*` |
| `platform.tenants` | Salud agregada por tenant (ingesta, exportadores silenciosos, cuotas) | estado | `platform.status.read` | `horus.flows.exporter.*.*.*`, `horus.auth.tenant.*.*.*` |

- **Evento**: cada mensaje cuenta; se entrega en orden de llegada; puede perderse ante desconexión.
- **Estado**: sólo importa el último valor por clave; al suscribirse se envía el último snapshot conocido; luego como
  máximo **1 actualización/s por topic y cliente** (coalescencia, [`architecture.md`](./architecture.md) §9.3).
- Permiso al suscribirse **y** filtro por alcance por mensaje en topics colectivos (`T.routers.status`): un operador
  con alcance `site:X` sólo recibe routers de X. El evento trae `site_id` para filtrar sin consultas.
- **Filtro de tenant por mensaje** (defensa en profundidad): además de suscribirse sólo a los subjects del tenant, el
  hub descarta cualquier mensaje cuyo `tenant_id` del sobre no coincida con el del topic, y lo cuenta
  (`horus_api_gateway_ws_tenant_mismatch_total`, alerta si > 0).
- Kioscos: sólo los topics que usan los widgets de sus dashboards asignados, más `T.dashboard.<id>` y `system`.

### 4.4 Mensajes

Cliente → servidor:

```json
{ "type": "subscribe",   "id": "c-17", "topic": "tenants.0192e000-0000-7000-8000-000000000001.router.0192f0c4-7a1e-7c3a-9b1d-2f6e8a4c1d55.metrics" }
{ "type": "unsubscribe", "id": "c-18", "topic": "tenants.0192e000-0000-7000-8000-000000000001.routers.status" }
{ "type": "auth",        "id": "c-20", "access_token": "eyJ..." }
{ "type": "ping",        "id": "c-19" }
```

Servidor → cliente:

```json
{ "type": "ack",   "id": "c-17", "topic": "tenants.0192e000-....router.0192...metrics" }
{ "type": "error", "id": "c-17", "code": "PERMISSION_DENIED", "message": "falta snmp.read en el alcance del router" }
{ "type": "event", "topic": "tenants.0192e000-....routers.status",
  "event": { "id": "0192f0d1-1b2c-7e44-8a10-6b9c2d1e0f33", "type": "horus.snmp.router.state_changed",
             "time": "2026-10-07T14:03:11.123Z", "subject": "0192f0c4-...", "aggregate_version": 42,
             "tenant_id": "0192e000-0000-7000-8000-000000000001",
             "data": { "router_id": "0192f0c4-...", "site_id": "0192e111-...", "previous_state": "online",
                       "state": "offline", "reason": "tunnel_down" } } }
{ "type": "state", "topic": "tenants.0192e000-....router.0192...metrics", "key": "device", "time": "2026-10-07T14:03:00Z",
  "data": { "cpu_percent": 13.2, "memory_percent": 61.0, "uptime_seconds": 1209600, "temperature_celsius": 47 } }
{ "type": "unsubscribed", "topic": "tenants.0192e000-....alerts", "reason": "forbidden" }
{ "type": "realtime_status", "status": "degraded", "reason": "event_bus_unavailable" }
{ "type": "heartbeat", "time": "2026-10-07T14:03:25Z" }
{ "type": "pong", "id": "c-19" }
{ "type": "auth_expiring", "expires_at": "2026-10-07T14:10:00Z" }
```

- `event.event` es una **proyección pública** del sobre NATS ([`events.md`](./events.md) §5): `id`, `type`, `time`,
  `subject`, `tenant_id`, `aggregate_version`, `data`. Se eliminan `actor` (salvo `actor.type`/`actor.id` si el topic lo
  requiere), `trace_parent`, `correlation_id`. Para kioscos se eliminan además los campos marcados `pii` en el catálogo
  (p. ej. `ip` de `customer.*`) salvo política del kiosco. `data` mantiene el esquema del evento (los tipos TS se generan del mismo
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
  (ver C-04).

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
- Suscripciones estáticas (todas las de dominio, de todos los tenants; volumen bajo): `horus.snmp.router.>`,
  `horus.snmp.interface.>`, `horus.snmp.poller.heartbeat.>`, `horus.devices.>`, `horus.wireguard.>`,
  `horus.detection.finding.>`, `horus.detection.customer_security.>`, `horus.dashboards.>`, `horus.auth.session.>`,
  `horus.auth.user.>`, `horus.auth.role.>`, `horus.auth.membership.>`, `horus.auth.tenant.>`, `horus.auth.kiosk.>`,
  `horus.alerts.>`, `horus.reporting.>`. El hub enruta por el token de tenant del subject (sin deserializar).
  Dinámicas (telemetría, sólo mientras haya suscriptores): `horus.telemetry.snmp.*.<t>.<router_id>`,
  `horus.telemetry.routeros.*.<t>.<router_id>`, `horus.telemetry.wireguard.peer_status.<t>.<server_id>`.
- El último snapshot de topics estado se cachea en memoria; si no hay valor caliente, el gateway lo pide al servicio por
  REST interno (`GET /routers/{id}/metrics/live`).
- Cada réplica se suscribe de forma independiente ⇒ escalado horizontal sin estado compartido (salvo Valkey de sesiones).
- Permiso NATS del usuario del gateway: sólo **subscribe** a esos subjects; ningún publish ([`security.md`](./security.md)).

### 4.9 Límites por conexión

| Límite | Valor |
| --- | --- |
| Conexiones por sesión | 5 ([`security.md`](./security.md)); la 6ª cierra la más antigua con `4429`. Kiosco: 2 |
| Conexiones por tenant | 500 (cuota ajustable por plataforma) |
| Suscripciones por conexión | 50 |
| Topics `router.<id>.metrics` por conexión | 20 |
| Mensaje entrante máximo | 16 KiB |
| Mensajes entrantes | 20/s (ráfaga 50) → `4429` |
| Tiempo hasta el primer `subscribe` | 30 s |
| Vida máxima | 12 h (cierre `1001` para rebalancear) |
| Objetivo por réplica | 10.000 conexiones (v1 espera ~50 usuarios + pantallas NOC de varios ISP; validar en pruebas de carga) |

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
│   ├── devices/v1/          # InventoryService (ListPollingTargets, GetPollingTarget, GetRouters, ListCustomers, ResolveCustomers)
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
- **Tenant explícito**: toda petición de un recurso de negocio lleva `string tenant_id = 1` en el **mensaje** (no sólo
  en metadata), y toda respuesta/elemento lo devuelve. El interceptor de `packages/go/authz` comprueba que, si la
  llamada va en nombre de un usuario, `tenant_id` ∈ `tnt` del JWT; si va en nombre de un servicio, el método debe estar
  autorizado para "todos los tenants" (p. ej. `ListPollingTargets` del poller) y lo declara en su política. Lint de
  `buf`/CI: un `*Request` de los paquetes de negocio sin campo `tenant_id` falla salvo excepción anotada.

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
  string tenant_id = 7;            // el poller sirve a todos los tenants; cada objetivo sabe el suyo
  RouterOsApiCredentials routeros_api = 8; // D10; opcional
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
| `x-horus-tenant` | Copia del `tenant_id` del mensaje, **sólo** para logs/trazas; la autorización usa el campo del mensaje |

Interceptor común (`packages/go/authz`, [`conventions.md`](./conventions.md)) que valida el JWT, extrae un `Actor`
(`type`, `id`, `sid`) y el `TenantScope` de la petición al `context.Context`; lo reutilizan logs, auditoría, los
repositorios (que exigen un `TenantScope` para cualquier consulta de negocio) y el sobre de eventos.

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
