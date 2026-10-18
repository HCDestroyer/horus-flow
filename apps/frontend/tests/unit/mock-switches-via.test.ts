import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { tokenForPath } from '~/composables/useAuth'
import { ApiError, createAuthFetch, createHorusClient, unwrap } from '~/utils/api-client'
import { viaPathOf } from '~/utils/switches'
import { bucketOf, routerStatusVisual } from '~/utils/router-health'
import { hasTunnel } from '~/utils/transport'
import { MOCK_PASSWORD, MOCK_TOTP_CODE, TENANTS } from '~~/mocks/data'
import { createMockFetch } from '~~/mocks/server'
import { resetMockState } from '~~/mocks/state'
import { resetSwitchMocks } from '~~/mocks/switches'

/**
 * Switches vía router del sitio (ADR-0032) en la API simulada: el alta exige un router del ISP con
 * túnel, asigna una dirección Horus (nunca la IP privada), devuelve la configuración del router,
 * la misma IP no se repite por router, el router lista sus switches y sus redes de gestión, y con
 * el túnel del router caído el switch queda «unreachable».
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
  local.setItem('horus.mock.switchVerifyMs', '1')
  vi.stubGlobal('localStorage', local)
})
afterEach(() => {
  vi.unstubAllGlobals()
  resetMockState()
  resetSwitchMocks()
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

async function fails(p: Promise<{ error?: unknown; data?: unknown }>) {
  try {
    await unwrap(p as never)
  } catch (e) {
    if (e instanceof ApiError) return e
    throw e
  }
  throw new Error('se esperaba un error')
}

describe('switches vía router (mock)', () => {
  it('alta vía router, camino, unicidad, lista del router y túnel caído', async () => {
    const api = await client()
    const routers = (await unwrap(api.GET('/routers'))).data.filter((r) =>
      hasTunnel(r.connection_mode),
    )
    const rt = routers[0]!
    const sw = await unwrap(
      api.POST('/routers', {
        body: {
          site_id: rt.site_id,
          name: 'sw-via',
          device_type: 'switch',
          switch_platform: 'vrp',
        },
      }),
    )
    const path = { router_id: sw.id }
    const noRouter = await fails(
      api.POST('/routers/{router_id}/switch-onboarding', {
        params: { path },
        body: { connection_mode: 'via_router', management_address: '192.168.88.2' },
      }),
    )
    expect(noRouter.status).toBe(422)
    const ob = await unwrap(
      api.POST('/routers/{router_id}/switch-onboarding', {
        params: { path },
        body: {
          connection_mode: 'via_router',
          via_router_id: rt.id,
          management_address: '192.168.88.2',
          sflow: true,
        },
      }),
    )
    expect(ob.connection_mode).toBe('via_router')
    expect(ob.path?.router_id).toBe(rt.id)
    expect(ob.path?.horus_address).not.toBe('192.168.88.2')
    expect(ob.router_config?.config).toContain('netmap to-addresses=192.168.88.2')
    expect(ob.config).toContain('10.255.0.1')

    const detail = await unwrap(api.GET('/routers/{router_id}', { params: { path } }))
    const vp = viaPathOf(detail)
    expect(vp).toMatchObject({
      router_id: rt.id,
      router_name: rt.name,
      horus_address: ob.path?.horus_address,
    })

    // La misma IP privada por el mismo router → 409.
    const sw2 = await unwrap(
      api.POST('/routers', {
        body: {
          site_id: rt.site_id,
          name: 'sw-via-2',
          device_type: 'switch',
          switch_platform: 'routeros',
        },
      }),
    )
    const dup = await fails(
      api.POST('/routers/{router_id}/switch-onboarding', {
        params: { path: { router_id: sw2.id } },
        body: {
          connection_mode: 'via_router',
          via_router_id: rt.id,
          management_address: '192.168.88.2',
        },
      }),
    )
    expect(dup.status).toBe(409)
    expect(dup.code).toBe('MANAGEMENT_ADDRESS_IN_USE')

    // El router lista sus switches y sus redes de gestión.
    const list = await unwrap(
      api.GET('/routers', { params: { query: { device_type: 'switch', via_router_id: rt.id } } }),
    )
    expect(list.data.map((r) => r.name)).toEqual(['sw-via'])
    const rp = { router_id: rt.id }
    await unwrap(
      api.POST('/routers/{router_id}/management-subnets', {
        params: { path: rp },
        body: { prefix: '192.168.88.0/24' },
      }),
    )
    const bad = await fails(
      api.POST('/routers/{router_id}/management-subnets', {
        params: { path: rp },
        body: { prefix: '8.8.8.0/24' },
      }),
    )
    expect(bad.status).toBe(422)
    const subs = await unwrap(
      api.GET('/routers/{router_id}/management-subnets', { params: { path: rp } }),
    )
    expect(subs.data.map((s) => s.prefix)).toEqual(['192.168.88.0/24'])

    // Túnel del router caído → unreachable / via_router_tunnel_down (no offline).
    await new Promise((r) => setTimeout(r, 5))
    local.setItem('horus.mock.viaTunnelDown', '1')
    const st = (await unwrap(api.GET('/switches/status'))).data.find((s) => s.router_id === sw.id)
    expect(st?.status).toBe('unreachable')
    expect(st?.reason).toBe('via_router_tunnel_down')
    expect(routerStatusVisual('unreachable').cls).toBe('text-warning')
    expect(bucketOf('unreachable')).toBe('degraded')
  })
})
