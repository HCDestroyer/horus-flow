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
const { reducedMotion, moreContrast } = useMotionPrefs()
const glow = computed(() => theme.value.mode === 'dark' && !moreContrast.value)
const option = computed<ChartOption>(() => ({
  animation: !reducedMotion.value,
  animationDurationUpdate: 700,
  color: [...theme.value.series],
  grid: { left: 0, right: 0, top: 2, bottom: 2 },
  xAxis: { type: 'category', show: false, boundaryGap: false },
  yAxis: { type: 'value', show: false, min: 'dataMin' },
  tooltip: { show: false },
  series: props.series.map((s, i) => ({
    type: 'line' as const,
    name: s.name,
    data: s.values,
    showSymbol: false,
    connectNulls: false,
    lineStyle: {
      width: 2,
      ...(glow.value
        ? { shadowBlur: 6, shadowColor: `${theme.value.series[i % theme.value.series.length]}99` }
        : {}),
    },
    emphasis: { disabled: true },
  })),
}))
</script>

<template>
  <EChart :option="option" :label="label" />
</template>
