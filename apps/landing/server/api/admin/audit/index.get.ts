// GET /api/admin/audit?page=1 — registro de auditoría (quién, qué, antes y después).
import { z } from 'zod'

const schema = z.object({ page: z.coerce.number().int().min(1).max(10_000).optional().default(1) })
const PAGE = 50

export default defineEventHandler((event) => {
  const { page } = schema.parse(getQuery(event))
  const db = adminAuthDeps().db
  const rows = db
    .prepare(
      `SELECT id, at, admin_email, action, entity, entity_id, before, after, ip FROM audit_log
       ORDER BY id DESC LIMIT ? OFFSET ?`,
    )
    .all(PAGE, (page - 1) * PAGE) as {
    id: number
    at: string
    admin_email: string
    action: string
    entity: string
    entity_id: string
    before: string | null
    after: string | null
    ip: string
  }[]
  const total = (db.prepare('SELECT COUNT(*) AS n FROM audit_log').get() as { n: number }).n
  return {
    page,
    pageSize: PAGE,
    total,
    items: rows.map((r) => ({
      id: r.id,
      at: r.at,
      adminEmail: r.admin_email,
      action: r.action,
      entity: r.entity,
      entityId: r.entity_id,
      before: r.before ? JSON.parse(r.before) : null,
      after: r.after ? JSON.parse(r.after) : null,
      ip: r.ip,
    })),
  }
})
