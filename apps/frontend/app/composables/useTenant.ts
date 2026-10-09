import type { Increment, NavSection } from '~/utils/navigation'

/**
 * ISP actual (frontend.md §3). El slug sale de la URL; la membresía, de `GET /me`; el token
 * del ISP lo activa el middleware `tenant.global` (`POST /auth/token`).
 */
export function useTenant() {
  const route = useRoute()
  const { me } = useAuth()
  const navIncrement = useNavIncrement()

  const slug = computed(() =>
    typeof route.params.slug === 'string' ? route.params.slug : (lastTenant() ?? undefined),
  )
  const membership = computed(
    () => findMembership(me.value, slug.value) ?? pickDefaultTenant(me.value, lastTenant()),
  )
  /** ISP del usuario en orden alfabético (selector, ⌘K). */
  const memberships = computed(() =>
    [...(me.value?.memberships ?? [])].sort((a, b) =>
      a.tenant_name.localeCompare(b.tenant_name, 'es'),
    ),
  )

  /** Cambia de ISP conservando la sección si existe allí (§3.2). */
  async function switchTo(toSlug: string) {
    if (toSlug === membership.value?.tenant_slug) return
    const target = tenantSwitchPath(route.path, toSlug, me.value, navIncrement.value)
    await navigateTo(target)
  }

  return {
    slug: computed(() => membership.value?.tenant_slug),
    membership,
    memberships,
    switchTo,
  }
}

/** Incremento máximo visible (`NUXT_PUBLIC_NAV_INCREMENT`, por defecto I1), normalizado. */
export function useNavIncrement() {
  const config = useRuntimeConfig()
  return computed<Increment>(() => parseIncrement(config.public.navIncrement))
}

/** Navegación visible para el usuario en el ISP actual. */
export function useNavigation() {
  const { me } = useAuth()
  const { slug } = useTenant()
  const max = useNavIncrement()
  return computed(() => buildNavigation(me.value, slug.value, max.value))
}

/**
 * Guardia de las páginas de sección: la función devuelta corta la página si la sección no
 * está disponible: 404 "No encontrado" si no existe o es de un incremento posterior al
 * visible; 403 en contexto sin permiso (frontend.md §9.3). Se crea en `setup` y puede
 * llamarse después (al cambiar la ruta).
 */
export function useSectionGuard() {
  const { me } = useAuth()
  const max = useNavIncrement()
  return (section: NavSection | undefined, slug?: string) => {
    const access = sectionAccess(section, me.value, slug, max.value)
    if (access === 'not-found') throw createError({ statusCode: 404, statusMessage: 'Not Found' })
    if (access === 'forbidden') throw createError({ statusCode: 403, statusMessage: 'Forbidden' })
  }
}

/**
 * Caché de datos del ISP actual (widgets, consultas): indexada por ISP y vaciada al
 * cambiar de ISP (frontend.md §3.2, §16). Solo en memoria.
 */
export function useTenantCache() {
  return useState<Record<string, unknown>>('tenant:cache', () => ({}))
}

/**
 * Descarta todo lo del ISP anterior: datos de `useAsyncData`, la caché por ISP y las
 * suscripciones de tiempo real (hook `horus:tenant-changed`, que escucha `realtime`).
 */
export async function resetTenantData(from: string | undefined, to: string | undefined) {
  const nuxtApp = useNuxtApp()
  clearNuxtData()
  useTenantCache().value = {}
  await nuxtApp.callHook('horus:tenant-changed', { from, to })
}
