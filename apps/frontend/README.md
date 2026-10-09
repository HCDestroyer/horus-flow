# apps/frontend — interfaz web (Nuxt 4 + Nuxt UI)

- **Propósito:** UI multi-ISP: shell, selector de ISP, marco de widgets, vistas, dashboards y modo
  kiosco/NOC.
- **Dueño:** UI — [`docs/backlog/team.md`](../../docs/backlog/team.md) §2.
- **Estado:** I0-14 (shell, login con TOTP, tema, i18n, estados), **I0-15** (cliente generado del
  OpenAPI, token por ISP, selector de ISP, API simulada del contrato) e **I0-16** (marco de
  widgets v0 con las plantillas "NOC del ISP" y "Seguridad", vista mural).
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
- Cliente WebSocket real (ticket, reconexión, re-suscripción) e indicador de conexión: I1.
- Kiosco (`/kiosk`, credencial de dispositivo, rotación, wake lock): I1.
- CI: el job `frontend` no ejecuta Playwright; `e2e --grep @tenant` y `@widgets` se corren a mano.

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
  `tests/unit/mock-findings.test.ts`.
- **D18:** el estado `infected` se muestra como "Infectado", siempre con su confianza.
- **Marca provisional** (P-20 abierta): `primary` teal, neutros zinc.
