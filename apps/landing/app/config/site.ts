// Datos fijos del sitio y del vendedor (no dependen del idioma).
export const site = {
  product: 'Horus Flow',
  seller: {
    legalName: 'Connection And Solutions Company, Sociedad Anónima',
    shortName: 'C&S Company',
    country: 'GT',
    email: 'info@kns.gt',
    /** Domicilio del titular (aviso legal, pie y JSON-LD Organization). */
    address: {
      street: '2da avenida',
      locality: 'San Martín Jilotepeque',
      region: 'Chimaltenango',
      country: 'Guatemala',
    },
  },
  /** URL pública por defecto (NUXT_PUBLIC_SITE_URL la sustituye en tiempo de ejecución). */
  url: 'https://horusflow.kns.gt',
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

/** Dirección en una línea: "2da avenida, San Martín Jilotepeque, Chimaltenango, Guatemala". */
export const sellerAddressLine = [
  site.seller.address.street,
  site.seller.address.locality,
  site.seller.address.region,
  site.seller.address.country,
].join(', ')
