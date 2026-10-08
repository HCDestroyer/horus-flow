<script setup lang="ts">
import { BOTNET_SIGNALS, type BotnetSignalsValues } from '../shapes'
import { maxListRows, type WidgetViewProps } from '../props'

/**
 * `botnet_signals` — ¿Qué señales de botnet vemos ahora? Clientes afectados por señal
 * (traffic-model.md §8), de más a menos. Habla de señales y clientes afectados.
 */
const props = defineProps<WidgetViewProps>()
const { t } = useI18n()
const v = computed(() =>
  props.data?.kind === 'state' ? (props.data.values as unknown as BotnetSignalsValues) : null,
)
const items = computed(() =>
  BOTNET_SIGNALS.map((s) => ({ s, n: v.value?.by_signal[s] ?? 0 }))
    .filter((x) => x.n > 0)
    .sort((a, b) => b.n - a.n)
    .slice(0, maxListRows(props.scale, props.rows))
    .map((x) => ({
      label: t(`signals.${x.s}`),
      value: x.n,
      display: t('widgets.botnet_signals.customers', x.n),
    })),
)
</script>

<template>
  <div v-if="v" class="flex min-h-0 flex-1 flex-col gap-[calc(var(--w-gap)*0.75)]">
    <p class="w-label text-muted">
      <span class="text-highlighted font-semibold tabular">{{ v.affected_customers }}</span>
      {{ t('widgets.botnet_signals.affected', v.affected_customers) }}
    </p>
    <BarList :items="items" :label="t('widgets.botnet_signals.listLabel')" />
  </div>
</template>
