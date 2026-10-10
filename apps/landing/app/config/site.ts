// Datos fijos del sitio y del vendedor (no dependen del idioma).
export const site = {
  product: 'Horus Flow',
  seller: {
    legalName: 'Connection And Solutions Company, Sociedad Anónima',
    shortName: 'C&S Company',
    country: 'GT',
    email: 'info@kns.gt',
  },
  /** Imagen Open Graph / Twitter (1200×630), generada por scripts/optimize-images.mjs. */
  ogImage: '/img/og.png',
  ogImageWidth: 1200,
  ogImageHeight: 630,
}

/** Rutas equivalentes en cada idioma (deben coincidir con i18n.pages de nuxt.config.ts). */
export const localizedPaths = {
  home: { es: '/', en: '/en' },
  buy: { es: '/comprar', en: '/en/buy' },
  notice: { es: '/legal/aviso-legal', en: '/en/legal/notice' },
  privacy: { es: '/legal/privacidad', en: '/en/legal/privacy' },
  terms: { es: '/legal/terminos', en: '/en/legal/terms' },
} as const

export type PageKey = keyof typeof localizedPaths
