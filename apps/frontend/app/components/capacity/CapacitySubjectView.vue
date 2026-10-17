<script setup lang="ts">
import type { CapacitySubjectDetail } from '~~/types/api'
import type { TimeSeries } from '~/utils/chart-palette'

/**
 * Detalle de un enlace de subida o de la salida internacional: KPI (capacidad, ahora, p95,
 * horas sobre umbral, pérdida / latencia / jitter, diagnóstico), recomendación del
 * pronóstico con sus entradas (crecimiento, confianza, días de historia, fechas de cruce),
 * gráfico de picos diarios vs capacidad con la proyección y su banda, y la serie horaria de
 * la sonda de su puerta de enlace. Todo con tabla alternativa.
 */
const props = defineProps<{ detail: CapacitySubjectDetail }>()
const { t, te } = useI18n()

const uplink = computed(() => props.detail.uplink)
const egress = computed(() => props.detail.egress)
const st = computed(() => props.detail.settings)
const capacity = computed(() => uplink.value?.capacity_bps ?? egress.value?.capacity_bps ?? null)
const window = computed(() => uplink.value?.window ?? egress.value?.window ?? null)
const util = computed(() => uplink.value?.utilization ?? egress.value?.utilization ?? null)
const rx = computed(() => uplink.value?.rx_bps ?? egress.value?.rx_bps ?? null)
const fc = computed(() => props.detail.forecast)
const bps = (v: number | null | undefined) => joinUnit(formatBps(v))
const hours = (v: number) => `${formatNumber(v, v < 10 ? 1 : 0)} h`
const diagnosis = (k: string) => (te(`capacity.diagnosis.${k}`) ? t(`capacity.diagnosis.${k}`) : k)
const crossingText = (c: {
  already: boolean
  date: string | null
  threshold: number
  days_until: number | null
}) => {
  if (c.already) return t('capacity.forecast.already', { pct: formatPercent(c.threshold) })
  if (!c.date) return t('capacity.forecast.noCrossing', { pct: formatPercent(c.threshold) })
  return t('capacity.forecast.crossesOnDays', {
    pct: formatPercent(c.threshold),
    date: formatDay(c.date),
    days: c.days_until ?? 0,
  })
}
const chartLabel = computed(() =>
  t('capacity.chart.label', {
    name: uplink.value?.label ?? t('capacity.egress.title'),
    days: props.detail.days,
  }),
)
const probe = computed(() => props.detail.probe_series[0] ?? null)
const lossSeries = computed<TimeSeries[]>(() =>
  probe.value
    ? [
        {
          name: t('capacity.probe.loss'),
          points: probe.value.points.map((p) => [p.at, p.loss_pct]),
        },
      ]
    : [],
)
const rttSeries = computed<TimeSeries[]>(() =>
  probe.value
    ? [
        {
          name: t('capacity.probe.rtt'),
          points: probe.value.points.map((p) => [p.at, p.rtt_avg_ms]),
        },
        {
          name: t('capacity.probe.jitter'),
          points: probe.value.points.map((p) => [p.at, p.jitter_ms]),
        },
      ]
    : [],
)
// Historial de la capacidad contratada (más reciente primero): cada día se mide contra la
// capacidad vigente entonces.
const history = computed(() => props.detail.capacity_history ?? [])
const periodBps = (v: number | null) => (v == null ? t('capacity.history.portSpeed') : bps(v))
const pctFmt = (v: number) => `${formatNumber(v, 1)} %`
const msFmt = (v: number) => formatMs(v)
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="capacity-detail">
    <dl class="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
      <div>
        <dt class="text-muted text-xs">{{ t('capacity.table.capacity') }}</dt>
        <dd class="text-highlighted tabular font-semibold" data-testid="detail-capacity">
          {{ bps(capacity) }}
        </dd>
        <dd v-if="uplink" class="text-muted text-xs">
          {{ t(`capacity.capacitySource.${uplink.capacity_source}`) }}
        </dd>
      </div>
      <div>
        <dt class="text-muted text-xs">{{ t('capacity.table.now') }}</dt>
        <dd class="text-highlighted tabular font-semibold">{{ formatPercent(util) }}</dd>
        <dd class="text-muted text-xs tabular">↓ {{ bps(rx) }}</dd>
      </div>
      <div>
        <dt class="text-muted text-xs">
          {{ t('capacity.detail.p95', { days: window?.days ?? 7 }) }}
        </dt>
        <dd class="text-highlighted tabular font-semibold">
          {{ formatPercent(window?.util_p95) }}
        </dd>
        <dd class="text-muted text-xs tabular">
          {{ bps(window ? Math.max(window.rx_p95_bps, window.tx_p95_bps) : null) }}
        </dd>
      </div>
      <div>
        <dt class="text-muted text-xs">
          {{
            t('capacity.table.hours', {
              warn: formatPercent(st.saturation_warning),
              crit: formatPercent(st.saturation_critical),
            })
          }}
        </dt>
        <dd class="text-highlighted tabular font-semibold">
          {{
            window
              ? `${hours(window.hours_above_warning)} · ${hours(window.hours_above_critical)}`
              : '—'
          }}
        </dd>
      </div>
      <div v-if="uplink">
        <dt class="text-muted text-xs">{{ t('capacity.table.quality') }}</dt>
        <dd class="text-highlighted tabular font-semibold">
          {{ uplink.probe?.loss_pct != null ? `${formatNumber(uplink.probe.loss_pct, 1)} %` : '—' }}
        </dd>
        <dd class="text-muted text-xs tabular">
          {{
            uplink.probe
              ? `${formatMs(uplink.probe.rtt_avg_ms)} · ± ${formatMs(uplink.probe.jitter_ms)}`
              : t('capacity.noProbe')
          }}
        </dd>
      </div>
      <div v-if="uplink">
        <dt class="text-muted text-xs">{{ t('capacity.detail.diagnosis') }}</dt>
        <dd class="text-highlighted font-semibold" data-testid="detail-diagnosis">
          {{ diagnosis(uplink.diagnosis.kind) }}
        </dd>
      </div>
    </dl>

    <UCard :ui="{ body: 'p-4 sm:p-4' }" data-testid="detail-forecast">
      <div class="flex flex-wrap items-center gap-2">
        <h2 class="text-highlighted text-base font-semibold">
          {{ t('capacity.detail.forecastTitle') }}
        </h2>
        <ForecastLevelBadge v-if="fc" :level="fc.level" />
      </div>
      <p
        v-if="detail.recommendation"
        class="text-highlighted mt-2 text-sm"
        data-testid="detail-recommendation"
      >
        {{ detail.recommendation }}
      </p>
      <p v-else class="text-muted mt-2 text-sm">{{ t('capacity.forecast.none') }}</p>
      <dl
        v-if="fc && fc.monthly_growth != null"
        class="mt-3 grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-4"
      >
        <div>
          <dt class="text-muted text-xs">{{ t('capacity.detail.growth') }}</dt>
          <dd class="tabular">
            {{ formatSigned(fc.monthly_growth) }}
            <span class="text-muted text-xs"
              >({{ formatSigned(fc.monthly_growth_low) }} –
              {{ formatSigned(fc.monthly_growth_high) }})</span
            >
          </dd>
        </div>
        <div>
          <dt class="text-muted text-xs">{{ t('capacity.detail.peak') }}</dt>
          <dd class="tabular">
            {{ bps(fc.current_peak_bps) }} → {{ bps(fc.peak_6m_bps) }}
            <span class="text-muted text-xs">{{ t('capacity.detail.in6m') }}</span>
          </dd>
        </div>
        <div>
          <dt class="text-muted text-xs">
            {{ t('capacity.detail.planning', { pct: formatPercent(fc.planning.threshold) }) }}
          </dt>
          <dd>{{ crossingText(fc.planning) }}</dd>
        </div>
        <div>
          <dt class="text-muted text-xs">
            {{ t('capacity.detail.urgent', { pct: formatPercent(fc.urgent.threshold) }) }}
          </dt>
          <dd>{{ crossingText(fc.urgent) }}</dd>
        </div>
      </dl>
      <p v-if="fc" class="text-muted mt-3 text-xs" data-testid="detail-method">
        {{
          t('capacity.detail.method', {
            days: fc.history_days,
            confidence: t(`capacity.confidence.${fc.confidence}`),
            lead: fc.lead_days,
          })
        }}
        <template v-if="fc.seasonality"> {{ t('capacity.detail.seasonality') }}</template>
      </p>
      <div class="mt-3 h-72">
        <CapacityForecastChart
          v-if="detail.daily.length"
          :daily="detail.daily"
          :forecast="fc"
          :capacity-bps="capacity"
          :planning="st.planning_threshold"
          :urgent="st.urgent_threshold"
          :label="chartLabel"
        />
        <p v-else class="text-muted flex h-full items-center justify-center text-sm">
          {{ t('capacity.detail.noDaily') }}
        </p>
      </div>
    </UCard>

    <UCard v-if="uplink" :ui="{ body: 'p-4 sm:p-4' }" data-testid="detail-capacity-history">
      <h2 class="text-highlighted text-base font-semibold">{{ t('capacity.history.title') }}</h2>
      <p class="text-muted mt-1 text-sm">{{ t('capacity.history.hint') }}</p>
      <ul v-if="history.length" class="mt-3 flex flex-col gap-2">
        <li
          v-for="p in history"
          :key="p.valid_from"
          class="flex flex-wrap items-baseline justify-between gap-2 text-sm"
          data-testid="capacity-period"
        >
          <span class="text-highlighted tabular font-semibold">{{
            periodBps(p.contracted_bps)
          }}</span>
          <span class="text-muted tabular">
            {{
              p.valid_to
                ? t('capacity.history.range', {
                    from: formatDay(p.valid_from),
                    to: formatDay(p.valid_to),
                  })
                : t('capacity.history.since', { from: formatDay(p.valid_from) })
            }}
          </span>
        </li>
      </ul>
      <p v-else class="text-muted mt-3 text-sm">{{ t('capacity.history.empty') }}</p>
    </UCard>

    <div v-if="probe" class="grid gap-4 lg:grid-cols-2">
      <UCard :ui="{ body: 'p-4 sm:p-4' }" data-testid="detail-probe-loss">
        <h3 class="text-highlighted mb-2 text-sm font-semibold">
          {{ t('capacity.probe.lossTitle', { target: probe.target }) }}
        </h3>
        <div class="h-48">
          <TimeSeriesChart
            :series="lossSeries"
            :label="t('capacity.probe.lossTitle', { target: probe.target })"
            scale="normal"
            :format="pctFmt"
            :min="0"
            :thresholds="[
              {
                value: st.loss_warning_pct,
                label: t('capacity.probe.threshold', { v: pctFmt(st.loss_warning_pct) }),
                level: 'warning',
              },
            ]"
          />
        </div>
      </UCard>
      <UCard :ui="{ body: 'p-4 sm:p-4' }" data-testid="detail-probe-rtt">
        <h3 class="text-highlighted mb-2 text-sm font-semibold">
          {{ t('capacity.probe.rttTitle', { target: probe.target }) }}
        </h3>
        <div class="h-48">
          <TimeSeriesChart
            :series="rttSeries"
            :label="t('capacity.probe.rttTitle', { target: probe.target })"
            scale="normal"
            :format="msFmt"
            :min="0"
            :thresholds="[
              {
                value: st.latency_warning_ms,
                label: t('capacity.probe.threshold', { v: msFmt(st.latency_warning_ms) }),
                level: 'warning',
              },
            ]"
          />
        </div>
      </UCard>
    </div>
  </div>
</template>
