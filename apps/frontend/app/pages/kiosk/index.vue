<script setup lang="ts">
/**
 * Modo kiosco (I1-21, frontend.md §7): enrolamiento por código y reproductor.
 * - Sin credencial: pantalla de código legible a 3–5 m (el código puede venir en el
 *   fragmento `#code=…` del QR: no viaja al servidor).
 * - Enrolada: dashboards a pantalla completa en tema oscuro, sin controles; rotación según la
 *   playlist con precarga del siguiente (nunca un esqueleto salvo el primer arranque);
 *   Espacio pausa/reanuda y ←/→ cambian de dashboard (WCAG 2.2.2); fundido ≤ 300 ms o
 *   cambio instantáneo con movimiento reducido.
 * - Recuperación: banda "Sin conexión desde HH:MM" mantiene los últimos datos; nueva versión
 *   del frontend → recarga en el siguiente cambio de dashboard y solo si el servidor
 *   responde; recarga diaria a las 04:00; *wake lock*; cursor oculto; desplazamiento de 1–2 px
 *   cada 10 min contra el quemado de pantalla.
 */
definePageMeta({ public: true, layout: 'kiosk', colorMode: 'dark' })

const { t, d } = useI18n()
const runtime = useRuntimeConfig()
const kiosk = useKiosk()
const { phase, config, dashboards, offlineSince } = kiosk

useHead({ title: () => t('kiosk.title') })

// --- Enrolamiento ------------------------------------------------------------------------
const code = ref('')
const enrollError = ref<string | null>(null)
const enrolling = ref(false)

onMounted(() => {
  const m = window.location.hash.match(/code=([A-Za-z0-9-]+)/)
  if (m) {
    code.value = m[1]!.toUpperCase()
    history.replaceState(null, '', window.location.pathname)
  }
})

async function submitCode() {
  enrollError.value = null
  if (code.value.replace(/[\s-]/g, '').length !== 8) {
    enrollError.value = t('kiosk.enroll.length')
    return
  }
  enrolling.value = true
  const result = await kiosk.enroll(code.value)
  enrolling.value = false
  if (result !== 'ok') enrollError.value = t(`kiosk.enroll.errors.${result}`)
  else code.value = ''
}

// --- Reproductor -------------------------------------------------------------------------
const items = computed(() =>
  (config.value?.items ?? []).filter((i) => dashboards.value.has(i.dashboard_id)),
)
const index = ref(0)
const paused = ref(false)
const total = computed(() => items.value.length)
const current = computed(() => items.value[index.value % Math.max(1, total.value)])
const nextIndex = computed(() => (total.value > 1 ? (index.value + 1) % total.value : null))
const tenantName = computed(() => config.value?.tenant_name ?? '')
const reducedMotion = ref(false)
let rotateTimer: ReturnType<typeof setTimeout> | undefined
/** Versión que pidió el servidor y ya provocó una recarga (evita bucles). */
const RELOADED_KEY = 'horus.kiosk.reloadedFor'

function schedule() {
  clearTimeout(rotateTimer)
  if (paused.value || total.value < 2 || !current.value) return
  const seconds = Math.max(10, current.value.duration_seconds ?? 30)
  rotateTimer = setTimeout(() => go(1), seconds * 1000)
}

async function go(step: number) {
  if (!total.value) return
  if (await maybeReload()) return
  index.value = (index.value + step + total.value) % total.value
  schedule()
}

/** Recarga en el cambio de dashboard si cambió la versión o toca la recarga diaria. */
async function maybeReload() {
  const wanted = config.value?.frontend_min_version
  const build = runtime.app.buildId
  let pending = false
  if (wanted && wanted !== build) {
    try {
      pending = sessionStorage.getItem(RELOADED_KEY) !== wanted
    } catch {
      pending = true
    }
  }
  const daily = Date.now() >= nextDailyReload
  if (!pending && !daily) return false
  // Nunca deja una pantalla en blanco: solo si el servidor responde.
  if (!(await kiosk.serverResponds())) return false
  try {
    if (wanted) sessionStorage.setItem(RELOADED_KEY, wanted)
  } catch {
    // sin storage
  }
  window.location.reload()
  return true
}

function nextAt4(from: number) {
  const dt = new Date(from)
  dt.setHours(4, 0, 0, 0)
  if (dt.getTime() <= from) dt.setDate(dt.getDate() + 1)
  return dt.getTime()
}
const nextDailyReload = nextAt4(Date.now())

watch([total, () => current.value?.dashboard_id], schedule)
watch(paused, schedule)

function onKey(e: KeyboardEvent) {
  if (phase.value !== 'playing') return
  if (e.key === ' ') {
    e.preventDefault()
    paused.value = !paused.value
  } else if (e.key === 'ArrowRight') go(1)
  else if (e.key === 'ArrowLeft') go(-1)
}

// Cursor oculto tras 3 s sin movimiento; aviso de pantalla completa una sola vez.
const cursorVisible = ref(true)
let cursorTimer: ReturnType<typeof setTimeout> | undefined
function pokeCursor() {
  cursorVisible.value = true
  clearTimeout(cursorTimer)
  cursorTimer = setTimeout(() => (cursorVisible.value = false), 3000)
}
const showFullscreenHint = ref(false)
async function enterFullscreen() {
  showFullscreenHint.value = false
  try {
    await document.documentElement.requestFullscreen()
  } catch {
    // El navegador en modo kiosco ya está a pantalla completa o no lo permite.
  }
}

// Wake lock: que la TV no entre en reposo; se renueva al volver a ser visible.
let wakeLock: { release: () => Promise<void> } | null = null
async function requestWakeLock() {
  try {
    const nav = navigator as Navigator & {
      wakeLock?: { request: (t: 'screen') => Promise<{ release: () => Promise<void> }> }
    }
    wakeLock = (await nav.wakeLock?.request('screen')) ?? null
  } catch {
    wakeLock = null
  }
}
function onVisibility() {
  if (document.visibilityState === 'visible') requestWakeLock()
}

// Protección contra quemado: el lienzo se desplaza 1–2 px cada 10 min.
const shift = computed(() => {
  const step = Math.floor(kiosk.now.value / 600_000) % 4
  return ['0px, 0px', '1px, 0px', '1px, 1px', '0px, 1px'][step]
})

// Watchdog: errores no capturados repetidos → recarga cuando el servidor responde.
let errors: number[] = []
async function onError() {
  const now = Date.now()
  errors = [...errors.filter((t) => now - t < 600_000), now]
  if (errors.length >= 5 && (await kiosk.serverResponds())) window.location.reload()
}

onMounted(() => {
  reducedMotion.value = window.matchMedia('(prefers-reduced-motion: reduce)').matches
  showFullscreenHint.value = !document.fullscreenElement && document.fullscreenEnabled
  window.addEventListener('keydown', onKey)
  window.addEventListener('error', onError)
  document.addEventListener('visibilitychange', onVisibility)
  requestWakeLock()
  pokeCursor()
})
onBeforeUnmount(() => {
  clearTimeout(rotateTimer)
  clearTimeout(cursorTimer)
  window.removeEventListener('keydown', onKey)
  window.removeEventListener('error', onError)
  document.removeEventListener('visibilitychange', onVisibility)
  wakeLock?.release().catch(() => undefined)
})

const rotation = computed(() => ({ index: index.value, total: total.value, paused: paused.value }))
</script>

<template>
  <div
    class="relative h-dvh w-full"
    :class="{ 'cursor-none': !cursorVisible }"
    data-testid="kiosk"
    :data-phase="phase"
    @pointermove="pokeCursor"
  >
    <!-- Arranque -->
    <div v-if="phase === 'starting'" class="kiosk-center" role="status">
      <AppLogo class="size-16" />
      <p class="kiosk-text text-muted">{{ t('kiosk.starting') }}</p>
    </div>

    <!-- Enrolamiento por código -->
    <form
      v-else-if="phase === 'enroll'"
      class="kiosk-center"
      data-testid="kiosk-enroll"
      @submit.prevent="submitCode"
    >
      <AppLogo class="size-16" />
      <h1 class="kiosk-title text-highlighted font-semibold">{{ t('kiosk.enroll.title') }}</h1>
      <p class="kiosk-text text-muted max-w-[28em] text-center">{{ t('kiosk.enroll.body') }}</p>
      <label for="kiosk-code" class="sr-only">{{ t('kiosk.enroll.label') }}</label>
      <input
        id="kiosk-code"
        v-model="code"
        class="kiosk-code bg-elevated text-highlighted ring-accented focus:ring-primary rounded-lg text-center font-mono tracking-[0.3em] uppercase ring-2 outline-none"
        maxlength="9"
        autocomplete="off"
        autocapitalize="characters"
        spellcheck="false"
        :aria-invalid="!!enrollError"
        aria-describedby="kiosk-code-error"
        data-testid="kiosk-code"
      />
      <p
        id="kiosk-code-error"
        class="kiosk-text min-h-[1.5em] font-medium"
        :class="enrollError ? 'text-error' : 'text-muted'"
        role="alert"
        data-testid="kiosk-enroll-error"
      >
        {{ enrollError ?? '' }}
      </p>
      <UButton
        type="submit"
        size="xl"
        :loading="enrolling"
        :label="t('kiosk.enroll.submit')"
        class="kiosk-text"
        data-testid="kiosk-submit"
      />
    </form>

    <!-- Reproductor -->
    <template v-else-if="phase === 'playing' && current">
      <div class="h-full w-full" :style="{ transform: `translate(${shift})` }">
        <template v-for="(item, i) in items" :key="`${i}-${item.dashboard_id}`">
          <div
            v-if="i === index || i === nextIndex"
            class="absolute inset-0"
            :class="[
              i === index ? 'kiosk-visible' : 'kiosk-preload',
              reducedMotion || config?.transition === 'none' ? '' : 'kiosk-fade',
            ]"
            :aria-hidden="i !== index"
            :data-testid="i === index ? 'kiosk-current' : 'kiosk-next'"
            :data-dashboard="item.dashboard_id"
          >
            <h1 v-if="i === index" class="sr-only">
              {{ dashboards.get(item.dashboard_id)?.name }}
            </h1>
            <DashboardGrid
              :dashboard="dashboards.get(item.dashboard_id)!"
              :tenant-name="tenantName"
              scale="wall"
              :rotation="i === index ? rotation : null"
            />
          </div>
        </template>
      </div>
    </template>

    <!-- Sin conexión: banda ámbar a todo el ancho, sin parpadeo -->
    <div
      v-if="offlineSince"
      class="kiosk-band bg-warning text-inverted absolute inset-x-0 top-0 z-10 flex items-center justify-center gap-[0.5em] font-semibold"
      role="status"
      data-testid="kiosk-offline"
    >
      <UIcon name="i-lucide-wifi-off" class="size-[1.2em]" aria-hidden="true" />
      {{ t('kiosk.offline', { time: d(new Date(offlineSince), 'time') }) }}
    </div>

    <button
      v-if="showFullscreenHint && phase === 'playing'"
      type="button"
      class="kiosk-text bg-elevated text-highlighted ring-default absolute end-4 bottom-4 z-10 rounded-lg px-4 py-2 ring-1"
      @click="enterFullscreen"
    >
      {{ t('kiosk.fullscreen') }}
    </button>
  </div>
</template>

<style scoped>
.kiosk-center {
  --u: min(calc(100dvh / 1080), calc(100dvw / 1920));
  display: flex;
  height: 100%;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: calc(var(--u) * 28);
  padding: 4vmin;
}
.kiosk-title {
  font-size: max(28px, calc(var(--u) * 56));
}
.kiosk-text {
  font-size: max(18px, calc(var(--u) * 28));
}
.kiosk-code {
  font-size: max(32px, calc(var(--u) * 72));
  width: min(90vw, 12em);
  padding: 0.2em 0.4em;
}
.kiosk-band {
  --u: min(calc(100dvh / 1080), calc(100dvw / 1920));
  font-size: max(18px, calc(var(--u) * 30));
  padding: calc(var(--u) * 14);
}
.kiosk-preload {
  visibility: hidden;
  pointer-events: none;
}
.kiosk-fade.kiosk-visible {
  animation: kiosk-fade 300ms ease-out;
}
@keyframes kiosk-fade {
  from {
    opacity: 0;
  }
  to {
    opacity: 1;
  }
}
@media (prefers-reduced-motion: reduce) {
  .kiosk-fade.kiosk-visible {
    animation: none;
  }
}
</style>
