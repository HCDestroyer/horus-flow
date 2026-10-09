<script setup lang="ts">
import type { Severity } from '~~/types/api'

/**
 * Severidad de hallazgo = icono + texto + color (frontend.md §13.2). Crítica y Alta
 * comparten color (misma urgencia) y se distinguen por icono y texto.
 */
const props = withDefaults(defineProps<{ severity: Severity | string; size?: 'sm' | 'md' }>(), {
  size: 'sm',
})
const { t, te } = useI18n()

const VISUAL: Record<string, { color: 'error' | 'warning' | 'neutral'; icon: string }> = {
  critical: { color: 'error', icon: 'i-lucide-octagon-alert' },
  high: { color: 'error', icon: 'i-lucide-triangle-alert' },
  medium: { color: 'warning', icon: 'i-lucide-circle-alert' },
  low: { color: 'neutral', icon: 'i-lucide-info' },
  info: { color: 'neutral', icon: 'i-lucide-info' },
}
const visual = computed(() => VISUAL[props.severity] ?? VISUAL.info!)
const label = computed(() =>
  te(`severity.${props.severity}`) ? t(`severity.${props.severity}`) : t('severity.other'),
)
</script>

<template>
  <span
    class="inline-flex shrink-0 items-center gap-1 font-medium"
    :class="{
      'text-error': visual.color === 'error',
      'text-warning': visual.color === 'warning',
      'text-muted': visual.color === 'neutral',
    }"
    :data-severity="severity"
  >
    <UIcon :name="visual.icon" class="size-[1.1em] shrink-0" aria-hidden="true" />
    <span>{{ label }}</span>
  </span>
</template>
