# 0012 — Nuxt 4 + Nuxt UI para el frontend

- Estado: Aceptada
- Fecha: 2026-10-07

## Contexto

El frontend es una aplicación interna de operación (dashboards, tablas, formularios de
inventario, configuración, gráficas en tiempo real) para operadores NOC y administradores.
`vision.md` define Nuxt 4, Vue 3, TypeScript, Nuxt UI, Tailwind, ECharts y WebSocket.

## Decisión

- **Nuxt 4 + Vue 3 + TypeScript (strict)**, **Nuxt UI v4** (componentes accesibles sobre
  Tailwind CSS v4), **Apache ECharts** (vía `vue-echarts`) para gráficas.
- Modo de render: **SPA** (`ssr: false`) servida como estáticos por Traefik/servidor Nitro mínimo;
  no hay requisito de SEO y así el frontend no necesita acceso a secretos ni tokens en servidor.
  Se reevalúa SSR solo si aparece una necesidad concreta (p. ej. reportes públicos).
- El frontend habla **solo** con `/api/v1` del gateway (REST + WebSocket); tipos generados desde
  OpenAPI ([api.md](../api.md)).
- Estado: composables + `useFetch`/`useAsyncData`; **Pinia solo** para estado global real
  (sesión, preferencias, suscripciones WS).
- Tokens: access token en memoria; refresh token en cookie `HttpOnly; Secure; SameSite=Strict`
  con ruta restringida (detalle en [security.md](../security.md)).
- Tests E2E con Playwright.

## Alternativas consideradas

- **Nuxt con SSR**: útil para SEO y primer pintado, irrelevante en una herramienta interna tras
  login y añade un servidor Node con acceso a cookies/tokens.
- **Vue + Vite sin Nuxt**: más ligero, pero se pierde routing por archivos, módulos y DX de Nuxt UI.
- **React/Next.js + shadcn**: ecosistema más grande, pero contradice la elección del equipo sin
  beneficio técnico decisivo.
- **Grafana como frontend**: rápido para dashboards, pero no sirve para CRUD, RBAC de negocio ni
  UX propia del producto. Grafana queda para observabilidad interna.

## Consecuencias

- (+) Componentes de dashboard y formularios listos, accesibles y con modo oscuro.
- (+) SPA estática simplifica despliegue y seguridad.
- (−) Dependencia de la evolución de Nuxt UI v4; fijar versiones y revisar upgrades.
- (−) ECharts con muchas series en tiempo real exige *downsampling* en backend (analytics devuelve
  series ya agregadas a la resolución de la ventana).
