// GET /api/admin/pages/:id — página completa e historial de versiones.
import { getPage, listPageVersions } from '../../../lib/pages'

export default defineEventHandler((event) => {
  const id = pageIdParam(event)
  const db = adminAuthDeps().db
  const page = getPage(db, id)
  if (!page) throw createError({ statusCode: 404, data: { code: 'NOT_FOUND' } })
  return { page, versions: listPageVersions(db, id) }
})
