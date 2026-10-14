// GET /api/admin/pages — lista de páginas editables (sin los cuerpos).
import { listPages } from '../../../lib/pages'

export default defineEventHandler(() => {
  const pages = listPages(adminAuthDeps().db).map(({ body, ...rest }) => ({
    ...rest,
    hasEnglishBody: body.en.trim().length > 0,
  }))
  return { pages }
})
