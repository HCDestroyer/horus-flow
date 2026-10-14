// Páginas públicas en cada idioma (para sitemap.xml y hreflang). Debe coincidir con
// i18n.pages de nuxt.config.ts.
export const SITE_PAGES: { es: string; en: string; priority: number }[] = [
  { es: '/', en: '/en', priority: 1 },
  { es: '/comprar', en: '/en/buy', priority: 0.8 },
  { es: '/legal/aviso-legal', en: '/en/legal/notice', priority: 0.2 },
  { es: '/legal/privacidad', en: '/en/legal/privacy', priority: 0.2 },
  { es: '/legal/terminos', en: '/en/legal/terms', priority: 0.2 },
]

/** URL pública sin barra final (NUXT_PUBLIC_SITE_URL). */
export function siteBase(url: string): string {
  return url.replace(/\/+$/, '')
}
