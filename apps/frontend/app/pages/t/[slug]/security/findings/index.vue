<script setup lang="ts">
import type { Finding } from '~~/types/api'

/**
 * Hallazgos (I1-18, frontend.md §8.2). KPIs del resumen de seguridad, filtros en la URL y
 * lista ordenada por severidad y recencia. Los hallazgos nuevos llegan por tiempo real
 * (tema `security`): entran arriba con un realce que se desvanece, sin mover lo que se está
 * leyendo (anclaje de scroll), y se anuncian de forma agregada (máx. 1 aviso cada 30 s).
 */
const { t, te } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const slug = computed(() => String(route.params.slug))

useSectionGuard()(findSection('tenant', 'security/findings'), slug.value)

const { siteName } = useSites()
const siteOptions = useSiteOptions()

const STATES = ['active', 'open', 'acknowledged', 'resolved', 'false_positive', 'all'] as const
const state = useQueryParam('state', 'active', STATES)
const severity = useQueryParam('severity', 'all', [
  'all',
  'critical',
  'high',
  'medium',
  'low',
] as const)
const KINDS = [
  'all',
  'outbound_scanning',
  'botnet_c2_communication',
  'spam_smtp_outbound',
  'beaconing',
  'ddos_participation',
  'reputation_hit',
] as const
const kind = useQueryParam('kind', 'all', KINDS)
const site = useQueryParam('site', 'all')

const { data: summary, refresh: refreshSummary } = useTenantQuery('security-summary', () =>
  unwrap($api.GET('/security/summary')),
)

const findings = ref<Finding[]>([])
const cursor = ref<string | null>(null)
const loading = ref(false)
const loadingMore = ref(false)
const error = shallowRef<unknown>(null)
const fresh = ref(new Set<string>())

function query(next?: string | null) {
  return {
    state:
      state.value === 'active'
        ? 'open,acknowledged'
        : state.value === 'all'
          ? undefined
          : state.value,
    severity: severity.value === 'all' ? undefined : severity.value,
    kind: kind.value === 'all' ? undefined : kind.value,
    site_id: site.value === 'all' ? undefined : site.value,
    sort: '-severity' as const,
    cursor: next ?? undefined,
    limit: 50,
  }
}

let seq = 0
async function load() {
  const mine = ++seq
  loading.value = true
  error.value = null
  try {
    const page = await unwrap($api.GET('/findings', { params: { query: query() } }))
    if (mine !== seq) return
    findings.value = page.data
    cursor.value = page.page.next_cursor
  } catch (e) {
    if (mine === seq) error.value = e
  } finally {
    if (mine === seq) loading.value = false
  }
}
async function loadMore() {
  loadingMore.value = true
  try {
    const page = await unwrap($api.GET('/findings', { params: { query: query(cursor.value) } }))
    findings.value = [...findings.value, ...page.data]
    cursor.value = page.page.next_cursor
  } finally {
    loadingMore.value = false
  }
}
onMounted(load)
watch([state, severity, kind, site], load)

// --- Tiempo real: altas por el tema `security` -------------------------------------------
const announcement = ref('')
let pendingAnnounce = 0
let lastAnnounce = 0
let announceTimer: ReturnType<typeof setTimeout> | undefined

function announce(n: number) {
  pendingAnnounce += n
  const wait = Math.max(0, lastAnnounce + 30_000 - Date.now())
  clearTimeout(announceTimer)
  announceTimer = setTimeout(() => {
    announcement.value = t('findings.live.announce', { n: pendingAnnounce })
    pendingAnnounce = 0
    lastAnnounce = Date.now()
  }, wait)
}
onBeforeUnmount(() => clearTimeout(announceTimer))

useRealtime('security', async (message) => {
  if (message.type !== 'event' || !message.event.type.endsWith('finding.opened')) return
  const id = String(message.event.data.finding_id ?? '')
  if (!id || findings.value.some((f) => f.id === id)) return
  try {
    const f = await unwrap(
      $api.GET('/findings/{finding_id}', { params: { path: { finding_id: id } } }),
    )
    if (!matchesFilters(f) || findings.value.some((x) => x.id === f.id)) return
    // Arriba de la lista, sin reordenar lo que hay debajo del cursor ni mover lo que se lee:
    // si el panel está desplazado, se compensa la altura insertada.
    const scroller = document.getElementById('main-content')?.parentElement
    const anchor = scroller && scroller.scrollTop > 0 ? firstVisibleRow(scroller) : null
    const top = anchor?.getBoundingClientRect().top ?? 0
    findings.value = [f, ...findings.value]
    if (scroller && anchor) {
      await nextTick()
      scroller.scrollTop += anchor.getBoundingClientRect().top - top
    }
    fresh.value = new Set([...fresh.value, f.id])
    announce(1)
    refreshSummary()
  } catch {
    // Se verá en la siguiente recarga.
  }
})

/** Primera fila visible del panel: el punto que no debe moverse al insertar arriba. */
function firstVisibleRow(scroller: HTMLElement) {
  const box = scroller.getBoundingClientRect()
  return (
    [...scroller.querySelectorAll<HTMLElement>('[data-finding-id]')].find((el) => {
      const r = el.getBoundingClientRect()
      return r.height > 0 && r.top >= box.top
    }) ?? null
  )
}

function matchesFilters(f: Finding) {
  const q = query()
  return (
    (!q.state || q.state.split(',').includes(f.state)) &&
    (!q.severity || q.severity === f.severity) &&
    (!q.kind || q.kind === f.kind) &&
    (!q.site_id || q.site_id === f.site_id)
  )
}

const filtered = computed(
  () =>
    state.value !== 'active' ||
    severity.value !== 'all' ||
    kind.value !== 'all' ||
    site.value !== 'all',
)
function clearFilters() {
  state.value = 'active'
  severity.value = 'all'
  kind.value = 'all'
  site.value = 'all'
}

const stateOptions = computed(() =>
  STATES.map((s) => ({ label: t(`findings.filters.state.${s}`), value: s })),
)
const severityOptions = computed(() => [
  { label: t('findings.filters.allSeverities'), value: 'all' },
  ...(['critical', 'high', 'medium', 'low'] as const).map((s) => ({
    label: t(`severity.${s}`),
    value: s,
  })),
])
const kindOptions = computed(() =>
  KINDS.map((k) => ({
    label:
      k === 'all'
        ? t('findings.filters.allKinds')
        : te(`findingKind.${k}`)
          ? t(`findingKind.${k}`)
          : k,
    value: k,
  })),
)

const severities = ['critical', 'high', 'medium', 'low'] as const
</script>

<template>
  <AppPage :title="t('nav.items.findings')" panel-id="findings">
    <section
      class="grid grid-cols-2 gap-3 md:grid-cols-4 xl:grid-cols-6"
      :aria-label="t('findings.kpis')"
      data-testid="findings-kpis"
    >
      <UCard v-for="s in severities" :key="s" :ui="{ body: 'p-3 sm:p-4' }">
        <SeverityBadge :severity="s" />
        <p class="text-highlighted mt-1 text-2xl font-semibold tabular">
          {{ summary?.open_by_severity[s] ?? '—' }}
        </p>
        <p class="text-muted text-xs">{{ t('findings.kpi.open') }}</p>
      </UCard>
      <UCard :ui="{ body: 'p-3 sm:p-4' }">
        <p class="text-muted text-sm">{{ t('findings.kpi.new24h') }}</p>
        <p class="text-highlighted mt-1 text-2xl font-semibold tabular">
          {{ summary?.new_last_24h ?? '—' }}
        </p>
      </UCard>
      <UCard :ui="{ body: 'p-3 sm:p-4' }">
        <p class="text-muted text-sm">{{ t('findings.kpi.affected') }}</p>
        <p class="text-highlighted mt-1 text-2xl font-semibold tabular">
          {{ summary?.affected_customers ?? '—' }}
        </p>
        <p v-if="summary" class="text-muted text-xs">
          {{ t('findings.kpi.infected', { n: summary.by_security_state.infected ?? 0 }) }}
        </p>
      </UCard>
    </section>

    <div
      class="grid grid-cols-2 gap-3 md:grid-cols-4 lg:flex lg:flex-wrap"
      role="group"
      :aria-label="t('findings.filters.label')"
    >
      <UFormField :label="t('findings.col.state')">
        <USelect
          v-model="state"
          :items="stateOptions"
          class="w-full lg:w-44"
          data-testid="filter-state"
        />
      </UFormField>
      <UFormField :label="t('findings.col.severity')">
        <USelect v-model="severity" :items="severityOptions" class="w-full lg:w-40" />
      </UFormField>
      <UFormField :label="t('findings.filters.kind')">
        <USelect v-model="kind" :items="kindOptions" class="w-full lg:w-48" />
      </UFormField>
      <UFormField :label="t('findings.col.site')">
        <USelect v-model="site" :items="siteOptions" class="w-full lg:w-44" />
      </UFormField>
    </div>

    <p class="sr-only" aria-live="polite" data-testid="findings-live">{{ announcement }}</p>

    <LoadingState v-if="loading && !findings.length" :rows="6" />
    <ErrorState v-else-if="error" :error="error" @retry="load" />
    <UCard v-else-if="!findings.length" class="py-6">
      <EmptyState
        v-if="filtered"
        icon="i-lucide-filter-x"
        :title="t('findings.emptyFiltered')"
        :actions="[
          {
            label: t('common.clearFilters'),
            color: 'neutral',
            variant: 'outline',
            onClick: clearFilters,
          },
        ]"
      />
      <EmptyState v-else icon="i-lucide-shield-check" :title="t('findings.empty')" />
    </UCard>
    <template v-else>
      <FindingsList :findings="findings" :slug="slug" :site-name="siteName" :fresh="fresh" />
      <div v-if="cursor" class="flex justify-center">
        <UButton
          color="neutral"
          variant="outline"
          icon="i-lucide-chevrons-down"
          :loading="loadingMore"
          :label="t('common.loadMore')"
          @click="loadMore"
        />
      </div>
    </template>
  </AppPage>
</template>
