<script setup lang="ts">
import type { WidgetViewProps } from '../props'

/**
 * `findings_trend` — ¿Aumentan los hallazgos? Hallazgos abiertos por día y tipo (barras
 * apiladas, ≤ 5 tipos en orden fijo de la paleta; nunca colores de estado). La leyenda
 * siempre visible identifica cada tipo (también en mural: aquí no caben etiquetas directas).
 */
const props = defineProps<WidgetViewProps>()
const { t, te } = useI18n()

const series = computed(() =>
  props.data?.kind === 'series'
    ? props.data.series.slice(0, 5).map((s) => ({
        name: te(`findingKind.${s.group}`)
          ? t(`findingKind.${s.group}`)
          : String(s.group ?? s.metric),
        points: s.points as [string, number | null][],
      }))
    : [],
)
const total = computed(() =>
  series.value.reduce((acc, s) => acc + s.points.reduce((a, [, v]) => a + (v ?? 0), 0), 0),
)
const theme = useChartTheme()
const format = (v: number) => formatNumber(v)
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col gap-[calc(var(--w-gap)*0.5)]">
    <ul
      class="w-meta text-default flex flex-wrap gap-x-[0.9em] gap-y-0.5"
      :aria-label="t('widgets.findings_trend.legend')"
    >
      <li v-for="(s, i) in series" :key="s.name" class="inline-flex items-center gap-[0.4em]">
        <span
          class="inline-block size-[0.75em] rounded-sm"
          :style="{ background: theme.series[i] }"
          aria-hidden="true"
        />
        {{ s.name }}
      </li>
      <li class="text-muted ms-auto tabular" data-fit-optional="1">
        {{ t('widgets.findings_trend.total', { n: total }) }}
      </li>
    </ul>
    <div class="min-h-0 flex-1">
      <TimeSeriesChart
        :series="series"
        :label="t('widgets.findings_trend.chartLabel')"
        :scale="scale"
        :format="format"
        kind="bar"
        stacked
        time-format="day"
        :show-legend="false"
      />
    </div>
  </div>
</template>
