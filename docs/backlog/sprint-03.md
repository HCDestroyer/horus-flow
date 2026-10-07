# Sprint 3 — Inventario

- **Objetivo del sprint:** el NOC registra y organiza sus sitios, routers y credenciales en
  Horus Flow, y ve en el Resumen cuántos routers responden y cuántos no.
- **Ajustes respecto a `vision.md`** ([`../roadmap.md`](../roadmap.md) §4): sondeo ICMP en el
  servicio `snmp`, que nace en este sprint (§4.2); aplicación de ACL a sitios y grupos (§4.1); entidad Cliente y
  asignación IP→cliente (§4.5); spike de WireGuard en contenedores para S4.
- **Estados posibles en S3:** `online`, `offline`, `stale`, `unknown`. `warning`, `critical` y
  `degraded` requieren SNMP y llegan en S5; la UI ya los soporta (enum pendiente de unificar, C-06).
- **Épicas:** EP-05, EP-04, EP-06, EP-08, EP-T5.

| ID | Historia | Flujo | Área | Pts | Prio | Depende de |
| --- | --- | --- | --- | --- | --- | --- |
| S03-01 | Catálogo: vendors, modelos y firmware | B | backend | 3 | Must | S02-02 |
| S03-02 | Sitios con jerarquía lógica | B | backend | 3 | Must | S02-02 |
| S03-03 | Routers: CRUD | B | backend | 5 | Must | S03-01, S03-02 |
| S03-04 | Credenciales cifradas | B | backend, security | 5 | Must | S03-03 |
| S03-05 | Interfaces e IPs (manual) | B | backend | 3 | Should | S03-03 |
| S03-06 | Tags y grupos | B | backend | 3 | Must | S03-03 |
| S03-07 | Importación CSV de routers | B | backend | 3 | Should | S03-03 |
| S03-08 | ACL aplicada a sitios y grupos | B | backend, security | 3 | Must | S02-09, S03-06 |
| S03-09 | Sondeo ICMP y eventos de alcanzabilidad | D | data | 5 | Must | S03-03 |
| S03-10 | Entidad Cliente y asignación IP→cliente | B | backend, data | 5 | Must | S03-02 |
| S03-11 | Pantalla: lista de routers | F | frontend | 5 | Must | S03-03 |
| S03-12 | Pantalla: alta/edición de router y credenciales | F | frontend | 5 | Must | S03-03, S03-04 |
| S03-13 | Pantalla: detalle de router (pestaña Resumen) | F | frontend | 3 | Must | S03-03 |
| S03-14 | Spike: WireGuard desde contenedor | P | infra, security | 3 | Must | — |
| S03-15 | Pantalla: sitios y grupos | F | frontend | 3 | Must | S03-02, S03-06 |
| S03-16 | Resumen con estado de routers en vivo | F | frontend | 3 | Must | S03-09, S01-12 |
| S03-17 | Pantalla: catálogo de vendors/modelos | F | frontend | 2 | Could | S03-01 |
| S03-18 | Recuperación de contraseña por email | B | backend, security | 3 | Could | P-12 |
| S03-19 | Datos de prueba de inventario (seed) | D | data | 2 | Must | S03-03 |

Total: 68 pts.

---

### S03-01 · Catálogo de vendors, modelos y firmware
- **Épica:** EP-05 · **Pts:** 3 · **Servicio:** devices

1. **Dado** la migración inicial, **cuando** arranca devices, **entonces** existen vendors semilla
   (MikroTik, Cisco, Huawei, Juniper, Otro) con modelos comunes (lista final con P-06).
2. **Dado** `devices.update`, **cuando** creo un modelo o versión de firmware, **entonces** queda
   disponible para routers; nombres duplicados por vendor → 409.
3. **Dado** un modelo con routers asociados, **cuando** intento borrarlo, **entonces** 409.

### S03-02 · Sitios
- **Épica:** EP-05 · **Pts:** 3

**Como** operador NOC **quiero** organizar routers por sitio (POP, torre, nodo) **para** saber
dónde está cada equipo.

1. **Dado** `devices.create`, **cuando** creo un sitio con nombre, código, tipo, dirección opcional
   y coordenadas opcionales, **entonces** se crea con UUIDv7.
2. **Dado** un sitio padre, **cuando** creo un sitio hijo, **entonces** se forma la ubicación lógica
   (p. ej. Región › Ciudad › POP), con profundidad máxima 4 y sin ciclos.
3. **Dado** un sitio con routers, **cuando** intento borrarlo, **entonces** 409 indicando cuántos.

### S03-03 · Routers: CRUD
- **Épica:** EP-05 · **Pts:** 5 · **Servicio:** devices

**Como** operador NOC **quiero** registrar, editar y eliminar routers **para** mantener el inventario
al día.

1. **Dado** `devices.create`, **cuando** registro un router con nombre, IP de gestión, sitio, modelo,
   firmware y tags, **entonces** se crea en estado `unknown` y se publica
   `horus.devices.router.created`.
2. **Dado** una IP de gestión ya usada, **cuando** registro, **entonces** 409.
3. **Dado** `devices.update`, **cuando** edito, **entonces** se publica
   `horus.devices.router.updated` con los campos cambiados y se audita.
4. **Dado** `devices.delete`, **cuando** elimino, **entonces** se hace borrado lógico, se publica
   `horus.devices.router.deleted` y deja de sondearse en < 1 ciclo.
5. **Dado** `GET /api/v1/routers`, **cuando** filtro por sitio, grupo, tag, vendor, estado y texto,
   **entonces** obtengo resultados paginados en < 300 ms p95 con 1 000 routers.
6. **Dado** un usuario con ACL limitada a un sitio, **cuando** lista, **entonces** solo ve los routers
   de ese sitio (S03-08).

### S03-04 · Credenciales cifradas
- **Épica:** EP-05, EP-T2 · **Pts:** 5 · **Área:** backend, security

**Como** operador NOC **quiero** guardar las credenciales SNMP/SSH/API de cada router **para** que los
colectores accedan sin que yo las comparta por chat.

1. **Dado** un router, **cuando** asocio una credencial (SNMP v2c community, SNMP v3
   usuario/auth/priv, SSH, API RouterOS), **entonces** se cifra con el esquema de
   [`../security.md`](../security.md) antes de persistir.
2. **Dado** la API, **cuando** leo una credencial, **entonces** **nunca** se devuelve el secreto; solo
   tipo, usuario, fecha de último cambio y quién la cambió.
3. **Dado** una credencial compartida por varios routers (perfil), **cuando** la roto, **entonces** se
   aplica a todos y se audita.
4. **Dado** el servicio que sondea (snmp en S5), **cuando** necesita el secreto, **entonces** lo
   obtiene por gRPC interno autenticado, nunca vía API pública.

### S03-05 · Interfaces e IPs (manual)
- **Épica:** EP-05 · **Pts:** 3 · **Prio:** Should

1. **Dado** un router, **cuando** registro interfaces (nombre, tipo, descripción) e IPs/prefijos,
   **entonces** quedan asociadas; en S5 el descubrimiento SNMP las reconcilia sin duplicar.
2. **Dado** una IP repetida en el mismo VRF, **cuando** se registra, **entonces** 409.

### S03-06 · Tags y grupos
- **Épica:** EP-05 · **Pts:** 3

1. **Dado** un router, **cuando** le asigno tags libres (`core`, `backhaul`, `cliente-empresa`),
   **entonces** puedo filtrar por ellos.
2. **Dado** un grupo estático (lista de routers) o dinámico (regla por tag/sitio/vendor), **cuando** lo
   creo, **entonces** se calcula su membresía y se usa en ACL y, después, en alertas y reportes.

### S03-07 · Importación CSV
- **Épica:** EP-05 · **Pts:** 3 · **Prio:** Should

**Como** operador NOC **quiero** importar mis routers desde un CSV **para** no darlos de alta uno a uno.

1. **Dado** un CSV con la plantilla descargable, **cuando** lo subo en modo validación, **entonces**
   veo cuántas filas se crearían, actualizarían o fallarían, con el error por fila, sin aplicar nada.
2. **Dado** la validación correcta, **cuando** confirmo, **entonces** se aplica en una transacción
   (todo o nada) y se audita como una operación con N cambios.

### S03-08 · ACL aplicada a sitios y grupos
- **Épica:** EP-04 · **Pts:** 3

1. **Dado** una concesión ACL "rol Técnico Norte → sitio Norte (y descendientes) → devices.*",
   **cuando** un técnico lista, ve o edita routers, **entonces** solo puede sobre los de ese sitio; el
   resto → 404 (no revelar existencia).
2. **Dado** un evento WebSocket de un router fuera de su ACL, **cuando** se emite, **entonces** no se
   le entrega.

### S03-09 · Sondeo ICMP y alcanzabilidad
- **Épica:** EP-08 · **Pts:** 5 · **Servicio:** `snmp` (nace en S3 solo con ICMP,
  [`../services.md`](../services.md) §1.1)

**Como** operador NOC **quiero** saber si cada router responde **para** detectar caídas antes de que
llamen los clientes.

1. **Dado** routers activos, **cuando** corre el sondeo (intervalo por defecto 30 s, configurable),
   **entonces** cada router recibe N pings y se calcula alcanzable/no alcanzable, RTT y pérdida.
2. **Dado** que un router cambia de estado tras las observaciones de histéresis
   ([`../architecture.md`](../architecture.md) §10.4), **cuando** ocurre, **entonces** `snmp`
   publica `horus.snmp.router.state_changed` ([`../events.md`](../events.md)) con estado anterior,
   nuevo, razón y `time`; `devices` actualiza su proyección `status` ([`../api.md`](../api.md) §2.6)
   y el gateway reenvía el cambio a la UI por el topic `routers.status`.
3. **Dado** 1 000 routers simulados, **cuando** corre el sondeo, **entonces** cada ciclo termina en
   < 30 s con < 200 MB de RAM.
4. **Dado** que el servicio de sondeo se cae, **cuando** pasan 2 intervalos sin datos, **entonces**
   los routers no pasan a `offline` sino que su estado se marca **obsoleto** (dato viejo), y la UI
   lo indica ([`../frontend.md`](../frontend.md) §7.2).

### S03-10 · Entidad Cliente y asignación IP→cliente
- **Épica:** EP-06 · **Pts:** 5 · **Prio:** Must (riesgo crítico, [`../roadmap.md`](../roadmap.md) §4.5)
- **Servicio:** devices (tablas `customer`, `customer_service_link`, `customer_ip_assignment` de
  [`../database.md`](../database.md)).

**Como** analista **quiero** que Horus Flow sepa qué IP pertenece a qué cliente y desde cuándo **para**
que el tráfico pueda atribuirse a clientes a partir de S6.

1. **Dado** `subscribers.manage` ([`../security.md`](../security.md) §6.1), **cuando** creo un cliente en `POST /api/v1/customers` con código, referencia
   externa, nombre, plan, tipo declarado (`residential`/`business`/`unknown`) y sitio,
   **entonces** se crea.
2. **Dado** una asignación IP o prefijo → cliente con vigencia `[desde, hasta)` y `source = manual`,
   **cuando** se crea, **entonces** la base de datos impide que se solape con otra asignación de la
   misma IP en el mismo realm (restricción `EXCLUDE`) y la API responde 409.
3. **Dado** un CSV de clientes y asignaciones, **cuando** lo importo, **entonces** se aplica con el
   mismo flujo de validación que S03-07.
4. **Dado** una IP y un instante, **cuando** consulto la resolución IP→cliente (endpoint `POST`
   con la IP en el cuerpo, nunca en la query string, según [`../security.md`](../security.md);
   ruta final en [`../api.md`](../api.md)), **entonces** obtengo el cliente vigente o 404.

**Notas:** la fuente automática (RADIUS accounting o API del router) se decide en el spike de S4
y se implementa como módulo de `devices` en S5–S6 (P-04, Q5 de
[`../open-questions/architecture.md`](../open-questions/architecture.md)). Los datos de contacto
del cliente son PII: requieren `subscribers.read`; el detalle de tráfico por cliente requiere
`traffic.client.read`.

### S03-11 · Pantalla: lista de routers
- **Épica:** EP-05 · **Área:** frontend · **Pts:** 5

**Como** operador NOC **quiero** una lista de routers filtrable **para** encontrar cualquier equipo en
segundos.

1. **Dado** `devices.read`, **cuando** abro Inventario › Routers, **entonces** veo `UTable` con estado
   (icono + texto + color), nombre, IP de gestión, sitio, modelo, firmware, tags, "visto por última
   vez" relativo; columnas ordenables y redimensionables, cabecera fija.
2. **Dado** la `UDashboardToolbar`, **cuando** filtro por estado, sitio, grupo, vendor y tags
   (`USelectMenu` múltiple) o busco por texto, **entonces** la tabla se actualiza con debounce
   ≤ 250 ms y los filtros quedan en la URL.
3. **Dado** un cambio de alcanzabilidad por WebSocket, **cuando** el router está visible, **entonces**
   su celda de estado cambia sin recargar la tabla ni perder la selección/scroll, con un realce
   breve (o sin animación con `prefers-reduced-motion`).
4. **Dado** cero routers, **cuando** abro, **entonces** el `UEmpty` ofrece "Registrar router" e
   "Importar CSV" (si tengo `devices.create`); **dado** filtros sin resultados, ofrece "Limpiar
   filtros".
5. **Dado** < 768 px, **cuando** abro, **entonces** cada router se ve como fila compacta (estado,
   nombre, sitio) y el resto en el detalle.
6. **Dado** selección múltiple, **cuando** tengo `devices.update`, **entonces** puedo asignar tags o
   grupo en bloque.

### S03-12 · Pantalla: alta/edición de router y credenciales
- **Épica:** EP-05 · **Área:** frontend · **Pts:** 5

1. **Dado** `devices.create`, **cuando** pulso "Registrar router", **entonces** un `USlideover` desde
   la derecha muestra `UForm` con secciones Identificación, Ubicación (sitio con `USelectMenu`
   buscable), Equipo (vendor → modelo → firmware encadenados), Acceso (credencial existente o
   nueva) y Tags (`UInputTags`).
2. **Dado** un campo inválido (IP mal formada, nombre duplicado devuelto por la API), **cuando** salgo
   del campo o recibo 409, **entonces** el error aparece junto al campo y el foco va al primero.
3. **Dado** una credencial guardada, **cuando** edito el router, **entonces** el secreto aparece como
   "Configurada · cambiada hace 3 días por ana" con botón "Reemplazar", nunca el valor.
4. **Dado** cambios sin guardar, **cuando** intento cerrar el panel, **entonces** se pide confirmación
   "Descartar cambios".

### S03-13 · Pantalla: detalle de router
- **Épica:** EP-05 · **Área:** frontend · **Pts:** 3

1. **Dado** un router, **cuando** abro `/inventory/routers/{id}`, **entonces** veo cabecera con estado,
   nombre, IP, sitio (breadcrumb), acciones (Editar, Eliminar) y `UTabs`: Resumen (S3), Interfaces
   (S3 manual / S5 SNMP), Métricas (S5), WireGuard (S4), Tráfico (S6+), Alertas (S11), Actividad
   (auditoría del router). Las pestañas de sprints futuros no se muestran.
2. **Dado** la pestaña Resumen, **cuando** la veo, **entonces** muestra estado de alcanzabilidad con
   RTT y pérdida, "último sondeo hace 12 s", datos de inventario y credenciales configuradas.
3. **Dado** un router inexistente o fuera de mi ACL, **cuando** abro la URL, **entonces** veo 404.

### S03-14 · Spike: WireGuard desde contenedor
- **Épica:** EP-07 · **Área:** infra, security · **Pts:** 3 (time-box 2 días) · **Tipo:** spike

1. **Dado** el diseño control + `wireguard-agent` de
   [ADR-0014](../adr/0014-granularidad-de-microservicios-en-el-mvp.md) (agente con
   `network_mode: host` y `CAP_NET_ADMIN`), **cuando** termina el spike, **entonces** un prototipo
   del agente crea una interfaz y un peer con `wgctrl` desde el contenedor, sobrevive a un reinicio
   del host reaplicando su estado local, y el resultado (permisos mínimos, alternativa wireguard-go
   si el kernel no lo permite) queda anotado en el ADR o en uno nuevo.

### S03-15 · Pantalla: sitios y grupos
- **Épica:** EP-05 · **Área:** frontend · **Pts:** 3

1. **Dado** Inventario › Sitios, **cuando** la abro, **entonces** veo la jerarquía con `UTree` y, por
   sitio, número de routers por estado.
2. **Dado** Inventario › Grupos, **cuando** creo un grupo dinámico, **entonces** veo la vista previa
   de qué routers incluiría antes de guardar.

### S03-16 · Resumen con estado de routers en vivo
- **Épica:** EP-05, EP-T5 · **Área:** frontend · **Pts:** 3

**Como** operador NOC **quiero** ver de un vistazo cuántos routers están online, offline o sin datos
**para** saber si hay un incidente.

1. **Dado** el Resumen, **cuando** lo abro, **entonces** veo contadores por estado (online, warning,
   critical, offline, unknown), cada uno con icono, texto y número, que enlazan a la lista filtrada.
2. **Dado** un evento `status_changed`, **cuando** llega por WebSocket, **entonces** los
   contadores y la lista "Cambios recientes" se actualizan en < 2 s sin recargar; el lector de
   pantalla anuncia como máximo un resumen cada 30 s (región `aria-live="polite"`).
3. **Dado** el WebSocket desconectado, **cuando** pasan más de 60 s, **entonces** los contadores
   muestran "Datos de hace X min" y el indicador de conexión está en "Reconectando…"; la vista hace
   *polling* cada 30 s mientras tanto.
4. **Dado** que el servicio de sondeo no reporta, **cuando** el backend marca los datos como
   obsoletos, **entonces** se muestra un `UAlert` de advertencia "El monitoreo de alcanzabilidad no
   reporta desde 10:42. Los estados mostrados pueden no ser actuales."

### S03-17 · Pantalla: catálogo de vendors y modelos
- **Épica:** EP-05 · **Área:** frontend · **Pts:** 2 · **Prio:** Could

1. **Dado** `devices.update`, **cuando** abro Inventario › Catálogo, **entonces** gestiono vendors,
   modelos y firmware en tablas con edición en `UModal`.

### S03-18 · Recuperación de contraseña por email
- **Épica:** EP-03 · **Pts:** 3 · **Prio:** Could (solo si P-12 confirma SMTP)

1. **Dado** "¿Olvidaste tu contraseña?", **cuando** introduzco mi email, **entonces** la respuesta es
   idéntica exista o no la cuenta, y si existe recibo un enlace de un solo uso con caducidad de
   30 min.
2. **Dado** el restablecimiento, **cuando** se completa, **entonces** se revocan todas mis sesiones y
   se audita.

### S03-19 · Datos de prueba de inventario
- **Épica:** EP-T4 · **Pts:** 2

1. **Dado** `make seed`, **cuando** se ejecuta en desarrollo, **entonces** se crean 5 sitios, 50
   routers apuntando al simulador SNMP/ICMP de S01-20, credenciales de prueba y 3 usuarios con roles
   distintos.

## Riesgos del sprint

| Riesgo | Mitigación |
| --- | --- |
| ICMP desde contenedor requiere privilegios (`CAP_NET_RAW`) | Capability mínima documentada en `security.md`; alternativa ICMP sin privilegios (`net.ipv4.ping_group_range`) |
| Inventario real del ISP en formatos heterogéneos | Plantilla CSV acordada con el PO al inicio del sprint (P-06) |
| Entidad Cliente sin fuente clara | Historia Should; la fuente real se decide con P-04 antes de S6 |
