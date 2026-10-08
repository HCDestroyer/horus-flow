import type { RealtimeHandler } from '~/utils/realtime'

/**
 * `useRealtime(topic, handler)` (frontend.md §10.5): suscribe el componente a un tema del ISP
 * actual mientras está montado; se re-suscribe si cambia el ISP y nunca recibe mensajes de
 * otro. `topic` nulo = sin tiempo real.
 */
export function useRealtime(
  topic: MaybeRefOrGetter<string | null | undefined>,
  handler: RealtimeHandler,
) {
  const { $realtime } = useNuxtApp()
  const { activeScope } = useAuth()
  let stop: (() => void) | null = null

  function subscribe() {
    stop?.()
    stop = null
    const t = toValue(topic)
    const scope = activeScope.value
    if (!t || scope?.kind !== 'tenant') return
    stop = $realtime.subscribe(scope.tenantId, t, handler)
  }

  onMounted(subscribe)
  watch([() => toValue(topic), activeScope], subscribe)
  onBeforeUnmount(() => stop?.())
}
