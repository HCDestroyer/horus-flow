import type { SsoLink, SsoTarget } from '~~/types/api'
import { TENANTS, type MockUser } from './data'
import { forbidden, json, noContent, notFound, problem, randomHex } from './http'
import { hash, mockUuid } from './random'

/**
 * Autologin desde ArgosISP en la API simulada (contrato §1.5): tickets de demostración, canje
 * con vinculación, vinculaciones por usuario y por ISP. Ticket de demo: cualquier cadena
 * base64url de 20+ caracteres (`/sso/entrar#demo-ticket-de-prueba-0001`); uno que empiece por
 * `vencido` responde 410. El usuario externo es siempre `u-17` (Ana López) y el destino el
 * servicio 812 de Fibra Norte. Estado en `localStorage` para sobrevivir a la recarga del e2e.
 */

const STORAGE_KEY = 'horus.mock.sso'
const CONNECTOR = mockUuid('0195c000', hash(TENANTS.fibraNorte.tenant_id + 'argos'))
export const DEMO_EXTERNAL = { id: 'u-17', email: 'ana.lopez@argos.example', name: 'Ana López' }

interface Pending {
  createdAt: number
  expires: number
  target: SsoTarget
}

interface SsoState {
  links: SsoLink[]
  used: string[]
  pending: Record<string, Pending>
}

function load(): SsoState {
  try {
    const s = JSON.parse(globalThis.localStorage?.getItem(STORAGE_KEY) ?? 'null') as SsoState | null
    if (s?.links) return s
  } catch {
    // estado ilegible
  }
  return { links: [], used: [], pending: {} }
}

let state: SsoState | null = null
function st(): SsoState {
  state ??= load()
  return state
}

function persist() {
  try {
    globalThis.localStorage?.setItem(STORAGE_KEY, JSON.stringify(st()))
  } catch {
    // sin almacenamiento
  }
}

/** Solo tests: vacía el estado simulado. */
export function resetSsoMock() {
  state = { links: [], used: [], pending: {} }
  try {
    globalThis.localStorage?.removeItem(STORAGE_KEY)
  } catch {
    // sin almacenamiento
  }
}

function demoTarget(): SsoTarget {
  const t = TENANTS.fibraNorte
  return {
    tenant_id: t.tenant_id,
    tenant_slug: t.tenant_slug,
    type: 'servicio',
    external_id: '812',
  }
}

export type SsoRedeemOutcome =
  | { kind: 'gone' }
  | { kind: 'link'; body: Record<string, unknown> }
  | { kind: 'linked'; userId: string; body: Record<string, unknown> }

/** Canje de un ticket (un uso). El servidor simulado abre la sesión si hay vinculación. */
export function redeemTicket(ticket: string, now: Date): SsoRedeemOutcome {
  const s = st()
  if (
    !/^[A-Za-z0-9_-]{20,128}$/.test(ticket) ||
    ticket.startsWith('vencido') ||
    s.used.includes(ticket)
  ) {
    return { kind: 'gone' }
  }
  s.used.push(ticket)
  const target = demoTarget()
  const base = { system: 'argosisp', target, external_user: DEMO_EXTERNAL }
  const link = s.links.find(
    (l) => l.tenant_id === target.tenant_id && l.external_user.id === DEMO_EXTERNAL.id,
  )
  if (link) {
    link.last_used_at = now.toISOString()
    persist()
    return { kind: 'linked', userId: link.user_id, body: base }
  }
  const token = `lt.${randomHex(40)}`
  const expires = now.getTime() + 10 * 60_000
  s.pending[token] = { createdAt: now.getTime(), expires, target }
  persist()
  return {
    kind: 'link',
    body: {
      ...base,
      status: 'link_required',
      link_token: token,
      link_expires_at: new Date(expires).toISOString(),
    },
  }
}

/** `POST /me/sso-links`: exige una autenticación posterior al canje (`issuedAt` del token). */
export function createLink(
  user: MockUser,
  linkToken: string,
  issuedAt: number,
  now: Date,
): Response {
  const s = st()
  const p = s.pending[linkToken]
  if (!p || p.expires < now.getTime()) {
    return problem(410, 'SSO_LINK_INVALID', 'La vinculación caducó o ya se usó.')
  }
  if (issuedAt <= p.createdAt) {
    return problem(
      403,
      'REAUTH_REQUIRED',
      'Confirme su contraseña para vincular la cuenta del otro sistema.',
    )
  }
  const membership = user.me.memberships.find((m) => m.tenant_id === p.target.tenant_id)
  if (!membership) {
    return problem(
      403,
      'SSO_NOT_A_MEMBER',
      'Su usuario no es miembro activo del ISP de esta integración.',
    )
  }
  s.pending = Object.fromEntries(Object.entries(s.pending).filter(([k]) => k !== linkToken))
  const link: SsoLink = {
    id: mockUuid('0195d000', hash(linkToken)),
    tenant_id: p.target.tenant_id,
    tenant_name: membership.tenant_name,
    system: 'argosisp',
    connector_id: CONNECTOR,
    user_id: user.me.id,
    user_email: user.me.email,
    external_user: DEMO_EXTERNAL,
    created_at: now.toISOString(),
    last_used_at: now.toISOString(),
  }
  s.links = [
    link,
    ...s.links.filter(
      (l) => !(l.tenant_id === link.tenant_id && l.external_user.id === DEMO_EXTERNAL.id),
    ),
  ]
  persist()
  return json({ link, target: p.target }, 201)
}

export function myLinks(user: MockUser) {
  return json({ data: st().links.filter((l) => l.user_id === user.me.id) })
}

export function deleteMyLink(user: MockUser, id: string) {
  const s = st()
  const before = s.links.length
  s.links = s.links.filter((l) => !(l.id === id && l.user_id === user.me.id))
  if (s.links.length === before) return notFound('SSO_LINK_NOT_FOUND')
  persist()
  return noContent()
}

/** Rutas de ISP: `GET|DELETE /integrations/argosisp/sso/links[/{id}]`. */
export function tenantSsoLinksRoute(ctx: {
  path: string
  method: string
  tenantId: string
  can: (p: string) => boolean
  reauthRequired: boolean
}): Response | undefined {
  const { path, method, tenantId, can } = ctx
  if (!path.startsWith('/integrations/argosisp/sso/links')) return undefined
  const s = st()
  if (path === '/integrations/argosisp/sso/links') {
    if (method === 'GET') {
      if (!can('users.read')) return forbidden()
      return json({ data: s.links.filter((l) => l.tenant_id === tenantId) })
    }
    if (method === 'DELETE') {
      if (!can('users.manage')) return forbidden()
      if (ctx.reauthRequired)
        return problem(403, 'REAUTH_REQUIRED', 'Confirma tu identidad para continuar')
      const n = s.links.filter((l) => l.tenant_id === tenantId).length
      s.links = s.links.filter((l) => l.tenant_id !== tenantId)
      persist()
      return json({ revoked: n })
    }
    return undefined
  }
  const m = path.match(/^\/integrations\/argosisp\/sso\/links\/([^/]+)$/)
  if (m && method === 'DELETE') {
    if (!can('users.manage')) return forbidden()
    const before = s.links.length
    s.links = s.links.filter((l) => !(l.id === m[1] && l.tenant_id === tenantId))
    if (s.links.length === before) return notFound('SSO_LINK_NOT_FOUND')
    persist()
    return noContent()
  }
  return undefined
}
