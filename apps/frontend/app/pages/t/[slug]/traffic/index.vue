<script setup lang="ts">
import type { Dashboard, DashboardWidget, RelativeRange } from '~~/types/api'

/**
 * Tráfico (I1-17, frontend.md §8.3): rango y nodo en la URL (nunca IPs); serie ↓/↑ y tops
 * de clientes, servicios, categorías y organizaciones con los mismos widgets del catálogo
 * (§6.2), alimentados por vista previa. Cobertura de atribución; `meta.partial` →
 * "Datos incompletos en este rango"; analítica caída (503) → aviso de degradado y el resto
 * de la app sigue funcionando.
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
      </UCard>
    </template>

    <DashboardGrid
      :key="viewKey"
      :dashboard="view"
      :tenant-name="membership?.tenant_name ?? ''"
      preview
    />
  </AppPage>
</template>
