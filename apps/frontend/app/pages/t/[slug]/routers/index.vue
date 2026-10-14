<script setup lang="ts">
import type { RouterEffectiveStatus, RouterStatus } from '~~/types/api'

/**
 * Salud de routers (EP-08, frontend.md §4, §8.4): la vista NOC de los routers del ISP.
 * Barra con el resumen por estado (números que ruedan), filtros de nodo y estado en la URL,
 * aviso cuando el sondeador SNMP no late o está desactivado (los estados son los últimos
 * conocidos: `stale` nunca es "caído"), y una rejilla de tarjetas, problemas primero
 * (`RouterHealthCard`). Datos de `GET /routers/status` cada 30 s y, entre medias, el tema
 * `routers.status` corrige solo la fila del router que cambió. `?scale=wall` abre el modo
 * mural: layout `wall`, rejilla que llena la pantalla sin scroll, tipografía mural y Escape
 * para salir.
 */
const { t } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const can = useCan()
const { membership } = useTenant()
const slug = computed(() => String(route.params.slug))

useSectionGuard()(findSection('tenant', 'routers'), slug.value)

const wall = computed(() => route.query.scale === 'wall')
setPageLayout(wall.value ? 'wall' : 'default')
watch(wall, (w) => setPageLayout(w ? 'wall' : 'default'))

const STATUS_FILTERS = ['all', 'problems', ...ROUTER_STATUSES] as const
const site = useQueryParam('site', 'all')
const statusFilter = useQueryParam('status', 'all', STATUS_FILTERS)
const siteOptions = useSiteOptions()
const statusOptions = computed(() => [
  { label: t('routersHealth.filters.allStatuses'), value: 'all' },
  { label: t('routersHealth.filters.problems'), value: 'problems' },
  ...ROUTER_STATUSES.map((s) => ({ label: t(`routerStatus.${s}`), value: s })),
])

const { data, error, status, refresh } = useTenantQuery('routers:status', () =>
  unwrap($api.GET('/routers/status')),
)
useAutoRefresh(() => refresh(), 30_000)

// Copia local de las filas para aplicar los eventos en vivo sin recargar la lista.
const rows = ref<RouterStatus[]>([])
const liveAt = ref<Record<string, number>>({})
watch(
  data,
  (d) => {
    if (d) rows.value = d.data
  },
  { immediate: true },
)
useRealtime('routers.status', (m) => {
  if (m.type !== 'event') return
  const id = String(m.event.data.router_id ?? '')
  const i = rows.value.findIndex((r) => r.router_id === id)
  if (i < 0) {
    refresh()
    return
  }
  const next = [...rows.value]
  next[i] = patchRouterStatus(next[i]!, m.event.data)
  rows.value = next
  liveAt.value = { ...liveAt.value, [id]: new Date(m.event.time).getTime() }
})

const summary = computed(() => summarizeStatuses(rows.value))
const filtered = computed(() =>
  sortRouterStatuses(
    rows.value.filter(
      (r) =>
        (site.value === 'all' || r.site_id === site.value) &&
        (statusFilter.value === 'all' ||
          (statusFilter.value === 'problems'
            ? isRouterProblem(r.status)
            : r.status === (statusFilter.value as RouterEffectiveStatus))),
    ),
  ),
)
const poller = computed(() => data.value?.meta.poller ?? null)
const pollerNotice = computed<'disabled' | 'unhealthy' | null>(() => {
  if (!poller.value) return null
  if (poller.value.enabled === false) return 'disabled'
  if (!poller.value.healthy) return 'unhealthy'
  return null
})
const canSeries = computed(() => can('snmp.read'))
const thresholdsOpen = ref(false)
const tenantName = computed(() => membership.value?.tenant_name ?? '')
const generatedAt = computed(() =>
  data.value ? new Date(data.value.meta.generated_at).getTime() : null,
)

// Mural: la rejilla reparte N tarjetas en columnas y filas que llenen la pantalla (tarjetas
// cercanas a 1,2:1) sin scroll; Esc vuelve y el botón de salida aparece al mover el puntero.
const viewport = ref({ w: 1920, h: 1080 })
function onResize() {
  viewport.value = { w: window.innerWidth, h: window.innerHeight }
}
const wallGrid = computed(() => {
  const n = Math.max(1, filtered.value.length)
  const { w, h } = viewport.value
  const avail = h - 0.11 * h
  let best = { cols: 1, rows: n, score: Infinity }
  for (let cols = 1; cols <= n; cols++) {
    const rowsN = Math.ceil(n / cols)
    const aspect = w / cols / (avail / rowsN)
    const score = Math.abs(aspect - 1.2)
    if (score < best.score) best = { cols, rows: rowsN, score }
  }
  return {
    gridTemplateColumns: `repeat(${best.cols}, minmax(0, 1fr))`,
    gridTemplateRows: `repeat(${best.rows}, minmax(0, 1fr))`,
  }
})
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
onMounted(() => {
  onResize()
  window.addEventListener('keydown', onKey)
  window.addEventListener('resize', onResize)
})
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey)
  window.removeEventListener('resize', onResize)
  clearTimeout(hideTimer)
})

useHead({ title: () => t('routersHealth.title') })
</script>

<template>
  <div
    v-if="wall"
    class="dash-wall relative flex h-dvh flex-col gap-[var(--w-gap)]"
    :class="{ 'cursor-none': !exitVisible }"
    data-testid="routers-wall"
    @pointermove="poke"
  >
    <header
      class="widget-band flex min-w-0 items-center justify-between gap-[1em] py-[calc(var(--w-pad)*0.5)]"
      data-layout-box
    >
      <h1 class="w-title text-highlighted truncate font-semibold">
        {{ t('routersHealth.title') }}
        <span class="text-muted font-normal">· {{ tenantName }}</span>
      </h1>
      <ul class="flex shrink-0 gap-[1.2em] tabular" :aria-label="t('routersHealth.summary.label')">
        <li
          v-for="b in STATUS_BUCKETS"
          :key="b"
          class="flex items-baseline gap-[0.35em] whitespace-nowrap"
          :data-summary="b"
        >
          <span
            class="w-kpi-2"
            :class="
              summary[b] > 0 ? routerStatusVisual(b === 'nodata' ? 'stale' : b).cls : 'text-dimmed'
            "
            ><RollingNumber :value="summary[b]"
          /></span>
          <span class="w-meta text-muted">{{ t(`routersHealth.summary.${b}`) }}</span>
        </li>
      </ul>
      <FreshnessLabel v-if="generatedAt" :at="generatedAt" :expected-seconds="30" />
    </header>
    <UAlert
      v-if="pollerNotice"
      color="warning"
      variant="subtle"
      icon="i-lucide-radio"
      class="w-label"
      :title="t(`routersHealth.poller.${pollerNotice}Title`)"
      data-testid="poller-notice"
    />
    <div class="grid min-h-0 flex-1 gap-[var(--w-gap)]" :style="wallGrid">
      <RouterHealthCard
        v-for="r in filtered"
        :key="r.router_id"
        :row="r"
        scale="wall"
        :can-series="canSeries"
        :live-at="liveAt[r.router_id] ?? null"
      />
    </div>
    <UButton
      color="neutral"
      variant="solid"
      icon="i-lucide-minimize-2"
      :label="t('routersHealth.exitWall')"
      class="absolute end-3 bottom-3 transition-opacity focus-visible:opacity-100"
      :class="exitVisible ? 'opacity-100' : 'opacity-0'"
      data-testid="exit-wall"
      @click="exitWall"
      @focus="poke"
    />
  </div>

  <AppPage v-else :title="t('routersHealth.title')" panel-id="routers-health">
    <template #navbar-right>
      <UButton
        v-if="canSeries"
        color="neutral"
        variant="outline"
        icon="i-lucide-gauge"
        :aria-label="t('routerThresholds.open')"
        data-testid="open-thresholds"
        @click="thresholdsOpen = true"
      >
        <span class="hidden sm:inline">{{ t('routerThresholds.open') }}</span>
      </UButton>
      <UButton
        color="neutral"
        variant="outline"
        icon="i-lucide-maximize-2"
        :aria-label="t('routersHealth.wall')"
        :to="{ path: route.path, query: { ...route.query, scale: 'wall' } }"
        data-testid="open-wall"
      >
        <span class="hidden sm:inline">{{ t('routersHealth.wall') }}</span>
      </UButton>
    </template>
    <template #toolbar-left>
      <div class="flex flex-wrap items-center gap-2">
        <USelect
          v-model="site"
          :items="siteOptions"
          class="w-44"
          :aria-label="t('routersHealth.filters.site')"
          data-testid="routers-site"
        />
        <USelect
          v-model="statusFilter"
          :items="statusOptions"
          class="w-48"
          :aria-label="t('routersHealth.filters.status')"
          data-testid="routers-status"
        />
      </div>
    </template>

    <UCard :ui="{ body: 'p-4 sm:p-4' }" data-testid="routers-summary">
      <div class="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
        <ul
          class="flex flex-wrap gap-x-6 gap-y-2 tabular"
          :aria-label="t('routersHealth.summary.label')"
        >
          <li
            v-for="b in STATUS_BUCKETS"
            :key="b"
            class="flex items-baseline gap-1.5"
            :data-summary="b"
          >
            <span
              class="text-2xl font-semibold"
              :class="
                summary[b] > 0
                  ? routerStatusVisual(b === 'nodata' ? 'stale' : b).cls
                  : 'text-dimmed'
              "
              ><RollingNumber :value="summary[b]"
            /></span>
            <span class="text-muted text-sm">{{ t(`routersHealth.summary.${b}`) }}</span>
          </li>
        </ul>
        <p v-if="poller && !pollerNotice" class="text-muted text-sm" data-testid="poller-ok">
          {{ t('routersHealth.poller.ok', { n: poller.targets, s: poller.interval_seconds }) }}
          <RelativeTime :at="poller.last_cycle_at" />
        </p>
      </div>
    </UCard>

    <UAlert
      v-if="pollerNotice"
      color="warning"
      variant="subtle"
      icon="i-lucide-radio"
      :title="t(`routersHealth.poller.${pollerNotice}Title`)"
      data-testid="poller-notice"
    >
      <template #description>
        <template v-if="pollerNotice === 'unhealthy'">
          {{ t('routersHealth.poller.unhealthyBody') }}
          <RelativeTime :at="poller?.last_cycle_at" />
        </template>
        <template v-else>{{ t('routersHealth.poller.disabledBody') }}</template>
      </template>
    </UAlert>

    <LoadingState v-if="status === 'pending' && !data" :rows="4" />
    <ErrorState v-else-if="error && !data" :error="error" @retry="refresh()" />
    <UCard v-else-if="!filtered.length" class="py-4">
      <EmptyState
        icon="i-lucide-heart-pulse"
        :title="rows.length ? t('routersHealth.empty') : t('routersHealth.emptyAll')"
        :actions="
          rows.length
            ? []
            : [{ label: t('nav.items.nodes'), to: `/t/${slug}/nodes`, icon: 'i-lucide-router' }]
        "
      />
    </UCard>
    <div
      v-else
      class="grid gap-4 [grid-template-columns:repeat(auto-fill,minmax(min(100%,19rem),1fr))]"
      role="list"
      :aria-label="t('routersHealth.listLabel')"
      data-testid="routers-grid"
    >
      <RouterHealthCard
        v-for="r in filtered"
        :key="r.router_id"
        role="listitem"
        :row="r"
        :to="`/t/${slug}/routers/${r.router_id}`"
        :can-series="canSeries"
        :live-at="liveAt[r.router_id] ?? null"
      />
    </div>
    <RouterThresholdsModal v-model:open="thresholdsOpen" />
  </AppPage>
</template>
