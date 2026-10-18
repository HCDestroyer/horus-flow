import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { tokenForPath } from '~/composables/useAuth'
import { ApiError, createAuthFetch, createHorusClient, unwrap } from '~/utils/api-client'
import { MOCK_PASSWORD, MOCK_TOTP_CODE, TENANTS } from '~~/mocks/data'
import { createMockFetch } from '~~/mocks/server'
import { resetMockState } from '~~/mocks/state'
import { isIkeId, modesFor, protectionFor, suggestedMode } from '~/utils/transport'

/**
 * API simulada del hub multitransporte (docs/tunnels.md): `PUT /routers/{id}/transport` con su
 * nivel de protección y la confirmación explícita sin cifrado por Internet, y
 * `POST /routers/{id}/tunnel-config` con la PSK de un solo uso; el túnel avanza con el reloj del
 * asistente. También las reglas de `utils/transport`.
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
  vi.useRealTimers()
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

describe('utils/transport', () => {
  it('transportes por fabricante, recomendado y nivel de protección', () => {
    expect(modesFor('mikrotik')).toEqual(['wireguard', 'ipsec', 'gre'])
    expect(modesFor('huawei')).toEqual(['ipsec', 'gre', 'direct'])
    expect(suggestedMode('huawei')).toBe('ipsec')
    expect(suggestedMode('mikrotik')).toBe('wireguard')
    expect(protectionFor('ipsec', 'internet')).toBe('encrypted')
    expect(protectionFor('gre', 'private')).toBe('unencrypted_private')
    expect(protectionFor('direct', 'internet')).toBe('unencrypted_internet')
    for (const ok of ['198.51.100.7', 'rt-1.isp.example', 'noc@isp.example', 'keyid:rt_1']) {
      expect(isIkeId(ok), ok).toBe(true)
    }
    for (const bad of ['', 'rt', 'a b.example', 'keyid:', '10.0.0.0/8']) {
      expect(isIkeId(bad), bad).toBe(false)
    }
  })
})

describe('API simulada: transportes IPsec y GRE', () => {
  it('MikroTik: IPsec → configuración con PSK → túnel arriba; GRE por Internet exige confirmar', async () => {
    local.setItem('horus.mock.emptyInventory', '1')
    const api = await client()
    const site = await unwrap(api.POST('/sites', { body: { name: 'Nodo IPsec' } }))
    const router = await unwrap(
      api.POST('/routers', { body: { site_id: site.id, name: 'rt-ipsec' } }),
    )
    expect(router).toMatchObject({ connection_mode: 'wireguard', protection_level: 'encrypted' })
    const path = { router_id: router.id }

    // Directo no existe en MikroTik; IPsec sin identidad → 422.
    const direct = await fails(
      unwrap(
        api.PUT('/routers/{router_id}/transport', {
          params: { path },
          body: { connection_mode: 'direct', path: 'private' },
        }),
      ),
    )
    expect(direct.code).toBe('CONNECTION_MODE_NOT_SUPPORTED')
    const noId = await fails(
      unwrap(
        api.PUT('/routers/{router_id}/transport', {
          params: { path },
          body: { connection_mode: 'ipsec' },
        }),
      ),
    )
    expect(noId.status).toBe(422)

    const ipsec = await unwrap(
      api.PUT('/routers/{router_id}/transport', {
        params: { path },
        body: { connection_mode: 'ipsec', tunnel_remote_address: '198.51.100.7' },
      }),
    )
    expect(ipsec).toMatchObject({
      connection_mode: 'ipsec',
      protection_level: 'encrypted',
      tunnel_remote_id: '198.51.100.7',
      onboarding_state: 'pending_configuration',
    })
    const cfg = await unwrap(
      api.POST('/routers/{router_id}/tunnel-config', { params: { path }, body: {} }),
    )
    expect(cfg.transport).toBe('ipsec')
    expect(cfg.psk).toHaveLength(48)
    expect(cfg.configs.map((c) => c.platform)).toEqual(['routeros', 'strongswan'])
    expect(cfg.configs[0]!.config).toContain(cfg.psk!)
    // El túnel avanza con el reloj del asistente (6 s por paso): se adelanta el reloj.
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date(Date.now() + 30_000))
    const up = await unwrap(api.GET('/routers/{router_id}', { params: { path } }))
    expect(['tunnel_up', 'exporting']).toContain(up.onboarding_state)
    const peers = await unwrap(
      api.GET('/wireguard/peers', { params: { query: { router_id: router.id } } }),
    )
    expect(peers.data[0]).toMatchObject({ transport: 'ipsec', status: 'active', public_key: null })
    expect(peers.data[0]!.tunnel?.state).toBe('established')

    // GRE por Internet: sin confirmación 422; con confirmación, aviso en la configuración.
    const unconfirmed = await fails(
      unwrap(
        api.PUT('/routers/{router_id}/transport', {
          params: { path },
          body: { connection_mode: 'gre', tunnel_remote_address: '198.51.100.7', path: 'internet' },
        }),
      ),
    )
    expect(unconfirmed.code).toBe('UNENCRYPTED_TRANSPORT_CONFIRMATION_REQUIRED')
    const gre = await unwrap(
      api.PUT('/routers/{router_id}/transport', {
        params: { path },
        body: {
          connection_mode: 'gre',
          tunnel_remote_address: '198.51.100.7',
          path: 'internet',
          confirm_unencrypted_internet: true,
        },
      }),
    )
    expect(gre).toMatchObject({ connection_mode: 'gre', protection_level: 'unencrypted_internet' })
    const greCfg = await unwrap(
      api.POST('/routers/{router_id}/tunnel-config', { params: { path } }),
    )
    expect(greCfg.psk).toBeNull()
    expect(greCfg.warnings.length).toBeGreaterThan(0)
    expect(greCfg.configs[0]!.config).toContain('!keepalive')
  })

  it('Huawei: recomendado IPsec; WireGuard no existe; IPsec por FQDN', async () => {
    local.setItem('horus.mock.emptyInventory', '1')
    const api = await client()
    const site = await unwrap(api.POST('/sites', { body: { name: 'Nodo NE40E' } }))
    const router = await unwrap(
      api.POST('/routers', { body: { site_id: site.id, name: 'ne40e-1', vendor: 'huawei' } }),
    )
    expect(router).toMatchObject({
      connection_mode: 'direct',
      protection_level: 'unencrypted_private',
    })
    const path = { router_id: router.id }
    const wg = await fails(
      unwrap(
        api.PUT('/routers/{router_id}/transport', {
          params: { path },
          body: { connection_mode: 'wireguard' },
        }),
      ),
    )
    expect(wg.code).toBe('CONNECTION_MODE_NOT_SUPPORTED')
    local.setItem('horus.mock.reauthRequired', '1')
    const reauth = await fails(
      unwrap(
        api.PUT('/routers/{router_id}/transport', {
          params: { path },
          body: { connection_mode: 'ipsec', tunnel_remote_id: 'ne40e-1.isp.example' },
        }),
      ),
    )
    expect(reauth.code).toBe('REAUTH_REQUIRED')
    local.removeItem('horus.mock.reauthRequired')
    const ipsec = await unwrap(
      api.PUT('/routers/{router_id}/transport', {
        params: { path },
        body: { connection_mode: 'ipsec', tunnel_remote_id: 'ne40e-1.isp.example' },
      }),
    )
    expect(ipsec).toMatchObject({ connection_mode: 'ipsec', tunnel_remote_address: null })
    const cfg = await unwrap(api.POST('/routers/{router_id}/tunnel-config', { params: { path } }))
    expect(cfg.configs[0]!.platform).toBe('vrp')
    expect(cfg.configs[0]!.config).toContain('tunnel-protocol ipsec')
  })
})
