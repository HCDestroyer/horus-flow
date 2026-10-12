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
import { invitationRoute, memberRoute, readFlag } from './members'
import { platformRoute, platformUserStatus, restorePlatformUsers } from './platform'
import { PERMISSION_CATALOG, restoreRoles, ROLES } from './roles'
import { tenantRoute } from './tenant-routes'
import { createTenant, restoreTenants } from './tenants'
import { restoreWireguard } from './wireguard'
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
/** Ventana de re-autenticación reciente (`auth_time`, security.md §4: 5 min). */
const REAUTH_WINDOW_MS = 5 * 60_000
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

/** Uso del disco local (`horus_store_disk_*`): `localStorage['horus.mock.diskRatio']`. */
function readDiskRatio() {
  try {
    const v = Number(window.localStorage.getItem('horus.mock.diskRatio'))
    if (Number.isFinite(v) && v > 0 && v <= 1) return v
  } catch {
    // sin storage
  }
  return 0.62
}

function readOffline() {
  try {
    return window.localStorage.getItem('horus.mock.offline') === '1'
  } catch {
    return false
  }
}

/** Vida del access token para pruebas: `localStorage['horus.mock.tokenTtlMs']`. */
const readTokenTtl = () => readNumber('horus.mock.tokenTtlMs')

function hasPermission(user: MockUser, tenantId: string | undefined, permission: string) {
  const m = user.me.memberships.find((x) => x.tenant_id === tenantId)
  return !!m?.permissions_with_scope[permission]?.length
}

/** ¿Tiene el permiso en todo el ISP (alcance `*` o `tenant`, no solo en un nodo)? */
function hasTenantWide(user: MockUser, tenantId: string | undefined, permission: string) {
  const m = user.me.memberships.find((x) => x.tenant_id === tenantId)
  const scopes = m?.permissions_with_scope[permission] ?? []
  return scopes.includes('*') || scopes.includes('tenant')
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
  /** Última re-autenticación (`POST /auth/reauth`) de cada usuario. */
  const reauthAt = new Map<string, number>()

  const findUser = (id: string | null | undefined) => USERS.find((u) => u.me.id === id)

  // Cambios de cuenta (contraseña, TOTP) persistidos como la sesión: sobreviven a recargar.
  const accountKey = (id: string) => `horus.mock.account.${id}`
  for (const u of USERS) {
    try {
      const saved = JSON.parse(storage?.getItem(accountKey(u.me.id)) ?? 'null')
      if (saved) {
        u.password = saved.password
        u.mfa = saved.mfa
        u.me.mfa_enabled = saved.mfa
        u.me.must_change_password = saved.mustChange
      }
    } catch {
      // estado guardado ilegible: se ignora
    }
  }
  // ISP dados de alta en esta sesión simulada (consola de plataforma).
  restoreTenants(storage)
  // Roles propios de los ISP creados en esta sesión simulada.
  restoreRoles(storage)
  // Cambios de cuentas hechos desde la consola de plataforma.
  restorePlatformUsers(storage)
  // Cambios del hub WireGuard (endpoint, MTU, rangos…).
  restoreWireguard(storage)

  const saveAccount = (u: MockUser) =>
    storage?.setItem(
      accountKey(u.me.id),
      JSON.stringify({ password: u.password, mfa: u.mfa, mustChange: u.me.must_change_password }),
    )

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

  /**
   * ¿La acción `x-reauth` exige re-autenticarse? Simulación: con
   * `localStorage['horus.mock.reauthRequired'] = '1'` el `auth_time` del login se trata como
   * viejo; vale solo una re-autenticación hecha en los últimos 5 minutos.
   */
  function reauthRequired(user: MockUser) {
    if (!readFlag('horus.mock.reauthRequired')) return false
    const at = reauthAt.get(user.me.id)
    return at === undefined || now().getTime() - at > REAUTH_WINDOW_MS
  }

  /** `POST /auth/reauth`: contraseña (+ TOTP) → token del mismo ámbito con `auth_time` nuevo. */
  function reauth(user: MockUser, record: TokenRecord, body: { password?: string; code?: string }) {
    if (body.password !== user.password) {
      return problem(401, 'INVALID_CREDENTIALS', 'Contraseña incorrecta')
    }
    if (user.mfa && String(body.code ?? '').replace(/\s/g, '') !== MOCK_TOTP_CODE) {
      return problem(401, 'INVALID_MFA_CODE', 'Código incorrecto')
    }
    reauthAt.set(user.me.id, now().getTime())
    return issue(user, record.scope, record.tenantId)
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
    // Una cuenta bloqueada desde la consola de plataforma no entra (mismo mensaje).
    const blocked = user && platformUserStatus(user) === 'disabled'
    if (!user || user.password !== body.password || blocked) {
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
    if (user.me.must_change_password) {
      return problem(403, 'PASSWORD_CHANGE_REQUIRED', 'Debe cambiar la contraseña')
    }
    if (body.scope === 'platform') {
      if (!user.me.platform_roles.length) {
        return problem(403, 'PERMISSION_DENIED', 'Sin acceso a la plataforma')
      }
      if (!user.me.mfa_enabled) {
        return problem(403, 'MFA_ENROLLMENT_REQUIRED', 'Active el TOTP')
      }
      return issue(user, 'platform')
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
            {
              name: 'local_storage',
              status: readDiskRatio() >= 0.85 ? 'degraded' : 'up',
              detail: `${Math.round(readDiskRatio() * 100)} % de 2 TB en /var/lib/horus`,
            },
            { name: 'remote_storage', status: 'not_configured' },
          ]
        : [],
      disk_usage_ratio: detail ? readDiskRatio() : null,
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
      case 'POST /auth/invitations/inspect':
      case 'POST /auth/invitations/accept':
        return invitationRoute(route, body, now())!
    }

    const kioskRecord = rawRecord(req)
    if (kioskRecord?.scope === 'kiosk') return kioskRoute(req, path, kioskRecord)

    const auth = bearer(req)
    if (!auth) return problem(401, 'TOKEN_EXPIRED', 'La sesión caducó')
    const { user, record } = auth

    switch (route) {
      case 'GET /me':
        return json(user.me)
      case 'POST /auth/reauth':
        return reauth(user, record, body)
      case 'POST /me/password': {
        if (body.current_password !== user.password) {
          return problem(401, 'INVALID_CREDENTIALS', 'Contraseña actual incorrecta')
        }
        if (String(body.new_password ?? '').length < 12) {
          return problem(422, 'VALIDATION_FAILED', 'La contraseña es demasiado corta')
        }
        user.password = String(body.new_password)
        user.me.must_change_password = false
        saveAccount(user)
        return new Response(null, { status: 204 })
      }
      case 'POST /me/totp/enroll':
        return json(
          {
            secret: 'JBSWY3DPEHPK3PXP',
            otpauth_uri: `otpauth://totp/Horus%20Flow:${encodeURIComponent(user.me.email)}?secret=JBSWY3DPEHPK3PXP&issuer=Horus%20Flow`,
          },
          200,
          { 'Cache-Control': 'no-store' },
        )
      case 'POST /me/totp/confirm': {
        if (!/^\d{6}$/.test(String(body.code ?? ''))) {
          return problem(401, 'INVALID_MFA_CODE', 'Código incorrecto')
        }
        user.me.mfa_enabled = true
        user.mfa = true
        saveAccount(user)
        return json({
          recovery_codes: Array.from(
            { length: 10 },
            (_, i) => `rc${i}-mock-${String(i).padStart(4, '0')}`,
          ),
        })
      }
      case 'GET /system/status':
        return systemStatus(user.me.platform_permissions.includes('platform.status.read'))
      case 'GET /widget-types':
        return json({ data: WIDGET_CATALOG as unknown as WidgetType[] })
      case 'GET /permissions':
        return json({ version: 'v0', permissions: PERMISSION_CATALOG, roles: ROLES })
    }

    if (record.scope === 'platform' && route === 'POST /platform/tenants') {
      return createTenant({
        req,
        body,
        can: (p) => user.me.platform_permissions.includes(p),
        now: now(),
        reauthRequired: reauthRequired(user),
        storage,
      })
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
        userId: user.me.id,
        reauthRequired: reauthRequired(user),
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
        reauthRequired: reauthRequired(user),
      }) ??
      channelRoute(shared) ??
      kioskAdminRoute(shared) ??
      memberRoute({
        ...shared,
        url,
        userId: user.me.id,
        reauthRequired: reauthRequired(user),
        tenantWide: (p) => hasTenantWide(user, tenantId, p),
      })
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
    // Corte de red simulado (pruebas del kiosco): `localStorage['horus.mock.offline'] = '1'`.
    if (readOffline()) throw new TypeError('Failed to fetch')
    return handle(req)
  }
}
