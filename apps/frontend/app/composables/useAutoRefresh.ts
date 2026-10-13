/**
 * Vuelve a cargar una vista cada `everyMs` mientras la pestaña está visible, y en cuanto vuelve
 * a estarlo tras un rato oculta (frontend.md §16: una ficha abierta no se queda con datos de
 * hace una hora). Se detiene al desmontar.
 */
export function useAutoRefresh(refresh: () => unknown, everyMs = 60_000) {
  let timer: ReturnType<typeof setInterval> | undefined
  let hiddenSince: number | null = null

  const start = () => {
    stop()
    if (import.meta.server) return
    timer = setInterval(() => {
      if (document.visibilityState === 'visible') void refresh()
    }, everyMs)
  }
  const stop = () => {
    if (timer) clearInterval(timer)
    timer = undefined
  }
  const onVisibility = () => {
    if (document.visibilityState === 'hidden') {
      hiddenSince = Date.now()
      return
    }
    if (hiddenSince !== null && Date.now() - hiddenSince >= everyMs / 2) void refresh()
    hiddenSince = null
  }

  onMounted(() => {
    start()
    document.addEventListener('visibilitychange', onVisibility)
  })
  onBeforeUnmount(() => {
    stop()
    document.removeEventListener('visibilitychange', onVisibility)
  })
}
