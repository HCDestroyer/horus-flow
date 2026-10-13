<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'
import type { Dashboard, DashboardWidget, RelativeRange, TopResult } from '~~/types/api'

/**
 * Tráfico (I1-17, frontend.md §8.3): rango y nodo en la URL (nunca IPs); serie ↓/↑ y tops
 * de clientes, servicios, categorías y organizaciones con los mismos widgets del catálogo
 * (§6.2), alimentados por vista previa. Cobertura de atribución; `meta.partial` →
 * "Datos incompletos en este rango"; analítica caída (503) → aviso de degradado y el resto
 * de la app sigue funcionando.
 * Desglose por organización o ASN remoto (`/analytics/traffic/top`): pestaña y sentido en la
 * URL; tabla con puesto, nombre, ↓/↑ y cuota. El ASN se muestra como "AS15169" con su nombre
 * si se conoce (si no, solo el número).
 */
const { t } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const slug = computed(() => String(route.params.slug))
const can = useCan()

useSectionGuard()(findSection('tenant', 'traffic'), slug.value)

const RANGES = ['1h', '6h', '24h', '7d', '30d'] as const
const range = useQueryParam('range', '24h', RANGES)
const site = useQueryParam('site', 'all')
const siteOptions = useSiteOptions()
const rangeOptions = computed(() => RANGES.map((r) => ({ label: t(`ranges.${r}`), value: r })))

const siteIds = computed(() => (site.value === 'all' ? [] : [site.value]))

const {
  data: attribution,
  error: attributionError,
  refresh,
} = useTenantQuery(
  () => `traffic:attribution:${range.value}:${site.value}`,
  () =>
    unwrap(
      $api.GET('/analytics/traffic/attribution', {
        params: {
          query: {
            range: range.value as RelativeRange,
            site_id: site.value === 'all' ? undefined : site.value,
          },
        },
      }),
    ),
  { watch: [range, site] },
)

// ¿Dónde está el resto? Rangos con tráfico fuera de los prefijos declarados (las propuestas del
// modo descubrimiento de cada nodo: flows.unattributed_1h agregado en /24), los que más pesan.
const { sites } = useSites()
const { data: unattributed } = useTenantQuery(
  () => `traffic:unattributed:${range.value}:${site.value}`,
  async () => {
    const ids =
      site.value === 'all' ? (sites.value ?? []).slice(0, 20).map((s) => s.id) : [site.value]
    const lists = await Promise.all(
      ids.map((id) =>
        unwrap(
          $api.GET('/sites/{site_id}/prefix-proposals', {
            params: { path: { site_id: id }, query: { range: range.value as RelativeRange } },
          }),
        )
          .then((r) => r.data.map((p) => ({ ...p, siteId: id })))
          .catch(() => []),
      ),
    )
    return lists
      .flat()
      .sort((a, b) => Number(b.bytes) - Number(a.bytes))
      .slice(0, 8)
  },
  { watch: [range, site, () => sites.value?.length] },
)
const siteName = (id: string) => sites.value?.find((s) => s.id === id)?.name ?? id

const degraded = computed(
  () => attributionError.value instanceof ApiError && attributionError.value.status === 503,
)

function widget(
  id: string,
  type: string,
  title: string,
  position: DashboardWidget['position'],
  config: Record<string, unknown> = {},
): DashboardWidget {
  return {
    id,
    type,
    title,
    position,
    config: { range: range.value, site_ids: siteIds.value, ...config },
    refresh_seconds: 60,
  }
}

/** Vista de tráfico como un dashboard sin guardar (12 columnas, mismos widgets). */
const view = computed<Dashboard>(() => ({
  id: 'traffic-view',
  tenant_id: null,
  version: 1,
  name: t('nav.items.traffic'),
  visibility: 'private',
  owner_id: null,
  layout: { grid: '12-col', columns: 12, row_height_px: 80 },
  default_range: range.value as RelativeRange,
  refresh_seconds: 60,
  created_at: '2026-10-01T00:00:00.000Z',
  updated_at: '2026-10-01T00:00:00.000Z',
  widgets: [
    widget(
      't-series',
      'traffic_timeseries',
      t('traffic.series'),
      { x: 0, y: 0, w: 12, h: 3 },
      {
        metrics: ['down_bps', 'up_bps'],
      },
    ),
    ...(can('customers.read')
      ? [
          widget(
            't-customers',
            'top_customers',
            t('traffic.topCustomers'),
            { x: 0, y: 3, w: 6, h: 4 },
            { n: 10 },
          ),
        ]
      : []),
    widget(
      't-services',
      'top_services',
      t('traffic.topServices'),
      { x: 6, y: 3, w: 6, h: 4 },
      { n: 10 },
    ),
    widget(
      't-categories',
      'top_categories',
      t('traffic.topCategories'),
      { x: 0, y: 7, w: 6, h: 4 },
      { n: 10 },
    ),
    widget(
      't-orgs',
      'top_organizations',
      t('traffic.topOrganizations'),
      { x: 6, y: 7, w: 6, h: 4 },
      { n: 10 },
    ),
  ],
}))
const viewKey = computed(() => `${range.value}:${site.value}`)
const { membership } = useTenant()

// --- Desglose por organización / ASN ---------------------------------------------------------
const DIMENSIONS = ['organizations', 'asns'] as const
const DIRECTIONS = ['both', 'download', 'upload'] as const
const by = useQueryParam('by', 'organizations', DIMENSIONS)
const direction = useQueryParam('direction', 'both', DIRECTIONS)
const byTabs = computed(() => [
  {
    label: t('traffic.breakdown.tabs.organizations'),
    value: 'organizations',
    icon: 'i-lucide-building-2',
  },
  { label: t('traffic.breakdown.tabs.asns'), value: 'asns', icon: 'i-lucide-network' },
])
const directionOptions = computed(() =>
  DIRECTIONS.map((d) => ({ label: t(`traffic.breakdown.directions.${d}`), value: d })),
)

const {
  data: top,
  error: topError,
  status: topStatus,
  refresh: refreshTop,
} = useTenantQuery(
  () => `traffic:top:${by.value}:${direction.value}:${range.value}:${site.value}`,
  () =>
    unwrap(
      $api.GET('/analytics/traffic/top', {
        params: {
          query: {
            dimension: by.value,
            direction: direction.value,
            n: 10,
            range: range.value as RelativeRange,
            site_id: site.value === 'all' ? undefined : site.value,
          },
        },
      }),
    ).then((r) => r.data),
  { watch: [by, direction, range, site] },
)

interface BreakdownRow {
  /** Puesto en el top; null en la fila "Otros". */
  rank: number | null
  /** "AS15169" en la vista por ASN; null en organizaciones y en "Otros". */
  asn: string | null
  name: string
  down: number
  up: number
  share: number | null
}

/** Nombre del ASN sin el prefijo "AS<n> · " que pone la API (vacío si no se conoce). */
function asnName(key: string, label: string) {
  const prefix = `AS${key}`
  if (!label.startsWith(prefix)) return label
  return label
    .slice(prefix.length)
    .replace(/^\s*·\s*/, '')
    .trim()
}

function breakdownRows(result: TopResult, dimension: (typeof DIMENSIONS)[number]) {
  const rows: BreakdownRow[] = result.rows.map((r, i) => ({
    rank: i + 1,
    asn: dimension === 'asns' ? `AS${r.key}` : null,
    name: dimension === 'asns' ? asnName(r.key, r.label) : r.label,
    down: Number(r.down_bytes),
    up: Number(r.up_bytes),
    share: r.share,
  }))
  const others = { down: Number(result.others.down_bytes), up: Number(result.others.up_bytes) }
  if (others.down + others.up > 0) {
    rows.push({ rank: null, asn: null, name: t('widgets.common.others'), ...others, share: null })
  }
  return rows
}

const breakdown = computed(() =>
  top.value && top.value.dimension === by.value ? breakdownRows(top.value, by.value) : [],
)
const breakdownLoading = computed(() => topStatus.value === 'pending' && !breakdown.value.length)

const breakdownColumns = computed<TableColumn<BreakdownRow>[]>(() => [
  {
    id: 'rank',
    header: () => t('traffic.breakdown.col.rank'),
    meta: { class: { th: 'w-12 pe-0 sm:pe-4', td: 'w-12 pe-0 sm:pe-4' } },
  },
  {
    id: 'name',
    header: () =>
      by.value === 'asns'
        ? t('traffic.breakdown.col.asn')
        : t('traffic.breakdown.col.organization'),
    meta: { class: { td: 'whitespace-normal' } },
  },
  {
    id: 'down',
    header: () => t('traffic.breakdown.col.down'),
    meta: { class: { th: 'text-end', td: 'text-end' } },
  },
  {
    id: 'up',
    header: () => t('traffic.breakdown.col.up'),
    // En móvil, la subida va bajo la bajada (sin scroll horizontal).
    meta: { class: { th: 'hidden text-end sm:table-cell', td: 'hidden text-end sm:table-cell' } },
  },
  {
    id: 'share',
    header: () => t('traffic.breakdown.col.share'),
    meta: { class: { th: 'text-end', td: 'text-end' } },
  },
])
</script>

<template>
  <AppPage :title="t('nav.items.traffic')" panel-id="traffic">
    <template #toolbar-left>
      <div class="flex flex-wrap items-center gap-2">
        <USelect
          v-model="range"
          :items="rangeOptions"
          class="w-40"
          :aria-label="t('traffic.range')"
          data-testid="traffic-range"
        />
        <USelect
          v-model="site"
          :items="siteOptions"
          class="w-44"
          :aria-label="t('clients.col.site')"
          data-testid="traffic-site"
        />
      </div>
    </template>

    <DegradedNotice v-if="attributionError" :error="attributionError" @retry="refresh()" />
    <template v-if="!degraded">
      <UAlert
        v-if="attribution?.meta.partial"
        color="warning"
        variant="subtle"
        icon="i-lucide-circle-dashed"
        :title="t('traffic.partial')"
        :description="t('traffic.partialHint', { pct: formatPercent(attribution.meta.coverage) })"
        data-testid="traffic-partial"
      />
      <UCard v-if="attribution" :ui="{ body: 'p-4 sm:p-4' }" data-testid="traffic-coverage">
        <div class="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
          <p class="text-default">
            <span class="text-highlighted text-lg font-semibold tabular">{{
              formatPercent(attribution.data.attributed_ratio)
            }}</span>
            {{ t('traffic.attributed') }}
            <span class="text-muted">
              ·
              {{
                t('traffic.unattributed', {
                  pct: formatPercent(attribution.data.unattributed_ratio),
                })
              }}
              ·
              {{
                t('traffic.infrastructure', {
                  pct: formatPercent(attribution.data.infrastructure_ratio),
                })
              }}
            </span>
          </p>
          <UButton
            :to="`/t/${slug}/nodes`"
            color="neutral"
            variant="outline"
            size="sm"
            icon="i-lucide-list-tree"
            :label="t('traffic.reviewPrefixes')"
            class="self-start"
          />
        </div>
        <div class="mt-3 flex h-2 overflow-hidden rounded-full" aria-hidden="true">
          <span
            class="bg-(--viz-1)"
            :style="{ width: `${attribution.data.attributed_ratio * 100}%` }"
          />
          <span
            class="bg-(--ui-border-accented)"
            :style="{ width: `${attribution.data.infrastructure_ratio * 100}%` }"
          />
          <span
            class="bg-(--ui-bg-accented)"
            :style="{ width: `${attribution.data.unattributed_ratio * 100}%` }"
          />
        </div>
        <details
          v-if="attribution.data.unattributed_ratio > 0"
          class="mt-3"
          data-testid="traffic-unattributed"
        >
          <summary class="text-default cursor-pointer text-sm font-medium">
            {{ t('traffic.rest.title') }}
          </summary>
          <p class="text-muted mt-2 text-sm">{{ t('traffic.rest.body') }}</p>
          <p v-if="!unattributed?.length" class="text-muted mt-2 text-sm">
            {{ t('traffic.rest.empty') }}
          </p>
          <ul v-else class="divide-default mt-2 divide-y text-sm">
            <li
              v-for="r in unattributed"
              :key="`${r.siteId}-${r.prefix}`"
              class="flex flex-wrap items-center justify-between gap-2 py-2"
              :data-unattributed="r.prefix"
            >
              <span class="min-w-0">
                <span class="text-highlighted font-mono">{{ r.prefix }}</span>
                <span class="text-muted">
                  · {{ siteName(r.siteId) }} ·
                  {{
                    t('prefixes.discovery.stats', {
                      ips: formatNumber(r.distinct_ips),
                      bytes: joinUnit(formatBytes(Number(r.bytes))),
                    })
                  }}
                  ·
                  {{
                    t('prefixes.discovery.suggested', {
                      role: t(`prefixes.role.${r.suggested_role}`),
                    })
                  }}
                </span>
              </span>
              <UButton
                :to="`/t/${slug}/nodes/${r.siteId}`"
                size="xs"
                color="neutral"
                variant="outline"
                icon="i-lucide-list-tree"
                :label="t('traffic.rest.review')"
              />
            </li>
          </ul>
        </details>
      </UCard>
    </template>

    <DashboardGrid
      :key="viewKey"
      :dashboard="view"
      :tenant-name="membership?.tenant_name ?? ''"
      preview
    />

    <UCard
      v-if="!degraded"
      :ui="{ header: 'p-4 sm:px-4', body: 'p-0 sm:p-0' }"
      data-testid="traffic-breakdown"
    >
      <template #header>
        <section class="flex flex-col gap-3" aria-labelledby="traffic-breakdown-title">
          <div class="flex flex-wrap items-baseline justify-between gap-2">
            <h2 id="traffic-breakdown-title" class="text-highlighted text-base font-semibold">
              {{ t('traffic.breakdown.title') }}
            </h2>
            <p class="text-muted text-sm">{{ t('traffic.breakdown.hint') }}</p>
          </div>
          <div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <UTabs
              v-model="by"
              :items="byTabs"
              :content="false"
              size="sm"
              class="w-full sm:w-auto"
              data-testid="traffic-breakdown-tabs"
            />
            <USelect
              v-model="direction"
              :items="directionOptions"
              size="sm"
              class="w-full sm:w-48"
              :aria-label="t('traffic.breakdown.direction')"
              data-testid="traffic-direction"
            />
          </div>
        </section>
      </template>

      <div v-if="breakdownLoading" class="p-4">
        <LoadingState :rows="5" />
      </div>
      <div v-else-if="topError" class="p-4">
        <ErrorState :error="topError" @retry="refreshTop()" />
      </div>
      <div v-else-if="!breakdown.length" class="p-4">
        <EmptyState icon="i-lucide-network" :title="t('traffic.breakdown.empty')" />
      </div>
      <UTable
        v-else
        :data="breakdown"
        :columns="breakdownColumns"
        class="w-full"
        :ui="{ td: 'px-3 py-2.5 sm:px-4', th: 'px-3 text-xs whitespace-nowrap sm:px-4' }"
        data-testid="traffic-breakdown-table"
      >
        <template #rank-cell="{ row }">
          <span class="text-muted tabular">{{ row.original.rank ?? '' }}</span>
        </template>
        <template #name-cell="{ row }">
          <span
            v-if="row.original.asn"
            class="flex flex-wrap items-baseline gap-x-2"
            data-testid="traffic-breakdown-asn"
          >
            <span class="text-highlighted font-mono font-medium">{{ row.original.asn }}</span>
            <span v-if="row.original.name" class="text-muted">{{ row.original.name }}</span>
          </span>
          <span
            v-else
            :class="row.original.rank === null ? 'text-muted' : 'text-highlighted font-medium'"
            >{{ row.original.name }}</span
          >
        </template>
        <template #down-cell="{ row }">
          <span class="tabular block whitespace-nowrap"
            >↓ {{ joinUnit(formatBytes(row.original.down)) }}</span
          >
          <span class="text-muted tabular block text-xs whitespace-nowrap sm:hidden"
            >↑ {{ joinUnit(formatBytes(row.original.up)) }}</span
          >
        </template>
        <template #up-cell="{ row }">
          <span class="text-muted tabular whitespace-nowrap"
            >↑ {{ joinUnit(formatBytes(row.original.up)) }}</span
          >
        </template>
        <template #share-cell="{ row }">
          <span v-if="row.original.share !== null" class="tabular">{{
            formatPercent(row.original.share, 1)
          }}</span>
          <span v-else class="text-dimmed">—</span>
        </template>
      </UTable>
    </UCard>
  </AppPage>
</template>
