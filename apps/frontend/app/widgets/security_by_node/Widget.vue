<script setup lang="ts">
import type { SecurityByNodeRow } from '../shapes'
import { maxListRows, type WidgetViewProps } from '../props'

/** `security_by_node` — ¿Qué nodos tienen más clientes con señales? Sin IPs (agregado). */
const props = defineProps<WidgetViewProps>()
const { t } = useI18n()
const items = computed(() =>
  props.data?.kind === 'table'
    ? (props.data.rows as unknown as SecurityByNodeRow[])
        .slice(0, maxListRows(props.scale, props.rows))
        .map((r) => ({
          label: r.site,
          value: r.customers_with_signals,
          display: t('widgets.security_by_node.customers', r.customers_with_signals),
          detail:
            props.scale === 'normal'
              ? t('widgets.security_by_node.findings', r.open_findings)
              : undefined,
        }))
    : [],
)
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col overflow-hidden">
    <BarList :items="items" :label="t('widgets.security_by_node.listLabel')" />
  </div>
</template>
