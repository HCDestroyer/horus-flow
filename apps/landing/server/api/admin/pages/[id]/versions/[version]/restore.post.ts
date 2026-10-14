// POST /api/admin/pages/:id/versions/:version/restore — vuelve a guardar el contenido de una
// versión anterior (como versión nueva: el historial no se reescribe).
import { audit } from '../../../../../../lib/auth/service'
import { useApp } from '../../../../../../lib/context'
import { restorePageVersion } from '../../../../../../lib/pages'

export default defineEventHandler((event) => {
  const id = pageIdParam(event)
  const version = versionIdParam(event)
  const deps = adminAuthDeps()
  const actor = adminActor(event)
  try {
    const r = restorePageVersion(deps.db, id, version, actor)
    audit(deps, actor, 'page.restore', 'page', id, r.before, r.page)
    useApp().cache.invalidate()
    return { page: r.page, version: r.version }
  } catch (err) {
    pageFail(err)
  }
})
