import type { Site } from '~~/types/api'

/**
 * Nodos del ISP actual (para filtros, nombres y enlaces). Una sola consulta por ISP: la
 * comparten todas las páginas que la usan (misma clave de `useAsyncData`).
 */
export function useSites() {
  const { $api } = useNuxtApp()
  const query = useTenantQuery('sites', () =>
    unwrap($api.GET('/sites', { params: { query: { limit: 200 } } })).then((r) => r.data),
  )
  const byId = computed(() => new Map((query.data.value ?? []).map((s: Site) => [s.id, s])))
  const siteName = (id: string | null | undefined) => (id ? (byId.value.get(id)?.name ?? '—') : '—')
  return { ...query, sites: query.data, byId, siteName }
}

/** Opciones de nodo para un `USelect` ("Todos los nodos" + cada nodo). */
export function useSiteOptions() {
  const { t } = useI18n()
  const { sites } = useSites()
  return computed(() => [
    { label: t('common.allSites'), value: 'all' },
    ...(sites.value ?? []).map((s) => ({ label: s.name, value: s.id })),
  ])
}
