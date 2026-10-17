# Contrato de integración Horus Flow ↔ ArgosISP

Decisión en [ADR-0030](../adr/0030-integracion-de-ecosistema-sin-dependencias.md).

Versión del contrato: **1.5.0-propuesta** · Estado: lado Horus implementado H1–H11 (§5.2); lado ArgosISP en curso (A1–A9, §5.1).

Cambios de 1.5.0 (menor, solo precisiones; ningún campo cambia). Motivo: al implementar H10–H11 en Horus aparecieron
huecos que obligaban a adivinar; se elige lo más seguro y se escribe:

- §1.4: `base_url` es el **origen** del sistema que canjea (`https://host[:puerto]`, sin ruta); cada lado añade
  `/api/v1` (se tolera que venga con `/api/v1`). Si el panel y la API de un sistema están en orígenes distintos (Argos
  publica `panel.` y `api.`), el enlace lleva el origen de la API en `?api=`: `https://<panel>/conectar?api=<origen>#<código>`;
  sin `api`, la API está en el origen del enlace.
- §1.4: repetir **la misma** petición de canje con el mismo código antes de que caduque devuelve la misma respuesta
  (idempotencia ante cortes de red); cualquier otra petición con un código usado, vencido o desconocido → `410`.
  Generar un enlace nuevo anula el anterior sin usar. Además del conector, el canal de webhooks hacia el otro sistema
  también queda apagado. El token del canje se emite a nombre de quien generó (o canjeó) el enlace y con la plantilla
  de §1.1/§1.2; un canje nuevo revoca el token del anterior.
- §1.5: `destino.id_externo` es siempre un id que el destino reconoce: para `cliente` y `servicio`, el id de ArgosISP;
  para `router` y `hallazgo`, el id de Horus (el que Argos recibe en los webhooks de §3.4).
- §1.5: la `url` del ticket es `https://<panel del destino>/sso/entrar#<ticket>` (su origen puede no ser el de la API);
  el origen solo la abre si es https, sin credenciales, con esa ruta y el ticket en el fragmento (nunca en la consulta).
- §1.5: si el conector se preparó con un enlace, el destino solo acepta tickets pedidos con el token de ese canje. La
  primera vinculación exige iniciar sesión (o re-autenticarse) en el destino **después** del canje; una sesión ya
  abierta en el navegador no basta.

Cambios de 1.4.0 (menor, solo aditivos). Reúne en una sola versión dos cambios que se hicieron en paralelo sobre
1.2.0: el enlace de conexión y el autologin (§1.4, §1.5, A8–A9, H10–H11), agregados a la rama de la PR de 1.2.0
después de su merge y que no habían llegado a Horus, y las precisiones de 1.3.0. No cambia ningún campo existente.

Cambios de 1.3.0 (menor). Motivo: al implementar H9 en Horus, el ejemplo de §3.5 usaba un estado
(`compromised`) que no existe; el estado de seguridad es el mismo de §3.1 y de `customer.security_state_changed`
(§3.4). Nadie lo consumía aún. Además se precisan, sin cambiar campos, los casos de un servicio con varias IPs:

- §3.5: `state` es `clean` | `suspected` | `infected` | `mitigated` (como §3.1); por servicio con varias IPs (IPv4
  y prefijo IPv6) se devuelve la de peor estado; por IP se añade el `external_service_id` de la identidad vigente.
- §3.1: con `external_service_id`, si el servicio tiene varias IPs, la del cliente visto más recientemente.

Cambios de 1.2.0 (menor, solo aditivos). Motivo: ArgosISP es multiempresa (un ISP puede operar varios NIT, cada servicio pertenece a una empresa) y sus pantallas parten del servicio, no de la IP:

- §2.1: cada resultado del resolver añade `empresa` (`id`, `nombre`, `nit`) del servicio.
- §2.3: los payloads de los webhooks Argos → Horus llevan `empresa_id` cuando el evento es de un servicio.
- §2.4: Horus guarda `external_company_id` y `company_name` en la identidad externa.
- §3.1: la consulta 360 acepta `external_service_id` en lugar de `address` (uno de los dos); `external` añade `company_id` y `company_name`.
- §3.4: `external` de los webhooks Horus → Argos añade `company_id`.
- §3.5 (nuevo): estado de seguridad por lotes.
- §1.4 (nuevo): **enlace de conexión** de un solo uso que cualquiera de los dos sistemas genera; al consumirlo en el otro se intercambian, en un paso, los tokens y los secretos de webhook de ambos sentidos.
- §1.5 (nuevo): **autologin** de un sistema al otro con un ticket de un solo uso emitido de servidor a servidor; la primera vez el usuario vincula su cuenta iniciando sesión en el destino. El destino aplica siempre su propio ACL.

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
  `customers.read`, `traffic.customer.read`, `security.findings.read`, `snmp.read` y, desde 1.2.0,
  `sso.issue` (§1.5).
  Caducidad por defecto 1 año, revocable, con "último uso". Se muestra una sola vez.
- Argos lo guarda cifrado y lo envía como `Authorization: Bearer hf_pat_…` a
  `https://<horus>/api/v1/…`.

### 1.2 Argos → Horus (Horus lee de Argos)

- En Argos, token `atk_…` con `alcances.permisos = ["ip.resolver", "clientes.ver", "servicios.ver", "sso.emitir"]`
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

### 1.4 Enlace de conexión (ambos sistemas)

Sustituye el copiado manual de URL, token y secreto (§1.1–§1.3), que se mantiene como alternativa.

1. En el sistema **A** (Argos o Horus), un administrador pulsa *Generar enlace de conexión*. A crea un
   código aleatorio de 32 bytes, guarda solo su SHA-256, con caducidad de **15 min** y **un solo uso**, y
   muestra una sola vez:
   `https://<A>/conectar#<codigo>` (el código va en el fragmento, que el navegador no envía en peticiones
   ni deja en registros de servidores).
2. En el sistema **B**, un administrador pega el enlace en *Conectar con enlace*. B (servidor a servidor):
   ```
   POST https://<A>/api/v1/<prefijo>/conexion/canjear          # Argos: /integraciones/horus · Horus: /integrations/argosisp
   # <A> = origen de la API de A: el del enlace o, si viene, el de ?api= (1.5.0)
   { "codigo": "<codigo>",
     "sistema": "horus" | "argosisp",
     "base_url": "https://<B>",                                # origen de la API de B; A añade /api/v1 (1.5.0)
     "token": "<token de B para que A lea de B>",              # B lo emite en este momento con la plantilla de §1.1/§1.2
     "webhook": { "url": "<receptor de B>", "secreto": "<secreto que B generó>" } }
   → 200 { "token": "<token de A para que B lea de A>",
           "webhook": { "url": "<receptor de A>", "secreto": "<secreto que A generó>" },
           "conector_id": "<id opaco del conector en A>" }
   → 410 código vencido o ya usado · 422 datos inválidos
   ```
3. Cada lado guarda lo recibido cifrado (de solo escritura) y deja el conector **apagado** hasta que un
   administrador lo active. Solo `https` (salvo el flag de laboratorio de cada sistema). El canje queda
   auditado en ambos lados; generar y canjear piden el permiso de configurar la integración.
4. (1.5.0) La misma petición repetida con el mismo código antes de que caduque recibe la misma respuesta; cualquier
   otra → `410`. Un enlace nuevo anula el anterior sin usar. El canal de webhooks hacia el otro sistema también queda
   apagado. En Horus, generar y canjear piden además `api_tokens.manage` y `alerts.manage` (el canje emite un
   `hf_pat_` y crea un canal webhook) y re-autenticación reciente.

### 1.5 Autologin entre sistemas (ticket de un solo uso)

Para abrir el detalle del mismo cliente en el otro sistema ("Ver en Horus" / "Ver en ArgosISP") sin volver a
iniciar sesión, **sin** que un sistema dé privilegios en el otro:

1. El usuario, con sesión activa en el origen y el permiso de abrir la integración, pulsa el botón. El
   **servidor** de origen pide el ticket al destino con su token de integración (scope `sso.emitir` /
   `sso.issue`):
   ```
   POST https://<destino>/api/v1/<prefijo>/sso/tickets
   { "usuario": { "id_externo": "u-17", "correo": "ana@isp.gt", "nombre": "Ana López" },
     "destino": { "tipo": "cliente"|"servicio"|"router"|"hallazgo", "id_externo": "812" } }
   → 201 { "url": "https://<destino>/sso/entrar#<ticket>", "expira_en": "…" }   # 60 s, un solo uso
   ```
   El navegador nunca pide el ticket: solo recibe la URL y la abre. (1.5.0) `destino.id_externo`: para `cliente` y
   `servicio`, el id de ArgosISP; para `router` y `hallazgo`, el id de Horus. La `url` apunta al panel del destino; el
   origen solo la abre si es https, sin credenciales, con la ruta `/sso/entrar` y el ticket en el fragmento. Si el
   conector se preparó con un enlace (§1.4), solo vale el token de ese canje.
2. El destino canjea el ticket en su propio servidor (un uso, 60 s, ligado al conector) y busca la
   **vinculación** `(conector, usuario.id_externo) → usuario local`:
   - Sin vinculación: muestra su inicio de sesión normal (con su MFA si lo exige); al entrar, guarda la
     vinculación. Nunca crea usuarios ni vincula solo por coincidir el correo. (1.5.0) La autenticación debe ser
     posterior al canje: una sesión ya abierta en el navegador pide re-autenticarse antes de vincular.
   - Con vinculación: abre una sesión normal del usuario local, con **sus** permisos y alcance (ISP,
     empresa, router), y lo lleva al destino resuelto (`id_externo` → recurso local). Si no tiene permiso
     para ese recurso, ve el error de acceso de siempre.
3. La sesión creada por ticket dura como máximo la sesión normal del destino; acciones sensibles pueden
   pedir reautenticación según las reglas del destino.
4. Auditoría en ambos lados: quién emitió, a quién, recurso, resultado del canje. Cada usuario puede ver y
   quitar sus vinculaciones; un administrador puede revocarlas todas.

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
el portal sin conocer la IP del momento. Con `at`, la identidad del servicio vigente en ese instante. Si el
servicio tiene varias IPs (IPv4 y prefijo IPv6 son clientes distintos en Horus), responde con la del cliente
visto más recientemente (1.3.0).

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
→ 200 { "items": [ { "external_service_id": 812, "address": "100.64.1.20",
                     "state": "clean"|"suspected"|"infected"|"mitigated",
                     "open_findings": 0, "max_severity": "high"?, "since": "…" },
                   { "external_service_id": 999, "address": null, "error": "not_found" } ],
        "meta": { "at": "…" } }
```

Mismo permiso que el bloque `security` de §3.1 (`security.findings.read`; la plantilla ArgosISP ya lo trae). Sin
conector Argos activo, solo se acepta `address`. `state` es el mismo estado de §3.1 y §3.4 (1.3.0). Por servicio
con varias IPs se devuelve la de peor estado y su `address`; por IP, el `external_service_id` de la identidad
vigente (o null). `not_found`: la IP no es de un cliente del ISP o el servicio no tiene identidad vigente. Un
cliente que nunca tuvo hallazgos está `clean` desde que se vio.

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
| A8 | Enlace de conexión: generar, mostrar una vez y canjear (`/integraciones/horus/conexion/canjear`) | §1.4 | en curso |
| A9 | Autologin: emitir tickets para Horus (`sso.emitir`) y canjear los de Horus (`/integraciones/horus/sso/tickets`, `/sso/entrar`) con vinculación de cuentas | §1.5 | en curso |

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
| H7 | Multiempresa: guardar y mostrar la empresa del servicio (`empresa` del resolver → `external_company_id`, `company_name`) y devolverla en `external` (consulta 360 y webhooks) | §2.1, §2.4, §3.1, §3.4 | hecho (rama `claude/sleepy-lovelace-yz0o32`) |
| H8 | Consulta 360 por `external_service_id` (sin IP), con la identidad vigente del servicio | §3.1 | hecho (rama `claude/sleepy-lovelace-yz0o32`) |
| H9 | `POST /api/v1/integrations/security-states`: estado de seguridad por lotes (máx. 500) | §3.5 | hecho (rama `claude/sleepy-lovelace-yz0o32`) |
| H10 | Enlace de conexión: generar en Horus y canjear el de Argos (`/integrations/argosisp/conexion/canjear`) | §1.4 | hecho (rama `claude/sleepy-lovelace-yz0o32`) |
| H11 | Autologin: emitir tickets para Argos (`sso.issue` en la plantilla ArgosISP) y canjear los de Argos con vinculación de cuentas | §1.5 | hecho (rama `claude/sleepy-lovelace-yz0o32`) |
