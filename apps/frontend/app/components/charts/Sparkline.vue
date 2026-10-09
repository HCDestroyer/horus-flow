<script setup lang="ts">
import type { ChartOption } from '~/utils/chart-palette'

/**
 * Sparkline (ECharts): tendencia sin ejes para acompañar un KPI. Decorativa: el KPI ya da
 * el valor y el texto alternativo resume la tendencia. Huecos como hueco.
 */
const props = defineProps<{
  series: { name: string; values: (number | null)[] }[]
  label: string
}>()

const theme = useChartTheme()
const option = computed<ChartOption>(() => ({
  animation: false,
  color: [...theme.value.series],
  grid: { left: 0, right: 0, top: 2, bottom: 2 },
  xAxis: { type: 'category', show: false, boundaryGap: false },
  yAxis: { type: 'value', show: false, min: 'dataMin' },
  tooltip: { show: false },
  series: props.series.map((s) => ({
    type: 'line' as const,
    name: s.name,
    data: s.values,
    showSymbol: false,
    connectNulls: false,
    lineStyle: { width: 2 },
    emphasis: { disabled: true },
  })),
}))
</script>

<template>
  <EChart :option="option" :label="label" />
</template>
