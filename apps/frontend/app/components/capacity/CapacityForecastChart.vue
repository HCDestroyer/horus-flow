<script setup lang="ts">
import type { CapacityForecast, UplinkDaily } from '~~/types/api'
import type { ChartOption, TimeSeries } from '~/utils/chart-palette'

/**
 * Pico diario (p95 de las medias de 5 min, entrada y salida) frente a la capacidad, con la
 * proyección del pronóstico y su banda (≈ 90 %) para los próximos 12 meses. Paleta
 * categórica (nunca colores de estado como serie); la capacidad es una línea neutra y los
 * umbrales de planificación / urgente, líneas discontinuas con su etiqueta. Huecos como
 * hueco. Siempre con tabla alternativa (`ChartTable`).
 */
const props = defineProps<{
  daily: UplinkDaily[]
  forecast: CapacityForecast | null
  capacityBps: number | null
  planning: number
  urgent: number
  label: string
}>()
const { t } = useI18n()
const theme = useChartTheme()
const { reducedMotion } = useMotionPrefs()

const fmt = (v: number) => joinUnit(formatBps(v))
const day = new Intl.DateTimeFormat('es-ES', { day: '2-digit', month: '2-digit', timeZone: 'UTC' })

const series = computed(() =>
  forecastSeries(props.daily, props.forecast, {
    rx: t('capacity.chart.rx'),
    tx: t('capacity.chart.tx'),
    projection: t('capacity.chart.projection'),
    low: t('capacity.chart.low'),
    high: t('capacity.chart.high'),
  }),
)
/** Para la tabla alternativa: historia y proyección por separado (cada una con sus fechas). */
const tableSeries = computed<TimeSeries[]>(() => series.value.history)
const projectionTable = computed<TimeSeries[]>(() => series.value.projection)

const option = computed<ChartOption>(() => {
  const { ink, series: colors } = theme.value
  const th = THRESHOLD_INK[theme.value.mode]
  const ts = (points: TimeSeries['points']) => points.map(([at, v]) => [new Date(at).getTime(), v])
  const [rx, tx] = series.value.history
  const [proj, low, high] = series.value.projection
  const cap = props.capacityBps
  const marks = cap
    ? [
        {
          value: cap,
          label: t('capacity.chart.capacity', { v: fmt(cap) }),
          color: ink.axis,
          type: 'solid' as const,
        },
        {
          value: cap * props.planning,
          label: t('capacity.chart.planning', { pct: formatPercent(props.planning) }),
          color: th.warning,
          type: 'dashed' as const,
        },
        {
          value: cap * props.urgent,
          label: t('capacity.chart.urgent', { pct: formatPercent(props.urgent) }),
          color: th.critical,
          type: 'dashed' as const,
        },
      ]
    : []
  const out: ChartOption = {
    animation: !reducedMotion.value,
    color: [colors[0], colors[1], colors[2]],
    aria: { enabled: true, decal: { show: false } },
    grid: { left: 4, right: 12, top: 36, bottom: 4, containLabel: true },
    legend: {
      top: 0,
      left: 0,
      icon: 'roundRect',
      itemWidth: 12,
      itemHeight: 4,
      textStyle: { color: ink.text, fontSize: 12 },
      data: [rx?.name, tx?.name, proj?.name].filter((n): n is string => !!n),
    },
    tooltip: {
      trigger: 'axis',
      valueFormatter: (v) => (typeof v === 'number' ? fmt(v) : '—'),
    },
    xAxis: {
      type: 'time',
      axisLine: { lineStyle: { color: ink.axis } },
      axisTick: { show: false },
      splitLine: { show: false },
      axisLabel: {
        color: ink.muted,
        fontSize: 12,
        hideOverlap: true,
        formatter: (v: number) => day.format(new Date(v)),
      },
    },
    yAxis: {
      type: 'value',
      min: 0,
      splitNumber: 4,
      axisLabel: { color: ink.muted, fontSize: 12, formatter: (v: number) => fmt(v) },
      splitLine: { lineStyle: { color: ink.grid, width: 1 } },
    },
    series: [
      {
        type: 'line',
        name: rx?.name,
        data: rx ? ts(rx.points) : [],
        showSymbol: false,
        connectNulls: false,
        lineStyle: { width: 2 },
        markLine: marks.length
          ? {
              silent: true,
              symbol: 'none',
              animation: false,
              data: marks.map((m) => ({
                yAxis: m.value,
                name: m.label,
                lineStyle: { color: m.color, type: m.type, width: 1 },
                label: {
                  formatter: m.label,
                  position: 'insideEndTop',
                  color: m.color,
                  fontSize: 11,
                },
              })),
            }
          : undefined,
      },
      {
        type: 'line',
        name: tx?.name,
        data: tx ? ts(tx.points) : [],
        showSymbol: false,
        connectNulls: false,
        lineStyle: { width: 2 },
      },
      ...(proj && low && high
        ? [
            {
              type: 'line' as const,
              name: proj.name,
              data: ts(proj.points),
              showSymbol: false,
              lineStyle: { width: 2, type: 'dashed' as const },
            },
            {
              type: 'line' as const,
              name: low.name,
              data: ts(low.points),
              stack: 'band',
              showSymbol: false,
              lineStyle: { opacity: 0 },
              color: colors[2],
              tooltip: { show: false },
            },
            {
              type: 'line' as const,
              name: high.name,
              data: high.points.map(([at, v], i) => [
                new Date(at).getTime(),
                v === null || low.points[i]?.[1] == null ? null : v - (low.points[i]![1] as number),
              ]),
              stack: 'band',
              showSymbol: false,
              lineStyle: { opacity: 0 },
              color: colors[2],
              areaStyle: { opacity: 0.14 },
              tooltip: { show: false },
            },
          ]
        : []),
    ],
  }
  return out
})
</script>

<template>
  <div class="flex size-full min-h-0 flex-col">
    <div class="min-h-0 flex-1">
      <EChart :option="option" :label="label" />
    </div>
    <ChartTable :series="tableSeries" :label="label" :format="fmt" time-format="day" />
    <ChartTable
      v-if="projectionTable.length"
      :series="projectionTable"
      :label="t('capacity.chart.projectionTable', { chart: label })"
      :format="fmt"
      time-format="day"
    />
  </div>
</template>
