<script setup lang="ts">
import type { CustomersActiveValues } from '../shapes'
import type { WidgetViewProps } from '../props'

/** `customers_active` — ¿Cuántos clientes hay activos y cuántos nuevos hoy? Un dato principal. */
const props = defineProps<WidgetViewProps>()
const { t } = useI18n()
const v = computed(() =>
  props.data?.kind === 'state' ? (props.data.values as unknown as CustomersActiveValues) : null,
)
</script>

<template>
  <div v-if="v" class="flex min-h-0 flex-1 flex-col gap-[calc(var(--w-gap)*0.5)]">
    <div class="flex min-w-0 flex-wrap items-end gap-x-[calc(var(--w-gap)*1.5)]">
      <p class="w-kpi text-highlighted" data-testid="customers-active">
        {{ formatNumber(v.active) }}
      </p>
      <p class="w-label text-default pb-[0.1em]" data-fit-optional="2">
        <span class="font-semibold tabular">+{{ formatNumber(v.new_today) }}</span>
        {{ t('widgets.customers_active.newToday', v.new_today) }}
      </p>
    </div>
    <p v-if="scale === 'normal'" class="w-meta text-muted mt-auto tabular" data-fit-optional="1">
      {{ t('widgets.customers_active.total', { n: formatNumber(v.total) }) }}
    </p>
  </div>
</template>
