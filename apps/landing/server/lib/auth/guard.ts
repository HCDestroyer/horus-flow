// Control de acceso a /api/admin/**: qué rutas son públicas, cuáles exigen una sesión a medias
// (segundo factor o alta del TOTP) y cuáles una sesión completa; además, CSRF en toda petición
// que cambia algo. Función pura: la usa server/middleware/admin.ts y la prueban los tests.
import type { SessionInfo, SessionStage } from './service'
import { safeEqual } from './crypto'

export const ADMIN_API = '/api/admin'

export function cookieNames(insecure: boolean) {
  // __Host-: solo HTTPS, Path=/ y sin Domain (no la puede fijar un subdominio).
  return insecure
    ? { session: 'hf_admin', preCsrf: 'hf_admin_csrf', secure: false }
    : { session: '__Host-hf_admin', preCsrf: '__Host-hf_admin_csrf', secure: true }
}

/** Rutas sin sesión. */
const OPEN = new Set(['/api/admin/auth/login', '/api/admin/auth/csrf', '/api/admin/auth/session'])
/** Rutas de sesión a medias, con la etapa exigida. */
const PENDING: Record<string, SessionStage[]> = {
  '/api/admin/auth/mfa': ['mfa'],
  '/api/admin/auth/enroll': ['enroll'],
  '/api/admin/auth/logout': ['mfa', 'enroll', 'full'],
}

export interface AdminRequest {
  method: string
  path: string
  /** Cabecera Origin (si la hay). */
  origin?: string
  /** Cabecera Host de la petición. */
  host?: string
  /** Sec-Fetch-Site (si el navegador la envía). */
  fetchSite?: string
  csrfHeader?: string
  preCsrfCookie?: string
}

export type GuardResult = { ok: true } | { ok: false; status: number; code: string }

const SAFE = new Set(['GET', 'HEAD', 'OPTIONS'])

export function normalizePath(path: string): string {
  let p = path.split('?')[0]!
  try {
    p = decodeURIComponent(p)
  } catch {
    // Codificación rota: se compara tal cual (y se trata como ruta de admin si lo parece).
  }
  return p
    .replace(/\/{2,}/g, '/')
    .replace(/\/+$/, '')
    .toLowerCase()
}

export function isAdminApi(path: string): boolean {
  const p = normalizePath(path)
  return p === ADMIN_API || p.startsWith(ADMIN_API + '/')
}

export function checkAdminRequest(req: AdminRequest, session: SessionInfo | null): GuardResult {
  const path = normalizePath(req.path)
  if (!isAdminApi(path)) return { ok: true }
  const method = req.method.toUpperCase()

  if (!SAFE.has(method)) {
    // Origen: el navegador siempre envía Origin en peticiones que cambian algo.
    if (req.fetchSite && !['same-origin', 'none'].includes(req.fetchSite)) {
      return { ok: false, status: 403, code: 'CROSS_SITE' }
    }
    if (req.origin) {
      let host: string
      try {
        host = new URL(req.origin).host
      } catch {
        host = ''
      }
      if (!host || !req.host || host.toLowerCase() !== req.host.toLowerCase()) {
        return { ok: false, status: 403, code: 'BAD_ORIGIN' }
      }
    }
    // CSRF: con sesión, el token de la sesión; sin sesión (login), doble envío con la cookie.
    const header = req.csrfHeader ?? ''
    const expected = session ? session.csrf : (req.preCsrfCookie ?? '')
    if (!header || !expected || !safeEqual(header, expected)) {
      return { ok: false, status: 403, code: 'CSRF' }
    }
  }

  if (OPEN.has(path)) return { ok: true }
  if (!session) return { ok: false, status: 401, code: 'UNAUTHENTICATED' }
  const stages = PENDING[path]
  if (stages) {
    return stages.includes(session.stage)
      ? { ok: true }
      : { ok: false, status: 409, code: 'WRONG_STAGE' }
  }
  if (session.stage !== 'full') return { ok: false, status: 401, code: 'MFA_REQUIRED' }
  return { ok: true }
}
