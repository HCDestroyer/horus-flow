// Movimiento que necesita JavaScript, aplicado con DOM puro sobre el HTML del servidor (las
// secciones de la portada no se hidratan). Lo llama el plugin motion.client.ts al montar la
// app y tras cada navegación. Todo es opcional: sin JS el HTML ya muestra el estado final.
//
// - [data-count]: cuenta hasta el valor del HTML cuando entra en pantalla. Si ya estaba a la
//   vista al cargar, no se toca (no pasa de "10 000" a "0").
// - [data-cycle]: animaciones en bucle (salud de routers, rotación del kiosco). Solo corren a
//   la vista, con la pestaña visible y sin reduced motion; siempre con botón de pausa
//   (WCAG 2.2.2). Con reduced motion se quedan en su estado inicial, que es representativo.
// - [data-replay]: botón "Ver de nuevo" que reinicia la animación de su bloque [data-reveal].

const reduced = () => window.matchMedia('(prefers-reduced-motion: reduce)').matches

function inViewport(el: Element) {
  const r = el.getBoundingClientRect()
  return r.bottom > 0 && r.top < window.innerHeight
}

// ─── Contadores ─────────────────────────────────────────────────────────────────────────────

let countObserver: IntersectionObserver | undefined

function format(n: number, sep: string) {
  return String(Math.round(n)).replace(/\B(?=(\d{3})+(?!\d))/g, sep)
}

function runCount(el: HTMLElement) {
  const target = Number(el.dataset.count)
  const sep = el.dataset.countSep ?? ' '
  const final = el.textContent ?? ''
  if (!Number.isFinite(target) || target <= 0) return
  el.style.minWidth = `${el.getBoundingClientRect().width}px`
  const duration = 1200
  const start = performance.now()
  const tick = (now: number) => {
    const p = Math.min(1, (now - start) / duration)
    // Salida suave (ease-out cúbica): rápido al principio, se posa en el valor.
    const eased = 1 - Math.pow(1 - p, 3)
    el.textContent = p < 1 ? format(target * eased, sep) : final
    if (p < 1) requestAnimationFrame(tick)
  }
  el.textContent = format(0, sep)
  requestAnimationFrame(tick)
}

function enhanceCounters() {
  if (reduced()) return
  countObserver ??= new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        if (!e.isIntersecting) continue
        countObserver?.unobserve(e.target)
        runCount(e.target as HTMLElement)
      }
    },
    { rootMargin: '0px 0px -10% 0px' },
  )
  for (const el of document.querySelectorAll<HTMLElement>('[data-count]:not([data-count-ready])')) {
    el.dataset.countReady = '1'
    if (inViewport(el)) continue
    countObserver.observe(el)
  }
}

// ─── Bucles con pausa ───────────────────────────────────────────────────────────────────────

type Cycle = {
  root: HTMLElement
  button: HTMLButtonElement | null
  step: number
  timer: number | undefined
  visible: boolean
  paused: boolean
  interval: number
  tick: (c: Cycle) => void
}

const cycles = new Set<Cycle>()
let cycleObserver: IntersectionObserver | undefined
let cycleListening = false

function setToggle(c: Cycle, playing: boolean) {
  if (!c.button) return
  const label = playing ? c.button.dataset.labelPause : c.button.dataset.labelPlay
  c.button.setAttribute('aria-label', label ?? '')
  c.button.title = label ?? ''
  c.button.dataset.state = playing ? 'playing' : 'paused'
}

function syncCycle(c: Cycle) {
  const run = c.visible && !c.paused && !document.hidden && !reduced()
  if (run && c.timer === undefined) {
    c.timer = window.setInterval(() => c.tick(c), c.interval)
    c.root.dataset.running = 'true'
  } else if (!run && c.timer !== undefined) {
    window.clearInterval(c.timer)
    c.timer = undefined
    c.root.dataset.running = 'false'
  }
  setToggle(c, !c.paused)
}

// Salud de routers: aplica el siguiente paso del guion (router, estado, razón) y recalcula el
// resumen por estado. El color cambia al instante; el punto hace un pulso (transform).
type RouterStep = [number, string, string]

function routersTick(c: Cycle) {
  const steps = JSON.parse(c.root.dataset.steps ?? '[]') as RouterStep[]
  const labels = JSON.parse(c.root.dataset.labels ?? '{}') as Record<string, string>
  if (!steps.length) return
  const [index, state, reason] = steps[c.step % steps.length]!
  c.step++
  const tiles = c.root.querySelectorAll<HTMLElement>('[data-tile]')
  const tile = tiles[index]
  if (!tile) return
  tile.dataset.state = state
  const label = tile.querySelector('[data-label]')
  if (label) label.textContent = labels[state] ?? state
  const why = tile.querySelector('[data-reason]')
  if (why) why.textContent = reason
  tile.classList.remove('is-changed')
  void tile.offsetWidth
  tile.classList.add('is-changed')
  const counts: Record<string, number> = {}
  for (const t of tiles) counts[t.dataset.state ?? ''] = (counts[t.dataset.state ?? ''] ?? 0) + 1
  for (const el of c.root.querySelectorAll<HTMLElement>('[data-count-state]')) {
    el.textContent = String(counts[el.dataset.countState ?? ''] ?? 0)
  }
}

// Kiosco: muestra la siguiente diapositiva y marca su elemento de la lista de reproducción.
function slidesTick(c: Cycle) {
  const slides = c.root.querySelectorAll<HTMLElement>('[data-slide]')
  if (slides.length < 2) return
  c.step = (c.step + 1) % slides.length
  slides.forEach((s, i) => (s.dataset.active = String(i === c.step)))
  c.root.querySelectorAll<HTMLElement>('[data-slide-chip]').forEach((chip, i) => {
    chip.dataset.active = String(i === c.step)
    if (i === c.step) chip.setAttribute('aria-current', 'true')
    else chip.removeAttribute('aria-current')
  })
}

function enhanceCycles() {
  for (const c of [...cycles]) {
    if (!c.root.isConnected) {
      if (c.timer !== undefined) window.clearInterval(c.timer)
      cycleObserver?.unobserve(c.root)
      cycles.delete(c)
    }
  }
  cycleObserver ??= new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        const c = [...cycles].find((x) => x.root === e.target)
        if (!c) continue
        c.visible = e.isIntersecting
        syncCycle(c)
      }
    },
    { threshold: 0.2 },
  )
  for (const root of document.querySelectorAll<HTMLElement>(
    '[data-cycle]:not([data-cycle-ready])',
  )) {
    root.dataset.cycleReady = '1'
    const kind = root.dataset.cycle
    const c: Cycle = {
      root,
      button: root.querySelector<HTMLButtonElement>('[data-cycle-toggle]'),
      step: 0,
      timer: undefined,
      visible: false,
      paused: false,
      interval: Number(root.dataset.interval) || (kind === 'slides' ? 6000 : 2200),
      tick: kind === 'slides' ? slidesTick : routersTick,
    }
    cycles.add(c)
    if (c.button) {
      c.button.hidden = reduced()
      c.button.addEventListener('click', () => {
        c.paused = !c.paused
        syncCycle(c)
      })
    }
    cycleObserver.observe(root)
    setToggle(c, true)
  }
  if (!cycleListening) {
    cycleListening = true
    const all = () => cycles.forEach(syncCycle)
    document.addEventListener('visibilitychange', all)
    window.matchMedia('(prefers-reduced-motion: reduce)').addEventListener('change', () => {
      for (const c of cycles) if (c.button) c.button.hidden = reduced()
      all()
    })
  }
}

// ─── "Ver de nuevo" ─────────────────────────────────────────────────────────────────────────

function enhanceReplay() {
  for (const btn of document.querySelectorAll<HTMLButtonElement>(
    '[data-replay]:not([data-replay-ready])',
  )) {
    btn.dataset.replayReady = '1'
    const block = btn.closest<HTMLElement>('[data-reveal]')
    if (!block) continue
    // Solo tiene sentido si la animación corre (con la clase `rv` en <html>).
    btn.hidden = !document.documentElement.classList.contains('rv')
    btn.addEventListener('click', () => {
      block.classList.remove('rv-in')
      void block.offsetWidth
      block.classList.add('rv-in')
    })
  }
}

export function enhanceMotion() {
  ;(window as Window & { __hfRv?: () => void }).__hfRv?.()
  enhanceCounters()
  enhanceCycles()
  enhanceReplay()
}
