import type { DashboardWidget, WidgetData } from '~~/types/api'

export type WidgetLoadStatus = 'idle' | 'loading' | 'ready' | 'error'

interface CachedWidgetData {
  data: WidgetData
  receivedAt: number
}

/**
 * Datos de UN widget (`GET /dashboards/{id}/widgets/{wid}/data`, C9), independientes de los
 * demás: un widget lento o roto no afecta al resto (api.md §2.11). Polling cada
 * `refreshSeconds`; las recargas en segundo plano no vuelven a mostrar esqueleto
 * (frontend.md §9.1) y, si fallan, se conservan los últimos datos. Caché indexada por ISP.
 */
export function useWidgetData(options: {
  dashboardId: string
  widget: DashboardWidget
  refreshSeconds: number
  /** `false` para tipos sin datos (`data_endpoint_kind: none`). */
  enabled: boolean
}) {
  const { $api } = useNuxtApp()
  const { activeScope } = useAuth()
  const cache = useTenantCache()

  const key = computed(() => {
    const scope = activeScope.value
    const tenant = scope?.kind === 'tenant' ? scope.tenantId : 'none'
    return `t:${tenant}:wd:${options.dashboardId}:${options.widget.id}`
  })

  const cached = cache.value[key.value] as CachedWidgetData | undefined
  const data = shallowRef<WidgetData | null>(cached?.data ?? null)
  const receivedAt = ref<number | null>(cached?.receivedAt ?? null)
  const error = shallowRef<unknown>(null)
  const status = ref<WidgetLoadStatus>(cached ? 'ready' : 'idle')
  let controller: AbortController | null = null
  let timer: ReturnType<typeof setInterval> | undefined

  async function load() {
    if (!options.enabled) return
    controller?.abort()
    controller = new AbortController()
    const requestKey = key.value
    if (!data.value) status.value = 'loading'
    try {
      const res = await unwrap(
        $api.GET('/dashboards/{dashboard_id}/widgets/{widget_id}/data', {
          params: { path: { dashboard_id: options.dashboardId, widget_id: options.widget.id } },
          signal: controller.signal,
        }),
      )
      // Respuesta de un ISP anterior (cambio en curso): se descarta.
      if (requestKey !== key.value) return
      data.value = res as WidgetData
      receivedAt.value = Date.now()
      error.value = null
      status.value = 'ready'
      cache.value[requestKey] = { data: res, receivedAt: receivedAt.value }
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return
      error.value = e
      status.value = 'error'
    }
  }

  function start() {
    stop()
    if (!options.enabled) return
    load()
    timer = setInterval(load, Math.max(5, options.refreshSeconds) * 1000)
  }

  function stop() {
    if (timer) clearInterval(timer)
    timer = undefined
    controller?.abort()
  }

  // Cambio de ISP: los datos del anterior desaparecen antes de pedir los del nuevo.
  watch(key, () => {
    data.value = null
    receivedAt.value = null
    error.value = null
    status.value = 'idle'
    start()
  })

  onMounted(start)
  onBeforeUnmount(stop)

  return { data, error, status, receivedAt, reload: load }
}
