import { beforeEach, describe, expect, it } from 'vitest'
import type { RouterStatusRow } from '~/widgets/shapes'
import { createAuthFetch, createHorusClient, unwrap, type ApiError } from '~/utils/api-client'
import { tokenForPath } from '~/composables/useAuth'
import { MOCK_PASSWORD, MOCK_TOTP_CODE, TENANTS } from '~~/mocks/data'
import { routersOf } from '~~/mocks/inventory'
import { createMockRealtime } from '~~/mocks/realtime'
import { createMockFetch } from '~~/mocks/server'
import {
  findSnmpRouter,
  routerDeviceSeries,
  routerInterfaces,
  routerInterfaceSeries,
  routerLiveMetrics,
  routerStatusList,
} from '~~/mocks/snmp'
import { resetMockState } from '~~/mocks/state'
import { widgetData } from '~~/mocks/widget-data'

/** Monitoreo de routers simulado (EP-08): estados, interfaces, series, sondeo y widget. */

const NOW = new Date('2026-10-08T15:00:00Z')
const fn = TENANTS.fibraNorte

beforeEach(() => resetMockState())

function memoryStorage() {
  const m = new Map<string, string>()
  return {
    getItem: (k: string) => m.get(k) ?? null,
    setItem: (k: string, v: string) => void m.set(k, v),
    removeItem: (k: string) => void m.delete(k),
  }
}

async function adminClient(clock: { now: Date }) {
  let session: string | null = null
  let scoped: { token: string } | null = null
  const api = createHorusClient(
    'http://horus.test/api/v1',
    createAuthFetch({
      fetch: createMockFetch({ latencyMs: 0, storage: memoryStorage(), now: () => clock.now }),
      basePath: '/api/v1',
      getAccessToken: (path) => tokenForPath(path, session, scoped),
    }),
  )
  const login = await unwrap(
    api.POST('/auth/login', {
      body: { username: 'ana.ruiz@fibranorte.example', password: MOCK_PASSWORD },
    }),
  )
  if (!('mfa_token' in login)) throw new Error('se esperaba 2FA')
  const verified = await unwrap(
    api.POST('/auth/mfa/verify', { body: { mfa_token: login.mfa_token, code: MOCK_TOTP_CODE } }),
  )
  session = verified.access_token
  scoped = {
    token: (await unwrap(api.POST('/auth/token', { body: { tenant_id: fn.tenant_id } })))
      .access_token,
  }
  return api
}

describe('estado efectivo de los routers (api.md §2.6)', () => {
  it('reparte online, warning/cpu_high, stale y unknown; problemas primero', () => {
    const list = routerStatusList(fn, NOW)
    const by = (s: string) => list.data.filter((r) => r.status === s)
    expect(list.data).toHaveLength(routersOf(fn, NOW).length)
    expect(by('online').length).toBeGreaterThanOrEqual(2)
    expect(by('warning')).toHaveLength(1)
    expect(by('warning')[0]!.reason).toBe('cpu_high')
    expect(by('warning')[0]!.device.cpu_percent).toBeGreaterThan(85)
    expect(by('warning')[0]!.thresholds[0]).toMatchObject({
      metric: 'cpu_percent',
      level: 'warning',
    })
    expect(by('stale')).toHaveLength(1)
    expect(new Date(by('stale')[0]!.last_observed_at!).getTime()).toBe(NOW.getTime() - 9 * 60_000)
    expect(by('unknown')).toHaveLength(1)
    expect(by('unknown')[0]!.device.sampled_at).toBeNull()
    expect(by('unknown')[0]!.signals).toMatchObject({ icmp: 'unknown', snmp: 'unknown' })
    expect(by('critical')).toHaveLength(0)
    expect(list.data[0]!.status).toBe('warning')
    expect(list.data.at(-1)!.status).toBe('online')
    expect(list.meta.poller).toMatchObject({ healthy: true, enabled: true, interval_seconds: 60 })
    expect(list.meta.poller.targets).toBe(list.data.length - 1)
    // Valores plausibles de la mayoría.
    for (const r of by('online')) {
      expect(r.device.cpu_percent!).toBeLessThan(40)
      expect(r.device.temperature_celsius!).toBeLessThan(60)
      expect(r.device.uptime_seconds!).toBeGreaterThan(86_400)
      expect(r.uplink!.name).toBe('sfp-sfpplus1')
      expect(r.uplink!.rx_bps!).toBeGreaterThan(r.uplink!.tx_bps!)
    }
  })

  it('el router con exportador silencioso está offline con sus señales; el crítico solo degradado', () => {
    const vc = routerStatusList(TENANTS.valleConecta, NOW)
    const off = vc.data.find((r) => r.status === 'offline')!
    expect(off.name).toBe('rt-ribera')
    expect(off.reason).toBe('tunnel_down')
    expect(off.signals).toMatchObject({
      icmp: 'fail',
      snmp: 'fail',
      wireguard: 'stale',
      exporter: 'silent',
    })
    expect(off.device.cpu_percent).toBeNull()
    expect(off.uplink!.rx_bps).toBeNull()
    expect(
      routerStatusList(fn, NOW, { degraded: true }).data.some((r) => r.status === 'critical'),
    ).toBe(true)
    const crit = routerStatusList(fn, NOW, { degraded: true }).data.find(
      (r) => r.status === 'critical',
    )!
    expect(crit.reason).toBe('temperature_high')
    expect(crit.device.temperature_celsius!).toBeGreaterThan(85)
    expect(routerStatusList(TENANTS.redAndina, NOW).data.some((r) => r.status === 'critical')).toBe(
      true,
    )
  })

  it('snmp-off: el sondeador está desactivado y nadie queda offline por falta de monitoreo', () => {
    const list = routerStatusList(fn, NOW, { scenario: 'snmp-off' })
    expect(list.meta.poller).toMatchObject({ healthy: false, enabled: false, targets: 0 })
    for (const r of list.data) expect(['stale', 'unknown']).toContain(r.status)
  })
})

describe('interfaces y series', () => {
  it('descubre 10 interfaces (sin <pppoe-…>) con un uplink y las caídas sin tasas', () => {
    const sr = findSnmpRouter(fn, '0192e333-0000-7000-8000-000000000102', NOW)!
    const inv = routerInterfaces(sr, NOW)
    expect(inv.data).toHaveLength(10)
    expect(inv.data.filter((i) => i.uplink)).toHaveLength(1)
    expect(inv.data.some((i) => i.name.includes('pppoe'))).toBe(false)
    const live = routerLiveMetrics(sr, NOW)
    expect(live.status.router_id).toBe(sr.item.router.id)
    expect(live.interfaces).toHaveLength(10)
    const down = live.interfaces.filter((i) => i.oper_status === 'down')
    expect(down).toHaveLength(2)
    for (const i of down) expect(i.rx_bps).toBeNull()
    const uplink = live.interfaces.find((i) => i.uplink)!
    expect(uplink.rx_bps).toBe(sr.status.uplink!.rx_bps)
    expect(
      live.interfaces.some((i) => (i.rx_errors_delta ?? 0) > 0 || (i.rx_drops_delta ?? 0) > 0),
    ).toBe(true)
  })

  it('las series del router apagado tienen huecos null (y disponibilidad 0), con meta de analítica', () => {
    const vc = TENANTS.valleConecta
    const off = findSnmpRouter(vc, '0192e333-0000-7000-8000-000000000202', NOW)!
    const s = routerDeviceSeries(off, NOW, '6h', ['cpu_percent', 'availability_ratio'])
    const cpu = s.data.series.find((x) => x.metric === 'cpu_percent')!
    expect(cpu.points.at(-1)![1]).toBeNull()
    expect(cpu.points[0]![1]).not.toBeNull()
    const avail = s.data.series.find((x) => x.metric === 'availability_ratio')!
    expect(avail.points.at(-1)![1]).toBe(0)
    expect(s.meta).toMatchObject({ step: 300, source: 'snmp', partial: false })
    const ok = findSnmpRouter(fn, '0192e333-0000-7000-8000-000000000101', NOW)!
    const i = routerInterfaceSeries(ok, NOW, '1h', [], ['rx_bps', 'tx_bps'])
    expect(i.data.series.map((x) => x.group)).toEqual(['sfp-sfpplus1', 'sfp-sfpplus1'])
    expect(i.data.series[0]!.points).toHaveLength(61)
    expect(i.data.series[0]!.points.every(([, v]) => typeof v === 'number')).toBe(true)
  })
})

describe('rutas: estado, sondeo y widget', () => {
  it('POST /routers/{id}/poll: 428 sin Idempotency-Key, 202 en cola y tras 2 s cambia sampled_at', async () => {
    const clock = { now: NOW }
    const api = await adminClient(clock)
    const id = '0192e333-0000-7000-8000-000000000101'
    const before = await unwrap(
      api.GET('/routers/{router_id}/metrics/live', { params: { path: { router_id: id } } }),
    )
    const missing = (await unwrap(
      api.POST('/routers/{router_id}/poll', {
        params: { path: { router_id: id }, header: {} as { 'Idempotency-Key': string } },
      }),
    ).catch((e: unknown) => e)) as ApiError
    expect(missing.status).toBe(428)
    const res = await unwrap(
      api.POST('/routers/{router_id}/poll', {
        params: { path: { router_id: id }, header: { 'Idempotency-Key': crypto.randomUUID() } },
      }),
    )
    expect(res).toEqual({ queued: true, reason: null })
    clock.now = new Date(NOW.getTime() + 2500)
    const after = await unwrap(
      api.GET('/routers/{router_id}/metrics/live', { params: { path: { router_id: id } } }),
    )
    expect(new Date(after.status.device.sampled_at!).getTime()).toBe(NOW.getTime() + 2000)
    expect(after.status.device.sampled_at).not.toBe(before.status.device.sampled_at)
    // Lista con filtros y análisis: el 404 de un router inexistente y el 422 de más de 8 interfaces.
    const list = await unwrap(
      api.GET('/routers/status', { params: { query: { status: 'warning' } } }),
    )
    expect(list.data.map((r) => r.status)).toEqual(['warning'])
    const tooMany = (await unwrap(
      api.GET('/analytics/routers/{router_id}/interfaces/metrics', {
        params: {
          path: { router_id: id },
          query: { interface_id: Array.from({ length: 9 }, (_, i) => `x${i}`).join(',') },
        },
      }),
    ).catch((e: unknown) => e)) as ApiError
    expect(tooMany.status).toBe(422)
    const missingRouter = (await unwrap(
      api.GET('/routers/{router_id}/interfaces', {
        params: { path: { router_id: '0192e333-0000-7000-8000-000000000999' } },
      }),
    ).catch((e: unknown) => e)) as ApiError
    expect(missingRouter.status).toBe(404)
  })

  it('el tiempo real emite routers.status al concluir un sondeo y router.{id}.metrics con snapshot', async () => {
    const realtime = createMockRealtime(5000, () => NOW, 60_000)
    const id = '0192e333-0000-7000-8000-000000000101'
    const events: string[] = []
    const stop = realtime.subscribe(fn.tenant_id, 'routers.status', (m) => events.push(m.type))
    const states: string[] = []
    const stopM = realtime.subscribe(fn.tenant_id, `router.${id}.metrics`, (m) =>
      states.push(m.topic),
    )
    await new Promise((r) => setTimeout(r, 10))
    expect(states).toEqual([`router.${id}.metrics`])
    const { notifySnmpChange } = await import('~~/mocks/snmp')
    notifySnmpChange(fn.tenant_id, id)
    notifySnmpChange(TENANTS.valleConecta.tenant_id, id)
    expect(events).toEqual(['event'])
    expect(states).toHaveLength(2)
    stop()
    stopM()
  })

  it('widget routers_status_grid: columnas exactas, filtros show/site_ids y orden', () => {
    const base = {
      tenant: fn,
      now: NOW,
      canSeePersonalData: true,
      empty: false,
    }
    const widget = (config: Record<string, unknown>) => ({
      id: 'w',
      type: 'routers_status_grid',
      title: null,
      position: { x: 0, y: 0, w: 6, h: 3 },
      config,
    })
    const d = widgetData({ ...base, widget: widget({}) }, { degraded: false })!
    expect(d.data.kind).toBe('table')
    if (d.data.kind !== 'table') return
    expect(d.data.columns.map((c) => c.key)).toEqual([
      'router',
      'site',
      'router_id',
      'status',
      'reason',
      'since',
      'last_observed_at',
      'cpu_percent',
      'memory_percent',
      'temperature_celsius',
      'uptime_seconds',
      'rx_bps',
      'tx_bps',
      'uplink',
      'exporter_state',
      'tunnel_state',
      'interfaces_up',
      'interfaces_total',
    ])
    const rows = d.data.rows as unknown as RouterStatusRow[]
    expect(rows).toHaveLength(routersOf(fn, NOW).length)
    expect(rows[0]!.status).toBe('warning')
    expect(Object.keys(rows[0]!)).not.toContain('name')
    const only = widgetData({ ...base, widget: widget({ show: ['online'] }) }, { degraded: false })!
    if (only.data.kind === 'table') for (const r of only.data.rows) expect(r.status).toBe('online')
    const byCpu = widgetData({ ...base, widget: widget({ sort: 'cpu' }) }, { degraded: false })!
    if (byCpu.data.kind === 'table') {
      const cpus = (byCpu.data.rows as unknown as RouterStatusRow[]).map((r) => r.cpu_percent ?? -1)
      expect(cpus).toEqual([...cpus].sort((a, b) => b - a))
    }
    const site = widgetData(
      { ...base, widget: widget({ site_ids: [fn.sites[0].id] }) },
      { degraded: false },
    )!
    if (site.data.kind === 'table') expect(site.data.rows).toHaveLength(1)
    const off = widgetData(
      { ...base, widget: widget({}) },
      { degraded: false, scenario: 'snmp-off' },
    )!
    if (off.data.kind === 'table')
      for (const r of off.data.rows) expect(['stale', 'unknown']).toContain(r.status)
  })
})
