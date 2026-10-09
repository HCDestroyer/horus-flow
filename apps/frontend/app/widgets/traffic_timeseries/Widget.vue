<script setup lang="ts">
import type { WidgetViewProps } from '../props'

/**
 * `traffic_timeseries` — ¿Cómo evoluciona el tráfico? Bajada y subida en bit/s con un solo
 * eje; los huecos de datos se ven como hueco (frontend.md §6.2).
 */
const props = defineProps<WidgetViewProps>()
const { t } = useI18n()

const NAMES: Record<string, string> = { down_bps: 'down', up_bps: 'up' }
const series = computed(() =>
  props.data?.kind === 'series'
    ? props.data.series.map((s) => ({
        name: t(`widgets.traffic_timeseries.${NAMES[s.metric] ?? 'down'}`),
        points: s.points as [string, number | null][],
      }))
    : [],
)
const format = (v: number) => joinUnit(formatBps(v))
const label = computed(() =>
  t('widgets.traffic_timeseries.chartLabel', { range: String(props.widget.config.range ?? '24h') }),
)
</script>

<template>
  <div class="min-h-0 flex-1">
    <TimeSeriesChart :series="series" :label="label" :scale="scale" :format="format" area />
  </div>
</template>
