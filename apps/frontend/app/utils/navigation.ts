import type { Me } from '~~/types/api'
import { findMembership, hasPlatformPermission, hasTenantPermission } from './permissions'

/**
 * Mapa de navegación (frontend.md §4). Fuente única para la barra lateral, la búsqueda
 * (⌘K) y las páginas de sección.
 *
 * - Aparición progresiva: cada sección declara su incremento y solo entra si
 *   `increment <= navIncrement` (`NUXT_PUBLIC_NAV_INCREMENT`, por defecto **I1** en las
 *   builds de entrega y demo). Una sección de un incremento posterior no aparece en la barra
 *   lateral, ni en ⌘K, ni en el Resumen, y su URL directa da "No encontrado" (404), igual que
 *   una ruta que no existe.
 * - Sin permiso de lectura, la sección se OCULTA, no se deshabilita (§12, criterio 4 de I0-14).
 */

export type Increment = 'I0' | 'I1' | 'I2' | 'I3'

export type NavGroupId = 'main' | 'operation' | 'security' | 'management' | 'admin' | 'platform'

export interface NavSection {
  id: string
  group: NavGroupId
  /** Clave i18n de la etiqueta (`nav.items.<id>`) y de la descripción (`sections.<id>.*`). */
  labelKey: string
  icon: string
  /** Ruta relativa: dentro del ISP (`/t/:slug/<path>`) o absoluta de plataforma. */
  path: string
  scope: 'tenant' | 'platform'
  increment: Increment
  /** Permiso de lectura de tenant o de plataforma (security.md §6.1). */
  permission?: string
}

export const NAV_GROUPS: { id: NavGroupId; labelKey?: string }[] = [
  { id: 'main' },
  { id: 'operation', labelKey: 'nav.groups.operation' },
  { id: 'security', labelKey: 'nav.groups.security' },
  { id: 'management', labelKey: 'nav.groups.management' },
  { id: 'admin', labelKey: 'nav.groups.admin' },
  { id: 'platform', labelKey: 'nav.groups.platform' },
]

// prettier-ignore
export const NAV_SECTIONS: NavSection[] = [
  { id: 'overview', group: 'main', labelKey: 'nav.items.overview', icon: 'i-lucide-activity', path: '', scope: 'tenant', increment: 'I0' },
  { id: 'dashboards', group: 'main', labelKey: 'nav.items.dashboards', icon: 'i-lucide-layout-dashboard', path: 'dashboards', scope: 'tenant', increment: 'I1', permission: 'dashboards.read' },
  { id: 'nodes', group: 'operation', labelKey: 'nav.items.nodes', icon: 'i-lucide-router', path: 'nodes', scope: 'tenant', increment: 'I0', permission: 'devices.read' },
  { id: 'clients', group: 'operation', labelKey: 'nav.items.clients', icon: 'i-lucide-users', path: 'clients', scope: 'tenant', increment: 'I1', permission: 'customers.read' },
  { id: 'traffic', group: 'operation', labelKey: 'nav.items.traffic', icon: 'i-lucide-arrow-down-up', path: 'traffic', scope: 'tenant', increment: 'I1', permission: 'traffic.read' },
  { id: 'findings', group: 'security', labelKey: 'nav.items.findings', icon: 'i-lucide-shield-alert', path: 'security/findings', scope: 'tenant', increment: 'I1', permission: 'security.findings.read' },
  { id: 'investigate', group: 'security', labelKey: 'nav.items.investigate', icon: 'i-lucide-search-check', path: 'security/investigate', scope: 'tenant', increment: 'I2', permission: 'security.findings.read' },
  { id: 'alerts', group: 'management', labelKey: 'nav.items.alerts', icon: 'i-lucide-bell', path: 'alerts', scope: 'tenant', increment: 'I3', permission: 'alerts.read' },
  { id: 'reports', group: 'management', labelKey: 'nav.items.reports', icon: 'i-lucide-file-chart-column', path: 'reports', scope: 'tenant', increment: 'I3', permission: 'reports.read' },
  { id: 'users', group: 'admin', labelKey: 'nav.items.users', icon: 'i-lucide-user-cog', path: 'admin/users', scope: 'tenant', increment: 'I0', permission: 'users.read' },
  { id: 'kiosks', group: 'admin', labelKey: 'nav.items.kiosks', icon: 'i-lucide-monitor', path: 'admin/kiosks', scope: 'tenant', increment: 'I1', permission: 'kiosks.manage' },
  { id: 'notifications', group: 'admin', labelKey: 'nav.items.notifications', icon: 'i-lucide-send', path: 'admin/notifications', scope: 'tenant', increment: 'I1', permission: 'alerts.read' },
  { id: 'detection', group: 'admin', labelKey: 'nav.items.detection', icon: 'i-lucide-scan-search', path: 'admin/detection', scope: 'tenant', increment: 'I1', permission: 'settings.read' },
  { id: 'audit', group: 'admin', labelKey: 'nav.items.audit', icon: 'i-lucide-scroll-text', path: 'admin/audit', scope: 'tenant', increment: 'I2', permission: 'audit.read' },
  { id: 'platform-isps', group: 'platform', labelKey: 'nav.items.platformIsps', icon: 'i-lucide-building-2', path: '/platform/isps', scope: 'platform', increment: 'I0', permission: 'platform.tenants.read' },
  { id: 'platform-users', group: 'platform', labelKey: 'nav.items.platformUsers', icon: 'i-lucide-users-round', path: '/platform/users', scope: 'platform', increment: 'I0', permission: 'platform.users.read' },
  { id: 'platform-wireguard', group: 'platform', labelKey: 'nav.items.platformWireguard', icon: 'i-lucide-waypoints', path: '/platform/wireguard', scope: 'platform', increment: 'I1', permission: 'platform.status.read' },
  { id: 'platform-storage', group: 'platform', labelKey: 'nav.items.platformStorage', icon: 'i-lucide-hard-drive', path: '/platform/storage', scope: 'platform', increment: 'I1', permission: 'platform.storage.manage' },
  { id: 'platform-reputation', group: 'platform', labelKey: 'nav.items.platformReputation', icon: 'i-lucide-list-checks', path: '/platform/reputation', scope: 'platform', increment: 'I1', permission: 'platform.reputation_sources.manage' },
  { id: 'platform-system', group: 'platform', labelKey: 'nav.items.platformSystem', icon: 'i-lucide-heart-pulse', path: '/platform/system', scope: 'platform', increment: 'I1', permission: 'platform.status.read' },
]

const INCREMENT_ORDER: Increment[] = ['I0', 'I1', 'I2', 'I3']

/** Incremento visible si la configuración no dice otro (o dice uno que no existe). */
export const DEFAULT_NAV_INCREMENT: Increment = 'I1'

/** Normaliza `navIncrement` ("i2", " I2 ") y cae en el valor por defecto si no es válido. */
export function parseIncrement(value: unknown, fallback: Increment = DEFAULT_NAV_INCREMENT) {
  const v = String(value ?? '')
    .trim()
    .toUpperCase()
  return (INCREMENT_ORDER as string[]).includes(v) ? (v as Increment) : fallback
}

export function isIncrementVisible(section: Increment, max: Increment) {
  return INCREMENT_ORDER.indexOf(section) <= INCREMENT_ORDER.indexOf(max)
}

/**
 * Acceso a la URL de una sección: `not-found` si no existe o es de un incremento posterior
 * al visible (misma respuesta que una ruta inexistente: no se revela qué llegará),
 * `forbidden` sin permiso de lectura, `ok` en otro caso.
 */
export function sectionAccess(
  section: NavSection | undefined,
  me: Me | null | undefined,
  slug: string | undefined,
  maxIncrement: Increment,
): 'ok' | 'not-found' | 'forbidden' {
  if (!section || !isIncrementVisible(section.increment, maxIncrement)) return 'not-found'
  if (canSeeSection(section, me, slug)) return 'ok'
  // La consola de plataforma no se revela a quien no es superadmin (I1-31 criterio 3).
  return section.scope === 'platform' ? 'not-found' : 'forbidden'
}

export function sectionHref(section: NavSection, slug?: string) {
  if (section.scope === 'platform') return section.path
  return section.path ? `/t/${slug}/${section.path}` : `/t/${slug}`
}

export function canSeeSection(section: NavSection, me: Me | null | undefined, slug?: string) {
  if (!me) return false
  if (section.scope === 'platform') {
    return section.permission ? hasPlatformPermission(me, section.permission) : false
  }
  const membership = findMembership(me, slug)
  if (!membership) return false
  return section.permission ? hasTenantPermission(membership, section.permission) : true
}

export interface VisibleNavGroup {
  id: NavGroupId
  labelKey?: string
  sections: (NavSection & { href: string })[]
}

/** Grupos visibles para el usuario en el ISP actual; los grupos vacíos desaparecen. */
export function buildNavigation(
  me: Me | null | undefined,
  slug: string | undefined,
  maxIncrement: Increment,
): VisibleNavGroup[] {
  return NAV_GROUPS.map((group) => ({
    ...group,
    sections: NAV_SECTIONS.filter(
      (s) =>
        s.group === group.id &&
        isIncrementVisible(s.increment, maxIncrement) &&
        canSeeSection(s, me, slug),
    ).map((s) => ({ ...s, href: sectionHref(s, slug) })),
  })).filter((group) => group.sections.length > 0)
}

/** Sección a partir del path relativo dentro del ISP o del path absoluto de plataforma. */
export function findSection(scope: NavSection['scope'], path: string) {
  return NAV_SECTIONS.find((s) => s.scope === scope && s.path === path)
}

/**
 * Destino al cambiar de ISP (frontend.md §3.2): se conserva la sección si existe y es visible
 * en el ISP nuevo ("Hallazgos" de A → "Hallazgos" de B); los detalles (IDs de A) y los
 * filtros (query) se descartan; si la sección no existe allí, se va al inicio del ISP.
 */
export function tenantSwitchPath(
  currentPath: string,
  toSlug: string,
  me: Me | null | undefined,
  maxIncrement: Increment,
) {
  const match = currentPath.match(/^\/t\/[^/]+\/?([^?#]*)/)
  const rest = (match?.[1] ?? '').replace(/\/$/, '')
  if (!rest) return `/t/${toSlug}`
  // La sección más específica cuyo path es prefijo de la ruta actual.
  const section = NAV_SECTIONS.filter(
    (s) => s.scope === 'tenant' && s.path && (rest === s.path || rest.startsWith(`${s.path}/`)),
  ).sort((a, b) => b.path.length - a.path.length)[0]
  if (
    section &&
    isIncrementVisible(section.increment, maxIncrement) &&
    canSeeSection(section, me, toSlug)
  ) {
    return sectionHref(section, toSlug)
  }
  return `/t/${toSlug}`
}
