<script setup lang="ts">
import type { Router, Site } from '~~/types/api'

/**
 * Nodos y routers (I1-19, frontend.md §8.4): los nodos del ISP (`GET /sites`), cada uno con su
 * router principal, estado del exportador y del túnel, clientes activos y estado de los
 * prefijos ("Sin prefijos · modo descubrimiento"). Un nodo sin routers invita a agregar el
 * primero. "Nuevo nodo" (sites.create) abre el alta; tras crearlo se ofrece agregar su router.
 * Estado en vivo por sondeo (30 s).
 */
const { t } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const can = useCan()
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
    const own = (routers.value ?? []).filter((r) => r.site_id === s.id)
    const rt = own.find((r) => r.id === s.primary_router_id) ?? own[0]
    return {
      site: s,
      router: rt,
      others: own.length - (rt ? 1 : 0),
      exporter: exporters.value?.find((e) => e.router_id === rt?.id),
      peer: peers.value?.find((p) => p.router_id === rt?.id),
      active: stats.value?.by_site.find((b) => b.site_id === s.id)?.active ?? 0,
    }
  }),
)

// --- Alta de nodo y del primer router ------------------------------------------------------
const canCreateSite = computed(() => can('sites.create'))
const canCreateRouter = computed(() => can('devices.create'))
const siteFormOpen = ref(false)
const routerFormOpen = ref(false)
const routerSite = ref<Site | null>(null)

function addRouter(site: Site) {
  routerSite.value = site
  routerFormOpen.value = true
}

function siteCreated(site: Site) {
  refresh()
  if (canCreateRouter.value) addRouter(site)
}

function routerSaved(_router: Router) {
  refreshRouters()
  refreshExporters()
  refresh()
}
</script>

<template>
  <AppPage :title="t('nav.items.nodes')" panel-id="nodes">
    <template v-if="canCreateSite && sites?.length" #toolbar-left>
      <p class="text-muted text-sm">{{ t('devices.nodes.intro') }}</p>
    </template>
    <template v-if="canCreateSite && sites?.length" #toolbar-right>
      <UButton
        icon="i-lucide-plus"
        :label="t('devices.nodes.add')"
        data-testid="add-node"
        @click="siteFormOpen = true"
      />
    </template>

    <LoadingState v-if="status === 'pending' && !sites" :rows="5" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <UCard v-else-if="!sites?.length" class="py-6" data-testid="nodes-empty">
      <EmptyState
        icon="i-lucide-network"
        :title="t('devices.nodes.emptyTitle')"
        :description="
          canCreateSite ? t('devices.nodes.emptyBody') : t('devices.nodes.emptyNoPermission')
        "
        :actions="
          canCreateSite
            ? [
                {
                  label: t('devices.nodes.addFirst'),
                  icon: 'i-lucide-plus',
                  onClick: () => (siteFormOpen = true),
                },
              ]
            : []
        "
      />
      <div v-if="canCreateSite" class="mx-auto mt-4 max-w-md">
        <SetupSteps current="site" />
      </div>
    </UCard>
    <ul v-else class="grid gap-3 md:grid-cols-2 2xl:grid-cols-3" data-testid="nodes-list">
      <li v-for="row in rows" :key="row.site.id">
        <UCard
          :ui="{ root: 'h-full', body: 'p-4 sm:p-4 flex flex-col gap-3' }"
          :data-node="row.site.name"
        >
          <div class="flex flex-wrap items-start justify-between gap-2">
            <NuxtLink
              :to="`/t/${slug}/nodes/${row.site.id}`"
              class="text-highlighted text-base font-semibold hover:underline"
            >
              {{ row.site.name }}
            </NuxtLink>
            <UBadge
              v-if="row.router && row.site.discovery_mode"
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
            <span v-if="row.others > 0" class="text-muted">
              {{ t('devices.nodes.otherRouters', { n: row.others }) }}
            </span>
          </div>
          <div v-else class="flex flex-col gap-2 text-sm" data-testid="node-no-routers">
            <p class="text-muted flex items-center gap-1.5">
              <UIcon name="i-lucide-router" class="size-4 shrink-0" aria-hidden="true" />
              {{ t('devices.nodes.noRouters') }}
            </p>
            <UButton
              v-if="canCreateRouter"
              icon="i-lucide-plus"
              :label="t('devices.nodes.addFirstRouter')"
              class="self-start"
              data-testid="node-add-router"
              @click="addRouter(row.site)"
            />
          </div>
          <p v-if="row.router" class="text-muted flex flex-wrap gap-x-3 text-sm">
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

    <SiteFormModal v-model:open="siteFormOpen" @saved="siteCreated" />
    <RouterFormModal
      v-model:open="routerFormOpen"
      :site-id="routerSite?.id ?? null"
      :site-has-primary="!!routerSite?.primary_router_id"
      @saved="routerSaved"
    />
  </AppPage>
</template>
