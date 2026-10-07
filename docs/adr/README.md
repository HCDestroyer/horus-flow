# Registro de decisiones de arquitectura (ADR)

Formato MADR corto, en español. Reglas de uso en [0001](0001-registrar-decisiones-con-adr.md).
Plantilla: copiar cualquier ADR y mantener las secciones **Contexto, Decisión, Alternativas
consideradas, Consecuencias**.

| # | Título | Estado | Área |
| --- | --- | --- | --- |
| [0001](0001-registrar-decisiones-con-adr.md) | Registrar decisiones con ADR | Aceptada | Proceso |
| [0002](0002-monorepo.md) | Monorepo | Aceptada | Repositorio |
| [0003](0003-go-para-backend.md) | Go para el backend | Aceptada | Lenguaje |
| [0004](0004-chi-para-rest.md) | Chi para REST (+ OpenAPI) | Aceptada | API |
| [0005](0005-grpc-protobuf-interno.md) | gRPC + Protobuf para comunicación interna | Aceptada | Comunicación |
| [0006](0006-nats-jetstream-bus-de-eventos.md) | NATS JetStream como bus de eventos (+ KV) | Aceptada | Mensajería |
| [0007](0007-postgresql-fuente-de-verdad.md) | PostgreSQL como fuente de verdad transaccional | Aceptada | Datos |
| [0008](0008-clickhouse-para-analitica.md) | ClickHouse para analítica (tablas publicadas, escritor único) | Aceptada | Datos |
| [0009](0009-redis.md) | Redis como caché efímero | Aceptada | Datos |
| [0010](0010-minio-sobre-nas.md) | MinIO (S3) sobre el NAS | Aceptada | Almacenamiento |
| [0011](0011-docker-compose-antes-que-kubernetes.md) | Docker Compose antes que Kubernetes | Aceptada | Infraestructura |
| [0012](0012-nuxt4-nuxt-ui.md) | Nuxt 4 + Nuxt UI (SPA) | Aceptada | Frontend |
| [0013](0013-api-gateway-propio.md) | API Gateway propio en Go detrás de Traefik | Propuesta | Borde |
| [0014](0014-granularidad-de-microservicios-en-el-mvp.md) | Granularidad de microservicios en el MVP | Propuesta | Servicios |
| [0015](0015-enriquecimiento-de-flujos-en-ingesta.md) | Enriquecimiento de flujos en la ingesta | Propuesta | Flujos |
| [0016](0016-transactional-outbox.md) | Transactional outbox para eventos de dominio | Propuesta | Mensajería |

"Aceptada" en Sprint 0 = elección ya fijada en [vision.md](../vision.md) y justificada aquí.
"Propuesta" = decisión nueva o que cuestiona el plan; requiere aprobación del equipo/PO en la
review del Sprint 0. Los ADR que aporten otros agentes (seguridad, eventos, datos) continúan la
numeración a partir de **0017**; el coordinador asigna números para evitar colisiones.
