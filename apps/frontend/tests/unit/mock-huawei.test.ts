import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { tokenForPath } from '~/composables/useAuth'
import { ApiError, createAuthFetch, createHorusClient, unwrap } from '~/utils/api-client'
import { MOCK_PASSWORD, MOCK_TOTP_CODE, TENANTS } from '~~/mocks/data'
import { createMockFetch } from '~~/mocks/server'
import { resetMockState } from '~~/mocks/state'

/**
 * API simulada de un router Huawei VRP en modo directo (docs/vendors/huawei.md): alta con
 * vendor=huawei (sin túnel ni RouterOS), alta directa con dirección de gestión y su
 * configuración VRP, y los errores del contrato (422 sin dirección, ROUTER_VENDOR_NOT_SUPPORTED
 * con un MikroTik, REAUTH_REQUIRED).
 */

const ADMIN = 'ana.ruiz@fibranorte.example'

function memoryStorage() {
  const map = new Map<string, string>()
  return {
    getItem: (k: string) => map.get(k) ?? null,
    setItem: (k: string, v: string) => void map.set(k, v),
    removeItem: (k: string) => void map.delete(k),
  }
}

let local: ReturnType<typeof memoryStorage>
beforeEach(() => {
  local = memoryStorage()
  vi.stubGlobal('localStorage', local)
})
afterEach(() => {
  vi.unstubAllGlobals()
  resetMockState()
})

async function client() {
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
  return api
}

async function fails(p: Promise<unknown>) {
  try {
    await p
  } catch (e) {
    if (e instanceof ApiError) return e
    throw e
  }
  throw new Error('se esperaba un error')
}

describe('API simulada: Huawei VRP en modo directo', () => {
  it('alta Huawei → modo directo sin túnel; configuración VRP con la dirección de gestión', async () => {
    local.setItem('horus.mock.emptyInventory', '1')
    const api = await client()
    const site = await unwrap(api.POST('/sites', { body: { name: 'Nodo Huawei' } }))
    const router = await unwrap(
      api.POST('/routers', {
        body: { site_id: site.id, name: 'ne40e-centro', vendor: 'huawei', model: 'NE40E-X8A' },
      }),
    )
    expect(router).toMatchObject({
      vendor: 'huawei',
      connection_mode: 'direct',
      tunnel_address: null,
      management_address: null,
      routeros_version_supported: null,
      onboarding_state: 'pending_configuration',
    })
    const path = { router_id: router.id }

    const missing = await fails(
      unwrap(api.POST('/routers/{router_id}/direct-onboarding', { params: { path }, body: {} })),
    )
    expect(missing.status).toBe(422)

    local.setItem('horus.mock.reauthRequired', '1')
    const reauth = await fails(
      unwrap(
        api.POST('/routers/{router_id}/direct-onboarding', {
          params: { path },
          body: { management_address: '198.51.100.20' },
        }),
      ),
    )
    expect(reauth.code).toBe('REAUTH_REQUIRED')
    local.removeItem('horus.mock.reauthRequired')

    const cfg = await unwrap(
      api.POST('/routers/{router_id}/direct-onboarding', {
        params: { path },
        body: { management_address: '198.51.100.20', sampling_rate: 512 },
      }),
    )
    expect(cfg).toMatchObject({ language: 'vrp', management_address: '198.51.100.20' })
    expect(cfg.config).toContain('ip netstream export source 198.51.100.20')
    expect(cfg.config).toContain('fix-packets 512 inbound')
    expect(cfg.config.split('\n').some((l) => l.trim().startsWith('#'))).toBe(false)
    expect(cfg.notes[0]).toContain('PENDIENTE DE VALIDACIÓN')

    const after = await unwrap(api.GET('/routers/{router_id}', { params: { path } }))
    expect(after.management_address).toBe('198.51.100.20')
  })

  it('un MikroTik no tiene alta directa', async () => {
    local.setItem('horus.mock.emptyInventory', '1')
    const api = await client()
    const site = await unwrap(api.POST('/sites', { body: { name: 'Nodo MikroTik' } }))
    const router = await unwrap(api.POST('/routers', { body: { site_id: site.id, name: 'rt-a' } }))
    expect(router.connection_mode).toBe('wireguard')
    const err = await fails(
      unwrap(
        api.POST('/routers/{router_id}/direct-onboarding', {
          params: { path: { router_id: router.id } },
          body: { management_address: '198.51.100.21' },
        }),
      ),
    )
    expect(err.code).toBe('ROUTER_VENDOR_NOT_SUPPORTED')
    const bad = await fails(
      unwrap(
        api.POST('/routers', {
          body: { site_id: site.id, name: 'rt-b', vendor: 'cisco', is_primary: false } as never,
        }),
      ),
    )
    expect(bad.status).toBe(422)
  })
})
