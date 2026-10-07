# Horus Flow — Catálogo de servicios

> Estado: **propuesta Sprint 0** · Dueño: Agente 1 (Arquitecto de sistema)
>
> Arquitectura general: [architecture.md](architecture.md) · Decisión de granularidad:
> [ADR-0014](adr/0014-granularidad-de-microservicios-en-el-mvp.md) · Modelo de datos detallado:
> [database.md](database.md) · Contrato REST: [api.md](api.md) · Contrato de eventos:
> [events.md](events.md).

Los nombres de evento de este documento son **de alto nivel** (dominio + entidad + hecho). El
contrato normativo (subject exacto, stream, payload, versión) es del Agente 3 en
[events.md](events.md); si hay diferencia, manda `events.md`.

---

## 1. Cuestionando el plan: qué existe en el MVP y qué se fusiona

El plan original ([vision.md](vision.md)) menciona 14 piezas: api-gateway, auth, device,
network, wireguard, snmp, flow collector, security, traffic-intelligence, reputation, detection,
analytics, alerts, reporting. Para un equipo pequeño y un MVP que debe validar la arquitectura
(vision §14), **cada proceso adicional cuesta**: pipeline CI, imagen, health checks, dashboards,
alertas, versión de contratos y un salto de red más. Se separa solo cuando hay una razón concreta:
**escala distinta, ciclo de vida distinto, privilegios distintos o equipo distinto.**

### 1.1 Decisiones

| Plan original | Propuesta | Justificación |
| --- | --- | --- |
| `network-service` + `wireguard-service` | **Un solo servicio `wireguard`** (control) + **`wireguard-agent`** (mismo binario, modo agente) | "network-service" no tiene un dominio propio definido: la topología (sitios, IPs de routers) es de `devices`, y la IPAM de túneles es inseparable de los peers WG. La separación real que sí importa es **por privilegios**: la parte que toca el kernel (`CAP_NET_ADMIN`, host network) se aísla en el agente; la API y PostgreSQL quedan en un contenedor sin privilegios. |
| `security-service` + `reputation-service` + `detection-service` | **Un servicio `detection`** con módulos internos `reputation`, `correlation`, `scoring` | Los tres comparten entrada (tráfico enriquecido + reputación), comparten salida (hallazgos → alertas) y el pipeline `Reputation → Behavior → Traffic → Detection` es una sola cadena de correlación: separarlo en 3 procesos convierte una función en 3 saltos de red. "Security" no es un servicio sino una **categoría de hallazgos**. Se separa `reputation` cuando la ingestión de feeds de reputación (descargas grandes, actualizaciones frecuentes) tenga un ciclo de vida o carga distinta, o cuando otro servicio (p. ej. `flows` para marcar en ingesta) necesite consultarla. |
| `analytics` + `reporting` | **Un servicio `analytics`** con un rol *worker* `reporting` (mismo binario, `--role=reporting-worker`) | Ambos leen las mismas tablas publicadas de ClickHouse con las mismas consultas. La generación de PDF/Excel es CPU-intensiva y lenta, por eso corre como **proceso separado del mismo binario**, no como servicio con código separado. Se separa en `services/reporting/` si la plantilla de reportes crece a dominio propio (programación, suscripciones, distribución). |
| `flow collector` | **`flows`** con dos roles: `collector` y `ingester` | Escalan distinto: el collector está atado a UDP y a la afinidad por exportador; el ingester escala por CPU y por throughput de ClickHouse. Mismo código de modelo de flujo. |
| `traffic-intelligence` | Se mantiene, pero **no está en el camino caliente** de los flujos | Dueño del catálogo de clasificación; publica snapshots versionados que el ingester de `flows` carga en memoria ([ADR-0015](adr/0015-enriquecimiento-de-flujos-en-ingesta.md)). Evita un salto NATS extra de cientos de miles de mensajes/s. |
| `alerts` | Se mantiene separado | Ciclo de vida propio (reglas, silencios, escalado, canales externos), consume de todos los dominios, y debe seguir funcionando cuando el plano analítico falla. |
| `auth` | Se mantiene; **incluye la auditoría** en v1 | La auditoría necesita identidad de usuario y comparte criterios de retención/seguridad con auth. Se puede extraer después. |
| `snmp` | Se mantiene; **incluye ICMP y el cálculo de estado observado** del router | Quien sondea es quien sabe si el router responde; meter el estado en otro servicio obligaría a reenviar cada sondeo. |

### 1.2 Resultado: unidades desplegables por fase

| Fase | Sprint | Desplegables nuevos | Total procesos |
| --- | --- | --- | ---: |
| MVP plataforma | 1–2 | api-gateway, auth | 2 |
| MVP inventario | 3 | devices | 3 |
| MVP WireGuard | 4 | wireguard, wireguard-agent | 5 |
| MVP SNMP (**fin del MVP técnico**) | 5 | snmp | 6 |
| Flujos | 6 | flows-collector, flows-ingester | 8 |
| Clasificación | 7 | traffic-intelligence | 9 |
| Seguridad | 8 | detection | 10 |
| Analítica | 9 | analytics | 11 |
| Detección res./com. | 10 | — (módulo `scoring` en detection) | 11 |
| Alertas | 11 | alerts | 12 |
| Reportes | 12 | analytics `reporting-worker` | 13 |

Carpetas en `services/`: se mantienen los nombres acordados. `services/reputation/` y
`services/reporting/` **no se crean** hasta que se separen; mientras tanto viven como
`services/detection/internal/reputation/` y `services/analytics/internal/reporting/`. El
`wireguard-agent` vive en `services/wireguard/cmd/wireguard-agent/`.

> **Punto a resolver con el Agente 5 (roadmap):** el Sprint 3 pide un dashboard
> online/offline/warning/critical, pero el estado observado nace con `snmp` en el Sprint 5. Ver
> [Q7](open-questions/architecture.md#q7). Propuesta: adelantar el sondeo **ICMP** (sin SNMP) al
> Sprint 3 como primera pieza del servicio `snmp`, o mostrar `unknown` hasta el Sprint 5.

> **Punto a resolver:** las alertas nacen en el Sprint 11, pero desde el Sprint 5 existe
> `router.offline`. Propuesta: entre los Sprints 5 y 11, el estado se notifica solo en la UI vía
> WebSocket y Prometheus/Alertmanager cubre alertas de infraestructura; no se adelanta `alerts`.

---

## 2. Convenciones comunes a todos los servicios

| Aspecto | Convención |
| --- | --- |
| Puertos | `8080` HTTP (REST público + `/healthz` `/readyz`), `9090` gRPC interno, `9100` `/metrics` Prometheus. |
| Base de datos | Esquema PostgreSQL propio `<servicio>` y usuario propio; sin `GRANT` sobre esquemas ajenos. Migraciones con comando `<svc> migrate`. |
| Eventos de salida | Siempre vía **outbox** si el servicio tiene PostgreSQL ([ADR-0016](adr/0016-transactional-outbox.md)). Telemetría (snmp, flows) publica directo. |
| Eventos de entrada | Consumers durables con nombre `<servicio>-<propósito>`; idempotentes por `event_id`. |
| Autorización | Fina en el servicio, con permisos del JWT (`recurso.accion`). |
| IDs / tiempos | UUIDv7; UTC (`timestamptz`, `DateTime64(3,'UTC')`). |
| Tenancy | Toda entidad raíz con `organization_id`. |
| Observabilidad | OTel traces + métricas RED + logs JSON a stdout ([observability.md](observability.md)). |

Leyenda de estado: **Stateless** = réplicas libres detrás de balanceo; **Singleton** = una sola
instancia activa; **Sharded** = N instancias que se reparten trabajo con *leases*.

---

## 3. Fichas de servicio

### api-gateway

| Campo | Valor |
| --- | --- |
| Responsabilidad | Punto único de entrada del frontend. AuthN de requests, autorización gruesa, enrutamiento a servicios, rate limiting, WebSocket fan-out, CORS, trazas. Detalle en [architecture.md §7](architecture.md#7-api-gateway). |
| Dueño de datos | Ninguno persistente. Usa Redis (claves `ratelimit:*`, `session_revoked:*` como caché). |
| API pública | `/api/v1/*` (proxy), `/api/v1/ws` (WebSocket). |
| gRPC | Cliente: `auth.SessionService/CheckSession` (fallback cuando Redis no responde). No expone gRPC. |
| Publica | Ninguno. |
| Consume | NATS **core** (no durable): eventos marcados como "UI-visibles" de todos los dominios (estado de routers, alertas, peers WG). |
| Dependencias | auth (JWKS + sesiones), Redis (degradable), NATS (degradable), servicios de dominio. |
| Sprint | 1 |
| Estado | **Stateless**, N réplicas. Conexiones WS ligadas a la réplica (sin sticky necesario: el cliente reconecta a cualquiera). |

### auth

| Campo | Valor |
| --- | --- |
| Responsabilidad | Identidad y acceso: usuarios, credenciales (Argon2id), TOTP, login/logout, refresh tokens con rotación, sesiones revocables, roles, permisos, ACL, recuperación de cuenta, **almacén de auditoría**, JWKS. Preparado para OIDC/WebAuthn. Política en [security.md](security.md). |
| Dueño de datos (PG `auth`) | `organizations`, `users`, `credentials`, `totp_secrets`, `sessions`, `refresh_tokens`, `roles`, `permissions`, `role_permissions`, `user_roles`, `acl_entries`, `password_resets`, `audit_log`, `outbox`. |
| API pública | `/api/v1/auth/*` (login, refresh, logout, totp), `/api/v1/users`, `/api/v1/roles`, `/api/v1/permissions`, `/api/v1/sessions`, `/api/v1/audit`, `/.well-known/jwks.json`. |
| gRPC | `SessionService.CheckSession`, `UserService.GetUsers` (nombres para mostrar en otros servicios). |
| Publica | `auth.user.created/updated/disabled`, `auth.session.revoked`, `auth.role.updated`, `auth.audit.recorded` (propia). |
| Consume | `*.audit.recorded` de todos los servicios → `audit_log`. |
| Dependencias | PostgreSQL (crítica), Redis (publica revocaciones), NATS (vía outbox), SMTP (recuperación). |
| Sprint | 1 (login básico), 2 (completo) |
| Estado | **Stateless**. Claves de firma de JWT en secreto montado (rotación con `kid`). |

### devices

| Campo | Valor |
| --- | --- |
| Responsabilidad | Inventario (estado deseado): sitios, routers, dispositivos, interfaces declaradas, IPs, vendors, modelos, firmware esperado, credenciales SNMP/API cifradas (envelope encryption), tags, grupos, ubicación lógica, **clientes y su asignación a sitio/router/IP** (ver [Q5](open-questions/architecture.md#q5)). Mantiene copia de solo lectura del estado observado para listar/filtrar. |
| Dueño de datos (PG `devices`) | `sites`, `routers`, `devices`, `interfaces`, `ip_addresses`, `vendors`, `models`, `firmwares`, `credentials`, `tags`, `router_tags`, `groups`, `customers`, `customer_assignments`, `router_status_cache` (proyección), `outbox`. |
| API pública | `/api/v1/sites`, `/api/v1/routers`, `/api/v1/devices`, `/api/v1/interfaces`, `/api/v1/vendors`, `/api/v1/models`, `/api/v1/customers`, `/api/v1/tags`, `/api/v1/groups`. |
| gRPC | `InventoryService.ListPollingTargets` (snapshot para snmp, con credenciales descifradas; solo para identidades de servicio autorizadas), `GetPollingTarget`, `GetRouters` (batch de nombres), `ListCustomerAddressMap` (snapshot IP→cliente para flows). |
| Publica | `devices.site.created/updated/deleted`, `devices.router.created/updated/deleted`, `devices.router.credentials_rotated`, `devices.customer.assigned/unassigned`, `devices.audit.recorded`. |
| Consume | `snmp.router.state_changed` → `router_status_cache`; `snmp.router.interfaces_discovered` (para sugerir interfaces). |
| Dependencias | PostgreSQL (crítica), NATS (outbox), clave de cifrado de credenciales (KMS/secret). |
| Sprint | 1 (esqueleto), 3 (completo) |
| Estado | **Stateless**. |

### wireguard

| Campo | Valor |
| --- | --- |
| Responsabilidad | Plano de control WireGuard: servidores/hubs, interfaces, peers, generación de claves (privadas cifradas en reposo, o generadas en el router — ver [Q9](open-questions/architecture.md#q9)), IPAM de direcciones de túnel, AllowedIPs, rotación y revocación, generación de configuración para el router. Estado observado (handshake, rx/tx, endpoint) recibido del agente. **Absorbe "network-service".** |
| Dueño de datos (PG `wireguard`) | `wg_servers`, `wg_interfaces`, `wg_peers`, `wg_keys`, `wg_ip_pools`, `wg_ip_allocations`, `wg_peer_status` (último observado), `outbox`. |
| API pública | `/api/v1/wireguard/servers`, `/api/v1/wireguard/peers`, `/api/v1/wireguard/peers/{id}/config`, `/api/v1/wireguard/peers/{id}/rotate`, `/api/v1/wireguard/peers/{id}/revoke`, `/api/v1/wireguard/pools`. |
| gRPC | Servidor: `WireGuardControl.ReportStatus` (agente → control). Cliente: `WireGuardAgent.ApplyDesiredState` (control → agente). |
| Publica | `wireguard.peer.created/updated/revoked`, `wireguard.peer.key_rotated`, `wireguard.peer.handshake_stale`, `wireguard.peer.handshake_recovered`, `wireguard.audit.recorded`. |
| Consume | `devices.router.created/deleted` (sugerir/limpiar peer asociado; nunca borra un peer automáticamente sin acción explícita). |
| Dependencias | PostgreSQL, NATS (outbox), wireguard-agent. |
| Sprint | 4 |
| Estado | **Stateless** (control). |

#### wireguard-agent

| Campo | Valor |
| --- | --- |
| Responsabilidad | Aplicar en el kernel (netlink, `wgctrl`) el estado deseado recibido; leer handshakes/contadores cada 15 s y reportarlos. **Nunca** borra peers por no poder contactar al control (fail-static). Persiste en disco local el último estado aplicado para reaplicarlo tras reinicio del host. |
| Dueño de datos | Archivo local de estado aplicado (no es fuente de verdad). |
| API | Solo gRPC `WireGuardAgent.ApplyDesiredState`, autenticado por mTLS. Ninguna API pública. |
| Publica | Nada directamente (reporta al control por gRPC; el control publica). |
| Sprint | 4 |
| Estado | **Singleton por hub WG**, `network_mode: host`, `CAP_NET_ADMIN`. |

### snmp

| Campo | Valor |
| --- | --- |
| Responsabilidad | Sondeo SNMP v2c/v3 (sistema: uptime, CPU, RAM, temperatura, firmware; interfaces: estado, velocidad, RX/TX, errores, drops), ICMP de reachability, adaptadores por fabricante (MikroTik, Cisco, Huawei, Juniper, genérico MIB-II), cálculo de deltas de contadores, **estado observado** del router (online/degraded/offline/stale + razón) correlacionando ICMP, SNMP y handshakes WG. Escritura de métricas a ClickHouse (rol `metrics-writer`). |
| Dueño de datos | ClickHouse: `snmp_device_metrics`, `snmp_interface_metrics` (+ agregados). NATS KV: `snmp_shards` (leases), `snmp_router_state` (estado actual). Sin PostgreSQL propio en v1 (el estado actual vive en KV y se proyecta a `devices`). |
| API pública | `/api/v1/routers/{id}/metrics/live` (último valor), `/api/v1/routers/{id}/poll` (sondeo bajo demanda, `devices.update`). Las series históricas las sirve `analytics`. |
| gRPC | `PollerService.PollNow`, `PollerService.GetRouterState`. |
| Publica | `snmp.metrics.batch` (telemetría, JetStream), `snmp.router.state_changed`, `snmp.router.rebooted`, `snmp.router.interfaces_discovered`, `snmp.poller.heartbeat`. |
| Consume | `devices.router.*`, `devices.router.credentials_rotated`, `wireguard.peer.handshake_*`, `snmp.metrics.batch` (rol metrics-writer). |
| Dependencias | devices (snapshot gRPC al arrancar; luego eventos), NATS (crítica para publicar; con buffer), ClickHouse (solo metrics-writer; degradable), red de gestión/túneles WG. |
| Sprint | 5 (ICMP posiblemente en 3, ver Q7) |
| Estado | **Sharded**: 64 shards virtuales por `hash(router_id)`, leases en NATS KV (TTL 30 s). v1 corre 1 réplica que toma todos los shards. El rol `metrics-writer` es stateless con consumer en *queue group*. |

### flows

| Campo | Valor |
| --- | --- |
| Responsabilidad | **collector**: escuchar UDP (NetFlow v5/v9, IPFIX, sFlow v5), gestionar plantillas por exportador, decodificar, normalizar a modelo común, agrupar en lotes y publicar. Detectar exportadores silenciosos. **ingester**: consumir lotes, enriquecer (IP→prefijo→ASN→organización→servicio→categoría; IP cliente→cliente/sitio/router; geolocalización si aplica) con el snapshot en memoria del catálogo y del mapa de clientes, insertar en ClickHouse por lotes con token de deduplicación. Sin captura de paquetes. |
| Dueño de datos | ClickHouse: `flows_raw` (escritor único) y vistas materializadas de agregación de 5 min, 1 h y 1 día (`customer_5m/1h/1d`, `site_5m/1h/1d`, `border_1h`, `unattributed_1h`; definidas en [database.md](database.md)); tabla de cobertura `flows_coverage`. NATS: stream de lotes de flujos. |
| API pública | `/api/v1/flows/exporters` (exportadores vistos, último paquete, plantillas, tasa). |
| gRPC | `FlowsAdmin.GetExporterStats`. |
| Publica | `flows.batch.received` (telemetría, alto volumen, JetStream con retención por límites), `flows.exporter.discovered`, `flows.exporter.silent`, `flows.exporter.recovered`. |
| Consume | `traffic.catalog.published` (recargar snapshot), `devices.customer.assigned/unassigned`, `devices.router.*` (mapa IP exportador → router). |
| Dependencias | NATS (crítica), ClickHouse (ingester; degradable con buffer), MinIO (descarga de snapshot; degradable con caché local), devices (snapshot de clientes). |
| Sprint | 6 (enriquecimiento en 7) |
| Estado | collector: **Sharded por exportador** (afinidad por IP origen; cada router apunta a un collector). ingester: **Stateless**, N réplicas en queue group. |

### traffic-intelligence

| Campo | Valor |
| --- | --- |
| Responsabilidad | Catálogo de clasificación actualizable (sin valores quemados): prefijos IP→ASN (importados de fuentes BGP/RIR), ASN→organización (PeeringDB/whois), reglas organización/prefijo/dominio/puerto→servicio (Google, YouTube, Netflix, Meta, TikTok, Steam, PlayStation, Xbox, Cloudflare, AWS, Azure, GCP…), servicio→categoría. Versionado del catálogo, compilación a **snapshot binario** (trie de prefijos) publicado en MinIO, API de consulta "¿qué es esta IP?". Modelo en [traffic-model.md](traffic-model.md). |
| Dueño de datos (PG `traffic`) | `asns`, `organizations`, `prefixes`, `services`, `categories`, `classification_rules`, `catalog_versions`, `import_jobs`, `outbox`. MinIO: `catalog-snapshots/`. |
| API pública | `/api/v1/traffic/services`, `/api/v1/traffic/categories`, `/api/v1/traffic/rules`, `/api/v1/traffic/asns`, `/api/v1/traffic/lookup?ip=`, `/api/v1/traffic/catalog/versions`. |
| gRPC | `Classifier.Lookup` (consulta puntual, no para el camino caliente). |
| Publica | `traffic.catalog.published` (versión + ubicación del snapshot + checksum), `traffic.rule.updated`, `traffic.audit.recorded`. |
| Consume | Ninguno obligatorio. |
| Dependencias | PostgreSQL, MinIO (publicar snapshot; degradable: se mantiene el último), fuentes externas HTTPS. |
| Sprint | 7 |
| Estado | **Stateless** para API; el job de importación de fuentes es **singleton** (lock con lease en NATS KV). |

### detection (incluye módulo reputation)

| Campo | Valor |
| --- | --- |
| Responsabilidad | **reputation**: ingerir listas de reputación (abuse, botnets C2, Tor exit, escáneres), con fuente, confianza y caducidad; consulta de reputación de IP. **correlation**: cadena `IP sospechosa → reputación → comportamiento → tráfico → hallazgo`, nunca "IP en lista = malware". **scoring** (Sprint 10): Residential/Commercial/Security/Anomaly Score por cliente, explicable (razones + confianza). |
| Dueño de datos | PG `detection`: `reputation_sources`, `reputation_entries` (o en ClickHouse si el volumen supera ~10M entradas; decisión del Agente 2), `findings`, `finding_evidence`, `scoring_models`, `detection_settings`, `outbox`. ClickHouse: `customer_scores` (histórico de scores), `reputation_hits` (escritor único). |
| API pública | `/api/v1/security/findings`, `/api/v1/security/reputation?ip=`, `/api/v1/security/sources`, `/api/v1/customers/{id}/scores`, `/api/v1/detection/models`. |
| gRPC | `Reputation.Check` (batch). |
| Publica | `detection.finding.created/updated/resolved`, `detection.score.changed` (solo cuando cambia de clase o > umbral), `detection.audit.recorded`. |
| Consume | `flows.batch.received` (opcional, muestreado, para marcar hits de reputación en caliente) o consultas periódicas a tablas publicadas de ClickHouse (agregados de 5 min); `snmp.router.state_changed`. |
| Dependencias | ClickHouse (degradable: sin scoring nuevo), PostgreSQL, NATS, fuentes externas. |
| Sprint | 8 (reputation + correlation), 10 (scoring) |
| Estado | API **stateless**; jobs de scoring periódicos **singleton** por job (lease NATS KV) o particionados por `customer_id` si crecen. |

### alerts

| Campo | Valor |
| --- | --- |
| Responsabilidad | `Alert Rule → Detection/Event → Alert → Notification`. Reglas (sobre eventos y sobre consultas umbral), deduplicación, agrupación, correlación (supresión de `router.offline` cuando `wireguard.hub.down`), silencios, ack, escalado, auto-resolución, canales (Web, Email, Telegram; luego WhatsApp, SMS, Webhook) con reintentos. |
| Dueño de datos (PG `alerts`) | `alert_rules`, `alerts`, `alert_events`, `silences`, `notification_channels`, `notification_deliveries`, `outbox`. |
| API pública | `/api/v1/alerts`, `/api/v1/alerts/{id}/ack`, `/api/v1/alert-rules`, `/api/v1/silences`, `/api/v1/notification-channels`. |
| gRPC | Ninguno inicialmente. |
| Publica | `alerts.alert.opened/acknowledged/resolved`, `alerts.notification.failed`, `alerts.audit.recorded`. |
| Consume | `snmp.router.state_changed`, `snmp.poller.heartbeat`, `wireguard.peer.handshake_stale`, `flows.exporter.silent`, `detection.finding.*`, `detection.score.changed`. |
| Dependencias | PostgreSQL, NATS, ClickHouse (solo reglas de umbral sobre series; degradable), SMTP/Telegram. |
| Sprint | 11 |
| Estado | Consumo de eventos **stateless** (queue group). Evaluador de reglas periódicas **singleton** (lease). Envío de notificaciones con cola persistente en PG (`notification_deliveries`). |

### analytics (incluye módulo/rol reporting)

| Campo | Valor |
| --- | --- |
| Responsabilidad | Servir todas las consultas analíticas y dashboards (ISP, cliente, router) sobre tablas publicadas de ClickHouse; resolver nombres vía gRPC/caché; cachear resultados en Redis. **reporting** (rol worker): reportes de consumo/seguridad/disponibilidad/anomalías/comerciales, exportación PDF/CSV/Excel a MinIO, programación. **archiver** (job): exportar particiones vencidas a MinIO (Parquet) antes de que la TTL las borre ([storage.md](storage.md)). |
| Dueño de datos | PG `analytics`: `dashboards` (guardados por usuario), `report_definitions`, `report_runs`, `archive_manifests`, `outbox`. ClickHouse: tabla `data_coverage` y vistas de lectura propias. MinIO: `reports/`, `archive/`. **Lee** (no escribe) tablas publicadas de flows, snmp, detection. |
| API pública | `/api/v1/analytics/isp/*`, `/api/v1/analytics/customers/{id}/*`, `/api/v1/analytics/routers/{id}/*`, `/api/v1/reports`, `/api/v1/reports/{id}/download` (URL prefirmada de MinIO, corta duración). |
| gRPC | `Analytics.Query` (para alerts/detection si necesitan agregados ya calculados; opcional). |
| Publica | `reporting.report.completed/failed`, `analytics.archive.completed`, `analytics.audit.recorded`. |
| Consume | `devices.router.updated`, `devices.customer.*` (caché de nombres). |
| Dependencias | ClickHouse (crítica para su función, no para el sistema), PostgreSQL, Redis, MinIO (reporting/archiver), devices/auth (nombres). |
| Sprint | 9 (analytics), 12 (reporting), 13 (archiver) |
| Estado | API **stateless**; `reporting-worker` **stateless** con cola (JetStream work-queue o tabla PG con `SKIP LOCKED`); `archiver` **singleton** (lease). |

### reputation, reporting (diferidos)

No existen como desplegables en v1. Criterios para separarlos (cualquiera):

- **reputation**: > 50M entradas o actualizaciones de feeds que degraden la API de detection;
  `flows` necesita consultarla en el camino caliente; equipo dedicado a threat intel.
- **reporting**: reportes programados con distribución (email, suscripciones) y plantillas
  editables como dominio propio; o la carga de renderizado afecta la latencia de analytics.

---

## 4. Mapa de dependencias

```
                      ┌──────────────┐
          Frontend ──►│ api-gateway  │──► (HTTP) auth, devices, wireguard, snmp, flows,
                      └──────┬───────┘           traffic-intel, detection, alerts, analytics
                             │ NATS core (WS fan-out)
   gRPC síncrono (pocos y acotados):
     gateway ──► auth.CheckSession             (fallback de Redis)
     snmp ──► devices.ListPollingTargets       (arranque / resync)
     flows ──► devices.ListCustomerAddressMap  (arranque / resync)
     wireguard ◄──► wireguard-agent            (estado deseado / observado)
     analytics ──► devices.GetRouters, auth.GetUsers  (nombres, con caché)

   Eventos (NATS JetStream):
     devices ─────► snmp, flows, wireguard, analytics, alerts*
     wireguard ───► snmp, alerts
     snmp ────────► devices, alerts, gateway(UI), snmp(metrics-writer)
     flows ───────► flows(ingester), detection, alerts
     traffic-int ─► flows(ingester)
     detection ───► alerts, gateway(UI)
     alerts ──────► gateway(UI)
     *.audit ─────► auth
```

Ciclos: no hay ciclos síncronos. `devices ⇄ snmp` es un ciclo **asíncrono** aceptable (devices
publica inventario, snmp publica estado observado).

---

## 5. Requisitos de estado y escalado (resumen)

| Servicio | Tipo | Réplicas v1 | Escala por | Coordinación |
| --- | --- | ---: | --- | --- |
| api-gateway | Stateless | 1 (2 en HA) | conexiones/req | — |
| auth | Stateless | 1 | req | — |
| devices | Stateless | 1 | req | — |
| wireguard | Stateless | 1 | — | — |
| wireguard-agent | Singleton por hub | 1 | nº de hubs | — |
| snmp (poller) | Sharded | 1 | routers | leases NATS KV, 64 shards |
| snmp (metrics-writer) | Stateless | 1 | métricas/s | queue group |
| flows (collector) | Sharded por exportador | 1 | flujos/s | afinidad IP origen / puerto |
| flows (ingester) | Stateless | 1–2 | flujos/s, CPU | queue group |
| traffic-intelligence | Stateless + job singleton | 1 | — | lease |
| detection | Stateless + jobs singleton | 1 | clientes | lease / partición |
| alerts | Stateless + evaluador singleton | 1 | eventos | queue group + lease |
| analytics | Stateless | 1 | consultas | — |
| analytics reporting-worker | Stateless | 1 | reportes | work-queue |
| analytics archiver | Singleton | 1 | — | lease |
