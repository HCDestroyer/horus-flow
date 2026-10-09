<script setup lang="ts">
/**
 * Frescura de un dato (frontend.md §10.2): "hace 12 s" con la hora absoluta en `title`
 * (sin tooltip obligatorio: en mural la información no depende de hover, §7.4).
 * Atrasado → reloj + color de aviso; obsoleto → "Sin datos recientes · dato de hace N".
 */
const props = withDefaults(
  defineProps<{
    /** Instante del dato (ms). */
    at: number
    /** Intervalo esperado de actualización (s). */
    expectedSeconds: number
    live?: boolean
    /** Mostrar también cuando está fresco (escritorio). */
    always?: boolean
  }>(),
  { live: false, always: true },
)

const { t, d } = useI18n()
const now = useNow()

const age = computed(() => Math.max(0, (now.value - props.at) / 1000))
const level = computed(() => freshnessLevel(age.value, props.expectedSeconds))
const ago = computed(() => {
  const p = agoParts(age.value)
  return t(p.key, { n: p.n })
})
const text = computed(() => {
  if (level.value === 'stale') return t('freshness.stale', { ago: ago.value })
  if (props.live && level.value === 'fresh') return t('freshness.live', { ago: ago.value })
  return ago.value
})
const icon = computed(() =>
  level.value === 'fresh'
    ? props.live
      ? 'i-lucide-radio'
      : 'i-lucide-clock'
    : 'i-lucide-clock-alert',
)
const absolute = computed(() => d(new Date(props.at), 'long'))
</script>

<template>
  <span
    v-if="always || level !== 'fresh'"
    class="w-meta inline-flex shrink-0 items-center gap-1 tabular"
    :class="level === 'fresh' ? 'text-muted' : 'text-warning'"
    :data-freshness="level"
    data-testid="freshness"
  >
    <UIcon :name="icon" class="size-[1.1em] shrink-0" aria-hidden="true" />
    <time :datetime="new Date(at).toISOString()" :title="absolute">{{ text }}</time>
  </span>
</template>
