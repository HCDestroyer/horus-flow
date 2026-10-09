<script setup lang="ts">
/**
 * Confianza de una inferencia (frontend.md §1: severidad y confianza se muestran por
 * separado): banda en texto + porcentaje, con tres barras como refuerzo (nunca solo forma).
 */
const props = defineProps<{ level: 'low' | 'medium' | 'high'; value?: number | null }>()
const { t } = useI18n()
const bars = computed(() => ({ low: 1, medium: 2, high: 3 })[props.level])
</script>

<template>
  <span class="inline-flex items-center gap-1.5" data-testid="confidence">
    <span class="inline-flex items-end gap-px" aria-hidden="true">
      <span
        v-for="i in 3"
        :key="i"
        class="w-1 rounded-sm"
        :class="i <= bars ? 'bg-(--ui-text-highlighted)' : 'bg-(--ui-border-accented)'"
        :style="{ height: `${4 + i * 3}px` }"
      />
    </span>
    <span>
      {{ t(`findings.confidence.${level}`) }}
      <span v-if="value !== null && value !== undefined" class="text-muted tabular">
        · {{ formatPercent(value) }}</span
      >
    </span>
  </span>
</template>
