/**
 * Mock de la página Administración › Detección (detection.yaml): umbrales vigentes
 * (`GET /detection/config`, solo lectura en I1), feeds de reputación del ISP y allowlist
 * (prefijos/ASN que no generan hallazgos). La allowlist vive en memoria por ISP.
 */
import type { AllowlistEntry } from '~~/types/api'
import { forbidden, json, noContent, notFound, problem } from './http'
import { mockUuid } from './random'

interface Ctx {
  method: string
  path: string
  body: Record<string, unknown>
  tenant: { tenant_id: string }
  userId: string
  can: (permission: string) => boolean
  now: Date
}

/** Espejo de domain.DefaultParams (services/detection/internal/domain/params.go). */
const DETECTORS = {
  c2_contact: {
    enabled: true,
    lookback: '2h0m0s',
    retro_window: '168h0m0s',
    retro_interval: '6h0m0s',
    min_indicator_confidence: 50,
    mining: true,
  },
  scanning: {
    enabled: true,
    window: '5m0s',
    syn_only_ratio: 0.7,
    min_destinations: 100,
    vertical_min_ports: 50,
    vertical_small_share: 0.5,
  },
  fanout: {
    enabled: true,
    window: '5m0s',
    min_destinations: 500,
    min_nets24: 200,
    commercial_factor: 2,
  },
  watched_ports: { enabled: true, window: '5m0s', min_destinations: 20, exclude_ports: [25] },
  smtp: { enabled: true, window: '1h0m0s', residential_servers: 20, commercial_servers: 200 },
  ddos: {
    enabled: true,
    window: '5m0s',
    min_pps: 2000,
    min_minutes: 2,
    max_targets: 3,
    amp_ports: [53, 123, 1900, 11211, 19],
    amp_min_pps: 1000,
    amp_min_targets: 20,
  },
  beaconing: {
    enabled: true,
    window: '24h0m0s',
    interval: '1h0m0s',
    min_span: '6h0m0s',
    min_connections: 10,
    max_cv: 0.2,
    max_mean_bytes: 4096,
    min_period: '30s',
    max_period: '1h0m0s',
    exclude_ports: [53, 123, 853],
    allow_asns: null,
  },
  sustained_upload: {
    enabled: false,
    window: '1h0m0s',
    min_mbps: 5,
    upload_share: 0.8,
    min_minutes: 30,
    cloud_asns: [16509, 14618, 15169, 396982, 8075, 714, 6185, 19679, 40401, 395747],
    cloud_share: 0.8,
    backup_from_hour: 22,
    backup_to_hour: 7,
  },
  customer_scoring: {
    enabled: true,
    window: '168h0m0s',
    min_active_hours: 48,
    active_hour_bytes: 1048576,
    commercial_threshold: 80,
    suspected_threshold: 50,
    hysteresis: '168h0m0s',
    dynamic_pool_factor: 0.6,
    business_from_hour: 8,
    business_to_hour: 19,
    business_days: [1, 2, 3, 4, 5, 6],
    inbound_ports_full: 3,
    inbound_peers_full: 100,
    residential_upload_ratio: 0.15,
    commercial_upload_ratio: 0.6,
    upload_median_full: 5,
    min_residential_peers: 10,
    business_share_low: 0.45,
    business_share_high: 0.75,
    always_on_low: 0.6,
    always_on_high: 0.95,
    remote_asns_low: 50,
    remote_asns_high: 300,
    score_change_delta: 5,
    weights: {
      inbound_services: 0.3,
      upload_ratio_high: 0.2,
      upload_vs_residential_median: 0.2,
      business_hours_pattern: 0.15,
      always_on: 0.1,
      many_remote_asns: 0.05,
    },
  },
  auto_expire: '168h0m0s',
}

const SOURCES = [
  {
    key: 'abuse_ch_feodo',
    name: 'abuse.ch Feodo Tracker',
    origin: 'catalog',
    confidence: 90,
    category: 'botnet_cc',
    entries: 412,
    age_seconds: 1800,
    commercial_use_allowed: true,
  },
  {
    key: 'abuse_ch_sslbl',
    name: 'abuse.ch SSLBL',
    origin: 'catalog',
    confidence: 80,
    category: 'botnet_cc',
    entries: 97,
    age_seconds: 3600,
    commercial_use_allowed: true,
  },
  {
    key: 'spamhaus_drop',
    name: 'Spamhaus DROP',
    origin: 'catalog',
    confidence: 95,
    category: 'blocklist',
    entries: 1204,
    age_seconds: 7200,
    commercial_use_allowed: false,
  },
]

const allow = new Map<string, AllowlistEntry[]>()

function entries(ctx: Ctx) {
  let list = allow.get(ctx.tenant.tenant_id)
  if (!list) {
    list = [
      {
        id: mockUuid('0195a000', 1),
        tenant_id: ctx.tenant.tenant_id,
        prefix: '200.0.113.0/24',
        asn: null,
        kinds: [],
        reason: 'Servidores propios de monitoreo (escaneo autorizado)',
        created_by: ctx.userId,
        created_at: '2026-10-01T15:00:00.000Z',
      },
    ]
    allow.set(ctx.tenant.tenant_id, list)
  }
  return list
}

export function detectionSettingsRoute(ctx: Ctx): Response | undefined {
  if (ctx.method === 'GET' && ctx.path === '/detection/config') {
    if (!ctx.can('settings.read')) return forbidden()
    return json({ detectors: DETECTORS, customized: ['sustained_upload'] })
  }
  if (ctx.method === 'GET' && ctx.path === '/reputation/sources') {
    if (!ctx.can('security.findings.read')) return forbidden()
    return json({
      data: SOURCES.map((s) => ({
        ...s,
        last_success_at: new Date(ctx.now.getTime() - s.age_seconds * 1000).toISOString(),
      })),
    })
  }
  if (ctx.path === '/reputation/allowlist') {
    if (ctx.method === 'GET') {
      if (!ctx.can('security.findings.read')) return forbidden()
      const data = entries(ctx)
      return json({
        data,
        page: { limit: 50, has_more: false, next_cursor: null, prev_cursor: null },
      })
    }
    if (ctx.method === 'POST') {
      if (!ctx.can('security.findings.manage')) return forbidden()
      const prefix = typeof ctx.body.prefix === 'string' ? ctx.body.prefix.trim() : ''
      const asn = Number(ctx.body.asn ?? 0)
      const reason = typeof ctx.body.reason === 'string' ? ctx.body.reason.trim() : ''
      const invalid = (field: string, message: string) =>
        problem(
          422,
          'VALIDATION_FAILED',
          'Revisa los campos marcados',
          {},
          { errors: [{ field, code: 'INVALID', message }] },
        )
      if (!reason) return invalid('reason', 'Explica por qué no debe generar hallazgos')
      if (!prefix === !asn) return invalid('prefix', 'Indica un prefijo o un ASN (uno de los dos)')
      if (prefix && !/^[0-9a-f:.]+\/\d{1,3}$/i.test(prefix))
        return invalid('prefix', 'Prefijo no válido (p. ej. 200.0.113.0/24)')
      const entry: AllowlistEntry = {
        id: crypto.randomUUID(),
        tenant_id: ctx.tenant.tenant_id,
        prefix: prefix || null,
        asn: asn || null,
        kinds: [],
        reason,
        created_by: ctx.userId,
        created_at: ctx.now.toISOString(),
      }
      entries(ctx).unshift(entry)
      return json(entry, 201)
    }
  }
  const del = ctx.path.match(/^\/reputation\/allowlist\/([^/]+)$/)
  if (del && ctx.method === 'DELETE') {
    if (!ctx.can('security.findings.manage')) return forbidden()
    const list = entries(ctx)
    const i = list.findIndex((e) => e.id === del[1])
    if (i < 0) return notFound()
    list.splice(i, 1)
    return noContent()
  }
}
