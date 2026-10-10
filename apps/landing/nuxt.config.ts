// Horus Flow — landing comercial (Nuxt 4 + Nuxt UI v4, SSR con servidor Nitro).
// Separada del frontend del producto (apps/frontend): aquí no hay datos de ningún ISP.
// Variables de entorno: ver README.md.
export default defineNuxtConfig({
  compatibilityDate: '2026-09-01',

  modules: ['@nuxt/eslint', '@nuxt/ui', '@nuxtjs/i18n', '~~/modules/pricing-guard'],

  devtools: { enabled: false },

  css: ['~/assets/css/main.css'],

  components: [{ path: '~/components', pathPrefix: false }],

  app: {
    head: {
      htmlAttrs: { lang: 'es' },
      meta: [
        { name: 'viewport', content: 'width=device-width, initial-scale=1, viewport-fit=cover' },
        { name: 'color-scheme', content: 'light dark' },
        { name: 'theme-color', content: '#f6f7f9', media: '(prefers-color-scheme: light)' },
        { name: 'theme-color', content: '#0d1220', media: '(prefers-color-scheme: dark)' },
        { name: 'format-detection', content: 'telephone=no' },
      ],
      link: [{ rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' }],
    },
  },

  runtimeConfig: {
    // Solo servidor. Todas se pueden fijar en tiempo de ejecución con NUXT_<NOMBRE>; el
    // servidor lee además los nombres cortos documentados (SMTP_HOST, LEADS_WEBHOOK_URL…)
    // en server/utils/config.ts.
    public: {
      // URL pública canónica (sin barra final). NUXT_PUBLIC_SITE_URL en tiempo de ejecución.
      siteUrl: 'https://horusflow.kns.gt',
      // Correo de ventas que se muestra en la página.
      salesEmail: 'info@kns.gt',
      // Tiempo mínimo de llenado de un formulario (antispam), en segundos.
      formMinFillSeconds: 3,
    },
  },

  ui: {
    // Fuente del sistema: sin descargas a terceros ni CLS por fuentes web.
    fonts: false,
    // Rendimiento: solo los colores que usa la página y solo los temas de los componentes
    // usados (el CSS pasa de ~200 KB a una fracción; Lighthouse móvil ≥ 90).
    theme: { colors: ['primary', 'neutral', 'error'] },
    experimental: { componentDetection: true },
  },

  colorMode: {
    // Solo la apariencia del sistema (HIG dark-mode › "Avoid offering an app-specific
    // appearance setting"): no hay selector de tema en la página.
    preference: 'system',
    fallback: 'light',
    storageKey: 'horus-landing-color-mode',
  },

  icon: {
    provider: 'none',
    clientBundle: {
      scan: {
        globInclude: ['app/**/*.{vue,ts}'],
        globExclude: ['node_modules', '.nuxt', '.output', 'tests'],
      },
      sizeLimitKb: 128,
    },
  },

  i18n: {
    defaultLocale: 'es',
    strategy: 'prefix_except_default',
    locales: [
      { code: 'es', language: 'es-GT', name: 'Español', file: 'es.json' },
      { code: 'en', language: 'en-US', name: 'English', file: 'en.json' },
    ],
    // La URL decide el idioma (cacheable e indexable); el selector cambia de URL.
    detectBrowserLanguage: false,
    customRoutes: 'config',
    pages: {
      comprar: { es: '/comprar', en: '/buy' },
      'legal-aviso-legal': { es: '/legal/aviso-legal', en: '/legal/notice' },
      'legal-privacidad': { es: '/legal/privacidad', en: '/legal/privacy' },
      'legal-terminos': { es: '/legal/terminos', en: '/legal/terms' },
    },
  },

  typescript: {
    strict: true,
  },

  eslint: {
    config: {
      stylistic: false,
    },
  },

  routeRules: {
    '/**': {
      headers: {
        'X-Content-Type-Options': 'nosniff',
        'Referrer-Policy': 'strict-origin-when-cross-origin',
        'X-Frame-Options': 'DENY',
        'Permissions-Policy': 'camera=(), microphone=(), geolocation=(), payment=()',
        'Content-Security-Policy': "frame-ancestors 'none'; base-uri 'self'; form-action 'self'",
      },
    },
    '/img/**': { headers: { 'Cache-Control': 'public, max-age=2592000' } },
    '/api/**': { headers: { 'Cache-Control': 'no-store' } },
  },

  nitro: {
    compressPublicAssets: true,
  },
})
