<script setup lang="ts">
import type { FindingState } from '~~/types/api'

/** Estado del hallazgo: neutral (la urgencia la da la severidad), icono + texto (§13.2). */
const props = defineProps<{ state: FindingState | string }>()
const { t, te } = useI18n()

const ICON: Record<string, string> = {
  open: 'i-lucide-circle-dot',
  acknowledged: 'i-lucide-eye',
  resolved: 'i-lucide-circle-check',
  false_positive: 'i-lucide-circle-slash',
}
</script>

<template>
  <span class="text-default inline-flex shrink-0 items-center gap-1" :data-finding-state="state">
    <UIcon
      :name="ICON[props.state] ?? 'i-lucide-circle'"
      class="text-muted size-4 shrink-0"
      aria-hidden="true"
    />
    {{ te(`findings.state.${state}`) ? t(`findings.state.${state}`) : state }}
  </span>
</template>
