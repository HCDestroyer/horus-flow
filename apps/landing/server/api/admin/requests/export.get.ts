// GET /api/admin/requests/export?kind=…&status=…&q=… — CSV (UTF-8 con BOM) de las solicitudes
// filtradas. Contiene datos personales: queda en la auditoría quién lo descargó.
import { audit } from '../../../lib/auth/service'
import { requestQuerySchema, requestsCsv } from '../../../lib/requests'

export default defineEventHandler((event) => {
  const parsed = requestQuerySchema.safeParse(getQuery(event))
  if (!parsed.success) throw createError({ statusCode: 400, data: { code: 'BAD_QUERY' } })
  const deps = adminAuthDeps()
  const csv = requestsCsv(deps.db, parsed.data)
  audit(deps, adminActor(event), 'requests.export', 'request', '', null, parsed.data)
  const date = new Date().toISOString().slice(0, 10)
  setResponseHeaders(event, {
    'Content-Type': 'text/csv; charset=utf-8',
    'Content-Disposition': `attachment; filename="horus-solicitudes-${date}.csv"`,
  })
  return csv
})
