/**
 * Preferencias del sistema que cambian la luz y el movimiento (frontend.md §13.1, §14):
 * movimiento reducido (sin animaciones de gráficos) y más contraste (sin halos).
 */
export function useMotionPrefs() {
  const reducedMotion = ref(false)
  const moreContrast = ref(false)
  onMounted(() => {
    if (!window.matchMedia) return
    const rm = window.matchMedia('(prefers-reduced-motion: reduce)')
    const mc = window.matchMedia('(prefers-contrast: more)')
    const sync = () => {
      reducedMotion.value = rm.matches
      moreContrast.value = mc.matches
    }
    sync()
    rm.addEventListener?.('change', sync)
    mc.addEventListener?.('change', sync)
    onBeforeUnmount(() => {
      rm.removeEventListener?.('change', sync)
      mc.removeEventListener?.('change', sync)
    })
  })
  return { reducedMotion, moreContrast }
}
