// Caché corta de lo que lee la landing (catálogo, ajustes, métodos de pago públicos, enlaces
// del pie y páginas editables ya convertidas a HTML). Se invalida al guardar en el panel; el
// TTL cubre el caso de varios procesos contra la misma base de datos. Un precio o una página
// cambiados en el panel se ven en la web en el siguiente render (mismo proceso) o en ≤ TTL
// (otros procesos).
import type { PublicSite } from '../../shared/catalog'
import type { PageLocale, PublicPage } from '../../shared/pages'
import { readCatalog, readSiteSettings } from './catalog'
import type { DB } from './db'
import { findPublishedPage, footerPages, toPublicPage } from './pages'
import { publicPayments } from './payment-config'
import type { SecretBox } from './secrets'

export const SITE_CACHE_TTL_MS = 30_000
/** Tope de páginas en caché (cada slug en cada idioma, también los que no existen). */
const PAGE_CACHE_MAX = 200

export class SiteCache {
  private value: PublicSite | null = null
  private at = 0
  private pages = new Map<string, { at: number; page: PublicPage | null }>()
  loads = 0
  pageLoads = 0

  constructor(
    private readonly db: () => DB,
    private readonly box: () => SecretBox | null,
    private readonly ttlMs = SITE_CACHE_TTL_MS,
    private readonly clock: () => number = Date.now,
  ) {}

  get(): PublicSite {
    const now = this.clock()
    if (!this.value || now - this.at >= this.ttlMs) {
      const db = this.db()
      this.value = {
        catalog: readCatalog(db),
        settings: readSiteSettings(db),
        payments: publicPayments(db, this.box()),
        pages: footerPages(db),
      }
      this.at = now
      this.loads++
    }
    return this.value
  }

  /** Página publicada por slug e idioma (null si no existe o no está publicada). */
  page(slug: string, locale: PageLocale): PublicPage | null {
    const key = `${locale}:${slug}`
    const now = this.clock()
    const hit = this.pages.get(key)
    if (hit && now - hit.at < this.ttlMs) return hit.page
    const found = findPublishedPage(this.db(), slug, locale)
    const page = found ? toPublicPage(found, locale) : null
    if (this.pages.size >= PAGE_CACHE_MAX) this.pages.clear()
    this.pages.set(key, { at: now, page })
    this.pageLoads++
    return page
  }

  invalidate(): void {
    this.value = null
    this.pages.clear()
  }
}
