// Mejora progresiva de MotionVideo.vue con DOM puro (sin hidratar Vue): las secciones de la
// portada se sirven con `hydrate-never` para no pagar JavaScript, así que el comportamiento del
// vídeo lo pone el plugin motion-video.client.ts sobre el HTML del servidor (y MotionVideo, si
// se monta en el cliente, llama a lo mismo).
//
// - Sin JS: solo el póster (imagen). El <video> no tiene fuentes hasta que se ve.
// - Carga diferida con IntersectionObserver; reproduce en pantalla y pausa fuera de ella y con
//   la pestaña oculta.
// - prefers-reduced-motion: póster estático y botón "Reproducir animación"; nada se descarga
//   hasta que la persona lo pide.
// - Botón para pausar/reanudar siempre disponible (WCAG 2.2.2, HIG accessibility › "Let people
//   control audio and video playback").

type State = {
  root: HTMLElement
  video: HTMLVideoElement
  button: HTMLButtonElement
  loaded: boolean
  visible: boolean
  userPaused: boolean
  userPlayed: boolean
}

const states = new Set<State>()
let observer: IntersectionObserver | undefined
let listening = false

function reducedMotion() {
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches
}

function load(s: State) {
  if (s.loaded) return
  s.loaded = true
  const large = window.matchMedia(s.root.dataset.largeMedia || 'not all').matches
  for (const source of s.video.querySelectorAll<HTMLSourceElement>('source')) {
    const src = large ? source.dataset.srcLarge : source.dataset.srcSmall
    if (src) source.src = src
  }
  s.video.preload = 'auto'
  s.video.load()
}

function setButton(s: State, playing: boolean) {
  const label = playing ? s.button.dataset.labelPause : s.button.dataset.labelPlay
  s.button.setAttribute('aria-label', label ?? '')
  s.button.title = label ?? ''
  s.button.dataset.state = playing ? 'playing' : 'paused'
  const text = s.button.querySelector<HTMLElement>('[data-motion-text]')
  if (text) text.textContent = label ?? ''
}

function shouldPlay(s: State) {
  if (s.userPaused || !s.visible || document.hidden) return false
  return s.userPlayed || !reducedMotion()
}

function sync(s: State) {
  if (shouldPlay(s)) {
    load(s)
    const p = s.video.play()
    if (p) p.catch(() => setButton(s, false))
    setButton(s, true)
  } else {
    if (!s.video.paused) s.video.pause()
    setButton(s, false)
  }
}

function onVisibility() {
  for (const s of states) sync(s)
}

export function enhanceMotionVideo(root: HTMLElement) {
  if (root.dataset.motionReady) return
  const video = root.querySelector<HTMLVideoElement>('video')
  const button = root.querySelector<HTMLButtonElement>('[data-motion-toggle]')
  const img = root.querySelector<HTMLImageElement>('img')
  if (!video || !button) return
  root.dataset.motionReady = '1'

  const s: State = {
    root,
    video,
    button,
    loaded: false,
    visible: false,
    userPaused: false,
    userPlayed: false,
  }
  states.add(s)

  // El póster no cargó: se muestra el respaldo (p. ej. el diagrama SVG).
  const fail = () => root.classList.add('motion-failed')
  if (img) {
    if (img.complete && img.naturalWidth === 0 && img.currentSrc) fail()
    img.addEventListener('error', fail, { once: true })
  }

  video.addEventListener('playing', () => root.classList.add('motion-playing'))
  // Si ningún formato se puede reproducir, se queda el póster.
  const sources = video.querySelectorAll('source')
  sources[sources.length - 1]?.addEventListener('error', () => {
    root.classList.remove('motion-playing')
    button.hidden = true
  })

  button.hidden = false
  root.classList.toggle('motion-reduced', reducedMotion())
  setButton(s, false)
  button.addEventListener('click', () => {
    const playing = button.dataset.state === 'playing'
    s.userPaused = playing
    s.userPlayed = !playing
    sync(s)
  })

  observer ??= new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        const st = [...states].find((x) => x.root === e.target)
        if (!st) continue
        st.visible = e.isIntersecting
        sync(st)
      }
    },
    { rootMargin: '120px 0px', threshold: 0.01 },
  )
  observer.observe(root)

  if (!listening) {
    listening = true
    document.addEventListener('visibilitychange', onVisibility)
    window.matchMedia('(prefers-reduced-motion: reduce)').addEventListener('change', () => {
      for (const st of states) st.root.classList.toggle('motion-reduced', reducedMotion())
      onVisibility()
    })
  }
}

/** Mejora todos los MotionVideo del documento y olvida los que ya no están. */
export function enhanceAllMotionVideos() {
  for (const s of [...states]) {
    if (!s.root.isConnected) {
      observer?.unobserve(s.root)
      states.delete(s)
    }
  }
  for (const el of document.querySelectorAll<HTMLElement>('[data-motion]')) enhanceMotionVideo(el)
}
