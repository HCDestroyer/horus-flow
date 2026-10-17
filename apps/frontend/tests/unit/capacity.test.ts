import { beforeEach, describe, expect, it } from 'vitest'
import type { CapacityUplink, UplinkDaily } from '~~/types/api'
import { createAuthFetch, createHorusClient, unwrap } from '~/utils/api-client'
import { tokenForPath } from '~/composables/useAuth'
import {
  capacityFromForm,
  capacityToForm,
  fillNetwatchScript,
  forecastSeries,
  formatMonth,
  formatSigned,
  healthMeta,
  isIPv4,
  parseAsnList,
  sortByHealth,
  sortUplinks,
  splitShares,
  utilLevel,
} from '~/utils/capacity'
import { subscribedEvents } from '~/utils/notification-events'
import { MOCK_PASSWORD, MOCK_TOTP_CODE, TENANTS } from '~~/mocks/data'
import { createMockFetch } from '~~/mocks/server'
import { resetMockState } from '~~/mocks/state'

/** Capacidad de enlaces (rol snmp): utilidades de la página y API simulada. */

const NOW = new Date('2026-10-16T21:00:00Z')
const fn = TENANTS.fibraNorte

beforeEach(() => resetMockState())

describe('utilidades de capacidad', () => {
  it('presenta la salud y ordena peor primero', () => {
    expect(healthMeta('critical')).toMatchObject({ color: 'error', halo: true })
    expect(healthMeta('degraded').color).toBe('warning')
    expect(healthMeta('raro').color).toBe('neutral')
    const rows = [
      { name: 'b', status: 'online' },
      { name: 'a', status: 'degraded' },
      { name: 'c', status: 'offline' },
      { name: 'd', status: 'unknown' },
    ]
    expect(sortByHealth(rows).map((r) => r.name)).toEqual(['c', 'a', 'd', 'b'])
  })

  it('convierte la capacidad del formulario y clasifica la utilización', () => {
    expect(capacityToForm(10e9)).toEqual({ value: 10, unit: 'Gbit/s' })
    expect(capacityToForm(1_500_000_000)).toEqual({ value: 1500, unit: 'Mbit/s' })
    expect(capacityToForm(null)).toEqual({ value: undefined, unit: 'Mbit/s' })
    expect(capacityFromForm(2.5, 'Gbit/s')).toBe(2_500_000_000)
    expect(capacityFromForm(800, 'Mbit/s')).toBe(800_000_000)
    expect(capacityFromForm(undefined, 'Mbit/s')).toBeNull()
    expect(capacityFromForm(0, 'Mbit/s')).toBeNull()
    expect(utilLevel(0.9, 0.7, 0.85)).toBe('critical')
    expect(utilLevel(0.72, 0.7, 0.85)).toBe('warning')
    expect(utilLevel(null, 0.7, 0.85)).toBe('ok')
  })

  it('ordena los enlaces por peor p95 y formatea fechas y crecimientos', () => {
    const u = (label: string, p95: number | null, now: number | null) =>
      ({
        label,
        window: p95 === null ? null : { util_p95: p95 },
        utilization: now,
      }) as CapacityUplink
    expect(
      sortUplinks([u('a', 0.3, 0.2), u('b', 0.9, 0.1), u('c', null, null), u('d', null, 0.5)]).map(
        (x) => x.label,
      ),
    ).toEqual(['b', 'd', 'a', 'c'])
    expect(formatMonth('2027-03-09T00:00:00Z')).toBe('03/2027')
    expect(formatSigned(0.042)).toBe('+4,2 %')
    expect(formatSigned(0.28)).toBe('+28 %')
    expect(formatSigned(-0.03)).toBe('−3,0 %')
  })

  it('arma las series del pronóstico con huecos y no inventa proporciones', () => {
    const daily = [
      { day: '2026-10-14T00:00:00Z', samples: 288, rx_p95_bps: 500, tx_p95_bps: 90 },
      { day: '2026-10-15T00:00:00Z', samples: 0, rx_p95_bps: 0, tx_p95_bps: 0 },
    ] as UplinkDaily[]
    const { history, projection } = forecastSeries(daily, null, {
      rx: 'rx',
      tx: 'tx',
      projection: 'p',
      low: 'l',
      high: 'h',
    })
    expect(history[0]!.points).toEqual([
      ['2026-10-14T00:00:00Z', 500],
      ['2026-10-15T00:00:00Z', null],
    ])
    expect(projection).toEqual([])
    expect(splitShares({ local_bytes: 0, international_bytes: 0 })).toBeNull()
    expect(splitShares({ local_bytes: 30, international_bytes: 70 })).toEqual({
      local: 0.3,
      international: 0.7,
    })
  })

  it('completa el script netwatch solo con IPv4 válidas', () => {
    const script = 'host={GATEWAY_1} a\nhost={GATEWAY_2} b\nhost={GATEWAY_1} c'
    const out = fillNetwatchScript(script, {
      '{GATEWAY_1}': ' 198.51.100.1 ',
      '{GATEWAY_2}': '10.0.0.999',
    })
    expect(out.script).toBe('host=198.51.100.1 a\nhost={GATEWAY_2} b\nhost=198.51.100.1 c')
    expect(out.missing).toEqual(['{GATEWAY_2}'])
    expect(isIPv4('8.8.8.8')).toBe(true)
    expect(isIPv4('256.1.1.1')).toBe(false)
    expect(parseAsnList('AS15169, 2906 2906;36040 x')).toEqual({
      asns: [2906, 15169, 36040],
      invalid: ['x'],
    })
  })

  it('la categoría capacity incluye sus seis tipos de aviso', () => {
    expect(subscribedEvents({ event_types: [], categories: ['capacity'] })).toEqual([
      'uplink_saturated',
      'uplink_degraded',
      'uplink_recovered',
      'capacity_forecast_planning',
      'capacity_forecast_urgent',
      'capacity_forecast_cleared',
    ])
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

describe('API simulada de capacidad', () => {
  it('sirve la vista por nodo, el detalle con pronóstico, el reparto y el script', async () => {
    const api = await client('ana.ruiz@fibranorte.example', true)
    const ov = await unwrap(api.GET('/capacity/overview'))
    expect(ov.data.length).toBeGreaterThan(0)
    const uplinks = ov.data.flatMap((s) => s.uplinks)
    expect(uplinks.length).toBeGreaterThan(0)
    expect(ov.egress?.members.length).toBeGreaterThan(0)
    // El primer enlace del ISP va cerca del límite: hay una recomendación de planificación.
    const planned = uplinks.find(
      (u) => u.forecast?.level === 'planning' || u.forecast?.level === 'urgent',
    )
    expect(planned?.forecast?.recommendation).toMatch(/upgrade/i)
    expect(ov.data.some((s) => s.health.summary.includes('% de pérdida'))).toBe(true)

    const u = uplinks[0]!
    const detail = await unwrap(
      api.GET('/capacity/uplinks/{interface_id}', {
        params: { path: { interface_id: u.interface_id }, query: { days: 30 } },
      }),
    )
    expect(detail.daily).toHaveLength(30)
    expect(detail.forecast?.projection.length).toBe(53)
    expect(detail.uplink?.interface_id).toBe(u.interface_id)

    const egress = await unwrap(api.GET('/capacity/egress', { params: { query: { days: 90 } } }))
    expect(egress.subject_kind).toBe('international_egress')
    expect(egress.recommendation).toBeTruthy()

    const split = await unwrap(
      api.GET('/capacity/traffic-split', { params: { query: { days: 7 } } }),
    )
    expect(split.daily).toHaveLength(7)
    expect(split.country).toBe('GT')
    expect(split.total.international_share).toBeGreaterThan(0.5)

    const sn = await unwrap(
      api.GET('/routers/{router_id}/netwatch-snippet', {
        params: { path: { router_id: u.router_id } },
      }),
    )
    expect(sn.script).toContain('/tool netwatch add type=icmp host={GATEWAY_1}')
    expect(sn.script).toContain('comment="horus-uplink;ref=intl-1.1.1.1"')
    expect(sn.script).not.toMatch(/script=/)
  })

  it('configura un enlace con If-Match, valida y exige permisos', async () => {
    const admin = await client('ana.ruiz@fibranorte.example', true)
    const ov = await unwrap(admin.GET('/capacity/overview'))
    const u = ov.data.flatMap((s) => s.uplinks).find((x) => x.configured)!
    const bad = await admin.PUT('/capacity/uplinks/{interface_id}', {
      params: { path: { interface_id: u.interface_id } },
      body: { router_id: u.router_id, label: '', kind: 'international_transit' },
    })
    expect(bad.response.status).toBe(422)
    const stale = await admin.PUT('/capacity/uplinks/{interface_id}', {
      params: { path: { interface_id: u.interface_id }, header: { 'If-Match': '"9"' } },
      body: { router_id: u.router_id, label: 'X', kind: 'international_transit' },
    })
    expect(stale.response.status).toBe(412)
    const saved = await unwrap(
      admin.PUT('/capacity/uplinks/{interface_id}', {
        params: {
          path: { interface_id: u.interface_id },
          header: { 'If-Match': `"${u.version}"` },
        },
        body: {
          router_id: u.router_id,
          label: 'Proveedor nuevo',
          kind: 'other',
          contracted_bps: 2e9,
        },
      }),
    )
    expect(saved).toMatchObject({
      label: 'Proveedor nuevo',
      international_egress: false,
      version: u.version + 1,
    })
    const settings = await admin.PUT('/capacity/settings', {
      body: { ...ov.settings, planning_threshold: 0.95, urgent_threshold: 0.9 },
    })
    expect(settings.response.status).toBe(422)
    const noc = await client('noc@fibranorte.example', false)
    expect((await unwrap(noc.GET('/capacity/overview'))).data.length).toBeGreaterThan(0)
    const denied = await noc.PUT('/capacity/uplinks/{interface_id}', {
      params: { path: { interface_id: u.interface_id } },
      body: { router_id: u.router_id, label: 'Y', kind: 'other' },
    })
    expect(denied.response.status).toBe(403)
  })
})
