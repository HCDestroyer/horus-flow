<script setup lang="ts">
import type { RelativeRange } from '~~/types/api'
import type { TimeSeries } from '~/utils/chart-palette'
import { CPU_THRESHOLDS, MEMORY_THRESHOLDS, TEMP_THRESHOLDS } from '~/utils/router-health'

/**
 * Pestaña Métricas de la ficha del router (frontend.md §8.4): series de
 * `GET /analytics/routers/{id}/metrics` con el rango en la URL; CPU (media y núcleo máximo),
 * memoria, temperatura (placa y CPU), latencia ICMP y disponibilidad, con líneas de umbral
 * (80/95 CPU, 70/85 °C) y los huecos como huecos. Analítica caída → aviso de degradado.
 */
const props = defineProps<{ routerId: string; range: RelativeRange }>()
const { t } = useI18n()
const { $api } = useNuxtApp()

const METRICS =
  'cpu_percent,cpu_max_core_percent,memory_percent,temperature_celsius,cpu_temperature_celsius,icmp_rtt_ms,availability_ratio'

const { data, error, status, refresh } = useTenantQuery(
  () => `router:${props.routerId}:metrics:${props.range}`,
  () =>
    unwrap(
      $api.GET('/analytics/routers/{router_id}/metrics', {
        params: {
          path: { router_id: props.routerId },
          query: { range: props.range, metrics: METRICS },
        },
      }),
    ),
  { watch: [() => props.range, () => props.routerId] },
)
useAutoRefresh(() => refresh(), 60_000)

function pick(metric: string, name: string, scale = 1): TimeSeries | null {
  const s = data.value?.data.series.find((x) => x.metric === metric)
  if (!s) return null
  const points: TimeSeries['points'] = s.points.map(([at, v]) => [
    String(at),
    typeof v === 'number' ? v * scale : null,
  ])
  return { name, points }
}
const nonEmpty = (list: (TimeSeries | null)[]) =>
  list.filter((s): s is TimeSeries => !!s && s.points.some(([, v]) => v !== null))

const pct = (v: number) => formatPct(v)
const celsius = (v: number) => formatCelsius(v)
const ms = (v: number) => formatMs(v)
const timeFormat = computed(() => (props.range === '7d' || props.range === '30d' ? 'day' : 'time'))
const rangeLabel = computed(() => t(`ranges.${props.range}`))

const charts = computed(() => [
  {
    key: 'cpu',
    title: t('router.metrics.cpu'),
    series: nonEmpty([
      pick('cpu_percent', t('router.metrics.cpuAvg')),
      pick('cpu_max_core_percent', t('router.metrics.cpuMax')),
    ]),
    format: pct,
    min: 0,
    max: 100,
    thresholds: [
      {
        value: CPU_THRESHOLDS.warning,
        label: t('router.metrics.thresholdWarning', { v: pct(CPU_THRESHOLDS.warning) }),
        level: 'warning' as const,
      },
      {
        value: CPU_THRESHOLDS.critical,
        label: t('router.metrics.thresholdCritical', { v: pct(CPU_THRESHOLDS.critical) }),
        level: 'critical' as const,
      },
    ],
  },
  {
    key: 'memory',
    title: t('router.metrics.memory'),
    series: nonEmpty([pick('memory_percent', t('router.metrics.memoryUsed'))]),
    format: pct,
    min: 0,
    max: 100,
    thresholds: [
      {
        value: MEMORY_THRESHOLDS.warning,
        label: t('router.metrics.thresholdWarning', { v: pct(MEMORY_THRESHOLDS.warning) }),
        level: 'warning' as const,
      },
    ],
  },
  {
    key: 'temperature',
    title: t('router.metrics.temperature'),
    series: nonEmpty([
      pick('temperature_celsius', t('router.metrics.board')),
      pick('cpu_temperature_celsius', t('router.metrics.cpuTemp')),
    ]),
    format: celsius,
    min: undefined,
    max: undefined,
    thresholds: [
      {
        value: TEMP_THRESHOLDS.warning,
        label: t('router.metrics.thresholdWarning', { v: celsius(TEMP_THRESHOLDS.warning) }),
        level: 'warning' as const,
      },
      {
        value: TEMP_THRESHOLDS.critical,
        label: t('router.metrics.thresholdCritical', { v: celsius(TEMP_THRESHOLDS.critical) }),
        level: 'critical' as const,
      },
    ],
  },
  {
    key: 'latency',
    title: t('router.metrics.latency'),
    series: nonEmpty([pick('icmp_rtt_ms', t('router.metrics.rtt'))]),
    format: ms,
    min: 0,
    max: undefined,
    thresholds: [],
  },
  {
    key: 'availability',
    title: t('router.metrics.availability'),
    series: nonEmpty([pick('availability_ratio', t('router.metrics.availabilityRatio'), 100)]),
    format: pct,
    min: 0,
    max: 100,
    thresholds: [],
  },
])
const empty = computed(() => !!data.value && charts.value.every((c) => c.series.length === 0))
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="router-metrics">
    <LoadingState v-if="status === 'pending' && !data" :rows="4" />
    <DegradedNotice v-else-if="error" :error="error" @retry="refresh()" />
    <UCard v-else-if="empty" class="py-4">
      <EmptyState icon="i-lucide-activity" :title="t('router.metrics.empty')" />
    </UCard>
    <template v-else>
      <p class="text-muted text-sm">{{ t('router.metrics.gaps') }}</p>
      <div class="grid gap-4 lg:grid-cols-2">
        <UCard
          v-for="c in charts"
          :key="c.key"
          :ui="{ body: 'p-4 sm:p-4' }"
          :data-testid="`router-chart-${c.key}`"
        >
          <h3 class="text-highlighted mb-2 text-sm font-semibold">{{ c.title }}</h3>
          <div class="h-56">
            <TimeSeriesChart
              v-if="c.series.length"
              :series="c.series"
              :label="t('router.metrics.chartLabel', { metric: c.title, range: rangeLabel })"
              scale="normal"
              :format="c.format"
              :time-format="timeFormat"
              :thresholds="c.thresholds"
              :min="c.min"
              :max="c.max"
            />
            <p v-else class="text-muted flex h-full items-center justify-center text-sm">
              {{ t('router.metrics.empty') }}
            </p>
          </div>
        </UCard>
      </div>
    </template>
  </div>
</template>
