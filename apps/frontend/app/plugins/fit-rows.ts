import type { Directive } from 'vue'

/**
 * `v-fit-rows`: oculta las filas (`[data-fit-item]`) que no caben enteras en el contenedor,
 * en vez de mostrarlas cortadas (tablas y listas murales: "≤ 8 filas, una sola línea por
 * fila", frontend.md §7.4). Se recalcula al cambiar el tamaño o los datos.
 */
function fit(el: HTMLElement) {
  const limit = el.clientHeight
  const top = el.getBoundingClientRect().top
  let hiding = false
  for (const item of el.querySelectorAll<HTMLElement>('[data-fit-item]')) {
    item.style.visibility = ''
    if (!hiding) {
      const bottom = item.getBoundingClientRect().bottom - top
      hiding = bottom > limit + 0.5
    }
    if (hiding) item.style.visibility = 'hidden'
  }
}

const observers = new WeakMap<HTMLElement, ResizeObserver>()

const fitRows: Directive<HTMLElement> = {
  mounted(el) {
    const observer = new ResizeObserver(() => fit(el))
    observer.observe(el)
    observers.set(el, observer)
    requestAnimationFrame(() => fit(el))
  },
  updated(el) {
    requestAnimationFrame(() => fit(el))
  },
  unmounted(el) {
    observers.get(el)?.disconnect()
    observers.delete(el)
  },
}

declare module 'vue' {
  interface GlobalDirectives {
    vFitRows: Directive<HTMLElement>
  }
}

export default defineNuxtPlugin((nuxtApp) => {
  nuxtApp.vueApp.directive('fit-rows', fitRows)
})
