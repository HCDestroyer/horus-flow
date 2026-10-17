<script setup lang="ts">
import type { DropdownMenuItem, TabsItem } from '@nuxt/ui'
import type { RelativeRange } from '~~/types/api'
import { CPU_THRESHOLDS, TEMP_THRESHOLDS } from '~/utils/router-health'

/**
 * Ficha del router (I1-19, frontend.md §8.4): cabecera con estado del exportador, túnel,
 * flujos/s; pestañas Resumen · Conexión (onboarding en tres pasos) · Credenciales · Prefijos de
 * clientes · Tráfico. "Editar router" (If-Match), "Script de desinstalación" y "Eliminar
 * router" en la barra y en "Más". La pestaña va en la ruta (`/routers/:id/:tab`).
 * I2 (EP-08): estado efectivo del router en la cabecera (`RouterStatusBadge` con razón) y
 * CPU / memoria / temperatura / uptime en vivo (`router.{id}.metrics`), "Sondear ahora"
 * (`POST /routers/{id}/poll`, `devices.update`), pestañas Métricas e Interfaces y el modo
 * mural de un solo router (`?scale=wall`, `RouterWall`).
 * Huawei VRP (`connection_mode = direct`, docs/vendors/huawei.md): sin túnel WireGuard, la
 * pestaña Conexión es el alta directa (`DirectOnboardingWizard`) y no hay huella TLS, script de
 * desinstalación RouterOS ni importación de prefijos por la API REST.
 */
const { t, te } = useI18n()
const route = useRoute()
const router = useRouter()
const { $api } = useNuxtApp()
const can = useCan()
const toast = useToast()
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
  refreshLive()
}

// Estado efectivo y última muestra SNMP (en vivo). La lista de estados da el estado aunque el
// usuario no tenga `snmp.read` (solo necesita `devices.read`).
const canSnmp = computed(() => can('snmp.read'))
const { live, liveAt, refresh: refreshLive } = useRouterLive(id, canSnmp)
const { data: statusList } = useTenantQuery('routers:status', () =>
  unwrap($api.GET('/routers/status')),
)
const routerStatus = computed(
  () => live.value?.status ?? statusList.value?.data.find((r) => r.router_id === id.value) ?? null,
)
const liveSample = computed(() => routerStatus.value?.device ?? null)
const hasSample = computed(() => liveSample.value?.cpu_percent != null)

// "Sondear ahora": 202 en cola → toast; deshabilitado 10 s para no encolar en ráfaga.
const pollBusy = ref(false)
async function pollNow() {
  if (pollBusy.value) return
  pollBusy.value = true
  setTimeout(() => (pollBusy.value = false), 10_000)
  try {
    const res = await unwrap(
      $api.POST('/routers/{router_id}/poll', {
        params: { path: { router_id: id.value }, header: { 'Idempotency-Key': idempotencyKey() } },
      }),
    )
    if (res.queued) {
      toast.add({
        title: t('router.pollQueued'),
        description: t('router.pollQueuedBody'),
        icon: 'i-lucide-radar',
        color: 'success',
      })
    } else {
      const reason = res.reason ?? ''
      toast.add({
        title: t('router.pollNotQueued'),
        description: te(`router.pollReasons.${reason}`)
          ? t(`router.pollReasons.${reason}`)
          : reason,
        icon: 'i-lucide-triangle-alert',
        color: 'warning',
      })
    }
  } catch (e) {
    toast.add({
      title: t('router.pollNotQueued'),
      description: t(describeError(e).messageKey),
      icon: 'i-lucide-circle-alert',
      color: 'error',
    })
  }
}

// Rango de las pestañas Métricas e Interfaces, en la URL.
const RANGES = ['1h', '6h', '24h', '7d', '30d'] as const
const range = useQueryParam('range', '24h', RANGES)
const rangeOptions = computed(() => RANGES.map((r) => ({ label: t(`ranges.${r}`), value: r })))

// Mural de un solo router: layout `wall`, Esc vuelve.
const wall = computed(() => route.query.scale === 'wall')
setPageLayout(wall.value ? 'wall' : 'default')
watch(wall, (w) => setPageLayout(w ? 'wall' : 'default'))
const exitVisible = ref(false)
let hideTimer: ReturnType<typeof setTimeout> | undefined
function poke() {
  exitVisible.value = true
  clearTimeout(hideTimer)
  hideTimer = setTimeout(() => (exitVisible.value = false), 3000)
}
function exitWall() {
  const { scale: _s, ...rest } = route.query
  navigateTo({ path: route.path, query: rest })
}
function onKey(e: KeyboardEvent) {
  if (wall.value && e.key === 'Escape') exitWall()
}
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey)
  clearTimeout(hideTimer)
})

// MikroTik (túnel WireGuard + API REST) frente a Huawei en modo directo (solo SNMP y NetStream).
const direct = computed(() => rt.value?.connection_mode === 'direct')
const routerOS = computed(() => (rt.value?.vendor ?? 'mikrotik') === 'mikrotik')

const activeCustomers = computed(
  () => stats.value?.by_site.find((s) => s.site_id === rt.value?.site_id)?.active ?? 0,
)

const TABS = [
  'summary',
  'connection',
  'credentials',
  'prefixes',
  'metrics',
  'interfaces',
  'traffic',
] as const
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
  { label: t('router.tabs.credentials'), value: 'credentials', icon: 'i-lucide-key-round' },
  { label: t('router.tabs.prefixes'), value: 'prefixes', icon: 'i-lucide-list-tree' },
  { label: t('router.tabs.metrics'), value: 'metrics', icon: 'i-lucide-activity' },
  { label: t('router.tabs.interfaces'), value: 'interfaces', icon: 'i-lucide-ethernet-port' },
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
// Editar y eliminar el router.
const editOpen = ref(false)
const deleteOpen = ref(false)
const deleting = ref(false)
const deleteBlocked = ref<string | null>(null)

function openDelete() {
  deleteBlocked.value = null
  deleteOpen.value = true
}

async function remove() {
  if (!rt.value) return
  deleting.value = true
  deleteBlocked.value = null
  const siteId = rt.value.site_id
  try {
    await unwrap(
      $api.DELETE('/routers/{router_id}', {
        params: {
          path: { router_id: rt.value.id },
          header: { 'If-Match': ifMatch(rt.value.version) },
        },
      }),
    )
    deleteOpen.value = false
    toast.add({
      title: t('devices.router.deleted', { name: rt.value.name }),
      icon: 'i-lucide-check',
      color: 'success',
    })
    // La ficha del nodo y la lista se vuelven a pedir sin el router borrado.
    clearNuxtData((key) => /:(routers|sites|site:|exporters|peers)/.test(key))
    await navigateTo(`/t/${slug.value}/nodes/${siteId}`)
  } catch (e) {
    if (e instanceof ApiError && e.status === 412) {
      deleteBlocked.value = t('devices.router.deleteConflict')
      refreshRouter()
    } else {
      deleteBlocked.value = t(describeError(e).messageKey)
    }
  } finally {
    deleting.value = false
  }
}

function showUninstallFromDelete() {
  deleteOpen.value = false
  openUninstall()
}

const more = computed<DropdownMenuItem[]>(() => [
  ...(can('wireguard.write') && routerOS.value
    ? [{ label: t('router.uninstall'), icon: 'i-lucide-file-x-2', onSelect: openUninstall }]
    : []),
  ...(can('devices.delete')
    ? [
        {
          label: t('devices.router.delete'),
          icon: 'i-lucide-trash-2',
          color: 'error' as const,
          onSelect: openDelete,
        },
      ]
    : []),
])

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
  <div
    v-if="wall"
    class="relative h-dvh"
    :class="{ 'cursor-none': !exitVisible }"
    @pointermove="poke"
  >
    <RouterWall
      v-if="rt"
      :router="rt"
      :site-name="site?.name ?? ''"
      :tenant-name="membership?.tenant_name ?? ''"
      :live="live"
      :live-at="liveAt"
      :fallback="routerStatus"
    />
    <UButton
      color="neutral"
      variant="solid"
      icon="i-lucide-minimize-2"
      :label="t('router.exitWall')"
      class="absolute end-3 bottom-3 transition-opacity focus-visible:opacity-100"
      :class="exitVisible ? 'opacity-100' : 'opacity-0'"
      data-testid="exit-wall"
      @click="exitWall"
      @focus="poke"
    />
  </div>

  <AppPage v-else :title="rt?.name ?? t('router.title')" panel-id="router-detail">
    <template #navbar-right>
      <UButton
        v-if="rt && can('devices.update')"
        color="neutral"
        variant="outline"
        icon="i-lucide-radar"
        :aria-label="t('router.pollNow')"
        :disabled="pollBusy"
        data-testid="poll-now"
        @click="pollNow"
      >
        <span class="hidden sm:inline">{{ t('router.pollNow') }}</span>
      </UButton>
      <UButton
        v-if="rt"
        color="neutral"
        variant="outline"
        icon="i-lucide-maximize-2"
        :aria-label="t('router.wall')"
        :to="{ path: route.path, query: { ...route.query, scale: 'wall' } }"
        data-testid="open-wall"
      >
        <span class="hidden sm:inline">{{ t('router.wall') }}</span>
      </UButton>
      <UButton
        v-if="rt && can('devices.update')"
        color="neutral"
        variant="outline"
        icon="i-lucide-pencil"
        :aria-label="t('devices.router.edit')"
        data-testid="edit-router"
        @click="editOpen = true"
      >
        <span class="hidden sm:inline">{{ t('devices.router.edit') }}</span>
      </UButton>
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
          <div
            class="flex flex-col gap-2 lg:flex-row lg:items-center lg:justify-between"
            data-testid="router-health"
          >
            <RouterStatusBadge
              v-if="routerStatus"
              :status="routerStatus.status"
              :reason="routerStatus.reason"
              :since="routerStatus.since"
              class="text-base"
              data-testid="router-status-badge"
            />
            <div
              v-if="hasSample && liveSample"
              class="flex flex-wrap items-center gap-x-4 gap-y-1"
              data-testid="router-live"
            >
              <dl class="text-muted flex flex-wrap gap-x-4 gap-y-1 text-sm tabular">
                <div class="flex gap-1">
                  <dt>{{ t('router.live.cpu') }}</dt>
                  <dd
                    class="text-highlighted font-medium"
                    :class="{
                      'text-warning':
                        meterLevel(liveSample.cpu_percent, CPU_THRESHOLDS) === 'warning',
                      'text-error':
                        meterLevel(liveSample.cpu_percent, CPU_THRESHOLDS) === 'critical',
                    }"
                  >
                    <RollingNumber :value="formatPct(liveSample.cpu_percent)" />
                  </dd>
                </div>
                <div class="flex gap-1">
                  <dt>{{ t('router.live.memory') }}</dt>
                  <dd class="text-highlighted font-medium">
                    <RollingNumber :value="formatPct(liveSample.memory_percent)" />
                  </dd>
                </div>
                <div class="flex gap-1">
                  <dt>{{ t('router.live.temperature') }}</dt>
                  <dd
                    class="text-highlighted font-medium"
                    :class="{
                      'text-warning':
                        meterLevel(liveSample.temperature_celsius, TEMP_THRESHOLDS) === 'warning',
                      'text-error':
                        meterLevel(liveSample.temperature_celsius, TEMP_THRESHOLDS) === 'critical',
                    }"
                  >
                    <RollingNumber :value="formatCelsius(liveSample.temperature_celsius)" />
                  </dd>
                </div>
                <div class="flex gap-1">
                  <dt>{{ t('router.live.uptime') }}</dt>
                  <dd class="text-highlighted font-medium">
                    {{ formatUptime(liveSample.uptime_seconds) }}
                  </dd>
                </div>
              </dl>
              <FreshnessLabel v-if="liveAt" :at="liveAt" :expected-seconds="60" live />
            </div>
          </div>
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
            <UBadge
              v-if="direct"
              color="neutral"
              variant="subtle"
              icon="i-lucide-shield-alert"
              :label="t('devices.vendor.directBadge')"
              :title="t('devices.vendor.directHint')"
              data-testid="router-direct-badge"
            />
            <span v-else class="text-muted text-sm">
              {{ peer?.last_handshake_at ? t('router.handshake') : t('router.noHandshake') }}
              <RelativeTime v-if="peer?.last_handshake_at" :at="peer.last_handshake_at" />
            </span>
            <span
              v-if="exporter?.last_flow_at ?? routerStatus?.last_flow_at"
              class="text-muted text-sm"
              data-testid="router-last-flow"
            >
              <RelativeTime
                :at="exporter?.last_flow_at ?? routerStatus?.last_flow_at"
                :prefix="t('router.lastFlow')"
              />
            </span>
            <span v-if="exporter?.flows_per_second" class="text-muted text-sm tabular">
              {{ t('router.fps', { n: formatNumber(exporter.flows_per_second) }) }}
            </span>
          </p>
          <p class="text-muted text-sm" data-testid="router-vendor-line">
            {{ t(`devices.vendor.${rt.vendor}`) }} · {{ rt.display_name }} · {{ rt.model ?? '—' }} ·
            {{ site?.name ?? '' }}
          </p>
        </div>
      </UCard>

      <RouterTlsFingerprint
        v-if="can('devices.read') && routerOS"
        :key="`tls-banner-${rt.id}`"
        :router="rt"
        compact
        @review="tab = 'credentials'"
      />

      <UTabs v-model="tab" :items="tabs" :content="false" variant="link" class="w-full" />

      <div
        v-if="tab === 'metrics' || tab === 'interfaces'"
        class="flex flex-wrap items-center justify-between gap-2"
      >
        <USelect
          v-model="range"
          :items="rangeOptions"
          class="w-44"
          :aria-label="t('router.metrics.range')"
          data-testid="router-range"
        />
      </div>

      <template v-if="tab === 'summary'">
        <UCard>
          <ExporterSummary :router="rt" :exporter="exporter ?? null" :peer="peer ?? null" />
        </UCard>
        <AiAnalysis feature="router" :context-id="rt.id" />
      </template>
      <template v-else-if="tab === 'connection'">
        <UAlert
          v-if="rt.routeros_version_supported === false"
          color="warning"
          variant="subtle"
          icon="i-lucide-triangle-alert"
          :title="t('devices.router.unsupportedTitle')"
          :description="t('devices.router.unsupportedBody')"
          data-testid="router-unsupported-banner"
        />
        <DirectOnboardingWizard
          v-if="direct"
          :router="rt"
          :exporter="exporter ?? null"
          :active-customers="activeCustomers"
          :discovery="site?.discovery_mode ?? false"
          @refresh="refreshAll"
        />
        <OnboardingWizard
          v-else
          :router="rt"
          :peer="peer ?? null"
          :exporter="exporter ?? null"
          :active-customers="activeCustomers"
          :discovery="site?.discovery_mode ?? false"
          @refresh="refreshAll"
        />
        <RouterTlsFingerprint
          v-if="routerOS && rt.onboarding_state !== 'pending_configuration'"
          :router="rt"
        />
        <UCard data-testid="next-steps">
          <div class="flex flex-col gap-3">
            <h2 class="text-highlighted font-semibold">{{ t('devices.steps.next') }}</h2>
            <SetupSteps
              :current="rt.onboarding_state === 'pending_configuration' ? 'script' : 'credentials'"
              :links="{
                site: `/t/${slug}/nodes/${rt.site_id}`,
                credentials: `/t/${slug}/routers/${rt.id}/credentials`,
                prefixes: `/t/${slug}/routers/${rt.id}/prefixes`,
              }"
            />
          </div>
        </UCard>
      </template>
      <template v-else-if="tab === 'credentials'">
        <!-- La huella se comparte con el aviso de la cabecera (misma clave de useTenantQuery). -->
        <RouterTlsFingerprint v-if="routerOS" :router="rt" />
        <RouterCredentials :router="rt" @refresh="refreshRouter()" />
      </template>
      <PrefixesPanel
        v-else-if="tab === 'prefixes'"
        :site-id="rt.site_id"
        :router-id="rt.id"
        :router-ready="routerOS && rt.onboarding_state !== 'pending_configuration'"
      />
      <template v-else-if="tab === 'metrics'">
        <RouterMetricsPanel v-if="canSnmp" :router-id="rt.id" :range="range as RelativeRange" />
        <UCard v-else class="py-4">
          <EmptyState icon="i-lucide-lock" :title="t('router.noSnmpAccess')" />
        </UCard>
      </template>
      <RouterInterfacesPanel
        v-else-if="tab === 'interfaces'"
        :router-id="rt.id"
        :range="range as RelativeRange"
        :live="live"
        :can-series="canSnmp"
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

      <RouterFormModal
        v-model:open="editOpen"
        :router="rt"
        @saved="refreshAll"
        @conflict="refreshRouter()"
      />
      <DeleteConfirmModal
        v-model:open="deleteOpen"
        :title="t('devices.router.deleteTitle', { name: rt.name })"
        :confirm-label="t('devices.router.deleteSubmit')"
        :loading="deleting"
        :blocked="deleteBlocked"
        @confirm="remove"
      >
        <UAlert
          v-if="routerOS && rt.onboarding_state !== 'pending_configuration'"
          color="warning"
          variant="subtle"
          icon="i-lucide-file-x-2"
          :title="t('devices.router.deprovisionFirstTitle')"
          :description="t('devices.router.deprovisionFirstBody')"
          data-testid="deprovision-first"
        >
          <template v-if="can('wireguard.write')" #actions>
            <UButton
              size="sm"
              color="neutral"
              variant="outline"
              icon="i-lucide-file-x-2"
              :label="t('router.uninstall')"
              data-testid="delete-show-uninstall"
              @click="showUninstallFromDelete"
            />
          </template>
        </UAlert>
        <p class="text-default text-sm">{{ t('devices.router.deleteBody') }}</p>
      </DeleteConfirmModal>
    </template>
  </AppPage>
</template>
