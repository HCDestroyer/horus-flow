// Caché corta de lo que lee la landing (catálogo, ajustes y métodos de pago públicos). Se
// invalida al guardar en el panel; el TTL cubre el caso de varios procesos contra la misma
// base de datos. Un precio cambiado en el panel se ve en la web en el siguiente render
// (mismo proceso) o en ≤ TTL (otros procesos).
import type { PublicSite } from '../../shared/catalog'
import { readCatalog, readSiteSettings } from './catalog'
import type { DB } from './db'
import { publicPayments } from './payment-config'
import type { SecretBox } from './secrets'

export const SITE_CACHE_TTL_MS = 30_000

export class SiteCache {
  private value: PublicSite | null = null
  private at = 0
  loads = 0

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
      }
      this.at = now
      this.loads++
    }
    return this.value
  }

  invalidate(): void {
    this.value = null
  }
}
