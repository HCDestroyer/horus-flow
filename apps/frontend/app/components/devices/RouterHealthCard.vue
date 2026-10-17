<script setup lang="ts">
import { NuxtLink } from '#components'
import type { RouterStatus } from '~~/types/api'
import { CPU_THRESHOLDS, MEMORY_THRESHOLDS, TEMP_THRESHOLDS } from '~/utils/router-health'

/**
 * Tarjeta de un router en la vista NOC (frontend.md §8.4): nombre y nodo, estado con razón
 * y "desde hace", señales (ICMP, SNMP, túnel, exportador) como pastillas, medidores de CPU,
 * memoria y temperatura con umbral visible, uptime, RTT y el enlace de subida con rx/tx y
 * utilización, más un sparkline de la bajada del uplink en la última hora que se pide solo
 * cuando la tarjeta entra en pantalla (`IntersectionObserver`). En mural no hay enlace ni
 * nada que dependa del puntero; `v-fit-optional` oculta lo secundario si no cabe. El halo
 * solo marca lo crítico o caído.
 */
const props = withDefaults(
  defineProps<{
    row: RouterStatus
    scale?: WidgetScale
    /** Enlace a la ficha (solo en escala normal). */
    to?: string
    /** El usuario puede pedir series SNMP (`snmp.read`). */
    canSeries?: boolean
    /** Hora del último dato en vivo recibido para este router. */
    liveAt?: number | null
  }>(),
  { scale: 'normal', to: undefined, canSeries: true, liveAt: null },
)
const { t, te } = useI18n()
const { $api } = useNuxtApp()
const tr = (key: string, fallback: string) => (te(key) ? t(key) : fallback)

const wall = computed(() => props.scale === 'wall')
const visual = computed(() => routerStatusVisual(props.row.status))
const d = computed(() => props.row.device)
const hasSample = computed(() => d.value.sampled_at !== null && d.value.cpu_percent !== null)
const signals = computed(() =>
  (['icmp', 'snmp', 'wireguard', 'exporter'] as const).map((k) => {
    const raw = props.row.signals[k]
    const value = raw ?? 'unknown'
    const ok = value === 'ok' || value === 'exporting'
    const bad = ['fail', 'auth_failed', 'stale', 'never', 'silent'].includes(value)
    return {
      key: k,
      name: t(`routerSignal.${k}`),
      value:
        k === 'exporter'
          ? tr(`exporterState.${value}`, value)
          : tr(`routerSignal.values.${value}`, value),
      cls: ok ? 'text-success' : bad ? 'text-error' : 'text-muted',
      dot: ok ? 'bg-success' : bad ? 'bg-error' : 'bg-(--ui-border-accented)',
    }
  }),
)
const uplink = computed(() => props.row.uplink)
const speed = computed(() =>
  uplink.value?.speed_bps ? joinUnit(formatBps(uplink.value.speed_bps)) : null,
)

// Sparkline perezoso: se pide al entrar en pantalla, no 50 series de golpe.
const root = useTemplateRef<HTMLElement | { $el: HTMLElement }>('root')
const visible = ref(false)
onMounted(() => {
  // `root` es el componente (NuxtLink) en escala normal y un <article> en mural.
  const el = root.value && '$el' in root.value ? root.value.$el : root.value
  if (!el || typeof IntersectionObserver === 'undefined') {
    visible.value = true
    return
  }
  const io = new IntersectionObserver(
    (entries) => {
      if (entries.some((e) => e.isIntersecting)) {
        visible.value = true
        io.disconnect()
      }
    },
    { rootMargin: '160px' },
  )
  io.observe(el)
  onBeforeUnmount(() => io.disconnect())
})
const wantsSpark = computed(
  () => props.canSeries && !!uplink.value && props.row.status !== 'unknown',
)
const { data: spark, execute } = useTenantQuery(
  () => `router:${props.row.router_id}:spark`,
  () =>
    unwrap(
      $api.GET('/analytics/routers/{router_id}/interfaces/metrics', {
        params: {
          path: { router_id: props.row.router_id },
          query: { range: '1h', metrics: 'rx_bps', interface_id: uplink.value?.interface_id },
        },
      }),
    )
      .then(
        (r): (number | null)[] =>
          r.data.series
            .find((s) => s.metric === 'rx_bps')
            ?.points.map(([, v]) => (typeof v === 'number' ? v : null)) ?? [],
      )
      .catch((): (number | null)[] => []),
  { immediate: false },
)
watch(
  [visible, wantsSpark],
  ([v, w]) => {
    if (v && w) execute()
  },
  { immediate: true },
)
const sparkSeries = computed(() =>
  spark.value?.length ? [{ name: 'rx', values: spark.value }] : [],
)
</script>

<template>
  <component
    :is="to && !wall ? NuxtLink : 'article'"
    ref="root"
    v-fit-optional
    :to="to && !wall ? to : undefined"
    class="surface-card relative flex min-w-0 flex-col overflow-hidden"
    :class="[
      wall
        ? 'gap-[0.45em] p-[0.7em]'
        : 'gap-3 p-4 transition-shadow hover:shadow-lg focus-visible:outline-2',
      visual.halo ? 'halo-critical' : '',
    ]"
    :data-router-status="row.status"
    :data-router="row.name"
    data-testid="router-card"
    data-layout-box
  >
    <header class="flex min-w-0 items-start justify-between gap-[0.6em]">
      <div class="min-w-0">
        <p
          class="text-highlighted truncate font-mono font-semibold"
          :class="wall ? 'leading-tight' : 'text-base'"
          :style="wall ? { fontSize: 'var(--w-kpi-2)' } : undefined"
        >
          {{ row.name }}
        </p>
        <p class="text-muted truncate" :class="wall ? 'w-meta' : 'text-sm'">{{ row.site_name }}</p>
      </div>
      <FreshnessLabel
        v-if="liveAt"
        :at="liveAt"
        :expected-seconds="60"
        live
        :always="false"
        class="shrink-0"
      />
    </header>

    <RouterStatusBadge
      :status="row.status"
      :reason="row.reason"
      :since="row.since"
      :class="wall ? 'w-label' : 'text-sm'"
    />

    <ul
      class="flex flex-wrap gap-[0.4em]"
      :class="wall ? 'w-meta' : 'text-xs'"
      :aria-label="t('routersHealth.card.signals')"
      data-fit-optional="1"
    >
      <li
        v-for="s in signals"
        :key="s.key"
        class="bg-muted inline-flex items-center gap-[0.35em] rounded-full px-[0.6em] py-[0.15em] whitespace-nowrap"
        :data-signal="s.key"
      >
        <span class="size-[0.5em] shrink-0 rounded-full" :class="s.dot" aria-hidden="true" />
        <span class="text-muted">{{ s.name }}</span>
        <span class="font-medium" :class="s.cls">{{ s.value }}</span>
      </li>
    </ul>

    <template v-if="hasSample">
      <div
        class="grid gap-x-[1em] gap-y-[0.5em]"
        :class="wall ? 'w-label grid-cols-3' : 'grid-cols-1 text-sm sm:grid-cols-3'"
      >
        <MeterBar
          :label="t('routersHealth.card.cpu')"
          :value="d.cpu_percent"
          :thresholds="CPU_THRESHOLDS"
          :format="(v) => formatPct(v)"
          :rolling="wall"
        />
        <MeterBar
          :label="t('routersHealth.card.memory')"
          :value="d.memory_percent"
          :thresholds="MEMORY_THRESHOLDS"
          :format="(v) => formatPct(v)"
          :rolling="wall"
        />
        <MeterBar
          :label="t('routersHealth.card.temperature')"
          :value="d.temperature_celsius"
          :max="100"
          :thresholds="TEMP_THRESHOLDS"
          :format="(v) => formatCelsius(v)"
          :rolling="wall"
        />
      </div>
      <p
        class="text-muted flex flex-wrap gap-x-[1em] tabular"
        :class="wall ? 'w-meta' : 'text-xs'"
        data-fit-optional="4"
      >
        <span
          >{{ t('routersHealth.card.uptime') }}
          <span class="text-highlighted">{{ formatUptime(d.uptime_seconds) }}</span></span
        >
        <span
          >{{ t('routersHealth.card.rtt') }}
          <span class="text-highlighted">{{ formatMs(d.icmp_rtt_ms) }}</span></span
        >
        <span v-if="row.last_flow_at" data-testid="router-card-last-flow">
          <RelativeTime :at="row.last_flow_at" :prefix="t('routersHealth.card.lastFlow')" />
        </span>
        <span v-if="row.interfaces_up !== null && row.interfaces_total !== null">
          {{
            t('routersHealth.card.interfaces', {
              up: row.interfaces_up,
              total: row.interfaces_total,
            })
          }}
        </span>
      </p>
    </template>
    <p
      v-else
      class="text-muted"
      :class="wall ? 'w-label' : 'text-sm'"
      data-testid="router-card-nosample"
    >
      <template v-if="row.last_observed_at">
        <RelativeTime :at="row.last_observed_at" :prefix="t('routersHealth.card.observed')" />
      </template>
      <template v-else>{{ t('routersHealth.card.noSample') }}</template>
    </p>

    <div
      v-if="uplink"
      class="mt-auto flex min-w-0 flex-col gap-[0.3em]"
      :class="wall ? 'w-meta' : 'text-xs'"
      data-fit-optional="4"
    >
      <p class="flex min-w-0 flex-wrap items-baseline gap-x-[0.7em] tabular">
        <span class="text-muted">{{ t('routersHealth.card.uplink') }}</span>
        <span class="text-highlighted font-mono font-medium">{{ uplink.name }}</span>
        <span v-if="uplink.rx_bps !== null && uplink.rx_bps !== undefined" class="text-highlighted">
          ↓ {{ joinUnit(formatBps(uplink.rx_bps)) }} · ↑ {{ joinUnit(formatBps(uplink.tx_bps)) }}
        </span>
        <span v-else class="text-dimmed">—</span>
        <span
          v-if="uplink.utilization !== null && uplink.utilization !== undefined && speed"
          class="text-muted"
        >
          {{
            t('routersHealth.card.utilization', {
              pct: formatPercent(uplink.utilization, 1),
              speed,
            })
          }}
        </span>
      </p>
      <div
        v-if="sparkSeries.length"
        class="w-full"
        :class="wall ? 'h-[2.4em]' : 'h-8'"
        data-fit-optional="3"
        data-testid="router-sparkline"
      >
        <Sparkline
          :series="sparkSeries"
          :label="t('routersHealth.card.sparkline', { name: uplink.name })"
        />
      </div>
    </div>
  </component>
</template>
