<script setup lang="ts">
import type { FlowExporter, Peer, Router } from '~~/types/api'

/**
 * Estado del exportador y del túnel de un router (frontend.md §8.4, §13.2): estado con desde
 * cuándo, último flujo, flujos/s, pérdida y handshake. Si está *Silencioso*, explica desde
 * cuándo y qué revisar (`hints` del contrato, traducidos).
 */
const props = defineProps<{ router: Router; exporter: FlowExporter | null; peer: Peer | null }>()
const { t, te, d } = useI18n()

const silent = computed(() => props.exporter?.state === 'silent')
const facts = computed(() => {
  const e = props.exporter
  const p = props.peer
  return [
    { key: 'lastFlow', value: e?.last_flow_at ?? null, time: true },
    { key: 'fps', value: e?.flows_per_second != null ? formatNumber(e.flows_per_second) : '—' },
    { key: 'loss', value: e?.loss_ratio_5m != null ? formatPercent(e.loss_ratio_5m, 1) : '—' },
    { key: 'source', value: e?.flow_source ? e.flow_source.toUpperCase() : '—' },
    { key: 'handshake', value: p?.last_handshake_at ?? null, time: true },
    { key: 'tunnelIp', value: props.router.tunnel_address ?? '—', mono: true },
    {
      key: 'routeros',
      value: props.router.routeros_version_detected ?? props.router.routeros_version ?? '—',
    },
    { key: 'model', value: props.router.model ?? '—' },
  ]
})
const hint = (h: string) => (te(`router.hints.${h}`) ? t(`router.hints.${h}`) : h)
</script>

<template>
  <div class="flex flex-col gap-4">
    <UAlert
      v-if="silent && exporter"
      color="error"
      variant="subtle"
      icon="i-lucide-wifi-off"
      :title="t('router.silentTitle', { since: d(new Date(exporter.state_since), 'long') })"
      data-testid="router-silent"
    >
      <template #description>
        <p>{{ t('router.silentBody') }}</p>
        <ul class="mt-2 list-disc space-y-1 ps-5">
          <li v-for="h in exporter.hints ?? []" :key="h">{{ hint(h) }}</li>
        </ul>
      </template>
    </UAlert>
    <UAlert
      v-for="w in router.warnings"
      :key="w"
      color="warning"
      variant="subtle"
      icon="i-lucide-triangle-alert"
      :title="t(`router.warnings.${w}`)"
    />
    <dl class="grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-4">
      <div v-for="f in facts" :key="f.key" class="min-w-0">
        <dt class="text-muted text-xs">{{ t(`router.facts.${f.key}`) }}</dt>
        <dd
          class="text-highlighted font-medium break-words"
          :class="{ 'font-mono text-sm': f.mono }"
        >
          <RelativeTime v-if="f.time" :at="f.value" />
          <template v-else>{{ f.value }}</template>
        </dd>
      </div>
    </dl>
  </div>
</template>
