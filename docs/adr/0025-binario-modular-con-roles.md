# 0025 — Monolito modular: un binario `horus` con roles y pocas unidades desplegables

- Estado: Aceptada
- Fecha: 2026-10-07
- Decisores: Agente A (arquitectura, ronda 2), a partir de [D7](../po-decisions.md) y
  [D9](../po-decisions.md)
- Sustituye: [ADR-0014](0014-granularidad-de-microservicios-en-el-mvp.md) en lo relativo a
  **unidades desplegables** (sus fronteras de módulo y fusiones se mantienen). Modifica
  [ADR-0002](0002-monorepo.md) (un `go.mod` por servicio → uno para el backend) y el
  enrutamiento de [ADR-0013](0013-api-gateway-propio.md) (proxy HTTP → montaje en proceso cuando
  el módulo es local).

## Contexto

ADR-0014 redujo el plan a 6 procesos en el MVP y 13 al final de la fase 1.0, pensando en un
equipo humano pequeño. Con **IA + 1 persona** ([D7](../po-decisions.md)) y un servidor como
despliegue inicial, cada proceso adicional sigue costando (imagen, health checks, dashboards,
versiones de contrato, saltos de red, depuración distribuida) y lo paga una sola persona que
opera. En cambio, el argumento de ADR-0014 contra el monolito era real: no mezclar **privilegios**
(WireGuard), **perfiles de carga** (UDP de flujos) ni romper P1 (el plano de administración no
depende del analítico).

## Decisión

### 1. Un binario, módulos con fronteras estrictas, roles seleccionables

- Un único binario Go `horus` (una imagen) que contiene todos los módulos. Qué ejecuta cada
  proceso se elige con `HORUS_ROLES` (`--roles`):

  | Rol | Contenido |
  | --- | --- |
  | `gateway` | Borde HTTP de la app: authN, rate limit, WebSocket fan-out; monta los handlers REST de los módulos locales y hace proxy a los remotos |
  | `auth` | Identidad, tenants, membresías, roles, sesiones, auditoría |
  | `devices` | Nodos, routers, realms, credenciales, **clientes** (descubrimiento, [ADR-0018](0018-la-ip-es-el-cliente.md)) |
  | `wireguard` | Control WireGuard (IPAM de plataforma, peers, scripts) |
  | `snmp` | Pollers SNMP/ICMP, estado observado |
  | `traffic` | Catálogo de clasificación y su snapshot |
  | `detection` | Reputación, correlación, hallazgos, scoring |
  | `alerts` | Reglas, alertas, notificaciones |
  | `analytics` | Consultas, dashboards, widgets |
  | `reporting` | Generación de reportes (CPU intensiva) |
  | `ingester` | Consumo de lotes de flujos **y** de métricas SNMP → ClickHouse (enriquecimiento, descubrimiento de IPs) |
  | `jobs` | Tareas singleton: archivado, sincronización remota con rclone ([ADR-0019](0019-almacenamiento-local-y-destino-remoto.md)), mantenimiento |
  | `collector` | Receptor UDP NetFlow/IPFIX/sFlow |
  | `wg-agent` | Aplicación de la configuración WireGuard en el kernel |

- Las **fronteras de ADR-0014 se mantienen como módulos** (`network` dentro de `wireguard`;
  `security`+`reputation`+`detection` = `detection`; `reporting` separado de `analytics` solo
  como rol; auditoría en `auth`).
- Reglas de módulo (verificadas por tests de arquitectura, [ADR-0023](0023-entrega-por-incrementos-y-equipo-ia.md)):
  - Cada módulo es dueño de **su esquema PostgreSQL y su usuario de BD** (P2 sigue vigente) y de sus
    tablas ClickHouse (escritor único).
  - Un módulo solo importa el **paquete público de contrato** de otro (interfaces generadas desde
    Protobuf), nunca su `internal/`.
  - **Llamadas síncronas**: por la interfaz del contrato; implementación en proceso cuando el
    módulo proveedor está en el mismo proceso, cliente gRPC cuando está en otro. Mismo contrato,
    mismo código del llamador ([ADR-0005](0005-grpc-protobuf-interno.md) sigue siendo el formato).
  - **Eventos**: siempre por NATS JetStream con outbox, **también entre módulos del mismo
    proceso**. No hay bus en memoria: así la semántica (durabilidad, reintento, idempotencia,
    reproceso) es idéntica al separar roles.

### 2. Unidades desplegables

**Perfil mínimo (un servidor; desarrollo, demo y producción inicial hasta escala S/M de
[architecture.md §9](../architecture.md#9-límites-de-escala-esperados)):**

| Contenedor | Roles | Por qué separado |
| --- | --- | --- |
| `horus-app` | `gateway, auth, devices, wireguard, snmp, traffic, detection, alerts, analytics, reporting, ingester, jobs` | — |
| `horus-collector` | `collector` | UDP de alto volumen; un paquete malformado o un pico de memoria no debe tumbar el login (P1) |
| `horus-wg-agent` | `wg-agent` | `CAP_NET_ADMIN` y red del host: privilegios aislados |

Más infraestructura: PostgreSQL, ClickHouse, NATS, Valkey, Traefik (+ observabilidad). **3
contenedores propios** frente a los 6 procesos del MVP y 13 de la fase 1.0 de ADR-0014.

**Perfil estándar (dos hosts, escala M):** `horus-app` se divide en `horus-core`
(`gateway, auth, devices, wireguard, snmp, traffic, alerts`) en el host de control y `horus-data`
(`ingester, analytics, reporting, detection, jobs`) junto a ClickHouse. El gateway enruta a los
módulos de `horus-data` por HTTP. **Separar es cambiar `HORUS_ROLES` en el compose**, no código.

**Disparadores para separar un rol en su propio contenedor** (los de ADR-0014 más uno medible):
perfil de recursos que perjudica a otros roles (medido: CPU/memoria/latencia p95 del login),
necesidad de réplicas independientes, privilegios distintos, migración a Kubernetes (cada conjunto
de roles = un Deployment).

### 3. Lo que no cambia

P1–P8 de [architecture.md](../architecture.md); outbox ([ADR-0016](0016-transactional-outbox.md));
NATS como bus ([ADR-0006](0006-nats-jetstream-bus-de-eventos.md)); Compose antes que Kubernetes
([ADR-0011](0011-docker-compose-antes-que-kubernetes.md)); gateway propio detrás de Traefik
([ADR-0013](0013-api-gateway-propio.md)), ahora como rol; las reglas de portabilidad a K8s
(config por entorno, probes por rol, SIGTERM).

## Alternativas consideradas

- **6 procesos en el MVP (ADR-0014)**: separación física útil para un equipo grande; para una
  persona, coste operativo sin beneficio medible a esta escala.
- **Monolito sin fronteras** (un proceso, paquetes libres): lo más simple hoy, pero se pierde la
  posibilidad de separar y los agentes acoplarían módulos sin control.
- **Todo en un solo proceso, incluidos collector y agente WG**: mezcla privilegios de kernel y UDP
  masivo con el login; rompe P1.
- **Bus de eventos en memoria entre módulos locales**: menos latencia, pero dos semánticas
  distintas (local vs NATS) y pérdida de eventos al reiniciar.

## Consecuencias

- (+) Una imagen, un pipeline, una versión; despliegue mínimo de 3 contenedores propios.
- (+) Depuración local sencilla (`--roles=all`), sin red entre módulos en el perfil mínimo.
- (+) La separación futura está preparada por contrato y se hace por configuración.
- (−) En el perfil mínimo, un fallo de memoria del proceso `horus-app` afecta a todos sus roles a
  la vez; se mitiga con límites de memoria del contenedor, `GOMEMLIMIT`, concurrencia acotada en
  `reporting`/`analytics` y reinicio automático, y se mide para decidir el paso al perfil estándar.
- (−) Todas las dependencias Go en un solo árbol: una actualización afecta a todo (aceptable con
  tests automáticos).
- (−) Health checks por rol: `/readyz` informa del estado de cada rol activo; un rol degradado
  (p. ej. `analytics` sin ClickHouse) no marca el proceso como no listo para el resto.
- Impacto: [services.md](../services.md) (catálogo por módulo y rol),
  [conventions.md](../conventions.md) (estructura del repo, `go.mod` único, roles, CI de una
  imagen), [observability.md](../observability.md) (métricas y readiness por rol),
  [roadmap.md](../roadmap.md), compose.
