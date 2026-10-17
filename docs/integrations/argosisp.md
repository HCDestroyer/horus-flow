# Contrato de integración Horus Flow ↔ ArgosISP

Decisión en [ADR-0030](../adr/0030-integracion-de-ecosistema-sin-dependencias.md). Espejo en ArgosISP:
`.claude/integration/contratos/horus.md`. Cualquier cambio se hace con una PR en cada repositorio,
enlazadas entre sí.

Estado: **v1 propuesta** (pendiente de implementación en ambos lados).

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

`POST https://<horus>/api/v1/integrations/argosisp/webhook` (firmado, §1.3). Eventos:

| `evento` | Payload mínimo | Efecto en Horus |
| --- | --- | --- |
| `asignacion.cambiada` | `servicio_id`, `ipv4?`, `prefijo_v6?`, `anterior?` | Cierra la vigencia anterior y re-resuelve |
| `sesion.iniciada` / `sesion.terminada` | `servicio_id`, `ipv4?`, `prefijo_enlace_v6?`, `prefijo_delegado_v6?` | Abre/cierra vigencia de la IP |
| `cliente.actualizado` | `cliente_id` | Refresca nombre/código en las identidades vigentes |
| `servicio.estado_cambiado` | `servicio_id`, `estado` | Refresca el estado mostrado (activo, limitado, suspendido, retirado) |

Cuerpo: `{ "id": "...", "evento": "...", "ocurrido_en": "ISO-8601", "payload": { ... } }`.

### 2.4 Qué guarda Horus

`devices.customer_external_identity`: `tenant_id`, `customer_id`, `system = 'argosisp'`,
`external_customer_id`, `external_service_id`, `display_name`, `code`, `service_state`, `plan`,
`valid_from`, `valid_to` (NULL = vigente), `synced_at`, `stale`. Con identidad vigente y
`alias_source <> 'manual'`: `alias = display_name`, `alias_source = 'argosisp'`.

## 3. Argos consume de Horus (consumo, salud, seguridad)

Todas con `Authorization: Bearer hf_pat_…`; el ISP sale del token.

### 3.1 Consulta 360 por IP

```
POST /api/v1/integrations/ip-lookup
{ "address": "100.64.1.20", "at": "2026-10-17T10:00:00Z"?, "include": ["consumption","health","security"]? }
→ 200 {
  "customer": { "id": "0192…", "address": "100.64.1.20", "alias": "Ferretería López", "kind": "commercial",
                "kind_source": "scoring", "status": "active", "first_seen": "…", "last_seen": "…",
                "site": { "id": "…", "name": "Nodo Centro" } },
  "external": { "system": "argosisp", "customer_id": 455, "service_id": 812, "code": "C-000455",
                "service_state": "activo", "synced_at": "…", "stale": false },
  "consumption": { "last_24h": { "up_bytes": 0, "down_bytes": 0 }, "last_7d": {…}, "last_30d": {…},
                   "peak_down_bps_24h": 0, "top_services_7d": [ { "name": "YouTube", "bytes": 0 } ] },
  "health": { "router": { "id": "…", "name": "…", "status": "online", "since": "…" },
              "exporter": { "status": "exporting", "last_flow_at": "…" },
              "last_seen_minutes": 2 },
  "security": { "state": "suspected", "open_findings": 1,
                "findings": [ { "id": "…", "kind": "outbound_scanning", "severity": "high", "opened_at": "…", "summary": "…" } ] } }
→ 404 si la IP no es de un prefijo de clientes del ISP o nunca se vio.
```

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

Cuerpo: `{ "id": "…", "event": "…", "occurred_at": "ISO-8601", "tenant": "<slug>", "data": { … } }`.

## 4. Versionado

- Rutas de Horus bajo `/api/v1`; campos nuevos son aditivos. Un cambio incompatible crea `/v2` y
  convive 6 meses.
- Los receptores ignoran campos y eventos desconocidos.
