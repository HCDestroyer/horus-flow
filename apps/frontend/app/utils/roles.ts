import type { CatalogPermission, Role } from '~~/types/api'

/**
 * Roles y permisos del ISP (I1): clave de un rol propio a partir de su nombre, áreas de
 * permisos (prefijo de la clave) y permisos efectivos de un rol (`*` = todos).
 */

/** Patrón de la clave de un rol propio (RoleCreate.key). */
export const ROLE_KEY_PATTERN = /^[a-z][a-z0-9_]{1,62}$/

/**
 * Clave a partir del nombre: minúsculas, sin acentos, espacios y símbolos → `_`, empieza por
 * una letra y como mucho 63 caracteres ("Técnico de campo" → `tecnico_de_campo`).
 */
export function deriveRoleKey(name: string) {
  return name
    .normalize('NFD')
    .replace(/[̀-ͯ]/g, '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '_')
    .replace(/^[^a-z]+/, '')
    .slice(0, 63)
    .replace(/_+$/, '')
}

export function isValidRoleKey(key: string) {
  return ROLE_KEY_PATTERN.test(key)
}

/** Área de un permiso: el prefijo de su clave (`devices.credentials.write` → `devices`). */
export function permissionArea(key: string) {
  const dot = key.indexOf('.')
  return dot === -1 ? key : key.slice(0, dot)
}

export interface PermissionGroup<P extends Pick<CatalogPermission, 'key'>> {
  id: string
  permissions: P[]
}

/** Agrupa los permisos por área en el orden del catálogo (primera aparición de cada área). */
export function groupPermissions<P extends Pick<CatalogPermission, 'key'>>(
  permissions: readonly P[],
): PermissionGroup<P>[] {
  const groups = new Map<string, P[]>()
  for (const p of permissions) {
    const area = permissionArea(p.key)
    const list = groups.get(area)
    if (list) list.push(p)
    else groups.set(area, [p])
  }
  return [...groups].map(([id, list]) => ({ id, permissions: list }))
}

/** ¿Incluye el rol este permiso? (`*` en `tenant_admin` = todos los de ISP). */
export function roleHasPermission(role: Pick<Role, 'permissions'>, key: string) {
  return role.permissions.includes('*') || role.permissions.includes(key)
}

/** Permisos efectivos del rol entre los del catálogo, en el orden del catálogo. */
export function rolePermissionKeys(
  role: Pick<Role, 'permissions'>,
  catalog: readonly Pick<CatalogPermission, 'key'>[],
) {
  return catalog.map((p) => p.key).filter((key) => roleHasPermission(role, key))
}

/** Roles de sistema primero (orden del backend), después los propios por nombre. */
export function sortRoles<R extends Pick<Role, 'is_system' | 'name'>>(roles: readonly R[]) {
  const system = roles.filter((r) => r.is_system)
  const custom = roles
    .filter((r) => !r.is_system)
    .sort((a, b) => a.name.localeCompare(b.name, 'es'))
  return [...system, ...custom]
}
