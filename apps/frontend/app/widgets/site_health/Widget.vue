<script setup lang="ts">
import type { SiteHealthRow } from '../shapes'
import type { WidgetViewProps } from '../props'

/**
 * `site_health` — ¿Qué nodo está mal y por qué? Una línea por nodo con su salud (icono +
 * texto + color) y la razón en español que da el rol snmp («Degradado: enlace Proveedor A al
 * 92 % en hora pico…»), la peor utilización p95 y, si un enlace cruzará el umbral de
 * planificación, la fecha estimada (mm/aaaa). Peor salud primero; en mural se oculta la
 * fecha y las filas que no caben se ocultan enteras (`v-fit-rows`), con el resumen
 * "N de M sanos" siempre visible.
 */
const props = defineProps<WidgetViewProps>()
const { t } = useI18n()

const rows = computed(() => {
  const list = props.data?.kind === 'table' ? (props.data.rows as unknown as SiteHealthRow[]) : []
  return sortByHealth(list.map((r) => ({ ...r, name: r.site })))
})
const healthy = computed(() => rows.value.filter((r) => r.status === 'online').length)
const wall = computed(() => props.scale === 'wall')
const monthYear = (iso: string) => {
  const d = new Date(iso)
  return `${String(d.getUTCMonth() + 1).padStart(2, '0')}/${d.getUTCFullYear()}`
}
/** El resumen sin el prefijo de estado (el estado ya lo dice la insignia). */
const reason = (r: SiteHealthRow) => r.summary.replace(/^[^:]+:\s*/, '')
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col gap-[calc(var(--w-gap)*0.5)]">
    <p class="w-label text-muted" data-fit-optional="4" data-testid="site-health-summary">
      {{ t('widgets.site_health.summary', { ok: healthy, total: rows.length }) }}
    </p>
    <ul
      v-fit-rows
      class="flex min-h-0 flex-1 flex-col gap-[calc(var(--w-gap)*0.4)] overflow-hidden"
      :aria-label="t('widgets.site_health.listLabel')"
    >
      <li
        v-for="row in rows"
        :key="row.site_id || row.site"
        class="bg-muted min-w-0 rounded-md px-[calc(var(--w-pad)*0.75)]"
        :class="[
          wall
            ? 'flex items-center gap-[0.8em] py-[calc(var(--w-pad)*0.25)] whitespace-nowrap'
            : 'flex flex-col gap-[0.15em] py-[calc(var(--w-pad)*0.4)]',
          healthMeta(row.status).halo ? 'halo-critical' : '',
        ]"
        :data-health="row.status"
        data-layout-box
        data-fit-item
      >
        <div
          class="flex min-w-0 items-baseline justify-between gap-[0.6em]"
          :class="wall ? 'shrink-0' : ''"
        >
          <span class="w-label text-highlighted truncate font-semibold">{{ row.site }}</span>
          <SiteHealthBadge :status="row.status" compact class="w-label shrink-0" />
        </div>
        <p
          class="w-meta text-muted flex min-w-0 gap-x-[0.7em] tabular"
          :class="wall ? 'flex-1 overflow-hidden whitespace-nowrap' : 'flex-wrap'"
        >
          <span
            v-if="row.status !== 'online' && row.status !== 'unknown'"
            class="text-highlighted min-w-0 truncate"
            data-fit-optional="1"
            >{{ reason(row) }}</span
          >
          <span v-if="row.worst_util_p95 !== null"
            >{{ t('widgets.site_health.p95') }} {{ formatPercent(row.worst_util_p95) }}</span
          >
          <span v-if="row.uplinks_down" class="text-highlighted">{{
            t('widgets.site_health.down', { n: row.uplinks_down })
          }}</span>
          <span
            v-if="
              !wall &&
              row.next_crossing &&
              (row.forecast_level === 'planning' || row.forecast_level === 'urgent')
            "
            data-fit-optional="2"
            data-testid="site-health-forecast"
            >{{ t('widgets.site_health.crossing', { date: monthYear(row.next_crossing) }) }}</span
          >
        </p>
      </li>
    </ul>
  </div>
</template>
