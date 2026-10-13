import type { RouterLiveMetrics } from '~~/types/api'

/**
 * Última muestra de un router (`GET /routers/{id}/metrics/live`) mantenida al día por el tema
 * `router.{id}.metrics` (snapshot al suscribirse y cada sondeo) y con un refresco de respaldo
 * cada minuto. `enabled` = el usuario tiene `snmp.read`.
 */
export function useRouterLive(
  id: MaybeRefOrGetter<string>,
  enabled: MaybeRefOrGetter<boolean> = true,
) {
  const { $api } = useNuxtApp()
  const query = useTenantQuery(
    () => `router:${toValue(id)}:live`,
    () =>
      toValue(enabled)
        ? unwrap(
            $api.GET('/routers/{router_id}/metrics/live', {
              params: { path: { router_id: toValue(id) } },
            }),
          )
        : Promise.resolve(null),
    { watch: [() => toValue(id)] },
  )
  const live = shallowRef<RouterLiveMetrics | null>(null)
  const liveAt = ref<number | null>(null)
  watch(
    query.data,
    (d) => {
      if (d) live.value = d
    },
    { immediate: true },
  )
  useRealtime(
    () => (toValue(enabled) ? `router.${toValue(id)}.metrics` : null),
    (m) => {
      if (m.type !== 'state') return
      live.value = m.data as unknown as RouterLiveMetrics
      liveAt.value = Date.now()
    },
  )
  useAutoRefresh(() => query.refresh(), 60_000)
  return { live, liveAt, error: query.error, status: query.status, refresh: query.refresh }
}
