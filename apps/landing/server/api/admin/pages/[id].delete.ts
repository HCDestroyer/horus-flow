// DELETE /api/admin/pages/:id — borra una página informativa (con su historial; la auditoría
// guarda el contenido). Las páginas legales no se borran: se despublican (409).
import { audit } from '../../../lib/auth/service'
import { useApp } from '../../../lib/context'
import { deletePage } from '../../../lib/pages'

export default defineEventHandler((event) => {
  const id = pageIdParam(event)
  const deps = adminAuthDeps()
  try {
    const before = deletePage(deps.db, id)
    audit(deps, adminActor(event), 'page.delete', 'page', id, before, null)
    useApp().cache.invalidate()
    return { ok: true }
  } catch (err) {
    pageFail(err)
  }
})
