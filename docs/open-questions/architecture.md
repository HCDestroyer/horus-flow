# Preguntas abiertas — Arquitectura

> Dueño: Agente A (arquitectura) · Para: Product Owner · Estado: **ronda 2** (tras
> [po-decisions.md](../po-decisions.md))
>
> Las decisiones del PO (D1–D10) resolvieron parte de las preguntas del Sprint 0; quedan
> registradas abajo con su ADR. Cada pregunta abierta incluye la **recomendación** y el **impacto**
> si se responde distinto. Los números Q1–Q16 se conservan; Q17+ son nuevas de la ronda 2.

## Resueltas

| # | Pregunta | Resolución | Decisión / ADR |
| --- | --- | --- | --- |
| Q1 | ¿Multi-tenant? | **Sí, desde v1**: tenant = ISP; `tenant_id` + RLS en PostgreSQL, row policies en ClickHouse, `tenant_id` en todo mensaje NATS, token de acceso por tenant, usuarios con varios ISP por membresía, usuarios de plataforma separados. | [D6](../po-decisions.md) · [ADR-0017](../adr/0017-multi-tenant-desde-v1.md) |
| Q4 | ¿Dónde corre MinIO respecto al NAS? | **No hay MinIO.** Almacenamiento local primario; NAS opcional como destino remoto por SFTP vía rclone. Sin servidor S3 en v1 (SeaweedFS solo si se cumple un disparador). | [D2](../po-decisions.md), [D3](../po-decisions.md) · [ADR-0019](../adr/0019-almacenamiento-local-y-destino-remoto.md) |
| Q5 | ¿De dónde sale el mapa IP ↔ cliente? | **La IP es el cliente**: se descubre desde los flujos (detector: ingester; dueño: `devices.customers`); identidad (tenant, realm, IP); tipo residencial por defecto, cambiado por scoring o a mano. Sin CRM/RADIUS. | [D1](../po-decisions.md) · [ADR-0018](../adr/0018-la-ip-es-el-cliente.md) |
| Q7 | ¿Qué estado de router muestra el dashboard del Sprint 3? | Obsoleta: sin sprints fijos. El estado (ICMP + SNMP) llega en el incremento "Nodo conectado", junto al alta del router. | [D9](../po-decisions.md) · [ADR-0023](../adr/0023-entrega-por-incrementos-y-equipo-ia.md) |
| Q11 | Licencias de MinIO y Redis | **Valkey** (BSD) en lugar de Redis; **sin MinIO**; si algún día hace falta S3, SeaweedFS (Apache-2.0), no Garage (AGPL). | [D3](../po-decisions.md) · [ADR-0020](../adr/0020-valkey-en-lugar-de-redis.md), [ADR-0019](../adr/0019-almacenamiento-local-y-destino-remoto.md) |
| Q13 | ¿Alertas antes del Sprint 11? | Obsoleta: `alerts` entra con la detección de botnets (incremento 5); hasta entonces, estado en la UI + Alertmanager para el hub y la infraestructura. | [D5](../po-decisions.md), [D9](../po-decisions.md) · [ADR-0024](../adr/0024-deteccion-de-botnets-como-objetivo-principal.md) |
| — | ClickHouse en el MVP vs tabla puente | ClickHouse desde el primer incremento con series o flujos; sin tabla puente. | [D4](../po-decisions.md) · [ADR-0021](../adr/0021-clickhouse-desde-el-primer-incremento.md) |
| — | Número de procesos con IA + 1 persona | Binario modular `horus` con roles: 3 contenedores propios en el perfil mínimo. | [D7](../po-decisions.md) · [ADR-0025](../adr/0025-binario-modular-con-roles.md) |
| — | Enrolment del router y rango de túneles | Endpoint con token de un solo uso (clave pública por `/tool fetch`), alternativa manual; IPAM de plataforma configurable (propuesta `10.255.0.0/16`) con validación de solapes. | [D10](../po-decisions.md) · [ADR-0022](../adr/0022-mikrotik-routeros-v7-primer-fabricante.md) |

## Abiertas

| # | Pregunta | Bloquea | Prioridad |
| --- | --- | --- | --- |
| [Q2](#q2) | Escala real: ISP, nodos, IPs de clientes | Dimensionamiento (incremento 3) | Alta |
| [Q3](#q3) | Confirmar que los routers se conectan por WireGuard | Incremento 2 | Alta |
| [Q6](#q6) | CGNAT: ¿dónde está el NAT en cada ISP? | Atribución (incremento 3) | **Crítica** |
| [Q8](#q8) | Auditoría: alcance y retención | Incremento 1 | Media |
| [Q9](#q9) | WireGuard: confirmar claves en el router y script manual | Incremento 2 | Baja |
| [Q10](#q10) | Retención raw y muestreo | Incremento 3 | Alta |
| [Q12](#q12) | RTO/RPO y destino remoto | Incremento 7 / endurecimiento | Media |
| [Q14](#q14) | Marco legal por país de cada ISP | Antes de tráfico real | Alta |
| [Q15](#q15) | IPv6 desde el inicio | Modelo de datos | Media |
| [Q16](#q16) | Servidor disponible | Despliegue (incremento 1) | Alta |
| [Q17](#q17) | ¿Quién opera Horus y qué administra cada ISP? | Incremento 1 | Alta |
| [Q18](#q18) | ¿Vistas o pantallas de kiosco con varios ISP a la vez? | Incremento 6 | Media |
| [Q19](#q19) | Prefijos de clientes por nodo | Incremento 3 | Alta |
| [Q20](#q20) | ¿Destino remoto por ISP? | Incremento 7 | Baja |
| [Q21](#q21) | Retención y cuotas por ISP | Incremento 3 / 7 | Media |
| [Q22](#q22) | ¿Routers que exporten sin WireGuard? | Incremento 2 | Media |
| [Q23](#q23) | ¿Acciones sobre el router (bloquear, *walled garden*)? | Después de v1 | Baja |
| [Q24](#q24) | MediaFire como destino | Incremento 7 | Baja |

---

<a id="q2"></a>
## Q2. ¿Cuál es la escala real y su crecimiento a 2–3 años?

- **Cambio por D6:** la unidad ya no es el router sino el **total de IPs de clientes activas** en
  todos los ISP ([architecture.md §9](../architecture.md#9-límites-de-escala-esperados),
  escenarios S/M/L/XL).
- **Necesitamos:** nº de ISP previstos, nodos por ISP, IPs de clientes por nodo, tráfico pico por
  nodo (Gbit/s), modelos de router principal (CCR2116/2216 con L3HW afectan a la cobertura).
- **Recomendación:** diseñar para **M (≈ 30k IPs) en el perfil estándar** y medir con el primer
  ISP real; L con clúster ClickHouse.
- **Impacto:** si el primer año ya supera 100k IPs, se adelanta el clúster ClickHouse/NATS.

<a id="q3"></a>
## Q3. Confirmar que los routers se conectan por WireGuard

- **Estado:** supuesto del PO en [po-decisions.md](../po-decisions.md); el diseño lo da por hecho
  ([ADR-0022](../adr/0022-mikrotik-routeros-v7-primer-fabricante.md)): túnel iniciado por el
  router, SNMP/API/IPFIX por el túnel.
- **Pregunta:** ¿algún ISP tiene ya una red de gestión (MPLS/VLAN) o exige que el tráfico de
  gestión no salga por Internet? ¿El servidor de Horus tendrá IP pública y un nombre DNS para el
  hub?
- **Recomendación:** WireGuard para todos; endpoint del hub por nombre DNS con TTL bajo (§10.9 de
  [architecture.md](../architecture.md#109-el-hub-wireguard-se-cae-punto-único-de-fallo-de-la-recolección)).

<a id="q6"></a>
## Q6. CGNAT: ¿dónde se hace el NAT en cada ISP? (crítica con D1)

- **Contexto:** con "la IP es el cliente" ([ADR-0018](../adr/0018-la-ip-es-el-cliente.md)), si los
  flujos llegan con la IP pública compartida, no hay cliente identificable. Según
  [vendors/mikrotik.md](../vendors/mikrotik.md) §2.4, si el NAT/CGNAT lo hace **el propio router
  principal**, Traffic Flow exporta las IPs privadas del cliente (esperado, a verificar en
  laboratorio); si el CGNAT está en **otro equipo entre clientes y router principal**, no se puede
  atribuir.
- **Pregunta:** por cada ISP, ¿quién hace el NAT y dónde?
- **Recomendación:** verificarlo automáticamente en el alta (lectura de `/ip/firewall/nat` y de los
  pools) y bloquear el paso a "activo" con un aviso si la topología no permite atribuir; exportar
  desde el equipo que vea la IP del cliente.

<a id="q8"></a>
## Q8. Auditoría: ¿qué se audita, quién la guarda y cuánto tiempo?

- **Recomendación (sin cambios, con tenant):** toda escritura y toda lectura sensible
  (credenciales, evidencias de hallazgos, flujos por IP, exportaciones), con actor, tenant,
  `via_platform`, IP y diff sin secretos. Almacén en `auth`, inmutable, **2 años** y después
  exportación al almacén local (y copia remota si existe). El acceso de un usuario de plataforma a
  datos de un tenant debe ser visible para el admin de ese tenant. Confirmar con
  [security.md](../security.md).

<a id="q9"></a>
## Q9. WireGuard: claves y aprovisionamiento

- **Decidido en [ADR-0022](../adr/0022-mikrotik-routeros-v7-primer-fabricante.md):** clave privada
  generada en el router; Horus no escribe en el router (script `.rsc` aplicado por el técnico);
  enrolment con token de un solo uso.
- **Queda por confirmar:** que el procedimiento "pegar un script" por router es aceptable para los
  técnicos de los ISP. Si no, ver [Q23](#q23).

<a id="q10"></a>
## Q10. Retención raw y muestreo

- **Recomendación:** raw **7 días** (mínimo para investigar hallazgos), agregados de 1 min 7 días
  (detección), 5 min 90 días, 1 h 13 meses, 1 día 5 años; **sin muestreo** en el router (el
  muestreo degrada la detección de escaneos); escalar ClickHouse antes que muestrear. Decisión
  final con [storage.md](../storage.md) / [open-questions/data.md](data.md).

<a id="q12"></a>
## Q12. ¿Qué disponibilidad se necesita (RTO/RPO)?

- **Recomendación v1:** plano de administración RTO 1 h / RPO 5 min **locales** (pgBackRest en
  otro disco); RPO **remoto** = frecuencia de rclone (p. ej. 1 h para WAL, 24 h para backups
  completos) **solo si hay destino**; plano analítico RTO 4 h / RPO 24 h para agregados. Sin HA
  automática; hub WG activo/pasivo cuando se exija.
- **Pregunta:** ¿algún ISP exige SLA (NOC 24/7)? ¿Se compromete el PO a tener al menos un destino
  SFTP? Sin destino remoto, la pérdida del servidor es pérdida total.

<a id="q14"></a>
## Q14. ¿Qué marco legal aplica a los datos de tráfico?

- **Cambio por D5:** el propósito declarado es la seguridad de la red del ISP (detección de
  botnets), lo que suele dar base legítima al tratamiento; sigue aplicando minimización, retención
  limitada y acceso por rol.
- **Pregunta:** país de cada ISP, regulador, obligaciones de retención o de límite, si hay que
  informar a los suscriptores; quién es responsable y quién encargado del tratamiento
  (ISP vs operador de Horus) en un despliegue multi-ISP.
- **Recomendación:** contrato de encargo de tratamiento por ISP; permisos separados para evidencias
  por cliente; retención configurable por tenant si la ley lo exige ([Q21](#q21)).

<a id="q15"></a>
## Q15. ¿IPv6 desde el inicio?

- **Recomendación:** **sí en el modelo** (`inet`, IPv6 en ClickHouse, tries duales); con D1, un
  cliente IPv6 es el **prefijo delegado** (`/64` por defecto, configurable por realm) para no crear
  un cliente por dirección temporal ([ADR-0018](../adr/0018-la-ip-es-el-cliente.md)). IPFIX es
  obligatorio para IPv6 en MikroTik.

<a id="q16"></a>
## Q16. ¿Qué servidor hay disponible?

- **Cambio por D2/D6/0025:** no se necesita NAS. Perfil mínimo: **un servidor** (16 vCPU, 64 GB,
  SSD/NVMe 2 TB para bases de datos + disco separado 2–4 TB para el almacén local), IP pública y
  nombre DNS para el hub WG ([architecture.md §8.1](../architecture.md#81-perfiles-de-despliegue)).
- **Pregunta:** ¿físico o VM?, ¿en qué red/datacenter?, ¿hay un segundo host para el perfil
  estándar?, ¿qué destino SFTP existe (si alguno)?

<a id="q17"></a>
## Q17. ¿Quién opera Horus y qué administra cada ISP?

- **Contexto:** D6 implica varios ISP en una instalación. No está claro si el operador es una
  empresa que da servicio a varios ISP (modelo SaaS) o un grupo de ISP relacionados.
- **Preguntas:** ¿los administradores de cada ISP gestionan sus propios usuarios? ¿Pueden dar de
  alta nodos y routers sin el operador? ¿Hay facturación o límites por ISP?
- **Recomendación:** rol `tenant_admin` con autogestión de usuarios, nodos y routers de su ISP; el
  operador de plataforma solo crea tenants, gestiona catálogo, reputación y almacenamiento, y
  accede a datos de un ISP de forma explícita y auditada ([ADR-0017](../adr/0017-multi-tenant-desde-v1.md)).

<a id="q18"></a>
## Q18. ¿Vistas o pantallas de kiosco con varios ISP a la vez?

- **Contexto:** el token es por tenant y las vistas también. Un NOC del operador podría querer una
  pantalla mural con el estado de todos los ISP.
- **Recomendación:** en v1 solo un **resumen de plataforma** sin datos de clientes
  (`/api/v1/platform/overview`: routers caídos, alertas abiertas, hallazgos por severidad, salud de
  ingesta por ISP) disponible como widget de kiosco para usuarios de plataforma. Vistas multi-ISP
  con datos de clientes, no.
- **Impacto si se pide más:** consultas cruzadas de tenant en `analytics` con un rol de lectura
  especial; complica el aislamiento.

<a id="q19"></a>
## Q19. ¿Quién declara los prefijos de clientes de cada nodo?

- **Contexto:** solo las IPs dentro de los prefijos de clientes del realm se convierten en clientes
  ([ADR-0018](../adr/0018-la-ip-es-el-cliente.md)).
- **Recomendación:** Horus los **sugiere** en el alta leyendo pools, direcciones y NAT del router
  (API 8729/REST) y el admin del ISP los confirma; cambios posteriores detectados en el router
  generan una sugerencia, no un cambio automático.

<a id="q20"></a>
## Q20. ¿Destino remoto por ISP?

- **Contexto:** v1 tiene destinos remotos de plataforma (backups contienen todos los tenants).
  Algunos ISP podrían querer sus reportes/archivo en su propio NAS o Drive.
- **Recomendación:** fuera de v1; el modelo (`remote_destinations.tenant_id` nullable) lo admite
  para exportar `archive/<tenant_id>/` y `reports/<tenant_id>/` más adelante.

<a id="q21"></a>
## Q21. ¿Retención y cuotas por ISP?

- **Recomendación:** retención uniforme por tabla en v1; cuotas técnicas por tenant (flujos/s en el
  collector, concurrencia de consultas) desde el principio para proteger a los demás ISP. Retención
  por tenant solo si la ley de un país lo exige ([Q14](#q14)): se implementaría con borrados por
  `tenant_id` programados (mutaciones/`DELETE` ligeros en ClickHouse), más costosos que la TTL.

<a id="q22"></a>
## Q22. ¿Habrá routers que exporten sin WireGuard?

- **Contexto:** el exportador se identifica por su IP de túnel, única en la plataforma
  ([ADR-0017](../adr/0017-multi-tenant-desde-v1.md) §8). Sin túnel, dos routers detrás de NAT o
  con IPs repetidas serían indistinguibles.
- **Recomendación:** no soportarlo en v1. Si hiciera falta: IP pública fija registrada por router o
  un puerto de colector distinto por exportador.

<a id="q23"></a>
## Q23. ¿Acciones sobre el router (bloquear, limitar, *walled garden*)?

- **Recomendación:** fuera de v1 (Horus solo lee, [ADR-0022](../adr/0022-mikrotik-routeros-v7-primer-fabricante.md),
  [ADR-0024](../adr/0024-deteccion-de-botnets-como-objetivo-principal.md)). Si se pide, sería un
  incremento propio con credenciales de escritura acotadas, aprobación humana por acción y
  auditoría.

<a id="q24"></a>
## Q24. ¿MediaFire como destino remoto?

- **Contexto:** rclone (herramienta elegida en [ADR-0019](../adr/0019-almacenamiento-local-y-destino-remoto.md))
  no tiene un backend estable para MediaFire.
- **Recomendación:** no soportarlo; usar SFTP, Google Drive, MEGA o Dropbox. Si es imprescindible,
  validar si la cuenta ofrece WebDAV.
