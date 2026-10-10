// POST /api/admin/auth/login — correo y contraseña. Abre una sesión a medias: segundo factor
// ("mfa") o alta del TOTP ("enroll"). Rate limit por IP + bloqueo progresivo por correo e IP.
import { z } from 'zod'
import { login } from '../../../lib/auth/service'
import { RateLimiter } from '../../../utils/rate-limit'

const limiter = new RateLimiter(10, 5 * 60_000)
const schema = z.object({
  email: z.string().trim().min(3).max(254),
  password: z.string().min(1).max(256),
})

export default defineEventHandler(async (event) => {
  const ip = requestClientIp(event)
  const rl = limiter.hit(ip)
  if (!rl.allowed) {
    setResponseHeader(event, 'Retry-After', rl.retryAfterSec)
    throw createError({
      statusCode: 429,
      data: { code: 'RATE_LIMITED', retryAfterSec: rl.retryAfterSec },
    })
  }
  const body = await readAdminBody(event, schema)
  try {
    const s = await login(adminAuthDeps(), body.email, body.password, {
      ip,
      userAgent: getRequestHeader(event, 'user-agent'),
    })
    setAdminSessionCookie(event, s.token, s.expiresAt)
    return { stage: s.stage, csrf: s.csrf }
  } catch (err) {
    adminFail(event, err)
  }
})
