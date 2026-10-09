import type {
  ClientPrefix,
  ClientPrefixInput,
  CustomerDetail,
  Finding,
  KindChange,
  SeriesResponse,
  TopResult,
} from '~~/types/api'
import { CATEGORIES, maskIp } from './base'
import type { MockTenant } from './data'
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
import { mockState } from './state'
import { widgetData } from './widget-data'

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
  empty: boolean
  /** Paso del asistente de alta simulado (ms entre "clave recibida", "túnel" y "primer flujo"). */
  stepMs: number
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

/** Rango relativo → ms. */
function rangeMs(range: string | null) {
  const map: Record<string, number> = {
    '15m': 0.25,
    '1h': 1,
    '6h': 6,
    '24h': 24,
    '7d': 168,
    '30d': 720,
    '90d': 2160,
  }
  return (map[range ?? '24h'] ?? 24) * 3_600_000
}

function analyticsMeta(ctx: TenantRouteContext, range: string | null, step: number | null) {
  const to = ctx.now.getTime()
  const span = rangeMs(range)
  // 30 días o más: una parte del rango cae antes de que el router empezara a exportar.
  const partial = span >= 30 * 86_400_000
  return {
    from: iso(to - span),
    to: iso(to),
    step,
    coverage: partial ? 0.71 : 0.97,
    partial,
    generated_at: ctx.now.toISOString(),
    source: 'flows',
  }
}

function analyticsDown(ctx: TenantRouteContext) {
  return ctx.degraded
    ? problem(503, 'ANALYTICS_UNAVAILABLE', 'Analítica no disponible', { 'Retry-After': '30' })
    : null
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
      (!alias || (c.alias ?? '').toLowerCase().includes(alias)),
  )
}

function sortCustomers(list: CustomerDetail[], sort: string | null) {
  const key = (sort ?? '-last_seen').replace(/^-/, '') as 'last_seen' | 'first_seen'
  const dir = sort?.startsWith('-') === false ? 1 : -1
  return [...list].sort((a, b) => dir * String(a[key] ?? '').localeCompare(String(b[key] ?? '')))
}

/** Vista de lista: sin notas ni sugerencias (solo en la ficha). */
function listItem(c: CustomerDetail) {
  const {
    notes: _n,
    suggested_kind: _k,
    suggested_confidence: _c,
    suggested_reasons: _r,
    ...rest
  } = c
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
        kind_confidence: 0.58,
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
    const daily = customerDown(tenant, index) ?? 0
    return json(customerSeries(ctx, range, (daily / 86_400) * 8, index))
  }
}

function customerSeries(
  ctx: TenantRouteContext,
  range: string,
  avgBps: number,
  seed: number,
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
  return {
    data: {
      series: [
        { metric: 'down_bps', unit: 'bps', points: down },
        { metric: 'up_bps', unit: 'bps', points: up },
      ],
    },
    meta: analyticsMeta(ctx, range, step),
  }
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
  if ((p = match(ctx, 'GET', '/sites/{id}/prefix-proposals'))) {
    const site = findSite(tenant, p.id!)
    if (!site) return notFound('SITE_NOT_FOUND')
    const down = analyticsDown(ctx)
    if (down) return down
    return json({ data: prefixProposals(tenant, site), meta: analyticsMeta(ctx, '7d', null) })
  }
  if (
    match(ctx, 'PATCH', '/client-prefixes/{id}') ||
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

  if (match(ctx, 'GET', '/routers/{id}')) return json(item.router, 200, etag(item.router.version))
  if (match(ctx, 'GET', '/flow-exporters/{id}')) return json(item.exporter)
  if (match(ctx, 'POST', '/routers/{id}/provisioning-script')) {
    if (!ctx.can('wireguard.write')) return forbidden()
    if (!ctx.req.headers.get('Idempotency-Key')) {
      return problem(428, 'PRECONDITION_REQUIRED', 'Falta Idempotency-Key')
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
  if (match(ctx, 'POST', '/routers/{id}/deprovisioning-script')) {
    if (!ctx.can('wireguard.write')) return forbidden()
    return text(deprovisioningScript(item))
  }
  if (match(ctx, 'POST', '/routers/{id}/prefix-import-preview')) {
    if (!ctx.can('sites.update')) return forbidden()
    if (item.router.onboarding_state === 'pending_configuration') {
      return problem(502, 'ROUTER_UNREACHABLE', 'El router no responde por el túnel')
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
        detail: `Solapa con ${clash.prefix}`,
        errors: [
          {
            field,
            code: 'OVERLAP',
            message: `Solapa con ${clash.prefix} (ya declarado en este nodo)`,
          },
        ],
      },
    )
  }
  return null
}

// --- Analítica y vista previa de widgets -------------------------------------------------------

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
      { degraded: false },
    )
    if (!data) return problem(422, 'WIDGET_TYPE_UNKNOWN', 'Tipo de widget desconocido')
    // Rangos largos: parte del periodo antes de que el router exportara (datos incompletos).
    if (rangeMs(r) >= 30 * 86_400_000) data.meta.partial = true
    return json(data)
  }
}

function playlistRoutes(ctx: TenantRouteContext): Response | undefined {
  if (match(ctx, 'GET', '/playlists')) {
    return json({
      data: [
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
      ],
      page: { limit: 50, has_more: false, next_cursor: null, prev_cursor: null },
    })
  }
}

export function tenantRoute(ctx: TenantRouteContext): Response | undefined {
  return (
    customerRoutes(ctx) ??
    findingRoutes(ctx) ??
    inventoryRoutes(ctx) ??
    analyticsRoutes(ctx) ??
    playlistRoutes(ctx)
  )
}
