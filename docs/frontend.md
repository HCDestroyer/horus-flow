# Frontend — Arquitectura de información y UX

Aplicación web de Horus Flow en **Nuxt 4 + Vue 3 + TypeScript, Nuxt UI, Tailwind CSS y Apache
ECharts** ([ADR-0012](adr/0012-nuxt4-nuxt-ui.md)). El frontend habla **solo** con el rol `gateway`
del binario `horus` ([ADR-0025](adr/0025-binario-modular-con-roles.md)) por HTTPS y WebSocket.

**Ronda 2.** Este documento aplica las decisiones del PO ([`po-decisions.md`](po-decisions.md)):
la IP es el cliente (D1), propósito de seguridad frente a botnets (D5), **multi-tenant** (D6),
**dashboard modular para pantallas de monitoreo** (D8) y MikroTik primero (D10). Las secciones
nuevas son §3 (multi-tenant), §6 (dashboard modular), §7 (modo NOC/kiosco) y §8 (clientes por IP,
seguridad, tráfico, onboarding). La columna "Inc." indica en qué incremento de
[`roadmap.md`](roadmap.md) aparece cada cosa; las historias están en
[`backlog/increment-0.md`](backlog/increment-0.md) y [`backlog/increment-1.md`](backlog/increment-1.md).

Documentos relacionados: contrato REST/WS en [`api.md`](api.md), eventos en
[`events.md`](events.md), permisos y token de kiosco en [`security.md`](security.md), modos de fallo
en [`architecture.md`](architecture.md), integración MikroTik en
[`vendors/mikrotik.md`](vendors/mikrotik.md).

Las citas a las skills de diseño usan el formato `archivo › sección`:

- **apple-design** = `.claude/skills/apple-design/SKILL.md` (secciones §1–§17).
- **HIG** = `.claude/skills/apple-hig/references/hig/<página>.md › <encabezado>`. Se aplican los
  *principios y fundamentos* (accesibilidad, color, tipografía, layout, escritura, widgets, pantalla
  completa, arrastrar y soltar), no las convenciones propias de iOS/macOS, como indica
  `apple-hig/SKILL.md › Step 1 › Scope and limits` para apps web.

---

## 1. Tesis de diseño

> **Horus Flow es la consola de verdad de la red del ISP: qué clientes hacen qué, cuáles muestran
> señales de botnet, desde cuándo y con qué certeza.**

El elemento distintivo es la **semántica de estado, frescura y certeza**: cada dato muestra su
estado (icono + texto + color), cuán reciente es y, si es una inferencia (hallazgo, tipo
comercial), con qué confianza y por qué. Todo lo demás es sobrio. Evitamos el cliché de "NOC"
(fondo negro con verde ácido), que `apple-hig/SKILL.md › Lens 3 (craft)` identifica como plantilla:
el tema oscuro existe para pantallas murales y turnos de noche, no como identidad.

| Principio (apple-design §16; HIG `design-principles.md`) | Cómo se aplica |
| --- | --- |
| Propósito | Cada pantalla y cada widget responde una pregunta operativa ("¿qué IPs muestran señales de botnet?", "¿llegan flujos del nodo Norte?"). Si no la responde, no se construye |
| Agencia | Filtros en la URL, deshacer en acciones reversibles, confirmación solo en lo destructivo; el tipo manual de un cliente prevalece sobre el automático |
| Responsabilidad | Hallazgos con lenguaje "señales compatibles con…", nunca "infectado" sin confirmación humana; IPs de clientes fuera de las URLs; secretos mostrados una vez |
| Familiaridad | Patrones estándar de Nuxt UI; lo que se ve igual se comporta igual |
| Flexibilidad | Escritorio de NOC, portátil, móvil para consultar y **pantalla mural sin interacción** |
| Simplicidad | Camino común primero; plantillas de dashboard listas antes que editor libre |
| Oficio | Cifras tabulares, unidades consistentes, tiempos relativos con absolutos en tooltip |
| Deleite | Calma: nada parpadea; los cambios en vivo se notan sin alarmar |

## 2. Usuarios y contextos

| Persona | Ámbito | Contexto | Necesita sobre todo |
| --- | --- | --- | --- |
| Superadministrador de plataforma | Todos los ISP | Escritorio | Alta de ISP, vista global, estado del sistema |
| Administrador del ISP | Su ISP | Escritorio | Nodos, routers, onboarding MikroTik, usuarios, pantallas NOC |
| Operador NOC | Su ISP | Escritorio + pantalla mural, 24/7 | Dashboards en vivo, exportadores, clientes, hallazgos nuevos |
| Analista de seguridad | Su ISP | Escritorio | Hallazgos de botnet, evidencia, decisión (confirmar / falso positivo) |
| Ingeniero de red | Su ISP | Escritorio | Tráfico por servicio, categoría, ASN; consumo por cliente |
| Gerente del ISP | Su ISP | Portátil/móvil, ocasional | Dashboard resumen, reportes (I3) |
| **Pantalla NOC** (kiosco) | Un ISP (o varios, si la crea el superadmin) | TV de 43–75", a 2–6 m, sin teclado | Dashboards legibles a distancia, siempre frescos |

## 3. Multi-tenant en la UI

Tenant = **ISP** (D6, [ADR-0017](adr/0017-multi-tenant-desde-v1.md)). Un usuario puede pertenecer a
uno o varios ISP con un rol en cada uno; el superadmin ve todos.

### 3.1 El ISP actual siempre visible

- El ISP forma parte de la URL: `/t/<slug>/…`. Un enlace compartido abre el mismo ISP; si quien lo
  abre no tiene acceso, ve **"No encontrado"** (nunca "sin permiso", para no revelar que existe).
- La cabecera de la barra lateral muestra el **nombre del ISP** con un monograma. Es el primer
  elemento de la jerarquía y responde "¿dónde estoy?" (apple-design §16 › Wayfinding).
- El título de la pestaña incluye el ISP: "Hallazgos · Fibra Norte · Horus Flow".

### 3.2 Selector de ISP (I0)

- Solo aparece si el usuario tiene **más de un ISP**. Con uno, el nombre es texto plano.
- Patrón de *workspace switcher*: `UDropdownMenu` desde la cabecera de la barra lateral; con más
  de 7 ISP incluye búsqueda. También en la búsqueda global (⌘K): "Cambiar a ISP…".
- Al cambiar: se conserva la sección si existe en el otro ISP (de "Hallazgos" de A a "Hallazgos" de
  B); los filtros que no aplican se descartan; se **vacían cachés y suscripciones WebSocket** del ISP
  anterior (test de aislamiento en el cliente); se recuerda como último ISP usado.
- Al iniciar sesión se abre el último ISP usado; si no hay, el primero alfabéticamente; el
  superadmin entra a la vista global.

### 3.3 Vista global del superadministrador

| Inc. | Qué ve |
| --- | --- |
| I0–I1 | `/platform/isps`: lista de ISP con nodos, routers *Exportando*/*Silenciosos*, hallazgos abiertos críticos y última actividad; clic → entra al ISP |
| I2 | `/global`: **dashboard de plataforma** con el widget `platform_summary` (salud agregada por ISP); también puede mostrarse en un **kiosco de plataforma**, que solo enseña salud agregada, nunca datos de clientes ([`api.md`](api.md) §2.12; Q18 de [`open-questions/architecture.md`](open-questions/architecture.md)) |

- Cuando el superadmin entra en un ISP, la barra de navegación muestra un distintivo discreto
  "Superadmin" y sus lecturas de detalle de cliente quedan auditadas igual que las de cualquiera.
- Ningún widget de ámbito ISP agrega datos de varios ISP; los de plataforma etiquetan cada valor con su ISP.

### 3.4 Consola de plataforma (superadmin)

| Página | Contenido | Inc. |
| --- | --- | --- |
| ISP | Alta, suspensión, administradores iniciales, límites | I0 |
| Usuarios de plataforma | Superadmins y operadores de plataforma | I0 |
| WireGuard | **Hubs y rangos de túneles** (IPAM de plataforma: rango, ocupación, endpoint público), peers por ISP; en I1 lectura, en I3 gestión (rotación, segundo hub) | I1 · I3 |
| Almacenamiento | Uso de disco local por tipo de dato y retención efectiva; **aviso permanente "Sin copia remota configurada"** (banner `warning` en esta página y tarjeta en Estado del sistema) mientras no haya destino remoto ([ADR-0019](adr/0019-almacenamiento-local-y-destino-remoto.md)) | I1 |
| Destinos remotos | Alta de destino (SFTP primero; Google Drive, MEGA, Dropbox con rclone; **MediaFire no se ofrece**: rclone no lo soporta), prueba de conexión, programación y última copia verificada. Al activar el cifrado, se muestra la **clave de recuperación** una sola vez y el formulario no se puede guardar hasta marcar "He guardado la clave de recuperación fuera de Horus" (sin ella las copias son irrecuperables) | I3 |
| Estado del sistema | Salud por rol del binario `horus`, dependencias, colas, versión | I1 |

## 4. Mapa de navegación

Barra lateral con **grupos y como máximo dos niveles** (`HIG sidebars.md › Best practices`: *"show
no more than two levels of hierarchy"*), etiquetas que nombran el contenido. **Aparición
progresiva:** una sección entra cuando funciona; no hay ítems "próximamente" (apple-design §16 ›
Purpose).

```
[◉ Fibra Norte ▾]                                  selector de ISP (§3.2)
Dashboards                    /t/:slug/dashboards[/:id]                         I1
OPERACIÓN
├─ Nodos y routers            /t/:slug/nodes[/:nodeId]
│                              /t/:slug/routers/:routerId[/:tab]                 I0 · I1
├─ Clientes                   /t/:slug/clients[/:clientId]                      I1
└─ Tráfico                    /t/:slug/traffic                                  I1
SEGURIDAD
├─ Hallazgos                  /t/:slug/security/findings[/:findingId]           I1
└─ Investigar IP              /t/:slug/security/investigate (IP en el cuerpo)   I2
GESTIÓN
├─ Alertas                    /t/:slug/alerts                                   I3
└─ Reportes                   /t/:slug/reports                                  I3
ADMINISTRACIÓN DEL ISP
├─ Usuarios y roles           /t/:slug/admin/users                              I0 · I2
├─ Pantallas NOC (kioscos)    /t/:slug/admin/kiosks                             I1
├─ Detección                  /t/:slug/admin/detection (umbrales)               I1 lectura · I2
└─ Auditoría                  /t/:slug/admin/audit                              I2
PLATAFORMA (solo superadmin, §3.4)
├─ Vista global               /global                                            I2
├─ ISP                        /platform/isps                                     I0
├─ Usuarios de plataforma     /platform/users                                    I0
├─ WireGuard (hubs y rangos)  /platform/wireguard                                I1 lectura · I3
├─ Almacenamiento             /platform/storage (+ destinos remotos en I3)       I1 · I3
└─ Estado del sistema         /platform/system                                   I1

Fuera de la barra: /login · /account · /kiosk (modo NOC, §7)
```

| Sección | Inc. | Permiso de lectura (nombres finales en `security.md`) | Tiempo real |
| --- | --- | --- | --- |
| Dashboards | I1 | autenticado; cada widget exige el suyo | ✔ |
| Nodos y routers | I0 (alta) · I1 (onboarding, exportador) · I2 (SNMP) | `devices.read` | estado del exportador ✔ |
| Clientes | I1 | `customers.read` (detalle auditado) | contador de nuevos ✔ |
| Tráfico | I1 | `traffic.read` | resumen ✔ |
| Hallazgos | I1 | `security.findings.read` (gestionar: `security.findings.manage`; evidencia: `security.evidence.read`) | ✔ |
| Pantallas NOC | I1 | `kiosks.manage` | estado de cada pantalla ✔ |
| Vista global / Plataforma | I0–I2 | rol `superadmin` | ✔ |

**Navegación transversal:** búsqueda global ⌘K (`UDashboardSearch`): nodos, routers, hallazgos,
comandos y cambio de ISP; la búsqueda de una IP de cliente abre Clientes con la IP en el estado de
la página, nunca en la URL. Breadcrumbs en detalles ("Fibra Norte › Nodo Centro › rt-centro").
Toda entidad mencionada enlaza a su detalle. Filtros, orden, rango y pestaña en la query string,
**salvo IPs de clientes** ([`security.md`](security.md), minimización): las páginas usan el ID opaco
del cliente.

## 5. Layout

### 5.1 Estructura

```
┌───────────────┬──────────────────────────────────────────────────────────────┐
│ ◉ Fibra Norte▾│ Hallazgos                    [● En vivo · hace 3 s] [🔔 2] [?] │ ← UDashboardNavbar
│ [⌘K Buscar]   ├──────────────────────────────────────────────────────────────┤
│ Dashboards    │ [Estado ▾] [Severidad ▾] [Nodo ▾] [Detector ▾]     [Exportar]  │ ← UDashboardToolbar
│ OPERACIÓN     ├──────────────────────────────────────────────────────────────┤
│  Nodos…       │  contenido (UDashboardPanel, scroll propio)                    │
│  Clientes     │                                                               │
│  Tráfico      │                                                               │
│ SEGURIDAD     │                                                               │
│  Hallazgos  4 │                                                               │
│───────────────│                                                               │
│ (A) Ana Ruiz  │                                                               │
└───────────────┴──────────────────────────────────────────────────────────────┘
```

- **Barra lateral** (`UDashboardSidebar` + `UNavigationMenu`): selector de ISP arriba, grupos con
  encabezado, colapsable a iconos y ocultable (`HIG sidebars.md`); iconos Lucide sin color salvo
  estado; contador de hallazgos nuevos en "Hallazgos".
- **Navbar**: título de la sección (nunca el nombre de la app, `HIG toolbars.md › Titles`),
  indicador de conexión (§10.3), centro de notificaciones, ayuda.
- **Toolbar**: filtros a la izquierda, **una sola acción primaria** a la derecha; el resto en "Más".
- **Banner global** (`UBanner`) solo para degradaciones que afectan a toda la app (§9.4).

### 5.2 Responsive

Layout guiado por espacio disponible (`HIG layout.md › Adaptability`).

| Ancho | Barra lateral | Tablas | Dashboards (grilla de 12 columnas) |
| --- | --- | --- | --- |
| ≥ 1440 px | Expandida | Todas las columnas | Layout tal como se diseñó |
| 1024–1439 px | Expandida o iconos | Columnas secundarias ocultables | Widgets anchos se mantienen; los de 3 columnas pasan a 4 |
| 768–1023 px | Oculta, botón de menú | Columnas prioritarias + detalle en slideover | Widgets a 6 o 12 columnas |
| < 768 px | Panel deslizable | Filas tipo lista | Una columna, en el orden de lectura del layout (izquierda→derecha, arriba→abajo) |

Mínimo 320 px sin scroll horizontal de página (WCAG 1.4.10). El modo kiosco tiene su propio
layout (§7).

### 5.3 Tema claro / oscuro

- Por defecto **sigue al sistema**. Desviación justificada de `HIG dark-mode.md › Best practices`
  (*"Avoid offering an app-specific appearance setting"*): en salas NOC con equipos compartidos el
  operador necesita forzar oscuro; el menú de usuario ofrece Sistema / Claro / Oscuro. **El modo
  kiosco fuerza oscuro** (§7.8).
- Ambos temas se diseñan y prueban por igual; fondos claros suavizados (`HIG dark-mode.md › Dark
  Mode colors`). ECharts usa un tema generado de los mismos tokens y se re-tematiza sin recargar datos.

## 6. Dashboard modular (D8)

### 6.1 Conceptos

| Concepto | Qué es |
| --- | --- |
| **Widget** | Unidad autocontenida que responde **una** pregunta (`HIG widgets.md › Best practices`: *"Choose simple ideas that relate to your app's main purpose"*). Tiene tipo, versión, parámetros, tamaños permitidos y su propia fuente de datos |
| **Catálogo** | Registro de tipos de widget disponibles en la build, filtrado por permisos y ámbito |
| **Dashboard** | Layout en una grilla + variables comunes (nodo, rango de tiempo) + lista de widgets con su posición y parámetros |
| **Visibilidad** | Plantilla de Horus (semilla, solo lectura, se duplica) · `private` (personal) · `tenant` (compartido en el ISP) · plataforma (superadmin, solo salud agregada) — ver [`api.md`](api.md) §2.11 |
| **Plantilla** | Dashboard de ámbito `system` que se usa tal cual o se duplica para editar |
| **Lista de reproducción** (*playlist*) | Secuencia de dashboards con duración que muestra un kiosco (§7.5; `GET /playlists`) |

### 6.2 Catálogo de widgets

Cada widget funciona en escala normal y **mural** (§7.4) y no depende de hover para su información
principal (`HIG charts.md › Best practices`: *"don't require interaction to reveal critical
information"*). Los nombres de tipo siguen la convención de [`api.md`](api.md) §2.11
(`snake_case`); el catálogo autoritativo lo sirve `GET /widget-types`. **Kiosco**: ✔ = `kiosk_allowed`;
**DP** = `contains_personal_data` (IPs de clientes: en kiosco solo si el ISP lo permite, §7.4).

| Tipo | Pregunta que responde | Tamaños (col × filas) | Fuente | Kiosco | Inc. |
| --- | --- | --- | --- | --- | --- |
| `noc_header` | ¿Qué hora es, qué ISP es y cuán frescos son estos datos? | 12×1 | local + estado WS | ✔ | I1 |
| `traffic_now` | ¿Cuánto tráfico pasa ahora? (↓/↑, variación vs. ayer, sparkline 1 h) | 3×2, 4×2 | WS resumen de tráfico | ✔ | I1 |
| `customers_active` | ¿Cuántos clientes hay activos y cuántos nuevos hoy? | 3×2 | `GET /customers/stats` | ✔ | I1 |
| `exporters_status` | ¿Llegan flujos de todos los routers? (estado, último flujo, flujos/s, pérdida, cobertura) | 4×3, 6×3, 12×2 | WS exportadores | ✔ | I1 |
| `traffic_timeseries` | ¿Cómo evoluciona el tráfico? (↓/↑, 6 h/24 h) | 6×3, 8×3, 12×3 | agregados | ✔ | I1 |
| `top_customers` | ¿Quién consume más? (IP o alias, tipo, ↓/↑) | 4×4, 6×4 | agregados | ✔ · DP | I1 |
| `top_services` / `top_categories` / `top_organizations` | ¿Qué se consume? | 4×4, 6×4 | agregados | ✔ | I1 |
| `findings_summary` | ¿Cuántos hallazgos abiertos hay y de qué severidad? | 3×2, 4×2 | `GET /security/summary` + WS | ✔ | I1 |
| `botnet_signals` | ¿Qué señales de botnet vemos ahora? Clientes afectados por señal: contacto con C2 (reputación), beaconing, fan-out, escaneo, puertos vigilados, salida sostenida, SMTP, DDoS ([`traffic-model.md`](traffic-model.md) §8) | 4×3, 6×3 | `GET /security/summary` + WS | ✔ | I1 |
| `findings_feed` | ¿Qué hallazgos nuevos o activos hay? (severidad, resumen, cliente, hace cuánto) | 6×4, 12×4 | WS + `GET /findings` | ✔ · DP | I1 |
| `findings_trend` | ¿Aumentan los hallazgos? (por día y tipo, 7/30 días) | 6×3 | REST | ✔ | I1 |
| `security_by_node` | ¿Qué nodos tienen más clientes con señales? | 4×4 | `GET /security/summary` | ✔ | I1 |
| `watched_ports` | ¿A qué puertos vigilados (23, 2323, 445, 7547, 8291…) sale tráfico y desde cuántos clientes? | 4×3 | agregados de seguridad | ✔ | I1 |
| `routers_status_grid` | ¿Están vivos mis routers? (online/degradado/offline con razón, CPU) | 4×3, 12×2 | WS SNMP | ✔ | I2 |
| `commercial_candidates` | ¿Qué IPs residenciales parecen comerciales? (confianza, razón principal) | 6×4 | REST | — · DP | I2 |
| `activity_heatmap` | ¿Cuándo hay más uso? (hora × día) | 6×3 | agregados | ✔ | I2 |
| `platform_summary` | ¿Cómo están todos los ISP? (exportadores, tráfico, hallazgos críticos, sin datos de clientes) | 12×4 | plataforma | ✔ (kiosco de plataforma) | I2 |
| `note` | Aviso del turno (texto fijo del ISP) | 3×1 … 12×1 | layout | ✔ | I2 |
| `alerts_active` | ¿Qué alertas están activas? | 6×4 | WS alertas | ✔ | I3 |

Reglas de contenido: tamaños pequeños muestran **un** dato; los grandes añaden contexto sin perder
el propósito (`HIG widgets.md`: *"Avoid expanding a smaller widget's content to simply fill a larger
area"*). Top N ≤ 10 + "Otros". Huecos de datos como hueco, nunca cero. Color estable por servicio y
categoría en todos los widgets. Los widgets de seguridad hablan de **señales** y **clientes
afectados**, nunca de "infectados".

### 6.3 Contrato de widget (C9)

El contrato tiene dos mitades:

- **Servidor** (autoritativo, [`api.md`](api.md) §2.11): `GET /widget-types` devuelve por tipo
  `type`, `title`, `config_schema` (JSON Schema de la configuración), `required_permission`,
  `data_endpoint_kind` (`state`/`series`/`table`), `realtime_topic`, `contains_personal_data` y
  `kiosk_allowed`. Los datos de cada widget los **resuelve el servidor** a partir de su configuración
  guardada (`GET /dashboards/{id}/widgets/{wid}/data`), con los permisos de quien mira (usuario o
  kiosco), y se cachean en Valkey para que diez pantallas no hagan diez consultas.
- **Frontend** (presentación, `apps/frontend/app/widgets/<type>/manifest.ts`, validado en build
  contra el catálogo del servidor):

| Campo | Contenido |
| --- | --- |
| `type` | Debe existir en `GET /widget-types`; un tipo sin renderizador se muestra como "Widget no disponible en esta versión" |
| `sizes` | Tamaños permitidos en unidades de grilla, por defecto y mínimo (normal y mural) |
| `description` | Clave i18n que empieza por verbo ("Muestra los clientes que más consumen…", `HIG widgets.md › Previews and placeholders`) |
| `category` | Tráfico, Clientes, Seguridad, Infraestructura, Plataforma |
| `configForm` | Formulario generado de `config_schema` con overrides de presentación (I2) |
| `placeholder` | Forma del esqueleto de carga (`HIG widgets.md`: placeholders que se reconocen) |
| `wall` | Variante mural: qué se oculta y qué se agranda (§7.4) |

Si el espectador no tiene el permiso del tipo, el servidor responde `403 WIDGET_TYPE_NOT_ALLOWED` y
la tarjeta muestra "No tienes acceso a este widget" sin datos; el resto del dashboard sigue.

### 6.4 Layout, grilla y guardado

- **Grilla de 12 columnas** con altura de fila fija (`row_height_px`); en mural, 12 columnas a
  pantalla completa (§7.4). El dashboard es **un documento** ([`api.md`](api.md) §2.11): `id`,
  `tenant_id`, `version` (ETag / `If-Match`), `name`, `visibility`, `owner_id`, `layout` (grilla,
  altura de fila, breakpoints), `default_range`, `refresh_seconds` y `widgets[]` (`id`, `type`,
  `title`, `position {x, y, w, h}`, `config`, `refresh_seconds`).
- **Variables del dashboard** (nodo, rango de tiempo) en la toolbar, compartidas por los widgets
  que las aceptan; un widget puede fijar su propio valor en `config`.
- **Guardado y visibilidad** (servidor, módulo `analytics`):

| Visibilidad | Quién lo crea | Quién lo ve | Quién lo edita | Inc. |
| --- | --- | --- | --- | --- |
| Plantilla de Horus | Semilla versionada | Todos | Nadie (se duplica con `POST /dashboards/{id}/duplicate`) | I1 |
| `tenant` | `dashboards.manage` del ISP | Todo el ISP | `dashboards.manage` | I2 (I1: solo plantillas) |
| `private` | Cualquier usuario con `dashboards.read` | Su autor | Su autor | I2 |
| Plataforma | Superadmin | Superadmins y kiosco de plataforma | Superadmin | I2 |

- Un dashboard compartido **no amplía permisos**: cada widget se resuelve con los permisos de quien mira.
- Cada usuario elige su **dashboard de inicio por ISP** (I2); por defecto, "NOC del ISP".
- Un cambio concurrente devuelve `412`/`409`; la UI ofrece "Ver cambios y combinar" o "Guardar como copia".

### 6.5 Plantillas

| Plantilla | Para | Widgets (orden de lectura) | Inc. |
| --- | --- | --- | --- |
| **NOC del ISP** | Operador NOC y pantalla mural | `noc_header` · `traffic_now` · `customers_active` · `findings_summary` · `exporters_status` · `traffic_timeseries` (24 h) · `top_categories` · `top_customers` · `findings_feed` | I1 |
| **Seguridad** | Analista y pantalla mural | `noc_header` · `findings_summary` · `botnet_signals` · `findings_feed` · `findings_trend` · `security_by_node` · `watched_ports` | I1 |
| **Nodo** | Ingeniero de red (variable `nodo`) | `exporters_status` · `traffic_timeseries` · `top_services` · `top_organizations` · `top_customers` | I2 |
| **Perfil de clientes** | Gestión del ISP | `commercial_candidates` · `activity_heatmap` · `top_customers` | I2 |
| **Plataforma** | Superadmin y kiosco de plataforma | `noc_header` · `platform_summary` | I2 |

Las plantillas se diseñan para que en 1920×1080 en mural quepan sin scroll (máx. ~9 widgets). En un
kiosco sin permiso de datos personales, `top_customers` y `findings_feed` muestran alias o la IP
enmascarada (§7.4).

### 6.6 Editor de dashboards (I2)

- **Modo edición explícito** ("Editar dashboard"): solo entonces los widgets muestran asas de
  movimiento y redimensión; fuera de él la grilla es estática (evita movimientos accidentales).
- **Arrastrar y redimensionar** sobre la grilla con encaje a celdas, previsualización del destino
  y empuje de los demás widgets. Librería candidata: `grid-layout-plus` (Vue 3); alternativa
  GridStack. Se decide en la historia de I2 con una prueba de accesibilidad.
- Feedback continuo (`HIG drag-and-drop.md › Providing feedback`): imagen de arrastre translúcida
  tras ~3 px de movimiento, destino resaltado solo mientras es válido, vuelta animada al origen si
  se suelta en un sitio inválido. Movimiento con resortes sin rebote, interrumpible (apple-design §3–§4).
- **Alternativa sin arrastre** (`HIG drag-and-drop.md › Best practices`: *"Offer alternative ways
  to accomplish drag-and-drop actions"*): con un widget seleccionado, flechas mueven una celda,
  Mayús + flechas redimensionan; menú del widget con "Mover a…", "Tamaño" (tamaños permitidos),
  "Duplicar", "Quitar". Cada cambio se anuncia en `aria-live` ("Top clientes movido a fila 2,
  columna 7").
- **Deshacer** (⌘Z / Ctrl+Z) de cualquier cambio de layout (*"Prefer letting people undo a
  drag-and-drop operation"*); "Descartar cambios" y "Guardar"; borrador local hasta guardar.
- **Añadir widget**: panel lateral con el catálogo agrupado por categoría, vista previa con datos
  reales del ISP (o simulados si tardan, `HIG widgets.md › Previews and placeholders`) y descripción.
- Plantillas `system`: botón "Duplicar para editar".

## 7. Modo NOC / kiosco (D8)

Pantallas murales que muestran dashboards **indefinidamente, sin interacción**, legibles a
distancia y que se recuperan solas. Ruta `/kiosk`, layout propio sin barra lateral ni toolbar.

### 7.1 Requisitos

| Requisito | Decisión |
| --- | --- |
| Sin sesión de usuario en la TV | El kiosco es un **dispositivo registrado**, no un usuario ([`api.md`](api.md) §2.12, [`security.md`](security.md)): credencial de dispositivo en cookie `HttpOnly` rotativa, JWT de kiosco de 10 min sin permisos de escritura, limitado a su ISP, a sus dashboards y a widgets `kiosk_allowed`, opcionalmente a la red del NOC (`allowed_cidrs`), con caducidad (180 días por defecto) y revocación inmediata. Un kiosco de plataforma solo muestra salud agregada |
| Pantalla completa | §7.3 |
| Rotación | §7.5 |
| Autorrefresco | §7.6 |
| Legibilidad a distancia | §7.4 |
| Tema oscuro | §7.8 |
| Sin interacción | §7.3 |
| Recuperación automática | §7.7 |

### 7.2 Alta de un kiosco (enrolamiento)

Flujo de [`api.md`](api.md) §2.12, visto desde la UI:

1. En **Pantallas NOC** (`kiosks.manage`), el administrador del ISP pulsa "Nuevo kiosco": nombre
   ("TV sala NOC"), lista de reproducción o dashboards, duración de cada uno, red permitida
   (recomendada: la del NOC), **mostrar datos de clientes** (desactivado por defecto: una pantalla
   la ven visitas y cámaras), caducidad e **interrupción por hallazgo crítico** (§7.5).
2. Pulsa "Generar código": se muestra un **código de 8 caracteres y un QR**, de un solo uso y
   válidos 10 min, con cuenta atrás. El QR lleva el código en el fragmento de la URL (no viaja al
   servidor ni a logs).
3. En la TV se abre `https://<horus>/kiosk`: pantalla de bienvenida legible a distancia con un
   campo grande para el código (o escaneo del QR desde un móvil que abre la URL en la propia TV
   si el navegador lo permite). Código erróneo → mensaje claro y nuevo intento; tras varios fallos el
   código se invalida y hay que generar otro.
4. Canjeado el código, la TV recibe su credencial (cookie `HttpOnly`, nunca `localStorage`), pide
   su configuración y empieza a reproducir.
5. Pantallas NOC lista cada kiosco con estado (*En línea · última señal hace 12 s*, *Desconectado
   desde 03:12*), última IP, versión del frontend y acciones Editar / Revocar. Revocar cierra su
   WebSocket y devuelve la TV a la pantalla de código en < 1 min.

### 7.3 Pantalla completa y sin interacción

- El layout ocupa siempre el viewport (`100dvh`, sin scroll). La Fullscreen API requiere un gesto:
  la guía de instalación recomienda abrir el navegador en modo kiosco (p. ej. Chromium
  `--kiosk`); si no está a pantalla completa, aparece **una vez** "Pulsa para pantalla completa" y
  desaparece tras el gesto (`HIG going-full-screen.md › Best practices`: priorizar el contenido
  ocultando controles; *"Let people choose when to exit full-screen mode"*: Esc sigue funcionando).
- **Nada requiere hover ni clic**: sin tooltips, sin enlaces, sin menús. El cursor se oculta tras 3 s
  sin movimiento.
- Se pide `navigator.wakeLock.request('screen')` y se renueva al volver a ser visible, para que la
  pantalla no entre en reposo.
- Una persona delante de la TV puede usar **Espacio** para pausar/reanudar la rotación y **←/→**
  para cambiar de dashboard (WCAG 2.2.2, pausar contenido en movimiento); no hay otra interacción.

### 7.4 Legibilidad a distancia

Referencia: TV de 55" 1920×1080 vista a 3–5 m (≈ 0,63 mm por píxel). Regla práctica de partida, a
validar con la TV real de la persona en I1-27: **valores clave legibles a 5 m y etiquetas a 3 m**.

| Elemento | Tamaño mínimo a 1080p | Notas |
| --- | --- | --- |
| Valor principal de KPI | 72 px, peso semibold, cifras tabulares | Unidades a 50 % del tamaño, mismo color |
| Título de widget | 28 px | Sentence case, sin truncar |
| Etiquetas, ejes, filas de tabla | 22 px | Nada por debajo de 20 px |
| Indicador de frescura | 20 px | Siempre visible en `noc_header` y en cada widget atrasado |

- Escala con el viewport: `--kiosk-scale` derivado de la altura (`clamp`), de modo que en 4K o en
  una TV de 75" las proporciones se mantienen (`HIG typography.md › Best practices`: *"Use font
  sizes that most people can read easily … at various viewing distances"*).
- Contraste **≥ 7:1** en valores clave y ≥ 4,5:1 en el resto; estados con icono + texto + color.
- Gráficos murales: líneas de 3 px, ≤ 5 marcas por eje con valores familiares (`HIG charts.md`:
  *"Prefer familiar sequences of values"*), **etiquetas directas** en lugar de leyendas, sin tooltips,
  ≤ 5 series.
- Tablas murales: ≤ 8 filas, una sola línea por fila, columnas imprescindibles.
- Densidad: ≤ 9 widgets por dashboard; mejor rotar dos dashboards claros que uno saturado
  (`HIG widgets.md › Best practices`: *"Balance information density"*).
- IPs de clientes en monoespaciada. Si el kiosco **no** tiene permitido mostrar datos de clientes
  (por defecto), los widgets con datos personales muestran el alias si existe o la IP enmascarada
  (`10.20.0.•••`) de forma consistente, y nunca enlazan a fichas.

### 7.5 Rotación

- Lista de reproducción: dashboards con duración (por defecto 30 s, mínimo 10 s). Si hay uno solo,
  no rota.
- Antes de cada cambio se **precargan** los datos del siguiente: nunca se muestra un esqueleto en
  la TV salvo en el primer arranque.
- Indicador discreto de posición ("2/3") y nombre del dashboard en `noc_header`. Con movimiento
  reducido, cambio instantáneo; si no, fundido corto (≤ 300 ms) (apple-design §14).
- **Interrupción por hallazgo crítico** (opcional por pantalla): banda superior fija durante 60 s
  con "Hallazgo crítico · Escaneo del puerto 23 desde 10.20.0.••• · nodo Centro · hace 20 s"
  (IP completa solo si el kiosco tiene permitido mostrar datos de clientes); sin
  parpadeo (WCAG 2.3.1), con un realce que se desvanece. No cambia el dashboard en pantalla.

### 7.6 Autorrefresco y frescura

- Datos en vivo por WebSocket (temas de los widgets de la lista) y *polling* REST de respaldo
  (30 s) mientras el WebSocket no está disponible.
- Cada widget muestra su frescura cuando está **atrasado**; `noc_header` muestra siempre
  "Actualizado hace N s" (`HIG widgets.md › Updating widget content`: *"consider displaying text
  that describes when the data was last updated"*).
- Umbrales: atrasado si edad > 2× intervalo esperado; obsoleto si > 5× (valor atenuado + "Dato de
  hace 6 min"). **Nunca** se muestra un valor obsoleto como si fuera actual.

### 7.7 Recuperación automática

| Situación | Comportamiento |
| --- | --- |
| Corte de red / WebSocket caído | Mantiene los últimos datos; reconexión con backoff 1 → 30 s y jitter; tras 60 s, banda ámbar a todo el ancho "Sin conexión desde 03:12 · mostrando los últimos datos"; al volver, snapshot REST + reanudación y la banda desaparece |
| Servidor reiniciado | Igual que arriba; al responder `/healthz`, re-suscripción y snapshot |
| Nueva versión del frontend | Consulta periódica de versión; recarga **en el siguiente cambio de dashboard** y solo si el servidor responde (nunca deja una pantalla en blanco) |
| Credencial revocada o caducada (`401`, cierre WS `4409`) | Vuelve a la pantalla de código (§7.2) |
| Error de un widget | Solo ese widget muestra "Sin datos · reintentando"; los demás siguen |
| Fugas o bloqueo de la página | Recarga programada diaria a la hora configurada (por defecto 04:00, zona del ISP) y *watchdog*: si el hilo principal deja de pintar o hay errores no capturados repetidos, recarga cuando el servidor responde |
| La TV se apaga y enciende | El navegador en modo kiosco reabre `/kiosk`; la cookie de dispositivo la identifica |
| Petición desde fuera de la red permitida | `403`; la TV muestra "Esta pantalla no está en la red autorizada" |

La pantalla renueva su token y mantiene el WebSocket; Pantallas NOC muestra `last_seen_at` y la última IP.

### 7.8 Tema oscuro y cuidado de la pantalla

- Siempre oscuro, sin negro puro (superficie base muy oscura y elevaciones ligeramente más claras)
  para evitar arrastre en paneles OLED/VA; sin grandes áreas blancas.
- Colores de estado ajustados para fondo oscuro con el contraste de §7.4.
- **Protección contra quemado** (OLED/plasma): la rotación ya cambia el contenido; además, el lienzo
  se desplaza 1–2 px cada 10 min y los elementos estáticos (`noc_header`) usan baja luminancia.

### 7.9 Pruebas del modo kiosco

Playwright con reloj acelerado simula 24 h (rotación, recargas diarias, cortes de red, reinicio de
servidor, nueva versión, revocación) y comprueba memoria, errores y que la frescura nunca miente;
capturas a 1920×1080 y 3840×2160; medición automática de tamaños de fuente mínimos (I1-21).

## 8. Vistas de dominio

### 8.1 Clientes por IP (I1; tipo comercial automático en I2)

Un cliente es una IP de un realm de un nodo, descubierta de los flujos (D1,
[ADR-0018](adr/0018-la-ip-es-el-cliente.md)).

**Lista** (`UTable`, paginación por cursor):

| Columna | Contenido |
| --- | --- |
| Cliente | IP en monoespaciada + alias si existe |
| Nodo | Enlace al nodo |
| Tipo | `Residencial` / `Comercial` + origen (`kind_source`): *por defecto*, *manual* (con candado: `kind_locked`), *detectado (confianza 85)* (I2); "indicios comerciales" si `commercial_use_suspected` (I2) |
| Tráfico 24 h | ↓ / ↑ en Mbps medios o GB, alineado a la derecha |
| Principal | Servicio o categoría con más tráfico |
| Seguridad | Insignia con nº de hallazgos abiertos y severidad máxima |
| Estado | *Activo* · *Inactivo* (sin tráfico ≥ 30 días, configurable por ISP) |
| Visto | "hace 2 min"; tooltip con primera vez visto |

Filtros: nodo, tipo, origen del tipo, estado (activo/inactivo), con hallazgos, prefijo. Búsqueda por IP o alias (la IP
viaja en el cuerpo de la petición, nunca en la URL).

**Detalle** (`/t/:slug/clients/:clientId`):

```
Fibra Norte › Nodo Centro › 10.20.0.41  "Panadería Sol"
┌─────────────────────────────────────────────────────────────────────┐
│ 10.20.0.41 · Nodo Centro · Visto hace 1 min · Primera vez 03/10      │
│ [Comercial · manual]  Marcado por Ana R. el 07/10: "Local con TPV"   │
│                                   [Cambiar tipo] [Alias] [⋯]          │
├─────────────────────────────────────────────────────────────────────┤
│ Resumen | Tráfico | Seguridad (2) | Historial                        │
└─────────────────────────────────────────────────────────────────────┘
```

- **Ciclo de vida** ([`database.md`](database.md) §2.3.4): *Activo* → *Inactivo* tras 30 días sin
  tráfico (vuelve a *Activo* si reaparece) → **purga** a los 25 meses sin tráfico (si la IP vuelve,
  es un cliente nuevo sin historia). La ficha lo explica en lenguaje claro ("Inactivo desde el
  02/09: no vemos tráfico de esta IP desde hace 35 días").
- **Reiniciar cliente** (en "⋯", con confirmación porque borra alias y notas): para cuando la IP
  pasó a otra persona. El tipo vuelve al defecto, el historial registra el reinicio y los
  dashboards muestran datos desde ese momento.
- **Tipo y razones.** El tipo nunca aparece sin su origen. En I2, si es *detectado*, la ficha
  muestra **razones** con dato y comparación ("Conexiones entrantes de 240 IPs distintas al puerto
  443 en 24 h", "Subida 5,2× la media residencial del nodo", "Actividad constante 24/7") y **qué no
  encaja** ("Sin tráfico de servidor de correo"), más la confianza (alta/media/baja). El scoring
  nunca pisa un tipo manual (`kind_locked`, candado visible); "Desbloquear" lo devuelve al scoring
  (I2). Si el scoring cambió el tipo mientras el operador miraba, el guardado devuelve `412` y la UI
  muestra el valor nuevo antes de volver a intentarlo.
- En prefijos dinámicos (pool PPPoE/DHCP) la ficha advierte: "En este rango la IP puede cambiar de
  persona; el cliente es la IP".
- **Cambiar tipo** abre un formulario corto con motivo obligatorio; el cambio aparece en Historial.
- **Pestañas:** Resumen (KPIs y top servicios), Tráfico (series y tops con ámbito de cliente),
  Seguridad (hallazgos de esta IP), Historial (tipo, alias, primera vez, cambios).
- **Privacidad:** el acceso queda registrado y la ficha lo dice en el pie ("Las consultas a fichas
  de cliente quedan registradas"); sin datos personales más allá del alias que ponga el ISP.

### 8.2 Seguridad y botnets (I1)

Propósito principal del producto (D5, [ADR-0024](adr/0024-deteccion-de-botnets-como-objetivo-principal.md)).

**Lenguaje.** "Señales compatibles con botnet", "posible escaneo saliente". Horus **nunca** escribe
"infectado": ninguna señal sola lo concluye ([`traffic-model.md`](traffic-model.md) §8). Severidad
y confianza son cosas distintas y se muestran por separado.

**Estados** ([`api.md`](api.md) §2.10): *Abierto* → *Reconocido* (alguien lo está mirando) →
*Resuelto*, o *Falso positivo* (con comentario obligatorio; alimenta la retroalimentación de
`detection`).

**Lista de hallazgos:** cabecera con KPIs (abiertos por severidad, nuevos en 24 h, IPs afectadas);
tabla con severidad (icono + texto + color), resumen legible ("Escaneo del puerto 23 a 1 240
destinos en 5 min"), cliente (IP/alias), nodo, tipo de hallazgo, confianza, estado, última vez.
Orden por severidad y recencia. Filtros: estado, severidad, tipo, nodo. Hallazgos nuevos por WebSocket
entran arriba sin mover el scroll.

**Detalle de hallazgo:**

```
┌───────────────────────────────────────────────────────────────────────┐
│ ▲ Alta · Posible escaneo saliente · 10.20.0.41 (Nodo Centro)           │
│ Confianza alta · Abierto · Primera vez 10:02 · Última 10:47 · 14 veces │
│                 [Reconocer] [Resolver] [Falso positivo] [⋯]            │
├───────────────────────────────────────────────────────────────────────┤
│ Por qué lo marcamos                                                    │
│ Esta IP intentó conectar al puerto 23 (Telnet) de 1 240 direcciones    │
│ distintas en 5 minutos; el 96 % no respondió. Es el patrón típico de   │
│ equipos IoT reclutados por botnets tipo Mirai.                         │
│ Evidencia: [destinos/min ▁▃▇▇▆] [puertos: 23 (98 %), 2323] [muestra]  │
│ Qué hacer: contactar al cliente; revisar cámaras/DVR y routers con     │
│ contraseñas por defecto; actualizar firmware.                          │
│ Línea de tiempo de ocurrencias                                         │
└───────────────────────────────────────────────────────────────────────┘
```

| Tipo de hallazgo (`kind`) | Señales ([`traffic-model.md`](traffic-model.md) §8) | Evidencia mostrada | Inc. |
| --- | --- | --- | --- |
| `botnet_c2_communication` / `reputation_hit` | Contacto con C2 conocido (reputación) | IP remota, ASN/organización, feed y fecha de inclusión, con respuesta o solo SYN, nº de conexiones, línea de tiempo | I1 |
| `outbound_scanning` | Escaneo, fan-out, puertos vigilados | Puerto(s), nº de destinos y de redes /24, % sin respuesta, destinos por minuto, muestra de destinos | I1 |
| `spam_smtp_outbound` | SMTP saliente directo | Nº de servidores SMTP distintos por hora, muestra, tipo del cliente | I1 |
| `ddos_participation` | Ráfaga hacia ≤ 3 destinos, puertos de amplificación | pps/bps, protocolo, destinos, duración vs. línea base | I1 |
| `beaconing` | Periodicidad hacia un mismo remoto | Intervalo medio y coeficiente de variación, bytes por conexión, duración | I1 (*should*) |
| `open_proxy_abuse` / `cryptomining` | Salida sostenida, destinos característicos | Proporción de subida, duración, destinos | I1 salida sostenida (*should*) · I4 |
| — | DNS anómalo, servicios entrantes inesperados, propagación interna | — | I4 |

- **Evidencia detallada** ("Ver flujos de evidencia": destinos, puertos, tiempos) requiere
  `security.evidence.read` y su acceso queda auditado; nunca hay payloads.
- **Acciones:** *Reconocer* (queda asignado a quien lo pulsa), *Resolver*, *Falso positivo*
  (comentario obligatorio; explica qué hará Horus con esa información). El historial queda en la
  línea de tiempo. **Allowlist del ISP** (prefijos/ASN propios que no deben generar hallazgos)
  en Administración › Detección.
- **Investigar IP** (I2): una IP (cliente o remota) → hallazgos, reputación, tráfico relacionado.
- **Mitigación** (cuarentena en el MikroTik): fuera de v1 ([ADR-0024](adr/0024-deteccion-de-botnets-como-objetivo-principal.md) §4);
  solo se diseñará si el PO lo aprueba (P-23).

### 8.3 Tráfico (I1)

Rango global en la toolbar (1 h · 6 h · 24 h · 7 d · 30 d), variable de nodo; series de tráfico
↓/↑; tops de clientes, servicios, categorías y organizaciones/ASN; cobertura ("92 % de los bytes
atribuidos a clientes; 8 % fuera de los prefijos declarados" con enlace a los prefijos del router).
Mismos widgets que el catálogo (§6.2).

### 8.4 Nodos, routers, prefijos de clientes y onboarding MikroTik (I0–I1; SNMP en I2)

- **Nodos:** lista con su router principal, estado del exportador y del túnel, clientes activos y
  estado de los prefijos ("Sin prefijos · modo descubrimiento").
- **Ficha del router:** cabecera con estado del exportador (*Pendiente de configurar*,
  *Exportando*, *Silencioso*, *Con pérdidas*, *Reloj desfasado*), túnel ("último handshake hace
  12 s"), flujos/s y cobertura. Pestañas: Resumen · Conexión · Prefijos de clientes · Tráfico ·
  (I2) Métricas, Interfaces.
- **Conexión (onboarding)** en tres pasos con estado en vivo, sin leer documentación
  ([`vendors/mikrotik.md`](vendors/mikrotik.md) §5.2, §7; [ADR-0022](adr/0022-mikrotik-routeros-v7-primer-fabricante.md)):
  1. **Generar script** — requisitos visibles (RouterOS v7 ≥ 7.12; Horus no escribe en el router:
     lo pega el técnico); "Copiar" y "Descargar .rsc"; aviso de que las contraseñas y el **token
     de enrolamiento** (un solo uso, 24 h) se muestran una sola vez; "Regenerar" y "Revocar token".
  2. **Pegar en el router** — el script crea la clave en el router y la envía sola a
     `POST /api/v1/enroll/wireguard` con el token; la UI pasa a "Clave recibida" sin que nadie copie
     nada. Si el token caducó o se revocó, lo dice y ofrece generar otro.
  3. **Verificación** — checklist que se marca sola: clave recibida → handshake del túnel → primer
     flujo → primeros clientes descubiertos. Si algo no llega en 2 min, explica qué revisar
     (firewall, salida a Internet del router hacia el endpoint, Traffic Flow, offload por hardware).
- **Prefijos de clientes** (`client_prefix`, [`traffic-model.md`](traffic-model.md) §4.1): tabla por
  nodo con prefijo, **rol** (*Clientes* · *Infraestructura* · *Excluido*), asignación (estática /
  dinámica) y origen (manual / importado / propuesto). Tres formas de llenarla:
  1. **Manual** con validación en línea de solapes (`CLIENT_PREFIX_OVERLAP`).
  2. **Importar del MikroTik**: lee por la API de RouterOS (solo lectura, usuario del script) los
     pools IPv4/IPv6 y las redes de las interfaces de cara al cliente y muestra una **lista para
     confirmar** (casillas, rol propuesto, diferencias con lo existente). Nada se aplica sin confirmar.
  3. **Modo descubrimiento** (nodo sin prefijos): banner "Este nodo no tiene prefijos de clientes:
     el tráfico cuenta para el nodo pero no crea clientes" y tarjeta de **propuestas** agregadas
     (p. ej. `10.20.0.0/24 · 212 IPs vistas · 41 GB`) con "Aceptar como Clientes", "Marcar como
     Infraestructura" o "Excluir".
  Quitar o reducir un prefijo avisa de cuántos clientes pasarán a *Inactivo*.
- "Script de desinstalación" en "Más".

## 9. Estados

Toda vista y **todo widget** implementan los cuatro estados (parte de la DoD de frontend,
[`backlog/README.md`](backlog/README.md) §8).

### 9.1 Carga
Mostrar algo de inmediato (`HIG loading.md`: *"Show something as soon as possible"*): `USkeleton`
con la forma final (placeholder del manifiesto); carga por widget, nunca de página completa; si una
consulta tarda > 3 s, "Calculando para 30 días…" con opción de cancelar; las recargas en segundo
plano no vuelven a mostrar esqueleto.

### 9.2 Vacío
`UEmpty` con título que describe la situación y **siguiente acción** (`HIG writing.md`: *"Provide
clear next steps on any blank screens"*):

| Caso | Copia | Acción |
| --- | --- | --- |
| Router sin configurar | "Este router aún no envía flujos" | "Conectar router" |
| Nodo sin prefijos | "Este nodo no tiene prefijos de clientes" | "Importar del MikroTik" · "Ver propuestas" |
| Sin clientes | "Aún no hemos visto clientes en los flujos de este nodo" | "Revisar prefijos de clientes" |
| Sin hallazgos | "Ningún cliente muestra señales de botnet en este periodo" | — (es una buena noticia; sin ilustración alarmista) |
| Sin resultados por filtro | "Ningún hallazgo coincide con estos filtros" | "Limpiar filtros" |
| Sin datos en el rango | "Sin tráfico registrado entre 02:00 y 03:00" | "Ampliar a 24 h" |

### 9.3 Error
En contexto, en el widget o campo que falló, con "Reintentar"; copia que dice qué pasó y qué
hacer, sin culpar (`HIG writing.md`); detalle técnico plegado con código y `trace_id`. 401 →
refresh transparente; 403 → mensaje de permiso; 404 (incluido otro ISP) → "No encontrado"; 409 →
conflicto con opciones; 422 → errores por campo; 429 → "Espera N s"; 503 → degradado (§9.4).

### 9.4 Degradado
Una dependencia caída no tumba la app; la UI dice qué está afectado y qué sigue funcionando
(estado del sistema de [`api.md`](api.md) + aviso por WebSocket).

| Falla | Qué ve el usuario | Sigue funcionando |
| --- | --- | --- |
| ClickHouse caído | Banner "Analítica no disponible desde 10:42. Los flujos se siguen recibiendo y se procesarán al recuperarse." Widgets de tráfico y tendencias muestran el aviso | Login, nodos, routers, estado de exportadores, hallazgos ya abiertos, clientes (lista) |
| Colector caído o router *Silencioso* | Exportador *Silencioso* con desde cuándo; huecos (no ceros) en gráficos | Todo lo demás |
| NATS reiniciando | "Reconectando…"; tras 1 min, banner "Las actualizaciones en vivo están en pausa; los datos se actualizan cada 30 s" | Todo por REST |
| Hub WireGuard caído | Túneles "sin handshake" y exportadores *Silenciosos* con la razón | Todo lo demás |
| PostgreSQL caído | Pantalla de mantenimiento de la app | El colector sigue recibiendo y encolando |
| Gateway caído | "No se puede conectar con Horus Flow" con reintento automático; el kiosco mantiene los últimos datos (§7.7) | — |

Para usuarios con permiso de plataforma se muestra el nombre técnico; para el resto, el funcional.

## 10. Tiempo real

### 10.1 Qué se actualiza en vivo

Un único WebSocket por pestaña o pantalla (ticket de un uso, protocolo en [`api.md`](api.md)) con
temas **siempre con ámbito de ISP**; el gateway filtra por ISP y permisos.

| Vista | Qué llega en vivo | Inc. |
| --- | --- | --- |
| Dashboards y kiosco | Resumen de tráfico, hallazgos, estado de exportadores, contador de clientes nuevos | I1 |
| Ficha del router | Handshake, primer flujo, estado del exportador | I1 |
| Hallazgos | Altas y cambios de estado | I1 |
| Pantallas NOC | Estado de cada pantalla | I1 |
| Routers (SNMP) | Estado observado y métricas | I2 |
| Alertas | Altas, reconocimiento, resolución | I3 |

### 10.2 Frescura

Todo dato operativo muestra **cuándo se obtuvo**: etiqueta relativa ("hace 12 s") con absoluto y
zona en tooltip (en kiosco, sin tooltip: §7.6). Niveles: fresco (≤ 2·T), atrasado (2–5·T, atenuado +
reloj), obsoleto (> 5·T, "Sin datos recientes", **nunca** presentado como caída ni como cero).

### 10.3 Estado de la conexión

Indicador siempre visible en la navbar: *En vivo* · *Reconectando…* (backoff 1 → 30 s con jitter;
polling REST cada 30 s mientras dure) · *Sin conexión en vivo · datos de hace 4 min* + Reintentar ·
*Sin conexión a Internet*. Al reconectar: re-suscripción y snapshot o reanudación.

### 10.4 Cómo se muestran los cambios

En el sitio, sin recargar ni mover el scroll ni reordenar filas bajo el cursor ("Hay 3 cambios de
orden · Reordenar"); realce breve que se desvanece (cambio de color sin animación con movimiento
reducido); los números se reemplazan, no "cuentan"; región `aria-live="polite"` con resúmenes
agregados (máx. 1 cada 30 s: "2 hallazgos nuevos"); toasts solo para acciones del usuario.

### 10.5 Implementación

`useRealtime(topic, handler)` sobre un único cliente WS (plugin solo cliente) que pide el ticket,
reconecta, re-suscribe y deduplica por `id` de evento; las suscripciones se indexan por ISP y se
cierran al cambiar de ISP. Throttle a 1 render/s por widget en ráfagas.

## 11. Patrones de pantalla

- **Tablas** (`UTable`): cabecera fija, columnas ordenables, redimensionables y ocultables
  (recordado por usuario), filtros facetados con contadores y "Limpiar filtros", paginación por
  cursor, números a la derecha con cifras tabulares y unidad en la cabecera, texto largo truncado
  con tooltip, acciones por fila en menú "…" y clic derecho (`HIG lists-and-tables.md`).
- **Formularios**: `USlideover` desde la derecha para crear/editar (apple-design §7 › Spatial
  consistency); validación en línea al salir del campo; verbos específicos ("Registrar router",
  "Marcar como falso positivo"), y el resultado usa el mismo verbo ("Marcado como falso positivo");
  confirmación al cerrar con cambios sin guardar (`HIG modality.md`).
- **Confirmaciones** (`UModal`) solo para lo destructivo e irreversible (revocar kiosco, reiniciar cliente,
  regenerar credenciales del router, eliminar nodo); botón nombrado por la acción y Cancelar con
  foco. Lo reversible (estado de un hallazgo, alias, quitar widget) sin confirmación, con Deshacer
  8 s (`HIG alerts.md`: *"Avoid displaying alerts for common, undoable actions"*).
- **Feedback** en cuatro tipos (apple-design §16): estado en línea, finalización (`UToast`),
  advertencia (`UAlert` en contexto) y error en contexto; respuesta inmediata en pointer-down.
- **Secretos** (script con contraseñas, códigos de pantalla): una sola vez, con "Copiar" y aviso.

## 12. Permisos en la UI

La UI refleja la autorización; la aplica el backend. **Ocultar por permiso, deshabilitar por
estado**, y todo control deshabilitado explica por qué (`HIG feedback.md`; `aria-disabled` para
que el lector de pantalla lea la razón).

| Situación | Tratamiento |
| --- | --- |
| Sin permiso de lectura de una sección | Oculta en barra, búsqueda y enlaces |
| Sin permiso para una acción | Acción oculta (un `isp_viewer` no ve "Cambiar tipo") |
| Recurso de otro ISP | No aparece; URL directa → "No encontrado" |
| Permiso, pero imposible por estado | Deshabilitada con razón ("Regenerar credenciales" deshabilitado: "El router no tiene túnel registrado") |
| Widget de un dashboard compartido sin permiso | "No tienes acceso a este widget", sin datos |
| Kiosco (JWT `typ = kiosk`) | Solo lectura de los dashboards asignados y widgets `kiosk_allowed`; cualquier otra ruta → `403 KIOSK_FORBIDDEN`; sin fichas de cliente |

Implementación: `usePermission()` y `useCan(permiso, isp?)` sobre `GET /me` (permisos **por ISP**);
`definePageMeta({ permission })` con middleware; refresco al recibir 403 o evento de cambio de rol.

## 13. Principios visuales

### 13.1 Materiales, profundidad y movimiento
Barra lateral y navbar como capa funcional; la navbar puede usar material translúcido con
desvanecido en el borde (apple-design §12); **nunca** translúcido sobre translúcido ni en el
contenido (tarjetas, tablas y gráficos sólidos, `apple-hig/SKILL.md › Lens 2`); con
`prefers-reduced-transparency`, sólido. Movimiento breve, interrumpible, resortes sin rebote
(~0,3 s) para paneles; paneles que salen y vuelven por el mismo lado (apple-design §3, §4, §7);
fundidos cortos con `prefers-reduced-motion` (apple-design §14). En kiosco, casi sin movimiento.

### 13.2 Color

Colores semánticos de Nuxt UI (`primary`, `neutral`, `success`, `info`, `warning`, `error`) y
utilidades semánticas; nunca colores crudos de Tailwind en componentes. **Un color, un
significado; nunca solo color** (`HIG color.md`, `HIG accessibility.md › Vision`).

| Familia | Valor | Color | Icono (Lucide) | Texto |
| --- | --- | --- | --- | --- |
| Exportador | Exportando | `success` | `i-lucide-radio-tower` | Exportando |
| | Pendiente de configurar | `neutral` | `i-lucide-circle-dashed` | Pendiente |
| | Silencioso | `error` | `i-lucide-radio-off` (o equivalente) | Silencioso · desde 10:42 |
| | Con pérdidas / Reloj desfasado | `warning` | `i-lucide-triangle-alert` | Con pérdidas · 3 % |
| Severidad de hallazgo | Crítica | `error` | `i-lucide-octagon-alert` | Crítica |
| | Alta | `error` | `i-lucide-triangle-alert` | Alta |
| | Media | `warning` | `i-lucide-circle-alert` | Media |
| | Baja | `neutral` | `i-lucide-info` | Baja |
| Estado de hallazgo | Abierto / Reconocido / Resuelto / Falso positivo | `neutral` (la urgencia la da la severidad) | iconos propios | texto |
| Tipo de cliente | Residencial / Comercial | `neutral` / `info` (no son estados de salud) | `i-lucide-house` / `i-lucide-building-2` (+ `i-lucide-lock` si es manual) | texto + origen |
| Estado de cliente | Activo / Inactivo | `neutral` (Inactivo atenuado) | — | texto |
| Router (I2) | online / degraded / offline / stale / unknown / maintenance | según el modelo de estado de [`api.md`](api.md) | | |

Crítica y Alta comparten color porque comparten urgencia; se distinguen por icono y texto.
`primary` (marca, P-20) solo para acciones primarias y selección. Ningún color de estado se usa
como color de serie en gráficos; paleta categórica validada para daltonismo, estable por servicio.

### 13.3 Tipografía
Fuente del sistema para la interfaz y monoespaciada para IPs, claves públicas y `trace_id`
(apple-design §15); cifras tabulares en tablas, KPIs y ejes; jerarquía por peso + tamaño;
tamaños en `rem`; cuerpo ≥ 14 px en tablas densas, 16 px en formularios; escala mural en §7.4.

### 13.4 Iconografía y escritura
Un único set (Lucide); botón solo-icono con `aria-label` y tooltip (salvo kiosco). Español neutro,
sentence case. Términos del sector (flujo, IPFIX, ASN, handshake) en su forma habitual, con
glosario. Unidades: bits/s para tasas, bytes para volumen, SI decimal; fechas `dd/mm/aaaa HH:mm`.
Hallazgos: frases que describen comportamiento observado, no culpa.

## 14. Accesibilidad (WCAG 2.1 AA)

Verificada en CI (axe en Playwright) y en cada demo.

| Criterio | Aplicación |
| --- | --- |
| 1.1.1 | Iconos con `aria-label`; cada gráfico con título, frase con el hallazgo principal y **tabla alternativa** (`HIG charts.md › Enhancing the accessibility of a chart`); ECharts con `aria` y decals |
| 1.3.1 | Tablas con `<th scope>`, formularios con `<label>`, landmarks, encabezados jerárquicos; cada widget es una región con nombre |
| 1.4.1 | Estado = icono + texto + color; series distinguibles por patrón |
| 1.4.3 / 1.4.11 | Texto ≥ 4,5:1, componentes y foco ≥ 3:1 en claro y oscuro; en kiosco ≥ 7:1 en valores clave (§7.4); tokens verificados por script en CI |
| 1.4.4 / 1.4.10 | Zoom 200 %; 320 px sin scroll horizontal |
| 2.1.1 | Todo operable con teclado, **incluido el editor de dashboards** (§6.6) |
| 2.2.1 | Aviso antes de expirar la sesión; toasts con acción se pausan con foco/hover |
| 2.2.2 | Rotación del kiosco pausable (Espacio); actualizaciones en vivo sin parpadeo |
| 2.3.1 | Nada parpadea más de 3 veces/s; hallazgos críticos sin parpadeo |
| 2.4.x | "Saltar al contenido", foco visible, foco devuelto al cerrar overlays, `<title>` con ISP |
| 3.3.x | Errores asociados al campo con sugerencia |
| 4.1.3 | Toasts y cambios en vivo en regiones `aria-live` con la política de §10.4; movimientos del editor anunciados |

Además: objetivos ≥ 24 × 24 px (≥ 44 px en móvil), `prefers-reduced-motion`,
`prefers-reduced-transparency`, `prefers-contrast: more`.

## 15. Componentes Nuxt UI por patrón

| Patrón | Componentes |
| --- | --- |
| App y layout | `UApp`, `UDashboardGroup`, `UDashboardSidebar`, `UDashboardSidebarCollapse`, `UDashboardSidebarToggle`, `UDashboardPanel`, `UDashboardNavbar`, `UDashboardToolbar` |
| Selector de ISP | `UDropdownMenu` (con `UInput` de búsqueda), `UAvatar` (monograma), `UCommandPalette` |
| Navegación | `UNavigationMenu`, `UBreadcrumb`, `UTabs`, `ULink`, `UDashboardSearch`, `UKbd` |
| Tablas | `UTable`, `UPagination`, `USelectMenu`, `UBadge`, `UCheckbox`, `UDropdownMenu`, `UContextMenu`, `UTooltip` |
| Dashboards y widgets | `UCard` como contenedor de widget, `vue-echarts`, `USkeleton`, `UProgress`, librería de grilla (§6.6), `USlideover` (catálogo de widgets) |
| Detalle | `UTabs`, `UCard`, `UBadge`, `UTimeline` (historial, ocurrencias), `UCollapsible` (detalle técnico) |
| Formularios | `UForm`, `UFormField`, `UInput`, `UTextarea` (motivos), `USelect`, `USwitch`, `UInputNumber` (umbrales, duraciones) |
| Onboarding | `UStepper`, `UButton` "Copiar", `UAlert` "solo se muestra una vez" |
| Overlays | `USlideover`, `UModal`, `UDrawer` (móvil), `UPopover` |
| Feedback y estados | `UToast`, `UAlert`, `UBanner`, `UChip`, `UEmpty`, `UError`, `USkeleton` |
| Kiosco | Layout propio sin componentes interactivos; `UBadge` y tipografía mural |

## 16. Arquitectura técnica del frontend

```
apps/frontend/
├── app/
│   ├── layouts/        default.vue, auth.vue, kiosk.vue
│   ├── pages/
│   │   ├── t/[slug]/    dashboards/, nodes/, routers/, clients/, traffic/, security/, admin/
│   │   ├── global/ platform/ account/ login/
│   │   └── kiosk/      emparejamiento y reproductor
│   ├── widgets/        un directorio por tipo: manifest.ts + Widget.vue (+ tests); registro.ts
│   ├── components/
│   │   ├── dashboard/  DashboardGrid, WidgetHost, WidgetCatalog (I2), DashboardEditor (I2)
│   │   ├── tenant/     TenantSwitcher, TenantBadge
│   │   ├── status/     StatusBadge, SeverityBadge, ClientTypeBadge, FreshnessLabel, ConnectionIndicator
│   │   ├── charts/     TimeSeriesChart, TopNBar, Sparkline, Heatmap, ChartCard (con tabla alternativa)
│   │   ├── findings/ clients/ onboarding/
│   │   └── states/     LoadingState, EmptyState, ErrorState, DegradedNotice
│   ├── composables/    useApi, useAuth, useTenant, usePermission, useRealtime, useSystemStatus,
│   │                   useTimeRange, useUrlState, useDashboard, useKiosk, useWakeLock
│   ├── middleware/     auth.global.ts, tenant.global.ts, permission.ts
│   ├── plugins/        realtime.client.ts, otel.client.ts, echarts.client.ts
│   └── app.config.ts   tema (colores semánticos, tokens de estado, escala mural)
├── i18n/locales/       es.json
├── mocks/              handlers generados del OpenAPI + datos del simulador
├── types/api/          generados desde OpenAPI (C5) y esquemas de dashboard (C9)
└── tests/              unit (Vitest), e2e (Playwright + axe), kiosk (reloj acelerado)
```

- **Datos:** `openapi-fetch` con tipos de `openapi-typescript`, envuelto en `useApi` (ISP actual,
  `traceparent`, errores tipados, refresh único ante 401). Cachés de consulta **indexadas por ISP**.
  Sin Pinia salvo que el editor de dashboards (I2) lo justifique.
- **Renderizado:** SPA (`ssr: false`) estática detrás de Traefik.
- **Sesión:** access token en memoria, refresh en cookie `HttpOnly`; credencial de kiosco en cookie
  `HttpOnly` propia (`Path=/api/v1/kiosk`); en `localStorage` solo preferencias (tema, columnas, último ISP).
- **Widgets:** cada widget es un módulo con manifiesto validado en build contra C9; el registro se
  genera por convención de carpeta; un widget no importa otro widget.
- **Presupuestos de rendimiento:** LCP < 2,5 s en el dashboard "NOC del ISP"; 60 fps con
  actualizaciones en vivo; bundle inicial < 250 KB gzip (ECharts y la librería de grilla bajo
  demanda); memoria del kiosco estable 24 h (§7.9).

## 17. Dependencias con otros documentos

| Necesito de | Qué | Estado |
| --- | --- | --- |
| [`api.md`](api.md) | Rutas `/t/{slug}`, `GET /me` con permisos por ISP, clientes (`set-kind`, `unlock-kind`, `reset`), `client_prefix` e importación, hallazgos (§2.10), dashboards, `widget-types` y datos por widget (§2.11), kioscos (§2.12), `POST /enroll/wireguard`; temas WS; estado del sistema | Alineado; se congela en C5/C6 (I0-05) |
| [`events.md`](events.md) | Eventos UI-visibles: exportador, cliente descubierto/cambio de tipo, hallazgo abierto/actualizado | A congelar en C4/C8 |
| [`security.md`](security.md) | Catálogo de permisos (`customers.*`, `security.findings.*`, `security.evidence.read`, `dashboards.*`, `kiosks.manage`, `wireguard.write`), credencial de kiosco, registro de acceso a fichas de cliente | A congelar en C7 |
| [`database.md`](database.md), [`traffic-model.md`](traffic-model.md) | Ciclo de vida del cliente, `kind_locked`, `client_prefix` (roles, modo descubrimiento), señales de botnet | Alineado; se congela en C2/C3 |
| [`vendors/mikrotik.md`](vendors/mikrotik.md) | Pasos y textos del onboarding, requisitos de versión, causas de "Silencioso" | Alineado |
