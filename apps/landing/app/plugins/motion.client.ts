// Movimiento con JavaScript sobre el HTML del servidor (contadores, bucles con pausa, "Ver de
// nuevo") y nueva búsqueda de elementos [data-reveal] tras navegar. Ver app/utils/motion-enhance.ts
// y app/utils/reveal-script.ts.
import { enhanceMotion } from '~/utils/motion-enhance'

export default defineNuxtPlugin((nuxtApp) => {
  const run = () => {
    requestAnimationFrame(() => enhanceMotion())
  }
  nuxtApp.hook('app:mounted', run)
  nuxtApp.hook('page:finish', run)
})
