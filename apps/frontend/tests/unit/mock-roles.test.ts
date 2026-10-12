import { afterEach, describe, expect, it } from 'vitest'
import { createAuthFetch, createHorusClient, unwrap, type ApiError } from '~/utils/api-client'
import { tokenForPath } from '~/composables/useAuth'
import { createMockFetch } from '~~/mocks/server'
import { MOCK_PASSWORD, MOCK_TOTP_CODE, TENANTS } from '~~/mocks/data'
import { resetPlatformUsers } from '~~/mocks/platform'
import { resetMockState } from '~~/mocks/state'

const ADMIN = 'ana.ruiz@fibranorte.example'
const ADMIN_ID = '01926b3e-1111-7000-8000-000000000001'

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
  async function login(scope: { tenant_id: string } | { scope: 'platform' }) {
    const res = await unwrap(
      api.POST('/auth/login', { body: { username: ADMIN, password: MOCK_PASSWORD } }),
    )
    if (!('mfa_required' in res)) throw new Error('se esperaba 2FA')
    const session = await unwrap(
      api.POST('/auth/mfa/verify', { body: { mfa_token: res.mfa_token, code: MOCK_TOTP_CODE } }),
    )
    tokens.session = session.access_token
    const scoped = await unwrap(api.POST('/auth/token', { body: scope }))
    tokens.scoped = { token: scoped.access_token }
  }
  return { api, login, storage }
}

const tenantScope = { tenant_id: TENANTS.fibraNorte.tenant_id }

afterEach(() => {
  resetMockState()
  resetPlatformUsers()
})

describe('API simulada: roles propios', () => {
  it('catálogo de permisos con descripciones y roles de sistema con asignaciones', async () => {
    const { api, login } = client()
    await login(tenantScope)
    const catalog = await unwrap(api.GET('/permissions'))
    const users = catalog.permissions.find((p) => p.key === 'users.read')
    expect(users).toMatchObject({ family: 'tenant', description: 'Ver miembros del ISP' })
    const roles = (await unwrap(api.GET('/roles'))).data
    const admin = roles.find((r) => r.key === 'tenant_admin')!
    expect(admin.is_system).toBe(true)
    expect(admin.version).toBeUndefined()
    expect(admin.assignments_count).toBeGreaterThan(0)
  })

  it('crear, editar con If-Match (412 si cambió), borrar; en uso → 409 ROLE_IN_USE', async () => {
    const { api, login, storage } = client()
    await login(tenantScope)
    const created = await unwrap(
      api.POST('/roles', {
        body: { key: 'soporte_n1', name: 'Soporte N1', permissions: ['devices.read'] },
      }),
    )
    expect(created).toMatchObject({ is_system: false, version: 1, assignments_count: 0 })
    // Persistido en el storage de la sesión simulada.
    expect(storage.getItem('horus.mock.roles')).toContain('soporte_n1')

    const dup = await api.POST('/roles', {
      body: { key: 'soporte_n1', name: 'Otro', permissions: ['devices.read'] },
    })
    expect(dup.response.status).toBe(409)
    const bad = await api.POST('/roles', {
      body: { key: '1mal', name: 'Mal', permissions: ['devices.read'] },
    })
    expect(bad.response.status).toBe(422)

    const stale = await api.PATCH('/roles/{role_id}', {
      params: { path: { role_id: created.id }, header: { 'If-Match': '"9"' } },
      body: { name: 'Soporte' },
    })
    expect(stale.response.status).toBe(412)
    const updated = await unwrap(
      api.PATCH('/roles/{role_id}', {
        params: { path: { role_id: created.id }, header: { 'If-Match': '"1"' } },
        body: { name: 'Soporte nivel 1', permissions: ['devices.read', 'sites.read'] },
      }),
    )
    expect(updated).toMatchObject({ name: 'Soporte nivel 1', version: 2 })

    // Asignado a un miembro: no se puede borrar.
    const member = (await unwrap(api.GET('/members', { params: { query: { q: 'marta' } } })))
      .data[0]!
    await unwrap(
      api.PUT('/members/{user_id}/role-assignments', {
        params: {
          path: { user_id: member.user_id },
          header: { 'If-Match': `"${member.version}"` },
        },
        body: [{ role_id: created.id, scope: 'tenant' }],
      }),
    )
    const inUse = await api.DELETE('/roles/{role_id}', {
      params: { path: { role_id: created.id }, header: { 'If-Match': '"2"' } },
    })
    expect(inUse.response.status).toBe(409)
    expect((inUse.error as ApiError['problem']).code).toBe('ROLE_IN_USE')
    expect(
      (await unwrap(api.GET('/roles'))).data.find((r) => r.id === created.id)?.assignments_count,
    ).toBe(1)

    const other = await unwrap(
      api.POST('/roles', {
        body: { key: 'temporal', name: 'Temporal', permissions: ['sites.read'] },
      }),
    )
    const removed = await api.DELETE('/roles/{role_id}', {
      params: { path: { role_id: other.id }, header: { 'If-Match': '"1"' } },
    })
    expect(removed.response.status).toBe(204)
  })

  it('los roles de sistema son de solo lectura (ROLE_READ_ONLY)', async () => {
    const { api, login } = client()
    await login(tenantScope)
    const noc = (await unwrap(api.GET('/roles'))).data.find((r) => r.key === 'noc')!
    const res = await api.PATCH('/roles/{role_id}', {
      params: { path: { role_id: noc.id }, header: { 'If-Match': '"1"' } },
      body: { name: 'NOC' },
    })
    expect(res.response.status).toBe(422)
    expect((res.error as ApiError['problem']).code).toBe('ROLE_READ_ONLY')
  })

  it('sin roles.manage en el ISP → 403', async () => {
    const { api, login } = client()
    // En Red Andina es analista de seguridad: no tiene roles.manage.
    await login({ tenant_id: TENANTS.redAndina.tenant_id })
    const res = await api.POST('/roles', {
      body: { key: 'nuevo', name: 'Nuevo', permissions: ['sites.read'] },
    })
    expect(res.response.status).toBe(403)
  })
})

describe('API simulada: usuarios de plataforma', () => {
  it('lista con búsqueda; bloquear y activar; nunca a uno mismo', async () => {
    const { api, login } = client()
    await login({ scope: 'platform' })
    const all = (await unwrap(api.GET('/platform/users'))).data
    expect(all.map((u) => u.status)).toEqual(
      expect.arrayContaining(['active', 'disabled', 'locked', 'pending']),
    )
    const q = (await unwrap(api.GET('/platform/users', { params: { query: { q: 'carlos' } } })))
      .data
    expect(q).toHaveLength(1)
    const blocked = await unwrap(
      api.PATCH('/platform/users/{user_id}', {
        params: { path: { user_id: q[0]!.id } },
        body: { status: 'disabled' },
      }),
    )
    expect(blocked.status).toBe('disabled')
    const self = await api.PATCH('/platform/users/{user_id}', {
      params: { path: { user_id: ADMIN_ID } },
      body: { status: 'disabled' },
    })
    expect(self.response.status).toBe(409)
  })

  it('roles de plataforma; nunca sin un superadministrador activo (LAST_PLATFORM_ADMIN)', async () => {
    const { api, login } = client()
    await login({ scope: 'platform' })
    const sofia = (await unwrap(api.GET('/platform/users', { params: { query: { q: 'sofía' } } })))
      .data[0]!
    const updated = await unwrap(
      api.PUT('/platform/users/{user_id}/platform-roles', {
        params: { path: { user_id: sofia.id } },
        body: { platform_roles: ['platform_auditor', 'platform_operator'] },
      }),
    )
    expect(updated.platform_roles).toEqual(['platform_auditor', 'platform_operator'])
    const last = await api.PUT('/platform/users/{user_id}/platform-roles', {
      params: { path: { user_id: ADMIN_ID } },
      body: { platform_roles: ['platform_operator'] },
    })
    expect(last.response.status).toBe(409)
    expect((last.error as ApiError['problem']).code).toBe('LAST_PLATFORM_ADMIN')
  })
})
