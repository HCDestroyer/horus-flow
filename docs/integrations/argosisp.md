# Contrato de integración Horus Flow ↔ ArgosISP

Decisión en [ADR-0030](../adr/0030-integracion-de-ecosistema-sin-dependencias.md).

Versión del contrato: **1.2.0-propuesta** · Estado: lado Horus 1.1.0 implementado (H1–H6, §5.2); lado ArgosISP en curso (A1–A7, §5.1); pedidos nuevos H7–H9 solicitados.

Cambios de 1.2.0 (menor, solo aditivos). Motivo: ArgosISP es multiempresa (un ISP puede operar varios NIT, cada servicio pertenece a una empresa) y sus pantallas parten del servicio, no de la IP:

- §2.1: cada resultado del resolver añade `empresa` (`id`, `nombre`, `nit`) del servicio.
- §2.3: los payloads de los webhooks Argos → Horus llevan `empresa_id` cuando el evento es de un servicio.
- §2.4: Horus guarda `external_company_id` y `company_name` en la identidad externa.
- §3.1: la consulta 360 acepta `external_service_id` en lugar de `address` (uno de los dos); `external` añade `company_id` y `company_name`.
- §3.4: `external` de los webhooks Horus → Argos añade `company_id`.
- §3.5 (nuevo): estado de seguridad por lotes.

Cambios de 1.1.0 (menor, aditivos salvo la URL del receptor, que aún no usaba nadie):

- §2.3: la URL del receptor de Horus lleva el id opaco del conector,
  `POST /api/v1/integrations/argosisp/webhook/{connector_id}` (Horus es multi-ISP: el id identifica qué secreto
  verifica la firma; no es el id del ISP). Horus muestra la URL completa junto con el secreto.
- §3.1: `external` añade `display_name` y `plan`; la respuesta añade `meta.omitted` (bloques no devueltos y motivo:
  `permission`, `unavailable`, `not_requested`, `not_configured`) y `meta.at`; `customer` añade `alias_source`.
- §3.2: cada elemento puede traer `error` (`not_found`) y, por servicio, `addresses` (IPs sumadas); la respuesta
  añade `meta`. Límite del periodo: 400 días.
- §3.4: `data.finding` incluye `id`, `state`, `opened_at` y `reopened`; `data.router` y `data.service` incluyen
  `previous_status`. Los ids de `external` solo si el cliente tiene identidad vigente; `customer.address` y
  `customer.alias` solo si el canal tiene `include_personal_data`. Prueba de conexión: `event: "ping"`; envío de
  prueba: `event: "test"`.

## Gobierno: fuente de verdad compartida

- Este texto es **la fuente de verdad compartida**. Existe con el mismo contenido en los dos
  repositorios: aquí y en ArgosISP como `.claude/integration/contratos/horus.md`. La línea de
  versión de arriba identifica qué copia está al día.
- **Cada sistema solo toca su propio código.** Horus no escribe código de Argos ni Argos de Horus.
  Lo que uno necesita del otro se **solicita** en la sección de solicitudes (§5) y el otro lo
  implementa en su repositorio.
- **Cambiar el contrato** = dos PRs enlazadas, una por repositorio, que solo cambian este archivo
  (y su espejo) más el código del lado de quien la abre. Se mergean cuando ambos lados la aprueban;
  la versión sube (semver: menor para campos o eventos nuevos, mayor para cambios incompatibles).
- Cada lado marca en §5 el estado de lo que le pidieron: `solicitado` → `en curso` → `hecho`
  (con el enlace a su PR).

## 0. Principios

| Regla | Detalle |
| --- | --- |
| Independencia | Cada sistema funciona completo sin el otro. Sin código de conexión, el conector está apagado. |
| Degradación | Si el otro no responde: último dato conocido marcado `stale`; reintento con espera exponencial (máx. 15 min). Nunca bloquea ingesta, detección, AAA, facturación ni corte. |
| Mínimo privilegio | Cada token solo lleva los permisos de esta integración y un solo ISP (tenant). |
| IPs fuera de URLs | En Horus las IPs de clientes van en el cuerpo (`POST`). Se pide a Argos lo mismo (§2.1). |
| Idempotencia | Webhooks con `id` estable; el receptor deduplica. Entrega al menos una vez. |
| Auditoría | Cada sistema audita las lecturas de datos personales que hace el otro (actor = token). |

## 1. Códigos de conexión y firma

### 1.1 Horus → Argos (Argos lee de Horus)

- En Horus, *Administración → Integraciones → ArgosISP → Generar código de conexión*: token
  `hf_pat_<32B base62>` del ISP, plantilla de permisos **ArgosISP**:
  `customers.read`, `traffic.customer.read`, `security.findings.read`, `snmp.read`.
  Caducidad por defecto 1 año, revocable, con "último uso". Se muestra una sola vez.
- Argos lo guarda cifrado y lo envía como `Authorization: Bearer hf_pat_…` a
  `https://<horus>/api/v1/…`.

### 1.2 Argos → Horus (Horus lee de Argos)

- En Argos, token `atk_…` con `alcances.permisos = ["ip.resolver", "clientes.ver", "servicios.ver"]`
  (y, si aplica, `routers`/`zonas` del ISP). Horus lo guarda cifrado, de solo escritura, junto con
  la URL base de Argos.

### 1.3 Webhooks firmados (ambos sentidos)

```
POST <url del receptor>
Content-Type: application/json
X-Webhook-Id: <uuid o id estable del evento>       # deduplicación
X-Webhook-Timestamp: <unix segundos>
X-Webhook-Signature: v1=<hex(HMAC-SHA256(secreto, timestamp + "." + cuerpo))>
```

El receptor rechaza firmas inválidas y timestamps con más de 5 min de desfase; responde `2xx`
rápido y procesa en segundo plano. Reintentos del emisor: 1 min, 5 min, 30 min, 2 h, 12 h.
El secreto lo genera **el receptor** y se copia al emisor junto con la URL.

## 2. Horus consume de Argos (identidad del cliente)

### 2.1 Resolver IP → servicio y cliente

Preferido (a implementar en Argos, IP en el cuerpo y por lotes):

```
POST /integraciones/horus/resolver
{ "ips": ["100.64.1.20", "2800:abcd:1:2::1"] }        # máx. 500
→ 200 { "resultados": [
    { "ip": "100.64.1.20", "servicio_id": 812, "cliente_id": 455, "prefijo": "100.64.1.20/32",
      "cliente": { "nombre": "Ferretería López", "codigo": "C-000455", "tipo": "empresa" },
      "servicio": { "user_ppp": "flopez", "plan": "Fibra 100", "estado": "activo", "router_id": 3 },
      "empresa": { "id": 2, "nombre": "Fibra Sur", "nit": "1234567K" },
      "desde": "2026-10-17T08:12:00Z" },
    { "ip": "2800:abcd:1:2::1", "error": "no_encontrada" } ] }
```

Compatibilidad: si el anterior no existe (404), Horus usa los endpoints actuales
`GET /ip/:ip` → `{servicio_id, cliente_id, prefijo}`, `GET /clientes/:id` y `GET /servicios/:id`.

### 2.2 Cuándo resuelve Horus

1. Cliente descubierto (`horus.devices.customer.discovered`).
2. Webhook de Argos (§2.3) para la IP o el servicio afectado.
3. Barrido periódico de clientes activos con identidad de más de 6 h (lotes de 500, limitado a
   10 req/s por ISP).
4. A demanda desde la ficha del cliente ("Actualizar desde ArgosISP").

### 2.3 Webhooks Argos → Horus

`POST https://<horus>/api/v1/integrations/argosisp/webhook/{connector_id}` (firmado, §1.3; la URL completa la muestra
Horus junto con el secreto). Eventos:

| `evento` | Payload mínimo | Efecto en Horus |
| --- | --- | --- |
| `asignacion.cambiada` | `servicio_id`, `ipv4?`, `prefijo_v6?`, `anterior?` | Cierra la vigencia anterior y re-resuelve |
| `sesion.iniciada` / `sesion.terminada` | `servicio_id`, `ipv4?`, `prefijo_enlace_v6?`, `prefijo_delegado_v6?` | Abre/cierra vigencia de la IP |
| `cliente.actualizado` | `cliente_id` | Refresca nombre/código en las identidades vigentes |
| `servicio.estado_cambiado` | `servicio_id`, `estado` | Refresca el estado mostrado (activo, limitado, suspendido, retirado) |

Cuerpo: `{ "id": "...", "evento": "...", "ocurrido_en": "ISO-8601", "payload": { ... } }`. Desde 1.2.0, el
`payload` de los eventos de un servicio incluye `empresa_id` (empresa del servicio en ArgosISP).

### 2.4 Qué guarda Horus

`devices.customer_external_identity`: `tenant_id`, `customer_id`, `system = 'argosisp'`,
`external_customer_id`, `external_service_id`, `external_company_id`, `company_name`, `display_name`,
`code`, `service_state`, `plan`, `valid_from`, `valid_to` (NULL = vigente), `synced_at`, `stale`. Con identidad vigente y
`alias_source <> 'manual'`: `alias = display_name`, `alias_source = 'argosisp'`.

## 3. Argos consume de Horus (consumo, salud, seguridad)

Todas con `Authorization: Bearer hf_pat_…`; el ISP sale del token.

### 3.1 Consulta 360 por IP

```
POST /api/v1/integrations/ip-lookup
{ "address": "100.64.1.20", "at": "2026-10-17T10:00:00Z"?, "include": ["consumption","health","security"]? }
# o, desde 1.2.0, por servicio de Argos (uno de los dos; con ambos → 422):
{ "external_service_id": 812, "at"?: "…", "include"?: [...] }
→ 200 {
  "customer": { "id": "0192…", "address": "100.64.1.20", "alias": "Ferretería López", "kind": "commercial",
                "kind_source": "scoring", "status": "active", "first_seen": "…", "last_seen": "…",
                "site": { "id": "…", "name": "Nodo Centro" } },
  "external": { "system": "argosisp", "customer_id": 455, "service_id": 812, "code": "C-000455",
                "service_state": "activo", "company_id": 2, "company_name": "Fibra Sur",
                "synced_at": "…", "stale": false },
  "consumption": { "last_24h": { "up_bytes": 0, "down_bytes": 0 }, "last_7d": {…}, "last_30d": {…},
                   "peak_down_bps_24h": 0, "top_services_7d": [ { "name": "YouTube", "bytes": 0 } ] },
  "health": { "router": { "id": "…", "name": "…", "status": "online", "since": "…" },
              "exporter": { "status": "exporting", "last_flow_at": "…" },
              "last_seen_minutes": 2 },
  "security": { "state": "suspected", "open_findings": 1,
                "findings": [ { "id": "…", "kind": "outbound_scanning", "severity": "high", "opened_at": "…", "summary": "…" } ] } }
→ 404 si la IP no es de un prefijo de clientes del ISP o nunca se vio.
```

Con `external_service_id`, Horus usa la identidad vigente de ese servicio (§2.4) y responde con su IP
actual; 404 si el servicio no tiene identidad vigente. Así Argos consulta desde la ficha del servicio o
el portal sin conocer la IP del momento.

### 3.2 Consumo por lotes (validar consumo de servicios)

```
POST /api/v1/integrations/consumption
{ "from": "2026-10-01T00:00:00Z", "to": "2026-11-01T00:00:00Z", "granularity": "day"|"total",
  "items": [ { "address": "100.64.1.20" }, { "external_service_id": 812 } ] }   # máx. 500
→ 200 { "items": [ { "address": "…", "external_service_id": 812, "up_bytes": 0, "down_bytes": 0,
                     "series": [ { "day": "2026-10-01", "up_bytes": 0, "down_bytes": 0 } ] } ] }
```

`external_service_id` usa las vigencias de §2.4 (suma todas las IPs que tuvo el servicio en el
periodo). Sin conector Argos activo, solo se acepta `address`.

### 3.3 Salud de la red

`GET /api/v1/routers/status` y `GET /api/v1/infra-services` (ya existen) aceptan el token.

### 3.4 Webhooks Horus → Argos

Canal de notificación `webhook` (Alertas) con URL y secreto de Argos. Eventos:

| `event` | Cuándo | Payload (sin IP en la URL; sí en el cuerpo) |
| --- | --- | --- |
| `finding.opened` / `finding.resolved` | Hallazgo de un cliente | `finding` (kind, severity, summary, window), `customer` (id, address, alias), `external` (customer_id, service_id) |
| `customer.security_state_changed` | Cambia el estado de seguridad | `state`, `previous_state`, `open_findings`, `customer`, `external` |
| `router.status_changed` | Router online/offline/degradado | `router` (id, name, status, reason) |
| `infra_service.state_changed` | Servicio de infraestructura (IPTV, HTTP…) cambia | `service` (id, name, status, reason) |

Cuerpo: `{ "id": "…", "event": "…", "occurred_at": "ISO-8601", "tenant": "<slug>", "data": { … } }`. Desde 1.2.0,
`external` incluye `company_id` cuando el cliente tiene identidad vigente.

### 3.5 Estado de seguridad por lotes

Para mostrar una columna "Seguridad" en las listas de servicios de Argos sin una consulta por fila:

```
POST /api/v1/integrations/security-states
{ "items": [ { "external_service_id": 812 }, { "address": "100.64.1.20" } ] }   # máx. 500
→ 200 { "items": [ { "external_service_id": 812, "address": "100.64.1.20", "state": "clean"|"suspected"|"compromised",
                     "open_findings": 0, "max_severity": "high"?, "since": "…" },
                   { "external_service_id": 999, "error": "not_found" } ],
        "meta": { "at": "…" } }
```

Mismo permiso que §3.1 (`security.findings.read`). Sin conector Argos activo, solo se acepta `address`.

## 4. Versionado

- Rutas de Horus bajo `/api/v1`; campos nuevos son aditivos. Un cambio incompatible crea `/v2` y
  convive 6 meses.
- Los receptores ignoran campos y eventos desconocidos.

## 5. Solicitudes

### 5.1 Horus solicita a ArgosISP

| # | Solicitud | Contrato | Estado |
| --- | --- | --- | --- |
| A1 | `POST /integraciones/horus/resolver` por cuerpo y por lotes (máx. 500), con cliente (`nombre`, `codigo`, `tipo`), servicio (`user_ppp`, `plan`, `estado`, `router_id`), `empresa` (1.2.0) y `desde` | §2.1 | en curso |
| A2 | Evento `cliente.actualizado` en el catálogo (cambio de nombre, tipo o código) | §2.3 | en curso |
| A3 | Código de cliente estable (`codigo`; p. ej. `C-000455` derivado del id o un campo propio) | §2.1 | en curso |
| A4 | Worker de webhooks firmados hacia Horus (grupo `horus` en `argos:eventos`) con reintentos | §1.3, §2.3 | en curso |
| A5 | Usuario técnico "Horus" y token `atk_` con `ip.resolver`, `clientes.ver`, `servicios.ver` | §1.2 | en curso |
| A6 | Conector Horus: guardar URL y `hf_pat_` cifrado; ficha de servicio con consumo, salud y seguridad (§3.1); consumo mensual por servicio (§3.2) | §3 | en curso |
| A7 | Receptor `POST /integraciones/horus/webhook` firmado; eventos internos para `notify` y la línea de tiempo | §3.4 | en curso |

Reglas para Argos: el conector viene apagado; nada crítico (AAA, facturación, corte, portal) espera
a Horus; Horus no decide cortes (solo `evaluarEstadoServicio` decide el estado del servicio).

### 5.2 ArgosISP solicita a Horus

| # | Solicitud | Contrato | Estado |
| --- | --- | --- | --- |
| H1 | Tokens de integración `hf_pat_` con plantilla "ArgosISP" (código de conexión) | §1.1 | hecho (rama `claude/ecosistema-argos`) |
| H2 | `POST /api/v1/integrations/ip-lookup` (consulta 360) | §3.1 | hecho (rama `claude/ecosistema-argos`) |
| H3 | `POST /api/v1/integrations/consumption` por IP y por servicio de Argos | §3.2 | hecho (rama `claude/ecosistema-argos`) |
| H4 | `GET /routers/status` e `/infra-services` aceptan `hf_pat_` | §3.3 | hecho (rama `claude/ecosistema-argos`) |
| H5 | Canal `webhook` firmado con los eventos de §3.4 | §3.4 | hecho (rama `claude/ecosistema-argos`) |
| H6 | Receptor `POST /api/v1/integrations/argosisp/webhook/{connector_id}` y conector de identidad con vigencia | §2.2–§2.4 | hecho (rama `claude/ecosistema-argos`) |
| H7 | Multiempresa: guardar y mostrar la empresa del servicio (`empresa` del resolver → `external_company_id`, `company_name`) y devolverla en `external` (consulta 360 y webhooks) | §2.1, §2.4, §3.1, §3.4 | solicitado |
| H8 | Consulta 360 por `external_service_id` (sin IP), con la identidad vigente del servicio | §3.1 | solicitado |
| H9 | `POST /api/v1/integrations/security-states`: estado de seguridad por lotes (máx. 500) | §3.5 | solicitado |
