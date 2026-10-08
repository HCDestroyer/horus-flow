import type { Me, Membership } from '~~/types/api'

/**
 * Permisos en la UI (frontend.md §12): la UI solo refleja la autorización; la aplica el
 * backend. Ocultar por permiso, deshabilitar por estado.
 */

export function findMembership(me: Me | null | undefined, slug: string | undefined) {
  if (!me || !slug) return undefined
  return me.memberships.find((m) => m.tenant_slug === slug)
}

/** ¿Tiene `permission` en el ISP de la membresía? Cualquier alcance cuenta para mostrar. */
export function hasTenantPermission(membership: Membership | undefined, permission: string) {
  const scopes = membership?.permissions_with_scope[permission]
  return Array.isArray(scopes) && scopes.length > 0
}

export function hasPlatformPermission(me: Me | null | undefined, permission: string) {
  return !!me?.platform_permissions.includes(permission)
}

export function isPlatformUser(me: Me | null | undefined) {
  return !!me && me.platform_roles.length > 0
}

/**
 * ISP inicial al entrar (frontend.md §3.2): el último usado si sigue siendo miembro; si no,
 * el primero alfabéticamente.
 */
export function pickDefaultTenant(me: Me | null | undefined, lastUsedSlug?: string | null) {
  if (!me || me.memberships.length === 0) return undefined
  const last = lastUsedSlug && me.memberships.find((m) => m.tenant_slug === lastUsedSlug)
  if (last) return last
  return [...me.memberships].sort((a, b) => a.tenant_name.localeCompare(b.tenant_name, 'es'))[0]
}
