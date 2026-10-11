import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { createAuthFetch, createHorusClient, unwrap, type ApiError } from '~/utils/api-client'
import { tokenForPath } from '~/composables/useAuth'
import { slugify, TENANT_SLUG_RE } from '~/utils/format'
import { createMockFetch } from '~~/mocks/server'
import { MOCK_PASSWORD, MOCK_TOTP_CODE, resetCreatedTenants, TENANTS, USERS } from '~~/mocks/data'
import { resetMockState } from '~~/mocks/state'
import { resetTenantReplies } from '~~/mocks/tenants'

const ADMIN = 'ana.ruiz@fibranorte.example'
const seededUsers = USERS.length

function memoryStorage() {
  const map = new Map<string, string>()
  return {
    getItem: (k: string) => map.get(k) ?? null,
    setItem: (k: string, v: string) => void map.set(k, v),
    removeItem: (k: string) => void map.delete(k),
  }
}

function client(storage = memoryStorage()) {
  const tokens: { session: string | null; scoped: { token: string } | null } = {
    session: null,
    scoped: null,
  }
  const api = createHorusClient(
    'http://horus.test/api/v1',
    createAuthFetch({
      fetch: createMockFetch({ latencyMs: 0, storage }),
      basePath: '/api/v1',
      getAccessToken: (path) => tokenForPath(path, tokens.session, tokens.scoped),
    }),
  )
  async function login(scope: { platform: true } | { tenant_id: string }) {
    const res = await unwrap(
      api.POST('/auth/login', { body: { username: ADMIN, password: MOCK_PASSWORD } }),
    )
    if (!('mfa_required' in res)) throw new Error('se esperaba 2FA')
    const session = await unwrap(
      api.POST('/auth/mfa/verify', { body: { mfa_token: res.mfa_token, code: MOCK_TOTP_CODE } }),
    )
    tokens.session = session.access_token
    const body = 'platform' in scope ? { scope: 'platform' as const } : scope
    const scoped = await unwrap(api.POST('/auth/token', { body }))
    tokens.scoped = { token: scoped.access_token }
  }
  return { api, login, tokens, storage }
}

const body = (over: Record<string, string> = {}) => ({
  name: 'Fibra del Lago',
  slug: 'fibra-del-lago',
  country: 'GT',
  timezone: 'America/Guatemala',
  initial_admin_email: ADMIN,
  ...over,
})

function create(api: ReturnType<typeof client>['api'], b = body(), key = crypto.randomUUID()) {
  return api.POST('/platform/tenants', {
    params: { header: { 'Idempotency-Key': key } },
    body: b,
  })
}

let flags: Map<string, string>
beforeEach(() => {
  flags = new Map()
  globalThis.localStorage = {
    getItem: (k: string) => flags.get(k) ?? null,
    setItem: (k: string, v: string) => void flags.set(k, v),
    removeItem: (k: string) => void flags.delete(k),
  } as Storage
})

afterEach(() => {
  resetMockState()
  resetCreatedTenants()
  resetTenantReplies()
  USERS.splice(seededUsers)
})

describe('API simulada: alta de ISP', () => {
  it('con el correo propio: queda como administrador y aparece en /me y en el resumen', async () => {
    const { api, login } = client()
    await login({ platform: true })
    const created = await unwrap(create(api))
    expect(created.slug).toBe('fibra-del-lago')
    expect(created.initial_admin_invitation).toMatchObject({
      outcome: 'added',
      invitation_url: null,
    })
    const me = await unwrap(api.GET('/me'))
    const m = me.memberships.find((x) => x.tenant_slug === 'fibra-del-lago')
    expect(m?.permissions_with_scope['users.manage']).toBeTruthy()
    const overview = await unwrap(api.GET('/platform/overview'))
    expect(overview.data.map((t) => t.slug)).toContain('fibra-del-lago')
  })

  it('con otro correo: invitación con enlace válido aunque el ISP no exista en esa pestaña', async () => {
    const { api, login } = client()
    await login({ platform: true })
    const created = await unwrap(create(api, body({ initial_admin_email: 'jefa@lago.example' })))
    const inv = created.initial_admin_invitation
    expect(inv.outcome).toBe('invited')
    const token = new URL(inv.invitation_url!).hash.replace('#token=', '')
    // El navegador del invitado no conoce el ISP recién creado.
    resetCreatedTenants()
    const other = client()
    const info = await unwrap(other.api.POST('/auth/invitations/inspect', { body: { token } }))
    expect(info).toMatchObject({ email: 'jefa@lago.example', tenant_name: 'Fibra del Lago' })
  })

  it('identificador repetido → 409; campos no válidos → 422; sin Idempotency-Key → 428', async () => {
    const { api, login } = client()
    await login({ platform: true })
    const dup = await create(api, body({ slug: TENANTS.fibraNorte.tenant_slug }))
    expect(dup.response.status).toBe(409)
    expect((dup.error as ApiError['problem']).code).toBe('ALREADY_EXISTS')

    const bad = await create(api, body({ slug: 'Con Espacios', timezone: 'Marte/Base' }))
    expect(bad.response.status).toBe(422)
    const fields = (bad.error as ApiError['problem']).errors?.map((e) => e.field)
    expect(fields).toEqual(['slug', 'timezone'])

    const noKey = await api.POST('/platform/tenants', {
      params: { header: { 'Idempotency-Key': '' } },
      body: body({ slug: 'otro-isp' }),
    })
    expect(noKey.response.status).toBe(428)
  })

  it('la misma Idempotency-Key repite la respuesta sin crear otro ISP', async () => {
    const { api, login } = client()
    await login({ platform: true })
    const key = crypto.randomUUID()
    const a = await unwrap(create(api, body(), key))
    const b = await unwrap(create(api, body(), key))
    expect(b.id).toBe(a.id)
    const overview = await unwrap(api.GET('/platform/overview'))
    expect(overview.data.filter((t) => t.slug === 'fibra-del-lago')).toHaveLength(1)
  })

  it('sobrevive a recargar (storage de la sesión simulada)', async () => {
    const first = client()
    await first.login({ platform: true })
    await unwrap(create(first.api))
    resetCreatedTenants()
    const again = client(first.storage)
    await again.login({ platform: true })
    const me = await unwrap(again.api.GET('/me'))
    expect(me.memberships.some((m) => m.tenant_slug === 'fibra-del-lago')).toBe(true)
  })
})

describe('API simulada: re-autenticación reciente', () => {
  it('REAUTH_REQUIRED hasta confirmar contraseña y TOTP; el token nuevo es del mismo ámbito', async () => {
    flags.set('horus.mock.reauthRequired', '1')
    const { api, login, tokens } = client()
    await login({ platform: true })
    const blocked = await create(api)
    expect(blocked.response.status).toBe(403)
    expect((blocked.error as ApiError['problem']).code).toBe('REAUTH_REQUIRED')

    const wrong = await api.POST('/auth/reauth', { body: { password: 'mala', code: null } })
    expect(wrong.response.status).toBe(401)
    const noCode = await api.POST('/auth/reauth', { body: { password: MOCK_PASSWORD } })
    expect((noCode.error as ApiError['problem']).code).toBe('INVALID_MFA_CODE')

    const fresh = await unwrap(
      api.POST('/auth/reauth', { body: { password: MOCK_PASSWORD, code: MOCK_TOTP_CODE } }),
    )
    expect(fresh.scope).toBe('platform')
    tokens.scoped = { token: fresh.access_token }
    const created = await unwrap(create(api))
    expect(created.initial_admin_invitation.outcome).toBe('added')
  })

  it('cambiar roles también exige la re-autenticación con la marca activa', async () => {
    flags.set('horus.mock.reauthRequired', '1')
    const { api, login, tokens } = client()
    await login({ tenant_id: TENANTS.fibraNorte.tenant_id })
    const members = await unwrap(api.GET('/members', { params: { query: {} } }))
    const target = members.data.find((m) => m.email.startsWith('jorge.diaz@'))!
    const put = () =>
      api.PUT('/members/{user_id}/role-assignments', {
        params: {
          path: { user_id: target.user_id },
          header: { 'If-Match': `"${target.version}"` },
        },
        body: target.role_assignments.map((a) => ({ role_id: a.role_id, scope: a.scope })),
      })
    expect((await put()).response.status).toBe(403)
    const fresh = await unwrap(
      api.POST('/auth/reauth', { body: { password: MOCK_PASSWORD, code: MOCK_TOTP_CODE } }),
    )
    expect(fresh.scope).toBe('tenant')
    tokens.scoped = { token: fresh.access_token }
    expect((await put()).response.status).toBe(200)
  })
})

describe('identificador del ISP (slug)', () => {
  it('se deriva del nombre: minúsculas, sin acentos, espacios y símbolos → guiones', () => {
    expect(slugify('Fibra Óptica del Lago')).toBe('fibra-optica-del-lago')
    expect(slugify('  Ñandú  Net & Cía. ')).toBe('nandu-net-cia')
    expect(slugify('--Red__Sur--')).toBe('red-sur')
    expect(slugify('x'.repeat(80))).toHaveLength(63)
  })
  it('valida con el patrón del contrato', () => {
    expect(TENANT_SLUG_RE.test(slugify('Fibra Óptica del Lago'))).toBe(true)
    expect(TENANT_SLUG_RE.test('-mal')).toBe(false)
    expect(TENANT_SLUG_RE.test('a')).toBe(false)
    expect(TENANT_SLUG_RE.test('Mayusculas')).toBe(false)
  })
})
