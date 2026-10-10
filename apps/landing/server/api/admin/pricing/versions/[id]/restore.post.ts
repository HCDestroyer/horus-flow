// POST /api/admin/pricing/versions/:id/restore — vuelve a publicar una versión anterior (como
// versión nueva: el historial no se reescribe).
import { audit } from '../../../../../lib/auth/service'
import { getPricingVersion, publishCatalog } from '../../../../../lib/catalog'
import { useApp } from '../../../../../lib/context'

export default defineEventHandler((event) => {
  const id = adminId(event)
  const deps = adminAuthDeps()
  const snapshot = getPricingVersion(deps.db, id)
  if (!snapshot) throw createError({ statusCode: 404, data: { code: 'NOT_FOUND' } })
  const actor = adminActor(event)
  const r = publishCatalog(deps.db, snapshot, actor, {
    note: `Restaurada la versión ${id}`,
    restoredFrom: id,
  })
  audit(deps, actor, 'pricing.restore', 'pricing', r.version, r.before, r.after)
  useApp().cache.invalidate()
  return { version: r.version, catalog: r.after }
})
