import type {
  ClientPrefix,
  ClientPrefixInput,
  CustomerDetail,
  Finding,
  KindChange,
  SeriesResponse,
  TopResult,
} from '~~/types/api'
import { asnRulesOf, asnRulesRoute } from './asn-rules'
import { CATEGORIES, maskIp, REMOTE_ASNS, tenantScale } from './base'
import type { MockTenant } from './data'
import { assistRoute } from './assist'
import { detectionSettingsRoute } from './detection-settings'
import { deviceRoutes, isManagementAddress } from './devices'
import { switchRoutes } from './switches'
import { tlsPinOf, tlsPinRoute } from './tls-pins'
import { evidenceFlows, findFinding, findingsOf, securitySummary, sortFindings } from './findings'
import {
  forbidden,
  ifMatch,
  json,
  noContent,
  notFound,
  paginate,
  problem,
  randomHex,
  text,
} from './http'
import {
  buildCustomer,
  customerCounts,
  customerDown,
  customerIndexFromId,
  customersOf,
  deprovisioningScript,
  devicesOf,
  directOnboarding,
  findSite,
  kindHistory,
  newPrefix,
  overlapping,
  prefixesOf,
  prefixImportPreview,
  prefixProposals,
  provisioningScript,
  routersOf,
  sitesOf,
  toSite,
} from './inventory'
import { contains, parseCidr, parseIp } from './net'
import { hash, iso, mockUuid, rng } from './random'
import { mockScoring, mockScoringDetail, mockScoringSeries } from './scoring'
import { mockState, type TransportOverride } from './state'
import { tunnelConfig } from './tunnels'
import { widgetData } from './widget-data'
import {
  analyticsDown as analyticsUnavailable,
  analyticsMeta as seriesMeta,
  rangeMs,
} from './analytics-meta'
import { capacityRoutes } from './capacity'
import { infraServiceRoutes } from './infra-services'
import { integrationsRoute } from './integrations'
import { reportRoutes } from './reports'
import { snmpRoutes } from './snmp'

/**
 * Rutas de negocio con token de ISP de la API simulada (I1): inventario, prefijos y onboarding,
 * clientes, hallazgos, analítica y vista previa de widgets. Mismas formas y errores que el
 * contrato v0 (C5); los datos salen de la base común (`mocks/base.ts`, `mocks/inventory.ts`).
 */

export interface TenantRouteContext {
  req: Request
  url: URL
  path: string
  method: string
  body: Record<string, unknown>
  tenant: MockTenant
  userId: string
  can: (permission: string) => boolean
  now: Date
  degraded: boolean
  /** Escenario del mock (`horus.mock.scenario`): 'no-asn' = sin dataset de ASN cargado. */
  scenario: string
  empty: boolean
  /** Paso del asistente de alta simulado (ms entre "clave recibida", "túnel" y "primer flujo"). */
  stepMs: number
  /** La acción `x-reauth` exige re-autenticarse antes (`auth_time` viejo simulado). */
  reauthRequired?: boolean
}

type Params = Record<string, string>

function match(ctx: TenantRouteContext, method: string, pattern: string): Params | null {
  if (ctx.method !== method) return null
  const names: string[] = []
  const re = new RegExp(
    `^${pattern.replace(/\{(\w+)\}/g, (_, n: string) => {
      names.push(n)
      return '([^/]+)'
    })}$`,
  )
  const m = ctx.path.match(re)
  if (!m) return null
  return Object.fromEntries(names.map((n, i) => [n, decodeURIComponent(m[i + 1]!)]))
}

const etag = (version: number) => ({ ETag: `"${version}"` })

function preconditionFailed(current: unknown) {
  return problem(
    412,
    'PRECONDITION_FAILED',
    'El recurso cambió mientras lo editabas',
    {},
    { current: current as Record<string, unknown> },
  )
}

function validation(field: string, message: string, code = 'REQUIRED') {
  return problem(
    422,
    'VALIDATION_FAILED',
    'Revisa los campos marcados',
    {},
    {
      errors: [{ field, code, message }],
    },
  )
}

function analyticsMeta(ctx: TenantRouteContext, range: string | null, step: number | null) {
  return seriesMeta(ctx.now, range, step)
}

function analyticsDown(ctx: TenantRouteContext) {
  return analyticsUnavailable(ctx.degraded)
}

// --- Clientes --------------------------------------------------------------------------------

function filterCustomers(ctx: TenantRouteContext, list: CustomerDetail[]) {
  const q = ctx.url.searchParams
  const siteIds = q.get('site_id')?.split(',').filter(Boolean)
  const alias = q.get('q')?.trim().toLowerCase()
  return list.filter(
    (c) =>
      (!siteIds?.length || siteIds.includes(c.site_id)) &&
      (!q.get('kind') || c.kind === q.get('kind')) &&
      (!q.get('kind_source') || c.kind_source === q.get('kind_source')) &&
      (!q.get('status') || c.status === q.get('status')) &&
      (!q.get('security_state') || c.security_state === q.get('security_state')) &&
      (q.get('has_open_findings') !== 'true' || c.open_findings > 0) &&
      (!q.get('client_prefix_id') || c.client_prefix_id === q.get('client_prefix_id')) &&
      (q.get('commercial_candidate') !== 'true' ||
        (!c.kind_locked && (c.suggested_kind === 'commercial' || c.commercial_use_suspected))) &&
      (!alias || (c.alias ?? '').toLowerCase().includes(alias)),
  )
}

function sortCustomers(list: CustomerDetail[], sort: string | null) {
  const key = (sort ?? '-last_seen').replace(/^-/, '') as 'last_seen' | 'first_seen'
  const dir = sort?.startsWith('-') === false ? 1 : -1
  return [...list].sort((a, b) => dir * String(a[key] ?? '').localeCompare(String(b[key] ?? '')))
}

/** Vista de lista: sin notas (solo en la ficha). */
function listItem(c: CustomerDetail) {
  const { notes: _n, ...rest } = c
  return rest
}

function customerRoutes(ctx: TenantRouteContext): Response | undefined {
  const { tenant, now } = ctx

  if (match(ctx, 'GET', '/customers/stats')) {
    const list = customersOf(tenant, now)
    const count = (fn: (c: CustomerDetail) => string) =>
      list.reduce<Record<string, number>>(
        (acc, c) => ((acc[fn(c)] = (acc[fn(c)] ?? 0) + 1), acc),
        {},
      )
    return json({
      generated_at: now.toISOString(),
      total: list.length,
      active: list.filter((c) => c.status === 'active').length,
      new_today: customerCounts(tenant).newToday,
      by_kind: count((c) => c.kind),
      by_kind_source: count((c) => c.kind_source),
      by_status: count((c) => c.status),
      by_security_state: count((c) => c.security_state),
      by_site: sitesOf(tenant).map((s) => ({
        site_id: s.id,
        active: list.filter((c) => c.site_id === s.id && c.status === 'active').length,
        with_open_findings: list.filter((c) => c.site_id === s.id && c.open_findings > 0).length,
      })),
    })
  }

  // Todo lo demás de clientes contiene IPs: exige customers.read (dato personal, auditado).
  if (!ctx.path.startsWith('/customers') && !ctx.path.startsWith('/analytics/customers')) return
  if (!ctx.can('customers.read')) return forbidden()

  if (match(ctx, 'GET', '/customers')) {
    const list = sortCustomers(
      filterCustomers(ctx, customersOf(tenant, now)),
      ctx.url.searchParams.get('sort'),
    )
    const page = paginate(list, ctx.url)
    return json({ data: page.data.map(listItem), page: page.page })
  }

  if (match(ctx, 'POST', '/customers/lookup')) {
    // La IP viaja en el cuerpo, nunca en la URL (D1, security.md).
    const address = typeof ctx.body.address === 'string' ? ctx.body.address.trim() : null
    const prefix = typeof ctx.body.prefix === 'string' ? ctx.body.prefix.trim() : null
    const siteId = typeof ctx.body.site_id === 'string' ? ctx.body.site_id : null
    const target = address ? parseCidr(address) : prefix ? parseCidr(prefix) : null
    if (!target) {
      return validation(
        address ? 'address' : 'prefix',
        'Escribe una IP o un prefijo válido',
        'INVALID_IP',
      )
    }
    const list = customersOf(tenant, now).filter((c) => {
      if (siteId && c.site_id !== siteId) return false
      const ip = parseIp(c.address)
      if (!ip) return false
      const len = ip.family === 6 ? 56 : 32
      const own = { family: ip.family, network: ip.value, length: len }
      // IPv6: una IP dentro del prefijo delegado encuentra al cliente (D22).
      return contains(target, own) || contains(own, target)
    })
    const page = paginate(list, ctx.url)
    return json({ data: page.data.map(listItem), page: page.page })
  }

  const idMatch = ctx.path.match(/^\/(?:analytics\/)?customers\/([^/]+)/)
  const index = idMatch ? customerIndexFromId(tenant, idMatch[1]!) : -1
  if (idMatch && index < 0) return notFound('CUSTOMER_NOT_FOUND')
  const customer = () => buildCustomer(tenant, index, now)

  if (match(ctx, 'GET', '/customers/{id}')) {
    const c = customer()
    return json(c, 200, etag(c.version))
  }

  if (match(ctx, 'PATCH', '/customers/{id}')) {
    if (!ctx.can('customers.update')) return forbidden()
    const c = customer()
    const expected = ifMatch(ctx.req)
    if (expected === null) return problem(428, 'PRECONDITION_REQUIRED', 'Falta If-Match')
    if (expected !== c.version) return preconditionFailed(c)
    const o = mockState.customers.get(c.id) ?? { version: c.version }
    if ('alias' in ctx.body) o.alias = (ctx.body.alias as string | null) || null
    if ('notes' in ctx.body) o.notes = (ctx.body.notes as string | null) || null
    o.version = c.version + 1
    mockState.customers.set(c.id, o)
    const next = customer()
    return json(next, 200, etag(next.version))
  }

  if (match(ctx, 'GET', '/customers/{id}/findings')) {
    const c = customer()
    const list = sortFindings(findingsOf(tenant, now, true).filter((f) => f.customer_id === c.id))
    return json(paginate(list, ctx.url))
  }

  if (match(ctx, 'GET', '/customers/{id}/scoring')) {
    // Scoring residencial/comercial (detection, I2): latest null sin scoring todavía.
    const c = customer()
    const s = mockScoring(index, c.status === 'inactive')
    const range = ctx.url.searchParams.get('range')
    const days = range?.match(/^(\d+)d$/) ? Number(range.slice(0, -1)) : 0
    if (!s) return json({ latest: null, series: [] })
    return json({
      latest: mockScoringDetail(c.id, index, s, c.kind, c.kind_locked, now),
      series: days ? mockScoringSeries(index, s, now, days) : [],
    })
  }

  if (match(ctx, 'GET', '/customers/{id}/kind-history')) {
    return json(paginate(kindHistory(tenant, index, now), ctx.url))
  }

  if (match(ctx, 'POST', '/customers/{id}/set-kind')) {
    if (!ctx.can('customers.kind.write')) return forbidden()
    const c = customer()
    const kind = ctx.body.kind as CustomerDetail['kind']
    const reason = String(ctx.body.reason ?? '').trim()
    if (!reason) return validation('reason', 'Escribe el motivo del cambio')
    if (!['residential', 'commercial', 'unknown'].includes(kind)) {
      return validation('kind', 'Tipo no válido', 'INVALID')
    }
    const expected = ifMatch(ctx.req)
    if (expected === null) return problem(428, 'PRECONDITION_REQUIRED', 'Falta If-Match')
    // Cliente con tipo detectado: el scoring lo reevaluó mientras el operador miraba (I2,
    // frontend.md §8.1): el primer intento devuelve 412 con el valor actual.
    if (c.kind_source === 'scoring' && !mockState.kindConflictServed.has(c.id)) {
      mockState.kindConflictServed.add(c.id)
      mockState.customers.set(c.id, {
        ...(mockState.customers.get(c.id) ?? {}),
        version: c.version + 1,
        kind: 'residential',
        kind_confidence: 58,
        kind_changed_at: now.toISOString(),
      })
      return preconditionFailed(customer())
    }
    if (expected !== c.version) return preconditionFailed(c)
    const change: KindChange = {
      id: mockUuid('0193d100', hash(c.id + now.getTime())),
      changed_at: now.toISOString(),
      from_kind: c.kind,
      to_kind: kind,
      source: 'manual',
      actor_id: ctx.userId,
      confidence: null,
      manual_reason: reason,
      model_ref: null,
      reasons: [],
    }
    mockState.kindHistory.set(c.id, [change, ...(mockState.kindHistory.get(c.id) ?? [])])
    mockState.customers.set(c.id, {
      ...(mockState.customers.get(c.id) ?? {}),
      version: c.version + 1,
      kind,
      kind_source: 'manual',
      kind_locked: true,
      kind_confidence: null,
      kind_changed_at: now.toISOString(),
    })
    const next = customer()
    return json(next, 200, etag(next.version))
  }

  if (match(ctx, 'POST', '/customers/{id}/unlock-kind')) {
    if (!ctx.can('customers.kind.write')) return forbidden()
    const c = customer()
    if (ifMatch(ctx.req) !== c.version) return preconditionFailed(c)
    mockState.customers.set(c.id, {
      ...(mockState.customers.get(c.id) ?? {}),
      version: c.version + 1,
      kind_locked: false,
    })
    const next = customer()
    return json(next, 200, etag(next.version))
  }

  if (match(ctx, 'POST', '/customers/{id}/reset')) {
    if (!ctx.can('customers.kind.write')) return forbidden()
    const c = customer()
    const reason = String(ctx.body.reason ?? '').trim()
    if (!reason) return validation('reason', 'Escribe el motivo del reinicio')
    if (ifMatch(ctx.req) !== c.version) return preconditionFailed(c)
    const change: KindChange = {
      id: mockUuid('0193d200', hash(c.id + now.getTime())),
      changed_at: now.toISOString(),
      from_kind: c.kind,
      to_kind: 'residential',
      source: 'reset',
      actor_id: ctx.userId,
      confidence: null,
      manual_reason: reason,
      model_ref: null,
      reasons: [],
    }
    mockState.kindHistory.set(c.id, [change, ...(mockState.kindHistory.get(c.id) ?? [])])
    mockState.customers.set(c.id, {
      version: c.version + 1,
      kind: 'residential',
      kind_source: 'default',
      kind_locked: false,
      kind_confidence: null,
      kind_changed_at: now.toISOString(),
      alias: null,
      notes: null,
      reset_at: now.toISOString(),
    })
    const next = customer()
    return json(next, 200, etag(next.version))
  }

  if (match(ctx, 'GET', '/analytics/customers/{id}/traffic')) {
    if (!ctx.can('traffic.customer.read') && !ctx.can('customers.read')) return forbidden()
    const down = analyticsDown(ctx)
    if (down) return down
    const range = ctx.url.searchParams.get('range') ?? '24h'
    const groupBy = ctx.url.searchParams.get('group_by') ?? 'none'
    if (!['none', 'service', 'category', 'asn'].includes(groupBy)) {
      return validation('group_by', 'none, service, category o asn', 'INVALID_VALUE')
    }
    const daily = customerDown(tenant, index) ?? 0
    return json(customerSeries(ctx, range, (daily / 86_400) * 8, index, groupBy !== 'none'))
  }
}

/** Reparto del tráfico de un cliente por categoría (la principal coincide con `traffic_24h`). */
function customerGroups(index: number): { label: string; share: number }[] {
  const main = CATEGORIES[(index * 3) % 5]!
  const others = CATEGORIES.filter((c) => c !== main).slice(0, 3)
  return [
    { label: main, share: 0.46 },
    { label: others[0]!, share: 0.27 },
    { label: others[1]!, share: 0.15 },
    { label: others[2]!, share: 0.08 },
    { label: 'Sin clasificar', share: 0.04 },
  ]
}

function customerSeries(
  ctx: TenantRouteContext,
  range: string,
  avgBps: number,
  seed: number,
  grouped = false,
): SeriesResponse {
  const span = rangeMs(range)
  const step = span <= 6 * 3_600_000 ? 300 : span <= 86_400_000 ? 900 : 3600
  const to = Math.floor(ctx.now.getTime() / (step * 1000)) * step * 1000
  const r = rng(hash(`${ctx.tenant.tenant_id}cust${seed}`))
  const down: [string, number | null][] = []
  const up: [string, number | null][] = []
  for (let at = to - span; at <= to; at += step * 1000) {
    const hour = (new Date(at).getUTCHours() - 5 + 24) % 24
    const shape = 0.25 + 1.5 * Math.exp(-((hour - 21) ** 2) / 10)
    const v = avgBps * shape * (0.7 + r() * 0.6)
    down.push([iso(at), avgBps ? Math.round(v) : null])
    up.push([iso(at), avgBps ? Math.round(v * 0.12) : null])
  }
  const meta = analyticsMeta(ctx, range, step)
  if (!grouped) {
    return {
      data: {
        series: [
          { metric: 'down_bps', unit: 'bps', points: down },
          { metric: 'up_bps', unit: 'bps', points: up },
        ],
      },
      meta,
    }
  }
  const scale = (pts: [string, number | null][], share: number): [string, number | null][] =>
    pts.map(([t, v]) => [t, v === null ? 0 : Math.round(v * share)])
  const series: SeriesResponse['data']['series'] = []
  for (const g of customerGroups(seed)) {
    series.push({ metric: 'down_bps', unit: 'bps', group: g.label, points: scale(down, g.share) })
    series.push({ metric: 'up_bps', unit: 'bps', group: g.label, points: scale(up, g.share) })
  }
  return { data: { series }, meta }
}

// --- Hallazgos -------------------------------------------------------------------------------

function findingRoutes(ctx: TenantRouteContext): Response | undefined {
  const { tenant, now } = ctx
  if (!ctx.path.startsWith('/findings') && ctx.path !== '/security/summary') return
  if (!ctx.can('security.findings.read')) return forbidden()
  const personal = ctx.can('customers.read')

  if (match(ctx, 'GET', '/security/summary')) return json(securitySummary(tenant, now))

  if (match(ctx, 'GET', '/findings')) {
    const q = ctx.url.searchParams
    const states = q.get('state')?.split(',').filter(Boolean)
    const severities = q.get('severity')?.split(',').filter(Boolean)
    const kinds = q.get('kind')?.split(',').filter(Boolean)
    const sites = q.get('site_id')?.split(',').filter(Boolean)
    const list = sortFindings(
      findingsOf(tenant, now, personal).filter(
        (f) =>
          (!states?.length || states.includes(f.state)) &&
          (!severities?.length || severities.includes(f.severity)) &&
          (!kinds?.length || kinds.includes(f.kind)) &&
          (!sites?.length || sites.includes(f.site_id)) &&
          (!q.get('customer_id') || f.customer_id === q.get('customer_id')),
      ),
    )
    return json(paginate(ctx.empty ? [] : list, ctx.url))
  }

  if (match(ctx, 'POST', '/findings/bulk-resolve')) {
    if (!ctx.can('security.findings.manage')) return forbidden()
    const kind = String(ctx.body.kind ?? '')
    const severity = String(ctx.body.severity ?? '')
    const siteId = String(ctx.body.site_id ?? '')
    const comment = String(ctx.body.comment ?? '').trim()
    const states = (ctx.body.states as string[] | undefined) ?? ['open', 'acknowledged']
    if (kind && !/^[a-z][a-z0-9_]*$/.test(kind))
      return validation('kind', 'Tipo de hallazgo no válido.')
    if (severity && !['low', 'medium', 'high', 'critical'].includes(severity)) {
      return validation('severity', 'Severidad no válida (low, medium, high o critical).')
    }
    if (comment.length < 3) {
      return validation('comment', 'El comentario es obligatorio (mínimo 3 caracteres).')
    }
    if (!states.length || states.some((s) => s !== 'open' && s !== 'acknowledged')) {
      return validation(
        'states',
        'Solo se pueden resolver hallazgos open o acknowledged.',
        'INVALID',
      )
    }
    const targets = findingsOf(tenant, now, personal)
      .filter(
        (f) =>
          (!kind || f.kind === kind) &&
          (!severity || f.severity === severity) &&
          (!siteId || f.site_id === siteId) &&
          states.includes(f.state),
      )
      .slice(0, 1000)
    for (const f of targets) {
      mockState.findings.set(f.id, {
        version: f.version + 1,
        state: 'resolved',
        updated_at: now.toISOString(),
        resolution: {
          verdict: 'resolved',
          resolved_at: now.toISOString(),
          resolved_by: ctx.userId,
          comment,
          actions_taken: [],
        },
      })
    }
    return json({ resolved: targets.length })
  }

  const id = ctx.path.split('/')[2] ?? ''
  const finding = findFinding(tenant, id, now, personal)
  if (!finding) return notFound('FINDING_NOT_FOUND')

  if (match(ctx, 'GET', '/findings/{id}')) return json(finding, 200, etag(finding.version))

  if (match(ctx, 'GET', '/findings/{id}/evidence')) {
    if (!ctx.can('security.evidence.read')) return forbidden()
    return json(paginate(evidenceFlows(finding), ctx.url, 25))
  }

  const transition = (
    to: Finding['state'],
    allowed: Finding['state'][],
    extra: () => Partial<Finding> = () => ({}),
  ) => {
    if (!ctx.can('security.findings.manage')) return forbidden()
    const expected = ifMatch(ctx.req)
    if (expected === null) return problem(428, 'PRECONDITION_REQUIRED', 'Falta If-Match')
    if (expected !== finding.version) return preconditionFailed(finding)
    if (!allowed.includes(finding.state)) {
      return problem(409, 'FINDING_STATE_INVALID', 'El hallazgo ya no admite esa acción')
    }
    mockState.findings.set(finding.id, {
      version: finding.version + 1,
      state: to,
      updated_at: now.toISOString(),
      ...extra(),
    })
    const next = findFinding(tenant, id, now, personal)!
    return json(next, 200, etag(next.version))
  }

  if (match(ctx, 'POST', '/findings/{id}/acknowledge')) {
    return transition('acknowledged', ['open'], () => ({
      acknowledged_at: now.toISOString(),
      acknowledged_by: ctx.userId,
    }))
  }
  if (match(ctx, 'POST', '/findings/{id}/resolve')) {
    return transition('resolved', ['open', 'acknowledged'], () => ({
      resolution: {
        verdict: 'resolved',
        resolved_at: now.toISOString(),
        resolved_by: ctx.userId,
        comment: (ctx.body.comment as string) ?? null,
        actions_taken: (ctx.body.actions_taken as string[]) ?? [],
      },
    }))
  }
  if (match(ctx, 'POST', '/findings/{id}/mark-false-positive')) {
    const comment = String(ctx.body.comment ?? '').trim()
    if (!comment) return validation('comment', 'Explica por qué es un falso positivo')
    const days = Number(ctx.body.silence_days ?? 30)
    return transition('false_positive', ['open', 'acknowledged'], () => ({
      resolution: {
        verdict: 'false_positive',
        resolved_at: now.toISOString(),
        resolved_by: ctx.userId,
        comment,
        silence_until: iso(now.getTime() + days * 86_400_000),
      },
    }))
  }
}

// --- Inventario, prefijos y onboarding --------------------------------------------------------

function inventoryRoutes(ctx: TenantRouteContext): Response | undefined {
  const { tenant, now } = ctx
  let p: Params | null
  const routers = () => routersOf(tenant, now, { degraded: ctx.degraded, stepMs: ctx.stepMs })
  const sitesFilter = ctx.url.searchParams.get('site_id')?.split(',').filter(Boolean)

  if (match(ctx, 'GET', '/sites')) {
    if (!ctx.can('sites.read') && !ctx.can('devices.read')) return forbidden()
    return json(
      paginate(
        sitesOf(tenant).map((s) => toSite(tenant, s)),
        ctx.url,
      ),
    )
  }
  if ((p = match(ctx, 'GET', '/sites/{id}'))) {
    const site = findSite(tenant, p.id!)
    if (!site) return notFound('SITE_NOT_FOUND')
    const s = toSite(tenant, site)
    return json(s, 200, etag(s.version))
  }
  if ((p = match(ctx, 'GET', '/sites/{id}/client-prefixes'))) {
    const site = findSite(tenant, p.id!)
    if (!site) return notFound('SITE_NOT_FOUND')
    const customers = customersOf(tenant, now)
    const role = ctx.url.searchParams.get('role')
    const list = prefixesOf(tenant, site)
      .filter((x) => !role || x.role === role)
      .map((x) => ({
        ...x,
        customers_count:
          x.role === 'customers'
            ? customers.filter((c) => c.client_prefix_id === x.id && c.status === 'active').length
            : null,
      }))
    return json(paginate(list, ctx.url))
  }
  if ((p = match(ctx, 'POST', '/sites/{id}/client-prefixes'))) {
    if (!ctx.can('sites.update')) return forbidden()
    const site = findSite(tenant, p.id!)
    if (!site) return notFound('SITE_NOT_FOUND')
    const input = ctx.body as unknown as ClientPrefixInput
    const err = checkPrefix(tenant, site, input)
    if (err) return err
    const created = newPrefix(tenant, site, input, now)
    prefixesOf(tenant, site).push(created)
    return json(created, 201, etag(1))
  }
  if ((p = match(ctx, 'POST', '/sites/{id}/client-prefixes/batch'))) {
    if (!ctx.can('sites.update')) return forbidden()
    if (!ctx.req.headers.get('Idempotency-Key')) {
      return problem(428, 'PRECONDITION_REQUIRED', 'Falta Idempotency-Key')
    }
    const site = findSite(tenant, p.id!)
    if (!site) return notFound('SITE_NOT_FOUND')
    const items = (ctx.body.items as ClientPrefixInput[] | undefined) ?? []
    if (!items.length) return validation('items', 'Elige al menos un prefijo')
    // Todo o nada: si uno solapa, no se aplica ninguno.
    for (const [i, item] of items.entries()) {
      const err = checkPrefix(tenant, site, item, `items.${i}.prefix`, items.slice(0, i))
      if (err) return err
    }
    const created = items.map((item) => newPrefix(tenant, site, item, now))
    prefixesOf(tenant, site).push(...created)
    return json({ data: created }, 201)
  }
  if ((p = match(ctx, 'POST', '/sites/{id}/unattributed-addresses'))) {
    if (!ctx.can('customers.read')) return forbidden()
    const site = findSite(tenant, p.id!)
    if (!site) return notFound('SITE_NOT_FOUND')
    const down = analyticsDown(ctx)
    if (down) return down
    const prefix = String(ctx.body.prefix ?? '')
    if (!/\//.test(prefix)) {
      return problem(422, 'VALIDATION_FAILED', 'prefix must be a CIDR')
    }
    const proposal = prefixProposals(tenant, site).find((x) => x.prefix === prefix)
    if (!proposal) return json({ data: [], meta: analyticsMeta(ctx, '7d', null) })
    const n = Math.min(Number(ctx.body.n ?? 50), 200)
    const base = prefix.includes(':')
      ? prefix.replace(/::\/\d+$/, '::')
      : prefix.replace(/\.0\/\d+$/, '.')
    const total = Number(proposal.bytes)
    const count = Math.min(n, proposal.distinct_ips)
    const data = Array.from({ length: count }, (_, i) => ({
      address: prefix.includes(':') ? `${base}${(i + 1).toString(16)}` : `${base}${i + 1}`,
      bytes: String(Math.round(total / (2 * (i + 1)))),
      packets: String(Math.round(total / (2 * (i + 1)) / 900)),
      last_seen_at: new Date(ctx.now.getTime() - i * 600_000).toISOString(),
    }))
    return json({ data, meta: analyticsMeta(ctx, '7d', null) })
  }
  if ((p = match(ctx, 'GET', '/sites/{id}/prefix-proposals'))) {
    const site = findSite(tenant, p.id!)
    if (!site) return notFound('SITE_NOT_FOUND')
    const down = analyticsDown(ctx)
    if (down) return down
    return json({ data: prefixProposals(tenant, site), meta: analyticsMeta(ctx, '7d', null) })
  }
  if (
    (p = match(ctx, 'PATCH', '/client-prefixes/{id}')) ||
    (p = match(ctx, 'DELETE', '/client-prefixes/{id}'))
  ) {
    if (!ctx.can('sites.update')) return forbidden()
    for (const site of sitesOf(tenant)) {
      const list = prefixesOf(tenant, site)
      const i = list.findIndex((x) => x.id === p!.id)
      if (i < 0) continue
      const current = list[i]!
      if (ifMatch(ctx.req) !== current.version) return preconditionFailed(current)
      if (ctx.method === 'DELETE') {
        list.splice(i, 1)
        return noContent()
      }
      const next: ClientPrefix = {
        ...current,
        ...(ctx.body as Partial<ClientPrefix>),
        version: current.version + 1,
        updated_at: now.toISOString(),
      }
      list[i] = next
      return json(next, 200, etag(next.version))
    }
    return notFound('CLIENT_PREFIX_NOT_FOUND')
  }

  if (match(ctx, 'GET', '/routers')) {
    if (!ctx.can('devices.read')) return forbidden()
    const list = routers()
      .filter((r) => !sitesFilter?.length || sitesFilter.includes(r.site.id))
      .map((r) => r.router)
    return json(paginate(list, ctx.url))
  }
  if (match(ctx, 'GET', '/flow-exporters')) {
    const list = routers()
      .filter((r) => !sitesFilter?.length || sitesFilter.includes(r.site.id))
      .map((r) => r.exporter)
    return json({ data: list })
  }
  if (match(ctx, 'GET', '/wireguard/peers')) {
    const routerId = ctx.url.searchParams.get('router_id')
    return json(
      paginate(
        routers()
          .filter((r) => !routerId || r.router.id === routerId)
          .map((r) => r.peer),
        ctx.url,
      ),
    )
  }
  if ((p = match(ctx, 'POST', '/wireguard/enrollment-tokens/{id}/revoke'))) {
    const run = [...mockState.onboarding.values()].find((r) => r.tokenId === p!.id)
    if (!run) return notFound()
    run.revokedAt = now.getTime()
    return noContent()
  }

  const routerMatch = ctx.path.match(/^\/(?:routers|flow-exporters)\/([^/]+)/)
  if (!routerMatch) return
  const item = routers().find((r) => r.router.id === routerMatch[1])
  if (!item) return notFound('ROUTER_NOT_FOUND')

  const pinRes = tlsPinRoute(ctx, item)
  if (pinRes) return pinRes
  if (match(ctx, 'GET', '/routers/{id}')) return json(item.router, 200, etag(item.router.version))
  if (match(ctx, 'GET', '/flow-exporters/{id}')) return json(item.exporter)
  if (match(ctx, 'POST', '/routers/{id}/provisioning-script')) {
    if (!ctx.can('wireguard.write')) return forbidden()
    if (ctx.reauthRequired) {
      return problem(403, 'REAUTH_REQUIRED', 'Vuelve a identificarte para generar el script')
    }
    if (!ctx.req.headers.get('Idempotency-Key')) {
      return problem(428, 'PRECONDITION_REQUIRED', 'Falta Idempotency-Key')
    }
    if (item.router.routeros_version_supported === false) {
      return problem(
        422,
        'ROUTEROS_VERSION_UNSUPPORTED',
        'RouterOS anterior a 7.12: actualiza el router antes de generar el script',
      )
    }
    const tokenId = mockUuid('0193e000', hash(item.router.id + now.getTime()))
    const ttl = readEnrollTtl()
    const expiresAt = now.getTime() + ttl
    // Regenerar invalida el token anterior.
    mockState.onboarding.set(item.router.id, { scriptAt: now.getTime(), tokenId, expiresAt })
    const token = `hfe_${randomHex(32)}`
    return text(provisioningScript(tenant, item, token, 'horus.fibranorte.example'), 201, {
      'X-Horus-Enrollment-Token-Id': tokenId,
      'X-Horus-Enrollment-Token-Expires-At': iso(expiresAt),
    })
  }
  if (match(ctx, 'POST', '/routers/{id}/direct-onboarding')) {
    if (!ctx.can('devices.credentials.write')) return forbidden()
    if (ctx.reauthRequired) {
      return problem(403, 'REAUTH_REQUIRED', 'Vuelve a identificarte para generar la configuración')
    }
    if (item.router.connection_mode !== 'direct') {
      return problem(
        422,
        'ROUTER_VENDOR_NOT_SUPPORTED',
        'El alta directa es para routers sin WireGuard (Huawei VRP).',
      )
    }
    const body = ctx.body as { management_address?: string | null; sampling_rate?: number | null }
    const address = body.management_address ?? item.router.management_address
    if (!address || !isManagementAddress(address)) {
      return validation(
        'management_address',
        'IP unicast del router tal y como la ve Horus.',
        address ? 'INVALID_VALUE' : 'REQUIRED',
      )
    }
    const dev = devicesOf(tenant)
    const edit = dev.routerEdits.get(item.router.id)
    dev.routerEdits.set(item.router.id, {
      version: item.router.version + 1,
      updated_at: iso(now.getTime()),
      fields: { ...(edit?.fields ?? { name: item.router.name }), management_address: address },
    })
    mockState.onboarding.set(item.router.id, {
      scriptAt: now.getTime(),
      tokenId: mockUuid('0193e100', hash(item.router.id + now.getTime())),
      expiresAt: now.getTime() + 24 * 3_600_000,
    })
    return json(directOnboarding(item, address, body.sampling_rate ?? 1000), 201, {
      'Cache-Control': 'no-store',
    })
  }
  if (match(ctx, 'PUT', '/routers/{id}/transport')) {
    if (!ctx.can('devices.credentials.write')) return forbidden()
    if (ctx.reauthRequired) {
      return problem(403, 'REAUTH_REQUIRED', 'Vuelve a identificarte para cambiar el transporte')
    }
    const body = ctx.body as {
      connection_mode?: string
      tunnel_remote_address?: string | null
      tunnel_remote_id?: string | null
      path?: string | null
      confirm_unencrypted_internet?: boolean
    }
    const mode = body.connection_mode as TransportOverride['mode']
    const allowed =
      item.router.vendor === 'huawei' ? ['ipsec', 'gre', 'direct'] : ['wireguard', 'ipsec', 'gre']
    if (!allowed.includes(mode)) {
      return problem(
        422,
        'CONNECTION_MODE_NOT_SUPPORTED',
        'Este fabricante no admite ese transporte.',
      )
    }
    const address = body.tunnel_remote_address?.trim() || null
    let id = body.tunnel_remote_id?.trim() || null
    if (mode === 'gre' && !address) {
      return validation(
        'tunnel_remote_address',
        'GRE necesita la IPv4 pública del router.',
        'REQUIRED',
      )
    }
    if (mode === 'ipsec') {
      id = id ?? address
      if (!id) return validation('tunnel_remote_id', 'Indique la identidad IKE.', 'REQUIRED')
    }
    const encrypted = mode === 'wireguard' || mode === 'ipsec'
    if (!encrypted && body.path !== 'private' && body.path !== 'internet') {
      return validation('path', 'Declare el camino: private o internet.', 'REQUIRED')
    }
    const level: TransportOverride['level'] = encrypted
      ? 'encrypted'
      : body.path === 'private'
        ? 'unencrypted_private'
        : 'unencrypted_internet'
    if (level === 'unencrypted_internet' && !body.confirm_unencrypted_internet) {
      return problem(
        422,
        'UNENCRYPTED_TRANSPORT_CONFIRMATION_REQUIRED',
        'Gestión y flujos viajarían sin cifrar por Internet: confirme explícitamente.',
      )
    }
    const taken = routers().find(
      (r) =>
        r.router.id !== item.router.id &&
        ((mode === 'gre' &&
          r.router.connection_mode === 'gre' &&
          r.router.tunnel_remote_address === address) ||
          (mode === 'ipsec' &&
            r.router.connection_mode === 'ipsec' &&
            r.router.tunnel_remote_id === id)),
    )
    if (taken)
      return problem(409, 'TUNNEL_REMOTE_IN_USE', 'Otro router ya usa esa IP pública o identidad.')
    const cur = mockState.transports.get(item.router.id)
    const same =
      (cur?.mode ?? item.router.connection_mode) === mode &&
      (cur?.level ?? item.router.protection_level) === level &&
      (cur?.remoteAddress ?? item.router.tunnel_remote_address) ===
        (mode === 'ipsec' || mode === 'gre' ? address : null) &&
      (cur?.remoteId ?? item.router.tunnel_remote_id) === (mode === 'ipsec' ? id : null)
    if (!same) {
      mockState.transports.set(item.router.id, {
        mode,
        level,
        remoteAddress: mode === 'ipsec' || mode === 'gre' ? address : null,
        remoteId: mode === 'ipsec' ? id : null,
        at: now.getTime(),
      })
      mockState.onboarding.delete(item.router.id)
    }
    const next = routers().find((r) => r.router.id === item.router.id)!
    return json(next.router, 200, etag(next.router.version))
  }
  if (match(ctx, 'POST', '/routers/{id}/tunnel-config')) {
    if (!ctx.can('wireguard.write')) return forbidden()
    if (ctx.reauthRequired) {
      return problem(403, 'REAUTH_REQUIRED', 'Vuelve a identificarte para generar la configuración')
    }
    const transport = mockState.transports.get(item.router.id)
    if (!transport || (transport.mode !== 'ipsec' && transport.mode !== 'gre')) {
      return problem(422, 'CONNECTION_MODE_MISMATCH', 'Este router no usa IPsec ni GRE.')
    }
    mockState.onboarding.set(item.router.id, {
      scriptAt: now.getTime(),
      tokenId: mockUuid('0193e200', hash(item.router.id + now.getTime())),
      expiresAt: now.getTime() + 24 * 3_600_000,
    })
    return json(tunnelConfig(item, transport, now.getTime()), 201, { 'Cache-Control': 'no-store' })
  }
  if (match(ctx, 'POST', '/routers/{id}/deprovisioning-script')) {
    if (!ctx.can('wireguard.write')) return forbidden()
    return text(deprovisioningScript(item))
  }
  if (match(ctx, 'POST', '/routers/{id}/prefix-import-preview')) {
    if (!ctx.can('sites.update')) return forbidden()
    if (item.router.onboarding_state === 'pending_configuration') {
      return problem(502, 'ROUTER_UNREACHABLE', 'El router no responde por el túnel')
    }
    // Horus solo lee el router con la huella TLS confirmada por un administrador.
    const pin = tlsPinOf(item, now)
    if (pin.status === 'pending_confirmation' || pin.status === 'not_captured') {
      return problem(409, 'ROUTER_TLS_FINGERPRINT_UNCONFIRMED', 'Confirme la huella TLS del router')
    }
    if (pin.status === 'changed') {
      return problem(409, 'ROUTER_TLS_FINGERPRINT_CHANGED', 'La huella TLS del router cambió')
    }
    return json(prefixImportPreview(tenant, item, now))
  }
}

/** Vida del token de enrolamiento (24 h; `localStorage['horus.mock.enrollTtlMs']` en pruebas). */
function readEnrollTtl() {
  try {
    const v = Number(window.localStorage.getItem('horus.mock.enrollTtlMs'))
    if (Number.isFinite(v) && v > 0) return v
  } catch {
    // sin storage
  }
  return 24 * 3_600_000
}

function checkPrefix(
  tenant: MockTenant,
  site: ReturnType<typeof findSite> & object,
  input: ClientPrefixInput,
  field = 'prefix',
  pending: ClientPrefixInput[] = [],
) {
  const parsed = parseCidr(String(input.prefix ?? ''))
  if (!parsed)
    return validation(
      field,
      'Escribe un prefijo en notación CIDR, p. ej. 10.20.0.0/22',
      'INVALID_CIDR',
    )
  if (!['customers', 'infrastructure', 'excluded'].includes(input.role)) {
    return validation(field.replace('prefix', 'role'), 'Elige un rol', 'REQUIRED')
  }
  const clash =
    overlapping(tenant, site, input.prefix) ??
    pending.find((x) => {
      const q = parseCidr(x.prefix)
      return q && (contains(parsed, q) || contains(q, parsed))
    })
  if (clash) {
    return problem(
      409,
      'CLIENT_PREFIX_OVERLAP',
      'El prefijo solapa con otro del nodo',
      {},
      {
        detail: `Solapa con ${clash.prefix}, ya declarado en este nodo: quítalo o edítalo en lugar de añadir otro.`,
        errors: [
          {
            field,
            code: 'OVERLAP',
            message: `Solapa con ${clash.prefix}, ya declarado en este nodo: quítalo o edítalo en lugar de añadir otro.`,
          },
        ],
      },
    )
  }
  return null
}

// --- Analítica y vista previa de widgets -------------------------------------------------------

/**
 * Top por ASN remoto u organización, como el servicio de analítica: clave = número de ASN
 * (`asns`) o slug de la organización (`organizations`; "AS<n>" si el ASN no está en el
 * catálogo); etiqueta "AS15169 · Google LLC" o solo "AS263237" si no se conoce el nombre.
 * Ordena y reparte la cuota según `direction`; lo que no cabe en N va a "Otros".
 */
function remoteTop(ctx: TenantRouteContext, dimension: 'asns' | 'organizations', n: number) {
  const q = ctx.url.searchParams
  const r = rng(hash(`${ctx.tenant.tenant_id}:asns`))
  const sites = q.get('site_id')?.split(',').filter(Boolean) ?? []
  const siteShare = sites.length ? Math.min(1, sites.length / ctx.tenant.sites.length) : 1
  const total =
    38e12 *
    tenantScale(ctx.tenant) *
    siteShare *
    (rangeMs(q.get('range')) / 86_400_000) *
    // Cada nodo tiene una mezcla algo distinta.
    (sites.length ? 0.9 + (hash(sites.join(',')) % 20) / 100 : 1)
  let rest = total
  const perAsn = REMOTE_ASNS.map((a, i) => {
    const down = Math.round(rest * (i === 0 ? 0.34 : 0.18 + r() * 0.1))
    rest -= down
    return { ...a, down, up: Math.round(down * (0.03 + r() * 0.12)) }
  })
  const groups = new Map<string, { key: string; label: string; down: number; up: number }>()
  // Las reglas ASN propias del ISP ganan al catálogo y al dataset (Administración › ASN propios).
  const rules = asnRulesOf(ctx.tenant.tenant_id)
  for (const a of perAsn) {
    const rule = rules.find((r) => r.asn === a.asn)
    const key =
      dimension === 'asns'
        ? String(a.asn)
        : rule
          ? `tenant-asn-${a.asn}`
          : a.org
            ? slugify(a.org)
            : `AS${a.asn}`
    const name = rule?.name ?? a.name
    const label =
      dimension === 'asns'
        ? `AS${a.asn}${name ? ` · ${name}` : ''}`
        : (rule?.name ?? a.org ?? a.name ?? `AS${a.asn}`)
    const g = groups.get(key) ?? { key, label, down: 0, up: 0 }
    g.down += a.down
    g.up += a.up
    groups.set(key, g)
  }
  const direction = q.get('direction') ?? 'both'
  const weight = (g: { down: number; up: number }) =>
    direction === 'download' ? g.down : direction === 'upload' ? g.up : g.down + g.up
  const all = [...groups.values()].sort((a, b) => weight(b) - weight(a))
  const unclassified = { down: Math.round(rest), up: Math.round(rest * 0.08) }
  const totalWeight = all.reduce((acc, g) => acc + weight(g), weight(unclassified))
  const top = all.slice(0, n)
  const others = all
    .slice(n)
    .reduce((acc, g) => ({ down: acc.down + g.down, up: acc.up + g.up }), unclassified)
  const totals = all.reduce((acc, g) => ({ down: acc.down + g.down, up: acc.up + g.up }), {
    ...unclassified,
  })
  return {
    rows: top.map((g) => ({
      key: g.key,
      label: g.label,
      down_bytes: String(g.down),
      up_bytes: String(g.up),
      share: totalWeight ? weight(g) / totalWeight : 0,
    })),
    others: { down_bytes: String(others.down), up_bytes: String(others.up) },
    totals: { down_bytes: String(totals.down), up_bytes: String(totals.up) },
  }
}

function slugify(text: string) {
  return text
    .toLowerCase()
    .normalize('NFD')
    .replace(/[\u0300-\u036f]/g, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-|-$/g, '')
}

function analyticsRoutes(ctx: TenantRouteContext): Response | undefined {
  const { tenant, now } = ctx
  if (!ctx.path.startsWith('/analytics/traffic') && ctx.path !== '/widget-data/preview') return
  if (!ctx.can('traffic.read')) return forbidden()
  const down = analyticsDown(ctx)
  if (down) return down
  const range = ctx.url.searchParams.get('range')

  if (match(ctx, 'GET', '/analytics/traffic/attribution')) {
    return json({
      data: { attributed_ratio: 0.92, infrastructure_ratio: 0.03, unattributed_ratio: 0.05 },
      meta: analyticsMeta(ctx, range, null),
    })
  }
  if (match(ctx, 'GET', '/analytics/traffic/top')) {
    const dimension = ctx.url.searchParams.get('dimension') ?? 'categories'
    const n = Number(ctx.url.searchParams.get('n') ?? 10)
    if (dimension === 'asns' || dimension === 'organizations') {
      return json({
        data: { dimension, ...remoteTop(ctx, dimension, n) },
        meta: analyticsMeta(ctx, range, null),
      })
    }
    const scale = rangeMs(range) / 86_400_000
    let rows: TopResult['rows']
    if (dimension === 'customers') {
      rows = customersOf(tenant, now)
        .filter((c) => c.traffic_24h)
        .sort((a, b) => Number(b.traffic_24h!.down_bytes) - Number(a.traffic_24h!.down_bytes))
        .slice(0, n)
        .map((c) => ({
          key: c.id,
          label: c.alias ?? c.address,
          down_bytes: String(Math.round(Number(c.traffic_24h!.down_bytes) * scale)),
          up_bytes: String(Math.round(Number(c.traffic_24h!.up_bytes) * scale)),
          share: 0,
          customer: {
            customer_id: c.id,
            address: ctx.can('customers.read') ? c.address : maskIp(c.address),
            address_masked: !ctx.can('customers.read'),
            alias: c.alias,
            kind: c.kind,
          },
        }))
    } else {
      rows = CATEGORIES.slice(0, n).map((label, i) => ({
        key: String(i),
        label,
        down_bytes: String(Math.round((4e12 / (i + 1)) * scale)),
        up_bytes: String(Math.round((3e11 / (i + 1)) * scale)),
        share: 0,
      }))
    }
    return json({
      data: {
        dimension,
        rows,
        others: { down_bytes: '0', up_bytes: '0' },
        totals: {
          down_bytes: String(rows.reduce((a, r) => a + Number(r.down_bytes), 0)),
          up_bytes: String(rows.reduce((a, r) => a + Number(r.up_bytes), 0)),
        },
      },
      meta: analyticsMeta(ctx, range, null),
    })
  }
  if (match(ctx, 'POST', '/widget-data/preview')) {
    const type = String(ctx.body.type ?? '')
    const config = (ctx.body.config as Record<string, unknown>) ?? {}
    const r = String(ctx.body.range ?? config.range ?? '24h')
    const data = widgetData(
      {
        tenant,
        widget: {
          id: `preview-${type}`,
          type,
          title: null,
          position: { x: 0, y: 0, w: 6, h: 4 },
          config: { ...config, range: r },
        },
        now,
        canSeePersonalData: ctx.can('customers.read'),
        empty: ctx.empty,
      },
      { degraded: false, scenario: ctx.scenario },
    )
    if (!data) return problem(422, 'WIDGET_TYPE_UNKNOWN', 'Tipo de widget desconocido')
    // Rangos largos: parte del periodo antes de que el router exportara (datos incompletos).
    if (rangeMs(r) >= 30 * 86_400_000) data.meta.partial = true
    return json(data)
  }
}

type MockPlaylist = {
  id: string
  tenant_id: string
  name: string
  transition: 'fade' | 'none'
  version: number
  created_at: string
  updated_at: string
  items: { dashboard_id: string; duration_seconds: number }[]
}
const playlistStore = new Map<string, MockPlaylist[]>()

function playlistsOf(ctx: TenantRouteContext) {
  let list = playlistStore.get(ctx.tenant.tenant_id)
  if (!list) {
    list = [
      {
        id: mockUuid('0194a000', 1),
        tenant_id: ctx.tenant.tenant_id,
        name: 'Sala NOC',
        transition: 'fade',
        version: 1,
        created_at: '2026-09-10T12:00:00.000Z',
        updated_at: '2026-09-10T12:00:00.000Z',
        items: [
          { dashboard_id: '0192f000-0000-7000-8000-00000000d001', duration_seconds: 30 },
          { dashboard_id: '0192f000-0000-7000-8000-00000000d002', duration_seconds: 30 },
        ],
      },
    ]
    playlistStore.set(ctx.tenant.tenant_id, list)
  }
  return list
}

/** Valida PlaylistInput (playlist.schema.json): nombre ≤ 80, 1–20 elementos de 10–3600 s. */
function playlistInvalid(body: Record<string, unknown>, partial: boolean) {
  if (!partial || 'name' in body) {
    const name = typeof body.name === 'string' ? body.name.trim() : ''
    if (!name) return validation('name', 'Escribe un nombre')
    if (name.length > 80) return validation('name', 'Máximo 80 caracteres', 'TOO_LONG')
  }
  if (!partial || 'items' in body) {
    const items = Array.isArray(body.items) ? body.items : []
    if (items.length < 1 || items.length > 20)
      return validation('items', 'Entre 1 y 20 dashboards', 'OUT_OF_RANGE')
    for (const it of items as { dashboard_id?: unknown; duration_seconds?: unknown }[]) {
      const d = Number(it.duration_seconds)
      if (typeof it.dashboard_id !== 'string' || !Number.isInteger(d) || d < 10 || d > 3600)
        return validation('items', 'Cada dashboard dura entre 10 y 3600 s', 'OUT_OF_RANGE')
    }
  }
  return null
}

function playlistRoutes(ctx: TenantRouteContext): Response | undefined {
  if (match(ctx, 'GET', '/playlists')) {
    return json({
      data: playlistsOf(ctx),
      page: { limit: 50, has_more: false, next_cursor: null, prev_cursor: null },
    })
  }
  if (match(ctx, 'POST', '/playlists')) {
    if (!ctx.can('dashboards.manage')) return forbidden()
    const bad = playlistInvalid(ctx.body, false)
    if (bad) return bad
    const now = ctx.now.toISOString()
    const pl: MockPlaylist = {
      id: crypto.randomUUID(),
      tenant_id: ctx.tenant.tenant_id,
      name: String(ctx.body.name).trim(),
      transition: ctx.body.transition === 'none' ? 'none' : 'fade',
      version: 1,
      created_at: now,
      updated_at: now,
      items: ctx.body.items as MockPlaylist['items'],
    }
    playlistsOf(ctx).push(pl)
    return json(pl, 201, etag(1))
  }
  const one = match(ctx, 'PATCH', '/playlists/{id}') ?? match(ctx, 'DELETE', '/playlists/{id}')
  if (one) {
    if (!ctx.can('dashboards.manage')) return forbidden()
    const list = playlistsOf(ctx)
    const pl = list.find((x) => x.id === one.id)
    if (!pl) return notFound()
    const expected = ifMatch(ctx.req)
    if (expected === null) return problem(428, 'PRECONDITION_REQUIRED', 'Falta If-Match')
    if (expected !== pl.version) return preconditionFailed(pl)
    if (ctx.method === 'DELETE') {
      list.splice(list.indexOf(pl), 1)
      return noContent()
    }
    const bad = playlistInvalid(ctx.body, true)
    if (bad) return bad
    if (typeof ctx.body.name === 'string') pl.name = ctx.body.name.trim()
    if (Array.isArray(ctx.body.items)) pl.items = ctx.body.items as MockPlaylist['items']
    if (ctx.body.transition === 'fade' || ctx.body.transition === 'none')
      pl.transition = ctx.body.transition
    pl.version++
    pl.updated_at = ctx.now.toISOString()
    return json(pl, 200, etag(pl.version))
  }
}

// --- Clasificación (módulo flows): con qué datasets se clasifica el tráfico -------------------
function classificationRoute(ctx: TenantRouteContext): Response | undefined {
  if (ctx.path !== '/classification/status') return undefined
  const missing = ctx.scenario === 'no-asn'
  return json({
    source: 'ingester',
    data: {
      asn_dataset: missing
        ? { loaded: false }
        : {
            loaded: true,
            version: '2026-10-12',
            created_at: new Date(ctx.now.getTime() - 86_400_000).toISOString(),
            entries: 1234567,
          },
      catalog: { loaded: true, version: 'v3' },
      reputation: { loaded: true, version: '2026-10-13' },
    },
  })
}

export function tenantRoute(ctx: TenantRouteContext): Response | undefined {
  return (
    integrationsRoute(ctx) ??
    customerRoutes(ctx) ??
    findingRoutes(ctx) ??
    switchRoutes(ctx) ??
    deviceRoutes(ctx) ??
    snmpRoutes(ctx) ??
    capacityRoutes(ctx) ??
    infraServiceRoutes(ctx) ??
    inventoryRoutes(ctx) ??
    classificationRoute(ctx) ??
    analyticsRoutes(ctx) ??
    playlistRoutes(ctx) ??
    detectionSettingsRoute(ctx) ??
    asnRulesRoute(ctx) ??
    reportRoutes(ctx) ??
    assistRoute(ctx)
  )
}
