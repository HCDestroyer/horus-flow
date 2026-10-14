<script setup lang="ts">
// Ilustración de "Salud de routers" (datos simulados). El HTML trae un estado inicial
// representativo; con movimiento, motion-enhance.ts aplica un guion de cambios de estado en
// bucle (solo a la vista, con botón de pausa, WCAG 2.2.2) que vuelve siempre al inicio.
// `compact`: 6 routers sin resumen ni bucle (tarjeta de la portada); entran escalonados.
const props = withDefaults(defineProps<{ compact?: boolean }>(), { compact: false })
const { t, locale } = useI18n()

type State =
  'online' | 'warning' | 'critical' | 'degraded' | 'offline' | 'stale' | 'unknown' | 'maintenance'

const es = computed(() => locale.value !== 'en')
const r = (key: string, v?: string | number) => t(`monitoring.grid.reasons.${key}`, { v: v ?? '' })

const names = computed(() =>
  es.value
    ? [
        'rt-norte',
        'rt-sur',
        'rt-centro',
        'rt-lago',
        'rt-industrial',
        'rt-cerro',
        'rt-valle',
        'rt-puerto',
        'rt-plaza',
        'rt-rio',
        'rt-mirador',
        'rt-colonia',
        'rt-alameda',
        'rt-sierra',
        'rt-bosque',
        'rt-estadio',
        'rt-mercado',
        'rt-campus',
      ]
    : [
        'rt-north',
        'rt-south',
        'rt-center',
        'rt-lake',
        'rt-industrial',
        'rt-hill',
        'rt-valley',
        'rt-port',
        'rt-plaza',
        'rt-river',
        'rt-lookout',
        'rt-colony',
        'rt-avenue',
        'rt-sierra',
        'rt-forest',
        'rt-stadium',
        'rt-market',
        'rt-campus',
      ],
)

const initial = computed<[State, string][]>(() => [
  ['online', r('cpu', 18)],
  ['online', r('latency', 5)],
  ['online', r('cpu', 26)],
  ['online', r('ok')],
  ['online', r('temp', 49)],
  ['warning', r('cpu', 86)],
  ['online', r('ok')],
  ['degraded', r('loss', 4)],
  ['online', r('latency', 7)],
  ['stale', r('lastSeen', 12)],
  ['online', r('cpu', 31)],
  ['online', r('ok')],
  ['online', r('latency', 9)],
  ['unknown', r('new')],
  ['online', r('ok')],
  ['critical', r('temp', 78)],
  ['online', r('cpu', 22)],
  ['maintenance', r('window', '06:00')],
])

// Guion en bucle: cada router vuelve a su estado inicial al terminar la vuelta.
const steps = computed(() =>
  JSON.stringify([
    [2, 'warning', r('cpu', 91)],
    [5, 'online', r('cpu', 34)],
    [9, 'online', r('ok')],
    [12, 'offline', r('noResponse')],
    [2, 'online', r('cpu', 28)],
    [7, 'online', r('latency', 6)],
    [15, 'warning', r('temp', 66)],
    [12, 'online', r('back', 1)],
    [5, 'warning', r('cpu', 86)],
    [9, 'stale', r('lastSeen', 12)],
    [7, 'degraded', r('loss', 4)],
    [15, 'critical', r('temp', 78)],
  ]),
)

const states: State[] = [
  'online',
  'warning',
  'critical',
  'degraded',
  'offline',
  'stale',
  'unknown',
  'maintenance',
]
const labels = computed(() =>
  Object.fromEntries(states.map((s) => [s, t(`monitoring.states.items.${s}.label`)])),
)
const summary: State[] = ['online', 'warning', 'critical', 'degraded', 'offline', 'stale']

const tiles = computed(() => {
  const all = names.value.map((name, i) => ({
    name,
    state: initial.value[i]![0],
    reason: initial.value[i]![1],
  }))
  // Compacto: una muestra variada.
  return props.compact ? [0, 5, 7, 15, 17, 9].map((i) => all[i]!) : all
})
const counts = computed(() => {
  const c: Record<string, number> = {}
  for (const tile of tiles.value) c[tile.state] = (c[tile.state] ?? 0) + 1
  return c
})
</script>

<template>
  <figure
    class="rh"
    :class="compact ? '' : 'surface rounded-2xl p-4 sm:p-5'"
    :data-cycle="compact ? undefined : 'routers'"
    :data-steps="compact ? undefined : steps"
    :data-labels="compact ? undefined : JSON.stringify(labels)"
  >
    <figcaption :class="compact ? 'sr-only' : 'mb-4 flex items-start gap-3'">
      <span :class="compact ? '' : 'min-w-0 flex-1'">
        <span v-if="!compact" class="block font-semibold text-highlighted">{{
          t('monitoring.grid.title')
        }}</span>
        <span class="block text-sm text-muted">{{ t('monitoring.grid.caption') }}</span>
      </span>
      <button
        v-if="!compact"
        type="button"
        data-cycle-toggle
        hidden
        :data-label-play="t('motion.play')"
        :data-label-pause="t('motion.pause')"
        :aria-label="t('motion.pause')"
        :title="t('motion.pause')"
        class="cycle-toggle flex size-11 shrink-0 items-center justify-center rounded-full border border-default text-toned hover:bg-(--ui-bg-accented)/60 hover:text-highlighted"
      >
        <UIcon name="i-lucide-pause" class="icon-pause size-5" aria-hidden="true" />
        <UIcon name="i-lucide-play" class="icon-play size-5" aria-hidden="true" />
      </button>
    </figcaption>

    <ul
      v-if="!compact"
      class="mb-4 flex flex-wrap gap-x-4 gap-y-1.5 rounded-xl bg-(--ui-bg-muted) px-3 py-2.5 text-sm"
    >
      <li v-for="s in summary" :key="s" :data-state="s" class="flex items-center gap-1.5">
        <span class="st-dot" aria-hidden="true" />
        <span class="font-semibold text-highlighted tabular" :data-count-state="s">{{
          counts[s] ?? 0
        }}</span>
        <span class="text-muted">{{ labels[s] }}</span>
      </li>
    </ul>

    <ul class="grid gap-2" :class="compact ? 'grid-cols-2' : 'grid-cols-2 sm:grid-cols-3'">
      <li
        v-for="(tile, i) in tiles"
        :key="tile.name"
        data-tile
        :data-state="tile.state"
        :data-reveal="compact ? 'scale' : undefined"
        :style="compact ? { '--rv-i': i + 2 } : undefined"
        class="tile min-w-0 rounded-xl border border-default bg-(--surface) px-3 py-2.5"
      >
        <span class="block truncate font-mono text-[0.8rem] text-toned">{{ tile.name }}</span>
        <span class="mt-1 flex items-center gap-1.5 text-sm font-semibold text-highlighted">
          <span class="st-dot relative" aria-hidden="true" />
          <span data-label class="truncate">{{ labels[tile.state] }}</span>
        </span>
        <span data-reason class="mt-0.5 block truncate text-xs text-muted">{{ tile.reason }}</span>
      </li>
    </ul>
  </figure>
</template>

<style scoped>
.cycle-toggle[data-state='playing'] .icon-play,
.cycle-toggle[data-state='paused'] .icon-pause {
  display: none;
}
.cycle-toggle:not([data-state]) .icon-play {
  display: none;
}

/* Cambio de estado: un pulso del punto y un anillo que se expande (solo transform/opacity). */
.tile .st-dot::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: inherit;
  background: var(--st);
  opacity: 0;
}
@media (prefers-reduced-motion: no-preference) {
  .tile.is-changed .st-dot {
    animation: st-pulse 0.6s var(--ease-out);
  }
  .tile.is-changed .st-dot::after {
    animation: st-ring 0.9s var(--ease-out);
  }
}
@keyframes st-pulse {
  40% {
    scale: 1.5;
  }
}
@keyframes st-ring {
  from {
    opacity: 0.55;
    scale: 1;
  }
  to {
    opacity: 0;
    scale: 3;
  }
}
</style>
