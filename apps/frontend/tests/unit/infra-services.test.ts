import { beforeEach, describe, expect, it } from 'vitest'
import type { InfraService } from '~~/types/api'
import { createAuthFetch, createHorusClient, unwrap, type ApiError } from '~/utils/api-client'
import { tokenForPath } from '~/composables/useAuth'
import {
  blankCheck,
  checkPayload,
  describeCheck,
  formatLatency,
  infraLatency,
  isIpAddress,
  isPrivateIp,
  sortInfraServices,
  summarizeInfra,
} from '~/utils/infra-services'
import { MOCK_PASSWORD, MOCK_TOTP_CODE, TENANTS } from '~~/mocks/data'
import { routersOf } from '~~/mocks/inventory'
import { createMockFetch } from '~~/mocks/server'
import { resetMockState } from '~~/mocks/state'

/** Servicios de infraestructura (rol snmp): utilidades de la página y API simulada. */

const NOW = new Date('2026-10-14T12:00:00Z')
const fn = TENANTS.fibraNorte
const label = (key: string, p?: Record<string, unknown>) =>
  `${key}${p ? ' ' + JSON.stringify(p) : ''}`

beforeEach(() => resetMockState())

function svc(name: string, state: InfraService['status']['state'], enabled = true) {
  return { name, enabled, status: { state, reason: null, check_type: null, since: null } }
}

describe('utilidades de servicios de infraestructura', () => {
  it('ordena problemas primero y resume por estado', () => {
    const rows = [
      svc('b', 'up'),
      svc('a', 'up'),
      svc('z', 'down'),
      svc('x', 'degraded'),
      svc('p', 'down', false),
    ]
    expect(sortInfraServices(rows).map((r) => r.name)).toEqual(['z', 'x', 'a', 'b', 'p'])
    expect(summarizeInfra(rows)).toEqual({ up: 2, down: 1, degraded: 1, unknown: 0, disabled: 1 })
  })

  it('describe los chequeos y su latencia', () => {
    expect(describeCheck({ type: 'http', https: true, path: '/health' }, label)).toBe(
      'infra.checkShort.https {"port":443,"path":"/health"}',
    )
    expect(describeCheck({ type: 'tcp', port: 554 }, label)).toBe(
      'infra.checkShort.tcp {"port":554}',
    )
    expect(describeCheck({ type: 'no_traffic' }, label)).toBe(
      'infra.checkShort.noTraffic {"minutes":15}',
    )
    expect(describeCheck({ type: 'ddos_inbound', pps_threshold: 50000 }, label)).toContain('pps')
    expect(formatLatency(3.24)).toBe('3,2 ms')
    expect(formatLatency(42.4)).toBe('42 ms')
    expect(formatLatency(null)).toBe('—')
    expect(
      infraLatency({
        check_status: [
          { type: 'icmp', latency_ms: 5 },
          { type: 'http', latency_ms: 30 },
          { type: 'no_traffic', latency_ms: null },
        ] as InfraService['check_status'],
      }),
    ).toBe(5)
  })

  it('el cuerpo de un chequeo solo lleva los campos de su tipo', () => {
    const http = { ...blankCheck('http'), forward_port: 18080, window_minutes: 5 }
    expect(checkPayload(http, false)).toEqual({
      type: 'http',
      port: 80,
      path: '/',
      https: false,
      expected_status_min: 200,
      expected_status_max: 399,
    })
    expect(checkPayload(http, true)).toMatchObject({ forward_port: 18080 })
    expect(
      checkPayload({ type: 'ddos_inbound', pps_threshold: 10, bps_threshold: null }, false),
    ).toEqual({
      type: 'ddos_inbound',
      pps_threshold: 10,
    })
  })

  it('reconoce IPs privadas y válidas', () => {
    for (const ip of ['10.1.2.3', '192.168.0.1', '172.20.0.1', '100.64.1.1', 'fd00::1']) {
      expect(isPrivateIp(ip), ip).toBe(true)
    }
    for (const ip of ['192.0.2.20', '8.8.8.8', '2001:db8::1', '172.32.0.1']) {
      expect(isPrivateIp(ip), ip).toBe(false)
    }
    expect(isIpAddress('192.0.2.300')).toBe(false)
    expect(isIpAddress('2001:db8::1')).toBe(true)
    expect(isIpAddress('servidor')).toBe(false)
  })
})

function memoryStorage() {
  const m = new Map<string, string>()
  return {
    getItem: (k: string) => m.get(k) ?? null,
    setItem: (k: string, v: string) => void m.set(k, v),
    removeItem: (k: string) => void m.delete(k),
  }
}

async function client(username: string, mfa: boolean) {
  let session: string | null = null
  let scoped: { token: string } | null = null
  const api = createHorusClient(
    'http://horus.test/api/v1',
    createAuthFetch({
      fetch: createMockFetch({ latencyMs: 0, storage: memoryStorage(), now: () => NOW }),
      basePath: '/api/v1',
      getAccessToken: (path) => tokenForPath(path, session, scoped),
    }),
  )
  const login = await unwrap(
    api.POST('/auth/login', { body: { username, password: MOCK_PASSWORD } }),
  )
  if ('mfa_token' in login) {
    if (!mfa) throw new Error('2FA inesperada')
    session = (
      await unwrap(
        api.POST('/auth/mfa/verify', {
          body: { mfa_token: login.mfa_token, code: MOCK_TOTP_CODE },
        }),
      )
    ).access_token
  } else {
    session = login.access_token
  }
  scoped = {
    token: (await unwrap(api.POST('/auth/token', { body: { tenant_id: fn.tenant_id } })))
      .access_token,
  }
  return api
}

describe('API simulada de servicios de infraestructura', () => {
  it('lista con estado, crea, edita con If-Match, genera el script y borra', async () => {
    const api = await client('ana.ruiz@fibranorte.example', true)
    const list = await unwrap(api.GET('/infra-services'))
    expect(list.meta.scheduler.enabled).toBe(true)
    const states = list.data.map((s) => s.status.state).sort()
    expect(states).toEqual(['degraded', 'down', 'unknown', 'up'])
    const dns = list.data.find((s) => s.status.state === 'down')!
    expect(dns.status.check_type).toBe('no_traffic')

    const detail = await unwrap(
      api.GET('/infra-services/{service_id}', { params: { path: { service_id: dns.id } } }),
    )
    expect(detail.check_status[0]!.recent!.length).toBeGreaterThan(0)

    const router = routersOf(fn, NOW).find((r) => r.router.tunnel_address)!.router
    const created = await unwrap(
      api.POST('/infra-services', {
        body: {
          name: 'Caché OCA',
          target: '10.30.0.9',
          kind: 'other',
          probe_via_router_id: router.id,
          checks: [{ type: 'icmp' }, { type: 'tcp', port: 443, forward_port: 18443 }],
        },
      }),
    )
    expect(created.target_private).toBe(true)
    expect(created.status.state).toBe('unknown')
    expect(created.checks.every((c) => c.id)).toBe(true)

    const snippet = await unwrap(
      api.GET('/infra-services/{service_id}/router-snippet', {
        params: { path: { service_id: created.id }, query: { router_id: router.id } },
      }),
    )
    expect(snippet.applicable).toBe(true)
    expect(snippet.script).toContain('dst-port=18443')
    expect(snippet.script).toContain('to-addresses=10.30.0.9 to-ports=443')
    expect(snippet.warnings.some((w) => w.includes('ICMP'))).toBe(true)

    const stale = await api.PATCH('/infra-services/{service_id}', {
      params: { path: { service_id: created.id }, header: { 'If-Match': '"9"' } },
      body: { name: 'x' },
    })
    expect(stale.response.status).toBe(412)
    const updated = await unwrap(
      api.PATCH('/infra-services/{service_id}', {
        params: { path: { service_id: created.id }, header: { 'If-Match': '"1"' } },
        body: { enabled: false },
      }),
    )
    expect(updated.enabled).toBe(false)
    expect(updated.version).toBe(2)
    const del = await api.DELETE('/infra-services/{service_id}', {
      params: { path: { service_id: created.id }, header: { 'If-Match': '"2"' } },
    })
    expect(del.response.status).toBe(204)
  })

  it('valida y exige permisos de escritura', async () => {
    const admin = await client('ana.ruiz@fibranorte.example', true)
    const bad = await admin.POST('/infra-services', {
      body: { name: 'X', target: 'no-ip', kind: 'dns', checks: [{ type: 'icmp' }] },
    })
    expect(bad.response.status).toBe(422)
    const noc = await client('noc@fibranorte.example', false)
    expect((await unwrap(noc.GET('/infra-services'))).data.length).toBe(4)
    const denied = await noc
      .POST('/infra-services', {
        body: { name: 'Y', target: '192.0.2.9', kind: 'dns', checks: [{ type: 'icmp' }] },
      })
      .then((r) => r.response.status)
    expect(denied).toBe(403)
    const err = await unwrap(
      noc.GET('/infra-services/{service_id}/router-snippet', {
        params: { path: { service_id: (await unwrap(noc.GET('/infra-services'))).data[0]!.id } },
      }),
    ).catch((e: ApiError) => e)
    expect((err as ApiError).status).toBe(403)
  })
})
