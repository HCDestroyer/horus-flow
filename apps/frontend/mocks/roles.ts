import type { Role, RoleAssignment } from '~~/types/api'
import { NOC, ROLE_IDS, SECURITY_ANALYST, TENANT_ADMIN, type MockTenant } from './data'
import { forbidden, ifMatch, json, noContent, notFound, problem } from './http'
import { hash, mockUuid } from './random'
import { mockState } from './state'

/**
 * Catálogo de permisos (C7, `GET /permissions`), roles de sistema y roles propios de cada ISP
 * (auth.yaml `/roles`, `/roles/{role_id}`). Datos sintéticos.
 *
 * - Los roles propios se guardan en el storage de la sesión simulada (`sessionStorage`), así
 *   sobreviven a recargar la página; las asignaciones (miembros) siguen en memoria.
 * - Anti-escalada: solo se conceden permisos que quien edita tiene en todo el ISP (`*`);
 *   si no, `403 PERMISSION_DENIED`.
 * - `localStorage['horus.mock.roleConflict'] = '1'`: el primer `PATCH` de cada rol responde
 *   `412` como si otra persona lo hubiera editado antes (su versión sube).
 */

type Permission = { key: string; description: string; family: 'tenant' | 'platform' }

const tenant = (key: string, description: string): Permission => ({
  key,
  description,
  family: 'tenant',
})
const platform = (key: string, description: string): Permission => ({
  key,
  description,
  family: 'platform',
})

/** Copia de `packages/schemas/permissions/v0/permissions.yaml` (descripciones en español). */
export const PERMISSION_CATALOG: Permission[] = [
  tenant('users.read', 'Ver miembros del ISP'),
  tenant('users.manage', 'Invitar y quitar miembros del ISP'),
  tenant('roles.read', 'Ver roles y asignaciones'),
  tenant('roles.manage', 'Crear y editar roles propios del ISP'),
  tenant('roles.assign', 'Asignar roles a miembros'),
  tenant('audit.read', 'Ver la auditoría del ISP'),
  tenant('audit.export', 'Exportar la auditoría del ISP'),
  tenant('sites.read', 'Ver nodos y prefijos de clientes'),
  tenant('sites.create', 'Crear nodos'),
  tenant('sites.update', 'Editar nodos y gestionar prefijos de clientes'),
  tenant('sites.delete', 'Dar de baja nodos'),
  tenant('devices.read', 'Ver routers'),
  tenant('devices.create', 'Registrar routers'),
  tenant('devices.update', 'Editar routers'),
  tenant('devices.delete', 'Dar de baja routers'),
  tenant('devices.credentials.write', 'Reemplazar credenciales write-only de los routers'),
  tenant('devices.credentials.reveal', 'Revelar credenciales en claro'),
  tenant('wireguard.read', 'Ver túneles (peers)'),
  tenant('wireguard.write', 'Generar script de onboarding y tokens de enrolamiento'),
  tenant('wireguard.keys.rotate', 'Rotación de claves'),
  tenant('snmp.read', 'Ver métricas SNMP'),
  tenant('snmp.manage', 'Perfiles e intervalos SNMP'),
  tenant('flows.read', 'Ver estado de exportadores'),
  tenant('flows.manage', 'Configuración de exportadores'),
  tenant('customers.read', 'Lista y ficha de clientes (IPs, dato personal)'),
  tenant('customers.update', 'Alias y notas de clientes'),
  tenant('customers.kind.write', 'Cambio manual de tipo, desbloqueo y reinicio'),
  tenant('customers.export', 'Exportar clientes (CSV con IPs)'),
  tenant('traffic.read', 'Agregados de tráfico por nodo, router, servicio, categoría y ASN'),
  tenant('traffic.customer.read', 'Tráfico de un cliente concreto'),
  tenant('traffic.catalog.read', 'Leer el catálogo de clasificación'),
  tenant('security.findings.read', 'Hallazgos, estado de seguridad por cliente y allowlist'),
  tenant('security.findings.manage', 'Reconocer, resolver, falso positivo y allowlist del ISP'),
  tenant('security.evidence.read', 'Flujos de evidencia de un cliente'),
  tenant('alerts.read', 'Ver canales de notificación, entregas y alertas'),
  tenant('alerts.ack', 'Reconocer alertas'),
  tenant('alerts.manage', 'Gestionar canales de notificación, sus credenciales y reglas'),
  tenant('reports.read', 'Ver y descargar reportes'),
  tenant('reports.export', 'Generar exportaciones'),
  tenant('dashboards.read', 'Ver dashboards compartidos y plantillas, crear privados'),
  tenant('dashboards.manage', 'Dashboards y playlists compartidos con el ISP'),
  tenant('kiosks.manage', 'Alta, códigos de enrolamiento y revocación de kioscos'),
  tenant('settings.read', 'Ver configuración del ISP'),
  tenant('settings.manage', 'Configuración del ISP (inactividad, reglas de tipo, retenciones)'),
  tenant('api_tokens.manage', 'API tokens del ISP'),
  tenant(
    'sso.issue',
    'Emitir tickets de autologin hacia Horus (solo API tokens; plantilla ArgosISP)',
  ),
  platform('platform.tenants.read', 'Ver ISP de la instalación'),
  platform('platform.tenants.manage', 'Alta, edición, suspensión y baja de ISP'),
  platform('platform.users.read', 'Ver usuarios y membresías'),
  platform('platform.users.manage', 'Bloquear usuarios, roles de plataforma, sesiones'),
  platform('platform.audit.read', 'Auditoría de plataforma'),
  platform('platform.status.read', 'Estado detallado de la instalación, hubs WireGuard'),
  platform('platform.storage.manage', 'Destinos remotos de copias'),
  platform(
    'platform.wireguard.manage',
    'Hub WireGuard: endpoint, MTU, keepalive y rangos de túnel',
  ),
  platform('platform.catalog.manage', 'Catálogo de clasificación de tráfico'),
  platform('platform.reputation_sources.manage', 'Fuentes de reputación'),
]

const TENANT_PERMISSION_KEYS = new Set(
  PERMISSION_CATALOG.filter((p) => p.family === 'tenant').map((p) => p.key),
)

/** Permisos de los roles de sistema de tenant (C7, permissions.yaml). */
export const ROLE_PERMISSIONS: Record<string, string[]> = {
  tenant_admin: TENANT_ADMIN,
  security_analyst: SECURITY_ANALYST,
  network_engineer: [
    'sites.read',
    'sites.create',
    'sites.update',
    'sites.delete',
    'devices.read',
    'devices.create',
    'devices.update',
    'devices.delete',
    'devices.credentials.write',
    'wireguard.read',
    'wireguard.write',
    'wireguard.keys.rotate',
    'snmp.read',
    'snmp.manage',
    'flows.read',
    'flows.manage',
    'alerts.read',
    'alerts.ack',
    'traffic.read',
    'dashboards.read',
    'dashboards.manage',
  ],
  noc: NOC,
  analyst: [
    'traffic.read',
    'traffic.customer.read',
    'customers.read',
    'customers.update',
    'customers.kind.write',
    'reports.read',
    'reports.export',
    'devices.read',
    'sites.read',
    'dashboards.read',
    'dashboards.manage',
  ],
  auditor: ['audit.read', 'audit.export', 'users.read', 'roles.read'],
  viewer: [
    'users.read',
    'roles.read',
    'sites.read',
    'devices.read',
    'wireguard.read',
    'snmp.read',
    'flows.read',
    'traffic.read',
    'traffic.catalog.read',
    'security.findings.read',
    'alerts.read',
    'reports.read',
    'dashboards.read',
    'settings.read',
  ],
}

const ROLE_INFO: Record<string, { name: string; mfa: boolean; description: string }> = {
  tenant_admin: {
    name: 'Administrador del ISP',
    mfa: true,
    description: 'Todos los permisos del ISP, incluida la administración de usuarios y roles.',
  },
  security_analyst: {
    name: 'Analista de seguridad',
    mfa: true,
    description: 'Hallazgos, evidencias y alertas de seguridad.',
  },
  network_engineer: {
    name: 'Ingeniero de red',
    mfa: true,
    description: 'Nodos, routers, túneles y exportadores.',
  },
  noc: {
    name: 'Operador NOC',
    mfa: false,
    description: 'Vigilancia diaria de la red, sin datos de clientes.',
  },
  analyst: { name: 'Analista', mfa: false, description: 'Tráfico, clientes y reportes.' },
  auditor: { name: 'Auditor', mfa: false, description: 'Auditoría, miembros y roles.' },
  viewer: { name: 'Solo lectura', mfa: false, description: 'Ve casi todo, sin datos personales.' },
}

export const ROLES: Role[] = Object.entries(ROLE_IDS).map(([key, id]) => ({
  id,
  tenant_id: null,
  key,
  name: ROLE_INFO[key]!.name,
  description: ROLE_INFO[key]!.description,
  is_system: true,
  requires_mfa: ROLE_INFO[key]!.mfa,
  permissions: key === 'tenant_admin' ? ['*'] : ROLE_PERMISSIONS[key]!,
}))

// --- Roles propios (persistidos en la sesión simulada) ---------------------------------------

const STORAGE_KEY = 'horus.mock.roles'
type Storage = Pick<globalThis.Storage, 'getItem' | 'setItem'> | null | undefined
let storage: Storage

/** Recupera los roles propios guardados en esta sesión simulada (al recargar). */
export function restoreRoles(s: Storage) {
  storage = s
  try {
    const saved = JSON.parse(s?.getItem(STORAGE_KEY) ?? '{}') as Record<string, Role[]>
    for (const [tenantId, roles] of Object.entries(saved)) {
      if (Array.isArray(roles) && !mockState.customRoles.has(tenantId)) {
        mockState.customRoles.set(tenantId, roles)
      }
    }
  } catch {
    // estado guardado ilegible: se ignora
  }
}

function save() {
  try {
    storage?.setItem(STORAGE_KEY, JSON.stringify(Object.fromEntries(mockState.customRoles)))
  } catch {
    // sin storage: solo en memoria
  }
}

export function customRolesOf(tenantId: string) {
  let list = mockState.customRoles.get(tenantId)
  if (!list) {
    list = []
    mockState.customRoles.set(tenantId, list)
  }
  return list
}

/** Roles visibles en un ISP: los de sistema y los propios. */
export function rolesOf(tenantId: string | undefined): Role[] {
  return tenantId ? [...ROLES, ...customRolesOf(tenantId)] : ROLES
}

export function findRole(tenantId: string | undefined, roleId: string) {
  return rolesOf(tenantId).find((r) => r.id === roleId)
}

/** Permisos efectivos (sin `*`) de un rol. */
export function permissionsOfRole(role: Role | undefined) {
  if (!role) return []
  if (role.key === 'tenant_admin') return TENANT_ADMIN
  return role.is_system ? (ROLE_PERMISSIONS[role.key] ?? []) : role.permissions
}

export function withKeys(tenantId: string | undefined, assignments: RoleAssignment[]) {
  return assignments.map((a) => ({
    role_id: a.role_id,
    role_key: findRole(tenantId, a.role_id)?.key,
    scope: a.scope,
  }))
}

// --- Rutas ---------------------------------------------------------------------------------

export interface RoleRouteContext {
  req: Request
  path: string
  method: string
  body: unknown
  tenant: MockTenant
  can: (permission: string) => boolean
  /** Permisos que quien llama tiene en todo el ISP. */
  tenantWide: (permission: string) => boolean
  /** Asignaciones activas de un rol en el ISP. */
  assignments: (roleId: string) => number
}

function validation(errors: { field: string; message: string; code?: string }[]) {
  return problem(
    422,
    'VALIDATION_FAILED',
    'Revisa los campos marcados',
    {},
    { errors: errors.map((e) => ({ code: 'INVALID', ...e })) },
  )
}

const KEY_RE = /^[a-z][a-z0-9_]{1,62}$/

function readPermissions(value: unknown) {
  if (!Array.isArray(value) || value.length === 0) {
    return validation([
      { field: 'permissions', code: 'REQUIRED', message: 'Elige al menos un permiso' },
    ])
  }
  const perms = [...new Set(value.map(String))]
  const unknown = perms.find((p) => !TENANT_PERMISSION_KEYS.has(p))
  if (unknown) {
    return validation([{ field: 'permissions', message: `Permiso desconocido: ${unknown}` }])
  }
  return perms
}

function readDescription(value: unknown) {
  if (value === undefined) return undefined
  if (value === null) return null
  const text = String(value).trim()
  return text ? text.slice(0, 500) : null
}

function present(role: Role, ctx: RoleRouteContext): Role {
  return { ...role, assignments_count: ctx.assignments(role.id) }
}

export function roleRoute(ctx: RoleRouteContext): Response | undefined {
  if (ctx.path === '/roles' && ctx.method === 'GET') {
    if (!ctx.can('roles.read')) return forbidden()
    return json({
      data: rolesOf(ctx.tenant.tenant_id).map((r) => present(r, ctx)),
      page: { limit: 50, has_more: false, next_cursor: null, prev_cursor: null },
    })
  }
  const list = customRolesOf(ctx.tenant.tenant_id)

  if (ctx.path === '/roles' && ctx.method === 'POST') {
    if (!ctx.can('roles.manage')) return forbidden()
    const body = (ctx.body ?? {}) as Record<string, unknown>
    const key = String(body.key ?? '').trim()
    const name = String(body.name ?? '').trim()
    const errors: { field: string; message: string }[] = []
    if (!KEY_RE.test(key)) {
      errors.push({ field: 'key', message: 'Minúsculas, números y _; empieza por una letra' })
    }
    if (!name || name.length > 120) errors.push({ field: 'name', message: 'Escribe un nombre' })
    if (errors.length) return validation(errors)
    const perms = readPermissions(body.permissions)
    if (perms instanceof Response) return perms
    if (rolesOf(ctx.tenant.tenant_id).some((r) => r.key === key)) {
      return problem(
        409,
        'CONFLICT',
        'Ya existe un rol con esa clave',
        {},
        {
          errors: [{ field: 'key', code: 'DUPLICATE', message: 'Ya existe un rol con esa clave' }],
        },
      )
    }
    if (perms.some((p) => !ctx.tenantWide(p))) {
      return problem(403, 'PERMISSION_DENIED', 'No puedes conceder permisos que no tienes')
    }
    const role: Role = {
      id: mockUuid('0192a100', hash(`${ctx.tenant.tenant_id}:${key}`)),
      tenant_id: ctx.tenant.tenant_id,
      key,
      name,
      description: readDescription(body.description) ?? null,
      is_system: false,
      requires_mfa: false,
      permissions: perms,
      version: 1,
    }
    list.push(role)
    save()
    return json(present(role, ctx), 201, {
      ETag: '"1"',
      Location: `/api/v1/roles/${role.id}`,
    })
  }

  const m = ctx.path.match(/^\/roles\/([^/]+)$/)
  if (!m) return
  if (!ctx.can('roles.manage')) return forbidden()
  const system = ROLES.find((r) => r.id === m[1])
  if (system && (ctx.method === 'PATCH' || ctx.method === 'DELETE')) {
    return problem(422, 'ROLE_READ_ONLY', 'Los roles de sistema no se pueden modificar')
  }
  const role = list.find((r) => r.id === m[1])
  if (!role) return notFound('ROLE_NOT_FOUND')
  const expected = ifMatch(ctx.req)
  if (expected === null) return problem(428, 'PRECONDITION_REQUIRED', 'Falta If-Match')

  if (ctx.method === 'PATCH') {
    const conflictKey = role.id
    if (readRoleConflict() && !mockState.roleConflictServed.has(conflictKey)) {
      // Otra persona editó el rol justo antes: su versión sube.
      mockState.roleConflictServed.add(conflictKey)
      role.version = (role.version ?? 1) + 1
      role.description = 'Actualizado por otro administrador'
      save()
    }
    if (expected !== role.version) {
      return problem(
        412,
        'PRECONDITION_FAILED',
        'El rol cambió mientras lo editabas',
        {},
        { current: present(role, ctx) as unknown as Record<string, unknown> },
      )
    }
    const body = (ctx.body ?? {}) as Record<string, unknown>
    const patch: Partial<Role> = {}
    if ('name' in body) {
      const name = String(body.name ?? '').trim()
      if (!name || name.length > 120) {
        return validation([{ field: 'name', message: 'Escribe un nombre' }])
      }
      patch.name = name
    }
    if ('description' in body) patch.description = readDescription(body.description) ?? null
    if ('permissions' in body) {
      const perms = readPermissions(body.permissions)
      if (perms instanceof Response) return perms
      // Anti-escalada: lo que se añade, solo si quien edita lo tiene en todo el ISP.
      if (perms.some((p) => !role.permissions.includes(p) && !ctx.tenantWide(p))) {
        return problem(403, 'PERMISSION_DENIED', 'No puedes conceder permisos que no tienes')
      }
      patch.permissions = perms
    }
    if (!Object.keys(patch).length) {
      return validation([{ field: '_', message: 'No hay nada que cambiar' }])
    }
    Object.assign(role, patch, { version: (role.version ?? 1) + 1 })
    save()
    return json(present(role, ctx), 200, { ETag: `"${role.version}"` })
  }

  if (ctx.method === 'DELETE') {
    if (expected !== role.version) {
      return problem(
        412,
        'PRECONDITION_FAILED',
        'El rol cambió mientras lo editabas',
        {},
        { current: present(role, ctx) as unknown as Record<string, unknown> },
      )
    }
    const count = ctx.assignments(role.id)
    if (count > 0) {
      return problem(409, 'ROLE_IN_USE', `El rol está asignado a ${count} miembros`)
    }
    list.splice(list.indexOf(role), 1)
    save()
    return noContent()
  }
  return notFound()
}

function readRoleConflict() {
  try {
    return globalThis.localStorage?.getItem('horus.mock.roleConflict') === '1'
  } catch {
    return false
  }
}

/** Membresía con los permisos efectivos de sus roles (sistema o propios). */
export function effectivePermissions(tenantId: string, roles: RoleAssignment[]) {
  return [...new Set(roles.flatMap((r) => permissionsOfRole(findRole(tenantId, r.role_id))))]
}
