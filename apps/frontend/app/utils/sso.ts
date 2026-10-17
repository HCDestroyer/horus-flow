import type { SsoExternalUser, SsoResolved, SsoTarget } from '~~/types/api'

/**
 * Autologin desde ArgosISP (contrato docs/integrations/argosisp.md §1.5, H11) y enlace de
 * conexión (§1.4, H10). El ticket y el código viajan en el fragmento de la URL (`#…`): el
 * navegador no lo envía en peticiones ni queda en registros. La consola lo lee, lo quita de la
 * barra de direcciones y lo canjea en el servidor de Horus.
 */

const TICKET_RE = /^[A-Za-z0-9_-]{20,128}$/

/** Ticket del fragmento (`#<ticket>`), o null si no tiene la forma esperada. */
export function ticketFromHash(hash: string | null | undefined): string | null {
  const raw = (hash ?? '').replace(/^#/, '').trim()
  return TICKET_RE.test(raw) ? raw : null
}

/** Pendiente de vincular: lo que devolvió el canje con `link_required`. */
export interface SsoPending {
  linkToken: string
  linkExpiresAt: string
  externalUser: SsoExternalUser
  target: SsoTarget
}

const PENDING_KEY = 'horus.sso.pending'

/**
 * Guarda la vinculación pendiente solo en esta pestaña (`sessionStorage`, nunca en la URL): el
 * `link_token` vale 10 min y un uso. Sin almacenamiento disponible, la vinculación se pierde y el
 * usuario vuelve a abrir el enlace desde ArgosISP.
 */
export function savePendingLink(p: SsoPending) {
  try {
    globalThis.sessionStorage?.setItem(PENDING_KEY, JSON.stringify(p))
  } catch {
    // sin almacenamiento
  }
}

export function readPendingLink(now = Date.now()): SsoPending | null {
  try {
    const raw = globalThis.sessionStorage?.getItem(PENDING_KEY)
    if (!raw) return null
    const p = JSON.parse(raw) as SsoPending
    if (!p?.linkToken || !p.target || Date.parse(p.linkExpiresAt) <= now) {
      clearPendingLink()
      return null
    }
    return p
  } catch {
    return null
  }
}

export function clearPendingLink() {
  try {
    globalThis.sessionStorage?.removeItem(PENDING_KEY)
  } catch {
    // sin almacenamiento
  }
}

/**
 * Ruta de la consola para el recurso resuelto (con el ACL del usuario). La construye la consola
 * a partir de tipo e id: nunca se navega a una URL recibida (sin open redirect).
 */
export function ssoTargetPath(slug: string, resolved: SsoResolved): string {
  const s = encodeURIComponent(slug)
  const id = encodeURIComponent(resolved.id)
  switch (resolved.kind) {
    case 'customer':
      return `/t/${s}/clients/${id}`
    case 'router':
      return `/t/${s}/routers/${id}`
    case 'finding':
      return `/t/${s}/security/findings/${id}`
  }
  return safeRedirect(`/t/${s}`)
}

/**
 * URL de autologin que devuelve ArgosISP ("Ver en ArgosISP"): el servidor ya la validó; aquí se
 * comprueba otra vez antes de abrirla (https, sin credenciales, ruta `/sso/entrar` y ticket en el
 * fragmento, nunca en la consulta).
 */
export function isSafeArgosEntryUrl(raw: string, allowHttp = false): boolean {
  let u: URL
  try {
    u = new URL(raw)
  } catch {
    return false
  }
  const scheme = u.protocol === 'https:' || (allowHttp && u.protocol === 'http:')
  return (
    scheme &&
    !u.username &&
    !u.password &&
    !u.search &&
    u.pathname.replace(/\/+$/, '') === '/sso/entrar' &&
    u.hash.length > 16
  )
}

/** Enlace de conexión de ArgosISP: `https://<panel>/conectar[?api=<origen>]#<código>`. */
export function looksLikeArgosLink(raw: string): boolean {
  let u: URL
  try {
    u = new URL(raw.trim())
  } catch {
    return false
  }
  return (
    u.protocol === 'https:' &&
    !u.username &&
    u.pathname.replace(/\/+$/, '') === '/conectar' &&
    /^#[A-Za-z0-9_-]{43}$/.test(u.hash)
  )
}
