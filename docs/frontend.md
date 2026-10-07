# Frontend — Arquitectura de información y UX

Aplicación web de Horus Flow en **Nuxt 4 + Vue 3 + TypeScript, Nuxt UI, Tailwind CSS y Apache
ECharts** ([`vision.md`](vision.md) §1). El frontend habla **solo** con el api-gateway por HTTPS y
WebSocket; nunca con PostgreSQL, ClickHouse, SNMP ni WireGuard.

Documentos relacionados: contrato REST/WS en [`api.md`](api.md), eventos en
[`events.md`](events.md), permisos en [`security.md`](security.md), modos de fallo en
[`architecture.md`](architecture.md) y [`disaster-recovery.md`](disaster-recovery.md), historias
en [`backlog/`](backlog/README.md).

Las citas a las skills de diseño usan el formato `archivo › sección`:

- **apple-design** = `.claude/skills/apple-design/SKILL.md` (secciones numeradas §1–§17).
- **HIG** = `.claude/skills/apple-hig/references/hig/<página>.md › <encabezado>`. Se aplican los
  *principios y fundamentos* (accesibilidad, color, tipografía, layout, escritura), no las
  convenciones propias de iOS/macOS, como indica `apple-hig/SKILL.md › Step 1 › Scope and limits`
  para apps web.

---

## 1. Tesis de diseño

> **Horus Flow es la consola de verdad de la red del ISP: dice qué está pasando, desde cuándo y
> con qué certeza.**

El elemento distintivo no es decorativo: es la **semántica de estado y frescura** — cada dato
muestra su estado (online, warning, critical, offline, unknown) con icono + texto + color, y cada
dato en vivo dice cuán reciente es. Todo lo demás es deliberadamente sobrio. Evitamos el cliché de
"NOC": fondo casi negro con un acento verde ácido, que `apple-hig/SKILL.md › Lens 3 (craft)`
identifica como plantilla; el tema oscuro existe para pantallas de NOC y turnos nocturnos, no como
identidad.

Principios rectores (apple-design §16; HIG `design-principles.md`):

| Principio | Cómo se aplica en Horus Flow |
| --- | --- |
| Propósito | Cada pantalla responde una pregunta operativa ("¿qué routers están caídos?", "¿quién consume más?"). Si no la responde, no se construye |
| Agencia | Filtros persistentes en la URL, deshacer en acciones reversibles, confirmación solo en destructivas irreversibles (apple-design §16 › Agency) |
| Responsabilidad | Secretos nunca visibles tras guardarse; clave privada WG mostrada una sola vez; scoring residencial/comercial siempre con razones y confianza |
| Familiaridad | Patrones estándar de Nuxt UI; lo que se ve igual se comporta igual en todas las secciones |
| Flexibilidad | Escritorio de NOC (≥ 1440 px y pantallas murales), portátil, tablet de técnico, móvil para consultar alertas |
| Simplicidad | Camino común primero, opciones avanzadas un nivel más abajo (apple-design §16 › Simplicity) |
| Oficio | Números tabulares alineados, unidades consistentes, tiempos relativos con absolutos en tooltip |
| Deleite | Calma y confianza: nada parpadea sin motivo; los cambios en vivo se notan sin alarmar |

## 2. Usuarios y contextos

Personas a validar con el PO (pregunta P-03 en [`open-questions/product.md`](open-questions/product.md)).

| Persona | Contexto | Necesita sobre todo |
| --- | --- | --- |
| Operador NOC | Escritorio, varias pantallas, turnos 24/7, pantalla mural | Estado en vivo, alertas, detalle de router, WireGuard |
| Técnico de campo | Tablet/móvil, conectividad irregular | Estado de routers de su zona, interfaces, último contacto |
| Analista de seguridad | Escritorio | Detecciones, reputación, búsqueda por IP, auditoría |
| Ingeniero de red / capacidad | Escritorio | Tráfico, ASN, servicios, interfaces saturadas |
| Gerente | Portátil/móvil, ocasional | Resumen, analítica ISP, reportes |
| Ejecutivo comercial | Portátil | Posibles clientes comerciales, consumo por cliente |
| Administrador de plataforma | Escritorio | Usuarios, roles, sesiones, auditoría, estado del sistema |

## 3. Mapa de navegación

Sidebar con **cinco grupos y como máximo dos niveles**, siguiendo
`HIG sidebars.md › Best practices`: *"In general, show no more than two levels of hierarchy in a
sidebar"* y *"use succinct, descriptive labels to title each group"*. Las etiquetas nombran el
contenido, no paraguas genéricos (apple-design §16 › "Direct, specific labels"), por eso la
portada se llama **Resumen** y no "Inicio".

Columna *Sprint*: cuándo aparece la sección. **MVP** = existe al cierre de S5 (hito H4 de
[`roadmap.md`](roadmap.md)). Columna *Permiso*: permiso de lectura que la hace visible (catálogo
definitivo en [`security.md`](security.md)).

### 3.1 Árbol

```
Resumen                                   /
OPERACIÓN
├─ Inventario                             /inventory
│   ├─ Routers                            /inventory/routers
│   │    └─ (detalle)                     /inventory/routers/:id[/:tab]
│   ├─ Sitios                             /inventory/sites[/:id]
│   ├─ Grupos                             /inventory/groups
│   ├─ Clientes                           /inventory/clients[/:id]
│   └─ Catálogo                           /inventory/catalog   (vendors, modelos, firmware)
├─ Monitoreo                              /monitoring
│   ├─ Estado de la red                   /monitoring/status
│   ├─ Interfaces                         /monitoring/interfaces
│   └─ Disponibilidad                     /monitoring/availability
└─ WireGuard                              /wireguard
    ├─ Servidores                         /wireguard/servers[/:id]
    └─ Peers                              /wireguard/peers[/:id]
INTELIGENCIA
├─ Tráfico                                /traffic
│   ├─ Panorama                           /traffic/overview
│   ├─ Explorador                         /traffic/explorer
│   ├─ Servicios y categorías             /traffic/services
│   └─ ASN y organizaciones               /traffic/asn[/:asn]
├─ Seguridad                              /security
│   ├─ Detecciones                        /security/detections[/:id]
│   ├─ Reputación                         /security/reputation
│   └─ Investigar IP                      /security/ip/:ip
└─ Analítica                              /analytics
    ├─ ISP                                /analytics/isp
    ├─ Clientes                           /analytics/clients[/:id]
    ├─ Routers                            /analytics/routers[/:id]
    └─ Perfil de uso                      /analytics/usage-profile   (residencial/comercial)
GESTIÓN
├─ Alertas                                /alerts
│   ├─ Activas                            /alerts/active
│   ├─ Historial                          /alerts/history
│   ├─ Reglas                             /alerts/rules
│   └─ Canales                            /alerts/channels
└─ Reportes                               /reports
    ├─ Generados                          /reports/generated
    ├─ Programados                        /reports/scheduled
    └─ Nuevo reporte                      /reports/new
ADMINISTRACIÓN
├─ Usuarios                               /admin/users
├─ Roles y permisos                       /admin/roles
├─ Sesiones                               /admin/sessions
├─ Auditoría                              /admin/audit
├─ Clasificación                          /admin/classification   (catálogo servicios/categorías, fuentes ASN)
├─ Retención y almacenamiento             /admin/storage
└─ Estado del sistema                     /admin/system

Fuera del sidebar
├─ /login, /login/2fa, /reset-password/:token, /forgot-password
├─ Mi cuenta  /account  (Perfil, Seguridad: contraseña y 2FA, Sesiones, Preferencias)
└─ Centro de notificaciones (panel desde la toolbar)
```

### 3.2 Secciones, sprint y MVP

| Sección | Sprint | MVP | Permiso de lectura | Tiempo real |
| --- | --- | --- | --- | --- |
| Resumen | S1 (base), S3 (estado), S9 (ISP) | ✔ | autenticado (cada tarjeta exige su permiso) | ✔ |
| Inventario › Routers / Grupos / Catálogo | S3 | ✔ | `devices.read` | estado ✔ |
| Inventario › Sitios | S3 | ✔ | `sites.read` | — |
| Inventario › Clientes | S3 (entidad), S9 (consumo) | ✔ (lista básica) | `customers.read`* (consumo por cliente: `traffic.client.read`) | — |
| Detalle de router | S3 → S5 (Métricas, Interfaces), S6+ (Tráfico), S11 (Alertas) | ✔ | `devices.read` (+ `snmp.read` para Métricas) | ✔ |
| WireGuard | S4 | ✔ | `wireguard.read` | handshake ✔ |
| Monitoreo | S5 (estado, interfaces), S12 (disponibilidad) | ✔ (estado, interfaces) | `devices.read`, `snmp.read` | ✔ |
| Tráfico | S6 (crudo), S7 (servicios, ASN) | — | `traffic.read` | Panorama ✔ |
| Seguridad | S8 | — | `security.read` | Detecciones ✔ |
| Analítica | S9, S10 (perfil de uso) | — | `traffic.read`; vistas por cliente y perfil de uso: `traffic.client.read` | ISP ✔ |
| Alertas | S5 (centro de notificaciones mínimo), S11 (completo) | parcial | `alerts.read` | ✔ |
| Reportes | S12 | — | `reports.read` | estado de generación ✔ |
| Admin › Usuarios, Roles, Sesiones, Auditoría | S2 | ✔ | `users.read`, `roles.read`, `sessions.read`, `audit.read` | sesiones ✔ |
| Admin › Clasificación | S7 | — | `classification.read` (editar: `classification.manage`) | — |
| Admin › Retención | S13 | — | `settings.read` (editar: `settings.manage`) | — |
| Admin › Estado del sistema | S5 (básico), S14 | ✔ | `settings.read` | ✔ |
| Mi cuenta | S1 (placeholder), S2 | ✔ | autenticado | — |

Permisos según el catálogo de [`security.md`](security.md) §6.1. \* `customers.read` /
`customers.manage` no existen todavía en ese catálogo; propuesta registrada como C-09 en
[`roadmap.md`](roadmap.md) §7.

**Regla de aparición progresiva:** una sección solo entra al sidebar en el sprint en que funciona.
No hay ítems "próximamente" — cada ítem visible debe llevar a algo útil (apple-design §16 ›
Purpose).

### 3.3 Navegación transversal

- **Búsqueda global (⌘K / Ctrl+K)** con `UDashboardSearch`: routers por nombre/IP, sitios,
  clientes, peers WG, IPs (→ Investigar IP), y comandos ("Registrar router", "Cambiar tema").
  Justificación: `HIG searching.md › Best practices` (la búsqueda importante merece un lugar
  principal).
- **Breadcrumbs** (`UBreadcrumb`) en vistas de detalle: *Inventario › Sitio Norte › rt-norte-01*.
  Responde "¿dónde estoy?" de apple-design §16 › Wayfinding.
- **Enlaces cruzados**: toda entidad mencionada es un enlace a su detalle (router en una alerta,
  cliente en una fila de tráfico, ASN en una detección).
- **Estado en la URL**: filtros, orden, página, rango temporal y pestaña activa se serializan en
  la query string para compartir y para volver atrás sin perder contexto.

## 4. Alcance del MVP (fin de S5)

Login (con TOTP) · Resumen con estado en vivo · Inventario (routers, sitios, grupos, catálogo,
clientes básicos) · Detalle de router (Resumen, Interfaces, Métricas, WireGuard, Actividad) ·
WireGuard (servidores, peers) · Monitoreo (estado de la red, interfaces) · Centro de
notificaciones mínimo · Administración (usuarios, roles, sesiones, auditoría, estado del sistema) ·
Mi cuenta.

## 5. Layout

### 5.1 Estructura

```
┌──────────────┬───────────────────────────────────────────────────────────────┐
│ ◉ Horus Flow │ Inventario › Routers          [● En vivo · hace 3 s] [🔔 2] [?] │ ← UDashboardNavbar
│ [⌘K Buscar]  ├───────────────────────────────────────────────────────────────┤
│              │ [Buscar…] [Estado ▾] [Sitio ▾] [Tags ▾]      [Importar] [+ Router]│ ← UDashboardToolbar
│ Resumen      ├───────────────────────────────────────────────────────────────┤
│ OPERACIÓN    │                                                               │
│  Inventario  │   contenido (UDashboardPanel #body, scroll propio)             │
│  Monitoreo   │                                                               │
│  WireGuard   │                                                               │
│ INTELIGENCIA │                                                               │
│  …           │                                                               │
│──────────────│                                                               │
│ (A) Ana Ruiz │                                                               │
│     Operador │                                                               │
└──────────────┴───────────────────────────────────────────────────────────────┘
  UDashboardSidebar (collapsible, resizable)        UDashboardPanel
```

- **Sidebar** (`UDashboardSidebar` + `UNavigationMenu` vertical): grupos con encabezado; los
  submenús se despliegan como acordeón (`HIG sidebars.md › "Group hierarchy with disclosure
  controls"`). Se puede colapsar a iconos y ocultar (`"Consider letting people hide the
  sidebar"`). Iconos Lucide coherentes, sin color salvo estado (`"Make sure any sidebar icon colors
  you choose serve a clear purpose"`). Pie: `UUser` + `UDropdownMenu` (menú de usuario). No hay
  acciones críticas en el pie.
- **Navbar** (`UDashboardNavbar`): título de la sección (`HIG toolbars.md › Titles`: título útil y
  conciso, nunca el nombre de la app), indicador de conexión (§7.3), campana del centro de
  notificaciones (`UChip` con contador), ayuda.
- **Toolbar** (`UDashboardToolbar`): filtros a la izquierda, acciones a la derecha con **una sola
  acción primaria** con estilo sólido; el resto en estilo neutral o en un menú "Más"
  (`HIG toolbars.md › Best practices`: *"Choose items deliberately to avoid overcrowding"*,
  *"Add a More menu to contain additional actions"*).
- **Banner global** (`UBanner`) encima del panel solo para estados del sistema que afectan a toda
  la app (§8.4).

### 5.2 Responsive

Layout guiado por espacio disponible, no por dispositivo (`HIG layout.md › Adaptability`:
*"Design a layout that adapts gracefully and consistently"*), con las mismas funciones en
cualquier tamaño.

| Ancho | Sidebar | Tablas | Dashboards |
| --- | --- | --- | --- |
| ≥ 1440 px (NOC) | Expandido, redimensionable | Todas las columnas | Rejilla de 12 columnas, hasta 4 gráficos por fila |
| 1024–1439 px | Expandido o colapsado a iconos (recordado) | Columnas secundarias ocultables | 2–3 por fila |
| 768–1023 px | Oculto; botón de menú (`UDashboardSidebarToggle`) abre panel | Columnas prioritarias + detalle en slideover | 1–2 por fila |
| < 768 px | Panel deslizable | Filas compactas tipo lista (estado, nombre, dato clave) | 1 por fila, ancho completo del área de trazado (`HIG charts.md › Best practices`: *"In a compact environment, maximize the width of the plot area"*) |

Mínimo soportado: **320 px** sin scroll horizontal de página (WCAG 1.4.10). Las tablas anchas
desplazan horizontalmente **dentro** de su contenedor con la primera columna fija.

**Modo mural** (`?display=wall`, S9): oculta sidebar y toolbar, aumenta tipografía, rota entre
dashboards elegidos; pensado para la pantalla del NOC.

### 5.3 Tema claro / oscuro

- Por defecto **sigue al sistema** (`prefers-color-scheme`); `HIG dark-mode.md › Best practices`
  recomienda *"Avoid offering an app-specific appearance setting"*. **Desviación justificada:**
  en una app web usada en salas de NOC con equipos compartidos y pantallas murales, el operador
  necesita forzar oscuro sin tocar el sistema operativo; por eso el menú de usuario ofrece
  **Sistema (por defecto) / Claro / Oscuro** (`UColorModeSelect` o equivalente en el menú).
- Ambos temas se diseñan y prueban por igual (*"Ensure that your app looks good in both
  appearance modes"*); fondos claros suavizados, no blanco puro (`HIG dark-mode.md › Dark Mode
  colors`: *"Soften the color of white backgrounds"*).
- El cambio de tema hace una transición suave de color, sin salto brusco de brillo (apple-design
  §14).
- ECharts usa un tema generado desde los mismos tokens CSS; al cambiar el modo, los gráficos se
  re-tematizan sin recargar datos.

## 6. Patrones de pantalla

### 6.1 Listas y tablas con filtros

Uso: routers, sitios, clientes, peers, interfaces, alertas, detecciones, usuarios, auditoría,
reportes.

- `UTable` (TanStack) con cabecera fija, columnas **ordenables y redimensionables**, visibilidad
  de columnas configurable y recordada por usuario (`HIG lists-and-tables.md › Platform
  considerations`: *"let people click a column heading to sort"*, *"Let people resize columns"*;
  *"Use descriptive column headings in a multicolumn table"*).
- Toolbar de filtros: búsqueda (`UInput` con icono y atajo `/`), filtros facetados
  (`USelectMenu` multiple con contadores), rango temporal donde aplique, "Limpiar filtros" visible
  cuando hay alguno activo. Filtros activos como `UBadge` removibles.
- Paginación por cursor del servidor (`UPagination` o "Cargar más" según [`api.md`](api.md)),
  tamaño de página 25/50/100. Nunca se descargan listas completas al cliente.
- Selección múltiple (`UCheckbox` en primera columna) → barra de acciones en bloque.
- Fila: clic abre el detalle; menú contextual por fila (`UDropdownMenu` con "…" y `UContextMenu`
  con clic derecho) con las mismas acciones.
- Celdas numéricas alineadas a la derecha con cifras tabulares; unidades en la cabecera
  ("Tráfico (Mbps)") y no repetidas en cada celda.
- Texto largo truncado con tooltip completo (`HIG lists-and-tables.md › Content`: *"Consider ways
  to preserve readability of text that might otherwise get clipped"*).
- Exportar CSV de la vista filtrada cuando el usuario tiene `reports.export`.

### 6.2 Detalle de router (plantilla de detalle de entidad)

```
Inventario › Sitio Norte › rt-norte-01
┌───────────────────────────────────────────────────────────────────────┐
│ ● Online   rt-norte-01   10.20.0.1 · MikroTik CCR2004 · RouterOS 7.15  │
│ Visto hace 8 s · RTT 4 ms · Uptime 41 d          [Editar] [⋯]          │
├───────────────────────────────────────────────────────────────────────┤
│ Resumen | Interfaces | Métricas | WireGuard | Tráfico | Alertas | Actividad │
├───────────────────────────────────────────────────────────────────────┤
│ [CPU 23 %] [RAM 41 %] [Temp 52 °C] [Interfaces 14/16 up] [Alertas 1]  │
│ ┌ CPU / RAM (últimas 6 h) ──────────┐ ┌ Tráfico WAN (6 h) ──────────┐ │
│ └───────────────────────────────────┘ └─────────────────────────────┘ │
│ Inventario · Credenciales · Túnel WG · Etiquetas                       │
└───────────────────────────────────────────────────────────────────────┘
```

- Cabecera con estado (icono + texto + color), identidad, frescura y acciones; la acción primaria
  es Editar; Eliminar, Rotar credencial, Ver en Grafana van en "⋯".
- `UTabs` sincronizadas con la URL (`/inventory/routers/:id/metrics`). Las pestañas aparecen
  según sprint y permiso (Tráfico requiere `traffic.read`). **Las pestañas navegan, no actúan**.
- Pestaña Interfaces: tabla con estado operativo/administrativo, velocidad, RX/TX actuales con
  sparkline, errores y drops; clic → slideover con gráficos de la interfaz.
- Pestaña Métricas: rango temporal (`1 h · 6 h · 24 h · 7 d · 30 d · personalizado`) compartido por
  todos los gráficos de la pestaña.
- Pestaña Actividad: auditoría y cambios de estado del router en `UTimeline`.
- Misma plantilla para sitio, cliente, peer WG, ASN, IP investigada y detección.

### 6.3 Dashboards con ECharts

Uso: Resumen, Monitoreo, Tráfico › Panorama, Analítica, Seguridad.

- **Rejilla de tarjetas** (`UCard` / `UPageCard`): fila superior de indicadores clave (KPI con
  valor, unidad, variación y *sparkline*), debajo gráficos ordenados por importancia
  (`HIG layout.md › Visual hierarchy`: *"Order content by relative importance"*).
- **Tipos comunes primero** (`HIG charting-data.md › Best practices`: *"In general, prefer using
  common chart types"*): series temporales (línea/área apilada) para tráfico y métricas; barras
  horizontales para top N (servicios, ASN, clientes); donut solo para composición de ≤ 6
  categorías; heatmap hora × día para patrones de uso (S10), con leyenda explicativa porque es
  menos familiar (*"help people learn how to interpret the chart"*).
- **Top N + "Otros"**: nunca más de 10 series; el resto se agrega.
- **Color estable por entidad**: el mismo servicio/categoría tiene el mismo color en todos los
  gráficos (`HIG charts.md › Best practices`: *"Maintain continuity among multiple charts that
  use the same data"*). Paleta categórica definida en tokens y validada para daltonismo; los
  colores de estado (§10.2) **no** se reutilizan como colores categóricos (`HIG color.md › Best
  practices`: *"Avoid using the same color to mean different things"*).
- **Rango temporal global** del dashboard en la toolbar; cada gráfico muestra su granularidad
  ("cada 5 min") y la marca de tiempo del dato más reciente.
- **Interacción opcional, no obligatoria** (`HIG charts.md › Best practices`: *"don't require
  interaction to reveal critical information"*): el valor clave está escrito en la tarjeta; el
  tooltip y el zoom (`dataZoom`) añaden detalle. Clic en una serie/barra → navega al detalle o
  aplica filtro.
- **Rendimiento**: renderer canvas, `sampling: 'lttb'` en series largas, agregación en servidor
  según el rango (ClickHouse decide la granularidad), carga diferida de gráficos fuera de pantalla,
  `vue-echarts` con importación modular de ECharts.
- **Huecos, no ceros**: un periodo sin datos (colector caído, router sin exportar, buffer
  desbordado) se dibuja como hueco con la marca "Sin datos" y nunca como cero; si la API devuelve
  `meta.partial = true`, la tarjeta muestra "Datos incompletos en este rango" (principio P4 de
  [`architecture.md`](architecture.md); tabla de cobertura de `analytics`).
- **Tiempos**: el backend envía UTC; la UI muestra en la zona horaria del usuario (preferencia, por
  defecto la de la organización — P-09) e indica la zona en ejes y tooltips de rangos > 1 día.

### 6.4 Formularios y paneles

- Crear/editar entidades: `USlideover` desde la derecha, que entra y sale por el mismo lado y se
  origina en el botón que lo abrió (apple-design §7 › Spatial consistency). Formularios largos
  (asistentes como alta de peer WG, nuevo reporte, regla de alerta): página completa o `UStepper`.
- `UForm` + `UFormField` con validación en línea al salir del campo, no solo al enviar
  (apple-design §16 › "validate inline (not on submit)"; `HIG feedback.md`).
- Botones con verbo específico: "Registrar router", "Revocar peer", "Guardar cambios"; nunca
  "Aceptar"/"Enviar" (`HIG writing.md › Best practices`: *"Be action oriented"*). La acción
  mantiene su nombre en el resultado: "Revocar peer" → toast "Peer revocado".
- Cambios sin guardar → confirmación al cerrar (`HIG modality.md › Best practices`: *"help people
  avoid data loss by getting confirmation before closing a modal view"*).
- Secretos (credenciales, claves WG, códigos 2FA): mostrados una única vez con "Copiar" y
  "Descargar", y aviso explícito.

### 6.5 Acciones destructivas y confirmaciones

- **Confirmación (`UModal`) solo para lo destructivo e irreversible**: eliminar router, revocar
  peer, desactivar usuario, rotar credencial compartida. Texto: qué pasa y a qué afecta ("Se
  revocará el peer y el router rt-norte-01 perderá el túnel de gestión"); botón destructivo
  nombrado por la acción, Cancelar con foco por defecto (`HIG alerts.md › Best practices`:
  *"Use alerts sparingly"*; apple-design §16 › Agency).
- Para acciones reversibles (quitar tag, silenciar alerta, archivar reporte): sin confirmación,
  toast con **Deshacer** durante 8 s (`HIG alerts.md`: *"Avoid displaying alerts for common,
  undoable actions"*).
- Para eliminaciones de alto impacto (sitio con hijos, servidor WG con peers activos): se pide
  escribir el nombre del recurso.

### 6.6 Feedback

Cuatro tipos (apple-design §16 › "Feedback comes in four kinds"):

| Tipo | Componente | Ejemplo |
| --- | --- | --- |
| Estado | Indicadores en línea, `UBadge`, barra de conexión | "Sondeando… 412/500 routers" |
| Finalización | `UToast` (auto-cierre 4–6 s) | "Router registrado" con enlace "Ver" |
| Advertencia | `UAlert` warning en contexto | "Este firmware tiene una versión más reciente" |
| Error | `UAlert` error en contexto, error de campo | "No se pudo conectar por SNMP: tiempo de espera agotado. Revisa la community o el túnel WG." |

La respuesta visual es inmediata en pointer-down (estado `:active` de botones) y nunca hay
retardos artificiales (apple-design §1 › Response).

## 7. Tiempo real

### 7.1 Qué se actualiza en vivo

Un único WebSocket por pestaña (`/api/v1/ws`, abierto con un ticket de un uso de
`POST /api/v1/realtime/tickets`; protocolo en [`api.md`](api.md) §4) con suscripción por
tema. El gateway hace *fan-out* desde NATS filtrando por permisos y ACL.

| Pantalla | Qué llega en vivo | Fuente (subjects, ver [`events.md`](events.md)) | Sprint |
| --- | --- | --- | --- |
| Resumen | Contadores por estado, cambios recientes, alertas activas, tráfico total actual | `horus.devices.router.status_changed`, `horus.alerts.alert.*`, agregados de analytics | S3, S5, S9, S11 |
| Lista de routers | Estado y "visto por última vez" de las filas visibles | `horus.devices.router.status_changed` | S3 |
| Detalle de router | Estado, KPIs, interfaces, último valor (`GET /routers/{id}/metrics/live` + eventos) | `horus.devices.router.status_changed`, `horus.snmp.router.*`, `horus.snmp.interface.*` filtrados por router | S5 |
| Monitoreo › Estado de la red | Matriz de estado por sitio/router | idem | S5 |
| WireGuard | Último handshake, bytes, peer conectado/desconectado | `horus.wireguard.peer.*` | S4 |
| Tráfico › Panorama | Throughput actual, top servicios (ventana 5 min) | agregados de analytics / traffic-intelligence | S7 |
| Seguridad › Detecciones | Nuevos hallazgos | `horus.detection.finding.*` | S8 |
| Centro de notificaciones (S5) | Cambios de estado de routers y peers | `horus.devices.router.status_changed`, `horus.wireguard.peer.handshake_*` | S5 |
| Alertas › Activas, centro de notificaciones (S11) | Alta, reconocimiento y resolución | `horus.alerts.alert.*` | S11 |
| Reportes | Progreso de generación | `horus.analytics.report.*` | S12 |
| Sesiones | Revocación de mi sesión → logout | `horus.auth.session.revoked` | S2 |

Los subjects son orientativos: la lista de eventos "UI-visibles" que reenvía el gateway la
define [`events.md`](events.md) (ver C-07 en [`roadmap.md`](roadmap.md)). No se actualizan en
vivo: listas administrativas, auditoría, analítica histórica (botón
"Actualizar" y marca de tiempo).

### 7.2 Frescura de los datos

Todo dato operativo muestra **cuándo se obtuvo**, no cuándo se pintó.

- Etiqueta relativa ("hace 12 s") con valor absoluto y zona en tooltip; se re-renderiza cada
  10 s sin peticiones.
- Tres niveles por tipo de dato, con umbrales derivados del intervalo de sondeo `T` del dato:

| Nivel | Condición | Presentación |
| --- | --- | --- |
| Fresco | edad ≤ 2·T | Normal |
| Atrasado | 2·T < edad ≤ 5·T | Valor en color atenuado + icono de reloj + "Dato de hace 3 min" |
| Obsoleto | edad > 5·T o el backend lo marca `stale` | Valor atenuado y tachado del estado vivo: el estado pasa a "Sin datos recientes" (gris, icono distinto de offline) — **nunca se presenta como offline** un router del que simplemente no sabemos nada |

Esto materializa la respuesta a "¿qué pasa si SNMP deja de funcionar?": la UI distingue *router
caído* de *monitoreo caído*.

### 7.3 Estado de la conexión

Indicador en la navbar, siempre visible:

| Estado | Indicador | Comportamiento |
| --- | --- | --- |
| Conectado | Punto + "En vivo" | — |
| Reconectando | Punto animado (estático con reduced motion) + "Reconectando…" | Backoff exponencial con jitter (1 s → 30 s); las vistas hacen *polling* REST cada 30 s mientras dure |
| Sin conexión | "Sin conexión en vivo · datos de hace 4 min" + botón Reintentar | Polling continúa; banner si > 2 min |
| Sin red | "Sin conexión a Internet" | Pausa polling; reanuda con evento `online` |

Al reconectar, el cliente se re-suscribe y pide el **estado actual** (snapshot REST o
reanudación desde el último ID si [`api.md`](api.md) lo soporta) para no mostrar huecos.

### 7.4 Cómo se muestran los cambios

- Actualización **en el sitio**: sin recargar la tabla, sin mover el scroll, sin reordenar filas
  bajo el cursor (si el orden depende de un valor en vivo, se muestra "Hay 3 cambios de orden ·
  Reordenar").
- Realce breve del valor cambiado (fondo que se desvanece en ~1 s); con `prefers-reduced-motion`
  se sustituye por un cambio de color sin animación (apple-design §14).
- Los números no "cuentan" animadamente: se reemplazan.
- Lector de pantalla: región `aria-live="polite"` con resúmenes agregados y limitados (máx. 1 cada
  30 s, p. ej. "2 routers pasaron a offline"); los eventos críticos (router core caído) usan
  `assertive` solo si el usuario lo activa.
- Toasts solo para eventos que el usuario provocó o alertas críticas; el resto va al centro de
  notificaciones.

### 7.5 Implementación

Composable `useRealtime(topic, handler)` sobre un único cliente WS (plugin Nuxt solo cliente) que
gestiona autenticación, reconexión, re-suscripción y deduplicación por `id` de evento (entrega al
menos una vez). Los handlers actualizan el estado local de la vista; no hay store global salvo el
de conexión. Throttle de renderizado a 1 actualización/segundo por tarjeta en ráfagas.

## 8. Estados

Toda vista con datos implementa los cuatro estados; es parte de la DoD de frontend
([`backlog/README.md`](backlog/README.md) §8).

### 8.1 Carga

- Mostrar algo de inmediato (`HIG loading.md › Best practices`: *"Show something as soon as
  possible"*): `USkeleton` con la forma final; el layout, sidebar y toolbar nunca esperan datos.
- Carga por tarjeta/sección, no de página completa: una consulta lenta de ClickHouse no bloquea
  los KPIs de PostgreSQL (*"Let people do other things … while they wait"*).
- Si una consulta tarda > 3 s, texto "Calculando para 30 días… puede tardar unos segundos" y
  opción de cancelar; operaciones largas (reportes, importaciones) con progreso determinado cuando
  sea posible (`HIG progress-indicators.md › Best practices`: *"When possible, use a determinate
  progress indicator"*, *"When it's feasible, let people halt processing"*).
- Recargas en segundo plano no vuelven a mostrar skeleton: se mantienen los datos con un
  indicador discreto.

### 8.2 Vacío

`UEmpty` con icono, título que describe la situación y **siguiente acción** (`HIG writing.md ›
Best practices`: *"Provide clear next steps on any blank screens"*). Se distinguen:

| Caso | Ejemplo de copia | Acción |
| --- | --- | --- |
| Primera vez | "Todavía no hay routers registrados" | "Registrar router" · "Importar CSV" (si hay permiso; si no: "Pide a un administrador que registre los routers") |
| Sin resultados por filtro | "Ningún router coincide con estos filtros" | "Limpiar filtros" |
| Sin datos en el rango | "Sin tráfico registrado entre 02:00 y 03:00" | "Ampliar a 24 h" |
| Pendiente de configuración | "Este router no exporta flujos" | "Ver cómo configurar NetFlow en MikroTik" |

### 8.3 Error

- Errores **en contexto**, en la tarjeta o campo que falló (`UAlert`/`UError` con Reintentar), no
  en diálogos (`HIG feedback.md › Best practices`: *"Show people when a command can't be carried
  out and help them understand why"*).
- La copia dice qué pasó y qué hacer, sin culpar ni disculparse (`HIG writing.md › Best
  practices`: *"Write clear error messages"*): "No se pudo guardar: ya existe un router con la IP
  10.20.0.1. Usa otra IP o edita el existente."
- Detalle técnico plegado: código de error y `trace_id` copiable para soporte.
- 401 → refresh/relogin transparente; 403 → mensaje de permiso; 404 → página "no encontrado" en el
  layout; 409 → error de campo; 422 → errores por campo; 429 → "Demasiadas solicitudes, espera N
  s"; 5xx/503 → estado de error o degradado (§8.4).

### 8.4 Degradado

Una dependencia caída **no debe tumbar la aplicación entera**; la UI muestra exactamente qué está
afectado. Fuente: `GET /api/v1/system/status` (contrato en [`api.md`](api.md)) consultado al
cargar y cada 30 s, más el evento WS correspondiente; mapa de capacidades
`{ analytics: 'degraded', realtime: 'ok', ... }` en el composable `useSystemStatus()`.

Presentación:

- **Banner global** (`UBanner`, warning) cuando la degradación afecta a varias secciones.
- **Aviso en la sección** (`UAlert` en lugar del contenido afectado) con lo que no funciona, desde
  cuándo y **lo que sí sigue funcionando**.
- Las acciones que dependen del componente caído se **deshabilitan con explicación** (§9).
- Para usuarios con `settings.read` se muestra el nombre técnico (ClickHouse, NATS); para el resto,
  el nombre funcional (`HIG writing.md`: los nombres vienen de lo que la gente reconoce, no de
  cómo está construido el sistema).

Matriz (alineada con los modos de fallo de `vision.md` §9 S0/S14; la respuesta técnica
definitiva vive en [`architecture.md`](architecture.md) y [`disaster-recovery.md`](disaster-recovery.md)):

| Falla | Qué ve el usuario | Sigue funcionando |
| --- | --- | --- |
| **ClickHouse caído** (API: `503` con `code: ANALYTICS_UNAVAILABLE`) | Banner: "Analítica no disponible: el almacén analítico no responde desde 10:42. Inventario, WireGuard, monitoreo y administración siguen operativos. Los flujos se siguen recibiendo y se procesarán al recuperarse." Tráfico, Analítica, Reportes, pestaña Tráfico y **gráficas históricas de CPU/interfaces** (si C-03 lleva las series SNMP a ClickHouse) muestran el aviso en lugar de gráficos; KPIs de tráfico del Resumen muestran "No disponible" | Login, inventario, WG, **estado de routers y último valor SNMP en vivo** (no dependen de ClickHouse, [`architecture.md`](architecture.md) §10.1), administración |
| **SNMP (servicio) caído** | Aviso en Monitoreo y detalle de router: "El monitoreo no reporta desde 10:42. Las métricas mostradas son de esa hora." Estados pasan a `stale` "Sin datos recientes" (§7.2), **no** a offline (principio P4 de [`architecture.md`](architecture.md): ausencia de datos ≠ dato cero). ICMP vive en el mismo servicio, así que tampoco hay alcanzabilidad | Todo lo demás |
| **NATS reiniciando** | Indicador "Reconectando…"; banner si > 1 min: "Las actualizaciones en vivo están en pausa. Los datos se actualizan cada 30 s." | Todo vía REST/polling; los colectores reintentan y nada se pierde (según [`events.md`](events.md)) |
| **Un router desaparece** | Router → Offline (icono + rojo) tras la histéresis, **con la razón** en lenguaje claro: `tunnel_down` → "Sin túnel ni respuesta: posible corte de enlace o energía en el sitio"; `host_unreachable_via_tunnel` → "El túnel está activo pero el router no responde". Si solo falla SNMP → `degraded` "Responde a ping pero no a SNMP: revisa credenciales o ACL". Entrada en cambios recientes y centro de notificaciones; detalle muestra "Sin respuesta desde 10:42 · último dato conocido"; sus gráficas muestran **hueco**, no ceros | Resto de la red |
| **NAS / MinIO no responde** | Reportes: "No se pueden generar ni descargar reportes ahora: el almacenamiento de archivos no responde." Admin › Retención: archivado en pausa | Todo lo operativo |
| **Redis caído** | Según [`security.md`](security.md): si sesiones dependen de Redis, posible relogin; rate limiting degradado. La UI muestra el error de login normal | Depende del diseño de sesiones |
| **PostgreSQL caído** | Pantalla de mantenimiento a nivel app: "Horus Flow no está disponible. Estamos restableciendo el servicio." (es la dependencia central) | Colectores siguen recibiendo y encolando (no visible) |
| **api-gateway caído** | Pantalla "No se puede conectar con Horus Flow" con reintento automático y último estado conocido en caché local de solo lectura (opcional, S14) | — |

## 9. Permisos en la UI

La UI **refleja** la autorización; quien la **aplica** es el backend (toda acción oculta o
deshabilitada sigue protegida por 403).

**Regla: ocultar por permiso, deshabilitar por estado.**

| Situación | Tratamiento | Ejemplo |
| --- | --- | --- |
| Sin permiso de lectura de una sección | Se **oculta** del sidebar, búsqueda y enlaces cruzados (el enlace se muestra como texto plano) | Técnico sin `security.read` no ve "Seguridad" |
| Sin permiso para una acción sobre un recurso que sí ve | Se **oculta** la acción | Solo lectura no ve "Editar" ni "Eliminar" |
| Recurso fuera de su ACL | No aparece en listas; URL directa → 404 | Router de otro sitio |
| Tiene permiso, pero la acción no es posible **por estado** | Se **deshabilita** con tooltip/razón visible | "Rotar claves" deshabilitado: "El peer está revocado"; "Exportar" deshabilitado: "Reportes no disponibles: almacenamiento sin respuesta" |
| Acción que la persona probablemente espera y cuya ausencia confundiría | Excepción: se **muestra deshabilitada** con la razón y a quién pedir acceso | "Exportar CSV" para quien tiene `reports.read` pero no `reports.export` |

Motivos: ocultar reduce ruido y no revela capacidades (Simplicidad, apple-design §16);
deshabilitar sin razón es un callejón sin salida, por eso **todo** control deshabilitado explica
por qué (`HIG feedback.md`: *"help them understand why"*). Los controles deshabilitados siguen
siendo enfocables (`aria-disabled` en lugar de `disabled`) para que el lector de pantalla lea la
razón.

Implementación: `usePermission()` y `useCan(resource, action, target?)` sobre los permisos de
`GET /api/v1/me`; `definePageMeta({ permission: 'devices.read' })` con middleware; los
permisos se refrescan al recibir 403 o el evento de cambio de roles.

## 10. Principios visuales

### 10.1 Materiales, profundidad y movimiento

- **Sidebar y navbar como capa funcional**, el contenido debajo. La navbar puede usar un material
  translúcido (`backdrop-filter`) con el contenido desplazándose debajo y un desvanecido en el
  borde en lugar de una línea dura (apple-design §12 › Materials & depth, "Scroll edge effects,
  not hard dividers"). **Nunca** material translúcido sobre otro translúcido, ni dentro del
  contenido: tarjetas, tablas y gráficos son sólidos (`apple-hig/SKILL.md › Lens 2 › Both`:
  blur solo en la capa funcional flotante). Con `prefers-reduced-transparency` → superficies
  sólidas (apple-design §14).
- **Modales vs paneles**: tarea modal (confirmación, alta) → `UModal`/`USlideover` con scrim;
  panel paralelo que no bloquea (detalle de interfaz mientras miro la tabla) → slideover sin scrim
  (apple-design §12 › "Dim to focus, separate to keep flow").
- **Movimiento** breve, con propósito e interrumpible: resortes críticamente amortiguados
  (`bounce: 0`, ~0,3–0,4 s) para paneles y menús; nada de rebote salvo gestos con inercia (cajón
  móvil arrastrado) (apple-design §4, Quick Reference). Paneles y popovers salen del elemento que
  los abrió y vuelven por el mismo camino (apple-design §7). Ninguna transición bloquea la entrada
  (apple-design §3). Con `prefers-reduced-motion` → fundidos cortos (apple-design §14).
- **Respuesta inmediata**: feedback en pointer-down, sin esperas artificiales (apple-design §1).

### 10.2 Color

Colores semánticos de Nuxt UI (`primary`, `neutral`, `success`, `info`, `warning`, `error`) y
utilidades semánticas (`text-default`, `bg-elevated`, `border-muted`), nunca colores crudos de la
paleta de Tailwind en componentes.

**Un color, un significado** (`HIG color.md › Best practices`: *"Avoid using the same color to mean
different things"*) y **nunca solo color** (`HIG accessibility.md › Vision`: *"Convey information
with more than color alone"*):

| Estado | Color semántico | Icono (Lucide) | Texto | Significado |
| --- | --- | --- | --- | --- |
| `online` | `success` | `i-lucide-circle-check` | Online | Todas las señales OK |
| `warning` | `warning` | `i-lucide-triangle-alert` | Advertencia | Umbral de CPU/temp/errores superado |
| `degraded` | `warning` | `i-lucide-circle-alert` | Degradado | Responde a ICMP pero no a SNMP (`snmp_unreachable`) |
| `critical` | `error` | `i-lucide-octagon-alert` | Crítico | Umbral crítico superado |
| `offline` | `error` | `i-lucide-unplug` | Offline | Sin respuesta (razón: `tunnel_down`, `host_unreachable_via_tunnel`) |
| `stale` | `neutral` | `i-lucide-clock-alert` | Sin datos recientes | El monitoreo no reporta; **no** implica caída |
| `unknown` | `neutral` | `i-lucide-circle-dashed` | Desconocido | Aún no sondeado |
| `maintenance` | `info` | `i-lucide-wrench` | Mantenimiento | Ventana declarada; no genera alertas |

Enum consolidado de [`architecture.md`](architecture.md) §10.4 y [`api.md`](api.md)
(`status-summary`); su unificación formal está pendiente (C-06 en [`roadmap.md`](roadmap.md)).
`warning`/`degraded` y `critical`/`offline` comparten color porque comparten urgencia; se
distinguen siempre por icono y texto.

`primary` (color de marca, a definir con el PO) se reserva para acciones primarias y selección;
nunca para estado. Ningún color de estado se usa como color de serie en gráficos.

### 10.3 Tipografía

- Fuente del sistema (`system-ui`) para interfaz (apple-design §15: *"Default to the platform's
  system font"*), con una monoespaciada para IPs, MAC, claves públicas, OIDs y `trace_id`.
- Cifras tabulares (`font-variant-numeric: tabular-nums`) en tablas, KPIs y ejes — criterio propio
  para que las columnas de números no "bailen" con las actualizaciones en vivo.
- Jerarquía por peso + tamaño + interlineado; tracking negativo solo en títulos grandes, cuerpo a
  0 (apple-design §15). Tamaños en `rem` para respetar el zoom del navegador.
- Cuerpo ≥ 14 px en tablas densas, 16 px en formularios y texto corrido.

### 10.4 Iconografía y escritura

- Un único set (Lucide, `i-lucide-*`), mismo trazo, tamaño acorde al texto adyacente; todo botón
  solo-icono tiene `aria-label` y tooltip.
- Español neutro, sentence case en botones y títulos ("Registrar router"), aplicado de forma
  consistente (`HIG writing.md › Best practices`: *"Adopt capitalization rules … then apply them
  consistently"*). Términos técnicos del dominio (peer, handshake, ASN, NetFlow) se mantienen en
  su forma habitual en el sector; glosario en i18n.
- Unidades: bits por segundo para tasas (Mbps/Gbps), bytes para volumen (GB/TB), SI decimal;
  fechas `dd/mm/aaaa HH:mm` según locale; tiempos relativos para frescura, absolutos en tablas de
  auditoría.

## 11. Accesibilidad (WCAG 2.1 AA)

Objetivo: **WCAG 2.1 nivel AA** en todas las pantallas, verificado en CI (axe en Playwright) y
manualmente en cada review.

| Criterio | Aplicación |
| --- | --- |
| 1.1.1 Contenido no textual | Iconos con `aria-label`; cada gráfico tiene título, descripción de una frase con el hallazgo principal y **alternativa en tabla** ("Ver datos") (`HIG charts.md › Enhancing the accessibility of a chart`; `HIG charting-data.md`: *"Make every chart in your app accessible"*). ECharts con `aria.enabled` y *decals* (patrones) en series |
| 1.3.1 Información y relaciones | Tablas con `<th scope>`; formularios con `<label>` asociado; landmarks (`nav`, `main`, `header`); encabezados jerárquicos |
| 1.4.1 Uso del color | Estado = icono + texto + color (§10.2); series distinguibles por patrón/forma además de color (`HIG charts.md › Color`: *"Avoid relying solely on color"*) |
| 1.4.3 / 1.4.11 Contraste | Texto ≥ 4,5:1 (≥ 3:1 en ≥ 18,66 px negrita / 24 px); componentes, bordes de campos, foco y elementos gráficos significativos ≥ 3:1, en claro **y** oscuro. Tokens verificados con script en CI |
| 1.4.4 / 1.4.10 Redimensionado y reflow | Zoom 200 % sin pérdida; 320 px sin scroll horizontal de página (§5.2) |
| 1.4.13 Contenido en hover/foco | Tooltips descartables con Esc, persistentes al pasar el puntero sobre ellos (comportamiento de `UTooltip`) |
| 2.1.1 Teclado | Todo operable con teclado: tablas (flechas en filas, Enter abre detalle), gráficos navegables (`HIG charts.md`: *"Make an interactive chart easy to navigate when using keyboard commands"*), ⌘K, atajos documentados y sin conflicto con lectores de pantalla (`HIG accessibility.md › Mobility`: *"Let people use the keyboard alone"*) |
| 2.2.1 Tiempo ajustable | Aviso 2 min antes de expirar la sesión con "Seguir conectado"; los toasts con acción (Deshacer) se pausan con foco/hover |
| 2.2.2 Pausar | Modo mural y actualizaciones en vivo con control de pausa; ningún parpadeo |
| 2.3.1 Destellos | Nada parpadea más de 3 veces por segundo; las alertas críticas no parpadean (`HIG accessibility.md`: *"Be cautious with fast-moving and blinking animations"*) |
| 2.4.1 / 2.4.3 / 2.4.7 | Enlace "Saltar al contenido"; orden de foco lógico; foco visible ≥ 2 px con contraste ≥ 3:1; el foco vuelve al disparador al cerrar overlays |
| 2.4.2 / 2.4.6 | `<title>` por página ("rt-norte-01 · Routers · Horus Flow"); encabezados y etiquetas descriptivos |
| 2.5.3 Etiqueta en el nombre | El nombre accesible contiene el texto visible |
| 3.1.1 Idioma | `lang="es"` (o el locale activo) |
| 3.3.1–3.3.3 Errores | Errores asociados al campo (`aria-describedby`), descritos en texto, con sugerencia |
| 4.1.3 Mensajes de estado | Toasts y actualizaciones en vivo en regiones `aria-live` con la política de §7.4 |

Además: objetivos táctiles ≥ 24 × 24 px (≥ 44 px en controles principales en móvil, `HIG
accessibility.md › Mobility`: *"Offer sufficiently sized controls"*), soporte de
`prefers-reduced-motion`, `prefers-reduced-transparency`, `prefers-contrast: more` (bordes
definidos y fondos sólidos, apple-design §14).

## 12. Componentes Nuxt UI por patrón

| Patrón | Componentes |
| --- | --- |
| App y layout | `UApp`, `UDashboardGroup`, `UDashboardSidebar`, `UDashboardSidebarCollapse`, `UDashboardSidebarToggle`, `UDashboardPanel`, `UDashboardNavbar`, `UDashboardToolbar`, `UDashboardResizeHandle` |
| Navegación | `UNavigationMenu` (vertical en sidebar), `UBreadcrumb`, `UTabs`, `ULink` |
| Búsqueda global | `UDashboardSearch`, `UDashboardSearchButton`, `UCommandPalette`, `UKbd` |
| Menú de usuario | `UUser`, `UAvatar`, `UDropdownMenu`, `UColorModeSelect` / `UColorModeButton` |
| Login y 2FA | `UAuthForm` (o `UForm` + `UFormField` + `UInput`), `UPinInput`, `UStepper` (enrolamiento), `UAlert` |
| Listas/tablas | `UTable`, `UPagination`, `UInput` (búsqueda), `USelectMenu` (filtros múltiples), `UInputDate`/`UCalendar` + `UPopover` (rango), `UBadge` (filtros activos, estado), `UCheckbox`, `UDropdownMenu` (acciones por fila), `UContextMenu`, `UTooltip` |
| Detalle de entidad | `UTabs`, `UBreadcrumb`, `UCard`, `UBadge`, `UTimeline` (actividad), `USeparator`, `UCollapsible` |
| Dashboards | `UCard`/`UPageCard` + `UPageGrid` (rejilla), `vue-echarts` (ECharts) dentro de `UCard`, `USkeleton`, `UProgress` (uso de CPU/RAM), `UTabs` (rangos) o `UFieldGroup` de `UButton` |
| Formularios | `UForm`, `UFormField`, `UInput`, `UInputNumber`, `UInputTags`, `USelect`, `USelectMenu`, `UInputMenu`, `UTextarea`, `USwitch`, `UCheckboxGroup`, `URadioGroup`, `UFileUpload` (CSV), `UStepper` (asistentes) |
| Overlays | `USlideover` (alta/edición, detalle paralelo), `UModal` (confirmaciones), `UDrawer` (acciones en móvil), `UPopover` |
| Feedback | `UToast` (`useToast`), `UAlert`, `UBanner` (degradación global), `UChip` (contador de notificaciones), `UProgress` |
| Estados | `USkeleton` (carga), `UEmpty` (vacío), `UError` / `UAlert` (error), `UBanner` + `UAlert` (degradado) |
| Jerarquías | `UTree` (sitios), `UAccordion` |
| Secretos | `UInput` tipo password con botón mostrar, `UButton` "Copiar" con feedback, `UAlert` de advertencia "solo se muestra una vez" |

## 13. Arquitectura técnica del frontend

```
apps/frontend/
├── app/
│   ├── layouts/        default.vue (dashboard), auth.vue (login), wall.vue (modo mural)
│   ├── pages/          rutas de §3.1
│   ├── components/
│   │   ├── status/     StatusBadge, FreshnessLabel, ConnectionIndicator
│   │   ├── charts/     TimeSeriesChart, TopNBar, DonutChart, Heatmap, Sparkline, ChartCard (con tabla alternativa)
│   │   ├── data/       DataTable (envoltorio de UTable con filtros/URL), FilterBar
│   │   └── states/     LoadingState, EmptyState, ErrorState, DegradedNotice
│   ├── composables/    useApi, useAuth, usePermission, useRealtime, useSystemStatus, useTimeRange, useUrlState
│   ├── middleware/     auth.global.ts, permission.ts
│   ├── plugins/        realtime.client.ts, otel.client.ts, echarts.client.ts
│   └── app.config.ts   tema Nuxt UI (colores semánticos, tokens de estado)
├── i18n/locales/       es.json (+ en.json según P-08)
├── types/api/          generados desde OpenAPI
└── tests/              unit (Vitest), e2e (Playwright + axe)
```

- **Datos**: `useFetch`/`$fetch` envueltos en `useApi` (refresh de token, errores tipados,
  `traceparent`). Sin Pinia mientras el estado compartido se limite a sesión, permisos, estado del
  sistema y conexión (composables con `useState`); Pinia se introduce solo si aparece estado
  compartido complejo entre páginas (`vision.md` §1).
- **Renderizado**: SPA autenticada (`ssr: false` en rutas de la app) servida estáticamente por el
  reverse proxy — no hay contenido público que indexar y simplifica el manejo de tokens. A
  confirmar en ADR (Agente 1).
- **i18n**: `@nuxtjs/i18n` desde S1, español por defecto; todas las cadenas en archivos de locale.
- **Fechas**: el API entrega UTC ISO 8601; formateo con `Intl` en la zona del usuario.
- **Observabilidad**: OpenTelemetry web para propagar `traceparent` y medir Web Vitals; errores de
  cliente enviados al colector (sin datos personales).
- **Presupuestos de rendimiento** (verificados en S15): LCP < 2,5 s en Resumen con 1 000 routers;
  tabla de 100 filas con actualizaciones en vivo a 60 fps; bundle inicial < 250 KB gzip (ECharts
  cargado bajo demanda).

## 14. Dependencias con otros documentos

| Necesito de | Qué | Estado |
| --- | --- | --- |
| [`api.md`](api.md) (Agente 3) | Formato de error `problem+json`, paginación por cursor, filtros, `GET /api/v1/me` con permisos efectivos, tickets y protocolo WS, `GET /routers/status-summary`, `meta.partial` | Alineado. **Falta** `GET /api/v1/system/status` agregado para §8.4 (C-08) |
| [`events.md`](events.md) (Agente 3) | Lista de eventos UI-visibles para §7.1 | Alineado con `horus.devices.router.status_changed`; ver C-07 |
| [`security.md`](security.md) (Agente 4) | Catálogo de permisos (§3.2 usa el de §6.1), almacenamiento de tokens en navegador, CSP compatible con ECharts | Alineado; falta `customers.*` (C-09) |
| [`architecture.md`](architecture.md) §10 | Matriz de §8.4 y modelo de estado | Alineado; enum de estado pendiente de unificar con `api.md` (C-06) |
| [`database.md`](database.md) (Agente 2) | Dónde viven las series SNMP (afecta a §8.4, fila ClickHouse) | C-03 pendiente del PO |
