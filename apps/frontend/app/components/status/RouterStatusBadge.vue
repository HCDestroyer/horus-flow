<script setup lang="ts">
import type { RouterEffectiveStatus } from '~~/types/api'

/**
 * Estado efectivo de un router (api.md §2.6) = icono + texto + color, con la razón traducida
 * y "desde hace…" (frontend.md §13.2). `stale` se lee "Sin datos recientes" (no sabemos),
 * nunca como caído; `unknown` = nunca observado.
 */
const props = withDefaults(
  defineProps<{
    status: RouterEffectiveStatus | string
    reason?: string | null
    since?: string | null
    /** Sin razón ni "desde" (listas densas y mural compacto). */
    compact?: boolean
  }>(),
  { reason: null, since: null, compact: false },
)
const { t, te } = useI18n()

const visual = computed(() => routerStatusVisual(props.status))
const label = computed(() =>
  te(`routerStatus.${props.status}`) ? t(`routerStatus.${props.status}`) : String(props.status),
)
const reasonText = computed(() => {
  if (!props.reason || props.reason === 'ok') return null
  return te(`routerReason.${props.reason}`) ? t(`routerReason.${props.reason}`) : props.reason
})
</script>

<template>
  <span
    class="inline-flex min-w-0 flex-wrap items-baseline gap-x-[0.5em] gap-y-[0.1em]"
    :data-router-status="status"
  >
    <span class="inline-flex shrink-0 items-center gap-[0.3em] font-medium" :class="visual.cls">
      <UIcon :name="visual.icon" class="size-[1.1em] shrink-0 self-center" aria-hidden="true" />
      <span>{{ label }}</span>
    </span>
    <span
      v-if="!compact && reasonText"
      class="text-highlighted min-w-0 font-medium"
      data-testid="router-status-reason"
      >· {{ reasonText }}</span
    >
    <span v-if="!compact && since" class="text-muted text-[0.92em] whitespace-nowrap">
      <RelativeTime :at="since" :prefix="t('router.since')" />
    </span>
  </span>
</template>
