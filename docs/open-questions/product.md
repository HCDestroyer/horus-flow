# Preguntas abiertas — Producto

Preguntas para el product owner (PO). Cada una indica **por qué importa**, **qué bloquea** y
**mi recomendación**, que se toma como supuesto de trabajo si no hay respuesta antes de la fecha
límite. Las preguntas técnicas relacionadas viven en
[`architecture.md`](architecture.md) (Q1–Q16), [`data.md`](data.md),
[`contracts.md`](contracts.md) y [`security-ops.md`](security-ops.md); aquí se enlazan en lugar de
duplicarse.

Prioridad: **Bloquea S1** · **Antes de S3** · **Antes de S6** · **Puede esperar**.

## Resumen

| ID | Pregunta | Límite | Recomendación (supuesto si no hay respuesta) |
| --- | --- | --- | --- |
| P-01 | Fecha de inicio y calendario | Bloquea S1 | Sprint 1 empieza el lunes siguiente al cierre de S0 |
| P-02 | Tamaño y composición del equipo humano | Bloquea S1 | 4 flujos (≥ 1 persona o agente cada uno) + PO con 4 h/semana |
| P-03 | ¿Quién usa la plataforma? (personas y roles) | Antes de S2 | NOC, ingeniería de red, seguridad, gerencia; ventas como lector de reportes |
| P-04 | Fuente del mapa IP ↔ cliente y verdad de tipo de plan | **Antes de S4** | RADIUS accounting si existe; si no, API del router (PPPoE/DHCP) |
| P-05 | Escala real: routers, clientes, tráfico | Antes de S3 | Diseñar para 1 000 routers / 50 000 clientes; medir en S6 |
| P-06 | Fabricantes y modelos reales; ¿hay sFlow? | Antes de S3 | MikroTik primero; NetFlow v9/IPFIX antes que sFlow |
| P-07 | ¿Para qué es WireGuard: gestión de routers o servicio a clientes? | Antes de S3 | Túneles de gestión hacia routers |
| P-08 | Idioma(s) de la interfaz | Bloquea S1 | Solo español en v1, con i18n preparado desde S1 |
| P-09 | País, zona horaria y formatos | Antes de S2 | Zona de la organización por defecto, editable por usuario |
| P-10 | Prioridad entre WireGuard (S4) y SNMP (S5) | Antes de S3 | Mantener el orden si los routers solo son alcanzables por túnel; invertir si son alcanzables directamente |
| P-11 | Marco legal y uso aceptable del perfilado de clientes | Antes de S6 | Asesoría legal antes de S6; scoring solo como indicio con revisión humana |
| P-12 | ¿Hay servidor SMTP disponible? | Antes de S3 | Sí → recuperación por email en S3; no → solo restablecimiento por admin hasta S11 |
| P-13 | Política de 2FA y SSO corporativo | Antes de S2 | 2FA obligatorio para todos; SSO post 1.0 |
| P-14 | Presupuesto para feeds de reputación comerciales | Antes de S7 | Solo feeds abiertos en v1 |
| P-15 | ¿GitHub o GitLab? | Bloquea S1 | GitHub + GitHub Actions |
| P-16 | Herramientas actuales (Zabbix, LibreNMS, The Dude, PRTG…) | Antes de S3 | Convivir hasta el piloto; importar inventario desde la actual |
| P-17 | ¿Avisos de router caído por email/Telegram antes de S11? | Antes de S5 | Sí, solo routers core, vía Alertmanager |
| P-18 | ¿Aceptar ClickHouse dentro del MVP técnico (S5)? | Antes de S4 | Sí |
| P-19 | Entorno piloto y routers de laboratorio | Antes de S3 | 1 router de laboratorio por fabricante + 10 routers reales en S5 |
| P-20 | Identidad visual (marca, color, logo) | Antes de S1 (fin) | Marca sobria con un único color primario; sin "estética NOC" |
| P-21 | Contextos de uso: pantalla mural, móvil, campo | Antes de S3 | Escritorio primero; mural en S9; móvil de consulta |
| P-22 | ¿Quién consume los reportes y con qué formato/branding? | Antes de S11 | PDF con logo del ISP para gerencia; CSV/Excel para análisis |

---

## P-01 · Fecha de inicio y calendario
- **Por qué importa:** fija fechas reales de hitos (H4 MVP técnico en la semana 11, H7 Release 1.0
  en la semana 33, [`../roadmap.md`](../roadmap.md) §1–2) y la disponibilidad del PO en reviews.
- **Pregunta:** ¿cuándo arranca el Sprint 1? ¿Hay fechas comerciales o regulatorias que fijen el
  MVP o la 1.0? ¿Vacaciones o congelaciones de cambios en la red?
- **Recomendación:** S1 empieza el lunes siguiente al cierre de S0; no comprometer fecha de 1.0
  hasta medir la velocidad de S1–S2.

## P-02 · Tamaño y composición del equipo
- **Por qué importa:** el plan de 16 sprints solo cabe en 33 semanas con ~4 flujos en paralelo;
  con 1–2 personas son ~20–22 sprints ([`../roadmap.md`](../roadmap.md) §6).
- **Pregunta:** ¿cuántas personas (y con qué perfil: Go, frontend, redes, datos, DevOps)? ¿Se usan
  agentes de IA por flujo? ¿Quién revisa y aprueba los PRs de los agentes?
- **Recomendación:** mínimo 4 flujos de [`../backlog/team.md`](../backlog/team.md) (Plataforma,
  Backend core, Frontend, Datos) con un humano que actúe como coordinador/revisor de contratos y
  el PO con ≥ 4 h/semana (refinement + review). Si hay agentes de IA, un humano aprueba todo
  merge que toque `area:security` o contratos.

## P-03 · ¿Quién usa la plataforma?
- **Por qué importa:** define roles predefinidos ([`../security.md`](../security.md) §6.2),
  prioridad de pantallas y personas de las historias ([`../frontend.md`](../frontend.md) §2).
- **Pregunta:** ¿la usan el NOC 24/7, ingeniería de red, seguridad, soporte de primer nivel,
  técnicos de campo, ventas, gerencia? ¿Cuántos usuarios de cada tipo? ¿Ventas debe ver datos de
  consumo por cliente (dato personal)?
- **Recomendación:** usuarios principales NOC e ingeniería de red (diseño centrado en ellos);
  seguridad y gerencia como secundarios; ventas solo accede a reportes de "posibles comerciales"
  agregados, sin navegación por tráfico individual. Confirmar si hace falta un rol de **técnico de
  campo** con ACL por sitio (no existe en `security.md` §6.2; se podría derivar de
  `noc_operator` + ACL).

## P-04 · Fuente del mapa IP ↔ cliente *(crítica)*
- **Por qué importa:** sin saber qué IP tiene cada cliente en cada momento, S7, S9 y S10 solo
  entregan valor por IP, no por cliente (riesgo crítico, Q5 de [`architecture.md`](architecture.md),
  [`../roadmap.md`](../roadmap.md) §4.5). El scoring residencial/comercial necesita además saber qué
  plan tiene contratado cada cliente para calibrarse.
- **Pregunta:** ¿cómo reciben IP los clientes (PPPoE, DHCP/IPoE, estática, CGNAT)? ¿Existe RADIUS
  con accounting? ¿Qué sistema de facturación/CRM se usa y tiene API? ¿Qué campo distingue un plan
  residencial de uno comercial? ¿Hay CGNAT y dónde se exportan los flujos (antes o después)
  (Q6)?
- **Recomendación:** RADIUS accounting si existe (fuente autoritativa y en tiempo real); si no, API
  de los routers (sesiones PPPoE / leases DHCP de RouterOS). Importación CSV desde facturación para
  datos del cliente y su plan. Decidir **antes de S4** (spike) para implementar el adaptador en S5.

## P-05 · Escala real
- **Por qué importa:** dimensiona ClickHouse, NATS, retención y el muestreo (necesario desde ~200
  routers, [`../architecture.md`](../architecture.md)). Ver Q2 y Q10.
- **Pregunta:** número de routers hoy y en 2–3 años; número de clientes; tráfico pico agregado
  (Gbps); ¿cuántos routers exportarán flujos?
- **Recomendación:** diseñar para 1 000 routers y 50 000 clientes; medir flujos/s reales con un
  router en S6 antes de fijar retención (supuesto: raw 7 días).

## P-06 · Fabricantes, modelos y protocolos de flujo
- **Por qué importa:** orden de adaptadores SNMP (S5–S8) y colectores (NetFlow/IPFIX/sFlow, S6).
- **Pregunta:** proporción por fabricante y modelo; versiones de firmware; ¿qué routers exportan
  NetFlow v5/v9, IPFIX o sFlow? ¿Existe inventario en hoja de cálculo para importar en S3?
- **Recomendación:** MikroTik + MIB estándar primero; NetFlow v9/IPFIX antes que sFlow; entregar
  el inventario actual al inicio de S3 para la plantilla CSV.

## P-07 · ¿Para qué es WireGuard?
- **Por qué importa:** cambia el alcance de S4 (túneles de gestión vs producto VPN para clientes)
  y la seguridad (Q3, Q9 de [`architecture.md`](architecture.md)).
- **Pregunta:** ¿los túneles conectan los routers a un hub central para gestión/monitoreo? ¿O el
  ISP venderá VPN a clientes? ¿Quién genera las claves y Horus debe configurar el lado del router?
- **Recomendación:** v1 = túneles de **gestión** hacia routers, claves generadas por Horus,
  configuración del router descargable (no se empuja automáticamente hasta post-MVP).

## P-08 · Idioma(s) de la interfaz
- **Por qué importa:** coste de i18n y de copia; tiene que decidirse antes de escribir textos.
- **Pregunta:** ¿solo español? ¿Algún usuario necesita inglés (proveedores, auditores)?
- **Recomendación:** solo español en v1, con todas las cadenas en archivos de i18n desde S1
  (coste marginal); inglés si un cliente o auditor lo pide.

## P-09 · País, zona horaria y formatos
- **Por qué importa:** los datos se guardan en UTC; los dashboards diarios, reportes mensuales y
  el scoring por horario dependen de la zona local. También afecta al marco legal (P-11).
- **Pregunta:** ¿en qué país(es) opera el ISP? ¿Una sola zona horaria? ¿Formato de números y
  fechas?
- **Recomendación:** zona horaria de la organización como valor por defecto (cortes diarios y
  reportes), preferencia por usuario para la visualización.

## P-10 · Prioridad entre WireGuard y SNMP
- **Por qué importa:** el plan pone WireGuard (S4) antes que SNMP (S5). Tiene sentido si los routers
  solo son alcanzables por túnel; si no, el NOC obtendría valor antes con SNMP.
- **Pregunta:** ¿los routers son alcanzables hoy por la red de gestión sin túnel? ¿Qué aporta más
  al NOC en el primer mes: ver métricas o gestionar túneles?
- **Recomendación:** mantener el orden si la respuesta a P-07/Q3 es "gestión por túnel"; si los
  routers ya son alcanzables, **intercambiar S4 y S5** (no cambia dependencias técnicas: el
  sondeo ICMP ya está en S3).

## P-11 · Marco legal del perfilado de clientes
- **Por qué importa:** S6–S10 procesan metadatos de tráfico de abonados (dato personal) y S10
  etiqueta clientes como "posible uso comercial" (Q14 de [`architecture.md`](architecture.md)).
- **Pregunta:** ¿qué ley de protección de datos y de retención de telecomunicaciones aplica? ¿Los
  contratos permiten este análisis? ¿Qué se hará con un "posible comercial": revisión, contacto
  comercial, cambio de plan automático?
- **Recomendación:** consulta legal antes de S6; el scoring es un **indicio para revisión
  humana**, nunca una acción automática; acceso por cliente restringido a `traffic.client.read`
  y auditado.

## P-12 · SMTP disponible
- **Por qué importa:** recuperación de contraseña por email ([`../roadmap.md`](../roadmap.md) §4.1)
  y canal Email de alertas (S11).
- **Pregunta:** ¿hay servidor SMTP corporativo o servicio transaccional utilizable?
- **Recomendación:** si existe, recuperación por email en S3 (historia S03-18); si no, solo
  restablecimiento asistido por administrador hasta S11.

## P-13 · Política de 2FA y SSO
- **Por qué importa:** S2 implementa TOTP; `security.md` lo hace obligatorio para roles con
  permisos de gestión.
- **Pregunta:** ¿2FA obligatorio para todos? ¿Existe un IdP corporativo (Entra ID, Google
  Workspace) con el que se espere SSO?
- **Recomendación:** 2FA obligatorio para todos (son pocos usuarios con acceso a datos sensibles);
  SSO como relying party después de 1.0 salvo que sea requisito de compra.

## P-14 · Feeds de reputación
- **Por qué importa:** calidad de detección en S8 y coste recurrente.
- **Pregunta:** ¿hay presupuesto para feeds comerciales? ¿Alguna lista que el ISP ya use?
- **Recomendación:** feeds abiertos (abuse.ch, Spamhaus DROP, listas de Tor exit, etc., sujeto a
  sus licencias) en v1; evaluar comerciales con datos de falsos positivos del piloto.

## P-15 · Plataforma de repositorio y CI
- **Por qué importa:** S01-03 implementa el pipeline.
- **Pregunta:** ¿GitHub o GitLab (vision.md §10 admite ambos)? ¿Runners propios para pruebas con
  hardware?
- **Recomendación:** GitHub + GitHub Actions (el repo ya está ahí); runner propio en S5 para las
  pruebas con routers de laboratorio.

## P-16 · Herramientas actuales
- **Por qué importa:** migración de inventario, expectativas del NOC y coexistencia durante el
  piloto.
- **Pregunta:** ¿qué usa hoy el NOC para monitoreo, inventario y alertas? ¿Qué echa de menos?
- **Recomendación:** coexistir hasta H6 (piloto); importar inventario de la herramienta actual en
  S3; entrevistar a 2–3 operadores antes de S3 para validar el Resumen y el detalle de router.

## P-17 · Avisos de router caído antes de S11
- **Por qué importa:** entre S5 y S11 la plataforma detecta caídas pero solo las muestra en la UI
  ([`../roadmap.md`](../roadmap.md) §4.6, Q13 de [`architecture.md`](architecture.md)).
- **Pregunta:** ¿el NOC necesita email/Telegram por caída de router desde el MVP?
- **Recomendación:** sí, solo para routers marcados como core/críticos, mediante una métrica
  `horus_router_up` en Prometheus y Alertmanager (ya desplegado para infraestructura), sin
  adelantar el servicio `alerts`.

## P-18 · ClickHouse en el MVP técnico
- **Por qué importa:** `vision.md` §14 define el MVP sin ClickHouse; el Agente 2 propone guardar
  las series SNMP en ClickHouse desde S5 (C-03 de [`../roadmap.md`](../roadmap.md) §7). Cambia el
  documento fuente y requiere ADR.
- **Pregunta:** ¿se acepta añadir ClickHouse al MVP técnico?
- **Recomendación:** sí: evita una tabla puente en PostgreSQL y una migración de datos en S6, y
  adelanta el aprendizaje operativo de ClickHouse a un sprint con poco volumen.

## P-19 · Entorno piloto y laboratorio
- **Por qué importa:** la DoR de S3–S6 exige probar con equipos reales; las reviews deben mostrar
  funcionalidad real (`vision.md` §12).
- **Pregunta:** ¿hay routers de laboratorio? ¿Qué routers de producción pueden usarse en el piloto
  y con qué ventana de cambios? ¿Qué servidor aloja el entorno de staging/piloto (Q16)?
- **Recomendación:** 1 router de laboratorio por fabricante principal desde S3, 10 routers reales
  en S5 (H4) y exportación de flujos de 1–3 routers desde S6.

## P-20 · Identidad visual
- **Por qué importa:** tokens de color y logo se fijan en S1 (S01-13).
- **Pregunta:** ¿Horus Flow tiene marca propia o usa la del ISP? ¿Color corporativo?
- **Recomendación:** marca propia sobria con un único color primario reservado a acciones
  principales; estados con colores semánticos independientes de la marca
  ([`../frontend.md`](../frontend.md) §10.2); evitar la estética genérica de "NOC" negro y verde.

## P-21 · Contextos de uso
- **Por qué importa:** prioridad de responsive, modo mural y rendimiento en móvil.
- **Pregunta:** ¿hay pantalla mural en el NOC? ¿Los técnicos de campo usarán tablet/móvil y con qué
  conectividad?
- **Recomendación:** escritorio primero; modo mural en S9; móvil para consulta (estado, alertas,
  detalle de router) sin edición compleja.

## P-22 · Reportes
- **Por qué importa:** alcance de S12 (plantillas, programación, distribución).
- **Pregunta:** ¿quién recibe reportes, con qué frecuencia, en qué formato? ¿Necesitan logo del
  ISP? ¿Se envían por email automáticamente?
- **Recomendación:** PDF mensual con logo para gerencia, CSV/Excel bajo demanda para analistas;
  envío programado por email después de S12 si P-12 lo permite.
