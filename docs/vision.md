# Horus Flow — Visión y plan base

> Documento fuente del proyecto, tal como lo definió el equipo. Todos los demás documentos de
> `docs/` derivan de este. Si algo contradice este documento, se registra como ADR.

Plataforma de inteligencia de red para un ISP: inventario de routers, monitoreo SNMP, gestión de
WireGuard, recolección de flujos (NetFlow/IPFIX/sFlow), clasificación de tráfico por
servicio/categoría, reputación y seguridad, analítica, detección de uso residencial/comercial,
alertas y reportes.

## 1. Stack frontend

- Nuxt 4 + Vue 3 + TypeScript, Nuxt UI, Tailwind CSS, Apache ECharts.
- Pinia solo cuando realmente sea necesario.
- WebSocket para eventos en tiempo real.
- El frontend **nunca** habla directamente con PostgreSQL, ClickHouse, SNMP, WireGuard, etc.

```
Frontend → HTTPS / WebSocket → API Gateway → Servicios
```

## 2. Backend

Go como lenguaje principal (SNMP, ICMP, WireGuard, colectores de tráfico, concurrencia, workers,
APIs, WebSockets, procesos de larga duración, muchos dispositivos simultáneos; binarios pequeños y
despliegue independiente por microservicio).

```
Go
├── REST API (Chi)
├── gRPC interno (gRPC + Protobuf)
├── WebSocket
├── Workers
├── SNMP collectors
├── Flow collectors
└── WireGuard manager
```

## 3. Bases de datos

- **PostgreSQL**: usuarios, roles, ACL, routers, clientes, sitios, WireGuard, configuración,
  categorías, reglas, alertas, auditoría, relaciones entre entidades.
  ("¿Qué router pertenece a este sitio?")
- **ClickHouse**: NetFlow/IPFIX/sFlow, tráfico, consumo, estadísticas, ASN, servicios, categorías,
  históricos, consultas analíticas.
  ("¿Cuánto tráfico generaron los clientes de este sitio durante las últimas 24 horas?")
- **Redis**: caché, sesiones temporales, rate limiting, estados rápidos, locks distribuidos cuando
  sean necesarios.
- **NATS JetStream**: comunicación asíncrona (SNMP → NATS → Analytics → Alerts), para que un
  servicio no tenga que esperar a otro.
- No se usa MongoDB como base principal.

## 4. Almacenamiento

MinIO como capa S3-compatible, inicialmente sobre el NAS:
`Aplicación → S3 API → MinIO → NAS`, y posteriormente `MinIO/NAS → Cloud Storage`.

## 5. Infraestructura

Docker + docker compose; todo reproducible. No se empieza con Kubernetes, pero la arquitectura
debe permitir migrar `Docker → Kubernetes` cuando haga falta escalar horizontalmente.

## 6. Observabilidad (desde el primer sprint)

- Métricas: Prometheus + Grafana (VictoriaMetrics más adelante si el volumen lo justifica).
- Logs: Loki + Grafana.
- Trazas: OpenTelemetry (Frontend → API → Device Service → PostgreSQL, saber dónde falló).

## 7. Seguridad

- Autenticación: OAuth2/OIDC como base. Inicialmente usuario/password con Argon2id, TOTP,
  refresh tokens, sesiones revocables; WebAuthn después.
- Autorización: RBAC + ACL. Permisos tipo `devices.read`, `devices.create`, `devices.update`,
  `devices.delete`, `traffic.read`, `wireguard.read`, `wireguard.write`, `security.read`,
  `security.manage`, `reports.read`, `reports.export`.

## 8. Arquitectura propuesta

```
                         ┌──────────────────┐
                         │     Nuxt 4       │
                         └────────┬─────────┘
                           HTTPS / WebSocket
                         ┌────────▼─────────┐
                         │   API Gateway    │
                         └────────┬─────────┘
          ┌───────────────────────┼──────────────────────┐
     Auth Service           Device Service         Network Service
          │                       │                 WireGuard
          └───────────────┬───────┘
                     NATS JetStream
        ┌─────────────────┼──────────────────┐
   SNMP Service      Flow Collector    Security Service
        └─────────────────┼──────────────────┘
                  Traffic Intelligence
              ┌───────────┼────────────┐
          Reputation   Detection    Analytics
              └───────────┼────────────┘
             ┌────────────┴────────────┐
        PostgreSQL                 ClickHouse
             └────────────┬────────────┘
                        MinIO
                          │
                         NAS
```

## 9. Plan Scrum (sprints de 2 semanas)

No se desarrollan todos los microservicios simultáneamente.

- **Sprint 0 — Arquitectura (1 semana).** Sin código de negocio. Entregables: ADRs, arquitectura
  general, definición de servicios, modelo de datos, eventos, convenciones de API, seguridad,
  almacenamiento, backups, observabilidad, convenciones de código, Git workflow.
  Documentos: `docs/architecture.md, services.md, database.md, api.md, events.md, security.md,
  storage.md, observability.md, disaster-recovery.md, adr/`.
  Al terminar se debe poder responder: ¿qué pasa si ClickHouse se cae? ¿si SNMP deja de
  funcionar? ¿si NATS se reinicia? ¿si un router desaparece? ¿si el NAS deja de responder?
- **Sprint 1 — Plataforma base.** Backend: api-gateway, auth-service, device-service. Frontend:
  login, dashboard base, layout, navegación, menú de usuario. Infra: PostgreSQL, Redis, NATS,
  MinIO. DevOps: Docker Compose, variables de entorno, secrets, Git, CI (lint, tests, builds).
  Resultado: aplicación funcionando aunque todavía no monitoree routers.
- **Sprint 2 — Usuarios, roles y seguridad.** Login, logout, refresh, sesiones, roles, permisos,
  ACL, 2FA, recuperación, auditoría. Frontend: usuarios, roles, permisos, sesiones, auditoría.
- **Sprint 3 — Inventario.** Device Service. Entidades: Site, Router, Device, Interface, IP,
  Credential, Vendor, Model, Firmware. Dashboard: routers online/offline/warning/critical.
  Funciones: registrar, editar, eliminar, tags, ubicación lógica, credenciales, grupos.
- **Sprint 4 — WireGuard.** network-service, wireguard-service: servidores, peers, generación de
  claves, asignación de IP, AllowedIPs, handshake, último contacto, configuración, rotación de
  claves, revocación. El frontend jamás ejecuta comandos WireGuard.
- **Sprint 5 — SNMP.** snmp-service. Device: uptime, CPU, RAM, temperatura, firmware.
  Interfaces: estado, velocidad, RX, TX, errores, drops. Luego adaptadores por fabricante
  (MikroTik, Cisco, Huawei, Juniper, otros).
- **Sprint 6 — Flow Collector.** NetFlow, IPFIX, sFlow. `Router → Flow Collector → NATS →
  ClickHouse`. Primero metadatos de flujo, no captura de paquetes.
- **Sprint 7 — Clasificación de tráfico.** traffic-intelligence-service. Pipeline
  `IP → Prefix → ASN → Organization → Service → Category` (ej. 142.x.x.x → AS32934 → Meta
  Platforms → Instagram → Social). Servicios: Google, YouTube, Netflix, Facebook, Instagram,
  WhatsApp, TikTok, Steam, PlayStation, Xbox, Cloudflare, AWS, Azure, Google Cloud. Sistema de
  clasificación actualizable, no valores quemados en código.
- **Sprint 8 — Reputation + Security.** reputation-service, security-service, detection-service.
  `IP sospechosa → Reputation → Behavior → Traffic → Detection → Alert`. Correlación, no
  "IP aparece en lista = malware".
- **Sprint 9 — Analytics.** Dashboards ISP (tráfico actual, clientes activos, routers activos,
  top categorías/servicios/ASN, alertas), Cliente (consumo diario/mensual, upload/download,
  categorías, servicios, ASN, destinos), Router (CPU, RAM, interfaces, tráfico, errores,
  disponibilidad).
- **Sprint 10 — Detección residencial/comercial.** Scoring, no reglas fijas ("más de X GB =
  comercial"). Residential/Commercial/Security/Anomaly Score. Variables: cantidad de
  dispositivos, conexiones simultáneas, consumo, horario, upload, download, destinos, protocolos,
  patrones, dispositivos detrás del CPE, comportamiento sostenido. Resultado explicable
  (ej. "Posible uso comercial, confianza 87%, razones: …").
- **Sprint 11 — Alertas.** `Alert Rule → Detection → Event → Alert → Notification`. Canales:
  Web, Email, Telegram; después WhatsApp, SMS, Webhook.
- **Sprint 12 — Reportes.** Consumo por cliente/categoría/router/ASN, seguridad,
  disponibilidad, anomalías, posibles clientes comerciales. Exportación PDF, CSV, Excel.
- **Sprint 13 — Storage / histórico.** Flow raw 7–30 días; agregado 6–12 meses; diario 2–5 años.
  `ClickHouse → Archive → MinIO → NAS`. Backups, restauración, checksum, retención, lifecycle.
- **Sprint 14 — Alta disponibilidad y resiliencia.** Pruebas de fallo deliberadas (apagar
  PostgreSQL, ClickHouse, NATS, Redis, MinIO, desconectar NAS, apagar SNMP collector,
  desconectar router). Degradación correcta: sin ClickHouse siguen funcionando login,
  administración, WireGuard, configuración, inventario; al volver, `Collector → NATS →
  procesamiento` continúa.
- **Sprint 15 — Optimización.** Consultas, índices, ClickHouse, NATS, memoria, CPU, latencia,
  WebSockets, frontend, caché. Pruebas de carga con 10, 100, 500 y 1,000 routers.
- **Sprint 16 — Release 1.0.** Documentación, instalación, actualización, backup, restore,
  seguridad, monitoreo, pruebas, manual de operación, disaster recovery, versión estable.

## 10. Herramientas

| Área | Herramienta |
| --- | --- |
| Frontend | Nuxt 4 + TypeScript |
| Backend | Go |
| API | REST (OpenAPI) |
| Comunicación interna | gRPC (Protobuf) |
| Eventos | NATS JetStream |
| DB principal | PostgreSQL |
| Analytics | ClickHouse |
| Cache | Redis |
| Storage | MinIO (NAS como backend S3) |
| SNMP | Go |
| Flow | NetFlow/IPFIX/sFlow |
| WireGuard | Go/integración con el sistema |
| Charts | Apache ECharts |
| CSS/UI | Nuxt UI + Tailwind |
| Containers | Docker |
| CI/CD | GitHub Actions/GitLab CI |
| Métricas / Dashboards / Logs / Trazas | Prometheus / Grafana / Loki / OpenTelemetry |
| Tests | Go test + Playwright |
| Reverse proxy | Nginx/Caddy/Traefik |

## 11. Estructura del repositorio (monorepo)

```
horus-flow/
├── apps/frontend/
├── services/
│   ├── api-gateway/  auth/  devices/  wireguard/  snmp/  flows/
│   ├── traffic-intelligence/  reputation/  detection/  alerts/
│   └── analytics/  reporting/
├── packages/  protobuf/  events/  schemas/
├── infrastructure/  docker/  postgres/  clickhouse/  nats/  redis/  minio/  observability/
├── docs/
├── scripts/
└── deployments/
```

## 12. Metodología

Cada sprint: Planning (objetivo, historias, criterios de aceptación, dependencias, riesgos),
desarrollo (`Code → Lint → Unit Test → Integration Test → Security Check → Build`), daily corta
(¿qué hice? ¿qué haré? ¿qué me bloquea?), review con funcionalidad real, retrospectiva.

## 13. Definición de terminado

Código, tests, manejo de errores, logs, métricas, seguridad, documentación, API documentada,
migración DB si aplica, Docker, CI/CD, health check, backup si aplica, revisión.

## 14. MVP técnico

```
Nuxt 4 → API Gateway → { Auth, Devices, WireGuard } → NATS → SNMP → PostgreSQL
```

Después: `Flow → ClickHouse → Traffic Intelligence → Categories → Analytics → Security`.
Así se valida la arquitectura antes de meter millones de registros de tráfico.

## Decisión clave

No se definen todavía todos los microservicios a nivel definitivo. Primero se cierran tres
documentos técnicos:

1. Modelo de datos.
2. Contrato de eventos NATS.
3. Modelo de tráfico y clasificación:

```
IP → Prefix → ASN → Organization → Service → Category → Reputation
   → Client → Router → Interface → Time → Bytes → Packets
```
