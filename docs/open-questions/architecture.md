# Preguntas abiertas — Arquitectura

> Dueño: Agente 1 (Arquitecto de sistema) · Para: Product Owner · Estado: abiertas (Sprint 0)
>
> Cada pregunta incluye la **recomendación** del arquitecto y el **impacto** si se responde
> distinto. Las referencias apuntan a [architecture.md](../architecture.md),
> [services.md](../services.md) y [adr/](../adr/README.md).

| # | Pregunta | Bloquea | Prioridad |
| --- | --- | --- | --- |
| [Q1](#q1) | ¿Multi-tenant en el futuro? | Modelo de datos (Sprint 1) | Alta |
| [Q2](#q2) | Escala real: routers, suscriptores, flujos | Dimensionamiento, retención (Sprint 6) | Alta |
| [Q3](#q3) | ¿Se llega a los routers por WireGuard? | SNMP/flows/WG (Sprints 4–6) | Alta |
| [Q4](#q4) | ¿Dónde corre MinIO respecto al NAS? | Infra (Sprint 1) | Media |
| [Q5](#q5) | ¿De dónde sale el mapa IP ↔ cliente? | Valor central del producto (Sprint 6–7) | **Crítica** |
| [Q6](#q6) | ¿Hay CGNAT y dónde se exportan los flujos? | Atribución de tráfico (Sprint 6) | Alta |
| [Q7](#q7) | Estado online/offline en el Sprint 3 | Roadmap | Media |
| [Q8](#q8) | Auditoría: alcance, dueño y retención | Sprint 2 | Media |
| [Q9](#q9) | WireGuard: ¿quién genera claves y se configura el router? | Sprint 4 | Alta |
| [Q10](#q10) | Retención raw y muestreo de flujos | Storage (Sprints 6, 13) | Alta |
| [Q11](#q11) | Licencias: MinIO y Redis | Infra (Sprint 1) | Media |
| [Q12](#q12) | Objetivos de disponibilidad (RTO/RPO) | HA, DR (Sprints 13–14) | Media |
| [Q13](#q13) | Alertas antes del Sprint 11 | Roadmap | Baja |
| [Q14](#q14) | Privacidad y marco legal de los datos de tráfico | Retención, accesos | Alta |
| [Q15](#q15) | ¿IPv6 desde el inicio? | Modelo de datos y de tráfico | Media |
| [Q16](#q16) | Infraestructura disponible (hosts, NAS, red) | Despliegue | Media |

---

<a id="q1"></a>
## Q1. ¿Horus Flow será multi-tenant (varios ISP u organizaciones)?

- **Contexto:** v1 asume un solo ISP. Si en el futuro se ofrece como SaaS o a varias empresas del
  grupo, cambiar el modelo después es caro (claves, permisos, particionado de ClickHouse,
  aislamiento de NATS).
- **Recomendación:** single-tenant en v1, pero **`organization_id` en toda entidad raíz** de
  PostgreSQL y como columna en tablas de ClickHouse desde el día uno, incluido en el JWT y en los
  subjects/headers de eventos. Coste casi nulo hoy; evita una migración masiva. No implementar
  aislamiento por tenant (RLS, cuentas NATS por tenant) hasta que haya un caso real.
- **Si la respuesta es "sí, pronto":** añadir Row-Level Security en PostgreSQL, cuentas NATS por
  tenant y cuotas en ClickHouse desde el Sprint 2.

<a id="q2"></a>
## Q2. ¿Cuál es la escala real y su crecimiento a 2–3 años?

- **Necesitamos:** nº de routers que exportarán flujos (no solo los que se monitorizan), nº de
  suscriptores totales y por router, tráfico agregado en hora pico (Gbit/s), si los routers son
  de borde/concentradores (BRAS/PPPoE) o CPE.
- **Recomendación:** usar los supuestos de [architecture.md §9.1](../architecture.md#91-supuestos-explícitos-a-validar-con-po--q2)
  (300 suscriptores/router, 5–10 flujos/s/suscriptor) hasta medir con 2–3 routers reales en el
  Sprint 6. Diseñar para **100 routers sin muestreo** y **1000 con muestreo**.
- **Impacto:** si son > 200 routers con exportación completa, se adelanta clúster ClickHouse o
  muestreo y se dimensiona NATS en clúster.

<a id="q3"></a>
## Q3. ¿Los routers se gestionan a través de túneles WireGuard hacia un hub central?

- **Supuesto actual:** sí — SNMP, ICMP y exportación de flujos viajan por WG. Esto hace del hub WG
  un punto único de fallo del plano de recolección ([architecture.md §10.9](../architecture.md#109-el-hub-wireguard-se-cae-escenario-derivado-del-supuesto-2)).
- **Preguntas:** ¿cuántos hubs? ¿hay routers con IP pública o en una red de gestión ya existente
  (MPLS/VLAN de gestión)? ¿los routers son mayoritariamente MikroTik?
- **Recomendación:** un hub WG en v1 en el host de control; segundo hub (activo/activo, cada
  router con dos peers) en el Sprint 14 si > 100 routers dependen de él. Permitir en el modelo de
  `devices` que un router tenga **varias direcciones de gestión** (túnel, pública, gestión).

<a id="q4"></a>
## Q4. ¿Dónde corre MinIO respecto al NAS?

- **Opciones:** (a) MinIO como contenedor **en el NAS** (Synology/QNAP/TrueNAS lo soportan);
  (b) MinIO en un host con almacenamiento del NAS por **iSCSI**; (c) MinIO en un host con el NAS
  por **NFS/SMB**; (d) el NAS ya ofrece S3 nativo (algunos modelos).
- **Recomendación:** (a) o (d) si el NAS lo permite; si no, (b). **Evitar (c)**: MinIO no soporta
  NFS como backend y hay riesgo de corrupción ([ADR-0010](../adr/0010-minio-sobre-nas.md)).
- **Necesitamos:** modelo de NAS, RAID, capacidad libre, red (1/10 GbE) hacia los hosts.

<a id="q5"></a>
## Q5. ¿De dónde sale el mapa IP ↔ cliente? (crítica)

- **Contexto:** "consumo por cliente", "detección residencial/comercial" y reportes por cliente
  requieren saber **qué cliente tenía qué IP en cada instante**. Con PPPoE/DHCP dinámico la IP
  cambia; sin este mapa, los flujos solo se pueden atribuir a router/interfaz.
- **Preguntas:** ¿cómo se asignan IPs a los clientes (PPPoE con RADIUS, DHCP, estático)? ¿Existe
  un sistema de facturación/CRM (p. ej. WispHub, Splynx, UISP, MikroWisp, propio) que sea la
  fuente de verdad de clientes? ¿Hay servidor RADIUS con accounting?
- **Recomendación:** **los clientes no se capturan a mano en Horus**: se importan/sincronizan del
  sistema existente (dueño en Horus: `devices`), y las sesiones IP↔cliente se obtienen de
  **RADIUS accounting** (Start/Interim/Stop) o, en su defecto, de la API del router (sesiones PPP
  activas, leases DHCP) con historial temporal (`customer_ip_sessions` con `valid_from/valid_to`).
  Esto probablemente requiere un componente nuevo (adaptador RADIUS/CRM) que hoy no está en el
  plan: proponer incorporarlo como módulo de `devices` en el Sprint 6–7 y registrarlo en un ADR.
- **Impacto si no se resuelve antes del Sprint 6:** el Sprint 7 y el 10 no entregan su valor.

<a id="q6"></a>
## Q6. ¿El ISP usa CGNAT? ¿En qué punto se exportarán los flujos?

- **Contexto:** si los flujos se exportan **después** del NAT, la IP origen es la pública
  compartida y no identifica al cliente. Si se exportan **antes** (interfaz hacia clientes), la IP
  es la privada/CGNAT del cliente (100.64.0.0/10) y sí sirve con el mapa de Q5.
- **Recomendación:** exportar en la interfaz del lado cliente del concentrador (pre-NAT), en
  ambas direcciones. Si solo es posible post-NAT, se requieren logs de traducción NAT
  (volumen enorme) — fuera de alcance v1. Documentar en [traffic-model.md](../traffic-model.md).

<a id="q7"></a>
## Q7. ¿Qué estado de router muestra el dashboard del Sprint 3?

- **Contexto:** el Sprint 3 pide "routers online/offline/warning/critical", pero el estado
  observado nace con `snmp` en el Sprint 5 (y los handshakes WG en el 4).
- **Opciones:** (a) mostrar `unknown` hasta el Sprint 5; (b) adelantar a Sprint 3 un sondeo
  **ICMP** mínimo como primera pieza del servicio `snmp` (online/offline sin warning/critical).
- **Recomendación:** (b). Es pequeño (ICMP con `pro-bing`, ~1–2 días), valida temprano el flujo
  `snmp → NATS → devices → gateway → WebSocket` y da valor visible. warning/critical llegan con
  SNMP en el Sprint 5. Requiere ajuste del Agente 5 en [roadmap.md](../roadmap.md).

<a id="q8"></a>
## Q8. Auditoría: ¿qué se audita, quién la guarda y cuánto tiempo?

- **Recomendación:** auditar toda acción de escritura y toda lectura de datos sensibles
  (credenciales, configuraciones WG, exportaciones de tráfico por cliente), con actor, IP, antes/
  después (sin secretos). Almacén en `auth` (v1), alimentado por eventos `*.audit.recorded` vía
  outbox. Retención **2 años** en PostgreSQL y archivo a MinIO después. Inmutable (solo
  `INSERT`, sin `UPDATE/DELETE` para el rol de la app). Confirmar con el Agente 4 ([security.md](../security.md)).

<a id="q9"></a>
## Q9. WireGuard: ¿quién genera las claves y Horus configura los routers?

- **Opciones de claves:** (a) Horus genera el par del peer y entrega la configuración (la clave
  privada pasa por Horus y debe guardarse cifrada o descartarse tras la descarga); (b) el router
  genera su par y Horus solo recibe la clave pública (más seguro).
- **Opciones de aprovisionamiento:** (i) Horus genera un script/archivo de config que el técnico
  aplica; (ii) Horus aplica la config en el router vía API (MikroTik RouterOS API/SSH).
- **Recomendación:** claves (b) cuando el router lo soporte (MikroTik lo hace), (a) solo como
  alternativa con la privada mostrada **una vez** y no almacenada. Aprovisionamiento (i) en v1;
  (ii) queda fuera del MVP porque convierte a Horus en sistema de gestión de configuración (otro
  dominio, otros riesgos).

<a id="q10"></a>
## Q10. ¿Cuánto tiempo guardar flujos raw y se acepta muestreo?

- **Contexto:** `vision.md` propone raw 7–30 días. A 100 routers sin muestreo, 30 días son 3–6 TB
  solo de raw ([architecture.md §9.2](../architecture.md#92-estimaciones-órdenes-de-magnitud)).
- **Recomendación:** **raw 7 días**, agregados 1 min 30 días, 1 h 12 meses, 1 día 5 años;
  muestreo 1:N configurable por router, desactivado por defecto hasta ~200 routers. Los
  agregados son exactos (sin muestreo) mientras no se active. Ajustar tras medir en el Sprint 6.
  Decisión final con el Agente 2 ([storage.md](../storage.md)).

<a id="q11"></a>
## Q11. ¿Aceptamos las licencias/distribución de MinIO y Redis?

- **Contexto:** MinIO Community es AGPLv3 y en 2025 redujo funciones de la consola y la
  distribución de binarios de la edición comunitaria; Redis cambió de licencia en 2024 (desde
  Redis 8 vuelve a ofrecer AGPLv3) y existe el fork Valkey (BSD). Para uso interno de un ISP sin
  redistribución, AGPL no suele ser un problema, pero sí si Horus se vende o distribuye.
- **Recomendación:** usar **Valkey** en lugar de Redis (compatible, BSD, sin riesgo). Para S3,
  mantener la API S3 como único contrato y evaluar en el Sprint 1 MinIO vs **Garage** o
  **SeaweedFS** según el estado de las imágenes mantenidas en ese momento; el código no cambia.
  Confirmar si Horus podría comercializarse en el futuro.

<a id="q12"></a>
## Q12. ¿Qué disponibilidad se necesita (RTO/RPO)?

- **Recomendación para v1:** plano de administración RTO 1 h / RPO 5 min (PostgreSQL con WAL);
  plano analítico RTO 4 h / RPO 24 h para agregados, raw "best effort"; recolección: tolera
  caídas de ClickHouse sin pérdida según §9.4. Sin HA automática hasta el Sprint 14. Valores
  definitivos con el Agente 4 ([disaster-recovery.md](../disaster-recovery.md)).
- **Pregunta:** ¿hay un SLA comprometido con algún área (NOC 24/7)? Si sí, adelantar réplica de
  PostgreSQL y segundo hub WG.

<a id="q13"></a>
## Q13. ¿Se necesitan alertas antes del Sprint 11?

- **Contexto:** desde el Sprint 3/5 se sabe si un router cae, pero `alerts` (con email/Telegram)
  llega en el Sprint 11.
- **Recomendación:** no adelantar `alerts`; mientras tanto, notificación en la UI vía WebSocket.
  Si el NOC lo necesita antes, una regla temporal en Prometheus/Alertmanager sobre una métrica
  `horus_router_up` expuesta por `snmp` cubre el caso con muy poco esfuerzo (y se retira en el 11).

<a id="q14"></a>
## Q14. ¿Qué marco legal aplica a los datos de tráfico de los suscriptores?

- **Contexto:** los flujos (IP origen/destino, horarios, servicios usados) asociados a clientes
  son datos personales y potencialmente sujetos a leyes de protección de datos y de
  telecomunicaciones (retención obligatoria o, al revés, límites de retención y finalidad).
- **Preguntas:** país/regulador, obligación legal de retención, quién puede ver el detalle por
  cliente, si los clientes deben ser informados.
- **Recomendación:** permisos separados para ver detalle por cliente (`traffic.read` vs
  `traffic.customer_detail.read`, a acordar con Agente 4), auditoría de esas lecturas (Q8),
  retención mínima necesaria, y posibilidad de seudonimizar al exportar reportes.

<a id="q15"></a>
## Q15. ¿IPv6 desde el inicio?

- **Recomendación:** **sí en el modelo** (tipos `inet` en PostgreSQL, `IPv6` en ClickHouse
  almacenando IPv4 como IPv4-mapped o columnas separadas, tries duales en el catálogo), aunque la
  red del ISP sea hoy solo IPv4. Retrofit de IPv6 en un modelo de flujos es caro. Confirmar con el
  Agente 2.

<a id="q16"></a>
## Q16. ¿Qué infraestructura está disponible?

- **Necesitamos:** número y especificaciones de servidores (CPU, RAM, discos SSD/NVMe), si son
  físicos o VMs (hipervisor), IP pública y salida a Internet para el hub WG, red hacia el NAS,
  posibilidad de un sitio secundario.
- **Recomendación mínima** para ≤ 100 routers: 2 hosts (control 8 vCPU/32 GB/SSD 500 GB;
  analítica 16 vCPU/64 GB/NVMe 4 TB) + NAS ([architecture.md §8.1](../architecture.md#81-topología-v1-docker-compose)).
