// POST /api/admin/auth/mfa — segundo factor: código TOTP o código de recuperación.
import { z } from 'zod'
import { verifySecondFactor } from '../../../lib/auth/service'

const schema = z
  .object({
    code: z.string().trim().max(10).optional(),
    recoveryCode: z.string().trim().max(20).optional(),
  })
  .refine((v) => v.code || v.recoveryCode)

export default defineEventHandler(async (event) => {
  const body = await readAdminBody(event, schema)
  try {
    const s = verifySecondFactor(adminAuthDeps(), adminSession(event), body, {
      ip: requestClientIp(event),
      userAgent: getRequestHeader(event, 'user-agent'),
    })
    setAdminSessionCookie(event, s.token, s.expiresAt)
    return { stage: s.stage, csrf: s.csrf }
  } catch (err) {
    adminFail(event, err)
  }
})
