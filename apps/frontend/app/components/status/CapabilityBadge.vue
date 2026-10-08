<script setup lang="ts">
import type { CapabilityState } from '~~/shared/api/types'

/** Estado = icono + texto + color, nunca solo color (frontend.md §13.2, WCAG 1.4.1). */
const props = defineProps<{ state: CapabilityState }>()

const { t } = useI18n()

const VISUAL: Record<
  CapabilityState,
  { color: 'success' | 'warning' | 'error' | 'neutral'; icon: string }
> = {
  ok: { color: 'success', icon: 'i-lucide-circle-check' },
  degraded: { color: 'warning', icon: 'i-lucide-triangle-alert' },
  stale: { color: 'warning', icon: 'i-lucide-clock-alert' },
  unavailable: { color: 'error', icon: 'i-lucide-circle-x' },
}

const visual = computed(() => VISUAL[props.state] ?? VISUAL.unavailable)
</script>

<template>
  <UBadge
    :color="visual.color"
    variant="subtle"
    :icon="visual.icon"
    :label="t(`overview.capabilityState.${state}`)"
    class="shrink-0"
  />
</template>
