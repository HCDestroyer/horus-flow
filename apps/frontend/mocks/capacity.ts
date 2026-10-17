import type {
  CapacityEgress,
  CapacityForecast,
  CapacityForecastSummary,
  CapacityOverview,
  CapacityProbe,
  CapacitySettings,
  CapacitySettingsValues,
  CapacitySite,
  CapacityPeriod,
  CapacitySubjectDetail,
  CapacityUplink,
  HealthStatus,
  NetwatchSnippet,
  SiteHealth,
  TrafficSplitReport,
  UplinkConfig,
  UplinkDaily,
} from '~~/types/api'
import { tenantScale } from './base'
import type { MockTenant } from './data'
import { forbidden, ifMatch, json, noContent, notFound, problem } from './http'
import { seededSites } from './inventory'
import { hash, mockUuid, rng } from './random'
import { interfaceId, snmpRouters, type SnmpOptions, type SnmpRouter } from './snmp'
import { mockState } from './state'
import type { TenantRouteContext } from './tenant-routes'

/**
 * Capacidad de enlaces y salud de nodos simuladas (`snmp.yaml`, tag `capacity`), derivadas
 * del mismo sondeo que `GET /routers/status` (`snmpRouters`): cada router activo tiene su
 * enlace de tránsito (`sfp-sfpplus1`, el que detecta el sondeador) y los dos primeros además
 * un IX local (`sfp-sfpplus2`). El primer enlace de cada ISP va cerca del límite (pico al
 * ~84 % de lo contratado, +4,5 % mensual) para que haya siempre una recomendación de
 * planificación que mirar; el primer router tiene además 2 % de pérdida hacia su puerta de
 * enlace. Pronóstico simplificado (exponencial sobre el pico) con el mismo texto que el rol
 * snmp. IPs de rangos de documentación: nunca clientes.
 */

const DAY = 86_400_000
const PROVIDERS = ['Proveedor Andino', 'Tránsito Global', 'Carrier Sur']

export const DEFAULT_CAPACITY_SETTINGS: CapacitySettingsValues = {
  country: null,
  planning_threshold: 0.8,
  urgent_threshold: 0.9,
  lead_days: 90,
  saturation_warning: 0.7,
  saturation_critical: 0.85,
  sustained_minutes: 15,
  loss_warning_pct: 1,
  loss_critical_pct: 5,
  latency_warning_ms: 80,
  latency_critical_ms: 200,
  local_asns: [],
  probe_targets: [],
}

const TENANT_COUNTRY: Record<string, string> = {
  'fibra-norte': 'GT',
  'valle-conecta': 'SV',
  'red-andina': 'PE',
}

function settingsOf(tenant: MockTenant) {
  return (
    mockState.capacitySettings.get(tenant.tenant_id) ?? {
      values: DEFAULT_CAPACITY_SETTINGS,
      version: 0,
      updated_at: '',
    }
  )
}

/** Pico diario del enlace de tránsito (bps): el máximo de la curva diaria del sondeo. */
function transitPeak(tenant: MockTenant) {
  const active = seededSites(tenant).filter((s) => s.role !== 'pending').length
  return 4.2e9 * tenantScale(tenant) * (1 / Math.max(1, active))
}

function activeRouters(tenant: MockTenant, now: Date, options: SnmpOptions) {
  return snmpRouters(tenant, now, options).filter((r) => r.profile !== 'unknown')
}

function seedConfigs(tenant: MockTenant, now: Date, options: SnmpOptions): UplinkConfig[] {
  const peak = transitPeak(tenant)
  const at = new Date(now.getTime() - 20 * DAY).toISOString()
  return activeRouters(tenant, now, options).flatMap((sr, i) => {
    const ratio = i === 0 ? 0.84 : i === 1 ? 0.62 : 0.45
    const contracted = Math.ceil(peak / ratio / 1e8) * 1e8
    const out: UplinkConfig[] = [
      {
        interface_id: interfaceId(sr.item.router.id, 9),
        router_id: sr.item.router.id,
        label: `${PROVIDERS[i % PROVIDERS.length]} · tránsito`,
        provider: PROVIDERS[i % PROVIDERS.length]!,
        kind: 'international_transit',
        contracted_bps: contracted,
        international_egress: true,
        updated_at: at,
        version: 1,
      },
    ]
    if (i < 2) {
      out.push({
        interface_id: interfaceId(sr.item.router.id, 10),
        router_id: sr.item.router.id,
        label: 'IX local',
        provider: null,
        kind: 'local_ix',
        contracted_bps: 10e9,
        international_egress: false,
        updated_at: at,
        version: 1,
      })
    }
    return out
  })
}

function configsOf(tenant: MockTenant, now: Date, options: SnmpOptions) {
  let list = mockState.capacityUplinks.get(tenant.tenant_id)
  if (!list) {
    list = seedConfigs(tenant, now, options)
    mockState.capacityUplinks.set(tenant.tenant_id, list)
  }
  return list
}

interface UplinkSeed {
  sr: SnmpRouter
  index: number
  ifIndex: number
  name: string
  config: UplinkConfig | null
  /** Pico de hoy (bps) y crecimiento mensual. */
  peak: number
  growth: number
  /** Tráfico ahora. */
  rx: number
  tx: number
}

function uplinkSeeds(tenant: MockTenant, now: Date, options: SnmpOptions): UplinkSeed[] {
  const cfgs = configsOf(tenant, now, options)
  const peak = transitPeak(tenant)
  return activeRouters(tenant, now, options).flatMap((sr, i) => {
    const seeds: UplinkSeed[] = []
    for (const [ifIndex, name, share] of [
      [9, 'sfp-sfpplus1', 1],
      [10, 'sfp-sfpplus2', 0.12],
    ] as const) {
      const id = interfaceId(sr.item.router.id, ifIndex)
      const config = cfgs.find((c) => c.interface_id === id) ?? null
      if (ifIndex === 10 && !config) continue
      seeds.push({
        sr,
        index: i,
        ifIndex,
        name,
        config,
        peak: peak * share,
        growth: i === 0 && ifIndex === 9 ? 0.045 : 0.02,
        rx: sr.status.uplink?.rx_bps != null ? sr.status.uplink.rx_bps * share : 0,
        tx: sr.status.uplink?.tx_bps != null ? sr.status.uplink.tx_bps * share : 0,
      })
    }
    return seeds
  })
}

/** Serie diaria determinista: pico con crecimiento, fin de semana +12 % y ruido ±5 %. */
function dailySeries(
  seed: UplinkSeed | null,
  peak: number,
  growth: number,
  capacity: number,
  now: Date,
  days: number,
) {
  const out: UplinkDaily[] = []
  const today = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate())
  const key = seed ? seed.sr.item.router.id + seed.ifIndex : 'egress'
  for (let d = days - 1; d >= 0; d--) {
    const day = today - d * DAY
    const r = rng(hash(key + day))
    const wd = new Date(day).getUTCDay()
    const v =
      peak *
      (1 + growth) ** (-d / 30.4375) *
      (wd === 0 || wd === 6 ? 1.06 : 0.97) *
      (0.95 + r() * 0.1)
    const hoursAbove = (th: number) =>
      capacity && v / capacity >= th ? Math.round((v / capacity - th) * 40 * 4) / 4 : 0
    out.push({
      day: new Date(day).toISOString(),
      samples: 288,
      rx_p95_bps: Math.round(v),
      tx_p95_bps: Math.round(v * 0.19),
      rx_max_bps: Math.round(v * 1.08),
      tx_max_bps: Math.round(v * 0.21),
      capacity_bps: capacity,
      hours_above_warning: hoursAbove(0.7),
      hours_above_critical: hoursAbove(0.85),
      errors: 0,
      drops: Math.round(r() * 40),
      packets: Math.round((v / 8 / 900) * 86_400 * 0.6),
    })
  }
  return out
}

function fmtBps(v: number) {
  const units: [number, string][] = [
    [1e12, 'Tb/s'],
    [1e9, 'Gb/s'],
    [1e6, 'Mb/s'],
    [1e3, 'kb/s'],
  ]
  for (const [div, sym] of units) {
    if (v >= div) {
      const q = v / div
      return q < 10 ? `${q.toFixed(1).replace('.', ',')} ${sym}` : `${Math.round(q)} ${sym}`
    }
  }
  return `${Math.round(v)} b/s`
}
const pct = (r: number) => `${Math.round(r * 100)} %`
const signed = (r: number) => {
  const p = r * 100
  const s = p < 0 ? '−' : '+'
  const a = Math.abs(p)
  return a < 10 ? `${s}${a.toFixed(1).replace('.', ',')} %` : `${s}${Math.round(a)} %`
}
const month = (iso: string) => `${iso.slice(5, 7)}/${iso.slice(0, 4)}`

/** Pronóstico simplificado: exponencial sobre el pico de tendencia (el día más cargado). */
function forecastOf(
  subjectId: string,
  kind: 'uplink' | 'international_egress',
  label: string,
  daily: UplinkDaily[],
  peak: number,
  growth: number,
  capacity: number,
  st: CapacitySettingsValues,
  now: Date,
): { full: CapacityForecast; summary: CapacityForecastSummary } {
  const daysPerMonth = 30.4375
  const b = Math.log1p(growth) / daysPerMonth
  const current = peak * 1.06
  const last = daily.at(-1)?.day ?? now.toISOString()
  const lastMs = new Date(last).getTime()
  const cross = (th: number) => {
    const target = th * capacity
    if (!capacity) return { threshold: th, date: null, days_until: null, already: false }
    if (current >= target)
      return {
        threshold: th,
        date: new Date(
          Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()),
        ).toISOString(),
        days_until: 0,
        already: true,
      }
    const h = Math.log(target / current) / b
    if (h > 3 * 365) return { threshold: th, date: null, days_until: null, already: false }
    const date = new Date(lastMs + h * DAY)
    date.setUTCHours(0, 0, 0, 0)
    return {
      threshold: th,
      date: date.toISOString(),
      days_until: Math.max(0, Math.ceil((date.getTime() - now.getTime()) / DAY)),
      already: false,
    }
  }
  const planning = cross(st.planning_threshold)
  const urgent = cross(st.urgent_threshold)
  const within = (c: { days_until: number | null }) =>
    c.days_until !== null && c.days_until <= st.lead_days
  const level = !capacity
    ? 'no_capacity'
    : within(urgent)
      ? 'urgent'
      : within(planning)
        ? 'planning'
        : 'ok'
  const p6 = current * Math.exp(b * 182)
  const subject =
    kind === 'international_egress' ? 'de salida internacional' : `del enlace «${label}»`
  const when = (c: typeof planning) =>
    c.already
      ? `ya supera el ${pct(c.threshold)}`
      : c.date
        ? `cruza el ${pct(c.threshold)} hacia ${month(c.date)}`
        : `no cruza el ${pct(c.threshold)} en 36 meses`
  const six = `en 6 meses ~${fmtBps(p6)} (${signed(p6 / current - 1)})`
  const recommendation =
    level === 'urgent'
      ? `Upgrade urgente ${subject}: pico ${fmtBps(current)}, ${six}, ${when(urgent)}.`
      : level === 'planning'
        ? `Planificar upgrade ${subject}: pico ${fmtBps(current)}, ${six}, ${when(planning)}.`
        : level === 'no_capacity'
          ? `Configure la capacidad contratada para estimar fechas: pico ${fmtBps(current)}, tendencia ${signed(growth)} mensual.`
          : `Sin acción ${subject} por ahora: pico ${fmtBps(current)} (${pct(current / capacity)} de la capacidad), ${signed(growth)} mensual, ${when(planning)}.`
  const projection = Array.from({ length: 53 }, (_, i) => {
    const h = i * 7
    return {
      day: new Date(lastMs + h * DAY).toISOString(),
      bps: Math.round(current * Math.exp(b * h)),
      low_bps: Math.round(current * Math.exp(b * 0.6 * h) * 0.9),
      high_bps: Math.round(current * Math.exp(b * 1.4 * h) * 1.1),
    }
  })
  const full: CapacityForecast = {
    level,
    method: 'theil_sen_log_weekly',
    history_days: daily.length,
    first_day: daily[0]?.day ?? null,
    last_day: last,
    seasonality: daily.length >= 21,
    confidence: daily.length >= 84 ? 'high' : 'medium',
    capacity_bps: capacity,
    observed_peak_bps: Math.max(...daily.slice(-7).map((d) => d.rx_p95_bps)),
    current_peak_bps: current,
    monthly_growth: growth,
    monthly_growth_low: growth * 0.6,
    monthly_growth_high: growth * 1.4,
    peak_6m_bps: p6,
    growth_6m: p6 / current - 1,
    planning,
    urgent,
    lead_days: st.lead_days,
    projection,
  }
  const summary: CapacityForecastSummary = {
    subject_id: subjectId,
    subject_kind: kind,
    level,
    confidence: full.confidence,
    computed_at: new Date(
      Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate(), 6, 20),
    ).toISOString(),
    history_days: full.history_days,
    seasonality: full.seasonality,
    capacity_bps: capacity,
    current_peak_bps: current,
    monthly_growth: growth,
    peak_6m_bps: p6,
    growth_6m: full.growth_6m,
    planning,
    urgent,
    lead_days: st.lead_days,
    recommendation,
  }
  return { full, summary }
}

function gatewayProbe(seed: UplinkSeed, now: Date): CapacityProbe {
  const r = rng(hash(seed.sr.item.router.id + 'gw' + seed.ifIndex))
  const loss = seed.index === 0 && seed.ifIndex === 9 ? 2.1 : Math.round(r() * 30) / 100
  const rtt = Math.round((3 + r() * 6) * 10) / 10
  return {
    probe_id: mockUuid('0195c100', hash(seed.sr.item.router.id + seed.ifIndex)),
    source: 'router_netwatch',
    ref: 'gw',
    target: `198.51.100.${1 + (hash(seed.sr.item.router.id) % 200)}`,
    label: `Puerta de enlace por ${seed.name}`,
    router_id: seed.sr.item.router.id,
    interface_id: interfaceId(seed.sr.item.router.id, seed.ifIndex),
    status: 'up',
    last_at: new Date(now.getTime() - 25_000).toISOString(),
    loss_pct: loss,
    loss_pct_max: loss * 3,
    rtt_avg_ms: rtt,
    jitter_ms: Math.round(r() * 15) / 10,
    last_loss_pct: loss,
    last_rtt_ms: rtt,
    last_jitter_ms: 0.8,
  }
}

function intlProbes(sr: SnmpRouter, now: Date): CapacityProbe[] {
  return ['1.1.1.1', '8.8.8.8'].map((host, i) => ({
    probe_id: mockUuid('0195c200', hash(sr.item.router.id + host)),
    source: 'router_netwatch',
    ref: 'intl',
    target: host,
    label: `Internacional ${host}`,
    router_id: sr.item.router.id,
    interface_id: null,
    status: 'up',
    last_at: new Date(now.getTime() - 25_000).toISOString(),
    loss_pct: 0,
    loss_pct_max: 1,
    rtt_avg_ms: 52 + i * 6,
    jitter_ms: 1.4,
    last_loss_pct: 0,
    last_rtt_ms: 52 + i * 6,
    last_jitter_ms: 1.2,
  }))
}

function capacityOf(seed: UplinkSeed) {
  return seed.config?.contracted_bps ?? 10e9
}

function uplinkView(
  seed: UplinkSeed,
  st: CapacitySettingsValues,
  now: Date,
  days: number,
): CapacityUplink {
  const capacity = capacityOf(seed)
  const daily = dailySeries(seed, seed.peak, seed.growth, capacity, now, days)
  const worst = daily.reduce((m, d) => Math.max(m, d.rx_p95_bps, d.tx_p95_bps), 0)
  const id = interfaceId(seed.sr.item.router.id, seed.ifIndex)
  const { summary } = forecastOf(
    id,
    'uplink',
    seed.config?.label ?? seed.name,
    dailySeries(seed, seed.peak, seed.growth, capacity, now, 120),
    seed.peak,
    seed.growth,
    capacity,
    st,
    now,
  )
  const util = Math.max(seed.rx, seed.tx) / capacity
  const utilP95 = worst / capacity
  return {
    interface_id: id,
    router_id: seed.sr.item.router.id,
    router_name: seed.sr.item.router.name,
    site_id: seed.sr.item.site.id,
    site_name: seed.sr.item.site.name,
    interface_name: seed.name,
    configured: !!seed.config,
    auto_detected: seed.ifIndex === 9,
    label: seed.config?.label ?? seed.name,
    provider: seed.config?.provider ?? null,
    kind: seed.config?.kind ?? null,
    international_egress: seed.config?.international_egress ?? false,
    contracted_bps: seed.config?.contracted_bps ?? null,
    speed_bps: 10e9,
    capacity_bps: capacity,
    capacity_source: seed.config?.contracted_bps ? 'contracted' : 'port_speed',
    oper_status: 'up',
    rx_bps: seed.rx || null,
    tx_bps: seed.tx || null,
    utilization: seed.rx ? Math.round(util * 1000) / 1000 : null,
    sampled_at: new Date(now.getTime() - 20_000).toISOString(),
    window: {
      days,
      from: daily[0]!.day,
      to: daily.at(-1)!.day,
      samples: daily.length * 288,
      rx_p95_bps: Math.max(...daily.map((d) => d.rx_p95_bps)),
      tx_p95_bps: Math.max(...daily.map((d) => d.tx_p95_bps)),
      util_p95: Math.round(utilP95 * 1000) / 1000,
      hours_above_warning: daily.reduce((s, d) => s + d.hours_above_warning, 0),
      hours_above_critical: daily.reduce((s, d) => s + d.hours_above_critical, 0),
      errors: 0,
      drops: daily.reduce((s, d) => s + d.drops, 0),
    },
    diagnosis: {
      kind:
        utilP95 >= st.saturation_critical
          ? 'full'
          : utilP95 >= st.saturation_warning
            ? 'busy'
            : 'ok',
      error_ratio: 0,
      drop_ratio: 0,
    },
    probe: seed.ifIndex === 9 ? gatewayProbe(seed, now) : null,
    conditions: [],
    forecast: summary,
    version: seed.config?.version ?? 0,
  }
}

function siteHealth(
  uplinks: CapacityUplink[],
  routers: SnmpRouter[],
  st: CapacitySettingsValues,
): SiteHealth {
  const reasons: SiteHealth['reasons'] = []
  const rank: Record<HealthStatus, number> = {
    unknown: 0,
    online: 1,
    warning: 2,
    degraded: 3,
    critical: 4,
    offline: 5,
  }
  for (const u of uplinks) {
    const parts: string[] = []
    let status: HealthStatus | null = null
    const util = u.window?.util_p95 ?? null
    if (util !== null && util >= st.saturation_critical) {
      status = 'critical'
      parts.push(`al ${pct(util)} en hora pico`)
    } else if (util !== null && util >= st.saturation_warning) {
      status = 'warning'
      parts.push(`al ${pct(util)} en hora pico`)
    }
    const loss = u.probe?.loss_pct ?? null
    if (loss !== null && loss >= st.loss_warning_pct) {
      const s: HealthStatus = loss >= st.loss_critical_pct ? 'critical' : 'degraded'
      status = !status || rank[s] > rank[status] ? s : status
      parts.push(`${loss.toFixed(1).replace('.', ',')} % de pérdida`)
    }
    if (status) {
      const text = `enlace ${u.label} ${parts.length > 1 ? `${parts.slice(0, -1).join(', ')} y ${parts.at(-1)}` : parts[0]}`
      reasons.push({
        status,
        code:
          util !== null && util >= st.saturation_warning ? 'uplink_saturated' : 'uplink_quality',
        text,
      })
    }
  }
  for (const sr of routers) {
    const s = sr.status.status
    if (s === 'warning' || s === 'critical' || s === 'degraded' || s === 'offline') {
      const label = {
        warning: 'aviso',
        critical: 'crítico',
        degraded: 'degradado',
        offline: 'fuera de servicio',
      }[s]
      reasons.push({
        status: s === 'offline' ? 'critical' : s,
        code: `router_${s}`,
        text: `router ${sr.item.router.name} ${label}${sr.status.reason && sr.status.reason !== 'ok' ? ` (${sr.status.reason.replaceAll('_', ' ')})` : ''}`,
      })
    }
  }
  reasons.sort((a, b) => rank[b.status] - rank[a.status])
  const known =
    uplinks.length > 0 ||
    routers.some((r) => r.status.status !== 'unknown' && r.status.status !== 'stale')
  const status = reasons[0]?.status ?? (known ? 'online' : 'unknown')
  const labels: Record<HealthStatus, string> = {
    online: 'Sano',
    warning: 'Aviso',
    degraded: 'Degradado',
    critical: 'Crítico',
    offline: 'Fuera de servicio',
    unknown: 'Sin datos',
  }
  const summary = reasons.length
    ? `${labels[status]}: ${reasons
        .slice(0, 3)
        .map((r) => r.text)
        .join('; ')}${reasons.length > 3 ? ` (y ${reasons.length - 3} más)` : ''}`
    : known
      ? 'Sano: enlaces, routers y servicios dentro de los umbrales'
      : 'Sin datos: el nodo no tiene enlaces, routers ni servicios observados'
  return { status, summary, reasons }
}

function egressSubject(tenant: MockTenant) {
  return mockUuid('0195c300', hash(tenant.tenant_id))
}

function egressOf(
  tenant: MockTenant,
  seeds: UplinkSeed[],
  st: CapacitySettingsValues,
  now: Date,
  days: number,
) {
  const members = seeds.filter((s) => s.config?.international_egress)
  if (!members.length) return null
  const capacity = members.reduce((s, m) => s + capacityOf(m), 0)
  const peak = members.reduce((s, m) => s + m.peak, 0)
  const growth = members.reduce((s, m) => s + m.growth * m.peak, 0) / peak
  const daily = dailySeries(null, peak, growth, capacity, now, days)
  const id = egressSubject(tenant)
  const fc = forecastOf(
    id,
    'international_egress',
    'Salida internacional',
    dailySeries(null, peak, growth, capacity, now, 120),
    peak,
    growth,
    capacity,
    st,
    now,
  )
  const rx = members.reduce((s, m) => s + m.rx, 0)
  const tx = members.reduce((s, m) => s + m.tx, 0)
  const egress: CapacityEgress = {
    subject_id: id,
    members: members.map((m) => interfaceId(m.sr.item.router.id, m.ifIndex)),
    capacity_bps: capacity,
    rx_bps: rx,
    tx_bps: tx,
    utilization: Math.round((Math.max(rx, tx) / capacity) * 1000) / 1000,
    window: {
      days,
      from: daily[0]!.day,
      to: daily.at(-1)!.day,
      samples: daily.length * 288,
      rx_p95_bps: Math.max(...daily.map((d) => d.rx_p95_bps)),
      tx_p95_bps: Math.max(...daily.map((d) => d.tx_p95_bps)),
      util_p95: Math.round((Math.max(...daily.map((d) => d.rx_p95_bps)) / capacity) * 1000) / 1000,
      hours_above_warning: daily.reduce((s, d) => s + d.hours_above_warning, 0),
      hours_above_critical: daily.reduce((s, d) => s + d.hours_above_critical, 0),
      errors: 0,
      drops: 0,
    },
    forecast: fc.summary,
  }
  return { egress, daily: dailySeries(null, peak, growth, capacity, now, 365), forecast: fc }
}

export function capacityOverview(
  tenant: MockTenant,
  now: Date,
  options: SnmpOptions = {},
  days = 7,
): CapacityOverview {
  const st = settingsOf(tenant).values
  const seeds = uplinkSeeds(tenant, now, options)
  const routers = snmpRouters(tenant, now, options)
  const bySite = new Map<string, CapacitySite>()
  for (const sr of routers) {
    const site = bySite.get(sr.item.site.id) ?? {
      site_id: sr.item.site.id,
      site_name: sr.item.site.name,
      health: { status: 'unknown', summary: '', reasons: [] },
      uplinks: [],
      router_probes: [],
      routers: [],
    }
    site.routers.push({
      router_id: sr.item.router.id,
      name: sr.item.router.name,
      status: sr.status.status,
      probes_error:
        sr.profile === 'unknown'
          ? 'no_tunnel'
          : sr.profile === 'stale'
            ? 'tls_fingerprint_not_pinned'
            : null,
    })
    if (sr.profile !== 'unknown' && sr.profile !== 'stale')
      site.router_probes.push(...intlProbes(sr, now))
    bySite.set(sr.item.site.id, site)
  }
  for (const seed of seeds)
    bySite.get(seed.sr.item.site.id)?.uplinks.push(uplinkView(seed, st, now, days))
  const data = [...bySite.values()].map((s) => ({
    ...s,
    health: siteHealth(
      s.uplinks,
      routers.filter((r) => r.item.site.id === s.site_id),
      st,
    ),
  }))
  const rank: Record<string, number> = {
    offline: 0,
    critical: 1,
    degraded: 2,
    warning: 3,
    unknown: 4,
    online: 5,
  }
  data.sort(
    (a, b) =>
      (rank[a.health.status] ?? 4) - (rank[b.health.status] ?? 4) ||
      a.site_name.localeCompare(b.site_name, 'es'),
  )
  const horus: CapacityProbe[] = st.probe_targets.map((p) => ({
    probe_id: mockUuid('0195c400', hash(tenant.tenant_id + p.address)),
    source: 'horus',
    ref: 'horus',
    target: p.address,
    label: p.label,
    router_id: null,
    interface_id: null,
    status: 'up',
    last_at: new Date(now.getTime() - 30_000).toISOString(),
    loss_pct: 0.4,
    loss_pct_max: 2,
    rtt_avg_ms: 38,
    jitter_ms: 2.1,
    last_loss_pct: 0,
    last_rtt_ms: 37.5,
    last_jitter_ms: 1.9,
  }))
  return {
    data,
    egress: egressOf(tenant, seeds, st, now, days)?.egress ?? null,
    horus_probes: horus,
    settings: st,
    meta: { generated_at: now.toISOString(), window_days: days, history: true, store: true },
  }
}

function settingsView(tenant: MockTenant): CapacitySettings {
  const s = settingsOf(tenant)
  const tenantCountry = TENANT_COUNTRY[tenant.tenant_slug] ?? null
  return {
    ...s.values,
    defaults: DEFAULT_CAPACITY_SETTINGS,
    customized: s.version > 0,
    version: s.version,
    updated_at: s.updated_at || null,
    tenant_country: tenantCountry,
    effective_country: s.values.country ?? tenantCountry,
  }
}

function invalid(errors: { field: string; code: string; message: string }[]) {
  return problem(422, 'VALIDATION_FAILED', 'Revisa los campos marcados', {}, { errors })
}

function validateSettings(b: Record<string, unknown>) {
  const errors: { field: string; code: string; message: string }[] = []
  const ratio = (k: string) => {
    const v = Number(b[k])
    if (!(v >= 0.3 && v <= 1))
      errors.push({ field: k, code: 'OUT_OF_RANGE', message: 'Proporción entre 0,3 y 1.' })
    return v
  }
  const p = ratio('planning_threshold')
  const u = ratio('urgent_threshold')
  const w = ratio('saturation_warning')
  const c = ratio('saturation_critical')
  if (p > u)
    errors.push({
      field: 'planning_threshold',
      code: 'OUT_OF_RANGE',
      message: 'El umbral de planificación no puede superar al urgente.',
    })
  if (w > c)
    errors.push({
      field: 'saturation_warning',
      code: 'OUT_OF_RANGE',
      message: 'El aviso de saturación no puede superar al crítico.',
    })
  const lead = Number(b.lead_days)
  if (!(lead >= 7 && lead <= 365))
    errors.push({
      field: 'lead_days',
      code: 'OUT_OF_RANGE',
      message: 'Antelación entre 7 y 365 días.',
    })
  for (const t of (b.probe_targets as { address?: string }[] | undefined) ?? []) {
    if (String(t.address ?? '').startsWith('10.255.')) {
      errors.push({
        field: 'probe_targets[0].address',
        code: 'INVALID_VALUE',
        message: 'Dirección interna de la instalación de Horus.',
      })
    }
  }
  return errors
}

function trafficSplit(
  tenant: MockTenant,
  now: Date,
  days: number,
  site: string | null,
  options: SnmpOptions,
): TrafficSplitReport {
  const st = settingsOf(tenant).values
  const country = st.country ?? TENANT_COUNTRY[tenant.tenant_slug] ?? null
  const sites = [
    ...new Map(
      snmpRouters(tenant, now, options).map((r) => [r.item.site.id, r.item.site]),
    ).values(),
  ].filter((s) => !site || s.id === site)
  const today = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate())
  const base = ((transitPeak(tenant) * 0.45) / 8) * 86_400 // bytes por día
  const listShare = st.local_asns.length ? 0.14 : 0
  const daily: TrafficSplitReport['daily'] = []
  const totals = new Map<
    string,
    { local: number; country: number; list: number; intl: number; unknown: number }
  >()
  for (let d = days; d >= 1; d--) {
    const day = today - d * DAY
    let local = 0
    let intl = 0
    let unknown = 0
    for (const s of sites) {
      const r = rng(hash(s.id + day))
      const vol = base * (0.9 + r() * 0.2)
      const lc = country ? vol * 0.2 : 0
      const ll = vol * listShare
      const iu = vol * 0.03
      const it = vol - lc - ll - iu
      const t = totals.get(s.id) ?? { local: 0, country: 0, list: 0, intl: 0, unknown: 0 }
      t.local += lc + ll
      t.country += lc
      t.list += ll
      t.intl += it
      t.unknown += iu
      totals.set(s.id, t)
      local += lc + ll
      intl += it
      unknown += iu
    }
    daily.push({
      day: new Date(day).toISOString(),
      local_bytes: Math.round(local),
      international_bytes: Math.round(intl),
      unknown_bytes: Math.round(unknown),
    })
  }
  const split = (t: {
    local: number
    country: number
    list: number
    intl: number
    unknown: number
  }) => {
    const known = t.local + t.intl
    return {
      local_bytes: Math.round(t.local),
      local_by_country_bytes: Math.round(t.country),
      local_by_list_bytes: Math.round(t.list),
      international_bytes: Math.round(t.intl),
      unknown_bytes: Math.round(t.unknown),
      international_share: known ? t.intl / known : null,
      local_share: known ? t.local / known : null,
    }
  }
  const all = [...totals.values()].reduce(
    (a, t) => ({
      local: a.local + t.local,
      country: a.country + t.country,
      list: a.list + t.list,
      intl: a.intl + t.intl,
      unknown: a.unknown + t.unknown,
    }),
    { local: 0, country: 0, list: 0, intl: 0, unknown: 0 },
  )
  return {
    from: new Date(today - days * DAY).toISOString(),
    to: new Date(today).toISOString(),
    days,
    country,
    country_source: st.country ? 'settings' : country ? 'tenant' : 'none',
    local_asns: st.local_asns.length,
    total: split(all),
    sites: sites.map((s) => ({ site_id: s.id, site_name: s.name, ...split(totals.get(s.id)!) })),
    daily,
  }
}

function snippetOf(sr: SnmpRouter, seeds: UplinkSeed[]): NetwatchSnippet {
  const ups = seeds.filter((s) => s.sr.item.router.id === sr.item.router.id)
  const lines = [
    '# ===== Horus Flow · sondas de pérdida, latencia y jitter por enlace · RouterOS v7 =====',
    `# Router: ${sr.item.router.name}`,
    '# Revise el script antes de pegarlo. Horus nunca escribe en el router: estas sondas las crea usted y',
    '# Horus solo las LEE (usuario de solo lectura, API REST por el túnel). No ejecutan scripts.',
    '# Sustituya cada {GATEWAY_n} por la IP de la puerta de enlace del proveedor en ese enlace.',
    '# Deshacer: /tool netwatch remove [find comment~"^horus-uplink"]',
    '',
  ]
  ups.forEach((u, i) => {
    lines.push(`# Enlace ${u.name} · ${u.config?.label ?? u.name}`)
    lines.push(
      `/tool netwatch add type=icmp host={GATEWAY_${i + 1}} interval=30s packet-count=10 packet-interval=50ms \\`,
    )
    lines.push(`    comment="horus-uplink;if=${u.name};ref=gw"`)
  })
  lines.push(
    '',
    '# Referencias internacionales (miden la salida del router por su ruta por defecto, no un enlace concreto)',
  )
  for (const host of ['1.1.1.1', '8.8.8.8']) {
    lines.push(
      `/tool netwatch add type=icmp host=${host} interval=30s packet-count=10 packet-interval=50ms \\`,
    )
    lines.push(`    comment="horus-uplink;ref=intl-${host}"`)
  }
  return {
    router_id: sr.item.router.id,
    router_name: sr.item.router.name,
    script: lines.join('\n') + '\n',
    placeholders: ups.map((u, i) => ({
      key: `{GATEWAY_${i + 1}}`,
      interface: u.name,
      description: `IP de la puerta de enlace del proveedor por ${u.name} (${u.config?.label ?? u.name})`,
    })),
    warnings: ups.length
      ? []
      : [
          'El router no tiene enlaces de subida detectados ni configurados: solo se generan las referencias internacionales.',
        ],
    probes_found: sr.profile === 'stale' ? 0 : 1 + 2,
    probes_error: sr.profile === 'stale' ? 'tls_fingerprint_not_pinned' : null,
  }
}

function daysParam(ctx: TenantRouteContext, def: number, lo: number, hi: number) {
  const raw = ctx.url.searchParams.get('days')
  if (!raw) return def
  const n = Number(raw)
  return Number.isInteger(n) && n >= lo && n <= hi ? n : null
}

const badDays = (lo: number, hi: number) =>
  problem(
    400,
    'VALIDATION_FAILED',
    'Revisa los campos marcados',
    {},
    {
      errors: [{ field: 'days', code: 'OUT_OF_RANGE', message: `Entre ${lo} y ${hi}.` }],
    },
  )

export function capacityRoutes(ctx: TenantRouteContext): Response | undefined {
  const { tenant, now } = ctx
  const options: SnmpOptions = {
    degraded: ctx.degraded,
    stepMs: ctx.stepMs,
    scenario: ctx.scenario,
  }
  const path = ctx.path
  const snippet = path.match(/^\/routers\/([^/]+)\/netwatch-snippet$/)
  if (!path.startsWith('/capacity/') && !snippet) return
  if (snippet) {
    if (ctx.method !== 'GET') return
    if (!ctx.can('snmp.manage')) return forbidden()
    const sr = snmpRouters(tenant, now, options).find((r) => r.item.router.id === snippet[1])
    if (!sr) return notFound()
    return json(snippetOf(sr, uplinkSeeds(tenant, now, options)))
  }
  if (!ctx.can('snmp.read')) return forbidden()
  if (path === '/capacity/overview' && ctx.method === 'GET') {
    const d = daysParam(ctx, 7, 1, 30)
    if (d === null) return badDays(1, 30)
    const ov = capacityOverview(tenant, now, options, d)
    const site = ctx.url.searchParams.get('site_id')
    if (site) ov.data = ov.data.filter((s) => s.site_id === site)
    if (ctx.empty) ov.data = []
    return json(ov)
  }
  if (path === '/capacity/traffic-split' && ctx.method === 'GET') {
    const d = daysParam(ctx, 30, 1, 90)
    if (d === null) return badDays(1, 90)
    return json(trafficSplit(tenant, now, d, ctx.url.searchParams.get('site_id'), options))
  }
  if (path === '/capacity/settings') {
    if (ctx.method === 'GET')
      return json(settingsView(tenant), 200, { ETag: `"${settingsOf(tenant).version}"` })
    if (!ctx.can('snmp.manage')) return forbidden()
    if (ctx.method === 'DELETE') {
      mockState.capacitySettings.delete(tenant.tenant_id)
      return json(settingsView(tenant), 200, { ETag: '"0"' })
    }
    if (ctx.method === 'PUT') {
      const current = settingsOf(tenant)
      const expect = ifMatch(ctx.req)
      if (expect !== null && expect !== current.version)
        return problem(412, 'PRECONDITION_FAILED', 'La versión cambió')
      const errors = validateSettings(ctx.body)
      if (errors.length) return invalid(errors)
      const b = ctx.body as unknown as CapacitySettingsValues
      const values: CapacitySettingsValues = {
        ...DEFAULT_CAPACITY_SETTINGS,
        ...b,
        country: b.country ? String(b.country).toUpperCase() : null,
        local_asns: [...new Set(b.local_asns ?? [])].sort((x, y) => x - y),
        probe_targets: b.probe_targets ?? [],
      }
      mockState.capacitySettings.set(tenant.tenant_id, {
        values,
        version: current.version + 1,
        updated_at: now.toISOString(),
      })
      return json(settingsView(tenant), 200, { ETag: `"${current.version + 1}"` })
    }
    return
  }
  if (path === '/capacity/egress' && ctx.method === 'GET') {
    const d = daysParam(ctx, 90, 7, 365)
    if (d === null) return badDays(7, 365)
    const st = settingsOf(tenant).values
    const eg = egressOf(tenant, uplinkSeeds(tenant, now, options), st, now, d)
    if (!eg) return notFound('UPLINK_NOT_FOUND')
    const detail: CapacitySubjectDetail = {
      subject_id: eg.egress.subject_id,
      subject_kind: 'international_egress',
      uplink: null,
      egress: eg.egress,
      days: d,
      daily: eg.daily.slice(-d),
      forecast: eg.forecast.full,
      recommendation: eg.forecast.summary.recommendation,
      computed_at: eg.forecast.summary.computed_at,
      probe_series: [],
      settings: st,
      capacity_history: [],
    }
    return json(detail)
  }
  const m = path.match(/^\/capacity\/uplinks\/([^/]+)$/)
  if (!m) return
  const id = m[1]!
  const seeds = uplinkSeeds(tenant, now, options)
  const st = settingsOf(tenant).values
  if (ctx.method === 'GET') {
    const seed = seeds.find((s) => interfaceId(s.sr.item.router.id, s.ifIndex) === id)
    if (!seed) return notFound('UPLINK_NOT_FOUND')
    const d = daysParam(ctx, 90, 7, 365)
    if (d === null) return badDays(7, 365)
    const view = uplinkView(seed, st, now, 7)
    const capacity = capacityOf(seed)
    const fc = forecastOf(
      id,
      'uplink',
      view.label,
      dailySeries(seed, seed.peak, seed.growth, capacity, now, 120),
      seed.peak,
      seed.growth,
      capacity,
      st,
      now,
    )
    const probe = view.probe
    // Historia: los enlaces con capacidad contratada tuvieron la mitad hasta hace 45 días
    // (upgrade): esos días se midieron contra la capacidad de entonces.
    const contracted = seed.config?.contracted_bps ?? null
    const upgradeAt = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()) - 45 * DAY
    const history: CapacityPeriod[] = contracted
      ? [
          {
            valid_from: new Date(upgradeAt).toISOString(),
            valid_to: null,
            contracted_bps: contracted,
          },
          {
            valid_from: new Date(upgradeAt - 365 * DAY).toISOString(),
            valid_to: new Date(upgradeAt).toISOString(),
            contracted_bps: Math.round(contracted / 2),
          },
        ]
      : []
    const daily = dailySeries(seed, seed.peak, seed.growth, capacity, now, d).map((x) =>
      contracted && Date.parse(x.day) < upgradeAt
        ? { ...x, capacity_bps: Math.round(contracted / 2) }
        : x,
    )
    const detail: CapacitySubjectDetail = {
      subject_id: id,
      subject_kind: 'uplink',
      uplink: view,
      egress: null,
      days: d,
      daily,
      capacity_history: history,
      forecast: fc.full,
      recommendation: fc.summary.recommendation,
      computed_at: fc.summary.computed_at,
      probe_series: probe
        ? [
            {
              probe_id: probe.probe_id,
              label: probe.label,
              target: probe.target,
              points: Array.from({ length: Math.min(d, 30) * 24 }, (_, i) => {
                const at = now.getTime() - (Math.min(d, 30) * 24 - i) * 3_600_000
                const r = rng(hash(probe.probe_id + Math.floor(at / 3_600_000)))
                const evening = new Date(at).getUTCHours() >= 1 && new Date(at).getUTCHours() <= 4
                return {
                  at: new Date(Math.floor(at / 3_600_000) * 3_600_000).toISOString(),
                  loss_pct:
                    Math.round((probe.loss_pct ?? 0) * (evening ? 2.2 : 0.5) * (0.5 + r()) * 100) /
                    100,
                  rtt_avg_ms:
                    Math.round(
                      (probe.rtt_avg_ms ?? 5) * (evening ? 1.8 : 1) * (0.9 + r() * 0.2) * 10,
                    ) / 10,
                  jitter_ms: Math.round((evening ? 3 : 1) * (0.5 + r()) * 10) / 10,
                }
              }),
            },
          ]
        : [],
      settings: st,
    }
    return json(detail)
  }
  if (!ctx.can('snmp.manage')) return forbidden()
  const cfgs = configsOf(tenant, now, options)
  if (ctx.method === 'DELETE') {
    const i = cfgs.findIndex((c) => c.interface_id === id)
    if (i < 0) return notFound('UPLINK_NOT_FOUND')
    cfgs.splice(i, 1)
    return noContent()
  }
  if (ctx.method === 'PUT') {
    const sr = snmpRouters(tenant, now, options).find(
      (r) => interfaceId(r.item.router.id, 9) === id || interfaceId(r.item.router.id, 10) === id,
    )
    const b = ctx.body
    if (!sr || b.router_id !== sr.item.router.id) return notFound('UPLINK_NOT_FOUND')
    const errors: { field: string; code: string; message: string }[] = []
    const label = String(b.label ?? '').trim()
    if (!label || label.length > 120)
      errors.push({
        field: 'label',
        code: 'INVALID_LENGTH',
        message: 'El nombre debe tener entre 1 y 120 caracteres.',
      })
    if (!['international_transit', 'local_ix', 'cache', 'other'].includes(String(b.kind))) {
      errors.push({ field: 'kind', code: 'INVALID_VALUE', message: 'Tipo no admitido.' })
    }
    const contracted = b.contracted_bps == null ? null : Number(b.contracted_bps)
    if (contracted !== null && !(contracted >= 1e6 && contracted <= 1.6e12)) {
      errors.push({
        field: 'contracted_bps',
        code: 'OUT_OF_RANGE',
        message: 'La capacidad contratada va de 1 Mb/s a 1,6 Tb/s.',
      })
    }
    if (b.capacity_valid_from != null) {
      const at = Date.parse(String(b.capacity_valid_from))
      if (!Number.isFinite(at) || at > now.getTime() + 300_000 || at < now.getTime() - 90 * DAY) {
        errors.push({
          field: 'capacity_valid_from',
          code: 'OUT_OF_RANGE',
          message: 'Como mucho 90 días atrás y nunca en el futuro.',
        })
      }
    }
    if (errors.length) return invalid(errors)
    const i = cfgs.findIndex((c) => c.interface_id === id)
    const prev = i >= 0 ? cfgs[i]! : null
    const expect = ifMatch(ctx.req)
    if (expect !== null && prev && expect !== prev.version)
      return problem(412, 'PRECONDITION_FAILED', 'La versión cambió')
    const next: UplinkConfig = {
      interface_id: id,
      router_id: sr.item.router.id,
      label,
      provider: b.provider ? String(b.provider) : null,
      kind: b.kind as UplinkConfig['kind'],
      contracted_bps: contracted,
      international_egress:
        typeof b.international_egress === 'boolean'
          ? b.international_egress
          : b.kind === 'international_transit',
      updated_at: now.toISOString(),
      version: (prev?.version ?? 0) + 1,
    }
    if (i >= 0) cfgs[i] = next
    else cfgs.push(next)
    return json(next, 200, { ETag: `"${next.version}"` })
  }
  return undefined
}
