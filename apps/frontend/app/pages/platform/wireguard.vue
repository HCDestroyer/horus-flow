<script setup lang="ts">
/**
 * WireGuard (I1-31, frontend.md §3.4; lectura en I1, gestión en I3): cada hub con su
 * endpoint, puerto, rango y ocupación ("37 de 65 534 direcciones").
 */
const { t } = useI18n()
const { $api } = useNuxtApp()
useSectionGuard()(findSection('platform', '/platform/wireguard'))

const {
  data: hubs,
  error,
  status,
  refresh,
} = useTenantQuery('platform:hubs', () =>
  unwrap($api.GET('/platform/wireguard/hubs')).then((r) => r.data),
)
const HUB_STATE = { up: 'ok', degraded: 'degraded', down: 'unavailable' } as const
</script>

<template>
  <AppPage :title="t('nav.items.platformWireguard')" panel-id="platform-wireguard">
    <p class="text-muted text-sm">{{ t('platform.wireguard.readOnly') }}</p>
    <LoadingState v-if="status === 'pending' && !hubs" :rows="3" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <UCard v-for="hub in hubs ?? []" :key="hub.id" data-testid="wg-hub">
      <div class="flex flex-col gap-4">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h2 class="text-highlighted font-mono text-base font-semibold">{{ hub.name }}</h2>
          <CapabilityBadge :state="HUB_STATE[hub.status]" />
        </div>
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
            :model-value="(hub.addresses_used / hub.addresses_total) * 100"
            class="mt-2"
            :aria-label="t('platform.wireguard.occupancyLabel')"
          />
        </div>
        <dl class="grid grid-cols-2 gap-x-4 gap-y-3 md:grid-cols-4">
          <div class="min-w-0">
            <dt class="text-muted text-xs">{{ t('platform.wireguard.endpoint') }}</dt>
            <dd class="text-highlighted font-mono text-sm break-all">
              {{ hub.endpoint }}:{{ hub.listen_port }}
            </dd>
          </div>
          <div class="min-w-0">
            <dt class="text-muted text-xs">{{ t('platform.wireguard.range') }}</dt>
            <dd class="text-highlighted font-mono text-sm">{{ hub.tunnel_cidr }}</dd>
          </div>
          <div class="min-w-0">
            <dt class="text-muted text-xs">{{ t('platform.wireguard.services') }}</dt>
            <dd class="text-highlighted font-mono text-sm">{{ hub.services_cidr ?? '—' }}</dd>
          </div>
          <div class="min-w-0">
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
      </div>
    </UCard>
  </AppPage>
</template>
