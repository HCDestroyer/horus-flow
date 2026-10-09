<script setup lang="ts">
import type { DropdownMenuItem, TabsItem } from '@nuxt/ui'
import type { CustomerDetail, Finding } from '~~/types/api'

/**
 * Ficha de un cliente (I1-16, frontend.md §8.1). La URL lleva el ID opaco, nunca la IP. Tipo
 * siempre con su origen; "Infectado" siempre con razones y confianza (D18); ciclo de vida
 * explicado; acciones ocultas sin permiso (§12). Las consultas a fichas quedan registradas.
 */
const { t, d } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const can = useCan()
const { membership } = useTenant()
const slug = computed(() => String(route.params.slug))
const id = computed(() => String(route.params.clientId))

useSectionGuard()(findSection('tenant', 'clients'), slug.value)

const { siteName } = useSites()
const {
  data: customer,
  error,
  status,
  refresh,
} = useTenantQuery(
  () => `customer:${id.value}`,
  () =>
    unwrap($api.GET('/customers/{customer_id}', { params: { path: { customer_id: id.value } } })),
)
watch(error, (e) => {
  if (e instanceof ApiError && e.status === 404) {
    showError(createError({ statusCode: 404, statusMessage: 'Not Found' }))
  }
})

const { data: findingsPage, refresh: refreshFindings } = useTenantQuery(
  () => `customer:${id.value}:findings`,
  () =>
    unwrap(
      $api.GET('/customers/{customer_id}/findings', {
        params: { path: { customer_id: id.value } },
      }),
    ),
)
const { data: history, refresh: refreshHistory } = useTenantQuery(
  () => `customer:${id.value}:history`,
  () =>
    unwrap(
      $api.GET('/customers/{customer_id}/kind-history', {
        params: { path: { customer_id: id.value } },
      }),
    ).then((r) => r.data),
)
const { data: prefixes } = useTenantQuery(
  () => `customer:${id.value}:prefixes:${customer.value?.site_id ?? ''}`,
  () =>
    customer.value
      ? unwrap(
          $api.GET('/sites/{site_id}/client-prefixes', {
            params: { path: { site_id: customer.value.site_id } },
          }),
        ).then((r) => r.data)
      : Promise.resolve([]),
  { watch: [() => customer.value?.site_id] },
)

const findings = computed<Finding[]>(() => findingsPage.value?.data ?? [])
const active = computed(() =>
  findings.value.filter((f) => f.state === 'open' || f.state === 'acknowledged'),
)
const prefix = computed(() =>
  prefixes.value?.find((p) => p.id === customer.value?.client_prefix_id),
)
const v6 = computed(() => customer.value?.address.includes(':') ?? false)
const prefixLen = computed(() => (v6.value ? (prefix.value?.ipv6_client_len ?? 64) : null))
const shownAddress = computed(() =>
  customer.value
    ? v6.value
      ? `${customer.value.address}/${prefixLen.value}`
      : customer.value.address
    : '',
)

/** Confianza del estado de seguridad: la mayor de sus hallazgos activos (D18). */
const securityConfidence = computed(() =>
  active.value.length ? Math.max(...active.value.map((f) => f.confidence)) : null,
)
const securityReasons = computed(() =>
  active.value
    .slice()
    .sort((a, b) => b.confidence - a.confidence)
    .flatMap((f) => f.reasons.slice(0, 2).map((r) => ({ ...r, finding: f })))
    .slice(0, 4),
)

const manualChange = computed(() =>
  customer.value?.kind_source === 'manual'
    ? history.value?.find((h) => h.source === 'manual')
    : undefined,
)

const inactiveDays = computed(() =>
  customer.value
    ? Math.floor((Date.now() - new Date(customer.value.last_seen).getTime()) / 86_400_000)
    : 0,
)

const tab = useQueryParam('tab', 'summary', ['summary', 'traffic', 'security', 'history'] as const)
const tabs = computed<TabsItem[]>(() => [
  { label: t('clients.tabs.summary'), value: 'summary', icon: 'i-lucide-gauge' },
  { label: t('clients.tabs.traffic'), value: 'traffic', icon: 'i-lucide-arrow-down-up' },
  {
    label: t('clients.tabs.security', { n: active.value.length }),
    value: 'security',
    icon: 'i-lucide-shield-alert',
  },
  { label: t('clients.tabs.history'), value: 'history', icon: 'i-lucide-history' },
])

watch(tab, (value) => value === 'security' && refreshFindings())

const kindOpen = ref(false)
const resetOpen = ref(false)
const aliasOpen = ref(false)
const canKind = computed(() => can('customers.kind.write'))
const canAlias = computed(() => can('customers.update'))

function applied(next: CustomerDetail) {
  customer.value = next
  refreshHistory()
}

async function unlock() {
  if (!customer.value) return
  try {
    applied(
      await unwrap(
        $api.POST('/customers/{customer_id}/unlock-kind', {
          params: {
            path: { customer_id: customer.value.id },
            header: { 'If-Match': ifMatch(customer.value.version) },
          },
        }),
      ),
    )
    useToast().add({ title: t('clients.unlocked'), icon: 'i-lucide-lock-open', color: 'success' })
  } catch {
    refresh()
  }
}

const moreItems = computed<DropdownMenuItem[]>(() => [
  ...(customer.value?.kind_locked
    ? [{ label: t('clients.unlock'), icon: 'i-lucide-lock-open', onSelect: unlock }]
    : []),
  {
    label: t('clients.reset.action'),
    icon: 'i-lucide-rotate-ccw',
    color: 'error' as const,
    onSelect: () => (resetOpen.value = true),
  },
])

const breadcrumb = computed(() => [
  { label: membership.value?.tenant_name ?? '', to: `/t/${slug.value}` },
  { label: t('nav.items.clients'), to: `/t/${slug.value}/clients` },
  ...(customer.value
    ? [
        {
          label: siteName(customer.value.site_id),
          to: `/t/${slug.value}/nodes/${customer.value.site_id}`,
        },
        { label: customer.value.alias ?? shownAddress.value },
      ]
    : []),
])

// --- Resumen y tráfico ---
const range = ref<'24h' | '7d' | '30d'>('24h')
const {
  data: traffic,
  error: trafficError,
  refresh: refreshTraffic,
} = useTenantQuery(
  () => `customer:${id.value}:traffic:${range.value}`,
  () =>
    unwrap(
      $api.GET('/analytics/customers/{customer_id}/traffic', {
        params: { path: { customer_id: id.value }, query: { range: range.value } },
      }),
    ),
  { watch: [range] },
)
const series = computed(() =>
  (traffic.value?.data.series ?? []).map((s) => ({
    name:
      s.metric === 'up_bps'
        ? t('widgets.traffic_timeseries.up')
        : t('widgets.traffic_timeseries.down'),
    points: s.points as [string, number | null][],
  })),
)
const formatRate = (v: number) => joinUnit(formatBps(v))
const rangeItems = computed(() => [
  { label: t('ranges.24h'), value: '24h' },
  { label: t('ranges.7d'), value: '7d' },
  { label: t('ranges.30d'), value: '30d' },
])

useHead({ title: () => customer.value?.alias ?? t('clients.detailTitle') })
</script>

<template>
  <AppPage :title="t('clients.detailTitle')" panel-id="client-detail">
    <UBreadcrumb :items="breadcrumb" class="min-w-0" />

    <LoadingState v-if="status === 'pending' && !customer" :rows="5" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />

    <template v-else-if="customer">
      <UCard data-testid="client-header">
        <div class="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
          <div class="flex min-w-0 flex-col gap-2">
            <h2 class="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1">
              <span
                class="text-highlighted font-mono text-xl font-semibold break-all"
                data-testid="client-ip"
                >{{ shownAddress }}</span
              >
              <span v-if="customer.alias" class="text-default text-lg" data-testid="client-alias"
                >“{{ customer.alias }}”</span
              >
            </h2>
            <p class="text-muted flex flex-wrap gap-x-3 gap-y-1 text-sm">
              <NuxtLink :to="`/t/${slug}/nodes/${customer.site_id}`" class="hover:underline">{{
                siteName(customer.site_id)
              }}</NuxtLink>
              <span>· <RelativeTime :at="customer.last_seen" :prefix="t('clients.seen')" /></span>
              <span
                >·
                {{
                  t('clients.firstSeen', { date: d(new Date(customer.first_seen), 'short') })
                }}</span
              >
              <UBadge
                v-if="v6"
                color="neutral"
                variant="outline"
                :label="t('clients.ipv6Delegated', { len: prefixLen })"
              />
            </p>
            <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
              <ClientKindBadge
                :kind="customer.kind"
                :source="customer.kind_source"
                :locked="customer.kind_locked"
                :confidence="customer.kind_confidence"
              />
              <span v-if="manualChange" class="text-muted text-sm" data-testid="kind-manual-note">
                {{
                  t('clients.markedBy', {
                    date: d(new Date(manualChange.changed_at), 'short'),
                    reason: manualChange.manual_reason ?? '',
                  })
                }}
              </span>
              <UBadge
                :color="customer.status === 'active' ? 'neutral' : 'neutral'"
                :variant="customer.status === 'active' ? 'subtle' : 'outline'"
                :label="t(`clients.status.${customer.status}`)"
                data-testid="client-status"
              />
            </div>
          </div>
          <div v-if="canKind || canAlias" class="flex shrink-0 flex-wrap gap-2">
            <UButton
              v-if="canKind"
              icon="i-lucide-tags"
              :label="t('clients.setKind.action')"
              data-testid="set-kind"
              @click="kindOpen = true"
            />
            <UButton
              v-if="canAlias"
              color="neutral"
              variant="outline"
              icon="i-lucide-pencil"
              :label="t('clients.alias.action')"
              @click="aliasOpen = true"
            />
            <UDropdownMenu v-if="canKind" :items="moreItems">
              <UButton
                color="neutral"
                variant="outline"
                icon="i-lucide-ellipsis"
                :aria-label="t('common.more')"
                data-testid="client-more"
              />
            </UDropdownMenu>
          </div>
        </div>
      </UCard>

      <UAlert
        v-if="customer.status === 'inactive'"
        color="neutral"
        variant="subtle"
        icon="i-lucide-moon"
        :title="t('clients.inactive.title', { date: d(new Date(customer.last_seen), 'short') })"
        :description="
          customer.inactive_reason === 'prefix_removed'
            ? t('clients.inactive.prefixRemoved')
            : t('clients.inactive.noTraffic', { days: inactiveDays })
        "
        data-testid="inactive-explanation"
      />
      <UAlert
        v-if="prefix?.assignment_mode === 'dynamic'"
        color="info"
        variant="subtle"
        icon="i-lucide-shuffle"
        :title="t('clients.dynamicPool')"
      />
      <UAlert
        v-if="customer.reset_at"
        color="neutral"
        variant="outline"
        icon="i-lucide-rotate-ccw"
        :title="t('clients.resetAt', { date: d(new Date(customer.reset_at), 'long') })"
      />

      <!-- Estado de seguridad (D18): "Infectado" nunca sin razones y confianza. -->
      <UCard v-if="customer.security_state !== 'clean'" data-testid="client-security">
        <div class="flex flex-col gap-3">
          <div class="flex flex-wrap items-center gap-x-4 gap-y-1">
            <SecurityStateBadge :state="customer.security_state" :confidence="securityConfidence" />
            <span class="text-muted text-sm">{{
              t('clients.findingsCount', { n: customer.open_findings })
            }}</span>
          </div>
          <div>
            <h3 class="text-highlighted text-sm font-semibold">{{ t('clients.whyState') }}</h3>
            <ul class="mt-1.5 space-y-1.5">
              <li
                v-for="r in securityReasons"
                :key="r.finding.id + r.code"
                class="flex gap-2 text-sm"
              >
                <UIcon
                  name="i-lucide-dot"
                  class="text-muted mt-0.5 size-4 shrink-0"
                  aria-hidden="true"
                />
                <span>
                  {{ r.detail }}
                  <NuxtLink
                    :to="`/t/${slug}/security/findings/${r.finding.id}`"
                    class="text-muted text-xs hover:underline"
                  >
                    · {{ r.finding.summary.text }}
                  </NuxtLink>
                </span>
              </li>
            </ul>
          </div>
        </div>
      </UCard>

      <UTabs v-model="tab" :items="tabs" :content="false" class="w-full" variant="link" />

      <section
        v-if="tab === 'summary'"
        class="grid gap-4 sm:grid-cols-3"
        :aria-label="t('clients.tabs.summary')"
      >
        <UCard>
          <p class="text-muted text-sm">{{ t('clients.kpi.down') }}</p>
          <p class="text-highlighted mt-1 text-2xl font-semibold tabular">
            {{
              customer.traffic_24h?.down_bytes
                ? joinUnit(formatBytes(Number(customer.traffic_24h.down_bytes)))
                : '—'
            }}
          </p>
          <p class="text-muted text-sm tabular">
            ↑
            {{
              customer.traffic_24h?.up_bytes
                ? joinUnit(formatBytes(Number(customer.traffic_24h.up_bytes)))
                : '—'
            }}
          </p>
        </UCard>
        <UCard>
          <p class="text-muted text-sm">{{ t('clients.kpi.top') }}</p>
          <p class="text-highlighted mt-1 text-lg font-semibold">
            {{ customer.traffic_24h?.top_category ?? '—' }}
          </p>
        </UCard>
        <UCard>
          <p class="text-muted text-sm">{{ t('clients.kpi.findings') }}</p>
          <p class="text-highlighted mt-1 text-2xl font-semibold tabular">
            {{ customer.open_findings }}
          </p>
          <p v-if="customer.notes" class="text-muted mt-1 text-sm">{{ customer.notes }}</p>
        </UCard>
      </section>

      <UCard v-else-if="tab === 'traffic'">
        <template #header>
          <div class="flex flex-wrap items-center justify-between gap-2">
            <h3 class="text-highlighted font-semibold">{{ t('clients.trafficTitle') }}</h3>
            <USelect
              v-model="range"
              :items="rangeItems"
              class="w-36"
              :aria-label="t('traffic.range')"
            />
          </div>
        </template>
        <DegradedNotice v-if="trafficError" :error="trafficError" @retry="refreshTraffic()" />
        <div v-else class="dash h-72">
          <TimeSeriesChart
            :series="series"
            :label="t('clients.trafficTitle')"
            scale="normal"
            :format="formatRate"
            area
          />
        </div>
      </UCard>

      <section
        v-else-if="tab === 'security'"
        :aria-label="t('clients.tabs.security', { n: active.length })"
      >
        <UCard v-if="!findings.length" class="py-6">
          <EmptyState icon="i-lucide-shield-check" :title="t('findings.empty')" />
        </UCard>
        <FindingsList
          v-else
          :findings="findings"
          :slug="slug"
          :site-name="siteName"
          :show-customer="false"
        />
      </section>

      <UCard v-else-if="tab === 'history'">
        <ol class="relative flex flex-col gap-4" data-testid="kind-history">
          <li v-for="h in history ?? []" :key="h.id" class="flex gap-3">
            <UIcon
              :name="
                h.source === 'reset'
                  ? 'i-lucide-rotate-ccw'
                  : h.source === 'manual'
                    ? 'i-lucide-user-pen'
                    : h.source === 'scoring'
                      ? 'i-lucide-sparkles'
                      : 'i-lucide-circle-plus'
              "
              class="text-muted mt-0.5 size-5 shrink-0"
              aria-hidden="true"
            />
            <div class="min-w-0">
              <p class="text-highlighted text-sm font-medium">
                {{ t(`clients.history.${h.source}`, { kind: t(`clients.kind.${h.to_kind}`) }) }}
              </p>
              <p class="text-muted text-xs">
                {{ d(new Date(h.changed_at), 'long') }}
                <template v-if="h.confidence">
                  ·
                  {{
                    t('clients.history.confidence', { pct: formatPercent(h.confidence) })
                  }}</template
                >
              </p>
              <p v-if="h.manual_reason" class="text-default mt-0.5 text-sm">
                “{{ h.manual_reason }}”
              </p>
            </div>
          </li>
        </ol>
      </UCard>

      <SetKindModal v-model:open="kindOpen" :customer="customer" @saved="applied" />
      <ResetClientModal v-model:open="resetOpen" :customer="customer" @done="applied" />
      <AliasModal v-model:open="aliasOpen" :customer="customer" @saved="applied" />
    </template>

    <p class="text-dimmed flex items-center gap-1.5 text-xs">
      <UIcon name="i-lucide-shield-check" class="size-4 shrink-0" aria-hidden="true" />
      {{ t('clients.privacy') }}
    </p>
  </AppPage>
</template>
