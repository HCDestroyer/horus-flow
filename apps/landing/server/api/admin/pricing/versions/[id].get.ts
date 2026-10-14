// GET /api/admin/pricing/versions/:id — instantánea de una versión publicada.
import { getPricingVersion } from '../../../../lib/catalog'

export default defineEventHandler((event) => {
  const snapshot = getPricingVersion(adminAuthDeps().db, adminId(event))
  if (!snapshot) throw createError({ statusCode: 404, data: { code: 'NOT_FOUND' } })
  return { catalog: snapshot }
})
