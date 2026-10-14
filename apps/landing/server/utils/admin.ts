// Utilidades h3 de las rutas del panel (/api/admin/**). El control de acceso y el CSRF los hace
// server/middleware/admin.ts antes de llegar a la ruta.
import type { H3Event } from 'h3'
import type { z } from 'zod'
import { cookieNames } from '../lib/auth/guard'
import { AuthError, type Actor, type SessionInfo } from '../lib/auth/service'
import { useApp } from '../lib/context'
import { maskIp } from './log'

export function adminCookieNames() {
  return cookieNames(useSubmissionDeps().config.adminCookieInsecure)
}

export function setAdminSessionCookie(event: H3Event, token: string, expiresAt: number) {
  const c = adminCookieNames()
  setCookie(event, c.session, token, {
    httpOnly: true,
    secure: c.secure,
    sameSite: 'strict',
    path: '/',
    expires: new Date(expiresAt),
  })
}

export function clearAdminSessionCookie(event: H3Event) {
  const c = adminCookieNames()
  deleteCookie(event, c.session, {
    httpOnly: true,
    secure: c.secure,
    sameSite: 'strict',
    path: '/',
  })
}

/** Sesión completa del administrador (la garantiza el middleware). */
export function adminSession(event: H3Event): SessionInfo {
  const s = event.context.adminSession as SessionInfo | undefined
  if (!s) throw createError({ statusCode: 401, data: { code: 'UNAUTHENTICATED' } })
  return s
}

export function adminActor(event: H3Event): Actor {
  const s = event.context.adminSession as SessionInfo | undefined
  return {
    id: s?.adminId ?? null,
    email: s?.email ?? 'anónimo',
    ip: maskIp(requestClientIp(event)),
  }
}

export function adminAuthDeps() {
  return useApp().auth()
}

/** Cuerpo JSON validado con zod; 422 con la lista de campos si no es válido. */
export async function readAdminBody<S extends z.ZodType>(
  event: H3Event,
  schema: S,
): Promise<z.output<S>> {
  const raw = await readJsonBody(event)
  const parsed = schema.safeParse(raw)
  if (!parsed.success) {
    throw createError({
      statusCode: 422,
      data: {
        code: 'VALIDATION',
        issues: parsed.error.issues
          .slice(0, 20)
          .map((i) => ({ path: i.path.join('.'), message: i.message })),
      },
    })
  }
  return parsed.data
}

/** Traduce los errores de la capa de autenticación a respuestas HTTP. */
export function adminFail(event: H3Event, err: unknown): never {
  if (err instanceof AuthError) {
    if (err.retryAfterSec) setResponseHeader(event, 'Retry-After', err.retryAfterSec)
    throw createError({
      statusCode: err.status,
      data: { code: err.code, retryAfterSec: err.retryAfterSec },
    })
  }
  throw err
}

export function adminId(event: H3Event): number {
  const id = Number(getRouterParam(event, 'id'))
  if (!Number.isInteger(id) || id <= 0)
    throw createError({ statusCode: 400, data: { code: 'BAD_ID' } })
  return id
}
