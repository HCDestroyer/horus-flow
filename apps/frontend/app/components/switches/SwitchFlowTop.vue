<script setup lang="ts">
import type { SwitchFlowTop, SwitchPort } from '~~/types/api'

/**
 * Top por puerto a partir del sFlow del switch (`GET /switches/{id}/flows/top`): FUENTE
 * SECUNDARIA (docs/traffic-model.md §13). Siempre con el aviso de que nunca se suma al tráfico de
 * clientes ni de nodos. Las IP solo viajan en la respuesta, nunca en la URL. Sin sFlow (o en una
 * plataforma que no lo tiene) se explica por qué no hay datos.
 */
const props = defineProps<{
  switchId: string
  platform: string | null
  sflowEnabled: boolean
  ports: SwitchPort[]
}>()

const { t } = useI18n()
const { $api } = useNuxtApp()
const by = ref<SwitchFlowTop['by']>('src_ip')
const port = ref('0')

const byItems = computed(() =>
  (['src_ip', 'dst_ip', 'protocol'] as const).map((v) => ({
    label: t(`switches.top.by.${v}`),
    value: v,
  })),
)
const portItems = computed(() => [
  { label: t('switches.top.allPorts'), value: '0' },
  ...props.ports
    .filter((p) => p.monitored)
    .map((p) => ({ label: p.name, value: String(p.if_index) })),
])

const enabled = computed(() => props.sflowEnabled && sflowSupported(props.platform))
const { data, error, status, refresh } = useTenantQuery(
  () => `switch:${props.switchId}:top:${by.value}:${port.value}`,
  () =>
    enabled.value
      ? unwrap(
          $api.GET('/switches/{router_id}/flows/top', {
            params: {
              path: { router_id: props.switchId },
              query: { by: by.value, if_index: Number(port.value), window_minutes: 60, limit: 10 },
            },
          }),
        )
      : Promise.resolve(null),
  { watch: [by, port, enabled] },
)
useAutoRefresh(() => refresh(), 60_000)

const total = computed(() => Number(data.value?.total_bytes ?? 0))
const rows = computed(() =>
  (data.value?.data ?? []).map((r) => ({
    key: r.key,
    bytes: Number(r.bytes),
    share: total.value ? Number(r.bytes) / total.value : 0,
  })),
)
</script>

<template>
  <UCard data-testid="switch-top">
    <template #header>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <h2 class="text-highlighted font-semibold">{{ t('switches.top.title') }}</h2>
        <div v-if="enabled" class="flex flex-wrap gap-2">
          <USelect
            v-model="by"
            :items="byItems"
            class="w-44"
            :aria-label="t('switches.top.title')"
            data-testid="switch-top-by"
          />
          <USelect
            v-model="port"
            :items="portItems"
            class="w-48"
            :aria-label="t('switches.top.port')"
          />
        </div>
      </div>
    </template>
    <div class="flex flex-col gap-3">
      <UAlert
        color="info"
        variant="subtle"
        icon="i-lucide-info"
        :title="t('switches.top.secondary')"
        data-testid="switch-top-secondary"
      />
      <p
        v-if="!sflowSupported(platform)"
        class="text-muted text-sm"
        data-testid="switch-top-unsupported"
      >
        {{ t('switches.top.unsupported') }}
      </p>
      <p v-else-if="!sflowEnabled" class="text-muted text-sm" data-testid="switch-top-disabled">
        {{ t('switches.top.disabled') }}
      </p>
      <template v-else>
        <LoadingState v-if="status === 'pending' && !data" :rows="4" />
        <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
        <p v-else-if="!rows.length" class="text-muted text-sm">{{ t('switches.top.empty') }}</p>
        <ul
          v-else
          class="flex flex-col gap-2"
          :aria-label="t('switches.top.title')"
          data-testid="switch-top-list"
        >
          <li v-for="r in rows" :key="r.key" class="flex flex-col gap-1">
            <div class="flex items-baseline justify-between gap-3 text-sm">
              <span class="text-default truncate font-mono">{{ r.key }}</span>
              <span class="text-muted shrink-0 tabular">
                {{ joinUnit(formatBytes(r.bytes)) }} · {{ formatPercent(r.share) }}
              </span>
            </div>
            <UProgress :model-value="r.share * 100" size="xs" />
          </li>
        </ul>
      </template>
    </div>
  </UCard>
</template>
