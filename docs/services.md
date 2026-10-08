# Horus Flow — Catálogo de módulos y roles

> Estado: **ronda 2 (tras decisiones del PO)** · Dueño: Agente A (arquitectura)
>
> Arquitectura general: [architecture.md](architecture.md) · Unidades desplegables:
> [ADR-0025](adr/0025-binario-modular-con-roles.md) (sustituye a ADR-0014) · Tenancy:
> [ADR-0017](adr/0017-multi-tenant-desde-v1.md) · Clientes: [ADR-0018](adr/0018-la-ip-es-el-cliente.md) ·
> Modelo de datos: [database.md](database.md) · REST: [api.md](api.md) · Eventos:
> [events.md](events.md) · MikroTik: [vendors/mikrotik.md](vendors/mikrotik.md).

Los nombres de evento de este documento son **de alto nivel**. El contrato normativo (subject
`horus.<dominio>.<entidad>.<evento>.<entity_id>`, stream, payload, versión) está en
[events.md](events.md); si hay diferencia, manda `events.md`.

---

## 1. De microservicios a un binario modular

### 1.1 Por qué cambia

El Sprint 0 ([ADR-0014](adr/0014-granularidad-de-microservicios-en-el-mvp.md)) redujo el plan de
14 piezas a 6 procesos en el MVP y 13 al final de la fase 1.0, para un equipo humano pequeño. Con
**agentes de IA + 1 persona** ([D7](po-decisions.md)) que además opera la plataforma, cada proceso
sigue costando (imagen, health checks, dashboards, versiones de contrato, depuración distribuida).
Se adopta un **monolito modular**: un binario `horus`, módulos con fronteras estrictas y **roles**
seleccionables por configuración ([ADR-0025](adr/0025-binario-modular-con-roles.md)).

Las **fusiones de dominio** del Sprint 0 se mantienen como módulos:

| Plan original | Módulo | Justificación (sin cambios) |
| --- | --- | --- |
| `network-service` + `wireguard-service` | `wireguard` (+ rol `wg-agent`) | La topología es de `devices`; la IPAM de túneles es inseparable de los peers. El agente con privilegios de kernel va aparte. |
| `security` + `reputation` + `detection` | `detection` (submódulos `reputation`, `correlation`, `scoring`) | Una sola cadena de correlación. Con [ADR-0024](adr/0024-deteccion-de-botnets-como-objetivo-principal.md) la reputación llega al camino caliente como **snapshot** en el ingester, sin separar proceso. |
| `analytics` + `reporting` | `analytics` + rol `reporting` | Mismas tablas; el render de reportes es CPU intensivo y va como rol. |
| `flow collector` | rol `collector` + rol `ingester` | Escalan distinto: UDP y afinidad por exportador vs CPU/ClickHouse. |
| `traffic-intelligence` | `traffic` | Dueño del catálogo, fuera del camino caliente ([ADR-0015](adr/0015-enriquecimiento-de-flujos-en-ingesta.md)). |
| `auth` | `auth` (incluye tenants y auditoría) | La identidad es de plataforma; los tenants y membresías viven con ella. |
| `snmp` | `snmp` (incluye ICMP y estado observado) | Quien sondea sabe si el router responde. |
| — (nuevo) | submódulo `customers` en `devices` | Clientes descubiertos desde los flujos ([ADR-0018](adr/0018-la-ip-es-el-cliente.md)). |

### 1.2 Unidades desplegables

| Perfil | Contenedores propios | Roles |
| --- | --- | --- |
| **Mínimo** (un servidor; desarrollo, demo, producción hasta escala S de [architecture.md §9](architecture.md#9-límites-de-escala-esperados)) | `horus-app` | `gateway, auth, devices, wireguard, snmp, traffic, detection, alerts, analytics, reporting, ingester, jobs` |
| | `horus-collector` | `collector` |
| | `horus-wg-agent` | `wg-agent` (host network, `CAP_NET_ADMIN`) |
| **Estándar** (dos hosts, escala M) | `horus-core` | `gateway, auth, devices, wireguard, snmp, traffic, alerts` |
| | `horus-data` | `ingester, analytics, reporting, detection, jobs` |
| | `horus-collector`, `horus-wg-agent` | igual |

Infraestructura: PostgreSQL, ClickHouse, NATS JetStream, Valkey, Traefik y observabilidad. **Sin
MinIO, sin NAS obligatorio** ([ADR-0019](adr/0019-almacenamiento-local-y-destino-remoto.md)).

Comparación: el Sprint 0 preveía 6 procesos propios en el MVP y 13 al cierre de la fase 1.0; ahora
son **3 contenedores propios en todo momento** en el perfil mínimo (4 en el estándar).

### 1.3 Módulos por incremento

El orden canónico está en [roadmap.md](roadmap.md) ([ADR-0023](adr/0023-entrega-por-incrementos-y-equipo-ia.md));
referencia arquitectónica:

| Incremento (sugerido) | Módulos/roles que entran o crecen |
| --- | --- |
| 1. Plataforma base | `gateway`, `auth` (tenants, plataforma, membresías) |
| 2. Nodo conectado | `devices` (nodos, routers, credenciales), `wireguard` + `wg-agent` (enrolment), `snmp` (ICMP + SNMP) |
| 3. Tráfico por IP | `collector`, `ingester`, ClickHouse, `devices.customers` (descubrimiento), `analytics` (básico) |
| 4. Clasificación + reputación | `traffic`, `detection.reputation` (snapshot en el ingester) |
| 5. Botnets + alertas | `detection.correlation`, `alerts` |
| 6. Dashboards modulares y kiosco | `analytics` (widgets, layouts) |
| 7. Scoring, reportes, archivo, copia remota | `detection.scoring`, `reporting`, `jobs` (archiver, rclone) |

### 1.4 Reglas de módulo

- Código en `services/<módulo>/` con `api/` (contrato público) e `internal/`. `services/cmd/horus/`
  compone los módulos según `HORUS_ROLES`. Un solo `go.mod` para el backend
  ([ADR-0025](adr/0025-binario-modular-con-roles.md); estructura exacta en
  [conventions.md](conventions.md)).
- Un módulo solo importa `services/<otro>/api`; los tests de arquitectura lo verifican.
- Cada módulo posee **su esquema PostgreSQL y su usuario de BD**, y sus tablas ClickHouse (escritor
  único).
- Llamadas síncronas por la interfaz del contrato (en proceso o gRPC según dónde corra el
  proveedor); **eventos siempre por NATS con outbox**, también dentro del mismo proceso.

---

## 2. Convenciones comunes

| Aspecto | Convención |
| --- | --- |
| Puertos (por proceso) | `8080` HTTP (REST de los roles locales + `/healthz` `/readyz`), `9090` gRPC (solo si hay roles remotos que lo usen), `9100` `/metrics`. |
| Base de datos | Esquema propio por módulo, usuario propio, **RLS por tenant** y `SET LOCAL horus.tenant_id` por transacción; rol de servicio separado con `BYPASSRLS` solo para jobs multi-tenant. Migraciones con `horus migrate --module=<m>`. |
| Tenancy | `tenant_id` en todo dato de tenant; el tenant de una petición sale del `tid` del token ([ADR-0017](adr/0017-multi-tenant-desde-v1.md)). Datos de plataforma sin `tenant_id`: catálogo, feeds de reputación, fabricantes, destinos remotos. |
| Eventos de salida | Outbox si el módulo tiene PostgreSQL; telemetría (collector, snmp, ingester) publica directo. Todo mensaje con `tenant_id` y cabecera `Horus-Tenant`. |
| Eventos de entrada | Consumers durables `<módulo>-<propósito>`; idempotentes por `event_id`; rechazo de mensajes sin tenant (salvo eventos de plataforma). |
| Autorización | Fina en el módulo, con permisos `recurso.accion` del token del tenant; permisos `platform.*` para operaciones de plataforma. |
| IDs / tiempos | UUIDv7; UTC. |
| Caché | Valkey, claves `t:<tenant_id>:…` ([ADR-0020](adr/0020-valkey-en-lugar-de-redis.md)). |
| Archivos | Solo por el puerto `BlobStore` (adaptador `fs`); snapshots por NATS Object Store. |
| Observabilidad | OTel + métricas RED por rol + logs JSON con `tenant_id` ([observability.md](observability.md)). |

Leyenda de estado: **Stateless** = réplicas libres; **Singleton** = una instancia activa (lease
NATS KV); **Sharded** = N instancias con *leases*.

---

## 3. Fichas de módulo

### gateway (rol)

| Campo | Valor |
| --- | --- |
| Responsabilidad | Entrada única del frontend: authN, contexto de tenant desde `tid`, autorización gruesa, enrutamiento (handlers en proceso o proxy a otro proceso), rate limit, WebSocket fan-out filtrado por tenant y permisos, endpoint público de enrolment protegido. Detalle en [architecture.md §7](architecture.md#7-api-gateway-rol-gateway). |
| Dueño de datos | Ninguno persistente. Valkey: `ratelimit:*`, `session_revoked:*`. |
| API pública | `/api/v1/*`, `/api/v1/ws`. |
| Consume | NATS core: eventos UI-visibles (estado de routers, alertas, hallazgos, clientes nuevos, peers). |
| Dependencias | auth (JWKS, sesiones), Valkey (degradable), NATS (degradable). |
| Estado | **Stateless**. |

### auth

| Campo | Valor |
| --- | --- |
| Responsabilidad | **Tenants (ISP)** y su ciclo de vida; usuarios como identidad de plataforma; **membresías** usuario↔tenant con roles; roles de plataforma (`platform.*`) y de tenant (plantillas + personalizados); credenciales (Argon2id), TOTP, sesiones de usuario, **access tokens por tenant** (`tid`), refresh con rotación, ACL, recuperación de cuenta, sesiones de kiosco, **auditoría** (con `tenant_id` y `via_platform`), JWKS. |
| Dueño de datos (PG `auth`) | `tenants`, `users`, `tenant_memberships`, `credentials`, `totp_secrets`, `sessions`, `refresh_tokens`, `roles`, `permissions`, `role_permissions`, `membership_roles`, `platform_role_assignments`, `acl_entries`, `password_resets`, `audit_log`, `outbox`. Detalle en [database.md](database.md). |
| API pública | `/api/v1/auth/*` (login, refresh, logout, totp, **`token`** para obtener el access token de un tenant), `/api/v1/me/tenants`, `/api/v1/users`, `/api/v1/roles`, `/api/v1/memberships`, `/api/v1/sessions`, `/api/v1/audit`, `/api/v1/platform/tenants`, `/api/v1/platform/users`, `/api/v1/platform/overview`, `/.well-known/jwks.json`. |
| Contrato síncrono | `SessionService.CheckSession`, `UserService.GetUsers`, `TenantService.GetTenants`. |
| Publica | `auth.tenant.created/updated/suspended`, `auth.user.created/updated/disabled`, `auth.membership.granted/revoked`, `auth.session.revoked`, `auth.role.updated`, `auth.audit.recorded`. |
| Consume | `*.audit.recorded` → `audit_log`. |
| Dependencias | PostgreSQL (crítica), Valkey, NATS (outbox), SMTP (recuperación). |
| Incremento | 1 |
| Estado | **Stateless**. |

### devices (incluye submódulo customers)

| Campo | Valor |
| --- | --- |
| Responsabilidad | Inventario del tenant (estado deseado): **nodos** (sitios), routers (principal/secundario), interfaces, fabricantes/modelos y **matriz de capacidades** ([ADR-0022](adr/0022-mikrotik-routeros-v7-primer-fabricante.md)), credenciales cifradas (SNMPv3, API de solo lectura), tags, grupos, **realms** y **prefijos de clientes** (sugeridos leyendo el router por API 8729/REST). **customers**: alta automática de clientes por IP desde `flows.client.first_seen` (upsert por `tenant, realm, ip`), tipo (`residential` por defecto; `kind_source`, `kind_locked`), alias opcionales, ciclo de vida `active/inactive`. Proyección del estado observado para listar/filtrar. Comprobación de NAT y de configuración de Traffic Flow del router en el alta. |
| Dueño de datos (PG `devices`) | `sites`, `routers`, `interfaces`, `ip_realms` (+ `client_prefixes`), `vendors`, `models`, `capabilities`, `credentials`, `tags`, `groups`, `customers`, `customer_aliases`, `router_status_cache` (proyección), `outbox`. Fabricantes/modelos/capacidades son de plataforma. |
| API pública | `/api/v1/sites`, `/api/v1/routers`, `/api/v1/routers/{id}/facts` (lectura del router), `/api/v1/interfaces`, `/api/v1/realms`, `/api/v1/customers` (lista/búsqueda por IP, `PATCH` tipo/alias; **sin alta manual obligatoria**), `/api/v1/tags`, `/api/v1/groups`, `/api/v1/vendors`, `/api/v1/models`. |
| Contrato síncrono | `InventoryService.ListPollingTargets` (todos los tenants, solo identidades de servicio), `GetPollingTarget`, `GetRouters`, `ListExporters` (IP de túnel → router → tenant, para el collector), `ListRealms` (prefijos de clientes, para el ingester), `ListKnownClients` (snapshot por realm para el ingester). |
| Publica | `devices.site.*`, `devices.router.created/updated/deleted`, `devices.router.credentials_rotated`, `devices.realm.updated`, **`devices.customer.discovered`**, `devices.customer.updated` (tipo/alias), `devices.customer.inactivated`, `devices.audit.recorded`. |
| Consume | `snmp.router.state_changed` → proyección; `snmp.router.interfaces_discovered`; **`flows.client.first_seen`** → alta de clientes; `flows.client.activity_summary` (horario, `last_seen_at`); **`detection.customer.kind_suggested`** → tipo si no está bloqueado. |
| Dependencias | PostgreSQL (crítica), NATS (outbox), clave de cifrado de credenciales, routers (lectura por API, degradable). |
| Incremento | 2 (inventario), 3 (customers) |
| Estado | **Stateless**; el consumidor de `first_seen` en *queue group*, idempotente por clave natural. |

### wireguard (+ rol wg-agent)

| Campo | Valor |
| --- | --- |
| Responsabilidad | Control WireGuard: hub(s), **IPAM de túneles de plataforma** (rangos configurables, validación de solapes con `100.64.0.0/10`, prefijos de clientes y redes leídas del router), peers por router, **enrolment** con token de un solo uso (`POST /api/v1/enroll/wireguard`; alternativa: clave pública pegada en la UI), generación del **script RouterOS** (WG iniciado por el router con keepalive 25 s, Traffic Flow, SNMPv3, usuario `horus-ro`, firewall), revocación. La clave privada del router **nunca** pasa por Horus; la del hub se guarda cifrada y se respalda. Estado observado recibido del agente. |
| Dueño de datos (PG `wireguard`) | `wg_hubs`, `wg_peers`, `wg_ip_pools`, `wg_ip_allocations`, `wg_enrollment_tokens` (solo hash), `wg_peer_status`, `outbox`. Pools y hubs de plataforma; peers con `tenant_id`. |
| API pública | `/api/v1/wireguard/peers`, `/api/v1/wireguard/peers/{id}/script`, `/api/v1/wireguard/peers/{id}/enrollment-token` (emitir/revocar), `/api/v1/wireguard/peers/{id}/revoke`, `/api/v1/enroll/wireguard` (público, token), `/api/v1/platform/wireguard/hubs`, `/api/v1/platform/wireguard/pools`. |
| Contrato síncrono | Servidor `WireGuardControl.ReportStatus` (agente → control); cliente `WireGuardAgent.ApplyDesiredState` (gRPC mTLS, siempre: el agente es otro proceso). |
| Publica | `wireguard.peer.created/enrolled/activated/revoked`, `wireguard.peer.handshake_stale/recovered`, `wireguard.hub.status_changed`, `wireguard.audit.recorded`. |
| Consume | `devices.router.created/deleted` (prepara/limpia peer; nunca borra sin acción explícita). |
| **wg-agent** | Aplica el estado deseado en el kernel (netlink), lee handshakes cada 15 s, **fail-static** (nunca borra peers por no contactar al control), persiste el último estado aplicado, aplica reglas nftables que **impiden tráfico entre peers**. Singleton por hub, host network, `CAP_NET_ADMIN`. |
| Incremento | 2 |
| Estado | Control **stateless**; agente **singleton por hub**. |

### snmp

| Campo | Valor |
| --- | --- |
| Responsabilidad | Sondeo SNMPv3 (v2c opcional) e ICMP por el túnel, perfiles por fabricante (`SNMPProfile`; MikroTik primero, MIB-II genérico), deltas de contadores, **estado observado** (online/degraded/offline/stale + razón) correlacionando ICMP, SNMP y handshakes WG, detección de caída del hub (una alerta, no cientos). No recorre interfaces PPPoE dinámicas. |
| Dueño de datos | ClickHouse (escritas por el rol `ingester` en su nombre): `snmp_device_metrics`, `snmp_interface_metrics` (+ agregados). NATS KV: `snmp_shards`, `snmp_router_state`. Sin PostgreSQL propio. |
| API pública | `/api/v1/routers/{id}/metrics/live`, `/api/v1/routers/{id}/poll`. |
| Publica | `snmp.metrics.batch` (telemetría), `snmp.router.state_changed`, `snmp.router.rebooted`, `snmp.router.interfaces_discovered`, `snmp.poller.heartbeat`. |
| Consume | `devices.router.*`, `devices.router.credentials_rotated`, `wireguard.peer.handshake_*`, `wireguard.peer.activated`. |
| Dependencias | devices (snapshot), NATS (con buffer), túneles WG. |
| Incremento | 2 |
| Estado | **Sharded** (64 shards virtuales, leases en NATS KV); 1 réplica toma todos en v1. |

### collector (rol de flows)

| Campo | Valor |
| --- | --- |
| Responsabilidad | Escuchar UDP (IPFIX, NetFlow v9/v5, sFlow v5); identificar el exportador por **IP origen = IP de túnel** → router → tenant (proyección de `devices.ListExporters`); **descartar** exportadores no registrados; plantillas por exportador; decodificar con `FlowExporterQuirks` (MikroTik primero); normalizar; **límite de flujos/s por exportador y por tenant**; lotes por exportador con `Horus-Tenant`; exportadores silenciosos. Sin captura de paquetes. |
| Dueño de datos | NATS: stream FLOWS (escritor). |
| Publica | `flows.batch.received` (telemetría), `flows.exporter.silent/recovered`, `flows.exporter.unregistered`. |
| Consume | `devices.router.*` (mapa exportadores). |
| Incremento | 3 |
| Estado | **Sharded por exportador** (afinidad por IP origen). Contenedor propio. |

### ingester (rol de flows y de métricas)

| Campo | Valor |
| --- | --- |
| Responsabilidad | Consumir lotes de flujos y de métricas SNMP; **enriquecer** flujos con el snapshot del catálogo (IP→prefijo→ASN→organización→servicio→categoría), el **snapshot de reputación** (`reputation_hit`) y el realm/lado cliente (prefijos de clientes); insertar en ClickHouse por lotes con token de deduplicación; **detectar IPs de clientes no vistas** en su conjunto por realm y publicar `flows.client.first_seen` en lotes deduplicados y con límite por realm; publicar `flows.client.activity_summary` horario. Nunca escribe en PostgreSQL. |
| Dueño de datos | ClickHouse: `flows_raw` (clave `tenant_id, realm_id, client_ip`, sin `customer_id`), vistas materializadas de 1 min (detección, retención corta), 5 min, 1 h y 1 día, `flows_coverage`; escribe también las tablas de métricas SNMP en nombre de `snmp`. Esquema en [database.md](database.md) / [traffic-model.md](traffic-model.md). |
| Publica | `flows.client.first_seen`, `flows.client.activity_summary`. |
| Consume | `flows.batch.received`, `snmp.metrics.batch`, `traffic.catalog.published`, `detection.reputation.snapshot_published`, `devices.realm.updated`, `devices.customer.discovered`. |
| Dependencias | NATS (crítica), ClickHouse (degradable con buffer), NATS Object Store (snapshots; caché local). |
| Incremento | 3 (4 para catálogo y reputación) |
| Estado | **Stateless**, queue group. |

### traffic

| Campo | Valor |
| --- | --- |
| Responsabilidad | Catálogo de clasificación **de plataforma** (sin valores quemados): prefijos→ASN (fuentes BGP/RIR), ASN→organización (PeeringDB), reglas →servicio→categoría; versionado; compilación a **snapshot binario** publicado en **NATS Object Store**; API "¿qué es esta IP?". Modelo en [traffic-model.md](traffic-model.md). |
| Dueño de datos (PG `traffic`) | `asns`, `organizations`, `prefixes`, `services`, `categories`, `classification_rules`, `catalog_versions`, `import_jobs`, `outbox`. Object Store: `catalog-snapshots`. |
| API pública | `/api/v1/traffic/services`, `/categories`, `/asns`, `/lookup?ip=`, `/catalog/versions` (lectura para todos; escritura `platform.catalog.manage`). |
| Publica | `traffic.catalog.published`, `traffic.rule.updated`, `traffic.audit.recorded`. |
| Incremento | 4 |
| Estado | API **stateless**; importación **singleton**. |

### detection (reputation, correlation, scoring)

| Campo | Valor |
| --- | --- |
| Responsabilidad | **Objetivo de primer nivel** ([ADR-0024](adr/0024-deteccion-de-botnets-como-objetivo-principal.md)). **reputation**: feeds de plataforma (C2 de botnets, listas de bloqueo, escáneres, pools de minería) con fuente, confianza, caducidad y licencia; compila y publica el **snapshot de reputación** para el ingester. **correlation**: reglas versionadas y explicables sobre agregados de 1–5 min y `reputation_hit` (contacto con C2, escaneo saliente, participación en DDoS, spam, *beaconing*, proxy/minería) → **hallazgos por cliente** (tenant, realm, IP) con evidencias, confianza y estado; gestión de falsos positivos; umbrales por tenant. **scoring**: residencial/comercial y anomalía por IP, explicable; publica sugerencias de tipo a `devices`. |
| Dueño de datos | PG `detection`: `reputation_sources`, `reputation_entries` (o en ClickHouse si el volumen lo exige; [database.md](database.md)), `detection_rules`, `findings`, `finding_evidence`, `tenant_detection_settings`, `scoring_models`, `outbox`. ClickHouse: `customer_scores`, `detection_windows`. Object Store: `reputation-snapshots`. |
| API pública | `/api/v1/security/findings` (+ `ack`, `resolve`, `false-positive`), `/api/v1/security/findings/{id}/evidence` (permiso propio, auditado), `/api/v1/security/reputation?ip=`, `/api/v1/security/rules`, `/api/v1/customers/{id}/scores`, `/api/v1/platform/reputation/sources`. |
| Publica | `detection.finding.opened/updated/resolved`, `detection.reputation.snapshot_published`, `detection.customer.kind_suggested`, `detection.score.changed`, `detection.audit.recorded`. |
| Consume | `devices.customer.discovered`, `snmp.router.state_changed`, `flows.exporter.silent`; consultas periódicas a tablas publicadas de ClickHouse (por tenant, con *watermark* para reprocesar tras caídas). |
| Dependencias | ClickHouse (degradable: sin hallazgos nuevos), PostgreSQL, NATS, fuentes externas. |
| Incremento | 4 (reputation), 5 (correlation), 7 (scoring) |
| Estado | API **stateless**; jobs de ventana **singleton** por job (lease), particionables por tenant. |

### alerts

| Campo | Valor |
| --- | --- |
| Responsabilidad | `Regla → Evento/Hallazgo → Alerta → Notificación` por tenant: reglas sobre eventos y umbrales, deduplicación, agrupación, correlación (supresión de `router.offline` durante `wireguard.hub.down`), silencios, ack, escalado, auto-resolución, canales (Web, Email, Telegram; luego WhatsApp, SMS, Webhook). Alertas de plataforma (hub caído, avalancha de IPs, destino remoto retrasado) para usuarios de plataforma. |
| Dueño de datos (PG `alerts`) | `alert_rules`, `alerts`, `alert_events`, `silences`, `notification_channels`, `notification_deliveries`, `outbox`. |
| API pública | `/api/v1/alerts`, `/api/v1/alerts/{id}/ack`, `/api/v1/alert-rules`, `/api/v1/silences`, `/api/v1/notification-channels`. |
| Publica | `alerts.alert.opened/acknowledged/resolved`, `alerts.notification.failed`, `alerts.audit.recorded`. |
| Consume | `snmp.router.state_changed`, `snmp.poller.heartbeat`, `wireguard.peer.handshake_stale`, `wireguard.hub.status_changed`, `flows.exporter.silent`, `detection.finding.*`, `jobs.coverage.low` (`flow_coverage_low`), `jobs.remote_sync.lagging`. |
| Incremento | 5 |
| Estado | Consumo **stateless** (queue group); evaluador periódico **singleton**. |

### analytics (+ rol reporting)

| Campo | Valor |
| --- | --- |
| Responsabilidad | Consultas analíticas por tenant sobre tablas publicadas (consumo por IP, top servicios/ASN/categorías, por nodo/router, cobertura); **widgets y layouts de dashboard guardados** y **modo kiosco/NOC** ([D8](po-decisions.md)): consultas de widgets cacheadas en Valkey y autorrefresco; resolución de nombres con caché; límites de concurrencia por tenant. **reporting** (rol): reportes de consumo, seguridad (hallazgos), disponibilidad, comerciales; PDF/CSV/Excel al almacén local; programación. |
| Dueño de datos | PG `analytics`: `dashboards`, `dashboard_widgets`, `kiosk_profiles`, `report_definitions`, `report_runs`, `outbox`. Almacén local: `reports/<tenant_id>/`. Lee tablas publicadas de ingester, snmp, detection. |
| API pública | `/api/v1/analytics/*`, `/api/v1/dashboards`, `/api/v1/widgets/{type}/data`, `/api/v1/kiosk/*`, `/api/v1/reports`, `/api/v1/reports/{id}/download` (servido por la API, autorizado por tenant). |
| Publica | `reporting.report.completed/failed`, `analytics.audit.recorded`. |
| Consume | `devices.router.updated`, `devices.customer.*` (caché de nombres/alias). |
| Dependencias | ClickHouse (crítica para su función), PostgreSQL, Valkey, almacén local (reporting). |
| Incremento | 3 (básico), 6 (widgets y kiosco), 7 (reporting) |
| Estado | **Stateless**; `reporting` con cola (JetStream work-queue). |

### jobs (rol)

| Campo | Valor |
| --- | --- |
| Responsabilidad | Tareas singleton de plataforma: **archivado** de particiones vencidas de ClickHouse a Parquet en `archive/<tenant_id>/` (verificado antes de permitir el borrado); **copia remota con rclone** a los destinos configurados (SFTP primero; Drive/MEGA/Dropbox después; cifrado `crypt` en nube), con estado y métricas por destino; **cobertura de flujos** (bytes de flujos vs contadores SNMP por router, cada hora); retención del almacén local; mantenimiento (inactivación de clientes sin tráfico vía evento a `devices`). [ADR-0019](adr/0019-almacenamiento-local-y-destino-remoto.md). |
| Dueño de datos | PG `jobs`: `remote_destinations` (credenciales cifradas; `tenant_id` nulo = plataforma), `remote_sync_runs`, `archive_manifests`, `outbox`. Almacén local: `archive/`, `audit/`. |
| API pública | `/api/v1/platform/storage/destinations` (CRUD, prueba de conexión), `/api/v1/platform/storage/status`, `/api/v1/coverage` (por tenant). |
| Publica | `jobs.archive.completed`, `jobs.remote_sync.completed/lagging`, `jobs.coverage.low`. |
| Dependencias | ClickHouse, almacén local, rclone, destinos remotos (**degradables**: nunca bloquean nada). |
| Incremento | 3 (cobertura), 7 (archivo y copia remota) |
| Estado | **Singleton** por tarea (lease NATS KV). |

### Diferidos

No existen como módulos propios en v1: `reputation` (vive en `detection`), `reporting` como
dominio separado (es un rol de `analytics`), aprovisionamiento con escritura en routers
([Q23](open-questions/architecture.md#q23)), enriquecimiento de clientes con RADIUS/PPP (opcional,
[ADR-0018](adr/0018-la-ip-es-el-cliente.md) §6).

---

## 4. Mapa de dependencias

```
                      ┌──────────────┐
          Frontend ──►│   gateway    │──► (en proceso o HTTP) auth, devices, wireguard, snmp,
                      └──────┬───────┘     traffic, detection, alerts, analytics, jobs
                             │ NATS core (WS fan-out, filtrado por tenant)
   Contratos síncronos (pocos y acotados):
     gateway ──► auth.CheckSession                     (fallback de Valkey)
     snmp ──► devices.ListPollingTargets               (arranque / resync)
     collector ──► devices.ListExporters               (arranque / resync)
     ingester ──► devices.ListRealms, ListKnownClients (arranque / resync)
     wireguard ◄──gRPC mTLS──► wg-agent                (estado deseado / observado)
     analytics ──► devices.GetRouters, auth.GetUsers   (nombres, con caché)

   Eventos (NATS JetStream):
     devices ─────► snmp, collector, ingester, wireguard, analytics, detection
     wireguard ───► snmp, alerts
     snmp ────────► devices, alerts, gateway(UI), ingester(métricas)
     collector ───► ingester, alerts
     ingester ────► devices (first_seen, activity_summary)
     traffic ─────► ingester (snapshot)
     detection ───► ingester (snapshot de reputación), devices (tipo), alerts, gateway(UI)
     jobs ────────► alerts, devices
     alerts ──────► gateway(UI)
     *.audit ─────► auth
```

Ciclos: ninguno síncrono. `devices ⇄ ingester` (realms/clientes ↔ IPs nuevas) y
`devices ⇄ detection` (clientes ↔ tipo sugerido) son ciclos **asíncronos** e idempotentes.

---

## 5. Requisitos de estado y escalado

| Rol | Tipo | Réplicas v1 | Escala por | Coordinación |
| --- | --- | ---: | --- | --- |
| gateway | Stateless | 1 | conexiones/req | — |
| auth, devices, wireguard, traffic, alerts, analytics | Stateless | 1 (en `horus-app`) | req | — |
| devices (consumidor `first_seen`) | Stateless | 1 | IPs nuevas/s | queue group, upsert idempotente |
| wg-agent | Singleton por hub | 1 | nº de hubs | — |
| snmp | Sharded | 1 | routers | leases NATS KV, 64 shards |
| collector | Sharded por exportador | 1 | flujos/s | afinidad IP origen |
| ingester | Stateless | 1–2 | flujos/s, CPU | queue group |
| detection (jobs de ventana) | Singleton por job | 1 | IPs × reglas | lease; partición por tenant si crece |
| alerts (evaluador) | Singleton | 1 | reglas | lease |
| reporting | Stateless | 1 | reportes | work-queue |
| jobs | Singleton por tarea | 1 | — | lease |
