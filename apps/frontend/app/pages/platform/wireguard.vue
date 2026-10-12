<script setup lang="ts">
import type { WireguardHub } from '~~/types/api'

/**
 * WireGuard (I1-31, frontend.md §3.4): cada hub con su endpoint, puerto, MTU, keepalive,
 * rangos de túneles y ocupación ("37 de 65 534 direcciones"). Con `platform.wireguard.manage`
 * (re-autenticación reciente): editar el hub y añadir o quitar rangos. Aviso cuando la
 * ocupación total supera el 80 %.
 */
const { t } = useI18n()
const { $api } = useNuxtApp()
useSectionGuard()(findSection('platform', '/platform/wireguard'))
const platformCan = usePlatformCan()
const canManage = computed(() => platformCan('platform.wireguard.manage'))

const {
  data: hubs,
  error,
  status,
  refresh,
} = useTenantQuery('platform:hubs', () =>
  unwrap($api.GET('/platform/wireguard/hubs')).then((r) => r.data),
)
const HUB_STATE = { up: 'ok', degraded: 'degraded', down: 'unavailable' } as const
/** Ocupación a partir de la cual se sugiere añadir un rango. */
const HIGH_OCCUPANCY = 0.8

const ratio = (hub: WireguardHub) =>
  hub.addresses_total > 0 ? hub.addresses_used / hub.addresses_total : 0

// Los modales reciben el hub vigente de la lista (tras cada recarga, su versión nueva).
const editingId = ref<string | null>(null)
const editing = computed(() => hubs.value?.find((h) => h.id === editingId.value) ?? null)
const editOpen = ref(false)
function edit(hub: WireguardHub) {
  editingId.value = hub.id
  editOpen.value = true
}

const addingId = ref<string | null>(null)
const adding = computed(() => hubs.value?.find((h) => h.id === addingId.value) ?? null)
const addOpen = ref(false)
function addPool(hub: WireguardHub) {
  addingId.value = hub.id
  addOpen.value = true
}
</script>

<template>
  <AppPage :title="t('nav.items.platformWireguard')" panel-id="platform-wireguard">
    <p class="text-muted text-sm" data-testid="wg-intro">
      {{ canManage ? t('platform.wireguard.intro') : t('platform.wireguard.readOnly') }}
    </p>
    <LoadingState v-if="status === 'pending' && !hubs" :rows="3" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <UCard v-for="hub in hubs ?? []" :key="hub.id" data-testid="wg-hub">
      <div class="flex flex-col gap-5">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div class="flex min-w-0 flex-wrap items-center gap-2">
            <h2 class="text-highlighted font-mono text-base font-semibold break-all">
              {{ hub.name }}
            </h2>
            <CapabilityBadge :state="HUB_STATE[hub.status]" />
          </div>
          <UButton
            v-if="canManage"
            size="sm"
            color="neutral"
            variant="outline"
            icon="i-lucide-pencil"
            :label="t('platform.wireguard.edit.open')"
            data-testid="wg-hub-edit"
            @click="edit(hub)"
          />
        </div>

        <UAlert
          v-if="ratio(hub) > HIGH_OCCUPANCY"
          color="warning"
          variant="subtle"
          icon="i-lucide-gauge"
          :title="t('platform.wireguard.highTitle', { percent: formatPercent(ratio(hub)) })"
          :description="
            canManage ? t('platform.wireguard.highBody') : t('platform.wireguard.highBodyReadOnly')
          "
          :actions="
            canManage
              ? [
                  {
                    label: t('platform.wireguard.pool.add'),
                    icon: 'i-lucide-plus',
                    color: 'warning',
                    variant: 'solid',
                    onClick: () => addPool(hub),
                  },
                ]
              : []
          "
          data-testid="wg-occupancy-alert"
        />

        <div>
          <p class="text-highlighted text-lg font-semibold tabular" data-testid="wg-occupancy">
            {{
              t('platform.wireguard.occupancy', {
                used: formatNumber(hub.addresses_used),
                total: formatNumber(hub.addresses_total),
              })
            }}
          </p>
          <UProgress
            :model-value="ratio(hub) * 100"
            :color="ratio(hub) > HIGH_OCCUPANCY ? 'warning' : 'primary'"
            class="mt-2"
            :aria-label="t('platform.wireguard.occupancyLabel')"
          />
        </div>
        <dl class="grid grid-cols-2 gap-x-4 gap-y-3 md:grid-cols-4">
          <div class="col-span-2 min-w-0 md:col-span-1">
            <dt class="text-muted text-xs">{{ t('platform.wireguard.endpoint') }}</dt>
            <dd class="text-highlighted font-mono text-sm break-all" data-testid="wg-endpoint">
              {{ hub.endpoint }}:{{ hub.listen_port }}
            </dd>
          </div>
          <div class="min-w-0">
            <dt class="text-muted text-xs">{{ t('platform.wireguard.mtu') }}</dt>
            <dd class="text-highlighted text-sm tabular" data-testid="wg-mtu">
              {{ hub.mtu ?? '—' }}
            </dd>
          </div>
          <div class="min-w-0">
            <dt class="text-muted text-xs">{{ t('platform.wireguard.keepalive') }}</dt>
            <dd class="text-highlighted text-sm tabular" data-testid="wg-keepalive">
              {{
                hub.persistent_keepalive_seconds == null
                  ? '—'
                  : hub.persistent_keepalive_seconds === 0
                    ? t('platform.wireguard.keepaliveOff')
                    : t('platform.wireguard.seconds', { n: hub.persistent_keepalive_seconds })
              }}
            </dd>
          </div>
          <div class="col-span-2 min-w-0 md:col-span-1">
            <dt class="text-muted text-xs">{{ t('platform.wireguard.peers') }}</dt>
            <dd class="text-highlighted text-sm tabular">
              {{
                t('platform.wireguard.peersValue', {
                  active: hub.peers_active ?? 0,
                  tenants: hub.tenants ?? 0,
                })
              }}
            </dd>
          </div>
          <div class="col-span-2 min-w-0 md:col-span-4">
            <dt class="text-muted text-xs">{{ t('platform.wireguard.publicKey') }}</dt>
            <dd class="flex flex-wrap items-center gap-2">
              <span class="text-highlighted font-mono text-sm break-all">{{ hub.public_key }}</span>
              <CopyButton :value="hub.public_key" size="xs" variant="ghost" />
            </dd>
          </div>
        </dl>

        <WireguardPools
          :hub="hub"
          :can-manage="canManage"
          @add="addPool(hub)"
          @changed="refresh()"
        />
      </div>
    </UCard>

    <WireguardHubEditModal
      v-if="editing"
      v-model:open="editOpen"
      :hub="editing"
      @saved="refresh()"
      @changed="refresh()"
    />
    <WireguardPoolAddModal v-if="adding" v-model:open="addOpen" :hub="adding" @added="refresh()" />
  </AppPage>
</template>
