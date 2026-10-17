<script setup lang="ts">
import type { TimeSeries } from '~/utils/chart-palette'

/**
 * Tráfico remoto local vs internacional (`GET /capacity/traffic-split`): proporción y
 * volumen del periodo, barras apiladas por día (paleta categórica) y tabla por nodo. Local =
 * ASN en la lista local del ISP o de su país; desconocido (sin ASN o sin país) queda fuera
 * de las proporciones. Error visible con "Reintentar"; nunca proporciones sobre datos vacíos.
 */
const props = defineProps<{ siteId: string | null; days: number }>()
const { t, te } = useI18n()
const { $api } = useNuxtApp()

const { data, error, status, refresh } = useTenantQuery(
  () => `capacity:traffic:${props.siteId ?? 'all'}:${props.days}`,
  () =>
    unwrap(
      $api.GET('/capacity/traffic-split', {
        params: { query: { days: props.days, ...(props.siteId ? { site_id: props.siteId } : {}) } },
      }),
    ),
  { watch: [() => props.siteId, () => props.days] },
)

const shares = computed(() => (data.value ? splitShares(data.value.total) : null))
const bytes = (v: number) => joinUnit(formatBytes(v))
const series = computed<TimeSeries[]>(() => {
  const d = data.value?.daily ?? []
  return [
    {
      name: t('capacity.traffic.international'),
      points: d.map((x) => [x.day, x.international_bytes]),
    },
    { name: t('capacity.traffic.local'), points: d.map((x) => [x.day, x.local_bytes]) },
    { name: t('capacity.traffic.unknown'), points: d.map((x) => [x.day, x.unknown_bytes]) },
  ]
})
const countryNote = computed(() => {
  const r = data.value
  if (!r) return ''
  const src = te(`capacity.traffic.source.${r.country_source}`)
    ? t(`capacity.traffic.source.${r.country_source}`)
    : r.country_source
  return t('capacity.traffic.basis', {
    country: r.country ?? '—',
    source: src,
    n: r.local_asns,
    p: r.local_prefixes,
  })
})
</script>

<template>
  <UCard :ui="{ body: 'p-4 sm:p-4' }" data-testid="traffic-split">
    <h2 class="text-highlighted text-base font-semibold">{{ t('capacity.traffic.title') }}</h2>
    <p class="text-muted mb-3 text-sm">{{ t('capacity.traffic.intro', { days }) }}</p>
    <LoadingState v-if="status === 'pending' && !data" :rows="3" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <template v-else-if="data">
      <p v-if="!shares" class="text-muted text-sm" data-testid="traffic-split-empty">
        {{ t('capacity.traffic.empty') }}
      </p>
      <template v-else>
        <dl class="mb-3 grid gap-3 sm:grid-cols-3">
          <div>
            <dt class="text-muted text-xs">{{ t('capacity.traffic.international') }}</dt>
            <dd
              class="text-highlighted tabular text-xl font-semibold"
              data-testid="traffic-intl-share"
            >
              {{ formatPercent(shares.international) }}
            </dd>
            <dd class="text-muted text-xs tabular">{{ bytes(data.total.international_bytes) }}</dd>
          </div>
          <div>
            <dt class="text-muted text-xs">{{ t('capacity.traffic.local') }}</dt>
            <dd class="text-highlighted tabular text-xl font-semibold">
              {{ formatPercent(shares.local) }}
            </dd>
            <dd class="text-muted text-xs tabular">
              {{ bytes(data.total.local_bytes) }} ·
              {{ t('capacity.traffic.byList', { v: bytes(data.total.local_by_list_bytes) }) }}
              <template v-if="data.local_prefixes > 0">
                ·
                <span data-testid="traffic-by-prefix">{{
                  t('capacity.traffic.byPrefix', { v: bytes(data.total.local_by_prefix_bytes) })
                }}</span>
              </template>
            </dd>
          </div>
          <div>
            <dt class="text-muted text-xs">{{ t('capacity.traffic.unknown') }}</dt>
            <dd class="text-highlighted tabular text-xl font-semibold">
              {{ bytes(data.total.unknown_bytes) }}
            </dd>
            <dd class="text-muted text-xs">{{ t('capacity.traffic.unknownHint') }}</dd>
          </div>
        </dl>
        <div class="h-56">
          <TimeSeriesChart
            :series="series"
            :label="t('capacity.traffic.chartLabel', { days })"
            scale="normal"
            kind="bar"
            stacked
            :format="bytes"
            time-format="day"
          />
        </div>
        <div
          v-if="data.sites.length > 1"
          class="ring-default mt-3 overflow-x-auto rounded-md ring-1"
        >
          <table class="w-full text-sm">
            <caption class="sr-only">
              {{
                t('capacity.traffic.bySite')
              }}
            </caption>
            <thead class="bg-elevated/50 text-muted text-left text-xs">
              <tr>
                <th scope="col" class="px-3 py-2 font-medium">{{ t('capacity.traffic.site') }}</th>
                <th scope="col" class="px-3 py-2 text-right font-medium">
                  {{ t('capacity.traffic.international') }}
                </th>
                <th scope="col" class="px-3 py-2 text-right font-medium">
                  {{ t('capacity.traffic.local') }}
                </th>
                <th scope="col" class="hidden px-3 py-2 text-right font-medium sm:table-cell">
                  {{ t('capacity.traffic.volume') }}
                </th>
              </tr>
            </thead>
            <tbody class="divide-default divide-y">
              <tr v-for="s in data.sites" :key="s.site_id">
                <td class="px-3 py-2">{{ s.site_name || s.site_id.slice(0, 8) }}</td>
                <td class="tabular px-3 py-2 text-right">
                  {{ formatPercent(s.international_share) }}
                </td>
                <td class="tabular px-3 py-2 text-right">{{ formatPercent(s.local_share) }}</td>
                <td class="tabular hidden px-3 py-2 text-right sm:table-cell">
                  {{ bytes(s.local_bytes + s.international_bytes + s.unknown_bytes) }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
      <p class="text-muted mt-2 text-xs">{{ countryNote }}</p>
    </template>
  </UCard>
</template>
