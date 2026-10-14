import type {
  InfraCheck,
  InfraCheckResult,
  InfraCheckStatus,
  InfraRouterSnippet,
  InfraService,
  InfraServiceState,
} from '~~/types/api'
import type { MockTenant } from './data'
import { forbidden, ifMatch, json, noContent, notFound, problem } from './http'
import { routersOf, sitesOf } from './inventory'
import { hash, mockUuid, rng } from './random'
import { mockState } from './state'
import type { TenantRouteContext } from './tenant-routes'

/**
 * Servicios de infraestructura del ISP (rol snmp): inventario con chequeos activos (ping, TCP,
 * HTTP) y pasivos (sin tráfico, ataque entrante), estado = el peor de sus chequeos conocidos,
 * historial reciente en el detalle y el script RouterOS para servidores privados. Las IPs son
 * de rangos de documentación (servidores del ISP, nunca clientes).
 */

const RED_SERVICIOS = '10.255.0.0/24'

function checkId(serviceId: string, i: number) {
  return mockUuid('0195a100', hash(serviceId + ':' + i))
}

interface SeedCheck {
  check: InfraCheck
  state: InfraServiceState
  reason: string
  latency?: number
  detail?: string
}

function status(
  serviceId: string,
  c: SeedCheck,
  i: number,
  now: number,
  sinceMin: number,
): InfraCheckStatus {
  const at = new Date(now - 20_000).toISOString()
  return {
    check_id: checkId(serviceId, i),
    type: c.check.type,
    state: c.state,
    reason: c.reason,
    since: new Date(now - sinceMin * 60_000).toISOString(),
    last_checked_at: at,
    latency_ms: c.latency ?? null,
    consecutive_failures: c.state === 'down' || c.state === 'degraded' ? 4 : 0,
    consecutive_successes: c.state === 'up' ? 12 : 0,
    detail: c.detail ?? null,
  }
}

function seed(tenant: MockTenant, now: Date): InfraService[] {
  const t = now.getTime()
  const sites = sitesOf(tenant)
  const site = (i: number) => sites[i % Math.max(1, sites.length)]?.id ?? null
  const base = {
    tenant_id: tenant.tenant_id,
    created_at: '2026-10-01T12:00:00.000Z',
    updated_at: '2026-10-01T12:00:00.000Z',
    version: 1,
    interval_seconds: 60,
    failures_before_down: 3,
    recoveries_before_up: 2,
    enabled: true,
    probe_via_router_id: null,
  }
  const make = (
    key: string,
    name: string,
    kind: string,
    target: string,
    siteId: string | null,
    checks: SeedCheck[],
    sinceMin: number,
    extra: Partial<InfraService> = {},
  ): InfraService => {
    const id = mockUuid('0195a000', hash(tenant.tenant_id + key))
    const cs = checks.map((c, i) => status(id, c, i, t, sinceMin))
    const svc: InfraService = {
      ...base,
      ...extra,
      id,
      name,
      kind,
      target,
      target_private: false,
      site_id: siteId,
      checks: checks.map((c, i) => ({ ...c.check, id: checkId(id, i) })),
      check_status: cs,
      status: { state: 'unknown', reason: null, check_type: null, since: null },
    }
    svc.target_private =
      /^(10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.|100\.(6[4-9]|[7-9]\d|1[01]\d|12[0-7])\.)/.test(
        target,
      )
    svc.status = aggregate(cs, new Date(t - sinceMin * 60_000).toISOString())
    return svc
  }
  return [
    make(
      'iptv',
      'IPTV cabecera',
      'iptv',
      '192.0.2.20',
      site(0),
      [
        { check: { type: 'icmp' }, state: 'up', reason: 'ok', latency: 3.2 },
        {
          check: { type: 'http', port: 8080, path: '/status', https: false },
          state: 'up',
          reason: 'ok',
          latency: 18,
          detail: 'HTTP 200',
        },
        {
          check: { type: 'ddos_inbound', pps_threshold: 80000 },
          state: 'up',
          reason: 'ok',
          detail: '1250 pps / 9400000 bps entrantes',
        },
      ],
      3 * 24 * 60,
    ),
    make(
      'dns1',
      'DNS recursivo 1',
      'dns',
      '192.0.2.53',
      site(0),
      [
        { check: { type: 'tcp', port: 53 }, state: 'up', reason: 'ok', latency: 1.4 },
        {
          check: { type: 'no_traffic', window_minutes: 15, min_bytes: 1000 },
          state: 'down',
          reason: 'no_traffic',
          detail: '0 bytes en 15 min',
        },
      ],
      12,
    ),
    make(
      'portal',
      'Portal web de clientes',
      'http',
      '198.51.100.10',
      site(1),
      [
        {
          check: { type: 'http', port: 443, path: '/', https: true, tls_verify: true },
          state: 'up',
          reason: 'ok',
          latency: 42,
          detail: 'HTTP 200',
        },
        {
          check: { type: 'ddos_inbound', pps_threshold: 20000, bps_threshold: 200000000 },
          state: 'degraded',
          reason: 'ddos_inbound',
          detail: '48210 pps / 512000000 bps entrantes',
        },
      ],
      7,
    ),
    make(
      'radius',
      'RADIUS PPPoE',
      'radius',
      '10.20.0.5',
      site(1),
      [{ check: { type: 'icmp' }, state: 'unknown', reason: 'pending' }],
      0,
    ),
  ]
}

const RANK: Record<InfraServiceState, number> = { down: 3, degraded: 2, up: 1, unknown: 0 }

function aggregate(cs: InfraCheckStatus[], since: string | null): InfraService['status'] {
  let worst: InfraCheckStatus | undefined
  for (const c of cs) if (!worst || RANK[c.state] > RANK[worst.state]) worst = c
  if (!worst || worst.state === 'unknown') {
    return { state: 'unknown', reason: worst?.reason ?? null, check_type: null, since: null }
  }
  if (worst.state === 'up') return { state: 'up', reason: 'ok', check_type: null, since }
  return { state: worst.state, reason: worst.reason, check_type: worst.type, since }
}

function servicesOf(tenant: MockTenant, now: Date) {
  let list = mockState.infraServices.get(tenant.tenant_id)
  if (!list) {
    list = seed(tenant, now)
    mockState.infraServices.set(tenant.tenant_id, list)
  }
  return list
}

/** Historial reciente simulado de un chequeo (el más reciente primero). */
export function recentResults(s: InfraService, c: InfraCheckStatus, now: Date): InfraCheckResult[] {
  if (!c.last_checked_at) return []
  const r = rng(hash(s.id + c.check_id))
  const out: InfraCheckResult[] = []
  const changedAt = c.since ? new Date(c.since).getTime() : 0
  for (let i = 0; i < 20; i++) {
    const at = now.getTime() - 20_000 - i * s.interval_seconds * 1000
    const before = at < changedAt
    const state: InfraServiceState = before && c.state !== 'up' ? 'up' : c.state
    const latency =
      c.latency_ms != null && state === 'up'
        ? Math.round(c.latency_ms * (0.8 + r() * 0.4) * 10) / 10
        : null
    out.push({
      at: new Date(at).toISOString(),
      state,
      latency_ms: latency,
      reason: state === 'up' ? 'ok' : c.reason,
    })
  }
  return out
}

function invalid(field: string, message: string) {
  return problem(
    422,
    'VALIDATION_FAILED',
    'Revisa los campos marcados',
    {},
    {
      errors: [{ field, code: 'INVALID', message }],
    },
  )
}

const IPV4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/

function validate(input: Partial<InfraService>) {
  if (!input.name?.trim()) return invalid('name', 'Escribe un nombre')
  const ip = String(input.target ?? '')
  const v4 = ip.match(IPV4)
  if (
    !(v4
      ? v4.slice(1).every((n) => Number(n) <= 255)
      : /^[0-9a-f:]+$/i.test(ip) && ip.includes(':'))
  ) {
    return invalid('target', 'IP del servidor no válida.')
  }
  if (!/^[a-z0-9][a-z0-9_-]{0,31}$/.test(String(input.kind ?? ''))) {
    return invalid(
      'kind',
      'Etiqueta de 1 a 32 caracteres: minúsculas, dígitos, guion o guion bajo.',
    )
  }
  if ((input.interval_seconds ?? 60) < 30)
    return invalid('interval_seconds', 'El intervalo debe estar entre 30 y 3600 s.')
  const checks = input.checks ?? []
  if (!checks.length) return invalid('checks', 'Entre 1 y 10 chequeos.')
  for (const [i, c] of checks.entries()) {
    if (c.type === 'tcp' && !c.port) return invalid(`checks[${i}].port`, 'Puerto obligatorio.')
    if (c.type === 'ddos_inbound' && c.pps_threshold == null && c.bps_threshold == null) {
      return invalid(`checks[${i}].pps_threshold`, 'Indique un umbral de pps o de bps.')
    }
    if (input.probe_via_router_id && (c.type === 'tcp' || c.type === 'http') && !c.forward_port) {
      return invalid(`checks[${i}].forward_port`, 'Puerto obligatorio.')
    }
  }
  return null
}

function isPrivate(ip: string) {
  return (
    /^(10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.|100\.(6[4-9]|[7-9]\d|1[01]\d|12[0-7])\.)/.test(
      ip,
    ) || /^f[cd]/i.test(ip)
  )
}

function withStatus(s: InfraService): InfraService {
  // Chequeos nuevos o editados: estado inicial "pendiente".
  const byId = new Map(s.check_status.map((c) => [c.check_id, c]))
  s.checks = s.checks.map((c, i) => ({ ...c, id: c.id ?? checkId(s.id + s.version, i) }))
  s.check_status = s.checks.map(
    (c) =>
      byId.get(c.id!) ?? {
        check_id: c.id!,
        type: c.type,
        state: 'unknown',
        reason: 'pending',
        since: null,
        last_checked_at: null,
        latency_ms: null,
        consecutive_failures: 0,
        consecutive_successes: 0,
        detail: null,
      },
  )
  s.status = aggregate(s.check_status, s.status.since)
  s.target_private = isPrivate(s.target)
  return s
}

function snippet(
  s: InfraService,
  router: { id: string; name: string; tunnel_address?: string | null },
): InfraRouterSnippet {
  const warnings: string[] = []
  const horusSide =
    'Del lado de Horus no hay que cambiar nada: los chequeos TCP/HTTP de este servicio van a la IP de túnel del router ' +
    '(su /32, que ya está en las AllowedIPs de su peer en el hub WireGuard) en el puerto de reenvío de cada chequeo, y el ' +
    'router los reenvía al servidor. El ping (ICMP) a un servidor privado no se puede reenviar: use un chequeo TCP o HTTP.'
  if (!s.target_private)
    warnings.push(
      'El servidor tiene IP pública: Horus lo sondea directamente, no hace falta el script.',
    )
  if (!router.tunnel_address) {
    warnings.push('El router todavía no tiene túnel con Horus: complete su alta primero.')
    return {
      router_id: router.id,
      router_name: router.name,
      applicable: false,
      script: '',
      horus_side: horusSide,
      warnings,
    }
  }
  const short = s.id.slice(0, 8)
  const rules: string[] = []
  for (const c of s.checks) {
    if (c.type === 'icmp') {
      warnings.push('El chequeo ICMP no se puede reenviar por el router: quedará en «desconocido».')
      continue
    }
    if (c.type !== 'tcp' && c.type !== 'http') continue
    const port = c.port ?? (c.type === 'http' ? (c.https ? 443 : 80) : 0)
    if (!c.forward_port) {
      warnings.push(
        `El chequeo ${c.type} al puerto ${port} no tiene puerto de reenvío: no se genera regla.`,
      )
      continue
    }
    rules.push(
      `/ip firewall nat add chain=dstnat in-interface=wg-horus dst-address=${router.tunnel_address} protocol=tcp dst-port=${c.forward_port} \\\n` +
        `    src-address=${RED_SERVICIOS} action=dst-nat to-addresses=${s.target} to-ports=${port} comment="horus-infra ${short}"\n` +
        `/ip firewall nat add chain=srcnat src-address=${RED_SERVICIOS} dst-address=${s.target} protocol=tcp dst-port=${port} \\\n` +
        `    action=masquerade comment="horus-infra ${short}"\n` +
        `/ip firewall filter add chain=forward in-interface=wg-horus src-address=${RED_SERVICIOS} dst-address=${s.target} protocol=tcp dst-port=${port} \\\n` +
        `    action=accept place-before=0 comment="horus-infra ${short}"`,
    )
  }
  if (!rules.length) {
    return {
      router_id: router.id,
      router_name: router.name,
      applicable: false,
      script: '',
      horus_side: horusSide,
      warnings,
    }
  }
  const script =
    '# ===== Horus Flow · acceso de sondeo a un servicio de infraestructura · RouterOS v7 =====\n' +
    `# Servicio: ${s.name} · router: ${router.name}\n` +
    '# Revise el script antes de pegarlo en el router. Horus nunca escribe en el router.\n' +
    `# Se deshace con: /ip firewall nat remove [find comment~"^horus-infra ${short}"]\n\n` +
    rules.join('\n\n') +
    '\n'
  return {
    router_id: router.id,
    router_name: router.name,
    applicable: true,
    script,
    horus_side: horusSide,
    warnings,
  }
}

export function infraServiceRoutes(ctx: TenantRouteContext): Response | undefined {
  if (!ctx.path.startsWith('/infra-services')) return undefined
  if (!ctx.can('snmp.read')) return forbidden()
  const list = servicesOf(ctx.tenant, ctx.now)
  const write = () => ctx.can('snmp.manage')

  if (ctx.path === '/infra-services') {
    if (ctx.method === 'GET') {
      const site = ctx.url.searchParams.get('site_id')
      const data = ctx.empty ? [] : list.filter((s) => !site || s.site_id === site)
      const disabled = ctx.scenario === 'snmp-off'
      return json({
        data,
        meta: {
          scheduler: {
            enabled: !disabled,
            healthy: !disabled,
            last_cycle_at: disabled ? null : new Date(ctx.now.getTime() - 4000).toISOString(),
            passive_checks: true,
          },
          generated_at: ctx.now.toISOString(),
        },
      })
    }
    if (ctx.method === 'POST') {
      if (!write()) return forbidden()
      const input = ctx.body as Partial<InfraService>
      const err = validate(input)
      if (err) return err
      if (list.some((s) => s.name.toLowerCase() === String(input.name).trim().toLowerCase())) {
        return problem(409, 'CONFLICT', 'Ya existe un servicio con ese nombre.')
      }
      const now = ctx.now.toISOString()
      const svc = withStatus({
        id: mockUuid('0195a200', hash(String(input.name) + now)),
        tenant_id: ctx.tenant.tenant_id,
        created_at: now,
        updated_at: now,
        version: 1,
        name: String(input.name).trim(),
        site_id: input.site_id ?? null,
        target: String(input.target),
        target_private: false,
        kind: String(input.kind),
        checks: input.checks ?? [],
        interval_seconds: input.interval_seconds ?? 60,
        failures_before_down: input.failures_before_down ?? 3,
        recoveries_before_up: input.recoveries_before_up ?? 2,
        enabled: input.enabled ?? true,
        probe_via_router_id: input.probe_via_router_id ?? null,
        status: { state: 'unknown', reason: null, check_type: null, since: null },
        check_status: [],
      })
      list.push(svc)
      return json(svc, 201, { ETag: '"1"', Location: `/api/v1/infra-services/${svc.id}` })
    }
    return undefined
  }

  const m = ctx.path.match(/^\/infra-services\/([^/]+)(\/router-snippet)?$/)
  const svc = m && list.find((s) => s.id === m[1])
  if (!m || !svc) return notFound()
  if (m[2]) {
    if (ctx.method !== 'GET') return undefined
    if (!write()) return forbidden()
    const rid = ctx.url.searchParams.get('router_id') ?? svc.probe_via_router_id
    if (!rid)
      return invalid(
        'router_id',
        'Indique el router del nodo (o fije «sondear por el router» en el servicio).',
      )
    const r = routersOf(ctx.tenant, ctx.now).find((x) => x.router.id === rid)
    if (!r) return notFound()
    return json(snippet(svc, r.router))
  }
  if (ctx.method === 'GET') {
    return json(
      {
        ...svc,
        check_status: svc.check_status.map((c) => ({
          ...c,
          recent: recentResults(svc, c, ctx.now),
        })),
      },
      200,
      { ETag: `"${svc.version}"` },
    )
  }
  if (ctx.method !== 'PATCH' && ctx.method !== 'DELETE') return undefined
  if (!write()) return forbidden()
  const v = ifMatch(ctx.req)
  if (v == null)
    return problem(
      428,
      'PRECONDITION_REQUIRED',
      'Falta la cabecera If-Match con el ETag del recurso.',
    )
  if (v !== svc.version) {
    return problem(
      412,
      'PRECONDITION_FAILED',
      'El servicio cambió',
      {},
      {
        current: svc as unknown as Record<string, unknown>,
      },
    )
  }
  if (ctx.method === 'DELETE') {
    list.splice(list.indexOf(svc), 1)
    return noContent()
  }
  const merged = { ...svc, ...(ctx.body as Partial<InfraService>) }
  const err = validate(merged)
  if (err) return err
  Object.assign(svc, ctx.body, { version: svc.version + 1, updated_at: ctx.now.toISOString() })
  withStatus(svc)
  return json(svc, 200, { ETag: `"${svc.version}"` })
}
