// GET /api/admin/pricing — catálogo publicado e historial de versiones.
import { listPricingVersions, readCatalog } from '../../../lib/catalog'

export default defineEventHandler(() => {
  const db = adminAuthDeps().db
  return { catalog: readCatalog(db), versions: listPricingVersions(db) }
})
