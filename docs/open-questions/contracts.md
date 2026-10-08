# Preguntas abiertas — Contratos (API, WebSocket, gRPC, eventos)

> Ronda 2 · Responsable: Agente C. Referencias: [`api.md`](../api.md), [`events.md`](../events.md),
> [`po-decisions.md`](../po-decisions.md). Si no hay respuesta antes del incremento indicado, **se adopta la
> recomendación** y se registra como ADR.

## Resueltas por el product owner

| ID | Pregunta | Resolución |
| --- | --- | --- |
| C-14 | ¿Varios ISP (multi-tenant)? | **Sí, desde v1** (D6, [ADR-0017](../adr/0017-multi-tenant-desde-v1.md)): access token por tenant (`tid`) obtenido con `POST /auth/token`, rutas de negocio sin tenant, plataforma en `/api/v1/platform/*`; `tenant_id` obligatorio en el sobre y cabecera `Horus-Tenant` en NATS, sin token de tenant en subjects ([`api.md`](../api.md) §0, [`events.md`](../events.md) §2.4). |
| C-17 | ¿Gráficas históricas SNMP en el MVP antes de `analytics`? | **Resuelta por D4** ([ADR-0021](../adr/0021-clickhouse-desde-el-primer-incremento.md)): ClickHouse desde el primer incremento; las series las sirve `analytics` sin enrutado temporal a `snmp`. |
| C-08 | Claves privadas WireGuard de los routers | **Resuelta por D10** ([ADR-0022](../adr/0022-mikrotik-routeros-v7-primer-fabricante.md)): la clave se genera en el MikroTik; Horus sólo recibe la pública por `POST /enroll/wireguard` con token de un solo uso. (El supuesto "WireGuard conecta los nodos con Horus" sigue pendiente de confirmación formal del PO.) |
| C-12 (parcial) | Volumen de flujos y autonomía del buffer | D10 fija la fuente (Traffic Flow IPFIX sin muestreo); el volumen real sigue abierto (abajo). |
| C-13 (parcial) | Bytes estimados por muestreo | Con MikroTik sin muestreo (D10) el factor es 1 en el caso normal; el contrato (`sampling_rate` en lote y registro) se mantiene para otros fabricantes. |

## Abiertas

| ID | Pregunta | Recomendación | Impacto si se decide otra cosa | Antes de |
| --- | --- | --- | --- | --- |
| C-01 | ¿Frontend y API en el **mismo dominio**? | Mismo dominio detrás de Traefik (sin CORS; cookie de sesión `SameSite=Strict`). El enrolamiento de routers usa el mismo host público. | Dominios distintos: CORS con lista blanca y cookie `SameSite=None`. | Incremento 1 |
| C-02 | Retención de eventos de dominio en NATS | 30 días (buffer técnico/replay). Con tenant sin token en subject, es también el plazo en que los eventos de un tenant dado de baja desaparecen del bus. | 7 días: menos margen de re-proyección; baja más rápida. | Incremento 1 |
| C-03 | ¿Saltar a la página N en tablas? | Cursor en todo; `include_total` sólo en colecciones administrativas. La lista de clientes (decenas de miles de IPs por ISP) **no** tendrá total exacto. | Offset limitado en auditoría/alertas. | Incremento 1 |
| C-04 | ¿WebSocket con replay sin pérdida? | Resincronizar por REST (best effort). Las pantallas NOC recargan sus widgets al reconectar. | Consumidores efímeros por réplica y cursores. | Incremento 6 |
| C-05 | ¿AsyncAPI para integradores? | No en v1; se genera del catálogo si aparece un integrador. | Generador en CI (~1–2 días). | — |
| C-06 | Idioma de errores y UI | `code` estable en inglés; `title/detail` en español; i18n en el frontend por `code`. | — | Incremento 1 |
| C-07 | ¿Qué ven los usuarios de un ISP en `GET /system/status`? | Resumen de capacidades; detalle de infraestructura sólo para plataforma (es compartida). | — | Incremento 1 |
| C-09 | Umbrales `warning`/`critical` de routers | Perfiles por modelo MikroTik con override por router, evaluados por `snmp`. | Si los define `alerts`, el dashboard inicial sólo muestra online/degraded/offline. | Incremento 2 |
| C-10 | Ticket del WebSocket en query string | Sí (un uso, 30 s, redactado en logs). | Ticket en `Sec-WebSocket-Protocol`. | Incremento 1 |
| C-11 | Pérdida aceptable ante corte eléctrico de NATS (nodo único) | Dominio/auditoría: cero (outbox). Telemetría: hasta ~2 min. Con un solo servidor (D7) no hay clúster NATS. | `sync_interval: always`. | Incremento 3 |
| C-12 | **Volumen real de flujos**: ¿cuántos ISP, nodos y routers el primer año? ¿horas de caída de ClickHouse a aguantar? | Objetivo 6 h a tasa pico **sumando todos los tenants**; `max_bytes` de `TLM_FLOWS` = `tasa_pico × 6 h × 1,2`; cuota `max_flows_per_second` por tenant. | Determina el SSD del servidor. | Incremento 3 |
| C-15 | Resolución en tiempo real de métricas de router | 60 s; mínimo 15 s en routers core. | 15 s para todos: ×4 volumen. | Incremento 2 |
| C-16 | Usuarios y **pantallas NOC** concurrentes | 50 usuarios + 20 kioscos por instalación; diseño para 10.000 conexiones por réplica. | Sólo pruebas de carga. | Incremento 6 |
| C-18 | Días sin tráfico para marcar un cliente-IP como `inactive` | 30 (configurable por tenant), purga a 25 meses sin actividad ([`database.md`](../database.md)). | Más días: listas de clientes "activos" infladas con IPs dinámicas. | Incremento 3 |
| C-19 | ¿Las pantallas NOC (kiosco) pueden mostrar **IPs de clientes** y hallazgos por cliente? | No por defecto; activable por kiosco con motivo auditado ([`api.md`](../api.md) §2.12). | Si sí por defecto: exposición de datos personales a visitas/cámaras. | Incremento 6 |
| C-20 | ¿El scoring **cambia** el tipo de cliente automáticamente o sólo **sugiere**? | Cambia si `kind_locked = false` y la confianza ≥ 80 % con histéresis de 7 días (D1 dice "se puede actualizar"); el manual bloquea. | Sólo sugerencia: más trabajo manual, menos errores visibles. | Incremento 7 |
| C-21 | **Mitigación activa** de botnets en el MikroTik (address-list, walled garden) | Fuera de v1 (Horus detecta y notifica); ver Q23 de [`architecture.md`](architecture.md). Si se aprueba: usuario de escritura distinto, acción con aprobación humana. | Credenciales de escritura en routers: otro nivel de riesgo. | Incremento 5 |
| C-22 | ¿Hace falta purgar del bus los eventos de un tenant **inmediatamente** en su baja? | No: caducan en 30 días ([`events.md`](../events.md) §2.4). | Sí: añadir token de tenant por *subject mapping*. | Incremento 1 |
