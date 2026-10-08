/**
 * Dashboards del ISP actual (C9). Claves de `useAsyncData` con el ISP: al cambiar de ISP
 * `resetTenantData` las vacía y nunca se muestra un dashboard del anterior.
 */
export function useDashboardList() {
  const { $api } = useNuxtApp()
  const { activeScope } = useAuth()
  const key = computed(() => `dashboards:${scopeKey(activeScope.value)}`)
  return useAsyncData(key, () => unwrap($api.GET('/dashboards')).then((r) => r.data), {
    server: false,
  })
}

export function useDashboard(id: MaybeRefOrGetter<string>) {
  const { $api } = useNuxtApp()
  const { activeScope } = useAuth()
  const key = computed(() => `dashboard:${scopeKey(activeScope.value)}:${toValue(id)}`)
  return useAsyncData(
    key,
    () =>
      unwrap(
        $api.GET('/dashboards/{dashboard_id}', { params: { path: { dashboard_id: toValue(id) } } }),
      ),
    { server: false },
  )
}

function scopeKey(scope: TokenScope | undefined) {
  if (!scope) return 'none'
  return scope.kind === 'tenant' ? scope.tenantId : 'platform'
}
