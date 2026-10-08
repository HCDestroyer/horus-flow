# apps/frontend — interfaz web (Nuxt 4 + Nuxt UI)

- **Propósito:** UI multi-ISP: shell, selector de ISP, marco de widgets, vistas, dashboards y modo
  kiosco/NOC.
- **Dueño:** UI — [`docs/backlog/team.md`](../../docs/backlog/team.md) §2.
- **Estado (I0-14):** proyecto Nuxt (SPA), login con paso de TOTP, layout principal con la
  navegación del I1, tema Sistema/Claro/Oscuro, i18n `es`, estados de carga/vacío/error y cliente
  de API contra una **API simulada** en memoria. La API real, el cliente generado del OpenAPI y el
  selector de ISP llegan en **I0-15**.
- **Documentación:** [`docs/frontend.md`](../../docs/frontend.md),
  [`docs/conventions.md`](../../docs/conventions.md) §3,
  [ADR-0012](../../docs/adr/0012-nuxt4-nuxt-ui.md), API en [`docs/api.md`](../../docs/api.md).

## Uso

Requisitos: Node ≥ 22.12 y pnpm 10 (`corepack enable`).

```sh
pnpm install            # instala y ejecuta `nuxt prepare`
pnpm dev                # http://localhost:3000 con la API simulada
pnpm lint               # ESLint (@nuxt/eslint + prettier) y prettier --check
pnpm typecheck          # vue-tsc (strict)
pnpm test               # Vitest (tests/unit)
pnpm build              # nuxt generate → .output/public (estáticos para Traefik)
pnpm e2e --grep @i0     # Playwright contra la build (la genera si no existe E2E_NO_BUILD=1)
pnpm screenshots        # capturas claro/oscuro en docs/screenshots/
```

Playwright usa el Chromium de `PLAYWRIGHT_BROWSERS_PATH`; `@playwright/test` está fijado a la
versión que corresponde a ese navegador (1.56.1 ↔ chromium-1194). No se ejecuta
`playwright install`.

### Cuentas de la API simulada

| Correo                        | Contraseña        | 2FA (TOTP) | Qué ve                                           |
| ----------------------------- | ----------------- | ---------- | ------------------------------------------------ |
| `ana.ruiz@fibranorte.example` | `horus-demo-2026` | `123456`   | `tenant_admin` de 2 ISP + `platform_admin`       |
| `noc@fibranorte.example`      | `horus-demo-2026` | —          | rol `noc` de Fibra Norte (sin Clientes ni admin) |

Escenarios: `localStorage['horus.mock.scenario'] = 'degraded' | 'system-error'`. La API simulada
solo se carga si `NUXT_PUBLIC_API_MOCK` no es `false` (va en un chunk aparte).

## Estructura

```
app/
├── app.vue · app.config.ts · error.vue
├── assets/css/main.css      tokens de tema (contraste AA en claro/oscuro, reduced-*)
├── layouts/                 default (shell) · auth (login)
├── pages/                   login · index (elige ISP) · t/[slug]/ (Resumen + secciones)
│                            platform/[...section] · account
├── components/              shell/ (AppPage, UserMenu, AppLogo, SectionPage) · tenant/
│                            states/ (Loading/Empty/ErrorState) · status/
├── composables/             useAuth (sesión, token en memoria) · useTenant · useNavigation
├── middleware/              auth.global (refresh al cargar) · tenant.global (404 si no es miembro)
├── plugins/api.ts           cliente único: Bearer en memoria, refresh único ante 401
└── utils/                   api-client · navigation (mapa §4) · permissions · errors · redirect
i18n/locales/es.json         textos (español por defecto)
mocks/                       API simulada (I0-14; en I0-15, handlers generados del OpenAPI)
shared/api/types.ts          tipos provisionales del contrato (I0-15: generados)
tests/unit · tests/e2e       Vitest · Playwright + axe
docs/screenshots/            capturas para revisión
```

## Decisiones

- **SPA** (`ssr: false`) generada con `nuxt generate`; iconos Lucide empaquetados en el cliente y
  sin fuentes remotas (CSP `connect-src 'self'`).
- **Sesión:** access token solo en memoria (`useState`); refresh con cookie HttpOnly y
  `X-Requested-With: horus`; sin Pinia (conventions.md §3.4). La API simulada guarda su "cookie"
  en `sessionStorage` solo dentro de `mocks/`, porque una cookie HttpOnly no se puede simular.
- **Navegación:** fuente única en `app/utils/navigation.ts`; aparición progresiva por incremento
  (`NUXT_PUBLIC_NAV_INCREMENT`, por defecto `I1`) y secciones **ocultas** sin permiso.
- **Marca provisional** (P-20 abierta): `primary` teal (700 en claro, 500 en oscuro), neutros zinc.
