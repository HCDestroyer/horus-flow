# Sprint 0 — Arquitectura (1 semana)

- **Objetivo del sprint:** cerrar las decisiones de arquitectura, datos, contratos, seguridad,
  operación y producto necesarias para que cuatro flujos trabajen en paralelo desde el Sprint 1
  sin reescrituras, y poder responder las 5 preguntas de fallo de [`../vision.md`](../vision.md) §9.
- **Épica:** EP-00 (más EP-T1, EP-T2, EP-T3 en sus documentos).
- **Sin código de aplicación.** Se permite el esqueleto de directorios del monorepo y archivos de
  configuración del repositorio (S00-13).
- **Ejecución:** 5 agentes en paralelo + coordinador. Capacidad comprometida: 63 puntos.

| ID | Historia | Responsable | Área | Pts | Prioridad |
| --- | --- | --- | --- | --- | --- |
| S00-01 | Arquitectura general y ADRs iniciales | Agente 1 | backend, infra | 5 | Must |
| S00-02 | Definición de servicios | Agente 1 | backend | 5 | Must |
| S00-03 | Modelo de datos PostgreSQL y ClickHouse | Agente 2 | data | 8 | Must |
| S00-04 | Modelo de tráfico y clasificación | Agente 2 | data | 5 | Must |
| S00-05 | Almacenamiento (MinIO/NAS, retención) | Agente 2 | data, infra | 3 | Must |
| S00-06 | Convenciones de API y contrato WebSocket | Agente 3 | backend, frontend | 5 | Must |
| S00-07 | Contrato de eventos NATS | Agente 3 | backend, data | 5 | Must |
| S00-08 | Seguridad (authn, authz, secretos, amenazas) | Agente 4 | security | 5 | Must |
| S00-09 | Observabilidad | Agente 4 | infra | 3 | Must |
| S00-10 | Disaster recovery y backups | Agente 4 | infra | 3 | Must |
| S00-11 | Convenciones de código y Git workflow | Agente 4 | infra | 3 | Must |
| S00-12 | Roadmap, backlog, reparto en paralelo, UX frontend y preguntas al PO | Agente 5 | frontend | 8 | Must |
| S00-13 | Esqueleto del monorepo | Coordinador | infra | 2 | Should |
| S00-14 | Revisión cruzada y respuesta a modos de fallo | Coordinador + todos | — | 3 | Must |

---

### S00-01 · Arquitectura general y ADRs iniciales
- **Épica:** EP-00 · **Área:** backend, infra · **Pts:** 5 · **Depende de:** —
- **Entregables:** [`../architecture.md`](../architecture.md), [`../adr/`](../adr/).

**Como** equipo de desarrollo **queremos** una arquitectura general con sus decisiones
justificadas **para** que todos construyamos sobre los mismos supuestos.

1. **Dado** `vision.md` §8, **cuando** se publica `architecture.md`, **entonces** incluye
   diagrama de contexto y de contenedores, flujos síncronos (REST/gRPC) y asíncronos (NATS) y
   fronteras de confianza.
2. **Dado** cada decisión difícil de revertir (monorepo, Chi, gRPC interno, NATS JetStream,
   ClickHouse, compose sin K8s, modelo de autenticación), **cuando** se revisa `adr/`,
   **entonces** existe un ADR con contexto, decisión, alternativas y consecuencias.
3. **Dado** los conflictos C-01 (network/security-service) y C-03 (series SNMP) de
   [`../roadmap.md`](../roadmap.md) §7, **cuando** termina el sprint, **entonces** hay un ADR que
   los resuelve o quedan registrados en `open-questions/` con dueño.

### S00-02 · Definición de servicios
- **Épica:** EP-00 · **Área:** backend · **Pts:** 5 · **Depende de:** S00-01
- **Entregable:** [`../services.md`](../services.md).

**Como** desarrollador backend **quiero** saber qué hace cada servicio, qué datos posee y con quién
habla **para** no duplicar responsabilidades.

1. **Dado** los 12 servicios acordados, **cuando** leo `services.md`, **entonces** cada uno tiene
   responsabilidad, datos de los que es dueño, APIs expuestas, eventos publicados/consumidos,
   dependencias y comportamiento si cada dependencia cae.
2. **Dado** el MVP técnico, **cuando** leo `services.md`, **entonces** queda claro qué servicios
   existen en S1–S5 y cuáles se crean después.
3. **Dado** el sondeo ICMP de S3 (C-04), **cuando** leo `services.md`, **entonces** está asignado a
   un servicio concreto.

### S00-03 · Modelo de datos
- **Épica:** EP-00 · **Área:** data · **Pts:** 8 · **Depende de:** S00-02
- **Entregable:** [`../database.md`](../database.md).

**Como** desarrollador **quiero** el modelo de datos de PostgreSQL y ClickHouse **para** escribir
migraciones sin rediseñar en cada sprint.

1. **Dado** las entidades de S1–S5 (usuarios, roles, permisos, sesiones, auditoría, sitios,
   routers, interfaces, IPs, credenciales, vendors, modelos, firmware, WireGuard), **cuando** leo
   `database.md`, **entonces** tienen tablas, claves (UUIDv7), relaciones, índices y dueño.
2. **Dado** la entidad Cliente y su asignación IP con vigencia (C-02), **cuando** leo
   `database.md`, **entonces** está modelada o registrada como pregunta abierta.
3. **Dado** ClickHouse, **cuando** leo `database.md`, **entonces** hay tablas de flujos crudos y
   agregados con motor, clave de orden, particionado y TTL.

### S00-04 · Modelo de tráfico y clasificación
- **Épica:** EP-00 · **Área:** data · **Pts:** 5 · **Depende de:** S00-03
- **Entregable:** [`../traffic-model.md`](../traffic-model.md).

**Como** ingeniero de datos **quiero** el modelo `IP → Prefix → ASN → Organization → Service →
Category → Reputation → Client → Router → Interface → Time → Bytes → Packets` **para** diseñar
ingesta y agregados.

1. **Dado** un flujo NetFlow/IPFIX/sFlow, **cuando** leo el documento, **entonces** sé qué campos
   se normalizan, cómo se enriquecen y en qué orden.
2. **Dado** el requisito "clasificación actualizable" (`vision.md` §9 S7), **cuando** leo el
   documento, **entonces** se describen el versionado del catálogo y qué pasa con datos ya
   clasificados cuando cambia.
3. **Dado** el muestreo de sFlow/NetFlow, **cuando** leo el documento, **entonces** se explica cómo
   se escalan bytes y paquetes.

### S00-05 · Almacenamiento
- **Épica:** EP-00 · **Área:** data, infra · **Pts:** 3 · **Depende de:** S00-03
- **Entregable:** [`../storage.md`](../storage.md).

1. **Dado** `MinIO → NAS → Cloud`, **cuando** leo `storage.md`, **entonces** hay buckets,
   convenciones de claves, retención por nivel y comportamiento si el NAS no responde.

### S00-06 · Convenciones de API y WebSocket
- **Épica:** EP-00, EP-T5 · **Área:** backend, frontend · **Pts:** 5 · **Depende de:** S00-02
- **Entregable:** [`../api.md`](../api.md).

**Como** desarrollador frontend **quiero** convenciones REST y WebSocket estables **para** trabajar
contra mocks generados desde OpenAPI desde el Sprint 1.

1. **Dado** `/api/v1/...` y JSON `snake_case`, **cuando** leo `api.md`, **entonces** están
   definidos paginación, filtros, ordenación, formato de error, idempotencia, versionado y
   códigos HTTP.
2. **Dado** el WebSocket, **cuando** leo `api.md`, **entonces** están definidos autenticación,
   sobre de mensaje, suscripción por tema, reconexión y cómo se reanuda sin perder eventos
   (necesidades de UI en [`../frontend.md`](../frontend.md) §7).
3. **Dado** un endpoint de estado del sistema, **cuando** leo `api.md`, **entonces** existe
   (p. ej. `GET /api/v1/system/status`) para que la UI muestre modos degradados
   ([`../frontend.md`](../frontend.md) §8.4).

### S00-07 · Contrato de eventos NATS
- **Épica:** EP-00 · **Área:** backend, data · **Pts:** 5 · **Depende de:** S00-02
- **Entregable:** [`../events.md`](../events.md).

1. **Dado** la convención `horus.<dominio>.<entidad>.<evento>`, **cuando** leo `events.md`,
   **entonces** hay catálogo de subjects de S1–S6, sobre común, versionado de esquemas, streams
   JetStream, retención, entrega (al menos una vez) e idempotencia en consumidores.
2. **Dado** "¿qué pasa si NATS se reinicia?", **cuando** leo `events.md`, **entonces** está la
   respuesta.

### S00-08 · Seguridad
- **Épica:** EP-00, EP-T2 · **Área:** security · **Pts:** 5 · **Depende de:** S00-01
- **Entregable:** [`../security.md`](../security.md).

1. **Dado** `vision.md` §7, **cuando** leo `security.md`, **entonces** hay: flujo de tokens
   (duración, rotación, revocación), Argon2id con parámetros, TOTP, catálogo de permisos
   inicial, modelo ACL, cifrado de credenciales de routers y claves WireGuard, gestión de secretos,
   y modelo de amenazas inicial.

### S00-09 · Observabilidad
- **Épica:** EP-00, EP-T1 · **Área:** infra · **Pts:** 3
- **Entregable:** [`../observability.md`](../observability.md).

1. **Dado** un request desde el frontend, **cuando** leo el documento, **entonces** sé cómo se
   propaga la traza hasta PostgreSQL, qué métricas y logs emite cada servicio y qué alertas
   operativas existen.

### S00-10 · Disaster recovery
- **Épica:** EP-00 · **Área:** infra · **Pts:** 3
- **Entregable:** [`../disaster-recovery.md`](../disaster-recovery.md).

1. **Dado** cada almacén de datos, **cuando** leo el documento, **entonces** tiene RPO, RTO,
   método de backup, frecuencia, verificación y procedimiento de restauración.

### S00-11 · Convenciones de código y Git workflow
- **Épica:** EP-00 · **Área:** infra · **Pts:** 3
- **Entregable:** [`../conventions.md`](../conventions.md).

1. **Dado** Go y TypeScript, **cuando** leo `conventions.md`, **entonces** hay linters, formato,
   estructura de servicio, ramas, commits, plantilla de PR con la DoD
   ([`README.md`](README.md) §8) y CODEOWNERS por flujo ([`team.md`](team.md)).

### S00-12 · Planificación de producto y UX frontend
- **Épica:** EP-00, EP-T3 · **Área:** frontend · **Pts:** 8
- **Entregables:** [`../roadmap.md`](../roadmap.md), [`README.md`](README.md),
  [`epics.md`](epics.md), `sprint-00..03.md`, [`team.md`](team.md),
  [`../frontend.md`](../frontend.md), [`../open-questions/product.md`](../open-questions/product.md).

1. **Dado** el plan de 16 sprints, **cuando** se lee el roadmap, **entonces** cada sprint tiene
   objetivo, entregable demostrable, dependencias, riesgos y los ajustes propuestos están
   justificados.
2. **Dado** el Sprint 1, **cuando** se lee el backlog, **entonces** todas sus historias cumplen la
   DoR.
3. **Dado** cuatro flujos paralelos, **cuando** se lee `team.md`, **entonces** cada flujo sabe qué
   hacer en S1–S5 y qué contratos necesita antes.

### S00-13 · Esqueleto del monorepo
- **Épica:** EP-01 · **Área:** infra · **Pts:** 2 · **Depende de:** S00-11
- **Tipo:** task

1. **Dado** `vision.md` §11, **cuando** se clona el repo, **entonces** existen los directorios con un
   README de una línea cada uno, `.editorconfig`, `.gitignore` y CODEOWNERS.

### S00-14 · Revisión cruzada y modos de fallo
- **Épica:** EP-00 · **Pts:** 3 · **Depende de:** S00-01…S00-12

**Como** PO **quiero** comprobar que los documentos son coherentes y responden los modos de fallo
**para** aprobar el inicio del Sprint 1.

1. **Dado** los documentos de S0, **cuando** se revisan en conjunto, **entonces** no hay
   contradicciones en nombres de servicio, subjects, permisos ni entidades (o están registradas).
2. **Dado** las preguntas "¿qué pasa si ClickHouse se cae / SNMP deja de funcionar / NATS se
   reinicia / un router desaparece / el NAS deja de responder?", **cuando** se hace la review,
   **entonces** cada respuesta cita el documento y la sección, e incluye el efecto visible en la
   UI ([`../frontend.md`](../frontend.md) §8.4).
3. **Dado** las preguntas al PO, **cuando** termina el sprint, **entonces** las marcadas "bloquea
   S1" están respondidas o tienen supuesto aceptado.
