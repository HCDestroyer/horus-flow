# Reparto del trabajo en paralelo

Cómo dividir el desarrollo en flujos paralelos a partir del Sprint 1, qué contratos deben estar
cerrados para que no se bloqueen entre sí y cómo se sincronizan. Sirve igual para equipos humanos
que para agentes de IA (un agente por flujo, §6).

Relacionado: [`../roadmap.md`](../roadmap.md) (§6 paralelización), [`README.md`](README.md)
(etiqueta `stream:`), [`../conventions.md`](../conventions.md) (Git workflow, CODEOWNERS).

## 1. Principios

1. **Contrato antes que código.** Ningún flujo implementa contra un contrato de otro que no esté
   aprobado en borrador. Mientras tanto, se trabaja contra mocks generados del contrato.
2. **Propiedad por directorio.** Cada flujo es dueño de directorios concretos del monorepo; tocar
   los de otro requiere su aprobación (CODEOWNERS).
3. **Integración continua, no al final.** Todo se integra en `main` a diario detrás de flags si
   hace falta; la demo de la review sale de `main`.
4. **Un flujo, un objetivo por sprint.** Cada flujo tiene su propio objetivo que contribuye al
   objetivo del sprint.

## 2. Flujos

| Flujo | Misión | Dueño de (directorios) | Servicios |
| --- | --- | --- | --- |
| **P — Plataforma / DevOps / SRE** | Entorno reproducible, CI/CD, observabilidad, secretos, backups, resiliencia, carga | `infrastructure/`, `deployments/`, `scripts/`, `.github/` (o `.gitlab-ci.yml`) | — (transversal) |
| **B — Backend core** | Identidad, autorización, auditoría, inventario, clientes y adaptador IP↔cliente, WireGuard (control + agente), gateway, tiempo real, alertas | `services/api-gateway`, `services/auth`, `services/devices`, `services/wireguard` (incl. `cmd/wireguard-agent`), `services/alerts` | api-gateway, auth, devices, wireguard, wireguard-agent, alerts |
| **F — Frontend** | Aplicación Nuxt, sistema de diseño, accesibilidad, e2e | `apps/frontend` | — |
| **D — Datos / colectores** | Sondeo (ICMP/SNMP) y estado observado, flujos, ClickHouse, enriquecimiento, clasificación, detección (reputación, correlación, scoring), analítica, reportes y archivado | `services/snmp`, `services/flows`, `services/traffic-intelligence`, `services/detection`, `services/analytics` (incl. roles `reporting-worker` y `archiver`), `infrastructure/clickhouse` | snmp, flows (collector, ingester), traffic-intelligence, detection, analytics |
| *(Rol)* **Coordinación / contratos** | Dueño de `packages/` (protobuf, events, schemas) y de `docs/`; revisa PRs con `contract:*`; PO proxy | `packages/`, `docs/` | — |

Unidades desplegables según [ADR-0014](../adr/0014-granularidad-de-microservicios-en-el-mvp.md):
`services/reputation/` y `services/reporting/` no existen hasta que se separen; viven como
módulos de `detection` y `analytics`.

Con más personas, B se divide en **B1 Identidad** (auth, gateway) y **B2 Red** (devices,
wireguard, adaptador de clientes), y D en **D1 Colectores** (snmp, flows) y **D2 Inteligencia**
(traffic-intelligence, detection, analytics). Un quinto flujo opcional, **S — Seguridad/QA**, revisa todo lo
etiquetado `area:security` y mantiene las suites de matriz de permisos y fallos; si no existe, ese
rol lo asume P.

## 3. Qué hace cada flujo en S1–S5

| Sprint | P — Plataforma | B — Backend core | F — Frontend | D — Datos |
| --- | --- | --- | --- | --- |
| **S1** | Compose (PG, Redis, NATS, MinIO), secretos, CI, observabilidad, reverse proxy (S01-01…05) | Plantilla Go, api-gateway, login/refresh/logout/me, devices esqueleto, WS base (S01-06…09, 11, 12) | Nuxt + Nuxt UI, login, layout, menú, dashboard base, cliente API, mocks, e2e (S01-13…19, 21) | Migraciones y usuario semilla, simulador SNMP (S01-10, 20); revisión de `database.md`/`events.md` |
| **S2** | Backup PG + verificación, matriz de permisos en CI, escaneo de seguridad | Permisos, usuarios, roles, sesiones, TOTP, reset asistido, auditoría, motor ACL | Usuarios, roles, sesiones, auditoría, login TOTP, Mi cuenta, `usePermission` | Consumidor de auditoría (outbox → PG), perfil ClickHouse, **poller SNMP contra simulador** (prototipo, sin integrar) |
| **S3** | Spike WireGuard (agente) en contenedor, capability ICMP, entorno de *staging* | Catálogo, sitios, routers, credenciales cifradas, tags/grupos, CSV, ACL aplicada, Cliente y asignaciones IP manuales/CSV, proyección de estado y `status_changed` | Lista/detalle/alta de routers, sitios y grupos, Resumen en vivo | **Servicio `snmp` con solo ICMP** + eventos de alcanzabilidad, seed, perfil ClickHouse |
| **S4** | `wireguard-agent` en compose (`network_mode: host`, `CAP_NET_ADMIN`, mTLS), pruebas de fallo de NATS, staging con routers de laboratorio | `wireguard` (control): servidores, peers, claves, IPAM, rotación, revocación; gRPC con el agente; eventos de handshake. **Spike de fuente IP↔cliente** con el PO | Pantallas WireGuard (servidores, peers, alta con descarga/QR única, estado de handshake en vivo); pestaña WG del router | `snmp`: polling MIB-II/IF-MIB/HOST-RESOURCES contra simulador y 1–2 routers reales, publicación en NATS; `metrics-writer` a ClickHouse si se aprueba C-03 |
| **S5 (MVP)** | Pruebas de fallo (snmp caído, router desaparece, NATS reinicia) en CI nocturno; simulador a 100 routers; dashboards Grafana y reglas Alertmanager del MVP | Notificaciones en vivo mínimas (EP-09), fan-out WS de métricas/estado con ACL, **adaptador IP↔cliente #1 (módulo de `devices`)**, endurecimiento | Detalle de router: pestañas Métricas e Interfaces con ECharts en vivo; centro de notificaciones; estados degradados | Adaptador MikroTik, estado observado completo (degraded/warning/critical con razón, correlación con handshake WG), descubrimiento de interfaces, **spike de datasets ASN/Org** (§4.3 roadmap), simulador de flujos |

A partir de S6 el reparto sigue el diagrama de [`../roadmap.md`](../roadmap.md) §6: D lleva la
carga principal (flows con pre-agregación, clasificación, detection, analytics, reporting,
archivado), B lleva el adaptador IP↔cliente en producción, alerts (S11) y el endurecimiento de
identidad/gateway, F construye analítica sobre componentes de gráficas preparados en S5–S8,
P lleva retención, carga y resiliencia.

## 4. Contratos que deben estar cerrados

"Cerrado" = PR mergeado en `packages/` o `docs/` con aprobación del flujo productor **y** de cada
flujo consumidor. Cambios posteriores incompatibles requieren nueva versión.

| Contrato | Dónde | Productor → Consumidores | Cerrado antes de |
| --- | --- | --- | --- |
| Convenciones REST (errores, paginación, filtros, auth) | [`../api.md`](../api.md) | B → F | Inicio S1 (S0) |
| OpenAPI auth (`login`, `refresh`, `logout`, `me`) | `services/auth/api/openapi.yaml` (ruta según `conventions.md`) | B → F | Día 2 de S1 |
| Protocolo WebSocket (sobre, temas, reconexión) | [`../api.md`](../api.md) | B → F | Inicio S1 (borrador), día 5 de S1 (cerrado) |
| Sobre común de eventos y versionado | [`../events.md`](../events.md), `packages/events` | Coord. → B, D | Inicio S1 |
| Esquema PG de identidad | [`../database.md`](../database.md) + migraciones | D/B → B | Inicio S1 |
| Catálogo de permisos | [`../security.md`](../security.md) + `S02-01` | B → F, todos | Día 2 de S2 |
| OpenAPI usuarios/roles/sesiones/auditoría | `services/auth` | B → F | Semana 2 de S1 (sync de contratos) |
| OpenAPI inventario (routers, sitios, credenciales, grupos, clientes) | `services/devices` | B → F, D | Semana 1 de S2 |
| Eventos `horus.devices.router.*`, `horus.snmp.router.*` y `status_changed` | `packages/events` | B, D → B (WS), F | Semana 1 de S2 |
| gRPC devices → snmp (`InventoryService.ListPollingTargets`, credenciales) | `packages/protobuf` | B → D | Semana 2 de S2 |
| gRPC wireguard ↔ wireguard-agent (`ApplyDesiredState`, `ReportStatus`) | `packages/protobuf` | B → B, P (despliegue) | Semana 2 de S3 |
| gRPC devices → flows (`ListCustomerAddressMap`) | `packages/protobuf` | B → D | Semana 2 de S4 |
| OpenAPI + eventos WireGuard | `services/wireguard`, `packages/events` | B → F | Semana 2 de S3 |
| Eventos `horus.snmp.*` (métricas, estado de interfaz) | `packages/events` | D → B (WS), F | Semana 2 de S3 |
| Esquema de series SNMP (C-03: ClickHouse desde S5, pendiente del PO) | [`../database.md`](../database.md) | D → B, F | Semana 2 de S3 |
| Enum único de estado de router (C-06) | [`../api.md`](../api.md), [`../events.md`](../events.md) | B, D → F | Semana 1 de S2 |
| Endpoint de estado del sistema (degradación) | [`../api.md`](../api.md) | B → F | Semana 2 de S3 |
| Esquema ClickHouse de flujos + eventos `horus.flows.*` | [`../traffic-model.md`](../traffic-model.md), `packages/events` | D → D, F | Semana 2 de S4 |

Regla de calendario: **los contratos del sprint N se cierran en la semana 2 del sprint N−1**
(sync de contratos), para que el refinement del sprint N cumpla la DoR.

### Cómo se trabaja sin bloquear

- **F** genera tipos TypeScript desde OpenAPI y usa mocks (S01-19); cuando el backend real existe,
  cambia a él sin tocar componentes.
- **B y D** usan pruebas de contrato: el productor valida sus respuestas/eventos contra el esquema
  en CI; el consumidor tiene tests con ejemplos del esquema.
- **D** desarrolla colectores contra simuladores (SNMP desde S1, flujos desde S5) antes de que
  devices tenga datos reales.

## 5. Sincronización

| Mecanismo | Frecuencia | Participan | Salida |
| --- | --- | --- | --- |
| Daily | Diaria (≤ 15 min; asíncrona para agentes) | Todos los flujos | Bloqueos cruzados explícitos con el flujo que desbloquea |
| Sync de contratos | Martes de la semana 2 | Un representante por flujo + coordinación | PRs de contrato del siguiente sprint aprobados o con fecha |
| Integración en `main` | Continua; *smoke e2e* diario en CI | P vigila | `main` verde; si se rompe, el flujo responsable lo arregla antes de seguir |
| Demo interna | Jueves de la semana 2 | Todos | Ensayo de la review en el entorno compose común |
| Review / Retro | Fin de sprint | Todos + PO | Ver [`README.md`](README.md) §9 |

Señales de alarma que el coordinador vigila: un PR con `contract:*` sin aprobación del consumidor
> 2 días; un flujo trabajando contra mocks a 3 días del fin del sprint; CI de `main` rojo > 4 h.

## 6. Asignación a agentes de IA

Cada flujo puede asignarse a un agente con estas reglas:

1. **Alcance acotado:** el agente solo escribe en los directorios de su flujo (§2). Para tocar
   `packages/` o `docs/` abre un PR de contrato que revisa coordinación.
2. **Rama/worktree propio** por historia: `<flujo>/<ID>-<slug>` (p. ej. `frontend/S01-14-login`),
   según el Git workflow de [`../conventions.md`](../conventions.md).
3. **Entrada:** la historia (con su ID), los contratos que consume y los documentos de `docs/`
   enlazados. **Salida:** PR con la DoD marcada y la salida de tests.
4. **Sin decisiones de arquitectura implícitas:** si un agente necesita una decisión no
   documentada, la registra en `docs/open-questions/` y continúa con un supuesto explícito y
   reversible.
5. **Coordinador (humano o agente):** reparte historias por flujo en el planning, fusiona PRs
   tras revisión cruzada, vigila las alarmas de §5 y mantiene el tablero.
6. **Revisión cruzada obligatoria** entre flujos para todo cambio de contrato; entre agentes del
   mismo flujo para el resto.

Asignación inicial sugerida para S1: Agente-P (Plataforma), Agente-B (Backend core), Agente-F
(Frontend), Agente-D (Datos), Coordinador. Los mismos agentes del Sprint 0 pueden continuar: el
autor de `security.md`/`observability.md` encaja en P, el de `api.md`/`events.md` en B, el de
`database.md`/`traffic-model.md` en D y el de este documento en F.
