# Épicas

Épicas de todo el proyecto, alineadas con los sprints de [`../vision.md`](../vision.md) §9 y los
ajustes de [`../roadmap.md`](../roadmap.md) §4. Formato de IDs y reglas en [`README.md`](README.md).

Cada épica indica: objetivo, alcance (dentro / fuera), sprint(s), servicios, flujo responsable
([`team.md`](team.md)) y criterio de cierre. Los servicios siguen
[`../services.md`](../services.md) y [ADR-0014](../adr/0014-granularidad-de-microservicios-en-el-mvp.md):
`wireguard` + `wireguard-agent`; `detection` con módulos `reputation`, `correlation`, `scoring`;
`reporting` como rol worker de `analytics`; auditoría dentro de `auth`; ICMP dentro de `snmp`.

## Resumen

| ID | Épica | Sprint(s) | Flujo |
| --- | --- | --- | --- |
| EP-00 | Arquitectura y decisiones base | S0 | Todos |
| EP-01 | Plataforma de ejecución y DevOps | S1 (+ continuo) | Plataforma |
| EP-02 | Shell del frontend | S1 | Frontend |
| EP-03 | Autenticación y sesiones | S1–S2 | Backend core |
| EP-04 | Autorización (RBAC + ACL) y auditoría | S2–S3 | Backend core |
| EP-05 | Inventario de red | S3 | Backend core |
| EP-06 | Clientes y mapeo IP → cliente | S3 (entidad), S4 (spike fuente), S5–S6 (adaptador) | Backend core |
| EP-07 | WireGuard | S4 | Backend core |
| EP-08 | Monitoreo de dispositivos (ICMP + SNMP, servicio `snmp`) | S3 (ICMP), S5 (+ adaptadores S6–S8) | Datos |
| EP-09 | Notificaciones en vivo mínimas (sin servicio `alerts`) | S5 | Backend core / Frontend |
| EP-10 | Colección de flujos | S6 | Datos |
| EP-11 | Enriquecimiento IP → Prefix → ASN → Organization | S5–S7 | Datos |
| EP-12 | Catálogo de servicios y categorías | S7 | Datos / Frontend |
| EP-13 | Reputación (módulo de `detection`) | S8 | Datos |
| EP-14 | Detección de seguridad | S8 (+ S10–S12) | Datos / Backend core |
| EP-15 | Analítica (ISP, Cliente, Router) | S9 | Frontend / Datos |
| EP-16 | Scoring residencial/comercial | S10 | Datos |
| EP-17 | Alertas y notificaciones | S11 | Backend core |
| EP-18 | Reportes (worker `reporting` de `analytics`) | S12 | Datos / Frontend |
| EP-19 | Almacenamiento histórico y archivado | S13 (TTL desde S6) | Plataforma / Datos |
| EP-20 | Resiliencia y alta disponibilidad | S14 (incremental desde S5) | Plataforma |
| EP-21 | Rendimiento y escalabilidad | S15 (incremental desde S6) | Plataforma / Datos |
| EP-22 | Release 1.0 y operación | S16 | Todos |
| EP-T1 | Observabilidad | Transversal desde S1 | Plataforma |
| EP-T2 | Seguridad transversal | Transversal desde S0 | Todos (revisión: Plataforma) |
| EP-T3 | Sistema de diseño y accesibilidad | Transversal desde S1 | Frontend |
| EP-T4 | Calidad y pruebas automatizadas | Transversal desde S1 | Todos |
| EP-T5 | Tiempo real (WebSocket) | S1 (base), S4–S5 | Backend core / Frontend |

---

## EP-00 · Arquitectura y decisiones base

- **Objetivo:** que las decisiones caras de revertir estén escritas, revisadas y sean coherentes
  entre sí antes de escribir código de negocio.
- **Alcance:** `architecture.md`, `services.md`, `adr/`, `database.md`, `traffic-model.md`,
  `storage.md`, `api.md`, `events.md`, `security.md`, `observability.md`,
  `disaster-recovery.md`, `conventions.md`, `roadmap.md`, `backlog/`, `frontend.md`,
  `open-questions/`.
- **Fuera:** código de aplicación, elección de proveedor cloud.
- **Cierre:** las 5 preguntas de fallo de `vision.md` §9 tienen respuesta trazable a un documento.

## EP-01 · Plataforma de ejecución y DevOps

- **Objetivo:** entorno reproducible y pipeline que impida regresiones desde el día 1.
- **Alcance:** monorepo según `vision.md` §11, `docker compose` (PostgreSQL, Redis, NATS
  JetStream, MinIO, reverse proxy), gestión de variables y secretos, CI (lint, tests, builds,
  security check), migraciones con goose + lint squawk ([`../database.md`](../database.md) D6),
  buckets MinIO con object lock creados en S1 ([`../storage.md`](../storage.md) §4), imágenes
  versionadas, backup diario de PostgreSQL (adelantado a S2, [`../roadmap.md`](../roadmap.md) §4.7).
- **Fuera:** Kubernetes (solo se garantiza que nada lo impida), CD a producción (S16).
- **Cierre:** un desarrollador nuevo levanta todo con un comando y < 15 min; CI < 15 min por PR.

## EP-02 · Shell del frontend

- **Objetivo:** aplicación Nuxt 4 navegable con layout, tema y patrones base.
- **Alcance:** layout dashboard (sidebar + toolbar), navegación filtrada por permisos, menú de
  usuario, login, dashboard base, i18n preparado, tema claro/oscuro, cliente HTTP y WS, manejo de
  errores global. Ver [`../frontend.md`](../frontend.md).
- **Fuera:** pantallas de dominio.
- **Cierre:** Lighthouse accesibilidad ≥ 95 en login y dashboard; e2e de login verde.

## EP-03 · Autenticación y sesiones

- **Objetivo:** identidad segura de usuarios.
- **Alcance:** usuario/contraseña con Argon2id, access token corto + refresh token rotatorio,
  logout, sesiones revocables, TOTP con códigos de respaldo, restablecimiento asistido por admin,
  recuperación por email (si hay SMTP), rate limiting y bloqueo progresivo. Base compatible con
  OAuth2/OIDC (`vision.md` §7).
- **Fuera:** WebAuthn y SSO externo (post 1.0, salvo que P-13 lo priorice).
- **Cierre:** checklist ASVS L1 de autenticación y sesión cumplido.

## EP-04 · Autorización (RBAC + ACL) y auditoría

- **Objetivo:** cada acción está permitida explícitamente y queda registrada.
- **Alcance:** catálogo de permisos `recurso.accion` (base `vision.md` §7), roles predefinidos y
  personalizados, motor ACL (sujeto–recurso–acción) en S2 y su aplicación a sitios/grupos en S3,
  enforcement en api-gateway y servicios, registro de auditoría inmutable y consultable (almacén
  en `auth`, eventos `*.audit.recorded` de todos los servicios).
- **Fuera:** multi-organización (v1 es un solo ISP).
- **Cierre:** test de matriz de permisos automatizado para cada endpoint.

## EP-05 · Inventario de red

- **Objetivo:** fuente de verdad de sitios, routers y su configuración de acceso.
- **Alcance:** Site, Router, Device, Interface, IP, Credential, Vendor, Model, Firmware; tags,
  grupos, ubicación lógica; credenciales cifradas; importación CSV; dashboard por estado.
- **Fuera:** descubrimiento automático de interfaces (llega con SNMP en S5), topología/mapa.
- **Cierre:** inventario real del ISP cargado (o muestra ≥ 20 routers).

## EP-06 · Clientes y mapeo IP → cliente *(propuesta nueva — riesgo crítico)*

- **Objetivo:** poder atribuir tráfico a clientes, requisito de S7, S9 y S10. Sin esta épica esos
  sprints solo entregan valor por IP (Q5 de
  [`../open-questions/architecture.md`](../open-questions/architecture.md)).
- **Alcance:** entidad `customer`, `customer_service_link` y `customer_ip_assignment` con vigencia
  temporal ([`../database.md`](../database.md)); alta manual e importación CSV (S3); **spike de
  fuente** con el PO (S4); **adaptador como módulo de `devices`** que ingiere asignaciones desde
  RADIUS accounting o la API del router (sesiones PPPoE / leases DHCP), con `source` y
  `session_ref` (S5); producción en el piloto y métrica de cobertura (S6); snapshot gRPC
  `ListCustomerAddressMap` para el ingester de `flows`.
- **Fuera:** facturación, CRM, sincronización bidireccional.
- **Cierre:** ≥ 95 % de bytes de flujos de un router piloto atribuidos a un cliente.

## EP-07 · WireGuard

- **Objetivo:** gestionar túneles WG sin que nadie ejecute comandos a mano.
- **Alcance:** `wireguard` (control: servidores, peers, claves, IPAM de túnel, AllowedIPs,
  configuración descargable, rotación, revocación, auditoría) y `wireguard-agent` (aplica el
  estado deseado en el kernel, reporta handshake y contadores, *fail-static*). Absorbe el
  *network-service* de `vision.md`.
- **Fuera:** configuración automática del lado del router (se evalúa con adaptadores MikroTik
  post-MVP).
- **Cierre:** ≥ 10 routers reales conectados por túneles gestionados desde la UI.

## EP-08 · Monitoreo de dispositivos (ICMP + SNMP)

- **Objetivo:** saber en todo momento el estado y salud de cada router.
- **Alcance:** servicio `snmp` que nace en S3 con ICMP; SNMP v2c/v3: uptime, CPU, RAM,
  temperatura, firmware; interfaces: estado, velocidad, RX, TX, errores, drops (S5); estado
  observado correlacionando ICMP + SNMP + handshake WG (`online/degraded/offline/stale` +
  `warning/critical`, [`../architecture.md`](../architecture.md) §10.4); series en ClickHouse si se
  aprueba C-03; adaptador MikroTik (S5); Cisco, Huawei, Juniper (S6–S8); simulador SNMP.
- **Fuera:** traps SNMP (post-MVP, evaluar en S11), configuración de equipos.
- **Cierre:** cambio de estado detectado en < 2 ciclos de sondeo con 100 routers simulados.

## EP-09 · Notificaciones en vivo mínimas *(adelanto propuesto)*

- **Objetivo:** que el NOC vea cambios de estado relevantes sin esperar a S11.
- **Alcance:** centro de notificaciones web alimentado por eventos de estado vía WebSocket
  (router online/offline/degraded, peer WG sin handshake), toasts y contador. Alertas de
  infraestructura por Prometheus/Alertmanager. Opcional (P-17): métrica `horus_router_up` para
  avisar por Alertmanager de routers core caídos.
- **Fuera:** servicio `alerts`, reglas configurables, canales de negocio (S11).
- **Cierre:** un router que cae aparece en el centro de notificaciones en < 5 s tras el evento.

## EP-10 · Colección de flujos

- **Objetivo:** ingesta fiable de metadatos de tráfico.
- **Alcance:** NetFlow v5/v9, IPFIX, sFlow; `flows` con roles `collector` e `ingester`;
  `Router → collector → NATS → ingester (enriquece en ingesta, ADR-0015) → ClickHouse`; batching;
  **modo de pre-agregación** de 60 s en el colector ([`../traffic-model.md`](../traffic-model.md)
  §11); muestreo 1:N configurable (necesario desde ~200 routers); TTL raw 7 días; generador
  sintético para carga.
- **Fuera:** captura de paquetes / DPI.
- **Cierre:** throughput objetivo (a fijar con P-05) sostenido 1 h sin pérdida ni lag creciente.

## EP-11 · Enriquecimiento IP → Prefix → ASN → Organization

- **Objetivo:** atribuir cada IP remota a su red y organización.
- **Alcance:** fuentes de prefijos/ASN y ASN→Org con actualización programada y versionado,
  rangos publicados por proveedores (Google, Meta, AWS, Azure, Cloudflare…), caché en memoria,
  métricas de cobertura.
- **Fuera:** geolocalización precisa (opcional, depende de licencia).
- **Cierre:** cobertura ≥ 95 % de bytes con ASN.

## EP-12 · Catálogo de servicios y categorías

- **Objetivo:** `Organization → Service → Category` editable sin despliegue.
- **Alcance:** catálogo versionado (Google, YouTube, Netflix, Facebook, Instagram, WhatsApp,
  TikTok, Steam, PlayStation, Xbox, Cloudflare, AWS, Azure, Google Cloud…), reglas por
  ASN/prefijo/puerto, UI de administración con vista previa del impacto, recálculo de agregados.
- **Cierre:** un cambio en el catálogo se refleja en la clasificación en < 5 min.

## EP-13 · Reputación

- **Objetivo:** contexto de riesgo de IPs y dominios externos.
- **Alcance:** módulo `reputation` de `detection`: ingesta de feeds (abiertos primero; comerciales
  según P-14), puntuación con decaimiento temporal, consulta por IP (`Reputation.Check`). Se separa
  a `services/reputation/` solo si cumple los criterios de [`../services.md`](../services.md).
- **Cierre:** consulta de reputación p95 < 10 ms desde caché.

## EP-14 · Detección de seguridad

- **Objetivo:** hallazgos correlacionados y explicables, no "IP en lista = malware".
- **Alcance:** `IP sospechosa → Reputation → Behavior → Traffic → Detection → Alert`, detectores
  iniciales (escaneo saliente, C2 correlacionado, upload anómalo), nivel de confianza y razones.
  Absorbe el *security-service* de `vision.md` ([ADR-0014](../adr/0014-granularidad-de-microservicios-en-el-mvp.md)).
- **Cierre:** cada detección muestra ≥ 2 señales que la justifican.

## EP-15 · Analítica

- **Objetivo:** responder "qué pasa en mi red" en segundos.
- **Alcance:** dashboards ISP, Cliente y Router de `vision.md` §9 S9; vistas materializadas en
  ClickHouse; componentes ECharts reutilizables.
- **Cierre:** cada dashboard carga en < 2 s p95 con 30 días de datos agregados.

## EP-16 · Scoring residencial/comercial

- **Objetivo:** identificar uso no acorde al plan con explicación.
- **Alcance:** módulo `scoring` de `detection`. Residential/Commercial/Security/Anomaly Score, variables de `vision.md` §9 S10,
  razones legibles, calibración contra planes conocidos, umbrales configurables.
- **Cierre:** precisión validada por el PO sobre una muestra revisada manualmente (P-04).

## EP-17 · Alertas y notificaciones

- **Objetivo:** avisar a la persona correcta por el canal correcto, sin ruido.
- **Alcance:** reglas, ciclo de vida, deduplicación, agrupación, silencios, escalado simple;
  canales Web, Email, Telegram; después WhatsApp, SMS, Webhook.
- **Cierre:** ninguna alerta duplicada para el mismo incidente en la prueba de caída de sitio.

## EP-18 · Reportes

- **Objetivo:** información exportable para gerencia, ventas y auditoría.
- **Alcance:** rol `reporting-worker` de `analytics`: consumo por cliente/categoría/router/ASN,
  seguridad, disponibilidad, anomalías, posibles comerciales; PDF, CSV, Excel; programados;
  almacenados en MinIO (`horus-reports`, lifecycle 7 días).
- **Cierre:** reporte mensual de disponibilidad generado y descargado por un gerente.

## EP-19 · Almacenamiento histórico y archivado

- **Objetivo:** retener lo que vale y poder recuperarlo.
- **Alcance:** niveles raw (7 días por defecto) / agregado / diario, job `archiver` de
  `analytics` (`ClickHouse → Parquet → MinIO → NAS`), checksums, lifecycle, object lock en
  auditoría y backups, restauración de rangos, backups y restauración de PostgreSQL y ClickHouse.
  TTL desde S6 y object lock desde S1.
- **Cierre:** restore de prueba de un mes archivado, documentado en
  [`../disaster-recovery.md`](../disaster-recovery.md).

## EP-20 · Resiliencia y alta disponibilidad

- **Objetivo:** degradación correcta y recuperación automática.
- **Alcance:** suite de pruebas de fallo (`vision.md` §9 S14), reintentos, *circuit breakers*,
  reproceso desde NATS, UI degradada ([`../frontend.md`](../frontend.md) §8).
- **Cierre:** todas las pruebas de fallo pasan en CI nocturno.

## EP-21 · Rendimiento y escalabilidad

- **Objetivo:** soportar 1 000 routers.
- **Alcance:** pruebas de carga 10/100/500/1 000 routers, perfiles de CPU/memoria, índices,
  ClickHouse, NATS, caché, WebSockets, frontend.
- **Cierre:** presupuestos de rendimiento cumplidos y publicados.

## EP-22 · Release 1.0 y operación

- **Objetivo:** producto instalable, actualizable y operable por terceros.
- **Alcance:** instalación, actualización, backup/restore, manual de operación, DR, notas de
  versión, endurecimiento final.
- **Cierre:** instalación limpia desde la documentación por alguien ajeno al equipo.

---

## Épicas transversales

### EP-T1 · Observabilidad
Métricas Prometheus, dashboards Grafana, logs en Loki, trazas OpenTelemetry desde el frontend
hasta la base de datos (`vision.md` §6). Cada servicio nuevo nace con RED + health checks.
Detalle en [`../observability.md`](../observability.md).

### EP-T2 · Seguridad transversal
Modelo de amenazas por sprint, gestión de secretos, cabeceras HTTP, CSP, escaneo de dependencias e
imágenes, revisión de historias `area:security`. Detalle en [`../security.md`](../security.md).

### EP-T3 · Sistema de diseño y accesibilidad
Tokens, componentes Nuxt UI configurados, patrones de pantalla, estados, WCAG 2.1 AA, tema
claro/oscuro, i18n. Detalle en [`../frontend.md`](../frontend.md).

### EP-T4 · Calidad y pruebas
Pirámide de tests (Go test, Vitest, integración con compose, Playwright), datos de prueba,
simuladores (SNMP, flujos), pruebas de contrato contra OpenAPI y esquemas de eventos.

### EP-T5 · Tiempo real (WebSocket)
Canal WebSocket en api-gateway, suscripción por tema con autorización, *fan-out* desde NATS,
reconexión y reanudación, indicadores de frescura en la UI. Contrato en [`../api.md`](../api.md)
/ [`../events.md`](../events.md); UX en [`../frontend.md`](../frontend.md) §7.
