<script setup lang="ts">
/**
 * Medidor fino (CPU, memoria, temperatura): valor en texto, barra con los umbrales de
 * aviso y crítico marcados, y el color solo cuando se superan (un color, un significado).
 * `null` = sin muestra: la barra queda vacía y el valor "—", nunca cero.
 */
const props = withDefaults(
  defineProps<{
    label: string
    /** Valor en la escala del medidor (0–`max`). */
    value: number | null | undefined
    max?: number
    thresholds: { warning: number; critical: number }
    format: (v: number | null | undefined) => string
    /** Valor con `RollingNumber` (mural y cabecera). */
    rolling?: boolean
  }>(),
  { max: 100, rolling: false },
)

const level = computed(() => meterLevel(props.value, props.thresholds))
const width = computed(() =>
  props.value === null || props.value === undefined
    ? 0
    : Math.min(100, Math.max(0, (props.value / props.max) * 100)),
)
const fill = computed(() =>
  level.value === 'critical'
    ? 'bg-error'
    : level.value === 'warning'
      ? 'bg-warning'
      : 'bg-(--viz-1)',
)
const pct = (v: number) => `${Math.min(100, (v / props.max) * 100)}%`
</script>

<template>
  <div class="flex min-w-0 flex-col gap-[0.2em]" :data-meter-level="level">
    <div class="flex items-baseline justify-between gap-[0.5em]">
      <span class="text-muted truncate">{{ label }}</span>
      <span
        class="text-highlighted shrink-0 font-semibold tabular"
        :class="{ 'text-warning': level === 'warning', 'text-error': level === 'critical' }"
      >
        <RollingNumber v-if="rolling" :value="format(value)" />
        <template v-else>{{ format(value) }}</template>
      </span>
    </div>
    <div
      class="relative h-[0.35em] min-h-1 overflow-hidden rounded-full bg-(--viz-track)"
      aria-hidden="true"
    >
      <span
        class="absolute inset-y-0 start-0 rounded-full transition-[width] duration-700"
        :class="fill"
        :style="{ width: `${width}%` }"
      />
      <span
        class="absolute inset-y-0 w-px bg-warning/70"
        :style="{ insetInlineStart: pct(thresholds.warning) }"
      />
      <span
        class="absolute inset-y-0 w-px bg-error/80"
        :style="{ insetInlineStart: pct(thresholds.critical) }"
      />
    </div>
  </div>
</template>
