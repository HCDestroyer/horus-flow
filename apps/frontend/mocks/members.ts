import type { InvitationIssued, Member, Membership, RoleAssignment } from '~~/types/api'
import {
  findTenant,
  newMockTenant,
  registerTenant,
  ROLE_IDS,
  scoped,
  USERS,
  type MockTenant,
} from './data'
import { forbidden, ifMatch, json, noContent, notFound, paginate, problem, randomHex } from './http'
import { hash, mockUuid } from './random'
import { effectivePermissions, findRole, ROLES, roleRoute, withKeys } from './roles'
import { mockState } from './state'

/**
 * Miembros, roles e invitaciones simulados (I1, auth.yaml `/members`, `/roles`,
 * `/auth/invitations/*`). Datos sintéticos.
 *
 * El enlace de invitación (`/invitation#token=…`) se abre normalmente en OTRO navegador (el
 * del invitado), donde esta API simulada no comparte memoria con la de quien invitó. Por eso
 * el token lleva dentro, codificados, el correo, el ISP, los roles y la caducidad (el backend
 * real guarda solo su hash): cualquier pestaña puede validarlo. Lo que sí queda en la memoria
 * de la pestaña: tokens consumidos y el último token de cada miembro (los anteriores dejan de
 * valer al generar uno nuevo).
 *
 * - `localStorage['horus.mock.smtp'] = '1'`: hay correo configurado (`email_sent: true`).
 * - `localStorage['horus.mock.reauthRequired'] = '1'`: cambiar roles responde
 *   `403 REAUTH_REQUIRED` (re-autenticación reciente, `x-reauth`) hasta que se confirma la
 *   identidad con `POST /auth/reauth` (`ctx.reauthRequired`, lo calcula `mocks/server.ts`).
 */

const INVITATION_TTL = 7 * 86_400_000

export { ROLES }

export const tenantRole = (key: string): RoleAssignment => ({
  role_id: ROLE_IDS[key]!,
  role_key: key,
  scope: 'tenant',
})

/** Miembros de partida: las cuentas de demo que pertenecen al ISP y algunos sintéticos. */
function seed(tenant: MockTenant): Member[] {
  const fromUsers: Member[] = USERS.flatMap((u) => {
    const m = u.me.memberships.find((x) => x.tenant_id === tenant.tenant_id)
    if (!m) return []
    return [
      {
        user_id: u.me.id,
        tenant_id: tenant.tenant_id,
        display_name: u.me.display_name,
        email: u.me.email,
        status: 'active' as const,
        mfa_enabled: u.me.mfa_enabled,
        role_assignments: m.roles,
        version: 1,
      },
    ]
  })
  // Un ISP recién dado de alta solo tiene a su primer administrador.
  if (tenant.created) return fromUsers
  const domain = `${tenant.tenant_slug.replace(/-/g, '')}.example`
  const synthetic = (
    n: number,
    name: string,
    local: string,
    status: Member['status'],
    role: string,
    mfa = false,
  ): Member => ({
    user_id: mockUuid('0195b000', `${hash(tenant.tenant_id) % 1000}${n}`),
    tenant_id: tenant.tenant_id,
    display_name: name,
    email: `${local}@${domain}`,
    status,
    mfa_enabled: mfa,
    role_assignments: [tenantRole(role)],
    version: 1,
  })
  return [
    ...fromUsers,
    synthetic(1, 'Marta Gómez', 'marta.gomez', 'active', 'security_analyst', true),
    synthetic(2, 'Jorge Díaz', 'jorge.diaz', 'active', 'network_engineer', true),
    synthetic(3, 'pedro.salas', 'pedro.salas', 'invited', 'noc'),
    synthetic(4, 'Elena Castro', 'elena.castro', 'revoked', 'viewer'),
  ]
}

export function membersOf(tenant: MockTenant) {
  let list = mockState.members.get(tenant.tenant_id)
  if (!list) {
    list = seed(tenant)
    mockState.members.set(tenant.tenant_id, list)
  }
  return list
}

// --- Tokens de invitación ------------------------------------------------------------------

interface InvitationPayload {
  /** Correo invitado. */
  e: string
  /** ISP. */
  t: string
  /** user_id del miembro. */
  u: string
  /** Roles iniciales. */
  r: RoleAssignment[]
  /** Caducidad (ms). */
  x: number
  /** Aleatorio: dos enlaces del mismo miembro nunca coinciden. */
  k: string
  /** Slug y nombre del ISP: el navegador del invitado no conoce un ISP recién creado. */
  s?: string
  n?: string
}

const b64url = (text: string) =>
  btoa(String.fromCharCode(...new TextEncoder().encode(text)))
    .replaceAll('+', '-')
    .replaceAll('/', '_')
    .replace(/=+$/, '')

function fromB64url(token: string) {
  const b64 = token.replaceAll('-', '+').replaceAll('_', '/')
  const bin = atob(b64 + '='.repeat((4 - (b64.length % 4)) % 4))
  return new TextDecoder().decode(Uint8Array.from(bin, (c) => c.charCodeAt(0)))
}

function encodeToken(payload: InvitationPayload) {
  return b64url(JSON.stringify(payload))
}

function decodeToken(token: string): InvitationPayload | null {
  if (token.length < 32) return null
  try {
    const p = JSON.parse(fromB64url(token)) as InvitationPayload
    if (typeof p.e !== 'string' || typeof p.t !== 'string' || typeof p.x !== 'number') return null
    return p
  } catch {
    return null
  }
}

const invalidInvitation = () =>
  problem(
    422,
    'VALIDATION_FAILED',
    'La invitación no es válida o ha caducado',
    {},
    {
      errors: [
        {
          field: 'token',
          code: 'INVALID',
          message: 'El enlace no es válido, ya se usó o ha caducado',
        },
      ],
    },
  )

function validInvitation(token: unknown, now: Date) {
  const raw = String(token ?? '')
  const p = decodeToken(raw)
  if (!p || p.x <= now.getTime() || mockState.usedInvitations.has(raw)) return null
  const latest = mockState.invitationTokens.get(p.u)
  if (latest && latest !== raw) return null
  const tenant =
    findTenant(p.t) ?? (p.s && p.n ? registerTenant(newMockTenant(p.t, p.s, p.n)) : undefined)
  return tenant ? { payload: p, tenant, raw } : null
}

export const existingAccount = (email: string) =>
  USERS.find((u) => u.username === email.trim().toLowerCase())

export function readFlag(key: string) {
  try {
    return globalThis.localStorage?.getItem(key) === '1'
  } catch {
    return false
  }
}

export function issueInvitation(member: Member, tenant: MockTenant, now: Date): InvitationIssued {
  const expires = now.getTime() + INVITATION_TTL
  const token = encodeToken({
    e: member.email,
    t: tenant.tenant_id,
    u: member.user_id,
    r: member.role_assignments,
    x: expires,
    k: randomHex(16),
    s: tenant.tenant_slug,
    n: tenant.tenant_name,
  })
  // Un enlace nuevo invalida los anteriores del mismo miembro.
  mockState.invitationTokens.set(member.user_id, token)
  const origin = globalThis.location?.origin ?? 'https://horus.example'
  return {
    outcome: 'invited',
    user_id: member.user_id,
    invitation_url: `${origin}/invitation#token=${token}`,
    expires_at: new Date(expires).toISOString(),
    email_sent: readFlag('horus.mock.smtp'),
  }
}

/** Membresía (con permisos efectivos) para `GET /me` de quien acepta. */
export function membershipFor(tenant: MockTenant, roles: RoleAssignment[]): Membership {
  return {
    tenant_id: tenant.tenant_id,
    tenant_slug: tenant.tenant_slug,
    tenant_name: tenant.tenant_name,
    tenant_status: 'active',
    roles: withKeys(tenant.tenant_id, roles),
    permissions_with_scope: scoped(effectivePermissions(tenant.tenant_id, roles)),
  }
}

// --- Rutas públicas ------------------------------------------------------------------------

/** `POST /auth/invitations/inspect` y `/accept` (sin token). */
export function invitationRoute(
  route: string,
  body: Record<string, unknown>,
  now: Date,
): Response | undefined {
  if (route === 'POST /auth/invitations/inspect') {
    const inv = validInvitation(body.token, now)
    if (!inv) return invalidInvitation()
    return json(
      {
        email: inv.payload.e,
        tenant_name: inv.tenant.tenant_name,
        needs_password: !existingAccount(inv.payload.e),
        expires_at: new Date(inv.payload.x).toISOString(),
      },
      200,
      { 'Cache-Control': 'no-store' },
    )
  }
  if (route === 'POST /auth/invitations/accept') {
    const inv = validInvitation(body.token, now)
    if (!inv) return invalidInvitation()
    const { payload, tenant, raw } = inv
    const account = existingAccount(payload.e)
    if (account) {
      if (!account.me.memberships.some((m) => m.tenant_id === tenant.tenant_id)) {
        account.me.memberships.push(membershipFor(tenant, payload.r))
      }
    } else {
      const password = typeof body.password === 'string' ? body.password : ''
      if (password.length < 12) {
        return problem(
          422,
          'VALIDATION_FAILED',
          'Revisa los campos marcados',
          {},
          {
            errors: [{ field: 'password', code: 'TOO_SHORT', message: 'Mínimo 12 caracteres' }],
          },
        )
      }
      const name = typeof body.display_name === 'string' ? body.display_name.trim() : ''
      USERS.push({
        username: payload.e.toLowerCase(),
        password,
        mfa: false,
        me: {
          id: payload.u,
          email: payload.e,
          display_name: name || payload.e.split('@')[0]!,
          locale: 'es',
          timezone: null,
          default_tenant_id: null,
          mfa_enabled: false,
          must_change_password: false,
          platform_roles: [],
          platform_permissions: [],
          memberships: [membershipFor(tenant, payload.r)],
        },
      })
    }
    mockState.usedInvitations.add(raw)
    mockState.invitationTokens.delete(payload.u)
    const member = mockState.members.get(tenant.tenant_id)?.find((m) => m.user_id === payload.u)
    if (member) {
      member.status = 'active'
      member.version++
    }
    return noContent()
  }
}

// --- Rutas del ISP -------------------------------------------------------------------------

export interface MemberRouteContext {
  req: Request
  url: URL
  path: string
  method: string
  body: unknown
  tenant: MockTenant
  userId: string
  can: (permission: string) => boolean
  now: Date
  /** La acción `x-reauth` exige re-autenticarse antes (simulación de `auth_time` viejo). */
  reauthRequired?: boolean
  /** ¿Tiene quien llama el permiso en todo el ISP (alcance `*`/`tenant`)? */
  tenantWide?: (permission: string) => boolean
}

function validationError(field: string, message: string, code = 'INVALID') {
  return problem(
    422,
    'VALIDATION_FAILED',
    'Revisa los campos marcados',
    {},
    { errors: [{ field, code, message }] },
  )
}

const SCOPE_RE = /^(tenant|site:[0-9a-f-]{36}|router_group:[0-9a-f-]{36})$/

function parseAssignments(
  tenantId: string,
  value: unknown,
  field: string,
): RoleAssignment[] | Response {
  if (!Array.isArray(value) || value.length === 0) {
    return validationError(field, 'Elige al menos un rol', 'REQUIRED')
  }
  const out: RoleAssignment[] = []
  for (const a of value as Partial<RoleAssignment>[]) {
    if (!a || !findRole(tenantId, String(a.role_id)) || !SCOPE_RE.test(String(a.scope))) {
      return validationError(field, 'Rol o alcance no válido')
    }
    out.push({ role_id: String(a.role_id), scope: String(a.scope) })
  }
  return withKeys(tenantId, out)
}

/** Asignaciones activas (miembros no revocados) de un rol en el ISP. */
export function roleAssignments(tenant: MockTenant, roleId: string) {
  return membersOf(tenant).filter(
    (m) => m.status !== 'revoked' && m.role_assignments.some((a) => a.role_id === roleId),
  ).length
}

export function memberRoute(ctx: MemberRouteContext): Response | undefined {
  if (ctx.path === '/roles' || ctx.path.startsWith('/roles/')) {
    return roleRoute({
      ...ctx,
      tenantWide: (p) => ctx.tenantWide?.(p) ?? ctx.can(p),
      assignments: (roleId) => roleAssignments(ctx.tenant, roleId),
    })
  }
  if (!ctx.path.startsWith('/members')) return
  const list = membersOf(ctx.tenant)

  if (ctx.path === '/members' && ctx.method === 'GET') {
    if (!ctx.can('users.read')) return forbidden()
    const q = (ctx.url.searchParams.get('q') ?? '').trim().toLowerCase()
    const status = ctx.url.searchParams.get('status')
    const rows = list
      .filter((m) => !status || m.status === status)
      .filter(
        (m) => !q || m.display_name.toLowerCase().includes(q) || m.email.toLowerCase().includes(q),
      )
      .sort((a, b) => a.display_name.localeCompare(b.display_name, 'es'))
    return json(paginate(rows, ctx.url))
  }

  if (ctx.path === '/members' && ctx.method === 'POST') {
    if (!ctx.can('users.manage')) return forbidden()
    const body = (ctx.body ?? {}) as { email?: unknown; role_assignments?: unknown }
    const email = String(body.email ?? '')
      .trim()
      .toLowerCase()
    if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email)) {
      return validationError('email', 'Escribe un correo válido')
    }
    const roles = parseAssignments(ctx.tenant.tenant_id, body.role_assignments, 'role_assignments')
    if (roles instanceof Response) return roles
    const existing = list.find((m) => m.email.toLowerCase() === email)
    const account = existingAccount(email)

    // Ya era miembro activo (o la cuenta existe y está activa): queda añadido sin enlace.
    if (existing?.status === 'active' || (account && existing?.status !== 'invited')) {
      const member: Member = existing ?? {
        user_id: account!.me.id,
        tenant_id: ctx.tenant.tenant_id,
        display_name: account!.me.display_name,
        email: account!.me.email,
        status: 'active',
        mfa_enabled: account!.me.mfa_enabled,
        role_assignments: roles,
        version: 1,
      }
      if (!existing) list.push(member)
      else if (existing.status !== 'active') {
        Object.assign(existing, { status: 'active', role_assignments: roles })
        existing.version++
      }
      if (account && !account.me.memberships.some((m) => m.tenant_id === ctx.tenant.tenant_id)) {
        account.me.memberships.push(membershipFor(ctx.tenant, roles))
      }
      const res: InvitationIssued = {
        outcome: 'added',
        user_id: member.user_id,
        invitation_url: null,
        expires_at: null,
        email_sent: readFlag('horus.mock.smtp'),
      }
      return json(res, 202)
    }

    let member = existing
    if (member) {
      member.status = 'invited'
      member.role_assignments = roles
      member.version++
    } else {
      member = {
        user_id: mockUuid('0195b100', hash(email + ctx.tenant.tenant_id)),
        tenant_id: ctx.tenant.tenant_id,
        display_name: email.split('@')[0]!,
        email,
        status: 'invited',
        mfa_enabled: false,
        role_assignments: roles,
        version: 1,
      }
      list.push(member)
    }
    return json(issueInvitation(member, ctx.tenant, ctx.now), 202, { 'Cache-Control': 'no-store' })
  }

  const m = ctx.path.match(/^\/members\/([^/]+)(?:\/(invitation|role-assignments))?$/)
  if (!m) return notFound()
  const member = list.find((x) => x.user_id === m[1])

  if (m[2] === 'invitation' && ctx.method === 'POST') {
    if (!ctx.can('users.manage')) return forbidden()
    if (!member) return notFound()
    if (member.status !== 'invited') {
      return problem(409, 'CONFLICT', 'Este miembro ya no está pendiente de aceptar')
    }
    return json(issueInvitation(member, ctx.tenant, ctx.now), 200, { 'Cache-Control': 'no-store' })
  }

  if (!m[2] && ctx.method === 'DELETE') {
    if (!ctx.can('users.manage')) return forbidden()
    if (!member) return notFound()
    if (member.user_id === ctx.userId) {
      return problem(409, 'CONFLICT', 'No puedes quitarte a ti mismo del ISP')
    }
    list.splice(list.indexOf(member), 1)
    mockState.invitationTokens.delete(member.user_id)
    return noContent()
  }

  if (m[2] === 'role-assignments' && ctx.method === 'PUT') {
    if (!ctx.can('roles.assign')) return forbidden()
    if (!member) return notFound()
    if (ctx.reauthRequired) {
      return problem(403, 'REAUTH_REQUIRED', 'Vuelve a identificarte para cambiar roles')
    }
    const expected = ifMatch(ctx.req)
    if (expected === null) return problem(428, 'PRECONDITION_REQUIRED', 'Falta If-Match')
    if (expected !== member.version) {
      return problem(
        412,
        'PRECONDITION_FAILED',
        'El recurso cambió mientras lo editabas',
        {},
        { current: member as unknown as Record<string, unknown> },
      )
    }
    const roles = parseAssignments(ctx.tenant.tenant_id, ctx.body, 'role_assignments')
    if (roles instanceof Response) return roles
    member.role_assignments = roles
    member.version++
    return json(roles, 200, { ETag: `"${member.version}"` })
  }

  return notFound()
}
