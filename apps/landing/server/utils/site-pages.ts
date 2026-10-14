// Páginas públicas en cada idioma (para sitemap.xml y hreflang). Debe coincidir con
// i18n.pages de nuxt.config.ts.
//
// Las páginas legales e informativas (/legal/*, /info/*) viven en la base de datos y se editan en
// /admin › Páginas: el sitemap las añade desde ahí (solo las publicadas) e ignora las entradas de
// esta lista con esos prefijos, que se quedan solo como referencia de las URL originales.
export const SITE_PAGES: { es: string; en: string; priority: number }[] = [
  { es: '/', en: '/en', priority: 1 },
  { es: '/comprar', en: '/en/buy', priority: 0.8 },
  { es: '/legal/aviso-legal', en: '/en/legal/notice', priority: 0.2 },
  { es: '/legal/privacidad', en: '/en/legal/privacy', priority: 0.2 },
  { es: '/legal/terminos', en: '/en/legal/terms', priority: 0.2 },
  { es: '/producto', en: '/en/product', priority: 0.9 },
  { es: '/inteligencia-artificial', en: '/en/ai', priority: 0.8 },
  { es: '/monitoreo', en: '/en/monitoring', priority: 0.8 },
  { es: '/pantallas-noc', en: '/en/noc-screens', priority: 0.7 },
]

/** URL pública sin barra final (NUXT_PUBLIC_SITE_URL). */
export function siteBase(url: string): string {
  return url.replace(/\/+$/, '')
}

/** ¿La ruta es de una página editable (base de datos) y no de esta lista? */
export function isDbPagePath(path: string): boolean {
  return /^(?:\/en)?\/(?:legal|info)\//.test(path)
}
