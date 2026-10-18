import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { tokenForPath } from '~/composables/useAuth'
import { ApiError, createAuthFetch, createHorusClient, unwrap } from '~/utils/api-client'
import {
  MAX_SWITCH_PORTS,
  portStatusVisual,
  portUtilization,
  selectionBody,
  sortPorts,
} from '~/utils/switches'
import { MOCK_PASSWORD, MOCK_TOTP_CODE, TENANTS } from '~~/mocks/data'
import { createMockFetch } from '~~/mocks/server'
import { resetMockState } from '~~/mocks/state'
import { resetSwitchMocks } from '~~/mocks/switches'

/**
 * API simulada de switches (contrato v0): alta con device_type=switch (nunca principal, siempre
 * directo), alta del switch con credencial (v2c en SwOS con aviso; sFlow solo en la serie S),
 * verificación SNMP simulada, puertos con elección (máximo 64, If-Match), vecinos y top sFlow
 * (fuente secundaria). El listado de routers no incluye switches.
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

beforeEach(() => {
  const local = memoryStorage()
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

describe('switches (mock)', () => {
  it('lista, alta, conexión, verificación y puertos', async () => {
    const api = await client()
    const routers = await unwrap(api.GET('/routers'))
    expect(routers.data.every((r) => r.device_type === 'router')).toBe(true)
    const seeded = await unwrap(
      api.GET('/routers', { params: { query: { device_type: 'switch' } } }),
    )
    expect(seeded.data.map((r) => r.name)).toEqual(['sw-core-1', 'sw-acceso-2'])

    const site = seeded.data[0]!.site_id
    const e = await fails(
      api.POST('/routers', {
        body: {
          site_id: site,
          name: 'sw-x',
          device_type: 'switch',
          switch_platform: 'vrp',
          is_primary: true,
        },
      }),
    )
    expect(e.status).toBe(422)
    const sw = await unwrap(
      api.POST('/routers', {
        body: { site_id: site, name: 'sw-new', device_type: 'switch', switch_platform: 'swos' },
      }),
    )
    expect(sw).toMatchObject({
      device_type: 'switch',
      is_primary: false,
      connection_mode: 'direct',
      vendor: 'mikrotik',
    })

    const path = { router_id: sw.id }
    const noSflow = await fails(
      api.POST('/routers/{router_id}/switch-onboarding', {
        params: { path },
        body: { management_address: '10.20.0.9', sflow: true },
      }),
    )
    expect(noSflow.status).toBe(422)
    const ob = await unwrap(
      api.POST('/routers/{router_id}/switch-onboarding', {
        params: { path },
        body: { management_address: '10.20.0.9' },
      }),
    )
    expect(ob.snmp_version).toBe('2c')
    expect(ob.language).toBe('swos_web')
    expect(ob.warnings.length).toBeGreaterThan(0)

    await new Promise((r) => setTimeout(r, 5))
    const verified = await unwrap(api.GET('/routers/{router_id}', { params: { path } }))
    expect(verified.onboarding_state).toBe('snmp_verified')

    const ports = await unwrap(api.GET('/switches/{router_id}/ports', { params: { path } }))
    expect(ports.selection.configured).toBe(false)
    expect(ports.data.find((p) => p.alias === 'Uplink')?.monitored).toBe(true)
    const saved = await unwrap(
      api.PUT('/switches/{router_id}/ports', {
        params: { path, header: {} },
        body: { ports: [{ name: 'Port3', uplink: true }] },
      }),
    )
    expect(saved.selection.version).toBe(1)
    expect(saved.data.find((p) => p.name === 'Port3')).toMatchObject({
      monitored: true,
      uplink: true,
    })
    const stale = await fails(
      api.PUT('/switches/{router_id}/ports', {
        params: { path, header: { 'If-Match': '"7"' } },
        body: { ports: [] },
      }),
    )
    expect(stale.status).toBe(412)
    const unknown = await fails(
      api.PUT('/switches/{router_id}/ports', {
        params: { path, header: {} },
        body: { ports: [{ name: 'nope' }] },
      }),
    )
    expect(unknown.status).toBe(422)

    const status = await unwrap(api.GET('/switches/status'))
    expect(status.data.find((s) => s.router_id === sw.id)?.platform).toBe('swos')
    const notSwitch = await fails(
      api.POST('/routers/{router_id}/switch-onboarding', {
        params: { path: { router_id: routers.data[0]!.id } },
        body: {},
      }),
    )
    expect(notSwitch.code).toBe('DEVICE_NOT_A_SWITCH')
  })

  it('top sFlow como fuente secundaria y vecinos LLDP', async () => {
    const api = await client()
    const seeded = await unwrap(
      api.GET('/routers', { params: { query: { device_type: 'switch' } } }),
    )
    const core = seeded.data.find((r) => r.name === 'sw-core-1')!
    const top = await unwrap(
      api.GET('/switches/{router_id}/flows/top', {
        params: { path: { router_id: core.id }, query: { by: 'protocol' } },
      }),
    )
    expect(top.source).toBe('secondary')
    expect(top.data[0]?.key).toBe('6/443')
    const nb = await unwrap(
      api.GET('/switches/{router_id}/neighbors', { params: { path: { router_id: core.id } } }),
    )
    expect(nb.data.some((n) => n.known_device)).toBe(true)
  })
})

describe('utils/switches', () => {
  const port = (name: string, extra: Record<string, unknown> = {}) =>
    ({
      name,
      if_index: Number(name.replace(/\D/g, '')) || 1,
      uplink: false,
      monitored: false,
      oper_status: 'up',
      admin_up: true,
      flaps_1h: 0,
      sample: null,
      ...extra,
    }) as never
  it('ordena, evalúa el estado y valida el máximo', () => {
    const sorted = sortPorts([
      port('e3', { monitored: true }),
      port('e1', { uplink: true, monitored: true }),
      port('e2'),
    ])
    expect(sorted.map((p: { name: string }) => p.name)).toEqual(['e1', 'e3', 'e2'])
    expect(portStatusVisual(port('e1', { flaps_1h: 5 })).key).toBe('flapping')
    expect(portStatusVisual(port('e1', { admin_up: false })).key).toBe('adminDown')
    expect(portStatusVisual(port('e1', { oper_status: 'down' })).key).toBe('down')
    expect(
      portUtilization(port('e1', { sample: { utilization_rx: 0.2, utilization_tx: 0.7 } })),
    ).toBe(0.7)
    expect(portUtilization(port('e1'))).toBeNull()
    const many = Object.fromEntries(
      Array.from({ length: MAX_SWITCH_PORTS + 1 }, (_, i) => [
        `p${i}`,
        { monitored: true, uplink: false },
      ]),
    )
    expect(selectionBody(many).tooMany).toBe(true)
    expect(
      selectionBody({
        a: { monitored: true, uplink: true },
        b: { monitored: false, uplink: false },
      }).ports,
    ).toEqual([{ name: 'a', uplink: true }])
  })
})
