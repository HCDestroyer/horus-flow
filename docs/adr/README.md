# Registro de decisiones de arquitectura (ADR)

Formato MADR corto, en español. Reglas de uso en [0001](0001-registrar-decisiones-con-adr.md).
Plantilla: copiar cualquier ADR y mantener las secciones **Contexto, Decisión, Alternativas
consideradas, Consecuencias**.

Las decisiones del product owner ([`po-decisions.md`](../po-decisions.md), D1–D10) **prevalecen**
sobre cualquier ADR anterior; los ADR 0017–0025 las registran.

| # | Título | Estado | Área |
| --- | --- | --- | --- |
| [0001](0001-registrar-decisiones-con-adr.md) | Registrar decisiones con ADR | Aceptada | Proceso |
| [0002](0002-monorepo.md) | Monorepo | Aceptada (modificada por [0025](0025-binario-modular-con-roles.md)) | Repositorio |
| [0003](0003-go-para-backend.md) | Go para el backend | Aceptada | Lenguaje |
| [0004](0004-chi-para-rest.md) | Chi para REST (+ OpenAPI) | Aceptada | API |
| [0005](0005-grpc-protobuf-interno.md) | gRPC + Protobuf para comunicación interna | Aceptada | Comunicación |
| [0006](0006-nats-jetstream-bus-de-eventos.md) | NATS JetStream como bus de eventos (+ KV) | Aceptada | Mensajería |
| [0007](0007-postgresql-fuente-de-verdad.md) | PostgreSQL como fuente de verdad transaccional | Aceptada (ajustada por [0017](0017-multi-tenant-desde-v1.md), [0019](0019-almacenamiento-local-y-destino-remoto.md)) | Datos |
| [0008](0008-clickhouse-para-analitica.md) | ClickHouse para analítica (tablas publicadas, escritor único) | Aceptada (ampliada por [0021](0021-clickhouse-desde-el-primer-incremento.md)) | Datos |
| [0009](0009-redis.md) | Redis como caché efímero | **Sustituido por [0020](0020-valkey-en-lugar-de-redis.md)** | Datos |
| [0010](0010-minio-sobre-nas.md) | MinIO (S3) sobre el NAS | **Sustituido por [0019](0019-almacenamiento-local-y-destino-remoto.md)** | Almacenamiento |
| [0011](0011-docker-compose-antes-que-kubernetes.md) | Docker Compose antes que Kubernetes | Aceptada | Infraestructura |
| [0012](0012-nuxt4-nuxt-ui.md) | Nuxt 4 + Nuxt UI (SPA) | Aceptada | Frontend |
| [0013](0013-api-gateway-propio.md) | API Gateway propio en Go detrás de Traefik | Propuesta (ajustada por [0025](0025-binario-modular-con-roles.md)) | Borde |
| [0014](0014-granularidad-de-microservicios-en-el-mvp.md) | Granularidad de microservicios en el MVP | **Sustituido por [0025](0025-binario-modular-con-roles.md)** | Servicios |
| [0015](0015-enriquecimiento-de-flujos-en-ingesta.md) | Enriquecimiento de flujos en la ingesta | Propuesta (ajustada por [0019](0019-almacenamiento-local-y-destino-remoto.md)) | Flujos |
| [0016](0016-transactional-outbox.md) | Transactional outbox para eventos de dominio | Propuesta | Mensajería |
| [0017](0017-multi-tenant-desde-v1.md) | Multi-tenant desde v1 (tenant = ISP), RLS, token por tenant | Aceptada (D6) | Tenancy |
| [0018](0018-la-ip-es-el-cliente.md) | La IP es el cliente: descubrimiento automático desde los flujos | Aceptada (D1) | Dominio |
| [0019](0019-almacenamiento-local-y-destino-remoto.md) | Almacenamiento local primario + destino remoto opcional (rclone, SFTP primero) | Aceptada (D2, D3) | Almacenamiento |
| [0020](0020-valkey-en-lugar-de-redis.md) | Valkey en lugar de Redis | Aceptada (D3) | Datos |
| [0021](0021-clickhouse-desde-el-primer-incremento.md) | ClickHouse desde el primer incremento con series o flujos | Aceptada (D4) | Datos |
| [0022](0022-mikrotik-routeros-v7-primer-fabricante.md) | MikroTik RouterOS v7 primero; adaptadores por capacidad; enrolment con token | Aceptada (D10) | Fabricantes |
| [0023](0023-entrega-por-incrementos-y-equipo-ia.md) | Entrega por incrementos; desarrollo por agentes de IA + 1 persona | Aceptada (D7, D9) | Proceso |
| [0024](0024-deteccion-de-botnets-como-objetivo-principal.md) | Detección de clientes en botnets como objetivo de primer nivel | Aceptada (D5) | Seguridad |
| [0025](0025-binario-modular-con-roles.md) | Monolito modular: un binario `horus` con roles | Aceptada | Servicios / despliegue |

"Aceptada" en Sprint 0 = elección ya fijada en [vision.md](../vision.md) y justificada aquí.
"Propuesta" = decisión que cuestiona el plan y espera aprobación del PO. "Aceptada (D#)" = registra
una decisión del product owner. Un ADR sustituido no se edita salvo su estado.

Numeración: los números **0017–0029** están reservados para el Agente A (arquitectura) en la
ronda 2; el resto de agentes no crea ADRs y el coordinador asigna los siguientes.
