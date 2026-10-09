<script setup lang="ts">
import type { BarItem } from '~/utils/chart-palette'

/**
 * Top N como lista de barras horizontales (frontend.md §6.2): etiqueta directa, valor
 * visible siempre (nada depende de hover) y barra de magnitud en un solo tono. Es una
 * lista semántica: el lector de pantalla oye "etiqueta, valor". La fila "Otros" va
 * atenuada al final.
 */

const props = withDefaults(defineProps<{ items: BarItem[]; label: string; max?: number }>(), {
  max: undefined,
})

const top = computed(() => props.max ?? Math.max(1, ...props.items.map((i) => i.value)))
</script>

<template>
  <ul
    v-fit-rows
    class="flex min-h-0 flex-1 flex-col justify-start gap-[calc(var(--w-gap)*0.55)] overflow-hidden"
    :aria-label="label"
  >
    <li
      v-for="item in items"
      data-fit-item
      :key="item.label"
      class="w-label min-w-0"
      :class="{ 'text-muted': item.muted }"
    >
      <div class="flex min-w-0 items-baseline justify-between gap-3">
        <span class="text-default min-w-0 truncate" :class="{ 'font-mono': item.mono }">{{
          item.label
        }}</span>
        <span class="text-highlighted shrink-0 font-medium tabular">
          {{ item.display }}
          <span v-if="item.detail" class="text-muted font-normal">· {{ item.detail }}</span>
        </span>
      </div>
      <div
        class="mt-1 h-[0.35em] min-h-1 overflow-hidden rounded-full bg-(--viz-track)"
        aria-hidden="true"
      >
        <div
          class="h-full rounded-full bg-(--viz-1)"
          :class="{ 'opacity-40': item.muted }"
          :style="{ width: `${Math.max(1, (item.value / top) * 100)}%` }"
        />
      </div>
    </li>
  </ul>
</template>
