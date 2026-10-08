# Preguntas abiertas — Producto

Preguntas para el product owner (PO). Tras la ronda 1 de respuestas
([`../po-decisions.md`](../po-decisions.md), D1–D10) se separan en **resueltas**, **resueltas en
parte** (queda una pregunta más concreta) y **abiertas**. Cada abierta indica qué bloquea y la
recomendación, que se toma como **supuesto de trabajo** hasta que haya respuesta. Las preguntas
técnicas relacionadas viven en [`architecture.md`](architecture.md), [`data.md`](data.md),
[`contracts.md`](contracts.md), [`security-ops.md`](security-ops.md) y en
[`../vendors/mikrotik.md`](../vendors/mikrotik.md) §9.2; aquí se enlazan en lugar de duplicarse.

Prioridad (incrementos de [`../roadmap.md`](../roadmap.md)): **Bloquea I1** · **Antes de I2** ·
**Antes de I3** · **Puede esperar**.

## 1. Resueltas

| ID | Pregunta | Decisión | Efecto en el plan |
| --- | --- | --- | --- |
| P-01 | Fecha de inicio y calendario | **D9**: no hay calendario de sprints; los agentes producen cada entregable en el menor tiempo posible | Roadmap por incrementos con tallas relativas y gates de la persona ([`../roadmap.md`](../roadmap.md) §1–§2) |
| P-02 | Tamaño y composición del equipo | **D7**: IA + 1 persona | Reparto en agentes, DoD verificable por máquina, aprobación de la persona solo en contratos, seguridad y demos ([`../backlog/team.md`](../backlog/team.md)) |
| P-04 | Fuente del mapa IP ↔ cliente | **D1**: la IP es el cliente, descubierta de los flujos; residencial por defecto | Desaparecen RADIUS/CRM/facturación como fuente; descubrimiento automático en I1 ([ADR-0018](../adr/0018-la-ip-es-el-cliente.md)) |
| P-10 | Prioridad entre WireGuard y SNMP | **D9 + D10**: se ordena por valor | Flujos y túnel mínimo en I1; SNMP en I2; gestión completa de WireGuard en I3 |
| P-18 | ¿ClickHouse en el MVP? | **D4**: desde donde se necesite | ClickHouse desde I0 ([ADR-0021](../adr/0021-clickhouse-desde-el-primer-incremento.md)) |
| P-21 | Contextos de uso: pantalla mural, móvil, campo | **D8**: dashboard modular para pantallas de monitoreo | Modo NOC/kiosco en el primer entregable ([`../frontend.md`](../frontend.md) §7) |

## 2. Resueltas en parte

| ID | Decidido | Lo que queda (pregunta concreta) | Límite | Supuesto de trabajo |
| --- | --- | --- | --- | --- |
| P-03 | **D6**: varios ISP; usuarios con acceso a uno o varios | ¿Qué roles hay dentro de cada ISP (NOC, seguridad, gerencia, técnicos)? ¿Quién opera la plataforma (superadmin): tú o cada ISP? | Antes de I2 | Roles `superadmin`, `isp_admin`, `isp_operator`, `isp_viewer` en I0; rol de seguridad por ISP en I2 |
| P-06 | **D10**: MikroTik primero | ¿Qué modelos y versiones de RouterOS tienen los routers principales? ¿Hay v6 que no se pueda actualizar? ¿Usan offload por hardware (L3HW, FastTrack HW)? (= `vendors/mikrotik.md` §9.2, preguntas 1 y 3) | **Bloquea I1** | RouterOS v7 ≥ 7.12; v6 fuera del primer entregable; offload detectado y avisado |
| P-07 | Supuesto de `po-decisions.md`: WireGuard conecta los routers con Horus; diseño en [ADR-0022](../adr/0022-mikrotik-routeros-v7-primer-fabricante.md) | Confirmar que los routers pueden iniciar un túnel saliente UDP hacia Horus y que el ISP acepta pegar el script de onboarding | **Bloquea I1** | Sí; el router inicia el túnel y se enrola solo con un token de un uso |
| P-11 | **D5**: propósito de seguridad (mitigar botnets) | País de cada ISP y ley aplicable; si los contratos/avisos de privacidad cubren este análisis | Antes de usar datos reales de terceros (antes del gate G1 si el router de la persona tiene clientes reales) | Minimización: crudo 7 días, IPs fuera de URLs y logs, acceso a fichas auditado, sin datos personales en kioscos por defecto |
| P-19 | El PO probará con su MikroTik | ¿Modelo, versión de RouterOS y cuántos clientes ve ese router? ¿Tiene clientes reales o es de laboratorio? ¿Hay una máquina con KVM para el laboratorio CHR? | **Bloquea I1** | Router con RouterOS v7 y clientes de laboratorio; laboratorio CHR en la máquina de los agentes |

## 3. Abiertas

### Bloquean I1

#### P-23 · ¿Detectar y avisar, o también actuar sobre el router?
- **Por qué importa:** "mitigar que clientes entren en botnets" (D5) puede significar detectar y
  avisar, o también **cortar o poner en cuarentena** a un cliente desde Horus (address-list en el
  MikroTik). Actuar exige escribir en el router, un usuario con permisos de escritura y un riesgo de
  cortar el servicio por error ([`../vendors/mikrotik.md`](../vendors/mikrotik.md) §9.2 pregunta 7;
  [ADR-0024](../adr/0024-deteccion-de-botnets-como-objetivo-principal.md) §4).
- **Pregunta:** ¿Horus solo detecta y explica, o quieres que en el futuro pueda aplicar una
  cuarentena? Si es así, ¿siempre con aprobación humana?
- **Recomendación:** v1 solo detecta, explica y avisa; Horus no escribe en el router. Cuarentena
  asistida (con aprobación, caducidad y reversión) como opción de I4 si la pides.

#### P-24 · ¿Dónde se hace el NAT/CGNAT y qué rangos son de clientes?
- **Por qué importa:** D1 solo funciona si el router principal ve la IP del cliente. Si el CGNAT
  está en otro equipo entre los clientes y el router, la IP observada es la pública compartida y no
  identifica a nadie ([`../vendors/mikrotik.md`](../vendors/mikrotik.md) §2.4; supuesto abierto de
  `po-decisions.md`). Además, Horus necesita saber qué rangos son de clientes, de infraestructura o
  excluidos ([`../traffic-model.md`](../traffic-model.md) §4.1).
- **Pregunta:** en cada ISP, ¿el NAT lo hace el router principal, otro equipo o el CPE? ¿Los pools de
  clientes están definidos en el MikroTik (`/ip pool`)?
- **Recomendación:** NAT en el router principal o en el CPE (ambos funcionan); prefijos importados
  del MikroTik y confirmados por el ISP, con modo descubrimiento como respaldo.

#### P-25 · ¿Quién recibe los hallazgos de botnet y qué hace con ellos?
- **Por qué importa:** define severidades por defecto, el lenguaje de la UI, la plantilla
  "Seguridad" y, en I3, a quién se avisa.
- **Pregunta:** en cada ISP, ¿quién revisa los hallazgos (NOC, soporte, seguridad)? ¿Se contacta al
  abonado? ¿Hace falta registrar qué se hizo (llamada, visita, cambio de CPE)?
- **Recomendación:** los revisa el NOC o seguridad del ISP; la UI registra el estado y un comentario;
  el contacto con el abonado queda fuera de Horus en v1.

#### P-26 · ¿La interfaz se expone a Internet?
- **Por qué importa:** con varios ISP (D6) puede hacer falta acceso desde fuera de una red privada;
  eso adelanta 2FA y endurecimiento ([`security-ops.md`](security-ops.md) Q13). El endpoint de
  enrolamiento del router sí debe ser alcanzable desde los routers.
- **Pregunta:** ¿los usuarios de los ISP entrarán por Internet o por VPN/red privada?
- **Recomendación:** en I1, UI solo en red privada o VPN y endpoint de enrolamiento + UDP de
  WireGuard públicos; 2FA en I2 antes de exponer la UI.

### Antes de I2

#### P-05 · Escala real
- **Pregunta:** número de ISP, nodos por ISP, clientes por nodo y tráfico pico; ¿cuántos routers
  exportarán flujos en 12 meses?
- **Recomendación:** medir con el router de la persona en I1; diseñar para 100 routers en un
  servidor y 1 000 con el perfil estándar ([ADR-0025](../adr/0025-binario-modular-con-roles.md)).

#### P-27 · ¿Quién opera Horus para varios ISP?
- **Por qué importa:** si un tercero (tú) opera una instalación para varios ISP, hacen falta vista
  global, límites por ISP y contratos de tratamiento de datos; si cada ISP instala el suyo, la vista
  global pierde peso. Afecta a P-11 y a la consola de plataforma.
- **Pregunta:** ¿una instalación compartida para varios ISP, una por ISP, o ambas?
- **Recomendación:** diseño multi-tenant (D6) válido para ambas; priorizar la instalación compartida
  operada por ti.

#### P-28 · Criterios de "uso comercial"
- **Por qué importa:** el scoring de I2 necesita saber qué consideras comercial
  ([`../traffic-model.md`](../traffic-model.md) §9) y qué se hace con el resultado.
- **Pregunta:** ¿qué señales te parecen comerciales (servidores expuestos, muchos dispositivos,
  horario laboral, volumen de subida)? ¿El resultado es solo informativo o tiene consecuencias
  (cambio de plan)?
- **Recomendación:** solo informativo, con razones y confianza; el tipo manual siempre prevalece.

#### P-13 · 2FA y SSO
- **Recomendación:** 2FA obligatorio para todos en I2; SSO después de 1.0.

#### P-09 · Zona horaria y formatos
- **Recomendación:** zona horaria por ISP (cortes diarios y kioscos) y preferencia por usuario.

### Antes de I3

#### P-17 · Avisos externos antes de I3
- **Pregunta:** ¿necesitas avisos por Telegram/email antes de I3 (p. ej. router *Silencioso*)?
- **Recomendación:** no; si urge, se adelanta un aviso mínimo de exportador silencioso.

#### P-12 · SMTP disponible
- **Recomendación:** si hay SMTP, canal email en I3; si no, Telegram y webhook.

#### P-14 · Feeds de reputación
- **Pregunta:** ¿presupuesto para feeds comerciales? ¿El ISP ya usa alguna lista?
- **Recomendación:** solo feeds abiertos con licencia de uso comercial en I1; evaluar comerciales con
  la tasa de falsos positivos medida.

#### P-22 · Reportes
- **Recomendación:** reporte semanal de seguridad y de consumo por ISP en PDF/CSV en I3.

#### P-29 · Destino de la copia remota
- **Por qué importa:** D2 deja el NAS opcional; sin copia remota no hay recuperación ante la pérdida
  del servidor ([ADR-0019](../adr/0019-almacenamiento-local-y-destino-remoto.md)). MediaFire no es
  viable (rclone no lo soporta).
- **Pregunta:** ¿qué destino usarás primero (SFTP a un NAS, Google Drive, MEGA, Dropbox)?
- **Recomendación:** SFTP a un NAS o servidor propio; cifrado del lado cliente con clave de
  recuperación guardada fuera de Horus.

### Pueden esperar

| ID | Pregunta | Recomendación |
| --- | --- | --- |
| P-08 | Idiomas de la interfaz | Solo español en v1, con i18n desde I0 |
| P-15 | GitHub o GitLab | GitHub + GitHub Actions (el repositorio ya está ahí; supuesto vigente en [`../conventions.md`](../conventions.md)) |
| P-16 | Herramientas actuales (The Dude, Zabbix, LibreNMS…) | Convivir; Horus no sustituye el monitoreo general en I1 |
| P-20 | Identidad visual | Marca propia sobria con un único color primario; estados con colores semánticos independientes ([`../frontend.md`](../frontend.md) §13.2) |
| P-30 | ¿Las pantallas NOC pueden mostrar IPs de clientes? | No por defecto (las ven visitas y cámaras); cada ISP puede activarlo por kiosco ([`../api.md`](../api.md) §2.12) |
