<script setup lang="ts">
import type { TrafficNowValues } from '../shapes'
import type { WidgetViewProps } from '../props'

/**
 * `traffic_now` — ¿Cuánto tráfico pasa ahora? Bajada como valor principal, subida al lado,
 * variación frente a ayer y sparkline de la última hora (oculta en mural). Se actualiza en
 * vivo con el tema WS `traffic.summary` (C6) y avisa al host de cada dato (`live`), que
 * muestra su frescura ("En vivo · hace 3 s").
 */
const props = defineProps<WidgetViewProps>()
const emit = defineEmits<{ live: [at: number] }>()
const { t } = useI18n()

const base = computed(() =>
  props.data?.kind === 'state' ? (props.data.values as unknown as TrafficNowValues) : null,
)
const live = ref<{ down_bps: number; up_bps: number } | null>(null)

useRealtime('traffic.summary', (message) => {
  const d = message.data as { down_bps?: number; up_bps?: number; partial?: boolean }
  if (typeof d.down_bps !== 'number' || typeof d.up_bps !== 'number') return
  live.value = { down_bps: d.down_bps, up_bps: d.up_bps }
  emit('live', new Date(message.time).getTime())
})

const down = computed(() => live.value?.down_bps ?? base.value?.down_bps ?? null)
const up = computed(() => live.value?.up_bps ?? base.value?.up_bps ?? null)
const downF = computed(() => formatBps(down.value))
const upF = computed(() => formatBps(up.value))
const downChange = computed(() => formatChange(down.value, base.value?.down_bps_yesterday ?? null))
const upChange = computed(() => formatChange(up.value, base.value?.up_bps_yesterday ?? null))

const spark = computed(() => {
  const s = base.value?.sparkline
  if (!s || s.down_bps.length === 0) return null
  return [
    { name: t('widgets.traffic_now.down'), values: s.down_bps },
    { name: t('widgets.traffic_now.up'), values: s.up_bps },
  ]
})
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col gap-[calc(var(--w-gap)*0.5)]">
    <div class="flex min-w-0 flex-wrap items-end gap-x-[calc(var(--w-gap)*2)] gap-y-1">
      <p class="text-highlighted min-w-0" data-testid="traffic-now-down">
        <span class="sr-only">{{ t('widgets.traffic_now.down') }}:</span>
        <span class="w-label text-muted me-1 align-top" aria-hidden="true">↓</span>
        <span class="w-kpi"
          >{{ downF.value }}<span class="w-unit">{{ downF.unit }}</span></span
        >
      </p>
      <div class="flex min-w-0 flex-col pb-[0.05em]" data-fit-optional="3">
        <p class="text-default">
          <span class="sr-only">{{ t('widgets.traffic_now.up') }}:</span>
          <span class="w-label text-muted me-1" aria-hidden="true">↑</span>
          <span class="w-kpi-2"
            >{{ upF.value }}<span class="w-unit">{{ upF.unit }}</span></span
          >
        </p>
        <p v-if="downChange || upChange" class="w-meta text-muted tabular" data-fit-optional="1">
          {{
            t('widgets.traffic_now.vsYesterday', { down: downChange ?? '—', up: upChange ?? '—' })
          }}
        </p>
      </div>
    </div>
    <div v-if="spark && scale === 'normal'" class="mt-auto h-10 min-h-0" data-fit-optional="2">
      <Sparkline :series="spark" :label="t('widgets.traffic_now.sparkLabel')" />
    </div>
  </div>
</template>
