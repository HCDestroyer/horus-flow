import type {
  AccessTokenResponse,
  Dashboard,
  DashboardSummary,
  SystemStatus,
  WidgetType,
} from '~~/types/api'
import nocTemplate from '~~/types/api/contract/templates/noc-isp.json'
import securityTemplate from '~~/types/api/contract/templates/security.json'
import { WIDGET_CATALOG } from '~~/types/api/widget-catalog'
import { channelRoute } from './channels'
import { findTenant, MOCK_TOTP_CODE, USERS, type MockUser } from './data'
import { json, problem, randomHex } from './http'
import { currentKiosk, enroll, kioskAdminRoute, kioskConfig, touchKiosk } from './kiosk'
import { platformRoute } from './platform'
import { tenantRoute } from './tenant-routes'
import { widgetData } from './widget-data'

/**
 * API simulada en memoria (I0-15): un `fetch` que responde como el gateway según el contrato
 * v0 (C5/C9). La usa el cliente `openapi-fetch` igual que el `fetch` real, así que la app no
 * distingue un modo del otro (`NUXT_PUBLIC_API_MOCK` / `HORUS_UI_MOCKS`).
 *
 * - Respuestas tipadas con los tipos generados del OpenAPI; plantillas, catálogo de widgets y
 *   ejemplos de hallazgos copiados del contrato por `pnpm api:generate`.
 * - Tokens por ámbito como el gateway (api.md §0.2): sesión → `/me`, `/auth/*`; ISP →
 *   rutas de negocio (`403 TOKEN_SCOPE_INVALID` sin él); otro ISP → `404 TENANT_NOT_FOUND`.
 * - La cookie HttpOnly de refresh no se puede simular desde JS: la "sesión" vive en
 *   `sessionStorage` SOLO en este archivo. El código de la app nunca persiste tokens.
 */

export type MockScenario = 'normal' | 'degraded' | 'system-error' | 'widget-error' | 'empty'

export interface MockOptions {
  /** Latencia simulada en ms (para ver los estados de carga). */
  latencyMs?: number
  /** Escenario; por defecto se lee de `localStorage['horus.mock.scenario']`. */
  scenario?: () => MockScenario
  storage?: Pick<Storage, 'getItem' | 'setItem' | 'removeItem'> | null
  now?: () => Date
  /** Vida del access token (10 min en el contrato). */
  tokenTtlMs?: number
}

const SESSION_KEY = 'horus.mock.session'
const SCENARIO_KEY = 'horus.mock.scenario'
const SCENARIOS: MockScenario[] = ['normal', 'degraded', 'system-error', 'widget-error', 'empty']
const MAX_FAILURES = 5
const LOCK_SECONDS = 60
const BASE_PATH = '/api/v1'

/** Plantillas de sistema del contrato (C9). */
export const TEMPLATES = [nocTemplate, securityTemplate] as Dashboard[]

/** Widgets que fallan una vez en el escenario `widget-error` (para probar "Reintentar"). */
const FAILING_ONCE = new Set(['w-traffic-24h', 'w-findings-trend'])
/** Tipos que dependen de la analítica (ClickHouse): degradados en el escenario `degraded`. */
const ANALYTICS_TYPES = new Set([
  'traffic_timeseries',
  'top_categories',
  'top_services',
  'top_organizations',
  'top_customers',
  'findings_trend',
])

interface TokenRecord {
  userId: string
  scope: AccessTokenResponse['scope']
  tenantId?: string
  /** Solo `scope=kiosk`: el kiosco (dispositivo) del token. */
  kioskId?: string
  expires: number
}

function safeStorage(): MockOptions['storage'] {
  try {
    return typeof window === 'undefined' ? null : window.sessionStorage
  } catch {
    return null
  }
}

export function readScenario(): MockScenario {
  try {
    const value = window.localStorage.getItem(SCENARIO_KEY) as MockScenario | null
    if (value && SCENARIOS.includes(value)) return value
  } catch {
    // sin storage: escenario normal
  }
  return 'normal'
}

function readNumber(key: string): number | undefined {
  try {
    const value = Number(window.localStorage.getItem(key))
    return Number.isFinite(value) && value > 0 ? value : undefined
  } catch {
    return undefined
  }
}

/** Vida del access token para pruebas: `localStorage['horus.mock.tokenTtlMs']`. */
const readTokenTtl = () => readNumber('horus.mock.tokenTtlMs')

function hasPermission(user: MockUser, tenantId: string | undefined, permission: string) {
  const m = user.me.memberships.find((x) => x.tenant_id === tenantId)
  return !!m?.permissions_with_scope[permission]?.length
}

function summary(d: Dashboard): DashboardSummary {
  return {
    id: d.id,
    tenant_id: d.tenant_id,
    version: d.version,
    name: d.name,
    visibility: d.visibility,
    owner_id: d.owner_id,
    template_key: d.template_key ?? null,
    widget_count: d.widgets.length,
    updated_at: d.updated_at,
  }
}

export function createMockFetch(options: MockOptions = {}) {
  const latency = options.latencyMs ?? 250
  const scenario = options.scenario ?? readScenario
  const storage = options.storage === undefined ? safeStorage() : options.storage
  const now = options.now ?? (() => new Date())
  const ttl = options.tokenTtlMs ?? readTokenTtl() ?? 10 * 60_000

  const tokens = new Map<string, TokenRecord>()
  const mfaTokens = new Map<string, { userId: string; expires: number }>()
  const failures = new Map<string, { count: number; lockedUntil: number }>()
  const failedOnce = new Set<string>()

  const findUser = (id: string | null | undefined) => USERS.find((u) => u.me.id === id)

  function issue(user: MockUser, scope: TokenRecord['scope'], tenantId?: string) {
    const token = `mock.${scope}.${randomHex(24)}`
    const expires = now().getTime() + ttl
    tokens.set(token, { userId: user.me.id, scope, tenantId, expires })
    const body: AccessTokenResponse = {
      access_token: token,
      token_type: 'Bearer',
      expires_at: new Date(expires).toISOString(),
      scope,
      tenant_id: tenantId ?? null,
    }
    return json(body)
  }

  function rawRecord(req: Request) {
    const header = req.headers.get('Authorization') ?? ''
    const token = header.startsWith('Bearer ') ? header.slice(7) : ''
    const record = tokens.get(token)
    if (!record || record.expires <= now().getTime()) return undefined
    return record
  }

  function bearer(req: Request) {
    const record = rawRecord(req)
    if (!record || record.scope === 'kiosk') return undefined
    const user = findUser(record.userId)
    return user ? { user, record } : undefined
  }

  /** `POST /kiosk/token`: la credencial del dispositivo da un JWT de kiosco de 10 min. */
  function kioskToken(req: Request) {
    if (req.headers.get('X-Requested-With') !== 'horus') {
      return problem(403, 'ORIGIN_NOT_ALLOWED', 'Origen no permitido')
    }
    const kiosk = currentKiosk(now())
    if (!kiosk) return problem(401, 'UNAUTHENTICATED', 'Pantalla no enrolada o revocada')
    touchKiosk(kiosk.id, now())
    const token = `mock.kiosk.${randomHex(24)}`
    const expires = now().getTime() + ttl
    tokens.set(token, {
      userId: '',
      scope: 'kiosk',
      tenantId: kiosk.tenant_id,
      kioskId: kiosk.id,
      expires,
    })
    const body: AccessTokenResponse = {
      access_token: token,
      token_type: 'Bearer',
      expires_at: new Date(expires).toISOString(),
      scope: 'kiosk',
      tenant_id: kiosk.tenant_id,
    }
    return json(body)
  }

  /** Rutas de un token de kiosco: solo lectura de sus dashboards (api.md §2.12). */
  function kioskRoute(req: Request, path: string, record: TokenRecord): Response {
    const kiosk = currentKiosk(now())
    // Revocado o caducado: 401 en cualquier llamada → la TV vuelve a la pantalla de código.
    if (!kiosk || kiosk.id !== record.kioskId) {
      return problem(401, 'SESSION_REVOKED', 'Pantalla revocada')
    }
    const config = kioskConfig(kiosk)
    const assigned = config.items.map((i) => i.dashboard_id)
    if (req.method === 'GET' && path === '/kiosk/config') return json(config)
    if (req.method === 'GET' && path === '/system/status') return systemStatus(false)
    if (req.method === 'GET' && path === '/widget-types') {
      return json({
        data: WIDGET_CATALOG.filter((t) => t.kiosk_allowed) as unknown as WidgetType[],
      })
    }
    const dashboardMatch = path.match(/^\/dashboards\/([^/]+)$/)
    if (req.method === 'GET' && dashboardMatch && assigned.includes(dashboardMatch[1]!)) {
      const dashboard = TEMPLATES.find((d) => d.id === dashboardMatch[1])
      if (dashboard) return json(dashboard, 200, { ETag: `"${dashboard.version}"` })
    }
    const dataMatch = path.match(/^\/dashboards\/([^/]+)\/widgets\/([^/]+)\/data$/)
    if (req.method === 'GET' && dataMatch && assigned.includes(dataMatch[1]!)) {
      return widgetDataFor(
        { kind: 'kiosk', showPersonalData: kiosk.show_personal_data },
        kiosk.tenant_id,
        dataMatch[1]!,
        dataMatch[2]!,
      )
    }
    return problem(403, 'KIOSK_FORBIDDEN', 'Una pantalla NOC solo puede leer sus dashboards')
  }

  function cookieUser() {
    return findUser(storage?.getItem(SESSION_KEY))
  }

  const invalidCredentials = () =>
    // Mismo mensaje exista o no la cuenta (security.md §4.5).
    problem(401, 'INVALID_CREDENTIALS', 'Usuario o contraseña incorrectos')

  function login(body: { username?: string; password?: string }) {
    const username = String(body.username ?? '')
      .trim()
      .toLowerCase()
    const entry = failures.get(username)
    const t = now().getTime()
    if (entry && entry.lockedUntil > t) {
      const wait = Math.ceil((entry.lockedUntil - t) / 1000)
      return problem(429, 'RATE_LIMITED', 'Demasiados intentos', { 'Retry-After': String(wait) })
    }
    const user = USERS.find((u) => u.username === username)
    if (!user || user.password !== body.password) {
      const count = (entry?.count ?? 0) + 1
      failures.set(username, {
        count: count >= MAX_FAILURES ? 0 : count,
        lockedUntil: count >= MAX_FAILURES ? t + LOCK_SECONDS * 1000 : 0,
      })
      return invalidCredentials()
    }
    failures.delete(username)
    if (user.mfa) {
      const mfaToken = `mfa.${randomHex(16)}`
      mfaTokens.set(mfaToken, { userId: user.me.id, expires: t + 5 * 60_000 })
      return json({ mfa_required: true, mfa_token: mfaToken })
    }
    storage?.setItem(SESSION_KEY, user.me.id)
    return issue(user, 'session')
  }

  function verifyMfa(body: { mfa_token?: string; code?: string }) {
    const pending = mfaTokens.get(String(body.mfa_token ?? ''))
    if (!pending || pending.expires < now().getTime()) {
      return problem(401, 'UNAUTHENTICATED', 'La verificación caducó; vuelve a iniciar sesión')
    }
    if (String(body.code ?? '').replace(/\s/g, '') !== MOCK_TOTP_CODE) {
      return problem(401, 'INVALID_CREDENTIALS', 'Código incorrecto')
    }
    mfaTokens.delete(String(body.mfa_token))
    const user = findUser(pending.userId)
    if (!user) return invalidCredentials()
    storage?.setItem(SESSION_KEY, user.me.id)
    return issue(user, 'session')
  }

  /** `POST /auth/token`: con token de sesión o con la cookie de refresh (api.md §0.2). */
  function tokenFor(req: Request, body: { tenant_id?: string; scope?: string }) {
    const auth = bearer(req)
    const user = auth?.record.scope === 'session' ? auth.user : cookieUser()
    if (!user) return problem(401, 'TOKEN_EXPIRED', 'La sesión caducó')
    if (body.scope === 'platform') {
      return user.me.platform_roles.length
        ? issue(user, 'platform')
        : problem(403, 'PERMISSION_DENIED', 'Sin acceso a la plataforma')
    }
    const membership = user.me.memberships.find((m) => m.tenant_id === body.tenant_id)
    if (!membership) return problem(404, 'TENANT_NOT_FOUND', 'No encontrado')
    if (membership.tenant_status === 'suspended') {
      return problem(403, 'TENANT_SUSPENDED', 'El ISP está suspendido')
    }
    return issue(user, 'tenant', membership.tenant_id)
  }

  function systemStatus(detail: boolean): Response {
    const mode = scenario()
    if (mode === 'system-error')
      return problem(503, 'SERVICE_UNAVAILABLE', 'Servicio no disponible')
    const degraded = mode === 'degraded'
    const body: SystemStatus = {
      status: degraded ? 'degraded' : 'ok',
      checked_at: now().toISOString(),
      capabilities: {
        auth: 'ok',
        inventory: 'ok',
        wireguard: 'ok',
        ingest: 'ok',
        monitoring: degraded ? 'stale' : 'ok',
        realtime: 'ok',
        analytics: degraded ? 'unavailable' : 'ok',
        detection: 'ok',
        notifications: 'ok',
      },
      components: detail
        ? [
            { name: 'postgres', status: 'up' },
            { name: 'valkey', status: 'up' },
            { name: 'nats', status: 'up' },
            {
              name: 'clickhouse',
              status: degraded ? 'down' : 'up',
              since: degraded ? new Date(now().getTime() - 18 * 60_000).toISOString() : null,
              detail: degraded ? 'Sin respuesta en el puerto 9000' : null,
            },
            { name: 'collector', status: 'up' },
            { name: 'wg_agent', status: 'up' },
            { name: 'local_storage', status: 'up', detail: '62 % de 2 TB en /var/lib/horus' },
            { name: 'remote_storage', status: 'not_configured' },
          ]
        : [],
      disk_usage_ratio: detail ? 0.62 : null,
    }
    return json(body)
  }

  type Viewer = { kind: 'user'; user: MockUser } | { kind: 'kiosk'; showPersonalData: boolean }

  function widgetDataFor(viewer: Viewer, tenantId: string, dashboardId: string, widgetId: string) {
    const dashboard = TEMPLATES.find((d) => d.id === dashboardId)
    if (!dashboard) return problem(404, 'DASHBOARD_NOT_FOUND', 'No encontrado')
    const widget = dashboard.widgets.find((w) => w.id === widgetId)
    if (!widget) return problem(404, 'NOT_FOUND', 'No encontrado')
    const type = WIDGET_CATALOG.find((t) => t.type === widget.type)
    if (!type) return problem(422, 'WIDGET_TYPE_UNKNOWN', 'Tipo de widget desconocido')
    const allowed =
      viewer.kind === 'kiosk'
        ? type.kiosk_allowed
        : hasPermission(viewer.user, tenantId, type.required_permission)
    if (!allowed) {
      return problem(403, 'WIDGET_TYPE_NOT_ALLOWED', 'No tienes acceso a este widget')
    }
    const mode = scenario()
    const failKey = `${tenantId}:${dashboardId}:${widgetId}`
    if (mode === 'widget-error' && FAILING_ONCE.has(widgetId) && !failedOnce.has(failKey)) {
      failedOnce.add(failKey)
      return problem(500, 'INTERNAL', 'Error interno')
    }
    if (mode === 'degraded' && ANALYTICS_TYPES.has(widget.type)) {
      return problem(503, 'ANALYTICS_UNAVAILABLE', 'Analítica no disponible', {
        'Retry-After': '30',
      })
    }
    const tenant = findTenant(tenantId)
    if (!tenant) return problem(404, 'TENANT_NOT_FOUND', 'No encontrado')
    const data = widgetData(
      {
        tenant,
        widget,
        now: now(),
        // Kiosco: IPs de clientes solo si la pantalla lo permite (frontend.md §7.4).
        canSeePersonalData:
          viewer.kind === 'kiosk'
            ? viewer.showPersonalData
            : hasPermission(viewer.user, tenantId, 'customers.read'),
        empty: mode === 'empty',
      },
      { degraded: mode === 'degraded' },
    )
    if (!data) return problem(404, 'NOT_FOUND', 'Este widget no tiene datos')
    return json(data, 200, { 'Cache-Control': 'private, max-age=7' })
  }

  async function handle(req: Request): Promise<Response> {
    const url = new URL(req.url)
    const path = url.pathname.startsWith(BASE_PATH)
      ? url.pathname.slice(BASE_PATH.length)
      : url.pathname
    const route = `${req.method} ${path}`
    const body = req.method === 'GET' ? {} : await req.json().catch(() => ({}))

    switch (route) {
      case 'POST /auth/login':
        return login(body)
      case 'POST /auth/mfa/verify':
        return verifyMfa(body)
      case 'POST /auth/refresh': {
        if (req.headers.get('X-Requested-With') !== 'horus') {
          return problem(403, 'ORIGIN_NOT_ALLOWED', 'Origen no permitido')
        }
        const user = cookieUser()
        return user ? issue(user, 'session') : problem(401, 'UNAUTHENTICATED', 'Sesión no iniciada')
      }
      case 'POST /auth/logout':
        storage?.removeItem(SESSION_KEY)
        tokens.clear()
        return new Response(null, { status: 204 })
      case 'POST /auth/token':
        return tokenFor(req, body)
      case 'POST /kiosk/enroll':
        return enroll(req, body, now())
      case 'POST /kiosk/token':
        return kioskToken(req)
    }

    const kioskRecord = rawRecord(req)
    if (kioskRecord?.scope === 'kiosk') return kioskRoute(req, path, kioskRecord)

    const auth = bearer(req)
    if (!auth) return problem(401, 'TOKEN_EXPIRED', 'La sesión caducó')
    const { user, record } = auth

    switch (route) {
      case 'GET /me':
        return json(user.me)
      case 'GET /system/status':
        return systemStatus(user.me.platform_permissions.includes('platform.status.read'))
      case 'GET /widget-types':
        return json({ data: WIDGET_CATALOG as unknown as WidgetType[] })
    }

    if (record.scope === 'platform' && path.startsWith('/platform/')) {
      const res = platformRoute({
        req,
        url,
        path,
        method: req.method,
        body,
        can: (p) => user.me.platform_permissions.includes(p),
        now: now(),
      })
      return res ?? problem(404, 'NOT_FOUND', 'No encontrado')
    }

    // Rutas de negocio: exigen token de ISP (api.md §0.2; D-G0: sin ISP → TOKEN_SCOPE_INVALID).
    if (record.scope !== 'tenant' || !record.tenantId) {
      return problem(403, 'TOKEN_SCOPE_INVALID', 'Esta petición necesita un token de ISP')
    }
    const tenantId = record.tenantId

    if (route === 'GET /dashboards') {
      if (!hasPermission(user, tenantId, 'dashboards.read')) {
        return problem(403, 'PERMISSION_DENIED', 'Sin permiso')
      }
      return json({
        data: TEMPLATES.map(summary),
        page: { next_cursor: null, prev_cursor: null, has_more: false, limit: 50 },
      })
    }
    const dashboardMatch = path.match(/^\/dashboards\/([^/]+)$/)
    if (req.method === 'GET' && dashboardMatch) {
      const dashboard = TEMPLATES.find((d) => d.id === dashboardMatch[1])
      if (!dashboard || !hasPermission(user, tenantId, 'dashboards.read')) {
        return problem(404, 'DASHBOARD_NOT_FOUND', 'No encontrado')
      }
      return json(dashboard, 200, { ETag: `"${dashboard.version}"` })
    }
    const dataMatch = path.match(/^\/dashboards\/([^/]+)\/widgets\/([^/]+)\/data$/)
    if (req.method === 'GET' && dataMatch) {
      return widgetDataFor({ kind: 'user', user }, tenantId, dataMatch[1]!, dataMatch[2]!)
    }

    const tenant = findTenant(tenantId)!
    const can = (p: string) => hasPermission(user, tenantId, p)
    const mode = scenario()
    const shared = { req, path, method: req.method, body, tenant, can, now: now() }
    const res =
      tenantRoute({
        ...shared,
        url,
        userId: user.me.id,
        degraded: mode === 'degraded',
        empty: mode === 'empty',
        stepMs: readNumber('horus.mock.onboardingStepMs') ?? 6000,
      }) ??
      channelRoute(shared) ??
      kioskAdminRoute(shared)
    if (res) return res

    return problem(404, 'NOT_FOUND', 'No encontrado')
  }

  return async (req: Request): Promise<Response> => {
    if (latency > 0) {
      // Los datos de widget tardan algo distinto cada uno: se ve la carga independiente.
      const extra = req.url.includes('/widgets/') ? Math.random() * latency * 1.5 : 0
      await new Promise((resolve) => setTimeout(resolve, latency + extra))
    }
    if (req.signal?.aborted) throw new DOMException('Aborted', 'AbortError')
    return handle(req)
  }
}
