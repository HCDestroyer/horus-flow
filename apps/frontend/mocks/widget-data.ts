import type {
  BotnetSignalsValues,
  CustomersActiveValues,
  ExporterRow,
  FindingFeedRow,
  FindingsSummaryValues,
  SecurityByNodeRow,
  TopCustomerRow,
  TopRow,
  TrafficNowValues,
  WatchedPortRow,
} from '~/widgets/shapes'
import type {
  DashboardWidget,
  SeriesData,
  TableData,
  WidgetData,
  WidgetDataMeta,
} from '~~/types/api'
import findingExample from '~~/types/api/contract/examples/finding-outbound-scanning.api.json'
import type { MockTenant } from './data'

/**
 * Datos de widgets de la API simulada (`GET /dashboards/{id}/widgets/{wid}/data`, C9).
 *
 * Sintéticos y deterministas por ISP (semilla = tenant) y minuto, con la forma del sobre
 * `widget-data.schema.json`. Los hallazgos parten del ejemplo del contrato
 * (`finding-outbound-scanning.api.json`). Huecos como `null`, nunca ceros.
 */

export interface WidgetDataContext {
  tenant: MockTenant
  widget: DashboardWidget
  now: Date
  /** El espectador puede ver IPs de clientes (`customers.read`). */
  canSeePersonalData: boolean
  empty: boolean
}

function hash(text: string) {
  let h = 2166136261
  for (let i = 0; i < text.length; i++) {
    h ^= text.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  return h >>> 0
}

/** PRNG determinista (mulberry32). */
function rng(seed: number) {
  let a = seed
  return () => {
    a = (a + 0x6d2b79f5) | 0
    let t = Math.imul(a ^ (a >>> 15), 1 | a)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

const iso = (ms: number) => new Date(ms).toISOString()

/** Curva diaria de tráfico (bps) con pico nocturno, típica de un ISP residencial. */
function dailyCurve(scale: number, ms: number, timeZoneOffsetHours = -5) {
  const hour = (new Date(ms).getUTCHours() + timeZoneOffsetHours + 24) % 24
  const minute = new Date(ms).getUTCMinutes()
  const h = hour + minute / 60
  const evening = Math.exp(-((h - 21) ** 2) / 8)
  const midday = 0.55 * Math.exp(-((h - 13) ** 2) / 10)
  return scale * (0.22 + 0.78 * Math.max(evening, midday))
}

function tenantScale(tenant: MockTenant) {
  return 1 + (hash(tenant.tenant_id) % 7) / 4 // 1–2.5
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

function maskIp(ip: string) {
  const parts = ip.split('.')
  return `${parts.slice(0, 3).join('.')}.•••`
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
  const r = rng(hash(ctx.tenant.tenant_id + 'customers'))
  const total = Math.round(800 * tenantScale(ctx.tenant) + r() * 200)
  const values: CustomersActiveValues = ctx.empty
    ? { active: 0, new_today: 0, total: 0 }
    : { active: Math.round(total * 0.91), new_today: Math.round(3 + r() * 9), total }
  return state(ctx, values)
}

function findingsSummary(ctx: WidgetDataContext): WidgetData {
  const r = rng(hash(ctx.tenant.tenant_id + 'findings'))
  const bySeverity = ctx.empty
    ? {}
    : {
        critical: Math.round(r() * 2),
        high: Math.round(2 + r() * 5),
        medium: Math.round(4 + r() * 8),
        low: Math.round(3 + r() * 6),
      }
  const minSeverity = String(ctx.widget.config.min_severity ?? 'low')
  const order = ['low', 'medium', 'high', 'critical']
  const filtered = Object.fromEntries(
    Object.entries(bySeverity).filter(([s]) => order.indexOf(s) >= order.indexOf(minSeverity)),
  )
  const openTotal = Object.values(filtered).reduce((a, b) => a + b, 0)
  const values: FindingsSummaryValues = {
    open_total: openTotal,
    open_by_severity: filtered,
    new_last_24h: ctx.empty ? 0 : Math.round(2 + r() * 6),
    affected_customers: ctx.empty ? 0 : Math.round(openTotal * 0.7),
    by_security_state: ctx.empty
      ? {}
      : { infected: Math.round(1 + r() * 2), suspected: Math.round(openTotal * 0.5) },
  }
  return state(ctx, values)
}

function botnetSignals(ctx: WidgetDataContext): WidgetData {
  const r = rng(hash(ctx.tenant.tenant_id + 'signals'))
  const values: BotnetSignalsValues = ctx.empty
    ? { by_signal: {}, affected_customers: 0 }
    : {
        by_signal: {
          c2_contact: Math.round(1 + r() * 3),
          beaconing: Math.round(r() * 4),
          fan_out: Math.round(2 + r() * 5),
          scanning: Math.round(3 + r() * 7),
          watched_ports: Math.round(2 + r() * 6),
          sustained_upload: Math.round(r() * 3),
          smtp: Math.round(r() * 2),
          ddos: 0,
        },
        affected_customers: Math.round(9 + r() * 8),
      }
  return state(ctx, values)
}

function exportersStatus(ctx: WidgetDataContext, degraded: boolean): WidgetData {
  const t = ctx.now.getTime()
  const r = rng(hash(ctx.tenant.tenant_id + 'exporters'))
  const rows: ExporterRow[] = ctx.tenant.sites.map((site, i) => {
    const silent = degraded && i === ctx.tenant.sites.length - 1
    const lossy = !silent && i === 1
    return {
      router: `rt-${site.name.replace('Nodo ', '').toLowerCase().normalize('NFD').replace(/[̀-ͯ]/g, '')}`,
      site: site.name,
      state: silent ? 'silent' : lossy ? 'lossy' : 'exporting',
      state_since: iso(t - (silent ? 7 * 60_000 : (2 + i) * 3_600_000)),
      last_flow_at: iso(t - (silent ? 7 * 60_000 : Math.round(1 + r() * 4) * 1000)),
      flows_per_second: silent ? null : Math.round(250 + r() * 600),
      loss_ratio: silent ? null : lossy ? 0.031 : Math.round(r() * 20) / 10_000,
    }
  })
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

const CATEGORIES = [
  'Streaming de video',
  'Redes sociales',
  'Videojuegos',
  'Actualizaciones de software',
  'Navegación web',
  'Mensajería',
  'Nube y almacenamiento',
  'Videollamadas',
  'Música',
  'Comercio electrónico',
]

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

const ALIASES = ['Panadería Sol', 'Ferretería El Puente', null, null, 'Clínica Dental Norte', null]

function topCustomers(ctx: WidgetDataContext): WidgetData {
  const r = rng(hash(ctx.tenant.tenant_id + 'top-customers'))
  const n = Number(ctx.widget.config.n ?? 10)
  const rows: TopCustomerRow[] = Array.from({ length: n }, (_, i) => {
    const down = Math.round(9.5e11 / (i + 1.4) + r() * 4e10)
    const alias = ALIASES[i % ALIASES.length] ?? null
    const ip = `${ctx.tenant.prefix}.${Math.floor(r() * 3)}.${Math.floor(10 + r() * 240)}`
    return {
      customer_ip: ctx.canSeePersonalData ? ip : maskIp(ip),
      alias,
      kind: alias ? 'commercial' : 'residential',
      site: ctx.tenant.sites[i % ctx.tenant.sites.length]!.name,
      down_bytes: down,
      up_bytes: Math.round(down * (alias ? 0.6 : 0.08 + r() * 0.1)),
    }
  })
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

/** Plantillas de resumen de hallazgos (el primero, del ejemplo del contrato). */
const FINDING_TEMPLATES: Pick<FindingFeedRow, 'kind' | 'severity' | 'summary'>[] = [
  {
    kind: findingExample.kind,
    severity: findingExample.severity as FindingFeedRow['severity'],
    summary: findingExample.summary.text,
  },
  {
    kind: 'botnet_c2_communication',
    severity: 'critical',
    summary: 'Conexiones a un servidor de control de botnet conocido (Feodo Tracker)',
  },
  {
    kind: 'spam_smtp_outbound',
    severity: 'medium',
    summary: 'SMTP saliente directo a 86 servidores en 1 h',
  },
  {
    kind: 'beaconing',
    severity: 'medium',
    summary: 'Conexiones periódicas cada 60 s al mismo destino',
  },
  {
    kind: 'outbound_scanning',
    severity: 'high',
    summary: 'Escaneo del puerto 445 a 610 destinos en 10 min',
  },
  {
    kind: 'ddos_participation',
    severity: 'high',
    summary: 'Ráfaga de 48 000 pps UDP/123 hacia 2 destinos',
  },
  {
    kind: 'reputation_hit',
    severity: 'low',
    summary: 'Contacto con una IP listada en Spamhaus DROP',
  },
  {
    kind: 'outbound_scanning',
    severity: 'medium',
    summary: 'Escaneo del puerto 7547 a 320 destinos en 15 min',
  },
  {
    kind: 'beaconing',
    severity: 'low',
    summary: 'Conexiones periódicas cada 300 s a un dominio nuevo',
  },
  {
    kind: 'spam_smtp_outbound',
    severity: 'low',
    summary: 'SMTP saliente directo a 14 servidores en 1 h',
  },
]

function findingsFeed(ctx: WidgetDataContext): WidgetData {
  const r = rng(hash(ctx.tenant.tenant_id + 'feed'))
  const limit = Number(ctx.widget.config.limit ?? 8)
  const order = ['info', 'low', 'medium', 'high', 'critical']
  const min = order.indexOf(String(ctx.widget.config.min_severity ?? 'low'))
  const t = ctx.now.getTime()
  const rows: FindingFeedRow[] = FINDING_TEMPLATES.filter((f) => order.indexOf(f.severity) >= min)
    .slice(0, limit)
    .map((f, i) => {
      const ip =
        i === 0
          ? findingExample.customer.address.replace('10.20', ctx.tenant.prefix)
          : `${ctx.tenant.prefix}.${Math.floor(r() * 3)}.${Math.floor(10 + r() * 240)}`
      const confidence = i === 1 ? 0.94 : Math.round((0.55 + r() * 0.4) * 100) / 100
      return {
        id: `${findingExample.id.slice(0, -2)}${String(i).padStart(2, '0')}`,
        ...f,
        customer_ip: ctx.canSeePersonalData ? ip : maskIp(ip),
        alias: i === 2 ? 'Ferretería El Puente' : null,
        site: ctx.tenant.sites[i % ctx.tenant.sites.length]!.name,
        // D18: un cliente con C2 confirmado y confianza alta aparece como "Infectado".
        security_state: f.kind === 'botnet_c2_communication' ? 'infected' : 'suspected',
        confidence,
        last_seen_at: iso(t - (i * 7 + 1) * 60_000 - Math.round(r() * 50_000)),
      }
    })
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

function findingsTrend(ctx: WidgetDataContext): WidgetData {
  const days = String(ctx.widget.config.range) === '30d' ? 30 : 7
  const dayMs = 86_400_000
  const end = Math.floor(ctx.now.getTime() / dayMs) * dayMs
  const kinds = [
    'outbound_scanning',
    'botnet_c2_communication',
    'spam_smtp_outbound',
    'beaconing',
    'ddos_participation',
  ]
  const series: SeriesData['series'] = ctx.empty
    ? []
    : kinds.map((kind, k) => {
        const r = rng(hash(ctx.tenant.tenant_id + kind))
        return {
          metric: 'findings_opened',
          unit: 'count',
          group: kind,
          points: Array.from({ length: days }, (_, i) => [
            iso(end - (days - 1 - i) * dayMs),
            Math.round((5 - k) * (0.6 + r()) + (k === 0 ? i * 0.4 : 0)),
          ]),
        }
      })
  return {
    data: { kind: 'series', series },
    meta: meta(ctx, 'series', {
      from: iso(end - (days - 1) * dayMs),
      to: iso(end),
      step: 86_400,
      freshness_seconds: 120,
    }),
  }
}

function securityByNode(ctx: WidgetDataContext): WidgetData {
  const r = rng(hash(ctx.tenant.tenant_id + 'by-node'))
  const rows: SecurityByNodeRow[] = ctx.tenant.sites
    .map((s) => ({
      site: s.name,
      customers_with_signals: Math.round(1 + r() * 9),
      open_findings: Math.round(2 + r() * 12),
    }))
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
