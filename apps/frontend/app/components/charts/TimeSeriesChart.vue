<script setup lang="ts">
import type { ChartOption, TimeSeries } from '~/utils/chart-palette'

/**
 * Serie temporal con ECharts (frontend.md §6.2, §7.4; skill dataviz):
 * - Un solo eje Y; color por serie en orden fijo de la paleta categórica validada.
 * - Huecos como hueco (`null`, `connectNulls: false`), nunca cero.
 * - Escritorio: leyenda + tooltip con cruz; líneas de 2 px.
 * - Mural: sin tooltip; la leyenda pasa a HTML encima del gráfico con el **último valor**
 *   de cada serie (texto del DOM: no se solapa y se audita, a diferencia de las etiquetas
 *   dibujadas en el canvas), líneas de 3 px, ≤ 5 marcas por eje y texto ≥ 22 px a 1080 p.
 * - Siempre con tabla alternativa para lectores de pantalla (`ChartTable`).
 */

const props = withDefaults(
  defineProps<{
    series: TimeSeries[]
    label: string
    scale: WidgetScale
    kind?: 'line' | 'bar'
    /** Formato del valor (ejes, tooltip, tabla). */
    format: (value: number) => string
    /** Formato del instante en el eje X. */
    timeFormat?: 'time' | 'day'
    stacked?: boolean
    area?: boolean
    showLegend?: boolean
  }>(),
  { kind: 'line', timeFormat: 'time', stacked: false, area: false, showLegend: true },
)

const theme = useChartTheme()
/** Factor de escala mural, igual que `--u` en CSS (diseño de 1920×1080 escalado). */
const unit = ref(1)
function onResize() {
  unit.value = Math.min(window.innerHeight / 1080, window.innerWidth / 1920)
}
onMounted(() => {
  onResize()
  window.addEventListener('resize', onResize)
})
onBeforeUnmount(() => window.removeEventListener('resize', onResize))

const wall = computed(() => props.scale === 'wall')
/** px a 1920×1080 escalados como `--u` en CSS. */
const px = (n: number) => (wall.value ? Math.round(n * unit.value) : n)

/** Leyenda mural: nombre y último valor no nulo de cada serie. */
const wallLegend = computed(() =>
  wall.value
    ? props.series.map((s, i) => {
        const last = [...s.points].reverse().find(([, v]) => typeof v === 'number')
        return {
          name: s.name,
          color: theme.value.series[i],
          value: last && props.kind === 'line' ? props.format(last[1] as number) : null,
        }
      })
    : [],
)

const timeFmt = computed(
  () =>
    new Intl.DateTimeFormat('es-ES', {
      ...(props.timeFormat === 'day'
        ? { day: '2-digit', month: '2-digit' }
        : { hour: '2-digit', minute: '2-digit', hour12: false }),
    }),
)

const option = computed<ChartOption>(() => {
  const { ink, series: colors } = theme.value
  const fontSize = wall.value ? px(22) : 12
  return {
    animation: false,
    color: [...colors],
    aria: { enabled: true, decal: { show: false } },
    grid: {
      left: 4,
      right: wall.value ? px(16) : 12,
      top: wall.value || !props.showLegend ? px(8) : 36,
      bottom: 4,
      containLabel: true,
    },
    legend: wall.value
      ? { show: false }
      : {
          show: props.showLegend && props.series.length > 1,
          top: 0,
          left: 0,
          icon: 'roundRect',
          itemWidth: 12,
          itemHeight: 4,
          textStyle: { color: ink.text, fontSize: 12 },
        },
    tooltip: wall.value
      ? { show: false }
      : {
          trigger: 'axis',
          axisPointer: { type: props.kind === 'bar' ? 'shadow' : 'line' },
          valueFormatter: (v) => (typeof v === 'number' ? props.format(v) : '—'),
        },
    xAxis: {
      type: 'time',
      axisLine: { lineStyle: { color: ink.axis } },
      axisTick: { show: false },
      splitLine: { show: false },
      splitNumber: wall.value ? 4 : 6,
      axisLabel: {
        color: ink.muted,
        fontSize,
        hideOverlap: true,
        formatter: (v: number) => timeFmt.value.format(new Date(v)),
      },
    },
    yAxis: {
      type: 'value',
      splitNumber: wall.value ? 3 : 4,
      axisLabel: { color: ink.muted, fontSize, formatter: (v: number) => props.format(v) },
      splitLine: { lineStyle: { color: ink.grid, width: 1 } },
    },
    series: props.series.map((s, i) => {
      const data = s.points.map(([t, v]) => [new Date(t).getTime(), v])
      if (props.kind === 'bar') {
        return {
          type: 'bar' as const,
          name: s.name,
          data,
          stack: props.stacked ? 'total' : undefined,
          barMaxWidth: wall.value ? px(48) : 28,
          itemStyle: { borderColor: ink.surface, borderWidth: props.stacked ? 1 : 0 },
          emphasis: { disabled: wall.value },
        }
      }
      return {
        type: 'line' as const,
        name: s.name,
        data,
        showSymbol: false,
        connectNulls: false,
        lineStyle: { width: wall.value ? 3 : 2 },
        areaStyle: props.area && i === 0 ? { opacity: 0.08 } : undefined,
        emphasis: { disabled: wall.value },
      }
    }),
  }
})
</script>

<template>
  <div class="flex size-full min-h-0 flex-col gap-[calc(var(--w-gap)*0.4)]">
    <ul
      v-if="wallLegend.length && showLegend"
      class="w-label flex min-w-0 flex-wrap gap-x-[calc(var(--w-gap)*2)]"
      data-testid="chart-wall-legend"
    >
      <li
        v-for="item in wallLegend"
        :key="item.name"
        class="inline-flex min-w-0 items-center gap-[0.4em] whitespace-nowrap"
      >
        <span
          class="inline-block h-[0.25em] w-[0.9em] shrink-0 rounded-full"
          :style="{ background: item.color }"
          aria-hidden="true"
        />
        <span class="text-muted">{{ item.name }}</span>
        <span v-if="item.value" class="text-highlighted font-semibold tabular">{{
          item.value
        }}</span>
      </li>
    </ul>
    <div class="min-h-0 flex-1">
      <EChart :option="option" :label="label" />
    </div>
    <ChartTable :series="series" :label="label" :format="format" :time-format="timeFormat" />
  </div>
</template>
