<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'
import type { Router, SwitchStatus } from '~~/types/api'

/**
 * Switches de monitoreo por nodo (docs/vendors/mikrotik.md §13, huawei.md §11): inventario
 * (`GET /routers?device_type=switch`) unido al estado del sondeador (`GET /switches/status`):
 * estado (icono + texto), sistema, puertos monitoreados, uplinks caídos, puertos inestables,
 * sFlow y CPU/temperatura (null = "—"). Filtro de nodo en la URL. "Agregar switch" abre el alta
 * y lleva a la ficha con el asistente de conexión.
 */
const { t } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const can = useCan()
const slug = computed(() => String(route.params.slug))

useSectionGuard()(findSection('tenant', 'switches'), slug.value)

const site = useQueryParam('site', 'all')
const siteOptions = useSiteOptions()
const { siteName } = useSites()

const inventory = useTenantQuery('switches:inventory', () =>
  unwrap($api.GET('/routers', { params: { query: { device_type: 'switch', limit: 200 } } })).then(
    (r) => r.data,
  ),
)
const status = useTenantQuery('switches:status', () => unwrap($api.GET('/switches/status')))
useAutoRefresh(() => {
  inventory.refresh()
  status.refresh()
}, 30_000)

interface Row {
  router: Router
  status: SwitchStatus | null
}
const rows = computed<Row[]>(() => {
  const st = new Map((status.data.value?.data ?? []).map((s) => [s.router_id, s]))
  return (inventory.data.value ?? [])
    .filter((r) => site.value === 'all' || r.site_id === site.value)
    .map((r) => ({ router: r, status: st.get(r.id) ?? null }))
    .sort((a, b) => a.router.name.localeCompare(b.router.name, 'es'))
})

const addOpen = ref(false)
function onCreated(r: Router) {
  inventory.refresh()
  navigateTo(`/t/${slug.value}/switches/${r.id}?onboard=1`)
}

const columns = computed<TableColumn<Row>[]>(() => [
  { id: 'name', header: () => t('switches.columns.name') },
  {
    id: 'site',
    header: () => t('switches.columns.site'),
    meta: { class: { th: 'hidden md:table-cell', td: 'hidden md:table-cell' } },
  },
  {
    id: 'platform',
    header: () => t('switches.columns.platform'),
    meta: { class: { th: 'hidden lg:table-cell', td: 'hidden lg:table-cell' } },
  },
  { id: 'status', header: () => t('switches.columns.status') },
  {
    id: 'ports',
    header: () => t('switches.columns.ports'),
    meta: { class: { th: 'text-end', td: 'text-end' } },
  },
  {
    id: 'uplinks',
    header: () => t('switches.columns.uplinks'),
    meta: { class: { th: 'hidden sm:table-cell', td: 'hidden sm:table-cell' } },
  },
  {
    id: 'flapping',
    header: () => t('switches.columns.flapping'),
    meta: { class: { th: 'hidden md:table-cell text-end', td: 'hidden md:table-cell text-end' } },
  },
  {
    id: 'sflow',
    header: () => t('switches.columns.sflow'),
    meta: { class: { th: 'hidden lg:table-cell', td: 'hidden lg:table-cell' } },
  },
  {
    id: 'health',
    header: () => t('switches.columns.health'),
    meta: { class: { th: 'hidden xl:table-cell text-end', td: 'hidden xl:table-cell text-end' } },
  },
])

const pct = (v: number | null | undefined) =>
  v === null || v === undefined ? '—' : `${formatNumber(v)} %`
const temp = (v: number | null | undefined) =>
  v === null || v === undefined ? '—' : `${formatNumber(v, 1)} °C`

useHead({ title: () => t('switches.title') })
</script>

<template>
  <AppPage :title="t('switches.title')" panel-id="switches">
    <template #toolbar-left>
      <USelect
        v-model="site"
        :items="siteOptions"
        class="w-48"
        :aria-label="t('switches.columns.site')"
        data-testid="switches-site"
      />
    </template>
    <template v-if="can('devices.create')" #toolbar-right>
      <UButton
        icon="i-lucide-plus"
        :label="t('switches.add')"
        data-testid="add-switch"
        @click="addOpen = true"
      />
    </template>

    <p class="text-muted mb-4 text-sm">{{ t('switches.subtitle') }}</p>
    <LoadingState v-if="inventory.status.value === 'pending' && !inventory.data.value" :rows="4" />
    <ErrorState
      v-else-if="inventory.error.value"
      :error="inventory.error.value"
      @retry="inventory.refresh()"
    />
    <UCard v-else-if="!rows.length" class="py-6" data-testid="switches-empty">
      <EmptyState
        icon="i-lucide-network"
        :title="inventory.data.value?.length ? t('switches.filteredEmpty') : t('switches.empty')"
        :actions="
          can('devices.create')
            ? [{ label: t('switches.add'), icon: 'i-lucide-plus', onClick: () => (addOpen = true) }]
            : []
        "
      />
    </UCard>
    <UCard v-else :ui="{ body: 'p-0 sm:p-0' }">
      <ErrorState
        v-if="status.error.value"
        class="m-4"
        :error="status.error.value"
        @retry="status.refresh()"
      />
      <UTable
        :data="rows"
        :columns="columns"
        class="w-full"
        :ui="{ td: 'px-3 py-2 sm:px-4', th: 'px-3 text-xs whitespace-nowrap sm:px-4' }"
        data-testid="switches-table"
      >
        <template #name-cell="{ row }">
          <NuxtLink
            :to="`/t/${slug}/switches/${row.original.router.id}`"
            class="text-highlighted font-mono font-medium hover:underline"
            :data-switch="row.original.router.name"
          >
            {{ row.original.router.name }}
          </NuxtLink>
        </template>
        <template #site-cell="{ row }">{{ siteName(row.original.router.site_id) }}</template>
        <template #platform-cell="{ row }">
          <span class="flex items-center gap-1.5 text-sm">
            <UIcon
              :name="
                PLATFORM_ICON[(row.original.router.switch_platform ?? 'routeros') as SwitchPlatform]
              "
              class="size-4"
              aria-hidden="true"
            />
            {{ t(`switches.platform.${row.original.router.switch_platform ?? 'routeros'}`) }}
          </span>
        </template>
        <template #status-cell="{ row }">
          <RouterStatusBadge
            v-if="
              row.original.status &&
              row.original.router.onboarding_state !== 'pending_configuration'
            "
            :status="row.original.status.status"
            :reason="row.original.status.reason"
            compact
          />
          <UBadge
            v-else
            color="neutral"
            variant="subtle"
            icon="i-lucide-circle-dashed"
            :label="t('switches.pending')"
          />
        </template>
        <template #ports-cell="{ row }">
          <span class="tabular">{{ row.original.status?.ports_monitored ?? '—' }}</span>
        </template>
        <template #uplinks-cell="{ row }">
          <span v-if="row.original.status" class="flex items-center gap-1.5 text-sm tabular">
            {{ row.original.status.uplinks }}
            <UBadge
              v-if="row.original.status.uplinks_down"
              color="error"
              variant="subtle"
              size="sm"
              icon="i-lucide-circle-x"
              :label="t('switches.uplinksDown', { n: row.original.status.uplinks_down })"
            />
          </span>
          <span v-else class="text-dimmed">—</span>
        </template>
        <template #flapping-cell="{ row }">
          <span
            class="tabular"
            :class="
              (row.original.status?.ports_flapping ?? 0) > 0
                ? 'text-warning font-medium'
                : 'text-muted'
            "
          >
            {{ row.original.status?.ports_flapping ?? '—' }}
          </span>
        </template>
        <template #sflow-cell="{ row }">
          <span class="text-muted text-sm">{{
            row.original.router.sflow_enabled ? t('switches.sflowOn') : t('switches.sflowOff')
          }}</span>
        </template>
        <template #health-cell="{ row }">
          <span class="text-muted text-sm tabular">
            {{ pct(row.original.status?.device.cpu_percent) }} ·
            {{ temp(row.original.status?.device.temperature_celsius) }}
          </span>
        </template>
      </UTable>
    </UCard>
    <AddSwitchModal
      v-model:open="addOpen"
      :site-id="site === 'all' ? null : site"
      @created="onCreated"
    />
  </AppPage>
</template>
