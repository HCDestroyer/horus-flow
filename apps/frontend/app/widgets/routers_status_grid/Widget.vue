<script setup lang="ts">
import type { RouterStatusRow } from '../shapes'
import type { WidgetViewProps } from '../props'

/**
 * `routers_status_grid` — ¿Están vivos mis routers? Una tarjeta compacta por router con el
 * estado (icono + texto + color) y su razón, CPU, temperatura y tráfico del enlace de
 * subida. Los problemas primero; en mural, una línea por router y las que no caben se
 * ocultan enteras (`v-fit-rows`), con el resumen "N de M en línea" siempre visible.
 */
const props = defineProps<WidgetViewProps>()
const { t, te } = useI18n()

const rows = computed(() => {
  const list = props.data?.kind === 'table' ? (props.data.rows as unknown as RouterStatusRow[]) : []
  return sortRouterStatuses(list.map((r) => ({ ...r, name: r.router })))
})
const online = computed(() => rows.value.filter((r) => r.status === 'online').length)
const wall = computed(() => props.scale === 'wall')
const reason = (r: RouterStatusRow) =>
  r.reason && r.reason !== 'ok'
    ? te(`routerReason.${r.reason}`)
      ? t(`routerReason.${r.reason}`)
      : r.reason
    : null
const bps = (v: number | null) => (v === null ? '—' : joinUnit(formatBps(v)))
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col gap-[calc(var(--w-gap)*0.5)]">
    <p class="w-label text-muted" data-fit-optional="4">
      {{ t('widgets.routers_status_grid.summary', { ok: online, total: rows.length }) }}
    </p>
    <ul
      v-fit-rows
      class="min-h-0 flex-1 overflow-hidden"
      :class="
        wall
          ? 'flex flex-col gap-[calc(var(--w-gap)*0.35)]'
          : 'grid [grid-auto-rows:min-content] gap-[calc(var(--w-gap)*0.75)] [grid-template-columns:repeat(auto-fill,minmax(min(100%,12rem),1fr))]'
      "
      :aria-label="t('widgets.routers_status_grid.listLabel')"
    >
      <li
        v-for="row in rows"
        :key="row.router_id"
        class="bg-muted min-w-0 rounded-md px-[calc(var(--w-pad)*0.75)]"
        :class="[
          wall
            ? 'flex items-center gap-[0.8em] py-[calc(var(--w-pad)*0.25)] whitespace-nowrap'
            : 'flex flex-col gap-[0.2em] py-[calc(var(--w-pad)*0.45)]',
          routerStatusVisual(row.status).halo ? 'halo-critical' : '',
        ]"
        :data-router-status="row.status"
        data-layout-box
        data-fit-item
      >
        <div
          class="flex min-w-0 items-baseline justify-between gap-[0.6em]"
          :class="wall ? 'flex-1' : ''"
        >
          <span class="w-label text-highlighted truncate font-mono font-semibold">{{
            row.router
          }}</span>
          <RouterStatusBadge :status="row.status" compact class="w-label shrink-0" />
        </div>
        <p
          class="w-meta text-muted flex min-w-0 gap-x-[0.7em] tabular"
          :class="wall ? 'shrink-0 whitespace-nowrap' : 'flex-wrap'"
        >
          <span v-if="!wall && reason(row)" class="text-highlighted" data-fit-optional="1">{{
            reason(row)
          }}</span>
          <span v-if="row.cpu_percent !== null">CPU {{ formatPct(row.cpu_percent) }}</span>
          <span v-if="row.temperature_celsius !== null" data-fit-optional="2">{{
            formatCelsius(row.temperature_celsius)
          }}</span>
          <span v-if="row.rx_bps !== null" data-fit-optional="3"
            >↓ {{ bps(row.rx_bps) }} · ↑ {{ bps(row.tx_bps) }}</span
          >
          <span v-else-if="row.last_observed_at" class="truncate" data-fit-optional="3"
            ><RelativeTime :at="row.last_observed_at" :prefix="t('routersHealth.card.observed')"
          /></span>
        </p>
      </li>
    </ul>
  </div>
</template>
