// Páginas editables desde el panel (/admin › Páginas): legales (aviso legal, privacidad,
// términos, cookies…) e informativas. Tipos y reglas compartidos entre el servidor
// (server/lib/pages.ts), la landing y el panel.

export const PAGE_KINDS = ['legal', 'info'] as const
export type PageKind = (typeof PAGE_KINDS)[number]
export type PageLocale = 'es' | 'en'

/** Prefijo de la URL según el tipo: /legal/<slug> y /info/<slug> (en inglés, con /en delante). */
export const PAGE_PREFIX: Record<PageKind, string> = { legal: '/legal', info: '/info' }

/** Slug: minúsculas, dígitos y guiones sueltos (sin guion al principio ni al final). */
export const SLUG_PATTERN = /^[a-z0-9]+(?:-[a-z0-9]+)*$/
export const SLUG_MAX = 80

/**
 * Palabras que no pueden ser slug: rutas del sitio, del servidor y de Nuxt. Aunque las páginas
 * vivan bajo /legal o /info, se reservan para no confundir a nadie ni chocar con rutas futuras.
 */
export const RESERVED_SLUGS = new Set([
  'admin',
  'api',
  'comprar',
  'buy',
  'en',
  'es',
  'legal',
  'info',
  'img',
  'motion',
  'nuevo',
  'new',
  'preview',
  'sitemap',
  'sitemap-xml',
  'robots',
  'robots-txt',
  'favicon',
  'nuxt',
  'index',
])

/** Ruta pública de una página en un idioma. */
export function pagePath(kind: PageKind, locale: PageLocale, slug: string): string {
  return `${locale === 'en' ? '/en' : ''}${PAGE_PREFIX[kind]}/${slug}`
}

/** Enlace del pie de página (páginas publicadas con show_in_footer). Ya en el idioma pedido. */
export interface SitePageLink {
  id: string
  kind: PageKind
  title: { es: string; en: string }
  path: { es: string; en: string }
}

/** Página pública lista para pintar (Markdown ya convertido a HTML seguro en el servidor). */
export interface PublicPage {
  id: string
  kind: PageKind
  locale: PageLocale
  title: string
  /** Idioma real del título ('es' si falta el inglés). */
  titleLocale: PageLocale
  summary: string
  /** HTML producido por server/lib/markdown.ts (sin HTML del autor, enlaces filtrados). */
  html: string
  /** Idioma real del cuerpo: 'es' si la versión en inglés está vacía y se muestra la española. */
  contentLocale: PageLocale
  draftNotice: boolean
  updatedAt: string
  slugs: { es: string; en: string }
  paths: { es: string; en: string }
}

/** Página completa tal como la edita el panel. */
export interface AdminPage {
  id: string
  kind: PageKind
  slug: { es: string; en: string }
  title: { es: string; en: string }
  summary: { es: string; en: string }
  body: { es: string; en: string }
  published: boolean
  showInFooter: boolean
  draftNotice: boolean
  sort: number
  createdAt: string
  updatedAt: string
  updatedBy: string
}

export interface PageVersionSummary {
  id: number
  createdAt: string
  adminEmail: string | null
  note: string
  restoredFrom: number | null
}
