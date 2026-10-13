<script setup lang="ts">
import type { Router, RouterLiveMetrics, RouterStatus } from '~~/types/api'
import type { TimeSeries } from '~/utils/chart-palette'
import { CPU_THRESHOLDS, MEMORY_THRESHOLDS, TEMP_THRESHOLDS } from '~/utils/router-health'

/**
 * Pantalla NOC de un solo router (`?scale=wall` en la ficha, frontend.md §7, §8.4): cabecera
 * grande con nombre, nodo, estado y razón; medidores grandes de CPU, memoria y temperatura
 * (números que ruedan), uptime y RTT; gráfica grande del enlace de subida (rx/tx, últimas
 * 6 h) y la tabla de interfaces físicas en una línea por fila (`v-fit-rows`). Sin nada que
 * dependa del puntero; tipografía escalada con `--u` (1920×1080 como diseño base).
 */
const props = defineProps<{
  router: Router
  siteName: string
  tenantName: string
  live: RouterLiveMetrics | null
  liveAt: number | null
  /** Estado de respaldo si aún no hay muestra en vivo. */
  fallback: RouterStatus | null
}>()
const { t, te } = useI18n()
const { $api } = useNuxtApp()
const tr = (key: string, fallback: string) => (te(key) ? t(key) : fallback)

const status = computed(() => props.live?.status ?? props.fallback ?? null)
const d = computed(() => status.value?.device ?? null)
const visual = computed(() => routerStatusVisual(status.value?.status ?? 'unknown'))
const uplinkId = computed(() => status.value?.uplink?.interface_id ?? null)
const uplinkName = computed(() => status.value?.uplink?.name ?? '')

const { data: series, refresh } = useTenantQuery(
  () => `router:${props.router.id}:wall:${uplinkId.value ?? ''}`,
  () =>
    uplinkId.value
      ? unwrap(
          $api.GET('/analytics/routers/{router_id}/interfaces/metrics', {
            params: {
              path: { router_id: props.router.id },
              query: { range: '6h', metrics: 'rx_bps,tx_bps', interface_id: uplinkId.value },
            },
          }),
        ).catch(() => null)
      : Promise.resolve(null),
  { watch: [uplinkId] },
)
useAutoRefresh(() => refresh(), 60_000)
const chart = computed<TimeSeries[]>(() =>
  (series.value?.data.series ?? []).map((s) => ({
    name: s.metric === 'rx_bps' ? t('router.interfaces.rx') : t('router.interfaces.tx'),
    points: s.points as TimeSeries['points'],
  })),
)
const bpsFormat = (v: number) => joinUnit(formatBps(v))
const bps = (v: number | null | undefined) =>
  v === null || v === undefined ? '—' : joinUnit(formatBps(v))

/** Interfaces físicas (ethernet, sfp) con su muestra, las caídas primero. */
const physical = computed(() =>
  (props.live?.interfaces ?? [])
    .filter((i) => /^(ether|sfp|wlan)/.test(i.name))
    .sort(
      (a, b) =>
        Number(b.oper_status !== 'up') - Number(a.oper_status !== 'up') || a.if_index - b.if_index,
    ),
)
const operDot = (s: string | null | undefined) =>
  s === 'up'
    ? 'bg-success'
    : s === 'down' || s === 'lower_layer_down'
      ? 'bg-error'
      : 'bg-(--ui-border-accented)'
const now = useNow()
const updatedAt = computed(
  () => props.liveAt ?? (d.value?.sampled_at ? new Date(d.value.sampled_at).getTime() : null),
)
void now
</script>

<template>
  <div
    class="dash-wall flex h-dvh flex-col gap-[var(--w-gap)]"
    :data-router-status="status?.status ?? 'unknown'"
    data-testid="router-wall"
  >
    <header
      class="widget-band flex min-w-0 items-center justify-between gap-[1em] py-[calc(var(--w-pad)*0.6)]"
      data-layout-box
    >
      <div class="flex min-w-0 items-baseline gap-[0.8em]">
        <h1
          class="text-highlighted truncate font-mono leading-tight font-semibold"
          :style="{ fontSize: 'calc(var(--u) * 56)' }"
        >
          {{ router.name }}
        </h1>
        <p class="w-label text-muted truncate">{{ siteName }} · {{ tenantName }}</p>
      </div>
      <div class="flex shrink-0 items-center gap-[1em]">
        <RouterStatusBadge
          v-if="status"
          :status="status.status"
          :reason="status.reason"
          :since="status.since"
          class="w-kpi-2 font-semibold"
        />
        <FreshnessLabel v-if="updatedAt" :at="updatedAt" :expected-seconds="60" :live="!!liveAt" />
      </div>
    </header>

    <section class="grid grid-cols-5 gap-[var(--w-gap)]" :aria-label="t('router.tabs.metrics')">
      <div
        class="surface-card w-label flex flex-col justify-center gap-[0.3em] p-[var(--w-pad)]"
        :class="{ 'halo-critical': visual.halo }"
        data-layout-box
      >
        <MeterBar
          :label="t('router.live.cpu')"
          :value="d?.cpu_percent"
          :thresholds="CPU_THRESHOLDS"
          :format="(v) => formatPct(v)"
          rolling
          class="[&>div:first-child>span:last-child]:w-kpi-2"
        />
      </div>
      <div
        class="surface-card w-label flex flex-col justify-center gap-[0.3em] p-[var(--w-pad)]"
        data-layout-box
      >
        <MeterBar
          :label="t('router.live.memory')"
          :value="d?.memory_percent"
          :thresholds="MEMORY_THRESHOLDS"
          :format="(v) => formatPct(v)"
          rolling
          class="[&>div:first-child>span:last-child]:w-kpi-2"
        />
      </div>
      <div
        class="surface-card w-label flex flex-col justify-center gap-[0.3em] p-[var(--w-pad)]"
        data-layout-box
      >
        <MeterBar
          :label="t('router.live.temperature')"
          :value="d?.temperature_celsius"
          :thresholds="TEMP_THRESHOLDS"
          :format="(v) => formatCelsius(v)"
          rolling
          class="[&>div:first-child>span:last-child]:w-kpi-2"
        />
      </div>
      <div class="surface-card flex flex-col justify-center p-[var(--w-pad)]" data-layout-box>
        <p class="w-label text-muted">{{ t('router.live.uptime') }}</p>
        <p class="w-kpi-2 text-highlighted tabular">
          <RollingNumber :value="formatUptime(d?.uptime_seconds)" />
        </p>
      </div>
      <div class="surface-card flex flex-col justify-center p-[var(--w-pad)]" data-layout-box>
        <p class="w-label text-muted">{{ t('router.live.rtt') }}</p>
        <p class="w-kpi-2 text-highlighted tabular">
          <RollingNumber :value="formatMs(d?.icmp_rtt_ms)" />
        </p>
      </div>
    </section>

    <section
      class="surface-card flex min-h-0 flex-1 flex-col gap-[calc(var(--w-gap)*0.5)] p-[var(--w-pad)]"
      :aria-label="t('router.wallUplink')"
      data-layout-box
    >
      <h2 class="w-title text-highlighted font-semibold">
        {{ t('router.wallUplink') }}
        <span class="text-muted font-mono font-normal">{{ uplinkName }}</span>
      </h2>
      <div class="min-h-0 flex-1">
        <TimeSeriesChart
          v-if="chart.length"
          :series="chart"
          :label="t('router.wallUplink')"
          scale="wall"
          :format="bpsFormat"
          area
        />
        <p v-else class="w-label text-muted">{{ t('router.metrics.empty') }}</p>
      </div>
    </section>

    <section
      class="surface-card flex max-h-[38%] min-h-0 flex-col gap-[calc(var(--w-gap)*0.5)] p-[var(--w-pad)]"
      :aria-label="t('router.wallInterfaces')"
      data-layout-box
    >
      <h2 class="w-title text-highlighted font-semibold">{{ t('router.wallInterfaces') }}</h2>
      <ul
        v-fit-rows
        class="w-label min-h-0 flex-1 overflow-hidden"
        data-testid="router-wall-interfaces"
      >
        <li
          v-for="i in physical"
          :key="i.interface_id"
          class="grid grid-cols-[minmax(0,1.3fr)_minmax(0,1.6fr)_minmax(0,1fr)_minmax(0,1.3fr)_minmax(0,1.3fr)_minmax(0,0.8fr)] items-center gap-[1em] whitespace-nowrap tabular border-default border-t py-[0.2em] first:border-t-0"
          :data-interface="i.name"
          data-fit-item
        >
          <span class="text-highlighted truncate font-mono font-medium">{{ i.name }}</span>
          <span class="text-muted truncate">{{ i.alias ?? '' }}</span>
          <span class="inline-flex items-center gap-[0.4em]">
            <span
              class="size-[0.45em] shrink-0 rounded-full"
              :class="operDot(i.oper_status)"
              aria-hidden="true"
            />
            {{
              tr(
                `router.interfaces.operStates.${i.oper_status ?? 'unknown'}`,
                String(i.oper_status ?? '—'),
              )
            }}
          </span>
          <span class="text-end">↓ {{ bps(i.rx_bps) }}</span>
          <span class="text-end">↑ {{ bps(i.tx_bps) }}</span>
          <span
            class="text-muted text-end"
            :class="{ 'text-warning': (i.rx_errors_delta ?? 0) > 0 }"
          >
            {{ (i.rx_errors_delta ?? 0) + (i.tx_errors_delta ?? 0) }} err
          </span>
        </li>
      </ul>
    </section>
  </div>
</template>
