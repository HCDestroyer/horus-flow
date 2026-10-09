<script setup lang="ts">
import type { WatchedPortRow } from '../shapes'
import { maxListRows, type WidgetViewProps } from '../props'

/**
 * `watched_ports` — ¿A qué puertos vigilados (23, 2323, 445, 7547, 8291…) sale tráfico y
 * desde cuántos clientes? Tabla compacta; en mural sin flujos ni protocolo.
 */
const props = defineProps<WidgetViewProps>()
const { t } = useI18n()
const rows = computed(() =>
  props.data?.kind === 'table'
    ? (props.data.rows as unknown as WatchedPortRow[]).slice(
        0,
        maxListRows(props.scale, props.rows),
      )
    : [],
)
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col overflow-hidden">
    <div v-fit-rows class="min-h-0 flex-1 overflow-hidden">
      <table class="w-label w-full border-collapse">
        <caption class="sr-only">
          {{
            t('widgets.watched_ports.caption')
          }}
        </caption>
        <thead class="w-meta text-muted">
          <tr>
            <th scope="col" class="pb-1 text-start font-medium">
              {{ t('widgets.watched_ports.port') }}
            </th>
            <th scope="col" class="pb-1 text-start font-medium">
              {{ t('widgets.watched_ports.service') }}
            </th>
            <th scope="col" class="pb-1 text-end font-medium">
              {{ t('widgets.watched_ports.customers') }}
            </th>
            <th v-if="scale === 'normal'" scope="col" class="pb-1 text-end font-medium">
              {{ t('widgets.watched_ports.flows') }}
            </th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="row in rows"
            data-fit-item
            :key="`${row.protocol}/${row.port}`"
            class="border-default border-t"
          >
            <td class="text-highlighted py-[0.3em] pe-2 font-mono tabular">
              {{ row.port
              }}<span v-if="scale === 'normal'" class="text-muted">/{{ row.protocol }}</span>
            </td>
            <td class="text-default truncate py-[0.3em] pe-2">{{ row.service }}</td>
            <td class="text-highlighted py-[0.3em] text-end font-semibold tabular">
              {{ row.customers }}
            </td>
            <td v-if="scale === 'normal'" class="text-muted py-[0.3em] text-end tabular">
              {{ formatNumber(row.flows) }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
