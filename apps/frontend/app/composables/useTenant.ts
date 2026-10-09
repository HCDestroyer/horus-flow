import type { Increment } from '~/utils/navigation'

/**
 * ISP actual (frontend.md §3). El slug sale de la URL; la membresía, de `GET /me`; el token
 * del ISP lo activa el middleware `tenant.global` (`POST /auth/token`).
 */
export function useTenant() {
  const route = useRoute()
  const { me } = useAuth()
  const config = useRuntimeConfig()

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
    const target = tenantSwitchPath(
      route.path,
      toSlug,
      me.value,
      config.public.navIncrement as Increment,
    )
    await navigateTo(target)
  }

  return {
    slug: computed(() => membership.value?.tenant_slug),
    membership,
    memberships,
    switchTo,
  }
}

/** Navegación visible para el usuario en el ISP actual. */
export function useNavigation() {
  const { me } = useAuth()
  const { slug } = useTenant()
  const config = useRuntimeConfig()
  const max = computed(() => config.public.navIncrement as Increment)
  return computed(() => buildNavigation(me.value, slug.value, max.value))
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
