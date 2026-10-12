import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { tokenForPath } from '~/composables/useAuth'
import { ApiError, createAuthFetch, createHorusClient, unwrap } from '~/utils/api-client'
import { MOCK_PASSWORD, MOCK_TOTP_CODE } from '~~/mocks/data'
import { createMockFetch } from '~~/mocks/server'
import { resetMockState } from '~~/mocks/state'

/**
 * API simulada del hub WireGuard: edición con If-Match (412/428/422), rangos (solapes, CGNAT,
 * longitud, dirección de red), quitar rangos (en uso, último, cuarentena) y re-autenticación.
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

async function client(storage = memoryStorage()) {
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
  const res = await unwrap(
    api.POST('/auth/login', { body: { username: ADMIN, password: MOCK_PASSWORD } }),
  )
  if (!('mfa_required' in res)) throw new Error('se esperaba 2FA')
  const session = await unwrap(
    api.POST('/auth/mfa/verify', { body: { mfa_token: res.mfa_token, code: MOCK_TOTP_CODE } }),
  )
  tokens.session = session.access_token
  const scoped = await unwrap(api.POST('/auth/token', { body: { scope: 'platform' } }))
  tokens.scoped = { token: scoped.access_token }
  return { api, storage }
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

async function hub(api: Awaited<ReturnType<typeof client>>['api']) {
  return (await unwrap(api.GET('/platform/wireguard/hubs'))).data[0]!
}

describe('API simulada: hub WireGuard', () => {
  it('el hub trae rangos, MTU, keepalive y versión; totales = suma de rangos', async () => {
    const { api } = await client()
    const h = await hub(api)
    expect(h).toMatchObject({
      mtu: 1420,
      persistent_keepalive_seconds: 25,
      version: 1,
      tunnel_cidr: '10.255.0.0/16',
      services_cidr: '10.254.0.0/24',
      addresses_total: 65534,
    })
    expect(h.pools).toHaveLength(1)
    expect(h.pools![0]!.addresses_used).toBe(h.addresses_used)
  })

  it('PATCH con If-Match: 428 sin cabecera, 422 por campo, 200 y versión nueva', async () => {
    const { api, storage } = await client()
    const h = await hub(api)
    const path = { hub_id: h.id }
    const missing = await fails(
      unwrap(
        api.PATCH('/platform/wireguard/hubs/{hub_id}', {
          params: { path, header: { 'If-Match': '' } },
          body: { mtu: 1400 },
        }),
      ),
    )
    expect(missing.status).toBe(428)
    const bad = await fails(
      unwrap(
        api.PATCH('/platform/wireguard/hubs/{hub_id}', {
          params: { path, header: { 'If-Match': '"1"' } },
          body: { mtu: 9000, endpoint: 'https://vpn.example.net:51820' },
        }),
      ),
    )
    expect(bad.status).toBe(422)
    expect(bad.problem.errors?.map((e) => e.field).sort()).toEqual(['endpoint', 'mtu'])
    const saved = await unwrap(
      api.PATCH('/platform/wireguard/hubs/{hub_id}', {
        params: { path, header: { 'If-Match': '"1"' } },
        body: { endpoint: 'vpn.fibranorte.example', mtu: 1380, persistent_keepalive_seconds: 0 },
      }),
    )
    expect(saved).toMatchObject({
      endpoint: 'vpn.fibranorte.example',
      mtu: 1380,
      persistent_keepalive_seconds: 0,
      version: 2,
    })
    expect(storage.getItem('horus.mock.wireguard')).toContain('vpn.fibranorte.example')
    const stale = await fails(
      unwrap(
        api.PATCH('/platform/wireguard/hubs/{hub_id}', {
          params: { path, header: { 'If-Match': '"1"' } },
          body: { name: 'otro' },
        }),
      ),
    )
    expect(stale.status).toBe(412)
    expect(stale.problem.current).toMatchObject({ version: 2, mtu: 1380 })
  })

  it('horus.mock.wgHubConflict: el primer PATCH responde 412 con la versión vigente', async () => {
    local.setItem('horus.mock.wgHubConflict', '1')
    const { api } = await client()
    const h = await hub(api)
    const params = { path: { hub_id: h.id }, header: { 'If-Match': '"1"' } }
    const conflict = await fails(
      unwrap(api.PATCH('/platform/wireguard/hubs/{hub_id}', { params, body: { mtu: 1400 } })),
    )
    expect(conflict.status).toBe(412)
    expect(conflict.problem.current).toMatchObject({ version: 2, persistent_keepalive_seconds: 30 })
    const ok = await unwrap(
      api.PATCH('/platform/wireguard/hubs/{hub_id}', {
        params: { ...params, header: { 'If-Match': '"2"' } },
        body: { mtu: 1400 },
      }),
    )
    expect(ok).toMatchObject({ version: 3, mtu: 1400, persistent_keepalive_seconds: 30 })
  })

  it('añadir rangos: válido 201; inválido 422; solape 409; persistido al recargar', async () => {
    const { api, storage } = await client()
    const h = await hub(api)
    const add = (cidr: string) =>
      unwrap(
        api.POST('/platform/wireguard/hubs/{hub_id}/pools', {
          params: { path: { hub_id: h.id } },
          body: { cidr },
        }),
      )
    const pool = await add('10.253.0.0/16')
    expect(pool).toMatchObject({ cidr: '10.253.0.0/16', addresses_total: 65534, addresses_used: 0 })

    for (const [cidr, code] of [
      ['100.64.0.0/16', 'CGNAT'],
      ['10.0.0.0/12', 'PREFIX'],
      ['fd00::/56', 'PREFIX'],
      ['10.252.1.0/16', 'NETWORK'],
      ['no-es-un-cidr', 'SYNTAX'],
    ] as const) {
      const e = await fails(add(cidr))
      expect(e.status, cidr).toBe(422)
      expect(e.code).toBe('WIREGUARD_POOL_INVALID')
      expect(e.problem.errors?.[0]).toMatchObject({ field: 'cidr', code })
    }
    for (const cidr of ['10.253.128.0/17', '10.254.0.0/16']) {
      const e = await fails(add(cidr))
      expect(e.status, cidr).toBe(409)
      expect(e.code).toBe('WIREGUARD_POOL_OVERLAP')
    }

    // Recarga: un fetch simulado nuevo sobre el mismo storage recupera los rangos.
    resetMockState()
    const again = await client(storage)
    expect((await hub(again.api)).pools?.map((p) => p.cidr)).toEqual([
      '10.255.0.0/16',
      '10.253.0.0/16',
    ])
  })

  it('quitar: el usado o el último → 409; el vacío → 204; cuarentena → 409', async () => {
    const { api } = await client()
    const h = await hub(api)
    const del = (pool_id: string) =>
      unwrap(
        api.DELETE('/platform/wireguard/hubs/{hub_id}/pools/{pool_id}', {
          params: { path: { hub_id: h.id, pool_id } },
        }),
      )
    const seed = h.pools![0]!.id
    const last = await fails(del(seed))
    expect(last).toMatchObject({ status: 409, code: 'WIREGUARD_POOL_IN_USE' })

    const added = await unwrap(
      api.POST('/platform/wireguard/hubs/{hub_id}/pools', {
        params: { path: { hub_id: h.id } },
        body: { cidr: '10.253.0.0/16' },
      }),
    )
    const used = await fails(del(seed))
    expect(used).toMatchObject({ status: 409, code: 'WIREGUARD_POOL_IN_USE' })

    local.setItem('horus.mock.wgQuarantine', '1')
    expect(await fails(del(added.id))).toMatchObject({ code: 'WIREGUARD_POOL_IN_USE' })
    local.removeItem('horus.mock.wgQuarantine')

    await del(added.id)
    expect((await hub(api)).pools).toHaveLength(1)
    expect(await fails(del(added.id))).toMatchObject({
      status: 404,
      code: 'WIREGUARD_POOL_NOT_FOUND',
    })
  })

  it('re-autenticación: 403 REAUTH_REQUIRED en las tres acciones', async () => {
    local.setItem('horus.mock.reauthRequired', '1')
    const { api } = await client()
    const h = await hub(api)
    const results = await Promise.all([
      fails(
        unwrap(
          api.PATCH('/platform/wireguard/hubs/{hub_id}', {
            params: { path: { hub_id: h.id }, header: { 'If-Match': '"1"' } },
            body: { mtu: 1400 },
          }),
        ),
      ),
      fails(
        unwrap(
          api.POST('/platform/wireguard/hubs/{hub_id}/pools', {
            params: { path: { hub_id: h.id } },
            body: { cidr: '10.253.0.0/16' },
          }),
        ),
      ),
      fails(
        unwrap(
          api.DELETE('/platform/wireguard/hubs/{hub_id}/pools/{pool_id}', {
            params: { path: { hub_id: h.id, pool_id: h.pools![0]!.id } },
          }),
        ),
      ),
    ])
    expect(results.map((e) => e.code)).toEqual([
      'REAUTH_REQUIRED',
      'REAUTH_REQUIRED',
      'REAUTH_REQUIRED',
    ])
  })

  it('horus.mock.wgUsage fija la ocupación del primer rango', async () => {
    local.setItem('horus.mock.wgUsage', '0.85')
    const { api } = await client()
    const h = await hub(api)
    expect(h.addresses_used / h.addresses_total).toBeCloseTo(0.85, 2)
  })
})
