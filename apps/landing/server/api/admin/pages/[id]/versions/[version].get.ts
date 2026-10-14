// GET /api/admin/pages/:id/versions/:version — instantánea de una versión guardada.
import { getPageVersion } from '../../../../../lib/pages'

export default defineEventHandler((event) => {
  const page = getPageVersion(adminAuthDeps().db, pageIdParam(event), versionIdParam(event))
  if (!page) throw createError({ statusCode: 404, data: { code: 'NOT_FOUND' } })
  return { page }
})
