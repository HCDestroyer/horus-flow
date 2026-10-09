<script setup lang="ts">
import type { DropdownMenuItem, TabsItem } from '@nuxt/ui'

/**
 * Ficha del router (I1-19, frontend.md §8.4): cabecera con estado del exportador, túnel,
 * flujos/s; pestañas Resumen · Conexión (onboarding en tres pasos) · Prefijos de clientes ·
 * Tráfico. "Script de desinstalación" en "Más". La pestaña va en la ruta
 * (`/routers/:id/:tab`).
 */
const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const { $api } = useNuxtApp()
const can = useCan()
const { membership } = useTenant()
const slug = computed(() => String(route.params.slug))
const id = computed(() => String(route.params.routerId))

useSectionGuard()(findSection('tenant', 'nodes'), slug.value)

const {
  data: rt,
  error,
  status,
  refresh: refreshRouter,
} = useTenantQuery(
  () => `router:${id.value}`,
  () => unwrap($api.GET('/routers/{router_id}', { params: { path: { router_id: id.value } } })),
)
watch(error, (e) => {
  if (e instanceof ApiError && e.status === 404) {
    showError(createError({ statusCode: 404, statusMessage: 'Not Found' }))
  }
})
const { data: exporter, refresh: refreshExporter } = useTenantQuery(
  () => `router:${id.value}:exporter`,
  () =>
    unwrap($api.GET('/flow-exporters/{router_id}', { params: { path: { router_id: id.value } } })),
)
const { data: peer, refresh: refreshPeer } = useTenantQuery(
  () => `router:${id.value}:peer`,
  () =>
    unwrap($api.GET('/wireguard/peers', { params: { query: { router_id: id.value } } })).then(
      (r) => r.data[0] ?? null,
    ),
)
const { data: stats, refresh: refreshStats } = useTenantQuery('customer-stats', () =>
  unwrap($api.GET('/customers/stats')),
)
const { data: site } = useTenantQuery(
  () => `site:${rt.value?.site_id ?? ''}`,
  () =>
    rt.value
      ? unwrap($api.GET('/sites/{site_id}', { params: { path: { site_id: rt.value.site_id } } }))
      : Promise.resolve(null),
  { watch: [() => rt.value?.site_id] },
)

function refreshAll() {
  refreshRouter()
  refreshExporter()
  refreshPeer()
  refreshStats()
}

const activeCustomers = computed(
  () => stats.value?.by_site.find((s) => s.site_id === rt.value?.site_id)?.active ?? 0,
)

const TABS = ['summary', 'connection', 'prefixes', 'traffic'] as const
type Tab = (typeof TABS)[number]
const pending = computed(() => rt.value?.onboarding_state !== 'exporting')
const tab = computed<Tab>({
  get() {
    const raw = String(route.params.tab ?? '')
    if ((TABS as readonly string[]).includes(raw)) return raw as Tab
    return pending.value ? 'connection' : 'summary'
  },
  set(value) {
    router.replace(`/t/${slug.value}/routers/${id.value}/${value}`)
  },
})
const tabs = computed<TabsItem[]>(() => [
  { label: t('router.tabs.summary'), value: 'summary', icon: 'i-lucide-gauge' },
  { label: t('router.tabs.connection'), value: 'connection', icon: 'i-lucide-plug' },
  { label: t('router.tabs.prefixes'), value: 'prefixes', icon: 'i-lucide-list-tree' },
  { label: t('router.tabs.traffic'), value: 'traffic', icon: 'i-lucide-arrow-down-up' },
])

// "Script de desinstalación" (en "Más").
const uninstallOpen = ref(false)
const uninstall = ref<string | null>(null)
async function openUninstall() {
  uninstallOpen.value = true
  const res = await $api.POST('/routers/{router_id}/deprovisioning-script', {
    params: { path: { router_id: id.value } },
    parseAs: 'text',
  })
  uninstall.value = res.response.ok ? (res.data as unknown as string) : null
}
const more = computed<DropdownMenuItem[]>(() =>
  can('wireguard.write')
    ? [{ label: t('router.uninstall'), icon: 'i-lucide-file-x-2', onSelect: openUninstall }]
    : [],
)

const breadcrumb = computed(() => [
  { label: membership.value?.tenant_name ?? '', to: `/t/${slug.value}` },
  { label: t('nav.items.nodes'), to: `/t/${slug.value}/nodes` },
  ...(site.value
    ? [{ label: site.value.name, to: `/t/${slug.value}/nodes/${site.value.id}` }]
    : []),
  { label: rt.value?.name ?? '' },
])
useHead({ title: () => rt.value?.name ?? t('router.title') })
</script>

<template>
  <AppPage :title="rt?.name ?? t('router.title')" panel-id="router-detail">
    <template #navbar-right>
      <UDropdownMenu v-if="more.length" :items="more">
        <UButton
          color="neutral"
          variant="ghost"
          icon="i-lucide-ellipsis"
          :aria-label="t('common.more')"
          data-testid="router-more"
        />
      </UDropdownMenu>
    </template>
    <UBreadcrumb :items="breadcrumb" class="min-w-0" />
    <LoadingState v-if="status === 'pending' && !rt" :rows="5" />
    <ErrorState v-else-if="error" :error="error" @retry="refreshAll" />
    <template v-else-if="rt">
      <UCard data-testid="router-header">
        <div class="flex flex-col gap-2">
          <p class="flex flex-wrap items-center gap-x-4 gap-y-1">
            <ExporterStateBadge
              :state="exporter?.state ?? 'pending_configuration'"
              data-testid="router-exporter-state"
            />
            <span
              v-if="exporter && exporter.state !== 'pending_configuration'"
              class="text-muted text-sm"
            >
              <RelativeTime :at="exporter.state_since" :prefix="t('router.since')" />
            </span>
            <span class="text-muted text-sm">
              {{ peer?.last_handshake_at ? t('router.handshake') : t('router.noHandshake') }}
              <RelativeTime v-if="peer?.last_handshake_at" :at="peer.last_handshake_at" />
            </span>
            <span v-if="exporter?.flows_per_second" class="text-muted text-sm tabular">
              {{ t('router.fps', { n: formatNumber(exporter.flows_per_second) }) }}
            </span>
          </p>
          <p class="text-muted text-sm">
            {{ rt.display_name }} · {{ rt.model ?? '—' }} · {{ site?.name ?? '' }}
          </p>
        </div>
      </UCard>

      <UTabs v-model="tab" :items="tabs" :content="false" variant="link" class="w-full" />

      <UCard v-if="tab === 'summary'">
        <ExporterSummary :router="rt" :exporter="exporter ?? null" :peer="peer ?? null" />
      </UCard>
      <OnboardingWizard
        v-else-if="tab === 'connection'"
        :router="rt"
        :peer="peer ?? null"
        :exporter="exporter ?? null"
        :active-customers="activeCustomers"
        :discovery="site?.discovery_mode ?? false"
        @refresh="refreshAll"
      />
      <PrefixesPanel
        v-else-if="tab === 'prefixes'"
        :site-id="rt.site_id"
        :router-id="rt.id"
        :router-ready="rt.onboarding_state !== 'pending_configuration'"
      />
      <UCard v-else class="py-4">
        <EmptyState
          icon="i-lucide-arrow-down-up"
          :title="t('router.trafficTitle')"
          :description="t('router.trafficHint')"
          :actions="[
            {
              label: t('router.openTraffic'),
              to: `/t/${slug}/traffic?site=${rt.site_id}`,
              icon: 'i-lucide-chart-line',
            },
          ]"
        />
      </UCard>

      <UModal
        v-model:open="uninstallOpen"
        :title="t('router.uninstall')"
        :description="t('router.uninstallHint')"
      >
        <template #body>
          <CodeBlock
            v-if="uninstall"
            :code="uninstall"
            :label="t('router.uninstall')"
            :filename="`horus-baja-${rt.name}.rsc`"
          />
          <LoadingState v-else :rows="3" />
        </template>
      </UModal>
    </template>
  </AppPage>
</template>
