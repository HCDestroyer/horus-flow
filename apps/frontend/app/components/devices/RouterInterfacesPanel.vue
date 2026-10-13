<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'
import type {
  InterfaceSample,
  RelativeRange,
  RouterInterface,
  RouterLiveMetrics,
} from '~~/types/api'
import type { TimeSeries } from '~/utils/chart-palette'

/**
 * Pestaña Interfaces (frontend.md §8.4): inventario de `GET /routers/{id}/interfaces` unido a la
 * última muestra en vivo (nombre, alias, tipo, estado con punto de color, velocidad, rx/tx con
 * barras de utilización, errores y descartes del último intervalo, insignia "uplink", "no
 * sondeada" para las no monitoreadas). Al marcar hasta 4 interfaces se dibuja su rx/tx del
 * rango elegido (`GET /analytics/routers/{id}/interfaces/metrics`); la de subida va marcada
 * por defecto.
 */
const props = defineProps<{
  routerId: string
  range: RelativeRange
  live: RouterLiveMetrics | null
  canSeries: boolean
}>()
const { t, te } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
const tr = (key: string, fallback: string) => (te(key) ? t(key) : fallback)

const {
  data: inventory,
  error,
  status,
  refresh,
} = useTenantQuery(
  () => `router:${props.routerId}:interfaces`,
  () =>
    unwrap(
      $api.GET('/routers/{router_id}/interfaces', {
        params: { path: { router_id: props.routerId } },
      }),
    ),
  { watch: [() => props.routerId] },
)
useAutoRefresh(() => refresh(), 120_000)

interface Row {
  def: RouterInterface
  sample: InterfaceSample | null
}
const rows = computed<Row[]>(() => {
  const samples = new Map((props.live?.interfaces ?? []).map((s) => [s.interface_id, s]))
  return (inventory.value?.data ?? []).map((def) => ({ def, sample: samples.get(def.id) ?? null }))
})

const MAX = 4
const selected = ref<string[]>([])
watch(
  rows,
  (list) => {
    if (selected.value.length || !list.length) return
    const uplink = list.find((r) => r.def.uplink) ?? list[0]
    if (uplink) selected.value = [uplink.def.id]
  },
  { immediate: true },
)
function toggle(id: string) {
  if (selected.value.includes(id)) {
    selected.value = selected.value.filter((x) => x !== id)
    return
  }
  if (selected.value.length >= MAX) {
    toast.add({
      title: t('router.interfaces.maxSelected'),
      icon: 'i-lucide-info',
      color: 'neutral',
    })
    return
  }
  selected.value = [...selected.value, id]
}
const selectedKey = computed(() => [...selected.value].sort().join(','))

const {
  data: series,
  error: seriesError,
  status: seriesStatus,
  refresh: refreshSeries,
} = useTenantQuery(
  () => `router:${props.routerId}:ifseries:${props.range}:${selectedKey.value}`,
  () =>
    props.canSeries && selected.value.length
      ? unwrap(
          $api.GET('/analytics/routers/{router_id}/interfaces/metrics', {
            params: {
              path: { router_id: props.routerId },
              query: {
                range: props.range,
                metrics: 'rx_bps,tx_bps',
                interface_id: selectedKey.value,
              },
            },
          }),
        )
      : Promise.resolve(null),
  { watch: [() => props.range, selectedKey] },
)
useAutoRefresh(() => refreshSeries(), 60_000)

const chartSeries = computed<TimeSeries[]>(() =>
  (series.value?.data.series ?? []).map((s) => ({
    name: `${s.group ?? ''} ${s.metric === 'rx_bps' ? '↓' : '↑'}`,
    points: s.points as TimeSeries['points'],
  })),
)
const selectedNames = computed(() =>
  rows.value
    .filter((r) => selected.value.includes(r.def.id))
    .map((r) => r.def.name)
    .join(', '),
)
const bpsFormat = (v: number) => joinUnit(formatBps(v))
const timeFormat = computed(() => (props.range === '7d' || props.range === '30d' ? 'day' : 'time'))

const operDot = (s: string | null | undefined) =>
  s === 'up'
    ? 'bg-success'
    : s === 'down' || s === 'lower_layer_down'
      ? 'bg-error'
      : 'bg-(--ui-border-accented)'
const operLabel = (s: string | null | undefined) =>
  s ? tr(`router.interfaces.operStates.${s}`, s) : '—'
const typeLabel = (ty: string) => tr(`router.interfaces.types.${ty}`, ty)
const bps = (v: number | null | undefined) =>
  v === null || v === undefined ? '—' : joinUnit(formatBps(v))
const util = (v: number | null | undefined) =>
  v === null || v === undefined ? 0 : Math.min(100, v * 100)
const count = (v: number | null | undefined) =>
  v === null || v === undefined ? '—' : formatNumber(v)

const columns = computed<TableColumn<Row>[]>(() => [
  { id: 'select', header: () => '', meta: { class: { th: 'w-10 pe-0', td: 'w-10 pe-0' } } },
  { id: 'name', header: () => t('router.interfaces.name') },
  {
    id: 'type',
    header: () => t('router.interfaces.type'),
    meta: { class: { th: 'hidden md:table-cell', td: 'hidden md:table-cell' } },
  },
  {
    id: 'oper',
    header: () => t('router.interfaces.oper'),
    meta: { class: { th: 'hidden sm:table-cell', td: 'hidden sm:table-cell' } },
  },
  {
    id: 'speed',
    header: () => t('router.interfaces.speed'),
    meta: { class: { th: 'hidden text-end lg:table-cell', td: 'hidden text-end lg:table-cell' } },
  },
  {
    id: 'rx',
    header: () => t('router.interfaces.rx'),
    meta: { class: { th: 'text-end', td: 'text-end' } },
  },
  {
    id: 'tx',
    header: () => t('router.interfaces.tx'),
    meta: { class: { th: 'hidden text-end sm:table-cell', td: 'hidden text-end sm:table-cell' } },
  },
  {
    id: 'errors',
    header: () => t('router.interfaces.errors'),
    meta: { class: { th: 'hidden text-end md:table-cell', td: 'hidden text-end md:table-cell' } },
  },
  {
    id: 'drops',
    header: () => t('router.interfaces.drops'),
    meta: { class: { th: 'hidden text-end md:table-cell', td: 'hidden text-end md:table-cell' } },
  },
])
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="router-interfaces">
    <LoadingState v-if="status === 'pending' && !inventory" :rows="5" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <UCard v-else-if="!rows.length" class="py-4">
      <EmptyState icon="i-lucide-ethernet-port" :title="t('router.interfaces.empty')" />
    </UCard>
    <template v-else>
      <UCard :ui="{ header: 'p-4 sm:px-4', body: 'p-0 sm:p-0' }">
        <template #header>
          <div class="flex flex-wrap items-baseline justify-between gap-2">
            <p class="text-muted text-sm">
              {{ t('router.interfaces.discovered') }}
              <RelativeTime :at="inventory?.discovered_at" />
            </p>
            <p class="text-muted text-sm tabular" data-testid="interfaces-selected">
              {{ t('router.interfaces.selected', { n: selected.length }) }}
            </p>
          </div>
        </template>
        <UTable
          :data="rows"
          :columns="columns"
          class="w-full"
          :ui="{ td: 'px-3 py-2 sm:px-4', th: 'px-3 text-xs whitespace-nowrap sm:px-4' }"
          data-testid="interfaces-table"
        >
          <template #select-cell="{ row }">
            <UCheckbox
              :model-value="selected.includes(row.original.def.id)"
              :disabled="!canSeries"
              :aria-label="t('router.interfaces.select', { name: row.original.def.name })"
              :data-interface="row.original.def.name"
              data-testid="interface-select"
              @update:model-value="toggle(row.original.def.id)"
            />
          </template>
          <template #name-cell="{ row }">
            <span class="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5">
              <span class="text-highlighted font-mono font-medium">{{
                row.original.def.name
              }}</span>
              <span v-if="row.original.def.alias" class="text-muted text-xs">{{
                row.original.def.alias
              }}</span>
              <UBadge
                v-if="row.original.def.uplink"
                color="primary"
                variant="subtle"
                size="sm"
                :label="t('router.interfaces.uplink')"
                data-testid="interface-uplink"
              />
              <UBadge
                v-if="!row.original.def.monitored"
                color="neutral"
                variant="subtle"
                size="sm"
                :label="t('router.interfaces.notMonitored')"
              />
            </span>
          </template>
          <template #type-cell="{ row }">
            <span class="text-muted text-sm">{{ typeLabel(row.original.def.type) }}</span>
          </template>
          <template #oper-cell="{ row }">
            <span
              class="inline-flex items-center gap-1.5 text-sm whitespace-nowrap"
              :data-oper="row.original.def.oper_status"
            >
              <span
                class="size-2 rounded-full"
                :class="operDot(row.original.sample?.oper_status ?? row.original.def.oper_status)"
                aria-hidden="true"
              />
              {{ operLabel(row.original.sample?.oper_status ?? row.original.def.oper_status) }}
            </span>
          </template>
          <template #speed-cell="{ row }">
            <span class="text-muted tabular">{{ bps(row.original.def.speed_bps) }}</span>
          </template>
          <template #rx-cell="{ row }">
            <span class="tabular block whitespace-nowrap"
              >↓ {{ bps(row.original.sample?.rx_bps) }}</span
            >
            <span
              class="mt-1 block h-1 w-12 overflow-hidden rounded-full bg-(--viz-track) ms-auto sm:w-20"
              aria-hidden="true"
            >
              <span
                class="block h-full bg-(--viz-1)"
                :style="{ width: `${util(row.original.sample?.utilization_rx)}%` }"
              />
            </span>
            <span class="text-muted tabular block text-xs whitespace-nowrap sm:hidden"
              >↑ {{ bps(row.original.sample?.tx_bps) }}</span
            >
          </template>
          <template #tx-cell="{ row }">
            <span class="tabular block whitespace-nowrap"
              >↑ {{ bps(row.original.sample?.tx_bps) }}</span
            >
            <span
              class="mt-1 block h-1 w-12 overflow-hidden rounded-full bg-(--viz-track) ms-auto sm:w-20"
              aria-hidden="true"
            >
              <span
                class="block h-full bg-(--viz-1)"
                :style="{ width: `${util(row.original.sample?.utilization_tx)}%` }"
              />
            </span>
          </template>
          <template #errors-cell="{ row }">
            <span
              class="tabular"
              :class="{
                'text-warning font-medium': (row.original.sample?.rx_errors_delta ?? 0) > 0,
              }"
            >
              {{ count(row.original.sample?.rx_errors_delta) }} /
              {{ count(row.original.sample?.tx_errors_delta) }}
            </span>
          </template>
          <template #drops-cell="{ row }">
            <span
              class="tabular"
              :class="{
                'text-warning font-medium': (row.original.sample?.rx_drops_delta ?? 0) > 0,
              }"
            >
              {{ count(row.original.sample?.rx_drops_delta) }} /
              {{ count(row.original.sample?.tx_drops_delta) }}
            </span>
          </template>
        </UTable>
      </UCard>

      <UCard v-if="canSeries" :ui="{ body: 'p-4 sm:p-4' }" data-testid="interfaces-chart">
        <h3 class="text-highlighted mb-2 text-sm font-semibold">
          {{ t('router.interfaces.chartTitle') }}
          <span v-if="selectedNames" class="text-muted font-mono font-normal"
            >· {{ selectedNames }}</span
          >
        </h3>
        <p v-if="!selected.length" class="text-muted text-sm">
          {{ t('router.interfaces.noSelection') }}
        </p>
        <DegradedNotice v-else-if="seriesError" :error="seriesError" @retry="refreshSeries()" />
        <div v-else class="h-64">
          <LoadingState v-if="seriesStatus === 'pending' && !series" :rows="3" />
          <TimeSeriesChart
            v-else-if="chartSeries.length"
            :series="chartSeries"
            :label="
              t('router.interfaces.chartLabel', {
                names: selectedNames,
                range: t(`ranges.${range}`),
              })
            "
            scale="normal"
            :format="bpsFormat"
            :time-format="timeFormat"
          />
        </div>
      </UCard>
    </template>
  </div>
</template>
