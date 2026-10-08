import type { Increment } from '~/utils/navigation'

/**
 * ISP actual (frontend.md §3). El slug sale de la URL; la membresía, de `GET /me`.
 * El cambio de ISP, el token por ISP y el vaciado de cachés llegan en I0-15.
 */
export function useTenant() {
  const route = useRoute()
  const { me } = useAuth()

  const slug = computed(() =>
    typeof route.params.slug === 'string' ? route.params.slug : (lastTenant() ?? undefined),
  )
  const membership = computed(
    () => findMembership(me.value, slug.value) ?? pickDefaultTenant(me.value, lastTenant()),
  )

  return { slug: computed(() => membership.value?.tenant_slug), membership }
}

/** Navegación visible para el usuario en el ISP actual. */
export function useNavigation() {
  const { me } = useAuth()
  const { slug } = useTenant()
  const config = useRuntimeConfig()
  const max = computed(() => config.public.navIncrement as Increment)
  return computed(() => buildNavigation(me.value, slug.value, max.value))
}
