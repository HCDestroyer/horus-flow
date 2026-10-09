<script setup lang="ts">
import type { BarItem } from '~/utils/chart-palette'
import type { TopRow } from '../shapes'
import { maxListRows, type WidgetViewProps } from '../props'

/**
 * `top_categories` — ¿Qué se consume? Top N (≤ 10) por volumen de bajada + "Otros".
 * Barras de magnitud en un solo tono; etiqueta y valor siempre visibles.
 */
const props = defineProps<WidgetViewProps>()
const { t } = useI18n()

const items = computed(() => {
  if (props.data?.kind !== 'table') return []
  const limit = maxListRows(props.scale, props.rows)
  const rows = (props.data.rows as unknown as TopRow[]).slice(0, limit - 1)
  const others = props.data.others as unknown as TopRow | null
  const all: BarItem[] = rows.map((r) => ({
    label: r.label,
    value: r.down_bytes,
    display: joinUnit(formatBytes(r.down_bytes)),
    detail: props.scale === 'normal' ? `↑ ${joinUnit(formatBytes(r.up_bytes))}` : undefined,
  }))
  if (others) {
    all.push({
      label: t('widgets.common.others'),
      value: others.down_bytes,
      display: joinUnit(formatBytes(others.down_bytes)),
      muted: true,
    })
  }
  return all
})
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col gap-[calc(var(--w-gap)*0.5)] overflow-hidden">
    <p class="w-meta text-muted">
      {{ t('widgets.common.downVolume', { range: String(widget.config.range ?? '24h') }) }}
    </p>
    <BarList :items="items" :label="t('widgets.top_categories.listLabel')" />
  </div>
</template>
