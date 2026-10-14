// POST /api/admin/pages/:id — guarda una página: versión nueva en el historial, auditoría con el
// antes y el después, e invalida la caché (la web lo muestra en la siguiente carga).
// `expectedUpdatedAt` evita pisar el cambio de otra persona (409 CONFLICT).
import { z } from 'zod'
import { audit } from '../../../lib/auth/service'
import { useApp } from '../../../lib/context'
import { updatePage } from '../../../lib/pages'

const schema = z.object({
  page: z.unknown(),
  note: z.string().trim().max(200).optional(),
  expectedUpdatedAt: z.string().max(40).optional(),
})

export default defineEventHandler(async (event) => {
  const id = pageIdParam(event)
  const body = await readAdminBody(event, schema, { maxBytes: PAGE_BODY_MAX_BYTES })
  const deps = adminAuthDeps()
  const actor = adminActor(event)
  try {
    const r = updatePage(deps.db, id, body.page, actor, {
      note: body.note,
      expectedUpdatedAt: body.expectedUpdatedAt,
    })
    audit(deps, actor, 'page.update', 'page', id, r.before, r.page)
    useApp().cache.invalidate()
    return { page: r.page, version: r.version }
  } catch (err) {
    pageFail(err)
  }
})
