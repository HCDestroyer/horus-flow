<script setup lang="ts">
import type { TopRow } from '../shapes'
import { maxListRows, type WidgetViewProps } from '../props'

/** PLANTILLA (ver manifest.ts): un top N con `BarList`, válido en escala normal y mural. */
const props = defineProps<WidgetViewProps>()
const { t } = useI18n()
const items = computed(() =>
  props.data?.kind === 'table'
    ? (props.data.rows as unknown as TopRow[])
        .slice(0, maxListRows(props.scale, props.rows))
        .map((r) => ({
          label: r.label,
          value: r.down_bytes,
          display: joinUnit(formatBytes(r.down_bytes)),
        }))
    : [],
)
</script>

<template>
  <BarList :items="items" :label="t('widgets.top_services.description')" />
</template>
