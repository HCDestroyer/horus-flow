<script setup lang="ts">
import type { CapacityUplink } from '~~/types/api'

/**
 * Capacidad (NOC): salud explicable de cada nodo y la tabla de sus enlaces de subida
 * (utilización ahora y p95, horas sobre 70 / 85 %, pérdida / latencia / jitter por enlace,
 * diagnóstico lleno o averiado, capacidad contratada y pronóstico con recomendación), la
 * salida internacional agregada, el reparto local vs internacional y las sondas de respaldo
 * de Horus. Filtros de nodo y ventana en la URL; `GET /capacity/overview` cada 60 s. Horus
 * nunca escribe en routers: las sondas netwatch las pega el usuario.
 */
const { t, te } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const can = useCan()
const slug = computed(() => String(route.params.slug))
useSectionGuard()(findSection('tenant', 'capacity'), slug.value)
const manage = computed(() => can('snmp.manage'))

const DAYS = ['1', '7', '30'] as const
const site = useQueryParam('site', 'all')
const days = useQueryParam('days', '7', DAYS)
const siteOptions = useSiteOptions()
const dayOptions = computed(() => DAYS.map((d) => ({ label: t(`capacity.window.${d}`), value: d })))
const siteId = computed(() => (site.value === 'all' ? null : site.value))

const { data, error, status, refresh } = useTenantQuery(
  () => `capacity:overview:${site.value}:${days.value}`,
  () =>
    unwrap(
      $api.GET('/capacity/overview', {
        params: {
          query: { days: Number(days.value), ...(siteId.value ? { site_id: siteId.value } : {}) },
        },
      }),
    ),
  { watch: [site, days] },
)
useAutoRefresh(() => refresh(), 60_000)

const sites = computed(() => data.value?.data ?? [])
const settings = computed(() => data.value?.settings)
const egress = computed(() => data.value?.egress ?? null)
const uplinkCount = computed(() => sites.value.reduce((n, s) => n + s.uplinks.length, 0))

const configOpen = ref(false)
const configuring = ref<CapacityUplink | null>(null)
function configure(u: CapacityUplink) {
  configuring.value = u
  configOpen.value = true
}
const snippetOpen = ref(false)
const snippetRouter = ref<string | null>(null)
function snippet(routerId: string) {
  snippetRouter.value = routerId
  snippetOpen.value = true
}
const settingsOpen = ref(false)
// Los ajustes cambian el reparto local / internacional (país, ASN y prefijos locales).
const settingsVersion = ref(0)
function onSettingsSaved() {
  settingsVersion.value++
  refresh()
}
const probesError = (code: string) =>
  te(`capacity.probesError.${code}`) ? t(`capacity.probesError.${code}`) : code
const bps = (v: number | null | undefined) => joinUnit(formatBps(v))

useHead({ title: () => t('capacity.title') })
</script>

<template>
  <AppPage :title="t('capacity.title')" panel-id="capacity">
    <template #toolbar-left>
      <USelect
        v-model="site"
        :items="siteOptions"
        class="w-48"
        :aria-label="t('capacity.filters.site')"
        data-testid="capacity-filter-site"
      />
      <USelect
        v-model="days"
        :items="dayOptions"
        class="w-40"
        :aria-label="t('capacity.filters.window')"
        data-testid="capacity-filter-days"
      />
    </template>
    <template v-if="manage" #toolbar-right>
      <UButton
        color="neutral"
        variant="outline"
        icon="i-lucide-sliders-horizontal"
        :label="t('capacity.settings.open')"
        data-testid="capacity-settings-open"
        @click="settingsOpen = true"
      />
    </template>

    <p class="text-muted max-w-3xl text-sm">{{ t('capacity.intro') }}</p>

    <LoadingState v-if="status === 'pending' && !data" :rows="5" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <template v-else-if="data">
      <UAlert
        v-if="!data.meta.history"
        color="warning"
        variant="subtle"
        icon="i-lucide-database-zap"
        :title="t('capacity.noHistory')"
        data-testid="capacity-no-history"
      />

      <UCard v-if="egress" :ui="{ body: 'p-4 sm:p-4' }" data-testid="capacity-egress">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div class="min-w-0">
            <h2 class="text-highlighted flex items-center gap-2 text-base font-semibold">
              <UIcon name="i-lucide-globe" class="size-4" aria-hidden="true" />
              {{ t('capacity.egress.title') }}
            </h2>
            <p class="text-muted text-sm">
              {{ t('capacity.egress.members', { n: egress.members.length }) }}
            </p>
          </div>
          <UButton
            :to="`/t/${slug}/capacity/egress`"
            color="neutral"
            variant="outline"
            size="sm"
            trailing-icon="i-lucide-arrow-right"
            :label="t('capacity.egress.detail')"
            data-testid="capacity-egress-detail"
          />
        </div>
        <dl class="mt-3 grid grid-cols-2 gap-3 sm:grid-cols-4">
          <div>
            <dt class="text-muted text-xs">{{ t('capacity.egress.capacity') }}</dt>
            <dd class="text-highlighted tabular font-semibold">{{ bps(egress.capacity_bps) }}</dd>
          </div>
          <div>
            <dt class="text-muted text-xs">{{ t('capacity.table.now') }}</dt>
            <dd class="text-highlighted tabular font-semibold">
              {{ formatPercent(egress.utilization) }}
            </dd>
            <dd class="text-muted text-xs tabular">↓ {{ bps(egress.rx_bps) }}</dd>
          </div>
          <div>
            <dt class="text-muted text-xs">{{ t('capacity.table.p95') }}</dt>
            <dd class="text-highlighted tabular font-semibold">
              {{ formatPercent(egress.window?.util_p95) }}
            </dd>
          </div>
          <div>
            <dt class="text-muted text-xs">{{ t('capacity.table.forecast') }}</dt>
            <dd v-if="egress.forecast"><ForecastLevelBadge :level="egress.forecast.level" /></dd>
            <dd v-else class="text-muted text-sm">{{ t('capacity.forecast.none') }}</dd>
          </div>
        </dl>
        <p
          v-if="egress.forecast"
          class="text-highlighted mt-3 text-sm"
          data-testid="capacity-egress-recommendation"
        >
          {{ egress.forecast.recommendation }}
        </p>
      </UCard>

      <UCard v-if="!sites.length" class="py-6">
        <EmptyState
          icon="i-lucide-gauge"
          :title="t('capacity.empty')"
          :description="t('capacity.emptyHint')"
        />
      </UCard>

      <section
        v-for="s in sites"
        :key="s.site_id ?? s.site_name"
        class="flex flex-col gap-3"
        :aria-labelledby="`site-${s.site_id ?? 'none'}`"
        data-testid="capacity-site"
        :data-health="s.health.status"
      >
        <header class="flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <h2 :id="`site-${s.site_id ?? 'none'}`" class="text-highlighted text-base font-semibold">
            {{ s.site_name || t('capacity.noSite') }}
          </h2>
          <SiteHealthBadge :status="s.health.status" />
          <p class="text-muted min-w-0 basis-full text-sm" data-testid="capacity-site-summary">
            {{ s.health.summary }}
          </p>
        </header>
        <UplinksTable
          v-if="s.uplinks.length && settings"
          :uplinks="s.uplinks"
          :settings="settings"
          :manage="manage"
          :slug="slug"
          @configure="configure"
        />
        <p v-else class="text-muted text-sm">{{ t('capacity.noUplinks') }}</p>
        <div class="flex flex-wrap gap-x-6 gap-y-2 text-sm">
          <div
            v-for="r in s.routers"
            :key="r.router_id"
            class="flex min-w-0 flex-wrap items-center gap-2"
            data-testid="capacity-router"
          >
            <span class="text-highlighted font-mono">{{ r.name }}</span>
            <span
              v-if="r.probes_error"
              class="text-muted inline-flex min-w-0 items-start gap-1 text-xs"
              data-testid="capacity-probes-error"
            >
              <UIcon name="i-lucide-radar" class="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
              <span class="min-w-0">{{ probesError(r.probes_error) }}</span>
            </span>
            <template v-else>
              <span
                v-for="p in s.router_probes.filter((x) => x.router_id === r.router_id)"
                :key="p.probe_id"
                class="text-muted tabular text-xs"
              >
                {{ p.target }}: {{ formatMs(p.rtt_avg_ms) }} ·
                {{ p.loss_pct != null ? `${formatNumber(p.loss_pct, 1)} %` : '—' }}
              </span>
            </template>
            <UButton
              v-if="manage"
              size="xs"
              color="neutral"
              variant="ghost"
              icon="i-lucide-terminal"
              :label="t('capacity.actions.snippet')"
              :data-testid="`capacity-snippet-${r.name}`"
              @click="snippet(r.router_id)"
            />
          </div>
        </div>
      </section>

      <TrafficSplitCard :site-id="siteId" :days="30" :version="settingsVersion" />

      <UCard
        v-if="data.horus_probes.length"
        :ui="{ body: 'p-4 sm:p-4' }"
        data-testid="capacity-horus-probes"
      >
        <h2 class="text-highlighted text-base font-semibold">
          {{ t('capacity.horusProbes.title') }}
        </h2>
        <p class="text-muted mb-2 text-sm">{{ t('capacity.horusProbes.intro') }}</p>
        <ul class="divide-default divide-y text-sm">
          <li
            v-for="p in data.horus_probes"
            :key="p.probe_id"
            class="flex flex-wrap justify-between gap-2 py-2"
          >
            <span
              >{{ p.label }} <span class="text-muted font-mono">· {{ p.target }}</span></span
            >
            <span class="text-muted tabular">
              {{ p.loss_pct != null ? `${formatNumber(p.loss_pct, 1)} %` : '—' }} ·
              {{ formatMs(p.rtt_avg_ms) }} · ±
              {{ formatMs(p.jitter_ms) }}
            </span>
          </li>
        </ul>
      </UCard>

      <p class="text-muted text-xs">{{ t('capacity.footnote', { n: uplinkCount }) }}</p>
    </template>

    <UplinkConfigModal v-model:open="configOpen" :uplink="configuring" @saved="refresh()" />
    <NetwatchSnippetModal v-model:open="snippetOpen" :router-id="snippetRouter" />
    <CapacitySettingsModal v-model:open="settingsOpen" @saved="onSettingsSaved" />
  </AppPage>
</template>
