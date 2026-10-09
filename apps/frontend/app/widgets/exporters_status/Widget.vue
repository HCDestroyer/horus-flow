<script setup lang="ts">
import type { ExporterRow } from '../shapes'
import type { WidgetViewProps } from '../props'

/**
 * `exporters_status` — ¿Llegan flujos de todos los routers? Una tarjeta por exportador con
 * estado (icono + texto + color), último flujo, flujos/s y pérdida. Los problemas primero.
 */
const props = defineProps<WidgetViewProps>()
const { t } = useI18n()
const now = useNow()

const ORDER: Record<string, number> = {
  silent: 0,
  clock_skew: 1,
  lossy: 2,
  pending_configuration: 3,
}
const rows = computed(() => {
  const list = props.data?.kind === 'table' ? (props.data.rows as unknown as ExporterRow[]) : []
  return [...list].sort((a, b) => (ORDER[a.state] ?? 9) - (ORDER[b.state] ?? 9))
})
const healthy = computed(() => rows.value.filter((r) => r.state === 'exporting').length)

function ago(iso: string | null) {
  if (!iso) return t('widgets.exporters_status.noFlows')
  const p = agoParts((now.value - new Date(iso).getTime()) / 1000)
  return t(p.key, { n: p.n })
}
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col gap-[calc(var(--w-gap)*0.5)]">
    <p class="w-label text-muted" data-fit-optional="4">
      {{ t('widgets.exporters_status.summary', { ok: healthy, total: rows.length }) }}
    </p>
    <ul
      class="grid min-h-0 flex-1 auto-rows-fr gap-[calc(var(--w-gap)*0.75)] [grid-template-columns:repeat(auto-fit,minmax(min(100%,13rem),1fr))]"
      :aria-label="t('widgets.exporters_status.listLabel')"
    >
      <li
        v-for="row in rows"
        :key="row.router"
        class="bg-muted flex min-w-0 flex-col justify-center gap-0.5 rounded-md px-[calc(var(--w-pad)*0.75)] py-[calc(var(--w-pad)*0.4)]"
        :data-exporter-state="row.state"
      >
        <div class="flex min-w-0 items-baseline justify-between gap-2">
          <span class="w-label text-highlighted truncate font-mono font-semibold">{{
            row.router
          }}</span>
          <ExporterStateBadge :state="row.state" class="w-label" />
        </div>
        <p class="w-meta text-muted flex min-w-0 flex-wrap gap-x-2 tabular">
          <span v-if="scale === 'normal'" class="truncate" data-fit-optional="1">{{
            row.site
          }}</span>
          <span>{{ t('widgets.exporters_status.lastFlow', { ago: ago(row.last_flow_at) }) }}</span>
          <span v-if="row.flows_per_second !== null" data-fit-optional="3">
            {{ t('widgets.exporters_status.fps', { n: formatNumber(row.flows_per_second) }) }}
          </span>
          <span v-if="scale === 'normal' && row.loss_ratio !== null" data-fit-optional="2">
            {{ t('widgets.exporters_status.loss', { pct: formatPercent(row.loss_ratio, 1) }) }}
          </span>
        </p>
      </li>
    </ul>
  </div>
</template>
