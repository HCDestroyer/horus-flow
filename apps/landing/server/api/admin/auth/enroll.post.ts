// POST /api/admin/auth/enroll — confirma el alta del TOTP con un código y entrega los códigos
// de recuperación (solo se muestran esta vez).
import { z } from 'zod'
import { enrollConfirm } from '../../../lib/auth/service'

const schema = z.object({ code: z.string().trim().min(6).max(10) })

export default defineEventHandler(async (event) => {
  const body = await readAdminBody(event, schema)
  try {
    const r = enrollConfirm(adminAuthDeps(), adminSession(event), body.code, {
      ip: requestClientIp(event),
      userAgent: getRequestHeader(event, 'user-agent'),
    })
    setAdminSessionCookie(event, r.session.token, r.session.expiresAt)
    return { stage: r.session.stage, csrf: r.session.csrf, recoveryCodes: r.recoveryCodes }
  } catch (err) {
    adminFail(event, err)
  }
})
