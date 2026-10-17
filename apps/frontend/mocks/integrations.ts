import type {
  ApiToken,
  ApiTokenIssued,
  ApiTokenTemplate,
  ArgosIntegration,
  CustomerDetail,
  CustomerExternalIdentity,
  ExternalIdentityPeriod,
  IpLookupResponse,
} from '~~/types/api'
import type { MockTenant } from './data'
import { forbidden, json, noContent, notFound, problem, randomHex } from './http'
import { customersOf, sitesOf } from './inventory'
import { contains, parseCidr, parseIp } from './net'
import { hash, mockUuid } from './random'
import type { TenantRouteContext } from './tenant-routes'

/**
 * Integraciones de ecosistema (ADR-0030) en la API simulada: API tokens `hf_pat_` con la
 * plantilla ArgosISP, conector ArgosISP (token write-only, secreto del webhook mostrado una
 * vez), identidad externa con vigencia por cliente, consulta 360 por IP y consumo por lotes.
 * El estado vive en memoria por ISP y se guarda en `localStorage` para sobrevivir a la
 * navegación con recarga de las pruebas e2e.
 */

interface TenantState {
  tokens: ApiToken[]
  argos: ArgosIntegration
  identities: Record<string, ExternalIdentityPeriod[]>
}

const STORAGE_KEY = 'horus.mock.integrations'
const states = new Map<string, TenantState>()

function load(): Record<string, TenantState> {
  try {
    return JSON.parse(globalThis.localStorage?.getItem(STORAGE_KEY) ?? '{}') as Record<
      string,
      TenantState
    >
  } catch {
    return {}
  }
}

function persist() {
  try {
    globalThis.localStorage?.setItem(STORAGE_KEY, JSON.stringify(Object.fromEntries(states)))
  } catch {
    // Sin almacenamiento (pestaña privada): queda en memoria.
  }
}

/** Solo tests: vacía el estado simulado. */
export function resetIntegrationsMock() {
  states.clear()
  try {
    globalThis.localStorage?.removeItem(STORAGE_KEY)
  } catch {
    // sin almacenamiento
  }
}

const ARGOS_TEMPLATE: ApiTokenTemplate = {
  key: 'argosisp',
  name: 'ArgosISP',
  description:
    'Código de conexión para ArgosISP: clientes, consumo, seguridad, salud de la red y autologin hacia Horus.',
  permissions: [
    'customers.read',
    'security.findings.read',
    'snmp.read',
    'sso.issue',
    'traffic.customer.read',
  ],
}

function state(tenant: MockTenant): TenantState {
  let s = states.get(tenant.tenant_id) ?? load()[tenant.tenant_id]
  if (s) {
    s.argos.connection ??= null // estado guardado antes del enlace de conexión (H10)
    states.set(tenant.tenant_id, s)
  }
  if (!s) {
    s = {
      tokens: [
        {
          id: mockUuid('0195a000', hash(tenant.tenant_id + 'tok')),
          name: 'ArgosISP producción',
          prefix: 'hf_pat_7Qa2',
          user_id: '01926b3e-1111-7000-8000-000000000001',
          permissions: ARGOS_TEMPLATE.permissions,
          template: 'argosisp',
          created_at: '2026-09-20T15:00:00.000Z',
          expires_at: '2027-09-20T15:00:00.000Z',
          last_used_at: '2026-10-17T09:58:00.000Z',
          last_used_ip: '203.0.113.40',
          revoked_at: null,
          active: true,
        },
      ],
      argos: {
        id: null,
        configured: false,
        enabled: false,
        base_url: null,
        has_token: false,
        webhook_url: null,
        has_webhook_secret: false,
        last_sync_at: null,
        last_sync_status: null,
        last_error: null,
        resolver: null,
        identities: 0,
        stale_identities: 0,
        version: 0,
        connection: null,
      },
      identities: {},
    }
    states.set(tenant.tenant_id, s)
  }
  return s
}

const NAMES = ['Ferretería López', 'Ana Pérez', 'Café del Centro', 'Clínica San José', 'Luis Gómez']
/** Empresas del ISP en ArgosISP (multiempresa, contrato 1.2.0). */
const COMPANIES = [
  { id: 2, name: 'Fibra Sur', nit: '1234567K' },
  { id: 3, name: 'Fibra Norte', nit: '7654321' },
]

/** Identidad de ArgosISP simulada de un cliente (determinista por id). */
function resolveArgos(c: CustomerDetail, now: Date): ExternalIdentityPeriod {
  const h = hash(c.id)
  const service = 800 + (h % 200)
  return {
    id: mockUuid('0195b000', h),
    system: 'argosisp',
    external_customer_id: 400 + (h % 100),
    external_service_id: service,
    display_name: NAMES[h % NAMES.length]!,
    code: `C-${String(400 + (h % 100)).padStart(6, '0')}`,
    service_state: h % 7 === 0 ? 'suspendido' : 'activo',
    plan: h % 2 ? 'Fibra 100' : 'Fibra 300',
    external_company_id: COMPANIES[h % COMPANIES.length]?.id ?? null,
    company_name: COMPANIES[h % COMPANIES.length]?.name ?? null,
    company_nit: COMPANIES[h % COMPANIES.length]?.nit ?? null,
    valid_from: new Date(now.getTime() - 3 * 86_400_000).toISOString(),
    valid_to: null,
    synced_at: now.toISOString(),
    stale: false,
  }
}

function identityView(s: TenantState, c: CustomerDetail): CustomerExternalIdentity {
  const history = s.identities[c.id] ?? []
  const cur = history.find((p) => p.valid_to === null) ?? null
  return {
    customer_id: c.id,
    connector_enabled: s.argos.enabled && s.argos.has_token,
    current: cur && {
      system: 'argosisp',
      customer_id: cur.external_customer_id,
      service_id: cur.external_service_id,
      code: cur.code,
      display_name: cur.display_name,
      service_state: cur.service_state,
      plan: cur.plan,
      // Estados guardados antes de 1.2.0 no tienen empresa.
      company_id: cur.external_company_id ?? null,
      company_name: cur.company_name ?? null,
      synced_at: cur.synced_at,
      stale: cur.stale,
    },
    history,
    last_checked_at: cur?.synced_at ?? null,
    last_check_status: cur ? (cur.stale ? 'error' : 'found') : null,
  }
}

function refreshCounts(s: TenantState) {
  let cur = 0
  let stale = 0
  for (const periods of Object.values(s.identities)) {
    const p = periods.find((x) => x.valid_to === null)
    if (p) {
      cur++
      if (p.stale) stale++
    }
  }
  s.argos.identities = cur
  s.argos.stale_identities = stale
}

function bytes(seed: number, scale: number) {
  return Math.round(((seed % 97) + 3) * scale)
}

export function integrationsRoute(ctx: TenantRouteContext): Response | undefined {
  const { path, method, tenant, now, body } = ctx
  const isTokens = path.startsWith('/api-token')
  const isIntegration = path.startsWith('/integrations/')
  const identityMatch = path.match(/^\/customers\/([^/]+)\/external-identity(\/refresh)?$/)
  if (!isTokens && !isIntegration && !identityMatch) return undefined
  const s = state(tenant)

  // --- API tokens ---------------------------------------------------------------------------
  if (isTokens) {
    if (!ctx.can('api_tokens.manage')) return forbidden()
    if (method === 'GET' && path === '/api-token-templates') return json({ data: [ARGOS_TEMPLATE] })
    if (method === 'GET' && path === '/api-tokens') return json({ data: s.tokens })
    if (method === 'POST' && path === '/api-tokens') {
      if (ctx.reauthRequired)
        return problem(403, 'REAUTH_REQUIRED', 'Confirma tu identidad para continuar')
      const name = String(body.name ?? '').trim()
      const perms = (body.permissions as string[] | undefined)?.length
        ? (body.permissions as string[])
        : body.template === 'argosisp'
          ? ARGOS_TEMPLATE.permissions
          : []
      if (!name || !perms.length) {
        return problem(
          422,
          'VALIDATION_FAILED',
          'Datos no válidos',
          {},
          {
            errors: [
              !name
                ? { field: 'name', code: 'INVALID_VALUE', message: 'Entre 1 y 80 caracteres.' }
                : {
                    field: 'permissions',
                    code: 'REQUIRED',
                    message: 'Indique una plantilla o permisos.',
                  },
            ],
          },
        )
      }
      const raw = `hf_pat_${randomHex(42)}X`.slice(0, 50)
      const expires = body.expires_at
        ? String(body.expires_at)
        : new Date(now.getTime() + 365 * 86_400_000).toISOString()
      const tok: ApiToken = {
        id: mockUuid('0195a000', hash(raw)),
        name,
        prefix: raw.slice(0, 11),
        user_id: ctx.userId,
        permissions: [...perms].sort(),
        template: body.template === 'argosisp' ? 'argosisp' : null,
        created_at: now.toISOString(),
        expires_at: expires,
        last_used_at: null,
        last_used_ip: null,
        revoked_at: null,
        active: true,
      }
      s.tokens = [tok, ...s.tokens]
      persist()
      const issued: ApiTokenIssued = { ...tok, token: raw }
      return json(issued, 201, { 'Cache-Control': 'no-store' })
    }
    const del = path.match(/^\/api-tokens\/([^/]+)$/)
    if (method === 'DELETE' && del) {
      const tok = s.tokens.find((t) => t.id === del[1] && !t.revoked_at)
      if (!tok) return notFound('API_TOKEN_NOT_FOUND')
      tok.revoked_at = now.toISOString()
      tok.active = false
      persist()
      return noContent()
    }
    return undefined
  }

  // --- Identidad externa de un cliente ------------------------------------------------------
  if (identityMatch) {
    const c = customersOf(tenant, now).find((x) => x.id === identityMatch[1])
    if (!c) return notFound('CUSTOMER_NOT_FOUND')
    if (!identityMatch[2]) {
      if (method !== 'GET') return undefined
      return ctx.can('customers.read') ? json(identityView(s, c)) : forbidden()
    }
    if (method !== 'POST') return undefined
    if (!ctx.can('customers.update')) return forbidden()
    if (!s.argos.enabled || !s.argos.has_token) {
      return problem(409, 'INTEGRATION_NOT_CONFIGURED', 'El conector ArgosISP no está activo.')
    }
    const periods = s.identities[c.id] ?? []
    const cur = periods.find((p) => p.valid_to === null)
    if (s.argos.base_url?.includes('caido')) {
      if (cur) cur.stale = true
      s.argos.last_sync_status = 'failing'
      s.argos.last_error = 'ArgosISP no responde.'
    } else if (cur) {
      cur.stale = false
      cur.synced_at = now.toISOString()
    } else {
      periods.unshift(resolveArgos(c, now))
      s.argos.last_sync_status = 'degraded'
      s.argos.resolver = 'fallback'
      s.argos.last_error = null
    }
    s.argos.last_sync_at = now.toISOString()
    s.identities[c.id] = periods
    refreshCounts(s)
    persist()
    return json(identityView(s, c))
  }

  // --- Conector ArgosISP ---------------------------------------------------------------------
  if (path === '/integrations/argosisp') {
    if (method === 'GET') return ctx.can('settings.read') ? json(s.argos) : forbidden()
    if (method === 'PUT') {
      if (!ctx.can('settings.manage')) return forbidden()
      if (ctx.reauthRequired)
        return problem(403, 'REAUTH_REQUIRED', 'Confirma tu identidad para continuar')
      const url = String(body.base_url ?? '').trim()
      if (!/^https:\/\/[^/@\s]+/.test(url)) {
        return problem(
          422,
          'VALIDATION_FAILED',
          'Datos no válidos',
          {},
          {
            errors: [
              {
                field: 'base_url',
                code: 'INVALID_FORMAT',
                message: 'URL https de ArgosISP (con /api/v1), sin credenciales.',
              },
            ],
          },
        )
      }
      const id = s.argos.id ?? mockUuid('0195c000', hash(tenant.tenant_id + 'argos'))
      s.argos = {
        ...s.argos,
        id,
        configured: true,
        enabled: Boolean(body.enabled),
        base_url: url.replace(/\/+$/, ''),
        has_token: s.argos.has_token || Boolean(body.token),
        has_webhook_secret: true,
        webhook_url: `https://horus.example.net/api/v1/integrations/argosisp/webhook/${id}`,
        version: s.argos.version + 1,
      }
      persist()
      return json(s.argos)
    }
    return undefined
  }
  if (method === 'POST' && path === '/integrations/argosisp/webhook-secret') {
    if (!ctx.can('settings.manage')) return forbidden()
    if (!s.argos.configured) {
      return problem(409, 'INTEGRATION_NOT_CONFIGURED', 'Configure primero el conector ArgosISP.')
    }
    return json(
      { webhook_url: s.argos.webhook_url, webhook_secret: `whsec_${randomHex(40)}` },
      200,
      { 'Cache-Control': 'no-store' },
    )
  }
  if (method === 'POST' && path === '/integrations/argosisp/connection-test') {
    if (!ctx.can('settings.manage')) return forbidden()
    if (!s.argos.has_token) {
      return problem(409, 'INTEGRATION_NOT_CONFIGURED', 'Configure la URL y el token de ArgosISP.')
    }
    const down = s.argos.base_url?.includes('caido')
    return json({
      ok: !down,
      tested_at: now.toISOString(),
      latency_ms: down ? 10_000 : 84,
      resolver: down ? null : 'fallback',
      error_code: down ? 'connect_failed' : null,
      error: down ? 'No se pudo conectar con ArgosISP.' : null,
    })
  }

  // --- Consulta 360 y consumo ----------------------------------------------------------------
  if (method === 'POST' && path === '/integrations/ip-lookup') {
    if (!ctx.can('customers.read')) return forbidden()
    const invalid = (field: string, code: string, message: string) =>
      problem(
        422,
        'VALIDATION_FAILED',
        'Datos no válidos',
        {},
        { errors: [{ field, code, message }] },
      )
    // address o, desde 1.2.0, external_service_id (exactamente uno de los dos).
    if ((body.address == null) === (body.external_service_id == null)) {
      return invalid(
        'address',
        'INVALID_VALUE',
        'Indique address o external_service_id (uno de los dos).',
      )
    }
    let c: CustomerDetail | undefined
    if (body.external_service_id != null) {
      const svc = Number(body.external_service_id)
      if (!Number.isInteger(svc) || svc < 1)
        return invalid('external_service_id', 'OUT_OF_RANGE', 'Id positivo.')
      c = customersOf(tenant, now).find((x) =>
        (s.identities[x.id] ?? []).some(
          (p) => p.valid_to === null && p.external_service_id === svc,
        ),
      )
    } else {
      const target = parseCidr(String(body.address ?? ''))
      if (!target || target.length !== (target.family === 4 ? 32 : 128)) {
        return invalid('address', 'INVALID_FORMAT', 'IP no válida.')
      }
      c = customersOf(tenant, now).find((x) => {
        const ip = parseIp(x.address)
        if (!ip) return false
        const own = { family: ip.family, network: ip.value, length: ip.family === 6 ? 56 : 32 }
        // IPv6: una IP dentro del prefijo delegado encuentra al cliente (D22).
        return contains(own, target)
      })
    }
    if (!c) return notFound('CUSTOMER_NOT_FOUND')
    const site = sitesOf(tenant).find((x) => x.id === c.site_id)
    const view = identityView(s, c)
    const h = hash(c.id)
    const omitted: IpLookupResponse['meta']['omitted'] = []
    const res: IpLookupResponse = {
      customer: {
        id: c.id,
        address: c.address,
        alias: c.alias,
        alias_source: c.alias_source,
        kind: c.kind,
        kind_source: c.kind_source,
        status: c.status,
        first_seen: c.first_seen,
        last_seen: c.last_seen,
        site: { id: c.site_id, name: site?.name ?? null },
      },
      external: view.current,
      consumption: ctx.can('traffic.customer.read')
        ? {
            last_24h: { up_bytes: bytes(h, 9e7), down_bytes: bytes(h, 8e8) },
            last_7d: { up_bytes: bytes(h, 6e8), down_bytes: bytes(h, 5.6e9) },
            last_30d: { up_bytes: bytes(h, 2.6e9), down_bytes: bytes(h, 2.4e10) },
            peak_down_bps_24h: bytes(h, 9e5),
            top_services_7d: [
              { name: 'YouTube', bytes: bytes(h, 2e9) },
              { name: 'Netflix', bytes: bytes(h + 3, 1.4e9) },
              { name: 'WhatsApp', bytes: bytes(h + 7, 2e8) },
            ],
          }
        : null,
      health: ctx.can('snmp.read')
        ? {
            router: {
              id: mockUuid('0192e333', h % 3),
              name: `rt-${(site?.name ?? 'nodo').toLowerCase().replace(/\s+/g, '-')}`,
              status: 'online',
              since: new Date(now.getTime() - 6 * 3600_000).toISOString(),
            },
            exporter: {
              status: 'exporting',
              last_flow_at: new Date(now.getTime() - 40_000).toISOString(),
            },
            last_seen_minutes: Math.max(
              0,
              Math.round((now.getTime() - Date.parse(c.last_seen)) / 60_000),
            ),
          }
        : null,
      security: ctx.can('security.findings.read')
        ? { state: c.security_state, open_findings: c.open_findings, findings: [] }
        : null,
      meta: { at: now.toISOString(), omitted },
    }
    if (!res.consumption) omitted.push({ section: 'consumption', reason: 'permission' })
    if (!res.health) omitted.push({ section: 'health', reason: 'permission' })
    if (!res.security) omitted.push({ section: 'security', reason: 'permission' })
    if (!s.argos.configured && !res.external)
      omitted.push({ section: 'external', reason: 'not_configured' })
    return json(res, 200, { 'Cache-Control': 'no-store' })
  }
  if (method === 'POST' && path === '/integrations/security-states') {
    if (!ctx.can('security.findings.read')) return forbidden()
    const items = (body.items as { address?: string; external_service_id?: number }[]) ?? []
    if (items.length < 1 || items.length > 500) {
      return problem(
        422,
        'VALIDATION_FAILED',
        'Datos no válidos',
        {},
        { errors: [{ field: 'items', code: 'OUT_OF_RANGE', message: 'Entre 1 y 500 elementos.' }] },
      )
    }
    const all = customersOf(tenant, now)
    return json(
      {
        items: items.map((it) => {
          const c =
            it.external_service_id != null
              ? all.find((x) =>
                  (s.identities[x.id] ?? []).some(
                    (p) => p.valid_to === null && p.external_service_id === it.external_service_id,
                  ),
                )
              : all.find((x) => x.address === it.address)
          if (!c) {
            return {
              address: it.address ?? null,
              external_service_id: it.external_service_id ?? null,
              error: 'not_found' as const,
            }
          }
          const cur = (s.identities[c.id] ?? []).find((p) => p.valid_to === null)
          return {
            address: it.address ?? c.address,
            external_service_id: it.external_service_id ?? cur?.external_service_id ?? null,
            state: c.security_state,
            open_findings: c.open_findings,
            max_severity: c.open_findings ? 'high' : null,
            since: c.first_seen,
          }
        }),
        meta: { at: now.toISOString() },
      },
      200,
      { 'Cache-Control': 'no-store' },
    )
  }
  if (method === 'POST' && path === '/integrations/consumption') {
    if (!ctx.can('traffic.customer.read')) return forbidden()
    const items = (body.items as { address?: string; external_service_id?: number }[]) ?? []
    return json({
      items: items.map((it, i) => ({
        address: it.address ?? null,
        external_service_id: it.external_service_id ?? null,
        up_bytes: bytes(i + 11, 1e8),
        down_bytes: bytes(i + 11, 1e9),
        series: [],
      })),
      meta: { from: body.from, to: body.to, granularity: body.granularity ?? 'total' },
    })
  }

  // --- Enlace de conexión (§1.4, H10) y autologin (§1.5, H11) --------------------------------
  const canLink =
    ctx.can('settings.manage') && ctx.can('api_tokens.manage') && ctx.can('alerts.manage')
  if (method === 'POST' && path === '/integrations/argosisp/connection-links') {
    if (!canLink) return forbidden()
    if (ctx.reauthRequired)
      return problem(403, 'REAUTH_REQUIRED', 'Confirma tu identidad para continuar')
    const code = `${randomHex(42)}Q`.slice(0, 43)
    return json(
      {
        url: `https://horus.example.net/conectar#${code}`,
        expires_at: new Date(now.getTime() + 15 * 60_000).toISOString(),
        ttl_seconds: 900,
      },
      201,
      { 'Cache-Control': 'no-store' },
    )
  }
  if (method === 'POST' && path === '/integrations/argosisp/connection-links/redeem') {
    if (!canLink) return forbidden()
    if (ctx.reauthRequired)
      return problem(403, 'REAUTH_REQUIRED', 'Confirma tu identidad para continuar')
    const link = String(body.link ?? '').trim()
    const m = link.match(/^https:\/\/([^/@\s?#]+)\/conectar(?:\?api=([^#]+))?#[A-Za-z0-9_-]{43}$/)
    if (!m) {
      return problem(
        422,
        'VALIDATION_FAILED',
        'Datos no válidos',
        {},
        {
          errors: [
            {
              field: 'link',
              code: 'INVALID_FORMAT',
              message: 'Pegue el enlace completo de ArgosISP: https://…/conectar#…',
            },
          ],
        },
      )
    }
    if (m[1]!.includes('vencido')) {
      return problem(
        422,
        'CONNECTION_LINK_EXPIRED',
        'ArgosISP dice que el enlace venció o ya se usó. Genere otro en ArgosISP.',
      )
    }
    const origin = m[2] ? decodeURIComponent(m[2]).replace(/\/+$/, '') : `https://${m[1]}`
    const id = s.argos.id ?? mockUuid('0195c000', hash(tenant.tenant_id + 'argos'))
    const tokenId = mockUuid('0195a000', hash(link))
    s.tokens = [
      {
        id: tokenId,
        name: 'ArgosISP (enlace de conexión)',
        prefix: 'hf_pat_Lk9x',
        user_id: ctx.userId,
        permissions: ARGOS_TEMPLATE.permissions,
        template: 'argosisp',
        created_at: now.toISOString(),
        expires_at: new Date(now.getTime() + 365 * 86_400_000).toISOString(),
        last_used_at: null,
        last_used_ip: null,
        revoked_at: null,
        active: true,
      },
      ...s.tokens,
    ]
    s.argos = {
      ...s.argos,
      id,
      configured: true,
      enabled: false,
      base_url: `${origin}/api/v1`,
      has_token: true,
      has_webhook_secret: true,
      webhook_url: `https://horus.example.net/api/v1/integrations/argosisp/webhook/${id}`,
      version: s.argos.version + 1,
      connection: {
        linked_at: now.toISOString(),
        method: 'link_redeemed',
        peer_connector_id: '7',
        api_token_id: tokenId,
        notification_channel_id: mockUuid('0195e000', hash(tenant.tenant_id + 'argos-webhook')),
      },
    }
    persist()
    return json(s.argos)
  }
  if (method === 'POST' && path === '/integrations/argosisp/sso/open') {
    if (!ctx.can('customers.read')) return forbidden()
    const c = customersOf(tenant, now).find((x) => x.id === body.customer_id)
    if (!c) return notFound('CUSTOMER_NOT_FOUND')
    if (!s.argos.enabled || !s.argos.has_token) {
      return problem(409, 'INTEGRATION_NOT_CONFIGURED', 'El conector ArgosISP no está activo.')
    }
    const cur = (s.identities[c.id] ?? []).find((p) => p.valid_to === null)
    if (!cur?.external_service_id) return notFound('EXTERNAL_IDENTITY_NOT_FOUND')
    return json(
      {
        url: `https://panel.argos.example/sso/entrar#${randomHex(43)}`,
        expires_at: new Date(now.getTime() + 60_000).toISOString(),
      },
      200,
      { 'Cache-Control': 'no-store' },
    )
  }
  if (method === 'POST' && path === '/integrations/argosisp/sso/resolve') {
    const type = String(body.type ?? '')
    const ext = String(body.external_id ?? '')
    if (type === 'servicio' || type === 'cliente') {
      if (!ctx.can('customers.read')) return forbidden()
      const all = customersOf(tenant, now)
      // Demo: el servicio de una identidad vigente o, si no hay, el primer cliente del ISP.
      const byIdentity = all.find((x) =>
        (s.identities[x.id] ?? []).some(
          (p) =>
            p.valid_to === null &&
            String(type === 'servicio' ? p.external_service_id : p.external_customer_id) === ext,
        ),
      )
      const c = byIdentity ?? all[0]
      return c ? json({ kind: 'customer', id: c.id }) : notFound('CUSTOMER_NOT_FOUND')
    }
    if (type === 'router') {
      if (!ctx.can('devices.read')) return forbidden()
      return json({ kind: 'router', id: ext })
    }
    if (type === 'hallazgo') {
      if (!ctx.can('security.findings.read')) return forbidden()
      return json({ kind: 'finding', id: ext })
    }
    return problem(422, 'VALIDATION_FAILED', 'Datos no válidos')
  }
  return undefined
}
