// GET /api/admin/auth/csrf — token CSRF previo al login (doble envío: cookie + cabecera).
import { randomToken } from '../../../lib/auth/crypto'

export default defineEventHandler((event) => {
  const c = adminCookieNames()
  let token = getCookie(event, c.preCsrf)
  if (!token || token.length < 20 || token.length > 100) {
    token = randomToken(24)
    setCookie(event, c.preCsrf, token, {
      httpOnly: true,
      secure: c.secure,
      sameSite: 'strict',
      path: '/',
      maxAge: 60 * 60,
    })
  }
  return { csrf: token }
})
