# Documentación de Horus Flow

Resultado del **Sprint 0 — Arquitectura**. Sin código de negocio: decisiones, modelos y
contratos que hay que cerrar antes de empezar el Sprint 1. El documento fuente es
[`vision.md`](vision.md).

## Índice

| Documento | Contenido |
| --- | --- |
| [`vision.md`](vision.md) | Plan base: stack, arquitectura, 16 sprints, MVP |
| [`architecture.md`](architecture.md) | C4, flujos síncronos/asíncronos, gateway, escala, **modos de fallo y degradación** |
| [`services.md`](services.md) | Catálogo de servicios del MVP, datos que posee cada uno, APIs y eventos |
| [`adr/`](adr/README.md) | Decisiones de arquitectura 0001–0016 |
| [`database.md`](database.md) | Modelo PostgreSQL por servicio y modelo ClickHouse, volúmenes |
| [`traffic-model.md`](traffic-model.md) | Flujo normalizado y clasificación IP → Prefijo → ASN → Organización → Servicio → Categoría |
| [`storage.md`](storage.md) | MinIO sobre NAS, buckets, retención, archivado |
| [`api.md`](api.md) | Convenciones REST, WebSocket, gRPC; endpoints del MVP |
| [`events.md`](events.md) | Contrato de eventos NATS JetStream: subjects, streams, envelope, catálogo |
| [`security.md`](security.md) | Amenazas, autenticación, RBAC + ACL, secretos, privacidad |
| [`observability.md`](observability.md) | Métricas, logs, trazas, SLOs, health checks |
| [`disaster-recovery.md`](disaster-recovery.md) | RPO/RTO, backups, runbooks, pruebas de caos |
| [`conventions.md`](conventions.md) | Convenciones de código, Git, CI, Docker, Definición de Terminado |
| [`frontend.md`](frontend.md) | Arquitectura de información y UX del frontend Nuxt |
| [`roadmap.md`](roadmap.md) | 16 sprints con hitos, dependencias y ajustes al plan |
| [`backlog/`](backlog/README.md) | Épicas, historias de los sprints 0–3 y [reparto en flujos paralelos](backlog/team.md) |
| [`open-questions/`](open-questions/README.md) | Decisiones pendientes del product owner, con recomendación |

## Decisiones clave del Sprint 0

- **MVP con 6 procesos** ([ADR-0014](adr/0014-granularidad-de-microservicios-en-el-mvp.md)):
  `network-service` se integra en `wireguard` (control + `wireguard-agent` con `NET_ADMIN`);
  security, reputation y detection empiezan como un solo servicio `detection`; `reporting` es un
  worker de `analytics`; la auditoría vive en `auth`.
- **Gateway** ([ADR-0013](adr/0013-api-gateway-propio.md)): Traefik en el borde y un
  `api-gateway` propio en Go que autentica, aplica rate limit y hace fan-out de WebSocket; hace
  proxy HTTP al REST de cada servicio.
- **Eventos** ([ADR-0016](adr/0016-transactional-outbox.md), [`events.md`](events.md)): los
  eventos de dominio salen por transactional outbox y no se pierden si NATS se reinicia; la
  telemetría usa streams separados con pérdida acotada y medida.
- **Clasificación en la ingesta** ([ADR-0015](adr/0015-enriquecimiento-de-flujos-en-ingesta.md)):
  el ingester de `flows` clasifica con un snapshot versionado del catálogo publicado por
  `traffic-intelligence`.
- **Estado del router** ([`api.md`](api.md) §2.6): `snmp` es dueño del estado observado
  (`horus.snmp.router.state_changed`); `devices` calcula el estado efectivo de 8 valores.
- **Agregados de tráfico** en 5 min / 1 h / 1 día con retención de 90 días / 13 meses / 5 años;
  flujos crudos 7 días.

## Integración del coordinador

Al integrar los documentos de los cinco agentes se unificaron: nombres de eventos
(`interfaces_discovered`, `reporting.report.completed`, `handshake_stale/recovered`,
`<dominio>.audit.recorded`), la eliminación de `devices.router.status_changed`, la granularidad
de agregados (5 min), las columnas `seq` y `headers` del outbox, `customer_id` como identificador
de cliente, el nombre de métrica `horus_outbox_pending` y el transporte gateway → servicio
(proxy HTTP con mTLS) en `security.md`. Las decisiones que dependen del product owner quedan en
[`open-questions/README.md`](open-questions/README.md); los ADR nuevos se numeran desde 0017.
