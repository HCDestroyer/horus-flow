// Puerta de /api/admin/**: sesión (cookie __Host-), rotación, etapa del login y CSRF. Ninguna
// ruta del panel llega a ejecutarse sin pasar por aquí (server/lib/auth/guard.ts).
import { checkAdminRequest, isAdminApi } from '../lib/auth/guard'
import { resolveSession } from '../lib/auth/service'
import { useApp } from '../lib/context'

export default defineEventHandler((event) => {
  const path = event.path
  if (!isAdminApi(path)) return

  setResponseHeaders(event, {
    'Cache-Control': 'no-store',
    'X-Robots-Tag': 'noindex, nofollow',
  })

  const cookies = adminCookieNames()
  const resolved = resolveSession(useApp().auth(), getCookie(event, cookies.session))
  if (resolved?.rotatedToken) {
    setAdminSessionCookie(event, resolved.rotatedToken, resolved.session.expiresAt)
  }
  const session = resolved?.session ?? null

  const result = checkAdminRequest(
    {
      method: event.method,
      path,
      origin: getRequestHeader(event, 'origin'),
      host: getRequestHeader(event, 'host'),
      fetchSite: getRequestHeader(event, 'sec-fetch-site'),
      csrfHeader: getRequestHeader(event, 'x-csrf-token'),
      preCsrfCookie: getCookie(event, cookies.preCsrf),
    },
    session,
  )
  if (!result.ok) {
    throw createError({ statusCode: result.status, data: { code: result.code } })
  }
  event.context.adminSession = session
})
