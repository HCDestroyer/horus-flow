<script setup lang="ts">
/**
 * "hace 2 min" con la fecha absoluta y la zona en `title` (frontend.md §10.2, §13.4). El
 * absoluto también está en `datetime` para lectores de pantalla.
 */
const props = defineProps<{ at: string | number | null | undefined; prefix?: string }>()
const { t, d } = useI18n()
const now = useNow()

const ms = computed(() =>
  props.at === null || props.at === undefined ? null : new Date(props.at).getTime(),
)
const text = computed(() => {
  if (ms.value === null) return '—'
  const p = agoParts((now.value - ms.value) / 1000)
  return t(p.key, { n: p.n })
})
</script>

<template>
  <time
    v-if="ms !== null"
    :datetime="new Date(ms).toISOString()"
    :title="d(new Date(ms), 'long')"
    class="tabular"
    >{{ prefix ? `${prefix} ` : '' }}{{ text }}</time
  >
  <span v-else class="text-dimmed">—</span>
</template>
