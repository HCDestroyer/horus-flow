import {
  BOTNET_SIGNALS,
  type BotnetSignal,
  type BotnetSignalsValues,
  type CustomersActiveValues,
  type ExporterRow,
  type FindingFeedRow,
  type FindingsSummaryValues,
  type SecurityByNodeRow,
  type TopCustomerRow,
  type TopRow,
  type TrafficNowValues,
  type WatchedPortRow,
} from '~/widgets/shapes'
import type {
  DashboardWidget,
  SeriesData,
  TableData,
  WidgetData,
  WidgetDataMeta,
} from '~~/types/api'
import {
  CATEGORIES,
  maskIp,
  openFindings,
  SEVERITY_ORDER,
  SIGNALS_BY_KIND,
  tenantScale,
  type MockFinding,
} from './base'
import type { MockTenant } from './data'
import { customerCounts, customersOf, isInactive, routersOf, sitesOf } from './inventory'
import { hash, iso, rng } from './random'

export { openFindings, type MockFinding } from './base'

/**
 * Datos de widgets de la API simulada (`GET /dashboards/{id}/widgets/{wid}/data`, C9).
 *
 * Sintéticos y deterministas por ISP (semilla = tenant) y minuto, con la forma del sobre
 * `widget-data.schema.json`. Los hallazgos parten del ejemplo del contrato
 * (`finding-outbound-scanning.api.json`) y todos los widgets de seguridad salen de una misma
 * base (`openFindings`). Huecos como `null`, nunca ceros.
 */

export interface WidgetDataContext {
  tenant: MockTenant
  widget: DashboardWidget
  now: Date
  /** El espectador puede ver IPs de clientes (`customers.read`). */
  canSeePersonalData: boolean
  empty: boolean
}

/** Curva diaria de tráfico (bps) con pico nocturno, típica de un ISP residencial. */
function dailyCurve(scale: number, ms: number, timeZoneOffsetHours = -5) {
  const hour = (new Date(ms).getUTCHours() + timeZoneOffsetHours + 24) % 24
  const minute = new Date(ms).getUTCMinutes()
  const h = hour + minute / 60
  const evening = Math.exp(-((h - 21) ** 2) / 8)
  const midday = 0.55 * Math.exp(-((h - 13) ** 2) / 10)
  return scale * (0.22 + 0.78 * Math.max(evening, midday))
}

function meta(
  ctx: WidgetDataContext,
  kind: WidgetDataMeta['data_endpoint_kind'],
  extra: Partial<WidgetDataMeta> = {},
): WidgetDataMeta {
  return {
    widget_type: ctx.widget.type,
    data_endpoint_kind: kind,
    generated_at: ctx.now.toISOString(),
    partial: false,
    masked_personal_data: false,
    freshness_seconds: 4,
    cache: 'miss',
    ...extra,
  }
}

function state(ctx: WidgetDataContext, values: object): WidgetData {
  return {
    data: { kind: 'state', values: values as Record<string, number> },
    meta: meta(ctx, 'state'),
  }
}

function table(
  ctx: WidgetDataContext,
  columns: TableData['columns'],
  rows: object[],
  extra: { others?: object | null; meta?: Partial<WidgetDataMeta> } = {},
): WidgetData {
  return {
    data: {
      kind: 'table',
      columns,
      rows: (ctx.empty ? [] : rows) as Record<string, unknown>[],
      others: ctx.empty ? null : ((extra.others ?? null) as Record<string, unknown> | null),
    },
    meta: meta(ctx, 'table', extra.meta),
  }
}

// ---------------------------------------------------------------------------------------------

function trafficNow(ctx: WidgetDataContext): WidgetData {
  const scale = 4.2e9 * tenantScale(ctx.tenant)
  const t = ctx.now.getTime()
  const r = rng(hash(ctx.tenant.tenant_id + Math.floor(t / 10_000)))
  const step = 60
  const down: (number | null)[] = []
  const up: (number | null)[] = []
  for (let i = 59; i >= 0; i--) {
    const at = t - i * step * 1000
    const jitter = 0.94 + rng(hash(ctx.tenant.tenant_id + Math.floor(at / 60_000)))() * 0.12
    down.push(Math.round(dailyCurve(scale, at) * jitter))
    up.push(Math.round(dailyCurve(scale * 0.19, at) * jitter))
  }
  const values: TrafficNowValues = {
    down_bps: ctx.empty ? null : down.at(-1)!,
    up_bps: ctx.empty ? null : up.at(-1)!,
    down_bps_yesterday: Math.round(down.at(-1)! * (0.9 + r() * 0.1)),
    up_bps_yesterday: Math.round(up.at(-1)! * (1.02 + r() * 0.08)),
    flows_per_second: Math.round(1200 * tenantScale(ctx.tenant) + r() * 300),
    sparkline: {
      step_seconds: step,
      down_bps: ctx.empty ? [] : down,
      up_bps: ctx.empty ? [] : up,
    },
  }
  return state(ctx, values)
}

function customersActive(ctx: WidgetDataContext): WidgetData {
  const { total, newToday } = customerCounts(ctx.tenant)
  let inactive = 0
  for (let i = 0; i < total; i++) if (isInactive(i)) inactive++
  const values: CustomersActiveValues = ctx.empty
    ? { active: 0, new_today: 0, total: 0 }
    : { active: total - inactive, new_today: newToday, total }
  return state(ctx, values)
}

function exportersStatus(ctx: WidgetDataContext, degraded: boolean): WidgetData {
  // Misma base que `GET /flow-exporters` y las fichas de router.
  const rows: ExporterRow[] = routersOf(ctx.tenant, ctx.now, { degraded }).map((item) => ({
    router: item.router.name,
    site: item.site.name,
    state: item.exporter.state,
    state_since: item.exporter.state_since,
    last_flow_at: item.exporter.last_flow_at,
    flows_per_second: item.exporter.flows_per_second,
    loss_ratio: item.exporter.loss_ratio_5m,
  }))
  return table(
    ctx,
    [
      { key: 'router', type: 'string' },
      { key: 'site', type: 'string' },
      { key: 'state', type: 'state' },
      { key: 'state_since', type: 'timestamp' },
      { key: 'last_flow_at', type: 'timestamp' },
      { key: 'flows_per_second', type: 'number' },
      { key: 'loss_ratio', type: 'percent' },
    ],
    rows,
  )
}

function rangeMs(range: unknown) {
  const map: Record<string, number> = { '1h': 1, '6h': 6, '24h': 24, '7d': 168, '30d': 720 }
  return (map[String(range)] ?? 24) * 3_600_000
}

function trafficTimeseries(ctx: WidgetDataContext): WidgetData {
  const span = rangeMs(ctx.widget.config.range)
  const stepS = span <= 6 * 3_600_000 ? 60 : 300
  const t = Math.floor(ctx.now.getTime() / (stepS * 1000)) * stepS * 1000
  const scale = 4.2e9 * tenantScale(ctx.tenant)
  const down: [string, number | null][] = []
  const up: [string, number | null][] = []
  // Hueco de 25 min hace ~5 h (colector reiniciado): se muestra como hueco, no como cero.
  const gapFrom = t - 5 * 3_600_000
  const gapTo = gapFrom + 25 * 60_000
  for (let at = t - span; at <= t; at += stepS * 1000) {
    const gap = at >= gapFrom && at < gapTo
    const jitter = 0.95 + rng(hash(ctx.tenant.tenant_id + at))() * 0.1
    down.push([iso(at), gap ? null : Math.round(dailyCurve(scale, at) * jitter)])
    up.push([iso(at), gap ? null : Math.round(dailyCurve(scale * 0.19, at) * jitter)])
  }
  const series: SeriesData['series'] = ctx.empty
    ? []
    : [
        { metric: 'down_bps', unit: 'bps', group: null, points: down },
        { metric: 'up_bps', unit: 'bps', group: null, points: up },
      ]
  return {
    data: { kind: 'series', series },
    meta: meta(ctx, 'series', {
      from: iso(t - span),
      to: iso(t),
      step: stepS,
      coverage: 0.92,
      freshness_seconds: 40,
    }),
  }
}

function topCategories(ctx: WidgetDataContext): WidgetData {
  const r = rng(hash(ctx.tenant.tenant_id + 'categories'))
  const n = Number(ctx.widget.config.n ?? 8)
  const total = 38e12 * tenantScale(ctx.tenant)
  let rest = total
  const rows: TopRow[] = CATEGORIES.map((label, i) => {
    const share = i === 0 ? 0.42 : (rest / total) * (0.28 + r() * 0.12)
    const down = Math.round(total * share)
    rest -= down
    return { label, down_bytes: down, up_bytes: Math.round(down * (0.04 + r() * 0.1)) }
  }).sort((a, b) => b.down_bytes - a.down_bytes)
  const others = rows.slice(n).reduce(
    (acc, row) => ({
      label: 'Otros',
      down_bytes: acc.down_bytes + row.down_bytes,
      up_bytes: acc.up_bytes + row.up_bytes,
    }),
    { label: 'Otros', down_bytes: Math.round(rest), up_bytes: Math.round(rest * 0.08) },
  )
  return table(
    ctx,
    [
      { key: 'label', type: 'string' },
      { key: 'down_bytes', type: 'bytes' },
      { key: 'up_bytes', type: 'bytes' },
    ],
    rows.slice(0, n),
    { others },
  )
}

function topCustomers(ctx: WidgetDataContext): WidgetData {
  // Los clientes que más bajan de la base común (los mismos de la lista de Clientes).
  const n = Number(ctx.widget.config.n ?? 10)
  const sites = new Map(sitesOf(ctx.tenant).map((s) => [s.id, s.name]))
  const rows: TopCustomerRow[] = customersOf(ctx.tenant, ctx.now)
    .filter((c) => c.traffic_24h)
    .sort((a, b) => Number(b.traffic_24h!.down_bytes) - Number(a.traffic_24h!.down_bytes))
    .slice(0, n)
    .map((c) => ({
      customer_ip: ctx.canSeePersonalData ? c.address : maskIp(c.address),
      alias: c.alias,
      kind: c.kind === 'commercial' ? 'commercial' : 'residential',
      site: sites.get(c.site_id) ?? '',
      down_bytes: Number(c.traffic_24h!.down_bytes),
      up_bytes: Number(c.traffic_24h!.up_bytes),
    }))
  return table(
    ctx,
    [
      { key: 'customer_ip', type: 'ip', personal_data: true },
      { key: 'alias', type: 'string', personal_data: true },
      { key: 'kind', type: 'string' },
      { key: 'site', type: 'string' },
      { key: 'down_bytes', type: 'bytes' },
      { key: 'up_bytes', type: 'bytes' },
    ],
    rows,
    { meta: { masked_personal_data: !ctx.canSeePersonalData } },
  )
}

function atLeast(findings: MockFinding[], minSeverity: unknown) {
  const min = SEVERITY_ORDER.indexOf(String(minSeverity ?? 'low'))
  return findings.filter((f) => SEVERITY_ORDER.indexOf(f.severity) >= min)
}

function findingsSummary(ctx: WidgetDataContext): WidgetData {
  if (ctx.empty) {
    return state(ctx, {
      open_total: 0,
      open_by_severity: {},
      new_last_24h: 0,
      affected_customers: 0,
      by_security_state: {},
    } satisfies FindingsSummaryValues)
  }
  const findings = atLeast(openFindings(ctx.tenant, ctx.now), ctx.widget.config.min_severity)
  const min = SEVERITY_ORDER.indexOf(String(ctx.widget.config.min_severity ?? 'low'))
  const bySeverity = Object.fromEntries(
    (['low', 'medium', 'high', 'critical'] as const)
      .filter((s) => SEVERITY_ORDER.indexOf(s) >= min)
      .map((s) => [s, findings.filter((f) => f.severity === s).length]),
  )
  const customers = (list: MockFinding[]) => new Set(list.map((f) => f.customer)).size
  const dayAgo = ctx.now.getTime() - 86_400_000
  const values: FindingsSummaryValues = {
    open_total: findings.length,
    open_by_severity: bySeverity,
    new_last_24h: findings.filter((f) => new Date(f.opened_at).getTime() >= dayAgo).length,
    affected_customers: customers(findings),
    by_security_state: {
      infected: customers(findings.filter((f) => f.security_state === 'infected')),
      suspected: customers(findings.filter((f) => f.security_state === 'suspected')),
    },
  }
  return state(ctx, values)
}

function botnetSignals(ctx: WidgetDataContext): WidgetData {
  if (ctx.empty) {
    return state(ctx, { by_signal: {}, affected_customers: 0 } satisfies BotnetSignalsValues)
  }
  const findings = openFindings(ctx.tenant, ctx.now)
  const bySignal = new Map<BotnetSignal, Set<number>>()
  const affected = new Set<number>()
  for (const f of findings) {
    for (const signal of SIGNALS_BY_KIND[f.kind] ?? []) {
      if (!bySignal.has(signal)) bySignal.set(signal, new Set())
      bySignal.get(signal)!.add(f.customer)
      affected.add(f.customer)
    }
  }
  const values: BotnetSignalsValues = {
    by_signal: Object.fromEntries(BOTNET_SIGNALS.map((s) => [s, bySignal.get(s)?.size ?? 0])),
    affected_customers: affected.size,
  }
  return state(ctx, values)
}

function findingsFeed(ctx: WidgetDataContext): WidgetData {
  const limit = Number(ctx.widget.config.limit ?? 8)
  const rows: FindingFeedRow[] = atLeast(
    openFindings(ctx.tenant, ctx.now),
    ctx.widget.config.min_severity,
  )
    .sort((a, b) => b.last_seen_at.localeCompare(a.last_seen_at))
    .slice(0, limit)
    .map(({ customer: _customer, opened_at: _opened, seq: _seq, ...row }) => ({
      ...row,
      customer_ip: ctx.canSeePersonalData ? row.customer_ip : maskIp(row.customer_ip),
    }))
  return table(
    ctx,
    [
      { key: 'severity', type: 'severity' },
      { key: 'summary', type: 'string' },
      { key: 'kind', type: 'string' },
      { key: 'customer_ip', type: 'ip', personal_data: true },
      { key: 'alias', type: 'string', personal_data: true },
      { key: 'site', type: 'string' },
      { key: 'security_state', type: 'state' },
      { key: 'confidence', type: 'percent' },
      { key: 'last_seen_at', type: 'timestamp' },
    ],
    rows,
    { meta: { masked_personal_data: !ctx.canSeePersonalData } },
  )
}

/** Tipos de la tendencia, en orden fijo (paleta categórica). */
export const TREND_KINDS = [
  'outbound_scanning',
  'botnet_c2_communication',
  'spam_smtp_outbound',
  'beaconing',
  'ddos_participation',
]

/** Hallazgos abiertos por día de apertura y tipo, de la misma base que el resumen. */
function findingsTrend(ctx: WidgetDataContext): WidgetData {
  const days = String(ctx.widget.config.range) === '30d' ? 30 : 7
  const dayMs = 86_400_000
  const end = Math.floor(ctx.now.getTime() / dayMs) * dayMs
  const start = end - (days - 1) * dayMs
  const findings = openFindings(ctx.tenant, ctx.now)
  const series: SeriesData['series'] = ctx.empty
    ? []
    : TREND_KINDS.map((kind) => {
        const counts = Array<number>(days).fill(0)
        for (const f of findings) {
          if (f.kind !== kind) continue
          const day = Math.floor((new Date(f.opened_at).getTime() - start) / dayMs)
          if (day >= 0 && day < days) counts[day]!++
        }
        return {
          metric: 'findings_opened',
          unit: 'count',
          group: kind,
          points: counts.map((n, i) => [iso(start + i * dayMs), n] as [string, number]),
        }
      })
  return {
    data: { kind: 'series', series },
    meta: meta(ctx, 'series', {
      from: iso(start),
      to: iso(end),
      step: 86_400,
      freshness_seconds: 20,
    }),
  }
}

function securityByNode(ctx: WidgetDataContext): WidgetData {
  const findings = openFindings(ctx.tenant, ctx.now)
  const rows: SecurityByNodeRow[] = ctx.tenant.sites
    .map((s) => {
      const here = findings.filter((f) => f.site === s.name)
      return {
        site: s.name,
        customers_with_signals: new Set(here.map((f) => f.customer)).size,
        open_findings: here.length,
      }
    })
    .filter((row) => row.open_findings > 0)
    .sort((a, b) => b.customers_with_signals - a.customers_with_signals)
    .slice(0, Number(ctx.widget.config.n ?? 8))
  return table(
    ctx,
    [
      { key: 'site', type: 'string' },
      { key: 'customers_with_signals', type: 'number' },
      { key: 'open_findings', type: 'number' },
    ],
    rows,
  )
}

const WATCHED_PORTS: Pick<WatchedPortRow, 'port' | 'protocol' | 'service'>[] = [
  { port: 23, protocol: 'tcp', service: 'Telnet' },
  { port: 2323, protocol: 'tcp', service: 'Telnet alternativo' },
  { port: 445, protocol: 'tcp', service: 'SMB' },
  { port: 7547, protocol: 'tcp', service: 'TR-069' },
  { port: 8291, protocol: 'tcp', service: 'Winbox' },
  { port: 25, protocol: 'tcp', service: 'SMTP' },
]

function watchedPorts(ctx: WidgetDataContext): WidgetData {
  const r = rng(hash(ctx.tenant.tenant_id + 'ports'))
  const rows: WatchedPortRow[] = WATCHED_PORTS.map((p, i) => ({
    ...p,
    customers: Math.max(1, Math.round((8 - i) * (0.4 + r()))),
    flows: Math.round((6 - i) * 1200 * (0.5 + r())),
  })).sort((a, b) => b.customers - a.customers)
  return table(
    ctx,
    [
      { key: 'port', type: 'number' },
      { key: 'protocol', type: 'string' },
      { key: 'service', type: 'string' },
      { key: 'customers', type: 'number' },
      { key: 'flows', type: 'number' },
    ],
    rows,
  )
}

/** Datos para un widget; `undefined` si el tipo no tiene datos (`data_endpoint_kind: none`). */
export function widgetData(
  ctx: WidgetDataContext,
  options: { degraded: boolean },
): WidgetData | undefined {
  switch (ctx.widget.type) {
    case 'traffic_now':
      return trafficNow(ctx)
    case 'customers_active':
      return customersActive(ctx)
    case 'findings_summary':
      return findingsSummary(ctx)
    case 'botnet_signals':
      return botnetSignals(ctx)
    case 'exporters_status':
      return exportersStatus(ctx, options.degraded)
    case 'traffic_timeseries':
      return trafficTimeseries(ctx)
    case 'top_categories':
    case 'top_services':
    case 'top_organizations':
      return topCategories(ctx)
    case 'top_customers':
      return topCustomers(ctx)
    case 'findings_feed':
      return findingsFeed(ctx)
    case 'findings_trend':
      return findingsTrend(ctx)
    case 'security_by_node':
      return securityByNode(ctx)
    case 'watched_ports':
      return watchedPorts(ctx)
    default:
      return undefined
  }
}

/** Valor en vivo de `traffic.summary` (mensaje WS `state`, C6) para la API simulada. */
export function trafficSummaryMessage(tenant: MockTenant, now: Date) {
  const scale = 4.2e9 * tenantScale(tenant)
  const t = now.getTime()
  const jitter = 0.97 + rng(hash(tenant.tenant_id + Math.floor(t / 5000)))() * 0.06
  return {
    type: 'state' as const,
    topic: 'traffic.summary',
    key: 'tenant',
    time: now.toISOString(),
    data: {
      site_id: null,
      down_bps: Math.round(dailyCurve(scale, t) * jitter),
      up_bps: Math.round(dailyCurve(scale * 0.19, t) * jitter),
      flows_per_second: Math.round(1200 * tenantScale(tenant) * jitter),
      active_customers: Math.round(800 * tenantScale(tenant)),
      window_seconds: 10,
      partial: false,
    },
  }
}
