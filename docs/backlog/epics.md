# Épicas

Épicas del proyecto tras las decisiones del PO ([`../po-decisions.md`](../po-decisions.md)),
ordenadas por incremento según [`../roadmap.md`](../roadmap.md). Formato de IDs y reglas en
[`README.md`](README.md); agentes en [`team.md`](team.md).

Se conservan los IDs del Sprint 0 cuando la épica sigue existiendo, para no romper referencias;
las épicas nuevas usan números ≥ 23. La columna **Agente** es el responsable principal.

## Resumen

| ID | Épica | Incremento(s) | Agente | Estado tras ronda 2 |
| --- | --- | --- | --- | --- |
| EP-00 | Arquitectura y decisiones base | Sprint 0 + ronda 2 | Todos | Hecha (documentación) |
| EP-01 | Plataforma de ejecución y DevOps | I0 (+ continuo) | PLAT | Recortada: sin MinIO/Redis |
| EP-02 | Shell del frontend | I0 | UI | Ampliada con selector de ISP |
| EP-03 | Autenticación y sesiones | I0 (mínima definitiva) · I2 (TOTP, sesiones) | CORE | Dividida |
| EP-04 | Autorización y auditoría | I0 (roles + aislamiento por ISP) · I2 (roles por ISP, auditoría) | CORE | ACL por sitio sustituida por aislamiento por ISP |
| EP-05 | Inventario de red | I0 (ISP, nodo, router, prefijos) · I1 (importación de pools) · I2 (asistente) | CORE | Recortada |
| EP-06 | **Clientes por IP (descubrimiento automático)** | I1 · I2 (tipo comercial) | FLOW + CORE | **Reescrita por D1** |
| EP-07 | WireGuard | I1 (script RouterOS) · I3 (gestionado) | CORE | Aplazada en parte |
| EP-08 | Monitoreo de routers (ICMP + SNMP) | I2 | FLOW | Aplazada |
| EP-09 | Notificaciones en vivo en UI | I1 | CORE + UI | Adelantada como parte de I1 |
| EP-10 | Colección de flujos | I1 | FLOW | Núcleo de I1 |
| EP-11 | Enriquecimiento IP → ASN → organización | I0 (datasets) · I1 (ingesta) | FLOW | Sin cambios de fondo |
| EP-12 | Catálogo de servicios y categorías | I1 (semilla) · I4 (editor) | FLOW | Editor aplazado |
| EP-13 | Reputación | I0 (feeds) · I1 (match) · I4 (decaimiento) | SEC | Adelantada por D5 |
| EP-14 | **Detección de botnets** | I1 (señales de `traffic-model.md` §8) · I4 (madurez) | SEC | **Prioridad máxima por D5** |
| EP-15 | Analítica de tráfico | I1 (tops) · I2 (vistas avanzadas) | FLOW + UI | Repartida |
| EP-16 | Perfil residencial / comercial | I1 (manual) · I2 (scoring) | SEC | Reorientada por D1 |
| EP-17 | Alertas y notificaciones externas | I3 | CORE | Aplazada |
| EP-18 | Reportes | I3 | FLOW + UI | Recortada |
| EP-19 | Retención, backup y copia remota | I1 (TTL, backup local, aviso) · I3 (destinos remotos) | PLAT | Reescrita por D2 |
| EP-20 | Resiliencia | Continuo · I5 | PLAT | Sin cambios de fondo |
| EP-21 | Rendimiento y escala | I1 (básica) · I5 | PLAT + FLOW | Sin cambios de fondo |
| EP-22 | Release 1.0 y operación | I1 (instalador) · I5 | PLAT | |
| EP-23 | **Multi-tenant (ISP)** | I0 · I1 · I2 (vista global) | CORE + UI | **Nueva por D6** |
| EP-24 | **Dashboard modular** | I0 (marco) · I1 (plantillas) · I2 (editor) | UI | **Nueva por D8** |
| EP-25 | **Modo NOC / kiosco** | I1 | UI + CORE | **Nueva por D8** |
| EP-26 | **Onboarding MikroTik** | I1 (script) · I2 (asistente + API) | CORE | **Nueva por D10** |
| EP-27 | Mitigación asistida | I4 (si P-23 = sí) | SEC + CORE | Nueva, condicionada |
| EP-28 | Otros fabricantes y sFlow | I6 | FLOW | Aplazada por D10 |
| EP-T1 | Observabilidad | Transversal desde I0 | PLAT | |
| EP-T2 | Seguridad y privacidad transversal | Transversal desde I0 | Todos (aprueba la persona) | |
| EP-T3 | Sistema de diseño y accesibilidad | Transversal desde I0 | UI | |
| EP-T4 | Calidad, simuladores y pruebas de aceptación | Transversal desde I0 | INT + FLOW | Ampliada: `make accept-iN` |
| EP-T5 | Tiempo real (WebSocket) | I0 (base) · I1 | CORE + UI | |

---

## EP-00 · Arquitectura y decisiones base
- **Objetivo:** decisiones caras de revertir escritas y coherentes.
- **Estado:** hecha en Sprint 0 y revisada en ronda 2 con D1–D10. Lo pendiente vive en
  [`../open-questions/`](../open-questions/README.md).

## EP-01 · Plataforma de ejecución y DevOps
- **Objetivo:** entorno reproducible y CI que impida regresiones sin revisión humana constante.
- **Alcance:** monorepo, compose de desarrollo y de producción de un servidor, CI (lint, tests,
  contratos, seguridad, build), protección de `main`, auto-merge con CI verde y revisión de agente,
  imágenes versionadas, `make up / sim / accept-iN`.
- **Fuera:** Kubernetes, MinIO, Redis/Valkey (salvo que la arquitectura lo requiera).
- **Cierre:** clon limpio → `make up && make accept-i0` en verde sin intervención; CI < 15 min.

## EP-02 · Shell del frontend
- **Alcance:** Nuxt 4 + Nuxt UI, login, layout, navegación por permisos, **selector de ISP**, tema
  claro/oscuro, i18n, cliente API generado, mocks, estados globales. Ver
  [`../frontend.md`](../frontend.md) §3–§5.
- **Cierre:** e2e de login → selector de ISP → dashboard verde; axe sin violaciones serias.

## EP-03 · Autenticación y sesiones
- **I0:** Argon2id, access token corto + refresh rotatorio, logout, `GET /me` con ISPs accesibles
  y permisos efectivos, bloqueo progresivo. Es la versión definitiva, no un mock.
- **I2:** TOTP, sesiones revocables con UI, restablecimiento asistido.
- **Fuera:** SSO, WebAuthn (post 1.0 salvo petición).

## EP-04 · Autorización y auditoría
- **I0:** roles `superadmin` (global), `isp_admin`, `isp_operator`, `isp_viewer`; pertenencia
  usuario ↔ ISP; middleware de tenant; suite de matriz de aislamiento en CI.
- **I1:** registro de acceso a detalle de cliente y cambios de tipo (base de la auditoría).
- **I2:** auditoría consultable, rol de seguridad por ISP, roles personalizados si se piden.
- **Cierre:** matriz de permisos y de tenants automatizada para cada endpoint y tema WebSocket.

## EP-05 · Inventario de red
- **I0:** ISP → nodo → router principal (MikroTik, versión de RouterOS) → prefijos de clientes
  del nodo (`client_prefix` con rol *customers* / *infrastructure* / *excluded*).
- **I1:** importación de pools desde el MikroTik (API de solo lectura, credenciales cifradas) y modo
  descubrimiento con propuestas de prefijos ([`../traffic-model.md`](../traffic-model.md) §4.1).
- **I2:** asistente de alta, interfaces y su `flow_role`, alias PPPoE opcionales.
- **Fuera:** catálogo de modelos/firmware, CSV, tags y grupos (hasta que se pidan).

## EP-06 · Clientes por IP (descubrimiento automático) *(reescrita por D1)*
- **Objetivo:** que cada IP de cliente vista en los flujos del router de un nodo sea un cliente,
  sin CRM, RADIUS ni facturación.
- **Alcance I1:** identidad `(ISP, realm, IP)` (IPv6 por `/64`); alta automática con `first_seen`,
  `last_seen`, tipo por defecto del prefijo (`residential`); ciclo de vida *activo* → *inactivo*
  (30 días) → purga (25 meses) y "reiniciar cliente" ([`../database.md`](../database.md) §2.3.4);
  alias opcional; cambio manual de tipo con motivo, `kind_locked` e historial inmutable; lista,
  detalle y consumo por cliente.
- **Alcance I2:** tipo `commercial` detectado (ver EP-16) con desbloqueo del tipo manual.
- **Fuera:** sincronización con CRM, datos personales del abonado (nombre, dirección).
- **Cierre:** ≥ 95 % de los bytes del lado cliente del router piloto atribuidos a un cliente
  descubierto (el resto, fuera de prefijos declarados, se muestra como "No atribuido").

## EP-07 · WireGuard
- **I1:** hub mínimo, IPAM de plataforma, enrolamiento del router con token de un uso
  (`POST /enroll/wireguard`), estado de handshake; clave privada generada en el router
  ([ADR-0022](../adr/0022-mikrotik-routeros-v7-primer-fabricante.md), [`../vendors/mikrotik.md`](../vendors/mikrotik.md) §5).
- **I3:** rotación de claves del router, revocación desde la UI, segundo hub, gestión de rangos.

## EP-08 · Monitoreo de routers (ICMP + SNMP)
- **I2:** ICMP + SNMP v2c/v3 (MikroTik + MIB estándar), estado observado con razón
  (online/degraded/offline/stale), interfaces físicas/VLAN/uplinks.
- **Fuera:** traps, interfaces dinámicas por cliente.

## EP-09 · Notificaciones en vivo en UI
- **I1:** centro de notificaciones y avisos en dashboard/kiosco por WebSocket: hallazgo nuevo,
  exportador silencioso/recuperado. Sin reglas ni canales externos.

## EP-10 · Colección de flujos
- **I1:** NetFlow v9 e IPFIX de RouterOS (v5 si sale gratis), solo de exportadores registrados;
  plantillas por exportador; lotes; ingesta a ClickHouse; TTL del crudo; métricas de recepción y
  descarte; estado del exportador.
- **I5/I6:** muestreo a escala, sFlow.
- **Cierre:** caudal objetivo sostenido 1 h sin pérdida ni lag creciente.

## EP-11 · Enriquecimiento IP → ASN → organización
- **I0:** cargador de datasets con versión, licencia y checksum registrados.
- **I1:** enriquecimiento en ingesta; métrica de cobertura (% de bytes con ASN).
- **Cierre:** ≥ 95 % de bytes remotos con ASN.

## EP-12 · Catálogo de servicios y categorías
- **I1:** catálogo semilla versionado (≈ 30 servicios principales y ≈ 10 categorías: vídeo,
  redes sociales, mensajería, juegos, CDN/nube, actualizaciones, correo, VPN/proxy, otros).
- **I4:** editor con vista previa del impacto.

## EP-13 · Reputación
- **I0:** cargador de feeds abiertos de uso comercial permitido (p. ej. listas de C2 de abuse.ch,
  Spamhaus DROP/EDROP, nodos de salida Tor — sujeto a la licencia de cada uno, P-14).
- **I1:** búsqueda en ingesta o en detección (IP remota ∈ feed) con fuente y fecha.
- **I4:** puntuación con decaimiento, varias fuentes, pesos.

## EP-14 · Detección de botnets *(prioridad máxima por D5)*
- **Objetivo:** detectar clientes cuyas IPs muestran señales compatibles con participación en
  botnets, con explicación suficiente para actuar.
- **I1:** señales de [`../traffic-model.md`](../traffic-model.md) §8: C2 conocido por reputación
  (también retroactivo), escaneo, fan-out, puertos vigilados, SMTP saliente, DDoS; beaconing y
  salida sostenida como *should*. Hallazgos con razones, confianza, deduplicación, allowlist del ISP
  y estados de [`../api.md`](../api.md) §2.10; retroalimentación "falso positivo".
- **I4:** DNS anómalo, servicios entrantes inesperados, propagación interna, línea base por
  cliente, mejores pesos de reputación.
- **Cierre I1:** los escenarios del simulador producen exactamente los hallazgos esperados y el
  escenario normal ninguno.

## EP-15 · Analítica de tráfico
- **I1:** tops por cliente, servicio, categoría y ASN/organización; series por ISP, nodo y cliente.
- **I2:** comparativas, heatmap hora × día, vista global de superadmin.
- **Cierre:** cada consulta de dashboard < 2 s p95 con 30 días de agregados.

## EP-16 · Perfil residencial / comercial *(reorientada por D1)*
- **I1:** tipo por defecto `residential`, cambio manual con motivo.
- **I2:** scoring explicable (servicios expuestos con conexiones entrantes, ratio de subida,
  actividad 24/7, nº de destinos distintos, horarios laborales…) con confianza y razones;
  umbrales por ISP; el tipo manual prevalece.
- **Cierre:** ≥ 80 % de acuerdo de la persona en una muestra revisada.

## EP-17 · Alertas y notificaciones externas
- **I3:** reglas predefinidas, ciclo de vida, deduplicación, silencios; Telegram, email, webhook.

## EP-18 · Reportes
- **I3:** reporte semanal de seguridad y de consumo por ISP en PDF/CSV, programado.

## EP-19 · Retención, backup y archivo externo *(reescrita por D2)*
- **I1:** TTL de ClickHouse desde la primera migración; backup local diario de PostgreSQL (y
  de configuración) con restauración probada en CI.
- **I1:** consola de plataforma con uso de disco y aviso permanente "Sin copia remota configurada".
- **I3:** destino remoto opcional por SFTP (luego Drive/MEGA/Dropbox con rclone; **MediaFire fuera
  de alcance**, rclone no lo soporta), cifrado del lado cliente con confirmación de que la clave de
  recuperación se guardó fuera de Horus, verificación de checksum. Ningún componente depende de
  que exista ([ADR-0019](../adr/0019-almacenamiento-local-y-destino-remoto.md)).

## EP-20 · Resiliencia
- Cada incremento añade pruebas de fallo de lo nuevo (p. ej. I1: reiniciar ClickHouse durante la
  ingesta; cortar la red del kiosco). Cierre en I5 con suite completa en CI nocturno.

## EP-21 · Rendimiento y escala
- **I1:** carga de un nodo al caudal medido. **I5:** 10/100/500/1 000 routers.

## EP-22 · Release 1.0 y operación
- **I1:** instalador de un servidor y guía para la persona. **I5:** manual, actualización, DR, `v1.0.0`.

## EP-23 · Multi-tenant (ISP) *(nueva por D6)*
- **Objetivo:** varios ISP en una instalación, cada uno con varios nodos y un router principal por
  nodo, aislados en datos, API, eventos, permisos y UI.
- **I0:** `tenant_id` en tablas, tokens, consultas, eventos y temas WS; suite de aislamiento.
- **I1:** aislamiento extendido a ClickHouse, hallazgos y kiosco.
- **I2:** vista global de superadmin, segundo ISP real.
- **Cierre:** ninguna prueba de la matriz de aislamiento falla; ningún endpoint sin ámbito de ISP
  salvo los de plataforma.

## EP-24 · Dashboard modular *(nueva por D8)*
- **I0:** registro de widgets, manifiesto, tarjeta con estados, grilla de solo lectura.
- **I1:** widgets de NOC y seguridad; plantillas "NOC del ISP" y "Seguridad".
- **I2:** editor (arrastrar, redimensionar, alternativa de teclado), guardado por usuario y por
  ISP, duplicar plantillas, vista global.
- Diseño en [`../frontend.md`](../frontend.md) §6.

## EP-25 · Modo NOC / kiosco *(nueva por D8)*
- **I1:** kiosco como dispositivo registrado ([`../api.md`](../api.md) §2.12: código de un uso,
  credencial HttpOnly rotativa, CIDR permitido, sin datos de clientes por defecto), pantalla completa, rotación de
  dashboards, autorrefresco, legibilidad a distancia, tema oscuro, sin interacción, recuperación
  automática de desconexiones y de versiones nuevas. Diseño en [`../frontend.md`](../frontend.md) §7.
- **Cierre:** 24 h sin intervención con cortes simulados.

## EP-26 · Onboarding MikroTik *(nueva por D10)*
- **I1:** script de onboarding de [`../vendors/mikrotik.md`](../vendors/mikrotik.md) §7 con
  enrolamiento automático, verificación en vivo (clave → handshake → flujos → clientes), importación
  de pools y script de desinstalación. Horus no escribe en el router.
- **I2:** asistente con lecturas adicionales por API (interfaces, alias PPPoE, comparación de CPU).

## EP-27 · Mitigación asistida *(condicionada a P-23)*
- **I4:** cuarentena de una IP en address-list de MikroTik vía API, con aprobación humana,
  caducidad y reversión auditada. Nunca automática en v1.

## EP-28 · Otros fabricantes y sFlow
- **I6:** adaptadores por fabricante (Cisco, Huawei, Juniper) y sFlow.

---

## Épicas transversales

- **EP-T1 · Observabilidad:** logs JSON con `trace_id` y `tenant_id`, métricas RED, `healthz`/`readyz`,
  métricas de negocio de ingesta (flujos/s por exportador, descartes, lag). Detalle en
  [`../observability.md`](../observability.md).
- **EP-T2 · Seguridad y privacidad:** gestión de secretos, minimización (IPs de clientes fuera de
  URLs y logs info), acceso a detalle de cliente registrado, revisión por la persona de todo
  `area:security`. Detalle en [`../security.md`](../security.md).
- **EP-T3 · Diseño y accesibilidad:** tokens, estados, WCAG 2.1 AA, tema oscuro de NOC, i18n.
  Detalle en [`../frontend.md`](../frontend.md).
- **EP-T4 · Calidad y aceptación:** pirámide de tests, simulador de flujos con escenarios,
  capturas reales de RouterOS como fixtures, `make accept-iN` como definición ejecutable de cada
  incremento.
- **EP-T5 · Tiempo real:** WebSocket en el gateway, temas con ámbito de ISP, reanudación,
  frescura en la UI. Contrato en [`../api.md`](../api.md).
