// POST /api/admin/pages — crea una página (legal o informativa). Guarda la primera versión,
// audita e invalida la caché de la landing.
import { z } from 'zod'
import { audit } from '../../../lib/auth/service'
import { useApp } from '../../../lib/context'
import { createPage } from '../../../lib/pages'

const schema = z.object({ page: z.unknown(), note: z.string().trim().max(200).optional() })

export default defineEventHandler(async (event) => {
  const body = await readAdminBody(event, schema, { maxBytes: PAGE_BODY_MAX_BYTES })
  const deps = adminAuthDeps()
  const actor = adminActor(event)
  try {
    const r = createPage(deps.db, body.page, actor, { note: body.note || 'Creada' })
    audit(deps, actor, 'page.create', 'page', r.page.id, null, r.page)
    useApp().cache.invalidate()
    return { page: r.page, version: r.version }
  } catch (err) {
    pageFail(err)
  }
})
