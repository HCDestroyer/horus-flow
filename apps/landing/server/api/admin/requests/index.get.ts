// GET /api/admin/requests?kind=demo|purchase|all&status=…&q=…&page=1 — lista de solicitudes.
import { listRequests, requestQuerySchema } from '../../../lib/requests'

const PAGE = 25

export default defineEventHandler((event) => {
  const parsed = requestQuerySchema.safeParse(getQuery(event))
  if (!parsed.success) throw createError({ statusCode: 400, data: { code: 'BAD_QUERY' } })
  const f = parsed.data
  const r = listRequests(adminAuthDeps().db, { ...f, limit: PAGE, offset: (f.page - 1) * PAGE })
  return { ...r, page: f.page, pageSize: PAGE }
})
