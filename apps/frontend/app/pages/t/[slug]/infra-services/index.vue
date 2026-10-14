<script setup lang="ts">
import type { InfraService } from '~~/types/api'

/**
 * Servicios de infraestructura del ISP (rol snmp): sus servidores de IPTV, web, DNS, RADIUS…
 * vigilados desde Horus con ping, puertos y páginas web, y por los flujos (sin tráfico, ataque
 * entrante). Resumen por estado, filtros de nodo y estado en la URL, tabla con insignia de
 * estado, latencia y último cambio (problemas primero), detalle con el historial reciente de
 * cada chequeo, alta y edición con el constructor de chequeos y, para servidores privados, el
 * script RouterOS que pega el usuario. `GET /infra-services` cada 30 s.
 */
const { t } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const can = useCan()
const toast = useToast()
const slug = computed(() => String(route.params.slug))
useSectionGuard()(findSection('tenant', 'infra-services'), slug.value)
const manage = computed(() => can('snmp.manage'))
const { siteName } = useSites()

const STATE_FILTERS = ['all', 'problems', ...INFRA_STATES] as const
const site = useQueryParam('site', 'all')
const stateFilter = useQueryParam('state', 'all', STATE_FILTERS)
const siteOptions = useSiteOptions()
const stateOptions = computed(() => [
  { label: t('infra.filters.allStates'), value: 'all' },
  { label: t('infra.filters.problems'), value: 'problems' },
  ...INFRA_STATES.map((s) => ({ label: t(`infra.state.${s}`), value: s })),
])

const { data, error, status, refresh } = useTenantQuery('infra-services', () =>
  unwrap($api.GET('/infra-services')),
)
useAutoRefresh(() => refresh(), 30_000)

const rows = computed(() => data.value?.data ?? [])
const summary = computed(() => summarizeInfra(rows.value))
const filtered = computed(() =>
  sortInfraServices(
    rows.value.filter(
      (r) =>
        (site.value === 'all' || r.site_id === site.value) &&
        (stateFilter.value === 'all' ||
          (stateFilter.value === 'problems'
            ? isInfraProblem(r)
            : r.enabled && r.status.state === stateFilter.value)),
    ),
  ),
)
const scheduler = computed(() => data.value?.meta.scheduler ?? null)
const notice = computed<'disabled' | 'unhealthy' | 'noPassive' | null>(() => {
  const s = scheduler.value
  if (!s) return null
  if (!s.enabled) return 'disabled'
  if (!s.healthy) return 'unhealthy'
  const passive = rows.value.some((r) =>
    r.checks.some((c) => c.type === 'no_traffic' || c.type === 'ddos_inbound'),
  )
  return passive && !s.passive_checks ? 'noPassive' : null
})

function reasonLabel(r: string | null | undefined) {
  if (!r) return ''
  const key = `infra.reason.${r}`
  const out = t(key)
  return out === key ? r : out
}
function kindLabel(k: string) {
  const key = `infra.kind.${k}`
  const out = t(key)
  return out === key ? k : out
}

// Formulario, detalle, script y borrado.
const formOpen = ref(false)
const editing = ref<InfraService | null>(null)
function create() {
  editing.value = null
  formOpen.value = true
}
function edit(s: InfraService) {
  drawerOpen.value = false
  editing.value = s
  formOpen.value = true
}
const drawerOpen = ref(false)
const detailId = ref<string | null>(null)
function details(s: InfraService) {
  detailId.value = s.id
  drawerOpen.value = true
}
const snippetOpen = ref(false)
const snippetFor = ref<InfraService | null>(null)
function snippet(s: InfraService) {
  drawerOpen.value = false
  snippetFor.value = s
  snippetOpen.value = true
}
const deleting = ref<InfraService | null>(null)
const deleteOpen = computed({
  get: () => !!deleting.value,
  set: (v) => {
    if (!v) deleting.value = null
  },
})
async function remove() {
  const s = deleting.value
  if (!s) return
  try {
    await unwrap(
      $api.DELETE('/infra-services/{service_id}', {
        params: { path: { service_id: s.id }, header: { 'If-Match': ifMatch(s.version) } },
      }),
    )
    toast.add({ title: t('infra.deleted'), color: 'success', icon: 'i-lucide-check' })
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  }
  deleting.value = null
  refresh()
}

useHead({ title: () => t('infra.title') })
</script>

<template>
  <AppPage :title="t('infra.title')" panel-id="infra-services">
    <template #toolbar-left>
      <USelect
        v-model="site"
        :items="siteOptions"
        class="w-48"
        :aria-label="t('infra.columns.site')"
        data-testid="infra-filter-site"
      />
      <USelect
        v-model="stateFilter"
        :items="stateOptions"
        class="w-48"
        :aria-label="t('infra.columns.state')"
        data-testid="infra-filter-state"
      />
    </template>
    <template v-if="manage" #toolbar-right>
      <UButton
        icon="i-lucide-plus"
        :label="t('infra.add')"
        data-testid="add-infra"
        @click="create"
      />
    </template>

    <p class="text-muted mb-4 max-w-3xl text-sm">{{ t('infra.intro') }}</p>

    <UAlert
      v-if="notice"
      class="mb-4"
      :color="notice === 'noPassive' ? 'neutral' : 'warning'"
      variant="subtle"
      icon="i-lucide-info"
      :title="t(`infra.scheduler.${notice}`)"
      :data-testid="`infra-notice-${notice}`"
    />

    <LoadingState v-if="status === 'pending' && !data" :rows="4" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <UCard v-else-if="!rows.length" class="py-6">
      <EmptyState
        icon="i-lucide-server-off"
        :title="t('infra.empty')"
        :description="t('infra.emptyHint')"
      />
    </UCard>
    <template v-else>
      <ul
        class="mb-4 flex flex-wrap gap-2"
        :aria-label="t('infra.summaryLabel')"
        data-testid="infra-summary"
      >
        <li
          v-for="s in ['down', 'degraded', 'unknown', 'up', 'disabled'] as const"
          :key="s"
          :data-summary="s"
        >
          <UBadge
            :color="s === 'disabled' ? 'neutral' : INFRA_STATE_META[s].color"
            :variant="summary[s] ? 'subtle' : 'outline'"
            :icon="s === 'disabled' ? 'i-lucide-circle-pause' : INFRA_STATE_META[s].icon"
            size="lg"
          >
            <span class="tabular font-semibold">{{ summary[s] }}</span>
            <span>{{ t(`infra.state.${s}`) }}</span>
          </UBadge>
        </li>
      </ul>

      <UCard v-if="!filtered.length" class="py-6">
        <EmptyState icon="i-lucide-filter-x" :title="t('infra.emptyFilter')" />
      </UCard>
      <div v-else class="ring-default overflow-x-auto rounded-md ring-1">
        <table class="w-full text-sm" data-testid="infra-table">
          <thead class="bg-elevated/50 text-muted text-left text-xs">
            <tr>
              <th scope="col" class="px-3 py-2 font-medium">{{ t('infra.columns.service') }}</th>
              <th scope="col" class="px-3 py-2 font-medium">{{ t('infra.columns.state') }}</th>
              <th scope="col" class="hidden px-3 py-2 font-medium md:table-cell">
                {{ t('infra.columns.checks') }}
              </th>
              <th scope="col" class="px-3 py-2 text-right font-medium">
                {{ t('infra.columns.latency') }}
              </th>
              <th scope="col" class="hidden px-3 py-2 font-medium sm:table-cell">
                {{ t('infra.columns.since') }}
              </th>
              <th scope="col" class="px-3 py-2">
                <span class="sr-only">{{ t('infra.columns.actions') }}</span>
              </th>
            </tr>
          </thead>
          <tbody class="divide-default divide-y">
            <tr
              v-for="s in filtered"
              :key="s.id"
              data-testid="infra-row"
              :data-service="s.name"
              :data-state="s.enabled ? s.status.state : 'disabled'"
            >
              <td class="px-3 py-2">
                <button
                  type="button"
                  class="text-highlighted text-left font-medium hover:underline"
                  :aria-label="t('infra.actions.details', { name: s.name })"
                  @click="details(s)"
                >
                  {{ s.name }}
                </button>
                <p class="text-muted flex flex-wrap items-center gap-x-2 text-xs">
                  <span class="font-mono">{{ s.target }}</span>
                  <span>{{ kindLabel(s.kind) }}</span>
                  <span v-if="s.site_id">{{ siteName(s.site_id) }}</span>
                  <UBadge
                    v-if="s.target_private"
                    color="warning"
                    variant="outline"
                    size="sm"
                    icon="i-lucide-lock"
                    :label="s.probe_via_router_id ? t('infra.viaRouter') : t('infra.private')"
                    data-testid="infra-private"
                  />
                </p>
              </td>
              <td class="px-3 py-2">
                <UBadge
                  v-if="s.enabled"
                  :color="INFRA_STATE_META[s.status.state].color"
                  :icon="INFRA_STATE_META[s.status.state].icon"
                  variant="subtle"
                  :label="t(`infra.state.${s.status.state}`)"
                  data-testid="infra-state"
                />
                <UBadge
                  v-else
                  color="neutral"
                  variant="outline"
                  icon="i-lucide-circle-pause"
                  :label="t('infra.state.disabled')"
                  data-testid="infra-state"
                />
                <p
                  v-if="s.enabled && s.status.check_type"
                  class="text-muted mt-0.5 text-xs"
                  data-testid="infra-reason"
                >
                  {{ t(`infra.checkType.${s.status.check_type}`) }} ·
                  {{ reasonLabel(s.status.reason) }}
                </p>
              </td>
              <td class="hidden px-3 py-2 md:table-cell">
                <span class="flex flex-wrap gap-1">
                  <UBadge
                    v-for="(c, i) in s.checks"
                    :key="c.id ?? i"
                    color="neutral"
                    variant="soft"
                    size="sm"
                    :label="describeCheck(c, t)"
                  />
                </span>
              </td>
              <td class="tabular px-3 py-2 text-right" data-testid="infra-latency">
                {{ formatLatency(infraLatency(s)) }}
              </td>
              <td class="hidden px-3 py-2 sm:table-cell"><RelativeTime :at="s.status.since" /></td>
              <td class="px-3 py-2">
                <span class="flex justify-end gap-1">
                  <UButton
                    v-if="manage && s.target_private"
                    size="sm"
                    color="neutral"
                    variant="ghost"
                    icon="i-lucide-terminal"
                    :aria-label="`${t('infra.actions.snippet')}: ${s.name}`"
                    data-testid="infra-snippet-open"
                    @click="snippet(s)"
                  />
                  <UButton
                    v-if="manage"
                    size="sm"
                    color="neutral"
                    variant="ghost"
                    icon="i-lucide-pencil"
                    :aria-label="t('infra.actions.edit', { name: s.name })"
                    @click="edit(s)"
                  />
                  <UButton
                    v-if="manage"
                    size="sm"
                    color="neutral"
                    variant="ghost"
                    icon="i-lucide-trash-2"
                    :aria-label="t('infra.actions.delete', { name: s.name })"
                    @click="deleting = s"
                  />
                </span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <InfraServiceForm v-model:open="formOpen" :service="editing" @saved="refresh()" />
    <InfraServiceDrawer
      v-model:open="drawerOpen"
      :service-id="detailId"
      @edit="edit"
      @snippet="snippet"
    />
    <InfraSnippetModal v-model:open="snippetOpen" :service="snippetFor" />

    <UModal
      v-model:open="deleteOpen"
      :title="t('infra.deleteTitle', { name: deleting?.name ?? '' })"
      :description="t('infra.deleteBody')"
    >
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton
            color="neutral"
            variant="outline"
            :label="t('common.cancel')"
            autofocus
            @click="deleting = null"
          />
          <UButton
            color="error"
            icon="i-lucide-trash-2"
            :label="t('infra.deleteSubmit')"
            @click="remove"
          />
        </div>
      </template>
    </UModal>
  </AppPage>
</template>
