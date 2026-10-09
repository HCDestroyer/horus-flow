<script setup lang="ts">
import type { FlowExporterState } from '~~/types/api'

/** Estado del exportador de flujos = icono + texto + color (frontend.md §13.2). */
const props = defineProps<{ state: FlowExporterState | string }>()
const { t, te } = useI18n()

const VISUAL: Record<string, { cls: string; icon: string }> = {
  exporting: { cls: 'text-success', icon: 'i-lucide-radio-tower' },
  pending_configuration: { cls: 'text-muted', icon: 'i-lucide-circle-dashed' },
  silent: { cls: 'text-error', icon: 'i-lucide-wifi-off' },
  lossy: { cls: 'text-warning', icon: 'i-lucide-triangle-alert' },
  clock_skew: { cls: 'text-warning', icon: 'i-lucide-clock-alert' },
}
const visual = computed(() => VISUAL[props.state] ?? VISUAL.pending_configuration!)
const label = computed(() =>
  te(`exporterState.${props.state}`) ? t(`exporterState.${props.state}`) : String(props.state),
)
</script>

<template>
  <span class="inline-flex shrink-0 items-center gap-1 font-medium" :class="visual.cls">
    <UIcon :name="visual.icon" class="size-[1.1em] shrink-0" aria-hidden="true" />
    <span>{{ label }}</span>
  </span>
</template>
