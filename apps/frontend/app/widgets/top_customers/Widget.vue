<script setup lang="ts">
import type { TopCustomerRow } from '../shapes'
import { maxListRows, type WidgetViewProps } from '../props'

/**
 * `top_customers` — ¿Quién consume más? IP (monoespaciada) o alias, tipo y volumen ↓/↑.
 * Dato personal (C9 `contains_personal_data`): si el servidor enmascaró las IPs
 * (`meta.masked_personal_data`), se dice; en mural se oculta el nodo y la subida.
 */
const props = defineProps<WidgetViewProps>()
const { t } = useI18n()
const rows = computed(() =>
  props.data?.kind === 'table'
    ? (props.data.rows as unknown as TopCustomerRow[]).slice(
        0,
        maxListRows(props.scale, props.rows),
      )
    : [],
)
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col gap-[calc(var(--w-gap)*0.5)] overflow-hidden">
    <div v-fit-rows class="min-h-0 flex-1 overflow-hidden">
      <table class="w-label w-full table-fixed border-collapse">
        <caption class="sr-only">
          {{
            t('widgets.top_customers.caption')
          }}
        </caption>
        <thead class="w-meta text-muted text-start">
          <tr>
            <th scope="col" class="w-[46%] pb-1 text-start font-medium">
              {{ t('widgets.top_customers.customer') }}
            </th>
            <th v-if="scale === 'normal'" scope="col" class="pb-1 text-start font-medium">
              {{ t('widgets.top_customers.site') }}
            </th>
            <th scope="col" class="pb-1 text-end font-medium">↓</th>
            <th v-if="scale === 'normal'" scope="col" class="pb-1 text-end font-medium">↑</th>
          </tr>
        </thead>
        <tbody>
          <tr
            data-fit-item
            v-for="row in rows"
            :key="row.customer_ip + row.site"
            class="border-default border-t"
          >
            <td class="truncate py-[0.3em] pe-2">
              <span v-if="row.alias" class="text-highlighted">{{ row.alias }}</span>
              <span
                class="font-mono"
                :class="
                  row.alias
                    ? ['text-muted ms-1.5', scale === 'normal' ? 'text-[0.85em]' : '']
                    : 'text-highlighted'
                "
                >{{ row.customer_ip }}</span
              >
              <UIcon
                v-if="row.kind === 'commercial'"
                name="i-lucide-building-2"
                class="text-info ms-1.5 size-[0.9em] align-[-0.1em]"
                :aria-label="t('widgets.top_customers.commercial')"
              />
            </td>
            <td v-if="scale === 'normal'" class="text-muted truncate py-[0.3em] pe-2">
              {{ row.site }}
            </td>
            <td class="py-[0.3em] text-end tabular">
              <span class="text-highlighted font-medium">{{
                joinUnit(formatBytes(row.down_bytes))
              }}</span>
            </td>
            <td v-if="scale === 'normal'" class="text-muted py-[0.3em] text-end tabular">
              {{ joinUnit(formatBytes(row.up_bytes)) }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-if="meta?.masked_personal_data" class="w-meta text-muted mt-auto flex items-center gap-1">
      <UIcon name="i-lucide-eye-off" class="size-[1.1em]" aria-hidden="true" />
      {{ t('widgets.common.masked') }}
    </p>
  </div>
</template>
