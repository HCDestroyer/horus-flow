import { afterEach, describe, expect, it } from 'vitest'
import { createAuthFetch, createHorusClient, unwrap, type ApiError } from '~/utils/api-client'
import { tokenForPath } from '~/composables/useAuth'
import { createMockFetch } from '~~/mocks/server'
import { MOCK_PASSWORD, MOCK_TOTP_CODE, TENANTS, USERS } from '~~/mocks/data'
import { resetMockState } from '~~/mocks/state'

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

function client() {
  const tokens: { session: string | null; scoped: { token: string } | null } = {
    session: null,
    scoped: null,
  }
  const api = createHorusClient(
    'http://horus.test/api/v1',
    createAuthFetch({
      fetch: createMockFetch({ latencyMs: 0, storage: memoryStorage() }),
      basePath: '/api/v1',
      getAccessToken: (path) => tokenForPath(path, tokens.session, tokens.scoped),
    }),
  )
  async function loginAdmin() {
    const res = await unwrap(
      api.POST('/auth/login', { body: { username: ADMIN, password: MOCK_PASSWORD } }),
    )
    if (!('mfa_required' in res)) throw new Error('se esperaba 2FA')
    const session = await unwrap(
      api.POST('/auth/mfa/verify', { body: { mfa_token: res.mfa_token, code: MOCK_TOTP_CODE } }),
    )
    tokens.session = session.access_token
    const scoped = await unwrap(
      api.POST('/auth/token', { body: { tenant_id: TENANTS.fibraNorte.tenant_id } }),
    )
    tokens.scoped = { token: scoped.access_token }
  }
  return { api, loginAdmin }
}

const tokenOf = (url: string | null | undefined) => new URL(url!).hash.replace('#token=', '')

afterEach(() => {
  resetMockState()
  USERS.splice(seededUsers)
})

describe('API simulada: miembros e invitaciones', () => {
  it('lista con búsqueda y filtro de estado; roles del catálogo', async () => {
    const { api, loginAdmin } = client()
    await loginAdmin()
    const all = await unwrap(api.GET('/members'))
    expect(all.data.map((m) => m.email)).toContain(ADMIN)
    const invited = await unwrap(api.GET('/members', { params: { query: { status: 'invited' } } }))
    expect(invited.data.every((m) => m.status === 'invited')).toBe(true)
    const q = await unwrap(api.GET('/members', { params: { query: { q: 'marta' } } }))
    expect(q.data).toHaveLength(1)
    const roles = await unwrap(api.GET('/roles'))
    expect(roles.data.map((r) => r.key)).toEqual(
      expect.arrayContaining(['tenant_admin', 'noc', 'viewer']),
    )
  })

  it('invitar un correo nuevo da enlace de un uso; inspeccionar y aceptar crea la cuenta', async () => {
    const { api, loginAdmin } = client()
    await loginAdmin()
    const roles = (await unwrap(api.GET('/roles'))).data
    const noc = roles.find((r) => r.key === 'noc')!
    const issued = await unwrap(
      api.POST('/members', {
        body: {
          email: 'nueva@fibranorte.example',
          role_assignments: [{ role_id: noc.id, scope: 'tenant' }],
        },
      }),
    )
    expect(issued.outcome).toBe('invited')
    expect(issued.email_sent).toBe(false)
    expect(issued.invitation_url).toMatch(/\/invitation#token=/)
    const token = tokenOf(issued.invitation_url)

    // Otra pestaña (el invitado): no comparte memoria con quien invitó.
    const guest = client().api
    const info = await unwrap(guest.POST('/auth/invitations/inspect', { body: { token } }))
    expect(info).toMatchObject({
      email: 'nueva@fibranorte.example',
      tenant_name: 'Fibra Norte',
      needs_password: true,
    })
    const short = await guest.POST('/auth/invitations/accept', {
      body: { token, display_name: 'Nueva', password: 'corta' },
    })
    expect(short.response.status).toBe(422)
    const ok = await guest.POST('/auth/invitations/accept', {
      body: { token, display_name: 'Nueva', password: 'una-contraseña-larga' },
    })
    expect(ok.response.status).toBe(204)
    // Un solo uso.
    const again = await guest.POST('/auth/invitations/inspect', { body: { token } })
    expect(again.response.status).toBe(422)
    // La cuenta nueva entra con su contraseña.
    const login = await unwrap(
      guest.POST('/auth/login', {
        body: { username: 'nueva@fibranorte.example', password: 'una-contraseña-larga' },
      }),
    )
    expect('access_token' in login).toBe(true)
  })

  it('un enlace nuevo invalida el anterior; solo para invitados (409)', async () => {
    const { api, loginAdmin } = client()
    await loginAdmin()
    const pending = (
      await unwrap(api.GET('/members', { params: { query: { status: 'invited' } } }))
    ).data[0]!
    const first = await unwrap(
      api.POST('/members/{user_id}/invitation', { params: { path: { user_id: pending.user_id } } }),
    )
    const second = await unwrap(
      api.POST('/members/{user_id}/invitation', { params: { path: { user_id: pending.user_id } } }),
    )
    expect(second.invitation_url).not.toBe(first.invitation_url)
    const old = await api.POST('/auth/invitations/inspect', {
      body: { token: tokenOf(first.invitation_url) },
    })
    expect(old.response.status).toBe(422)

    const active = (await unwrap(api.GET('/members', { params: { query: { status: 'active' } } })))
      .data[0]!
    const conflict = await api.POST('/members/{user_id}/invitation', {
      params: { path: { user_id: active.user_id } },
    })
    expect(conflict.response.status).toBe(409)
  })

  it('una cuenta existente queda añadida sin enlace', async () => {
    const { api, loginAdmin } = client()
    await loginAdmin()
    const roles = (await unwrap(api.GET('/roles'))).data
    const res = await unwrap(
      api.POST('/members', {
        body: {
          email: 'noc@fibranorte.example',
          role_assignments: [{ role_id: roles[0]!.id, scope: 'tenant' }],
        },
      }),
    )
    expect(res).toMatchObject({ outcome: 'added', invitation_url: null })
  })

  it('quitar y cambiar roles con If-Match (428/412)', async () => {
    const { api, loginAdmin } = client()
    await loginAdmin()
    const marta = (await unwrap(api.GET('/members', { params: { query: { q: 'marta' } } })))
      .data[0]!
    const body = [{ role_id: marta.role_assignments[0]!.role_id, scope: 'tenant' }]
    const stale = await api.PUT('/members/{user_id}/role-assignments', {
      params: { path: { user_id: marta.user_id }, header: { 'If-Match': '"99"' } },
      body,
    })
    expect(stale.response.status).toBe(412)
    expect((stale.error as ApiError['problem']).current).toMatchObject({ version: marta.version })
    const ok = await api.PUT('/members/{user_id}/role-assignments', {
      params: { path: { user_id: marta.user_id }, header: { 'If-Match': `"${marta.version}"` } },
      body,
    })
    expect(ok.response.status).toBe(200)

    const del = await api.DELETE('/members/{user_id}', {
      params: { path: { user_id: marta.user_id } },
    })
    expect(del.response.status).toBe(204)
    const after = await unwrap(api.GET('/members', { params: { query: { q: 'marta' } } }))
    expect(after.data).toHaveLength(0)
  })

  it('token inválido: 422', async () => {
    const { api } = client()
    const res = await api.POST('/auth/invitations/inspect', { body: { token: 'x'.repeat(40) } })
    expect(res.response.status).toBe(422)
  })
})
