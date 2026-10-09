<script setup lang="ts">
import type { Customer } from '~~/types/api'

/**
 * Tipo de cliente: nunca sin su origen (frontend.md §8.1, §13.2). Residencial / Comercial
 * (neutral / info: no son estados de salud) + "por defecto", "manual" con candado o
 * "detectado (confianza 85)".
 */
const props = defineProps<{
  kind: Customer['kind']
  source: Customer['kind_source']
  locked?: boolean
  confidence?: number | null
}>()
const { t } = useI18n()

const icon = computed(() =>
  props.kind === 'commercial'
    ? 'i-lucide-building-2'
    : props.kind === 'residential'
      ? 'i-lucide-house'
      : 'i-lucide-circle-help',
)
const origin = computed(() =>
  props.source === 'scoring' && props.confidence !== null && props.confidence !== undefined
    ? t('clients.kindSource.scoringWith', { n: Math.round(props.confidence * 100) })
    : t(`clients.kindSource.${props.source}`),
)
</script>

<template>
  <span
    class="inline-flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5"
    data-testid="client-kind"
  >
    <span
      class="inline-flex items-center gap-1 font-medium"
      :class="kind === 'commercial' ? 'text-info' : 'text-default'"
    >
      <UIcon :name="icon" class="size-4 shrink-0" aria-hidden="true" />
      {{ t(`clients.kind.${kind}`) }}
    </span>
    <span class="text-muted inline-flex items-center gap-0.5 text-xs">
      <UIcon
        v-if="locked"
        name="i-lucide-lock"
        class="size-3.5 shrink-0"
        :aria-label="t('clients.locked')"
        data-testid="kind-lock"
      />
      · {{ origin }}
    </span>
  </span>
</template>
