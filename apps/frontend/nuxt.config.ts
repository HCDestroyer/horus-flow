// Horus Flow — frontend (Nuxt 4, SPA). Ver docs/frontend.md §16 y docs/conventions.md §3.
export default defineNuxtConfig({
  compatibilityDate: '2026-09-01',

  // SPA estática servida por Traefik (conventions.md §3.1).
  ssr: false,

  modules: ['@nuxt/eslint', '@nuxt/ui', '@nuxtjs/i18n', '@nuxt/test-utils/module'],

  devtools: { enabled: false },

  css: ['~/assets/css/main.css'],

  // Nombres de componente sin prefijo de carpeta: <ErrorState>, <TenantHeader>…
  components: [{ path: '~/components', pathPrefix: false }],

  app: {
    head: {
      htmlAttrs: { lang: 'es' },
      titleTemplate: '%s · Horus Flow',
      meta: [
        { name: 'viewport', content: 'width=device-width, initial-scale=1, viewport-fit=cover' },
        { name: 'color-scheme', content: 'light dark' },
        { name: 'referrer', content: 'no-referrer' },
      ],
      link: [{ rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' }],
    },
  },

  runtimeConfig: {
    public: {
      // Base de la API del gateway. Relativa: el dominio lo pone cada instalación (D14).
      apiBase: '/api/v1',
      // API simulada en memoria (mocks/, generada del contrato) con HORUS_UI_MOCKS=1 (I0-15,
      // criterio 4); sin la variable, la SPA habla con el gateway en `apiBase`. En una SPA
      // generada el valor queda fijado en la build (`pnpm build:mocks` para demos y e2e).
      apiMock: ['1', 'true'].includes(process.env.HORUS_UI_MOCKS ?? ''),
      // Incremento máximo cuyas secciones existen en la UI (frontend.md §4, aparición
      // progresiva): las posteriores no salen en el menú y su URL da 404. Valores: I0 | I1 |
      // I2 | I3; por defecto I1 (entrega y demo). En una SPA generada se fija en la build:
      // `NUXT_PUBLIC_NAV_INCREMENT=I2 pnpm build`. Un valor no válido cae en I1.
      navIncrement: process.env.NUXT_PUBLIC_NAV_INCREMENT ?? 'I1',
    },
  },

  ui: {
    // Fuente del sistema (frontend.md §13.3) y sin descargas a terceros (CSP).
    fonts: false,
    theme: {
      colors: ['primary', 'secondary', 'success', 'info', 'warning', 'error', 'neutral'],
    },
  },

  colorMode: {
    preference: 'system',
    fallback: 'light',
    storageKey: 'horus-color-mode',
  },

  icon: {
    // CSP connect-src 'self' (security.md §3.1): sin llamadas a la API de Iconify.
    provider: 'none',
    clientBundle: {
      // También .ts: los iconos de la navegación viven en app/utils/navigation.ts.
      scan: {
        globInclude: ['app/**/*.{vue,ts}'],
        globExclude: ['node_modules', '.nuxt', '.output', 'tests'],
      },
      sizeLimitKb: 256,
    },
  },

  i18n: {
    defaultLocale: 'es',
    strategy: 'no_prefix',
    locales: [{ code: 'es', language: 'es-ES', name: 'Español', file: 'es.json' }],
    detectBrowserLanguage: false,
  },

  typescript: {
    strict: true,
  },

  eslint: {
    config: {
      stylistic: false,
    },
  },

  nitro: {
    prerender: {
      // SPA pura: solo se genera el shell (index.html + 200.html/404.html).
      crawlLinks: false,
      routes: ['/'],
    },
  },
})
