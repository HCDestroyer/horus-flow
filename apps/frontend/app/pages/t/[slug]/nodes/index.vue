<script setup lang="ts">
/**
 * Nodos y routers (I1-19, frontend.md §8.4): cada nodo con su router principal, estado del
 * exportador y del túnel, clientes activos y estado de los prefijos ("Sin prefijos · modo
 * descubrimiento"). Estado en vivo por sondeo (30 s).
 */
const { t } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const slug = computed(() => String(route.params.slug))

useSectionGuard()(findSection('tenant', 'nodes'), slug.value)

const { sites, status, error, refresh } = useSites()
const { data: routers, refresh: refreshRouters } = useTenantQuery('routers', () =>
  unwrap($api.GET('/routers', { params: { query: { limit: 200 } } })).then((r) => r.data),
)
const { data: exporters, refresh: refreshExporters } = useTenantQuery('exporters', () =>
  unwrap($api.GET('/flow-exporters')).then((r) => r.data),
)
const { data: peers } = useTenantQuery('peers', () =>
  unwrap($api.GET('/wireguard/peers', { params: { query: { limit: 200 } } })).then((r) => r.data),
)
const { data: stats } = useTenantQuery('customer-stats', () => unwrap($api.GET('/customers/stats')))

let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  timer = setInterval(() => {
    refreshRouters()
    refreshExporters()
  }, 30_000)
})
onBeforeUnmount(() => clearInterval(timer))

const rows = computed(() =>
  (sites.value ?? []).map((s) => {
    const rt = routers.value?.find((r) => r.id === s.primary_router_id)
    return {
      site: s,
      router: rt,
      exporter: exporters.value?.find((e) => e.router_id === rt?.id),
      peer: peers.value?.find((p) => p.router_id === rt?.id),
      active: stats.value?.by_site.find((b) => b.site_id === s.id)?.active ?? 0,
    }
  }),
)
</script>

<template>
  <AppPage :title="t('nav.items.nodes')" panel-id="nodes">
    <LoadingState v-if="status === 'pending' && !sites" :rows="5" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <ul v-else class="grid gap-3 md:grid-cols-2 2xl:grid-cols-3" data-testid="nodes-list">
      <li v-for="row in rows" :key="row.site.id">
        <UCard :ui="{ body: 'p-4 sm:p-4 flex flex-col gap-3' }" :data-node="row.site.name">
          <div class="flex flex-wrap items-start justify-between gap-2">
            <NuxtLink
              :to="`/t/${slug}/nodes/${row.site.id}`"
              class="text-highlighted text-base font-semibold hover:underline"
            >
              {{ row.site.name }}
            </NuxtLink>
            <UBadge
              v-if="row.site.discovery_mode"
              color="info"
              variant="subtle"
              icon="i-lucide-radar"
              :label="t('nodes.discovery')"
              data-testid="node-discovery"
            />
          </div>
          <div v-if="row.router" class="flex flex-col gap-1.5 text-sm">
            <NuxtLink
              :to="`/t/${slug}/routers/${row.router.id}`"
              class="text-default inline-flex items-center gap-1.5 font-mono hover:underline"
            >
              <UIcon name="i-lucide-router" class="text-muted size-4 shrink-0" aria-hidden="true" />
              {{ row.router.name }}
            </NuxtLink>
            <span class="flex flex-wrap items-center gap-x-3 gap-y-1">
              <ExporterStateBadge :state="row.exporter?.state ?? 'pending_configuration'" />
              <span v-if="row.exporter?.state === 'silent'" class="text-muted">
                <RelativeTime :at="row.exporter.state_since" :prefix="t('router.since')" />
              </span>
            </span>
            <span class="text-muted">
              {{ row.peer?.last_handshake_at ? t('router.handshake') : t('router.noHandshake') }}
              <RelativeTime v-if="row.peer?.last_handshake_at" :at="row.peer.last_handshake_at" />
            </span>
          </div>
          <p class="text-muted flex flex-wrap gap-x-3 text-sm">
            <span class="tabular">{{
              t('nodes.activeCustomers', { n: formatNumber(row.active) })
            }}</span>
            <span v-if="row.exporter?.flows_per_second" class="tabular">{{
              t('router.fps', { n: formatNumber(row.exporter.flows_per_second) })
            }}</span>
          </p>
          <UButton
            v-if="row.router?.onboarding_state === 'pending_configuration'"
            :to="`/t/${slug}/routers/${row.router.id}/connection`"
            icon="i-lucide-plug"
            :label="t('nodes.connect')"
            class="self-start"
            data-testid="node-connect"
          />
        </UCard>
      </li>
    </ul>
  </AppPage>
</template>
