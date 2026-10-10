// GET /api/admin/requests/:id — detalle de una solicitud con su historial de estados.
import { requestDetail } from '../../../lib/requests'

export default defineEventHandler((event) => {
  const detail = requestDetail(adminAuthDeps().db, adminId(event))
  if (!detail) throw createError({ statusCode: 404, data: { code: 'NOT_FOUND' } })
  return detail
})
