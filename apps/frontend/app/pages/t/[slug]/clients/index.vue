<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'
import type { Customer } from '~~/types/api'

/**
 * Clientes por IP (I1-16, frontend.md §8.1). Filtros en la URL; la búsqueda por IP viaja en
 * el cuerpo de `POST /customers/lookup` y vive solo en el estado de la página (la IP nunca
 * entra en la URL ni en el historial). Paginación por cursor ("Cargar más").
 */
const { t } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const slug = computed(() => String(route.params.slug))

useSectionGuard()(findSection('tenant', 'clients'), slug.value)

const { siteName } = useSites()
const siteOptions = useSiteOptions()

const site = useQueryParam('site', 'all')
const kind = useQueryParam('kind', 'all', ['all', 'residential', 'commercial'] as const)
const status = useQueryParam('status', 'all', ['all', 'active', 'inactive'] as const)
const security = useQueryParam('security', 'all', ['all', 'with_findings', 'infected'] as const)

/** Búsqueda: IP/prefijo (cuerpo de POST) o alias. Solo en memoria. */
const searchInput = ref('')
const search = ref('')
const looksLikeIp = (v: string) => /^[0-9a-f:.]+(\/\d+)?$/i.test(v) && /[.:]/.test(v)

const rows = ref<Customer[]>([])
const cursor = ref<string | null>(null)
const loading = ref(false)
const loadingMore = ref(false)
const error = shallowRef<unknown>(null)
const searchError = ref<string | null>(null)

function filters() {
  return {
    site_id: site.value === 'all' ? undefined : site.value,
    kind: kind.value === 'all' ? undefined : kind.value,
    status: status.value === 'all' ? undefined : status.value,
    has_open_findings: security.value === 'with_findings' ? true : undefined,
    security_state: security.value === 'infected' ? 'infected' : undefined,
  }
}

async function fetchPage(next: string | null) {
  const term = search.value.trim()
  if (term && looksLikeIp(term)) {
    const body = term.includes('/') ? { prefix: term } : { address: term }
    return unwrap(
      $api.POST('/customers/lookup', {
        body: { ...body, site_id: site.value === 'all' ? undefined : site.value },
        params: { query: { cursor: next ?? undefined, limit: 50 } },
      }),
    )
  }
  return unwrap(
    $api.GET('/customers', {
      params: {
        query: { ...filters(), q: term || undefined, cursor: next ?? undefined, limit: 50 },
      },
    }),
  )
}

let seq = 0
async function load() {
  const mine = ++seq
  loading.value = true
  error.value = null
  searchError.value = null
  try {
    const page = await fetchPage(null)
    if (mine !== seq) return
    rows.value = page.data
    cursor.value = page.page.next_cursor
  } catch (e) {
    if (mine !== seq) return
    if (e instanceof ApiError && e.status === 422) searchError.value = t('clients.searchInvalid')
    else error.value = e
    rows.value = []
  } finally {
    if (mine === seq) loading.value = false
  }
}

async function loadMore() {
  if (!cursor.value) return
  loadingMore.value = true
  try {
    const page = await fetchPage(cursor.value)
    rows.value = [...rows.value, ...page.data]
    cursor.value = page.page.next_cursor
  } catch (e) {
    error.value = e
  } finally {
    loadingMore.value = false
  }
}

function submitSearch() {
  search.value = searchInput.value
  load()
}
function clearAll() {
  searchInput.value = ''
  search.value = ''
  site.value = 'all'
  kind.value = 'all'
  status.value = 'all'
  security.value = 'all'
}

onMounted(load)
watch([site, kind, status, security], load)

const filtered = computed(
  () =>
    !!search.value ||
    site.value !== 'all' ||
    kind.value !== 'all' ||
    status.value !== 'all' ||
    security.value !== 'all',
)

const detailHref = (c: Customer) => `/t/${slug.value}/clients/${c.id}`
const down = (c: Customer) =>
  c.traffic_24h?.down_bytes ? joinUnit(formatBytes(Number(c.traffic_24h.down_bytes))) : '—'
const up = (c: Customer) =>
  c.traffic_24h?.up_bytes ? joinUnit(formatBytes(Number(c.traffic_24h.up_bytes))) : '—'
const prefixLen = (c: Customer) => (c.address.includes(':') ? 56 : null)

const columns: TableColumn<Customer>[] = [
  { id: 'client', header: () => t('clients.col.client') },
  { id: 'site', header: () => t('clients.col.site') },
  { id: 'kind', header: () => t('clients.col.kind') },
  {
    id: 'traffic',
    header: () => t('clients.col.traffic'),
    meta: { class: { th: 'text-end', td: 'text-end' } },
  },
  { id: 'top', header: () => t('clients.col.top') },
  { id: 'security', header: () => t('clients.col.security') },
  { id: 'status', header: () => t('clients.col.status') },
  { id: 'seen', header: () => t('clients.col.seen') },
]

const kindOptions = computed(() => [
  { label: t('clients.filters.allKinds'), value: 'all' },
  { label: t('clients.kind.residential'), value: 'residential' },
  { label: t('clients.kind.commercial'), value: 'commercial' },
])
const statusOptions = computed(() => [
  { label: t('clients.filters.allStatus'), value: 'all' },
  { label: t('clients.status.active'), value: 'active' },
  { label: t('clients.status.inactive'), value: 'inactive' },
])
const securityOptions = computed(() => [
  { label: t('clients.filters.allSecurity'), value: 'all' },
  { label: t('clients.filters.withFindings'), value: 'with_findings' },
  { label: t('securityState.infected'), value: 'infected' },
])
</script>

<template>
  <AppPage :title="t('nav.items.clients')" panel-id="clients">
    <form
      class="flex flex-col gap-3 lg:flex-row lg:flex-wrap lg:items-end"
      role="search"
      @submit.prevent="submitSearch"
    >
      <UFormField
        :label="t('clients.search')"
        :hint="t('clients.searchHint')"
        class="min-w-0 lg:w-80"
      >
        <UInput
          v-model="searchInput"
          icon="i-lucide-search"
          :placeholder="t('clients.searchPlaceholder')"
          autocomplete="off"
          class="w-full"
          data-testid="client-search"
          :ui="{ base: 'font-mono' }"
        >
          <template v-if="searchInput" #trailing>
            <UButton
              color="neutral"
              variant="link"
              size="sm"
              icon="i-lucide-x"
              :aria-label="t('common.clear')"
              @click="
                () => {
                  searchInput = ''
                  submitSearch()
                }
              "
            />
          </template>
        </UInput>
      </UFormField>
      <div class="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:flex lg:flex-1 lg:flex-wrap">
        <UFormField :label="t('clients.col.site')">
          <USelect v-model="site" :items="siteOptions" class="w-full lg:w-44" />
        </UFormField>
        <UFormField :label="t('clients.col.kind')">
          <USelect v-model="kind" :items="kindOptions" class="w-full lg:w-40" />
        </UFormField>
        <UFormField :label="t('clients.col.status')">
          <USelect v-model="status" :items="statusOptions" class="w-full lg:w-36" />
        </UFormField>
        <UFormField :label="t('clients.col.security')">
          <USelect v-model="security" :items="securityOptions" class="w-full lg:w-44" />
        </UFormField>
      </div>
      <UButton
        type="submit"
        icon="i-lucide-search"
        :label="t('clients.searchSubmit')"
        class="self-start lg:self-end"
      />
    </form>

    <UAlert
      v-if="searchError"
      color="warning"
      variant="subtle"
      icon="i-lucide-circle-alert"
      :title="searchError"
    />

    <LoadingState v-if="loading && !rows.length" :rows="6" />
    <ErrorState v-else-if="error" :error="error" @retry="load" />
    <UCard v-else-if="!rows.length" class="py-6">
      <EmptyState
        v-if="filtered"
        icon="i-lucide-search-x"
        :title="t('clients.emptyFiltered')"
        :actions="[
          {
            label: t('common.clearFilters'),
            icon: 'i-lucide-filter-x',
            color: 'neutral',
            variant: 'outline',
            onClick: clearAll,
          },
        ]"
      />
      <EmptyState
        v-else
        icon="i-lucide-users"
        :title="t('clients.empty')"
        :actions="[
          {
            label: t('clients.reviewPrefixes'),
            to: `/t/${slug}/nodes`,
            color: 'neutral',
            variant: 'outline',
          },
        ]"
      />
    </UCard>

    <template v-else>
      <p class="text-muted text-sm" aria-live="polite" data-testid="clients-count">
        {{ t('clients.showing', { n: formatNumber(rows.length) }) }}
      </p>

      <!-- Escritorio: tabla. -->
      <UCard class="hidden lg:block" :ui="{ body: 'p-0 sm:p-0' }">
        <UTable
          :data="rows"
          :columns="columns"
          :loading="loading"
          class="w-full"
          :ui="{ td: 'align-top py-2.5', th: 'text-xs' }"
          data-testid="clients-table"
        >
          <template #client-cell="{ row }">
            <NuxtLink
              :to="detailHref(row.original)"
              class="hover:underline focus-visible:underline"
              data-testid="client-link"
            >
              <ClientAddress
                :address="row.original.address"
                :alias="row.original.alias"
                :prefix-len="prefixLen(row.original)"
              />
            </NuxtLink>
          </template>
          <template #site-cell="{ row }">
            <NuxtLink :to="`/t/${slug}/nodes/${row.original.site_id}`" class="hover:underline">{{
              siteName(row.original.site_id)
            }}</NuxtLink>
          </template>
          <template #kind-cell="{ row }">
            <ClientKindBadge
              :kind="row.original.kind"
              :source="row.original.kind_source"
              :locked="row.original.kind_locked"
              :confidence="row.original.kind_confidence"
            />
          </template>
          <template #traffic-cell="{ row }">
            <span class="tabular block whitespace-nowrap">↓ {{ down(row.original) }}</span>
            <span class="text-muted tabular block text-xs whitespace-nowrap"
              >↑ {{ up(row.original) }}</span
            >
          </template>
          <template #top-cell="{ row }">
            <span class="text-muted">{{ row.original.traffic_24h?.top_category ?? '—' }}</span>
          </template>
          <template #security-cell="{ row }">
            <span v-if="row.original.open_findings" class="flex flex-col">
              <SecurityStateBadge :state="row.original.security_state" />
              <NuxtLink
                :to="`${detailHref(row.original)}?tab=security`"
                class="text-muted text-xs hover:underline"
              >
                {{ t('clients.findingsCount', { n: row.original.open_findings }) }}
              </NuxtLink>
            </span>
            <span v-else class="text-muted">{{ t('securityState.clean') }}</span>
          </template>
          <template #status-cell="{ row }">
            <span :class="row.original.status === 'inactive' ? 'text-dimmed' : 'text-default'">
              {{ t(`clients.status.${row.original.status}`) }}
            </span>
          </template>
          <template #seen-cell="{ row }">
            <RelativeTime :at="row.original.last_seen" class="text-muted whitespace-nowrap" />
          </template>
        </UTable>
      </UCard>

      <!-- Móvil y tableta: lista (frontend.md §5.2). -->
      <ul class="flex flex-col gap-2 lg:hidden" data-testid="clients-list">
        <li v-for="c in rows" :key="c.id">
          <NuxtLink
            :to="detailHref(c)"
            class="bg-default ring-default hover:bg-elevated focus-visible:ring-primary flex flex-col gap-1.5 rounded-md p-3 ring-1"
          >
            <span class="flex min-w-0 items-start justify-between gap-2">
              <ClientAddress :address="c.address" :alias="c.alias" :prefix-len="prefixLen(c)" />
              <RelativeTime :at="c.last_seen" class="text-muted shrink-0 text-xs" />
            </span>
            <span class="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
              <span class="text-muted">{{ siteName(c.site_id) }}</span>
              <ClientKindBadge
                :kind="c.kind"
                :source="c.kind_source"
                :locked="c.kind_locked"
                :confidence="c.kind_confidence"
              />
            </span>
            <span class="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
              <span class="tabular">↓ {{ down(c) }} · ↑ {{ up(c) }}</span>
              <SecurityStateBadge v-if="c.open_findings" :state="c.security_state" />
              <span v-if="c.open_findings" class="text-muted text-xs">{{
                t('clients.findingsCount', { n: c.open_findings })
              }}</span>
              <span v-if="c.status === 'inactive'" class="text-dimmed">{{
                t('clients.status.inactive')
              }}</span>
            </span>
          </NuxtLink>
        </li>
      </ul>

      <div v-if="cursor" class="flex justify-center">
        <UButton
          color="neutral"
          variant="outline"
          icon="i-lucide-chevrons-down"
          :loading="loadingMore"
          :label="t('common.loadMore')"
          data-testid="load-more"
          @click="loadMore"
        />
      </div>
    </template>

    <p class="text-dimmed flex items-center gap-1.5 text-xs">
      <UIcon name="i-lucide-shield-check" class="size-4 shrink-0" aria-hidden="true" />
      {{ t('clients.privacy') }}
    </p>
  </AppPage>
</template>
