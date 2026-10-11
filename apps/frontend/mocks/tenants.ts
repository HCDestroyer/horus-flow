import type { Member, TenantCreated } from '~~/types/api'
import {
  ALL_TENANTS,
  findTenant,
  newMockTenant,
  registerTenant,
  USERS,
  type MockTenant,
  type MockUser,
} from './data'
import { forbidden, json, problem } from './http'
import {
  existingAccount,
  issueInvitation,
  membersOf,
  membershipFor,
  readFlag,
  tenantRole,
} from './members'
import { hash, mockUuid } from './random'

/**
 * Alta de ISP desde la consola de plataforma (`POST /platform/tenants`, platform.yaml):
 * crea el ISP y deja a su primer administrador como miembro activo si la cuenta ya existía
 * (`outcome=added`, p. ej. el propio superadministrador) o invitado con un enlace de un solo
 * uso (`outcome=invited`, compatible con `/invitation` de `mocks/members.ts`).
 *
 * - `Idempotency-Key` obligatoria (falta → 428); la misma clave repite la respuesta.
 * - `x-reauth`: con `localStorage['horus.mock.reauthRequired'] = '1'` responde
 *   `403 REAUTH_REQUIRED` hasta confirmar la identidad (`POST /auth/reauth`).
 * - Los ISP creados y las membresías de cuentas existentes se guardan en el storage de la
 *   sesión simulada (`sessionStorage`), así sobreviven a recargar la página.
 */

const STORAGE_KEY = 'horus.mock.tenants'
const SLUG_RE = /^[a-z0-9][a-z0-9-]{1,62}$/
const EMAIL_RE = /^[^@\s]+@[^@\s]+\.[^@\s]+$/

type Storage = Pick<globalThis.Storage, 'getItem' | 'setItem'> | null | undefined

interface SavedTenant {
  tenant: MockTenant
  /** Cuentas existentes que quedaron como administradoras. */
  admins: string[]
}

/** Respuestas ya dadas por `Idempotency-Key`. */
const replies = new Map<string, TenantCreated>()

export function resetTenantReplies() {
  replies.clear()
}

function load(storage: Storage): SavedTenant[] {
  try {
    const saved = JSON.parse(storage?.getItem(STORAGE_KEY) ?? '[]')
    return Array.isArray(saved) ? saved : []
  } catch {
    return []
  }
}

function addAdmin(user: MockUser, tenant: MockTenant) {
  if (!user.me.memberships.some((m) => m.tenant_id === tenant.tenant_id)) {
    user.me.memberships.push(membershipFor(tenant, [tenantRole('tenant_admin')]))
  }
}

/** Vuelve a dar de alta los ISP creados en esta sesión simulada (al recargar). */
export function restoreTenants(storage: Storage) {
  for (const saved of load(storage)) {
    const tenant = registerTenant(saved.tenant)
    for (const id of saved.admins) {
      const user = USERS.find((u) => u.me.id === id)
      if (user) addAdmin(user, tenant)
    }
  }
}

export interface TenantRouteContext {
  req: Request
  body: Record<string, unknown>
  can: (permission: string) => boolean
  now: Date
  reauthRequired: boolean
  storage: Storage
}

function validate(body: Record<string, unknown>) {
  const errors: { field: string; code: string; message: string }[] = []
  const str = (k: string) => (typeof body[k] === 'string' ? (body[k] as string).trim() : '')
  const name = str('name')
  if (!name) errors.push({ field: 'name', code: 'REQUIRED', message: 'Escribe el nombre del ISP' })
  else if (name.length > 120) {
    errors.push({ field: 'name', code: 'TOO_LONG', message: 'Máximo 120 caracteres' })
  }
  if (!SLUG_RE.test(str('slug'))) {
    errors.push({
      field: 'slug',
      code: 'INVALID',
      message: 'Solo minúsculas, números y guiones (2 a 63 caracteres)',
    })
  }
  if (!/^[A-Z]{2}$/.test(str('country'))) {
    errors.push({ field: 'country', code: 'INVALID', message: 'País no válido' })
  }
  const timezone = str('timezone')
  try {
    if (!timezone) throw new RangeError('vacía')
    new Intl.DateTimeFormat('en', { timeZone: timezone })
  } catch {
    errors.push({ field: 'timezone', code: 'INVALID', message: 'Zona horaria IANA no válida' })
  }
  if (!EMAIL_RE.test(str('initial_admin_email'))) {
    errors.push({
      field: 'initial_admin_email',
      code: 'INVALID',
      message: 'Escribe un correo válido',
    })
  }
  return errors
}

/** `POST /platform/tenants`. */
export function createTenant(ctx: TenantRouteContext): Response {
  if (!ctx.can('platform.tenants.manage')) return forbidden()
  const key = ctx.req.headers.get('Idempotency-Key')
  if (!key) return problem(428, 'PRECONDITION_REQUIRED', 'Falta Idempotency-Key')
  const replay = replies.get(key)
  if (replay) return json(replay, 201, { Location: `/api/v1/platform/tenants/${replay.id}` })
  if (ctx.reauthRequired) {
    return problem(403, 'REAUTH_REQUIRED', 'Vuelve a identificarte para dar de alta un ISP')
  }

  const errors = validate(ctx.body)
  if (errors.length) {
    return problem(422, 'VALIDATION_FAILED', 'Revisa los campos marcados', {}, { errors })
  }
  const slug = String(ctx.body.slug).trim()
  if (ALL_TENANTS.some((t) => t.tenant_slug === slug)) {
    return problem(
      409,
      'ALREADY_EXISTS',
      'Ya existe un ISP con ese identificador',
      {},
      {
        errors: [{ field: 'slug', code: 'ALREADY_EXISTS', message: 'Ese identificador ya existe' }],
      },
    )
  }

  const at = ctx.now.toISOString()
  const country = String(ctx.body.country)
  const timezone = String(ctx.body.timezone).trim()
  let id = mockUuid('01926b3e', hash(slug))
  while (findTenant(id)) id = mockUuid('01926b3e', hash(id))
  const tenant = registerTenant(
    newMockTenant(id, slug, String(ctx.body.name).trim(), { country, timezone, at }),
  )

  const email = String(ctx.body.initial_admin_email).trim().toLowerCase()
  const account = existingAccount(email)
  let invitation: TenantCreated['initial_admin_invitation']
  if (account) {
    addAdmin(account, tenant)
    invitation = {
      outcome: 'added',
      user_id: account.me.id,
      invitation_url: null,
      expires_at: null,
      email_sent: readFlag('horus.mock.smtp'),
    }
  } else {
    const member: Member = {
      user_id: mockUuid('0195b100', hash(email + tenant.tenant_id)),
      tenant_id: tenant.tenant_id,
      display_name: email.split('@')[0]!,
      email,
      status: 'invited',
      mfa_enabled: false,
      role_assignments: [tenantRole('tenant_admin')],
      version: 1,
    }
    membersOf(tenant).push(member)
    invitation = issueInvitation(member, tenant, ctx.now)
  }

  const saved = load(ctx.storage)
  saved.push({ tenant, admins: account ? [account.me.id] : [] })
  ctx.storage?.setItem(STORAGE_KEY, JSON.stringify(saved))

  const created: TenantCreated = {
    id: tenant.tenant_id,
    version: 1,
    slug,
    name: tenant.tenant_name,
    status: 'active',
    country,
    timezone,
    quotas: {},
    settings: {},
    counts: { sites: 0, routers: 0 },
    created_at: at,
    updated_at: at,
    initial_admin_invitation: invitation,
  }
  replies.set(key, created)
  return json(created, 201, {
    Location: `/api/v1/platform/tenants/${tenant.tenant_id}`,
    ETag: '"1"',
    'Cache-Control': 'no-store',
  })
}
