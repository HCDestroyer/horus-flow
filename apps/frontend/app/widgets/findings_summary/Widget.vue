<script setup lang="ts">
import type { FindingsSummaryValues } from '../shapes'
import type { WidgetViewProps } from '../props'

/**
 * `findings_summary` — ¿Cuántos hallazgos abiertos hay y de qué severidad? Total como valor
 * principal; desglose por severidad (icono + texto + número) y clientes por estado de
 * seguridad (D18: "Infectado").
 */
const props = defineProps<WidgetViewProps>()
const { t } = useI18n()
const v = computed(() =>
  props.data?.kind === 'state' ? (props.data.values as unknown as FindingsSummaryValues) : null,
)
const severities = computed(() =>
  (['critical', 'high', 'medium', 'low'] as const)
    .map((s) => ({ s, n: v.value?.open_by_severity[s] }))
    .filter((x): x is { s: (typeof x)['s']; n: number } => typeof x.n === 'number'),
)
const states = computed(() =>
  (['infected', 'suspected'] as const)
    .map((s) => ({ s, n: v.value?.by_security_state[s] ?? 0 }))
    .filter((x) => x.n > 0),
)
</script>

<template>
  <div v-if="v" class="flex min-h-0 flex-1 flex-col gap-[calc(var(--w-gap)*0.5)]">
    <div class="flex items-end gap-[calc(var(--w-gap)*1.5)]">
      <p class="w-kpi text-highlighted" data-testid="findings-open">
        {{ formatNumber(v.open_total) }}
      </p>
      <ul
        class="w-label flex flex-wrap gap-x-3 gap-y-0.5 pb-[0.1em]"
        :aria-label="t('widgets.findings_summary.bySeverity')"
      >
        <li v-for="x in severities" :key="x.s" class="inline-flex items-baseline gap-1">
          <SeverityBadge :severity="x.s" />
          <span class="text-highlighted font-semibold tabular">{{ x.n }}</span>
        </li>
      </ul>
    </div>
    <ul
      v-if="states.length"
      class="w-meta flex flex-wrap gap-x-3 gap-y-0.5"
      :aria-label="t('widgets.findings_summary.byState')"
    >
      <li v-for="x in states" :key="x.s" class="inline-flex items-baseline gap-1">
        <SecurityStateBadge :state="x.s" />
        <span class="text-highlighted font-semibold tabular">{{ x.n }}</span>
      </li>
    </ul>
    <p v-if="scale === 'normal'" class="w-meta text-muted mt-auto tabular">
      {{
        t('widgets.findings_summary.new24h', { n: v.new_last_24h, customers: v.affected_customers })
      }}
    </p>
  </div>
</template>
