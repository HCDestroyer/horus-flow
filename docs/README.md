# Documentación de Horus Flow

Diseño de Horus Flow antes de escribir código. Dos rondas:

1. **Sprint 0** — arquitectura a partir del plan base ([`vision.md`](vision.md)).
2. **Ronda 2** — revisión completa con las decisiones del product owner
   ([`po-decisions.md`](po-decisions.md), D1–D10), que **prevalecen** sobre todo lo anterior.

## Índice

| Documento | Contenido |
| --- | --- |
| [`po-decisions.md`](po-decisions.md) | Decisiones del product owner D1–D10 |
| [`vision.md`](vision.md) | Plan base original (histórico; lo matizan las decisiones del PO) |
| [`architecture.md`](architecture.md) | C4, binario modular con roles, multi-tenant, escala, **modos de fallo y degradación** |
| [`services.md`](services.md) | Módulos del binario `horus`, datos que posee cada uno, APIs y eventos |
| [`adr/`](adr/README.md) | Decisiones de arquitectura 0001–0029 |
| [`database.md`](database.md) | PostgreSQL multi-tenant con RLS, cliente = IP, modelo ClickHouse, volúmenes |
| [`traffic-model.md`](traffic-model.md) | Flujo normalizado, atribución por prefijos del nodo, clasificación, señales de botnet y de uso comercial |
| [`storage.md`](storage.md) | Almacenamiento local, destino remoto opcional con rclone (SFTP primero), retención |
| [`api.md`](api.md) | REST, token por ISP, WebSocket, gRPC, kioscos, dashboards; endpoints |
| [`events.md`](events.md) | Contrato de eventos NATS JetStream: subjects, streams, envelope, catálogo |
| [`security.md`](security.md) | Aislamiento entre ISPs, autenticación, RBAC, secretos, kiosco, privacidad |
| [`observability.md`](observability.md) | Métricas, logs, trazas, SLOs, health checks, alertas por ISP |
| [`disaster-recovery.md`](disaster-recovery.md) | RPO/RTO, backups locales + remoto, runbooks |
| [`conventions.md`](conventions.md) | Código, monorepo, flujo de trabajo de agentes de IA, CI, Definición de Terminado |
| [`vendors/mikrotik.md`](vendors/mikrotik.md) | Integración MikroTik RouterOS v7: Traffic Flow, SNMP, API, WireGuard, onboarding, laboratorio CHR |
| [`frontend.md`](frontend.md) | UI multi-ISP, dashboard modular, modo NOC/kiosco, clientes, hallazgos |
| [`roadmap.md`](roadmap.md) | Plan por incrementos de valor (I0–I6) y trazabilidad con los sprints originales |
| [`backlog/`](backlog/README.md) | Épicas, historias de [I0](backlog/increment-0.md) e [I1](backlog/increment-1.md), [reparto entre agentes](backlog/team.md) |
| [`open-questions/`](open-questions/README.md) | Decisiones pendientes del product owner, con recomendación |

## Decisiones clave

- **Multi-tenant desde v1** ([ADR-0017](adr/0017-multi-tenant-desde-v1.md)): tenant = ISP;
  ISP → nodo → router principal → prefijos de clientes → IPs. Token de acceso por ISP, RLS en
  PostgreSQL, row policies en ClickHouse, cabecera `Horus-Tenant` en NATS
  ([ADR-0027](adr/0027-tenant-en-cabecera-nats-y-subjects-de-trabajo.md)).
- **La IP es el cliente** ([ADR-0018](adr/0018-la-ip-es-el-cliente.md)): se descubre sola desde
  los flujos, residencial por defecto; el scoring sugiere uso comercial y un humano puede fijarlo.
- **Detección de botnets como objetivo principal**
  ([ADR-0024](adr/0024-deteccion-de-botnets-como-objetivo-principal.md)).
- **Un binario `horus` con roles** ([ADR-0025](adr/0025-binario-modular-con-roles.md)): perfil
  mínimo de 3 contenedores propios (`horus-app`, `horus-collector`, `horus-wg-agent`) más
  PostgreSQL, ClickHouse, NATS y Valkey.
- **Almacenamiento local** con copia remota opcional cifrada por rclone
  ([ADR-0019](adr/0019-almacenamiento-local-y-destino-remoto.md),
  [ADR-0029](adr/0029-copias-locales-siempre-y-paquete-de-secretos-offline.md)). Sin MinIO ni NAS
  obligatorio.
- **MikroTik RouterOS v7 primero**, solo lectura, alta con token de un solo uso por WireGuard
  ([ADR-0022](adr/0022-mikrotik-routeros-v7-primer-fabricante.md)).
- **Dashboard modular y kiosco registrado** para las pantallas del NOC
  ([ADR-0026](adr/0026-kiosco-como-dispositivo-registrado.md)).
- **Entrega por incrementos** con agentes de IA y verificación automática
  ([ADR-0023](adr/0023-entrega-por-incrementos-y-equipo-ia.md),
  [ADR-0028](adr/0028-flujo-de-trabajo-de-agentes-de-ia.md)). Primer entregable: **I1 — NOC de un
  nodo MikroTik** ([`roadmap.md`](roadmap.md)).
