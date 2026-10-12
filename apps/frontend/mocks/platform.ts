import type {
  InstallationAccess,
  PlatformRoleKey,
  PlatformUser,
  PlatformUserStatus,
  ReputationSource,
  ReputationSourceCreate,
  TenantOverview,
} from '~~/types/api'
import { openFindings } from './base'
import { ALL_TENANTS, TENANTS, USERS, type MockUser } from './data'
import { forbidden, ifMatch, json, noContent, notFound, paginate, problem } from './http'
import { routersOf } from './inventory'
import { parseIp } from './net'
import { hash, mockUuid } from './random'
import { mockState } from './state'
import { remoteDestinationRoute } from './remote-destinations'
import { wireguardHubs, wireguardRoute } from './wireguard'

/**
 * Consola de plataforma simulada (I1-31, D19, D20): resumen por ISP, hubs WireGuard, acceso a
 * la instalación, destinos remotos SFTP (`mocks/remote-destinations.ts`) y fuentes de
 * reputación con alta de listas personalizadas.
 */

export interface PlatformRouteContext {
  req: Request
  url: URL
  path: string
  method: string
  body: Record<string, unknown>
  can: (permission: string) => boolean
  now: Date
  /** Quien llama (no puede bloquearse a sí mismo). */
  userId?: string
  /** La acción `x-reauth` exige re-autenticarse antes (simulación de `auth_time` viejo). */
  reauthRequired?: boolean
}

function readAccessMode(): InstallationAccess['access_mode'] {
  try {
    const v = window.localStorage.getItem('horus.mock.accessMode')
    if (v === 'domain' || v === 'subdomain' || v === 'ip_only') return v
  } catch {
    // sin storage
  }
  return 'ip_only'
}

const FINGERPRINT =
  'SHA256:3C:9F:A1:7E:52:0B:D4:66:8A:19:F3:C2:5E:71:0D:AA:4B:93:E8:27:6C:15:BF:D0:82:39:41:7A:CE:05:96:1F'

export function installationAccess(now: Date): InstallationAccess {
  const mode = readAccessMode()
  const host =
    mode === 'ip_only'
      ? '203.0.113.10'
      : mode === 'domain'
        ? 'horus.fibranorte.example'
        : 'fibranorte.horus-flow.example'
  const named = mode !== 'ip_only'
  return {
    access_mode: mode,
    host,
    public_base_url: `https://${host}`,
    allowed_origins: [`https://${host}`],
    wireguard_endpoint: host,
    tls: named
      ? {
          mode: 'acme',
          hsts: true,
          issuer: "Let's Encrypt R11",
          fingerprint_sha256: null,
          not_after: new Date(now.getTime() + 61 * 86_400_000).toISOString(),
        }
      : {
          mode: 'self_signed',
          hsts: false,
          issuer: 'Horus Flow (autogenerado)',
          fingerprint_sha256: FINGERPRINT,
          not_after: new Date(now.getTime() + 820 * 86_400_000).toISOString(),
        },
    warnings: named
      ? []
      : [
          {
            code: 'ip_only_access',
            severity: 'warning',
            message:
              'Se accede solo por la IP del servidor: los navegadores mostrarán un aviso de certificado.',
          },
          {
            code: 'self_signed_certificate',
            severity: 'info',
            message: 'Certificado autogenerado: compara la huella antes de aceptar el aviso.',
          },
        ],
  }
}

function overview(now: Date): TenantOverview[] {
  return ALL_TENANTS.map((tenant) => {
    const routers = routersOf(tenant, now)
    return {
      tenant_id: tenant.tenant_id,
      slug: tenant.tenant_slug,
      name: tenant.tenant_name,
      status: 'active',
      routers_total: routers.length,
      exporters_exporting: routers.filter((r) => ['exporting', 'lossy'].includes(r.exporter.state))
        .length,
      exporters_silent: routers.filter((r) => r.exporter.state === 'silent').length,
      tunnels_down: routers.filter((r) => r.peer.handshake_state === 'stale').length,
      ingest_flows_per_second: routers.reduce((a, r) => a + (r.exporter.flows_per_second ?? 0), 0),
      open_findings_critical: openFindings(tenant, now).filter((f) => f.severity === 'critical')
        .length,
      flow_coverage_ratio: 0.92,
      flows_dropped_quota_last_hour: '0',
    }
  })
}

const CATALOG: Omit<ReputationSource, 'created_at' | 'updated_at'>[] = [
  {
    id: mockUuid('0194b000', 1),
    key: 'abuse_ch_feodo',
    name: 'abuse.ch Feodo Tracker',
    url: 'https://feodotracker.abuse.ch/downloads/ipblocklist.csv',
    format: 'abusech-feodo-csv',
    category: 'botnet_cc',
    confidence: 90,
    frequency: '1h',
    origin: 'catalog',
    commercial_use: 'approved',
    license: 'CC0',
    enabled: true,
    status: 'ok',
    entries: 412,
    has_auth: false,
    numeric_id: 1,
    consecutive_failures: 0,
    last_attempt_at: null,
    last_success_at: null,
    last_error: null,
    created_by: null,
    version: 1,
  },
  {
    id: mockUuid('0194b000', 2),
    key: 'abuse_ch_threatfox',
    name: 'abuse.ch ThreatFox (IOC IP:puerto)',
    url: 'https://threatfox.abuse.ch/export/csv/ip-port/recent/',
    format: 'abusech-threatfox-csv',
    category: 'malware_dist',
    confidence: 75,
    frequency: '1h',
    origin: 'catalog',
    commercial_use: 'approved',
    license: 'CC0',
    enabled: true,
    status: 'ok',
    entries: 3_870,
    has_auth: false,
    numeric_id: 2,
    consecutive_failures: 0,
    last_attempt_at: null,
    last_success_at: null,
    last_error: null,
    created_by: null,
    version: 1,
  },
  {
    id: mockUuid('0194b000', 3),
    key: 'spamhaus_drop',
    name: 'Spamhaus DROP',
    url: 'https://www.spamhaus.org/drop/drop_v4.json',
    format: 'spamhaus-drop-json',
    category: 'blocklist',
    confidence: 85,
    frequency: '24h',
    origin: 'catalog',
    commercial_use: 'approved',
    license: 'Spamhaus DROP (uso permitido)',
    enabled: true,
    status: 'ok',
    entries: 1_402,
    has_auth: false,
    numeric_id: 3,
    consecutive_failures: 0,
    last_attempt_at: null,
    last_success_at: null,
    last_error: null,
    created_by: null,
    version: 1,
  },
  {
    id: mockUuid('0194b000', 4),
    key: 'tor_exit',
    name: 'Nodos de salida de Tor',
    url: 'https://check.torproject.org/torbulkexitlist',
    format: 'netset',
    category: 'tor_exit',
    confidence: 60,
    frequency: '6h',
    origin: 'catalog',
    commercial_use: 'approved',
    license: 'CC BY 3.0 US',
    enabled: false,
    status: 'disabled',
    entries: 0,
    has_auth: false,
    numeric_id: 4,
    consecutive_failures: 0,
    last_attempt_at: null,
    last_success_at: null,
    last_error: null,
    created_by: null,
    version: 1,
  },
  {
    id: mockUuid('0194b000', 5),
    key: 'csirt_regional_scanners',
    name: 'CSIRT regional · escáneres',
    url: 'https://csirt.example.net/feeds/scanners.netset',
    format: 'netset',
    category: 'scanner',
    confidence: 70,
    frequency: '6h',
    origin: 'custom',
    commercial_use: 'operator_acknowledged',
    license: 'Acuerdo de intercambio con el CSIRT',
    enabled: true,
    status: 'failing',
    entries: 2_215,
    has_auth: true,
    numeric_id: 101,
    consecutive_failures: 3,
    last_attempt_at: null,
    last_success_at: null,
    last_error: 'HTTP 503 del origen en los últimos 3 intentos; se conserva la carga anterior.',
    created_by: '01926b3e-1111-7000-8000-000000000001',
    version: 2,
  },
]

function sources(now: Date) {
  if (!mockState.reputation) {
    const t = now.getTime()
    mockState.reputation = CATALOG.map((s, i) => ({
      ...s,
      created_at: '2026-09-01T10:00:00.000Z',
      updated_at: '2026-09-01T10:00:00.000Z',
      last_attempt_at: s.enabled ? new Date(t - (12 + i * 7) * 60_000).toISOString() : null,
      last_success_at:
        s.status === 'ok'
          ? new Date(t - (12 + i * 7) * 60_000).toISOString()
          : s.status === 'failing'
            ? new Date(t - 19 * 3_600_000).toISOString()
            : null,
    }))
  }
  return mockState.reputation
}

const FREQUENCY = /^(\d+)(m|h|d)$/
function frequencyOk(value: string) {
  const m = value.match(FREQUENCY)
  if (!m) return false
  const minutes = Number(m[1]) * ({ m: 1, h: 60, d: 1440 } as const)[m[2] as 'm']
  return minutes >= 15 && minutes <= 7 * 1440
}

function urlAllowed(raw: string) {
  try {
    const url = new URL(raw)
    if (url.protocol !== 'https:') return false
    const ip = parseIp(url.hostname.replace(/^\[|\]$/g, ''))
    // Nada de redes privadas ni locales (SSRF).
    if (ip?.family === 4) {
      const [a, b] = url.hostname.split('.').map(Number) as [number, number]
      if (a === 10 || a === 127 || (a === 172 && b >= 16 && b < 32) || (a === 192 && b === 168))
        return false
    }
    return url.hostname !== 'localhost'
  } catch {
    return false
  }
}

function invalid(field: string, message: string, code = 'INVALID') {
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

function reputationRoutes(ctx: PlatformRouteContext): Response | undefined {
  if (!ctx.path.startsWith('/platform/reputation')) return
  if (!ctx.can('platform.reputation_sources.manage')) return forbidden()
  const list = sources(ctx.now)
  if (ctx.method === 'GET' && ctx.path === '/platform/reputation/sources') {
    return json({ data: list })
  }
  if (ctx.method === 'POST' && ctx.path === '/platform/reputation/sources') {
    const input = ctx.body as unknown as ReputationSourceCreate
    if (!input.name?.trim()) return invalid('name', 'Escribe un nombre', 'REQUIRED')
    if (!/^[a-z][a-z0-9_]{2,47}$/.test(input.key ?? '')) {
      return invalid('key', 'Solo minúsculas, números y guion bajo (3–48), empezando por letra')
    }
    if (list.some((s) => s.key === input.key)) {
      return problem(
        409,
        'ALREADY_EXISTS',
        'Ya existe una fuente con esa clave',
        {},
        {
          errors: [
            { field: 'key', code: 'ALREADY_EXISTS', message: 'Ya existe una fuente con esa clave' },
          ],
        },
      )
    }
    if (!urlAllowed(input.url ?? '')) {
      return problem(
        422,
        'REPUTATION_SOURCE_URL_NOT_ALLOWED',
        'URL no permitida',
        {},
        {
          errors: [
            {
              field: 'url',
              code: 'URL_NOT_ALLOWED',
              message: 'Usa una URL https pública (no se permiten redes privadas ni locales)',
            },
          ],
        },
      )
    }
    if (!frequencyOk(String(input.frequency ?? ''))) {
      return invalid('frequency', 'Entre 15 min y 7 días, p. ej. 1h, 6h o 24h')
    }
    if (!(input.confidence >= 0 && input.confidence <= 100)) {
      return invalid('confidence', 'Entre 0 y 100')
    }
    if (input.terms_acknowledged !== true) {
      return invalid('terms_acknowledged', 'Confirma que puedes usar esta lista', 'REQUIRED')
    }
    const now = ctx.now.toISOString()
    const created: ReputationSource = {
      id: mockUuid('0194b100', hash(input.key)),
      key: input.key,
      name: input.name,
      url: input.url,
      format: input.format,
      category: input.category,
      confidence: input.confidence,
      frequency: input.frequency,
      origin: 'custom',
      commercial_use: 'operator_acknowledged',
      license: input.license ?? null,
      notes: input.notes ?? null,
      enabled: input.enabled ?? true,
      status: 'pending',
      entries: 0,
      has_auth: !!input.auth_header_value,
      numeric_id: 100 + list.length,
      consecutive_failures: 0,
      last_attempt_at: null,
      last_success_at: null,
      last_error: null,
      created_by: '01926b3e-1111-7000-8000-000000000001',
      created_at: now,
      updated_at: now,
      version: 1,
    }
    list.push(created)
    return json(created, 201, { ETag: '"1"' })
  }
  const m = ctx.path.match(/^\/platform\/reputation\/sources\/([^/]+)(\/refresh)?$/)
  const source = m && list.find((s) => s.id === m[1])
  if (!m || !source) return notFound('REPUTATION_SOURCE_NOT_FOUND')
  if (m[2] && ctx.method === 'POST') {
    if (!ctx.req.headers.get('Idempotency-Key')) {
      return problem(428, 'PRECONDITION_REQUIRED', 'Falta Idempotency-Key')
    }
    Object.assign(source, {
      status: 'ok',
      consecutive_failures: 0,
      last_error: null,
      last_attempt_at: ctx.now.toISOString(),
      last_success_at: ctx.now.toISOString(),
      entries: source.entries || 128,
    })
    return json(source, 202)
  }
  if (ctx.method === 'GET') return json(source, 200, { ETag: `"${source.version}"` })
  if (ifMatch(ctx.req) !== source.version) {
    return problem(
      412,
      'PRECONDITION_FAILED',
      'La fuente cambió',
      {},
      {
        current: source as unknown as Record<string, unknown>,
      },
    )
  }
  if (ctx.method === 'DELETE') {
    if (source.origin === 'catalog') {
      return problem(
        422,
        'REPUTATION_SOURCE_READ_ONLY',
        'Las fuentes del catálogo base no se borran: desactívalas',
      )
    }
    list.splice(list.indexOf(source), 1)
    return noContent()
  }
  if (ctx.method === 'PATCH') {
    const patch = ctx.body
    if (source.origin === 'catalog' && Object.keys(patch).some((k) => k !== 'enabled')) {
      return problem(
        422,
        'REPUTATION_SOURCE_READ_ONLY',
        'Del catálogo base solo se puede activar o desactivar',
      )
    }
    Object.assign(source, patch, {
      version: source.version + 1,
      updated_at: ctx.now.toISOString(),
    })
    if ('enabled' in patch) source.status = patch.enabled ? 'pending' : 'disabled'
    return json(source, 200, { ETag: `"${source.version}"` })
  }
}

// --- Usuarios de plataforma (I1, `GET/PATCH /platform/users`) -------------------------------

/** Permisos de cada rol de plataforma (C7, permissions.yaml). */
export const PLATFORM_ROLE_PERMISSIONS: Record<string, string[]> = {
  platform_admin: [
    'platform.tenants.read',
    'platform.tenants.manage',
    'platform.users.read',
    'platform.users.manage',
    'platform.audit.read',
    'platform.status.read',
    'platform.updates.read',
    'platform.storage.manage',
    'platform.wireguard.manage',
    'platform.catalog.manage',
    'platform.reputation_sources.manage',
  ],
  platform_operator: ['platform.status.read', 'platform.tenants.read', 'platform.audit.read'],
  platform_auditor: ['platform.audit.read', 'platform.tenants.read', 'platform.users.read'],
}

/** Cuentas sintéticas que no inician sesión en la demo (solo para la lista). */
const EXTRA_USERS: PlatformUser[] = [
  {
    id: mockUuid('0195c000', 1),
    email: 'carlos.mendez@horus.example',
    display_name: 'Carlos Méndez',
    status: 'active',
    mfa_enabled: true,
    platform_roles: ['platform_operator'],
    memberships: [],
  },
  {
    id: mockUuid('0195c000', 2),
    email: 'sofia.herrera@horus.example',
    display_name: 'Sofía Herrera',
    status: 'active',
    mfa_enabled: true,
    platform_roles: ['platform_auditor'],
    memberships: [],
  },
  {
    id: mockUuid('0195c000', 3),
    email: 'marta.gomez@fibranorte.example',
    display_name: 'Marta Gómez',
    status: 'locked',
    mfa_enabled: true,
    platform_roles: [],
    memberships: [
      {
        tenant_id: TENANTS.fibraNorte.tenant_id,
        tenant_slug: TENANTS.fibraNorte.tenant_slug,
        role_keys: ['security_analyst'],
      },
    ],
  },
  {
    id: mockUuid('0195c000', 4),
    email: 'pablo.ortega@valleconecta.example',
    display_name: 'Pablo Ortega',
    status: 'disabled',
    mfa_enabled: false,
    platform_roles: [],
    memberships: [
      {
        tenant_id: TENANTS.valleConecta.tenant_id,
        tenant_slug: TENANTS.valleConecta.tenant_slug,
        role_keys: ['noc', 'analyst'],
      },
    ],
  },
  {
    id: mockUuid('0195c000', 5),
    email: 'pedro.salas@fibranorte.example',
    display_name: 'pedro.salas',
    status: 'pending',
    mfa_enabled: false,
    platform_roles: [],
    memberships: [
      {
        tenant_id: TENANTS.fibraNorte.tenant_id,
        tenant_slug: TENANTS.fibraNorte.tenant_slug,
        role_keys: ['noc'],
      },
    ],
  },
]

/** Roles de plataforma de partida de las cuentas de demo (para `resetPlatformUsers`). */
const SEEDED_ROLES = new Map(
  USERS.map((u) => [
    u.me.id,
    { roles: [...u.me.platform_roles], perms: [...u.me.platform_permissions] },
  ]),
)

/** Pruebas: deshace los cambios de roles de plataforma en las cuentas de demo. */
export function resetPlatformUsers() {
  mockState.platformUsers.clear()
  for (const u of USERS) {
    const seeded = SEEDED_ROLES.get(u.me.id)
    if (seeded) {
      u.me.platform_roles = [...seeded.roles]
      u.me.platform_permissions = [...seeded.perms]
    }
  }
}

const USERS_KEY = 'horus.mock.platformUsers'
type Storage = Pick<globalThis.Storage, 'getItem' | 'setItem'> | null | undefined
let usersStorage: Storage

/** Recupera los cambios de cuentas hechos en esta sesión simulada (al recargar). */
export function restorePlatformUsers(storage: Storage) {
  usersStorage = storage
  try {
    const saved = JSON.parse(storage?.getItem(USERS_KEY) ?? '{}') as Record<
      string,
      { status?: PlatformUserStatus; platform_roles?: PlatformRoleKey[] }
    >
    for (const [id, change] of Object.entries(saved)) {
      if (!mockState.platformUsers.has(id)) mockState.platformUsers.set(id, change)
      const user = USERS.find((u) => u.me.id === id)
      if (user && change.platform_roles) applyPlatformRoles(user, change.platform_roles)
    }
  } catch {
    // estado guardado ilegible: se ignora
  }
}

function saveUsers() {
  try {
    usersStorage?.setItem(USERS_KEY, JSON.stringify(Object.fromEntries(mockState.platformUsers)))
  } catch {
    // sin storage: solo en memoria
  }
}

function applyPlatformRoles(user: MockUser, roles: PlatformRoleKey[]) {
  user.me.platform_roles = [...roles]
  user.me.platform_permissions = [
    ...new Set(roles.flatMap((r) => PLATFORM_ROLE_PERMISSIONS[r] ?? [])),
  ]
}

/** Estado de una cuenta que inicia sesión: el fijado desde la consola o el derivado. */
export function platformUserStatus(user: MockUser): PlatformUserStatus {
  const override = mockState.platformUsers.get(user.me.id)?.status
  if (override) return override
  return user.me.must_change_password && !user.me.mfa_enabled ? 'pending' : 'active'
}

function platformUsers(): PlatformUser[] {
  const real: PlatformUser[] = USERS.map((u) => ({
    id: u.me.id,
    email: u.me.email,
    display_name: u.me.display_name,
    platform_roles: [...u.me.platform_roles],
    mfa_enabled: u.me.mfa_enabled,
    status: platformUserStatus(u),
    memberships: u.me.memberships.map((m) => ({
      tenant_id: m.tenant_id,
      tenant_slug: m.tenant_slug,
      role_keys: m.roles.map((r) => r.role_key ?? ''),
    })),
  }))
  const extra = EXTRA_USERS.map((u) => {
    const change = mockState.platformUsers.get(u.id)
    return {
      ...u,
      status: change?.status ?? u.status,
      platform_roles: change?.platform_roles ?? u.platform_roles,
    }
  })
  return [...real, ...extra].sort((a, b) => a.display_name.localeCompare(b.display_name, 'es'))
}

const PLATFORM_ROLES = Object.keys(PLATFORM_ROLE_PERMISSIONS) as PlatformRoleKey[]

function lastAdminConflict() {
  return problem(409, 'LAST_PLATFORM_ADMIN', 'Debe quedar al menos un superadministrador activo')
}

function usersRoutes(ctx: PlatformRouteContext): Response | undefined {
  if (ctx.path === '/platform/users' && ctx.method === 'GET') {
    if (!ctx.can('platform.users.read')) return forbidden()
    const q = (ctx.url.searchParams.get('q') ?? '').trim().toLowerCase()
    const rows = platformUsers().filter(
      (u) => !q || u.display_name.toLowerCase().includes(q) || u.email.toLowerCase().includes(q),
    )
    return json(paginate(rows, ctx.url))
  }
  const m = ctx.path.match(/^\/platform\/users\/([^/]+)(\/platform-roles)?$/)
  if (!m) return
  const isRoles = !!m[2]
  if ((isRoles && ctx.method !== 'PUT') || (!isRoles && ctx.method !== 'PATCH')) return
  if (!ctx.can('platform.users.manage')) return forbidden()
  const all = platformUsers()
  const target = all.find((u) => u.id === m[1])
  if (!target) return notFound()
  if (ctx.reauthRequired) {
    return problem(403, 'REAUTH_REQUIRED', 'Vuelve a identificarte para gestionar usuarios')
  }
  const activeAdmins = (list: PlatformUser[]) =>
    list.filter((u) => u.status === 'active' && u.platform_roles.includes('platform_admin')).length

  let next: PlatformUser
  if (isRoles) {
    const roles = (ctx.body as { platform_roles?: unknown }).platform_roles
    if (
      !Array.isArray(roles) ||
      roles.some((r) => !PLATFORM_ROLES.includes(r as PlatformRoleKey))
    ) {
      return problem(
        422,
        'VALIDATION_FAILED',
        'Revisa los campos marcados',
        {},
        {
          errors: [
            { field: 'platform_roles', code: 'INVALID', message: 'Rol de plataforma no válido' },
          ],
        },
      )
    }
    next = { ...target, platform_roles: [...new Set(roles as PlatformRoleKey[])] }
  } else {
    const status = String((ctx.body as { status?: unknown }).status ?? '')
    if (status !== 'active' && status !== 'disabled') {
      return problem(
        422,
        'VALIDATION_FAILED',
        'Revisa los campos marcados',
        {},
        { errors: [{ field: 'status', code: 'INVALID', message: 'Estado no válido' }] },
      )
    }
    if (status === 'disabled' && target.id === ctx.userId) {
      return problem(409, 'CONFLICT', 'No puedes bloquear tu propia cuenta')
    }
    next = { ...target, status }
  }
  if (activeAdmins(all.map((u) => (u.id === next.id ? next : u))) === 0) return lastAdminConflict()

  const roles = next.platform_roles as PlatformRoleKey[]
  const change = { ...mockState.platformUsers.get(target.id) }
  if (isRoles) change.platform_roles = roles
  else change.status = next.status
  mockState.platformUsers.set(target.id, change)
  const account = USERS.find((u) => u.me.id === target.id)
  if (account && isRoles) applyPlatformRoles(account, roles)
  saveUsers()
  return json(next)
}

export function platformRoute(ctx: PlatformRouteContext): Response | undefined {
  const rep =
    reputationRoutes(ctx) ??
    usersRoutes(ctx) ??
    wireguardRoute({ ...ctx, defaultEndpoint: installationAccess(ctx.now).wireguard_endpoint }) ??
    remoteDestinationRoute(ctx)
  if (rep) return rep
  if (ctx.method !== 'GET') return
  switch (ctx.path) {
    case '/platform/overview':
      if (!ctx.can('platform.status.read')) return forbidden()
      return json({ data: overview(ctx.now), generated_at: ctx.now.toISOString() })
    case '/platform/wireguard/hubs':
      if (!ctx.can('platform.status.read')) return forbidden()
      return json({
        data: wireguardHubs(ctx.now, installationAccess(ctx.now).wireguard_endpoint),
      })
    case '/platform/installation':
      if (!ctx.can('platform.status.read')) return forbidden()
      return json(installationAccess(ctx.now))
    case '/platform/tenants':
      return json({
        data: ALL_TENANTS.map((t) => ({
          id: t.tenant_id,
          slug: t.tenant_slug,
          name: t.tenant_name,
          status: 'active',
          country: t.created?.country ?? 'CO',
          timezone: t.created?.timezone ?? 'America/Bogota',
          quotas: {},
          settings: {},
          counts: { sites: t.sites.length, routers: routersOf(t, ctx.now).length },
          created_at: t.created?.at ?? '2026-09-01T10:00:00.000Z',
          updated_at: t.created?.at ?? '2026-09-01T10:00:00.000Z',
          version: 1,
        })),
        page: { limit: 50, has_more: false, next_cursor: null, prev_cursor: null },
      })
  }
}
