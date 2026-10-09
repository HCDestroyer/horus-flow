# apps/frontend — interfaz web (Nuxt 4 + Nuxt UI)

- **Propósito:** UI multi-ISP: shell, selector de ISP, marco de widgets, vistas, dashboards y modo
  kiosco/NOC.
- **Dueño:** UI — [`docs/backlog/team.md`](../../docs/backlog/team.md) §2.
- **Estado:** I0-14 (shell, login con TOTP, tema, i18n, estados), **I0-15** (cliente generado del
  OpenAPI, token por ISP, selector de ISP, API simulada del contrato) e **I0-16** (marco de
  widgets v0 con las plantillas "NOC del ISP" y "Seguridad", vista mural). **I1:** Clientes por IP
  (I1-16), Tráfico (I1-17), Hallazgos con evidencia y acciones recomendadas (I1-18), Nodos,
  routers, onboarding y prefijos (I1-19), widgets `top_services`/`top_organizations` (I1-20),
  kiosco `/kiosk` y Pantallas NOC (I1-21), consola de plataforma y fuentes de reputación
  (I1-31, D19, D20), canales de notificación por ISP (D13, D17) e imagen `horus-web`.
- **Documentación:** [`docs/frontend.md`](../../docs/frontend.md),
  [`docs/conventions.md`](../../docs/conventions.md) §3,
  [ADR-0012](../../docs/adr/0012-nuxt4-nuxt-ui.md), API en [`docs/api.md`](../../docs/api.md).

## Uso

Requisitos: Node ≥ 22.12 y pnpm 10 (`corepack enable`).

```sh
pnpm install            # instala y ejecuta `nuxt prepare`
pnpm dev                # contra el gateway (apiBase /api/v1)
pnpm dev:mocks          # HORUS_UI_MOCKS=1: API simulada generada del contrato
pnpm api:generate       # regenera types/api/* desde packages/schemas (C5, C9)
pnpm lint               # api:check + ESLint + prettier --check
pnpm typecheck          # vue-tsc (strict); valida también los manifiestos de widget
pnpm test               # Vitest (tests/unit); `pnpm test widgets` = marco de widgets
pnpm build              # nuxt generate contra el gateway → .output/public
pnpm build:mocks        # igual, con la API simulada (demos y e2e)
pnpm e2e --grep @tenant # Playwright (construye con build:mocks si no hay E2E_NO_BUILD=1)
pnpm e2e --grep @widgets
pnpm e2e --grep @layout # sin desbordamiento: mural 720p–4K, escritorio y móvil 320–430 px
pnpm e2e --grep @tour   # recorrido con capturas en docs/screenshots/tour/
pnpm e2e --grep "@clients|@traffic|@security|@onboarding|@platform|@kiosk"   # I1
pnpm screenshots        # capturas en docs/screenshots/
```

`HORUS_UI_MOCKS=1` decide en la build si la SPA usa la API simulada (I0-15, criterio 4): en una
SPA generada el valor queda fijado, por eso hay `build:mocks`. Con `E2E_NO_BUILD=1` los e2e
reutilizan `.output/public`, que debe venir de `pnpm build:mocks`.

Playwright usa el Chromium de `PLAYWRIGHT_BROWSERS_PATH`; `@playwright/test` está fijado a la
versión de ese navegador (1.56.1 ↔ chromium-1194). No se ejecuta `playwright install`.

### Cuentas de la API simulada

| Correo                        | Contraseña        | 2FA      | Qué ve                                                                                            |
| ----------------------------- | ----------------- | -------- | ------------------------------------------------------------------------------------------------- |
| `ana.ruiz@fibranorte.example` | `horus-demo-2026` | `123456` | `tenant_admin` de Fibra Norte y Valle Conecta, `security_analyst` de Red Andina, `platform_admin` |
| `noc@fibranorte.example`      | `horus-demo-2026` | —        | rol `noc` de Fibra Norte (sin Clientes, sin `top_customers`, IP enmascaradas)                     |

Escenarios: `localStorage['horus.mock.scenario']` = `degraded` (analítica caída, un exportador
silencioso) · `system-error` · `widget-error` (dos widgets fallan una vez) · `empty`.
`localStorage['horus.mock.tokenTtlMs']` acorta la vida del token para probar el refresh.

Interruptores de I1 (todos en `localStorage`, solo leídos por `mocks/`):
`horus.mock.findingEveryMs` (hallazgo nuevo por tiempo real; 45 s por defecto, `0` = nunca) ·
`horus.mock.onboardingStepMs` (paso del alta simulada del router, 6 s) ·
`horus.mock.enrollTtlMs` (vida del token de enrolamiento) · `horus.mock.offline` = `1` (corte de
red para el kiosco) · `horus.mock.frontendVersion` (versión mínima que pide el kiosco) ·
`horus.mock.accessMode` = `domain` | `subdomain` | `ip_only` (D19) · `horus.mock.diskRatio`
(uso del disco local, aviso desde 0,85). Datos de demo: Fibra Norte tiene el Nodo Costa en
modo descubrimiento y el router `rt-lago` pendiente de configurar; Valle Conecta, `rt-ribera`
silencioso; en el ISP hay clientes IPv6 por prefijo delegado (D22).

### Imagen `horus-web` (producción)

`Dockerfile` (contexto: la raíz del repo; `Dockerfile.dockerignore` limita el contexto) genera
la SPA con `pnpm build` y la sirve con `scripts/serve-static.mjs` sobre distroless Node, como
`nonroot`, puerto 8081, sistema de archivos de solo lectura y `/healthz`. Fallback de SPA,
caché inmutable para `/_nuxt/*`, CSP de `security.md` con los scripts en línea de Nuxt
permitidos por hash. Traefik debe enrutar a `horus-web:8081` todo lo que no sea `/api` ni
`/ws` (regla de menor prioridad que la del gateway).

## Cliente de API (I0-15)

- `scripts/generate-api.mjs` genera, de forma reproducible (mismo contrato ⇒ mismos bytes):
  `types/api/schema.d.ts` (openapi-typescript desde `packages/schemas/openapi/dist/horus-api.v0.yaml`),
  `types/api/widget-catalog.ts` (catálogo C9 como constante) y copias de plantillas y ejemplos
  en `types/api/contract/`. `pnpm lint` falla si están desactualizados. Alias legibles en
  `types/api/index.ts`.
- `$api` = `openapi-fetch` tipado (`unwrap($api.GET('/me'))`). `createAuthFetch` pone el token
  según la ruta (sesión para `/me` y `/auth/*`, ISP para el resto), la cabecera anti-CSRF y
  hace un único refresh ante 401 con reintento transparente; serializado **entre pestañas** con
  Web Locks (el refresh rota y su reutilización revoca la sesión) y cierre de sesión propagado
  por `BroadcastChannel`. Tokens solo en memoria.
- Al entrar en un ISP (`middleware/tenant.global.ts`): `POST /auth/token {tenant_id}`; si cambia,
  `resetTenantData` vacía `useAsyncData`, la caché por ISP y las suscripciones de tiempo real.
- Selector (`components/tenant/TenantSwitcher.vue`): solo con más de un ISP, búsqueda con más de
  7, también en ⌘K; conserva la sección (`tenantSwitchPath`) y recuerda el último ISP.
- API simulada (`mocks/server.ts`): un `fetch` tipado con el contrato que reproduce tokens por
  ámbito, `TENANT_NOT_FOUND`, `TOKEN_SCOPE_INVALID`, `WIDGET_TYPE_NOT_ALLOWED`, enmascarado de
  IP y datos de widget (`mocks/widget-data.ts`, deterministas por ISP); `mocks/realtime.ts`
  emite `traffic.summary` (C6).

## Marco de widgets (I0-16)

- `app/widgets/<type>/{manifest.ts, Widget.vue}`; registro por convención de carpeta
  (`app/widgets/registry.ts`, componentes bajo demanda: ECharts no entra en el bundle inicial).
  `_example/` es la plantilla documentada para crear uno nuevo.
- El manifiesto (C9 `presentation-manifest`) se comprueba en **typecheck** (tipo del catálogo y
  tamaños permitidos) y en el **build** (`modules/widget-manifests.ts`): uno inválido rompe
  `nuxt generate`.
- `DashboardGrid`: 12 columnas con `row_height_px`, adaptación §5.2 por ancho del contenedor,
  vista mural (`?scale=wall`) a pantalla completa: el diseño de 1920×1080 se escala de forma
  uniforme con `--u` = min(alto/1080, ancho/1920) (KPI 72 px, títulos 28, etiquetas 22, nada
  < 20 a 1080p); si algo no cabe, `v-fit-optional` oculta primero lo secundario
  (`data-fit-optional`) y `v-fit-rows` las filas que no caben enteras. Los huecos de las
  plantillas se cierran estirando widgets (`fillGaps`); en una o dos columnas las filas crecen
  con el contenido. `WidgetHost`: carga, vacío, error con "Reintentar",
  sin permiso, degradado (503), datos parciales y frescura (fresco/atrasado/obsoleto).
- Widgets: `noc_header`, `traffic_now` (en vivo), `customers_active`, `findings_summary`,
  `botnet_signals`, `exporters_status`, `traffic_timeseries`, `top_categories`, `top_customers`,
  `findings_feed`, `findings_trend`, `security_by_node`, `watched_ports`.
- Paleta de gráficos validada con el validador de la skill dataviz (`utils/chart-palette.ts`).
- Galería: `/t/:slug/dev/widgets` (escala normal y `?scale=wall`).

## Pendientes

- **Claves de `data` por tipo de widget:** C9 fija el sobre pero no los `values`/columnas de cada
  tipo; la lectura del frontend está en `app/widgets/shapes.ts` y debe acordarse con `analytics`.
- Cliente WebSocket real (ticket, reconexión, re-suscripción) e indicador de conexión: los temas
  `security` y `traffic.summary` llegan hoy de la fuente simulada; el onboarding sondea cada 3 s.
- Kiosco: banda de hallazgo crítico (`critical_finding_banner`) y QR del código (hoy enlace con
  el código en el fragmento) sin implementar.
- Diferencias con el contrato (detalle en el resumen de la historia): `Customer` sin la confianza
  del estado de seguridad (la ficha la toma de sus hallazgos); el rol `viewer` (= `isp_viewer`)
  no tiene `customers.read`, así que no puede abrir fichas (I1-16 criterio 6 se prueba con un rol
  con lectura y sin `customers.kind.write`); sin endpoint de uso de disco por tipo de dato ni de
  versión del binario para la consola de plataforma; `/analytics/traffic/top` sin dimensión de
  servicios con etiqueta de servicio en la vista (se usa la vista previa de widgets).
- CI: el job `frontend` no ejecuta Playwright; los e2e se corren a mano.

## Decisiones

- **SPA** (`ssr: false`) generada con `nuxt generate`; iconos Lucide empaquetados y sin fuentes
  remotas (CSP `connect-src 'self'`).
- **Sesión:** access token en memoria (`useState`); refresh con cookie HttpOnly y
  `X-Requested-With: horus`; sin Pinia. La API simulada guarda su "cookie" en `sessionStorage`
  solo dentro de `mocks/`.
- **Navegación:** fuente única en `app/utils/navigation.ts`; aparición progresiva por incremento
  (`NUXT_PUBLIC_NAV_INCREMENT`, fijado en la build; por defecto **I1**): las secciones de
  incrementos posteriores no aparecen y su URL da "No encontrado"; secciones **ocultas** sin
  permiso.
- **API simulada coherente:** todos los widgets de seguridad (resumen, feed, tendencia, por nodo,
  señales) salen de una base de hallazgos abiertos (`openFindings`); lo garantiza
  `tests/unit/mock-findings.test.ts`. En I1 la base se amplía en `mocks/base.ts` (identidad de
  clientes y hallazgos) y `mocks/inventory.ts` (nodos, routers, túneles, prefijos, clientes):
  widgets, listas, fichas y resumen de seguridad salen de ahí (`tests/unit/mock-i1.test.ts`).
- **D11:** los hallazgos muestran acciones recomendadas con comandos RouterOS para copiar y su
  comando para deshacer; no existe ningún botón que actúe sobre el router.
- **Kiosco:** el JWT de kiosco ocupa el mismo hueco en memoria que el token de ISP; la
  credencial de dispositivo es una cookie HttpOnly del gateway (en la API simulada,
  `localStorage` dentro de `mocks/`). La consola de plataforma da "No encontrado" (no 403) a
  quien no es superadmin.
- **D18:** el estado `infected` se muestra como "Infectado", siempre con su confianza; en la
  ficha del cliente, además, con las razones de los hallazgos que lo sostienen.
- **Marca provisional** (P-20 abierta): `primary` teal, neutros zinc.
