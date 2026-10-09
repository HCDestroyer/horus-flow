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

/**
 * `v-fit-optional`: degradación ordenada de un widget que no cabe (frontend.md §7.4,
 * "ocultar lo secundario antes que cortar"). Mientras el contenedor desborde (su contenido
 * ocupa más que su caja), oculta con `display: none` los elementos `[data-fit-optional="N"]`
 * de menor a mayor N (1 = lo primero que sobra). Si cabe todo, no oculta nada; con filas
 * de altura automática (móvil) nunca desborda y se ve todo. Se recalcula al cambiar el
 * tamaño o el contenido (datos en vivo, reloj).
 */
const OPTIONAL = '[data-fit-optional]'

function overflows(el: HTMLElement) {
  return el.scrollHeight > el.clientHeight + 1 || el.scrollWidth > el.clientWidth + 1
}

function fitOptional(el: HTMLElement) {
  const items = [...el.querySelectorAll<HTMLElement>(OPTIONAL)]
  if (!items.length) return
  for (const item of items) item.style.removeProperty('display')
  items.sort((a, b) => Number(a.dataset.fitOptional) - Number(b.dataset.fitOptional))
  for (const item of items) {
    if (!overflows(el)) break
    item.style.setProperty('display', 'none')
  }
}

interface FitWatch {
  resize: ResizeObserver
  mutation: MutationObserver
  frame: number
}
const watches = new WeakMap<HTMLElement, FitWatch>()

function schedule(el: HTMLElement) {
  const w = watches.get(el)
  if (!w || w.frame) return
  w.frame = requestAnimationFrame(() => {
    w.frame = 0
    fitOptional(el)
  })
}

const fitOptionalDirective: Directive<HTMLElement> = {
  mounted(el) {
    const resize = new ResizeObserver(() => schedule(el))
    // Solo texto y nodos: los cambios de `style` que hace el propio ajuste no lo relanzan.
    const mutation = new MutationObserver(() => schedule(el))
    watches.set(el, { resize, mutation, frame: 0 })
    resize.observe(el)
    mutation.observe(el, { childList: true, characterData: true, subtree: true })
    schedule(el)
  },
  updated(el) {
    schedule(el)
  },
  unmounted(el) {
    const w = watches.get(el)
    if (!w) return
    w.resize.disconnect()
    w.mutation.disconnect()
    cancelAnimationFrame(w.frame)
    watches.delete(el)
  },
}

declare module 'vue' {
  interface GlobalDirectives {
    vFitRows: Directive<HTMLElement>
    vFitOptional: Directive<HTMLElement>
  }
}

export default defineNuxtPlugin((nuxtApp) => {
  nuxtApp.vueApp.directive('fit-rows', fitRows)
  nuxtApp.vueApp.directive('fit-optional', fitOptionalDirective)
})
