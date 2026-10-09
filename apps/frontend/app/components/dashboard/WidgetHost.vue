<script setup lang="ts">
import type { DashboardWidget } from '~~/types/api'
import { isEmptyData, widgetFor } from '~/widgets/registry'

/**
 * Contenedor de un widget (frontend.md §6.3, §9): pide SUS datos, resuelve los estados
 * (carga, vacío, error con "Reintentar", sin permiso, degradado, no disponible) y la
 * frescura, y pinta el `Widget.vue` del tipo. Un fallo aquí nunca afecta a otro widget.
 */
const props = defineProps<{
  widget: DashboardWidget
  dashboardId: string
  /** Refresco por defecto del dashboard (s). */
  refreshSeconds: number
}>()

const { t, te } = useI18n()
const now = useNow()
const ctx = inject(DASHBOARD_CONTEXT)!
const registered = widgetFor(props.widget.type)
const scale = computed(() => ctx.scale.value)

const hasData = registered ? registered.catalog.data_endpoint_kind !== 'none' : false
const refresh = props.widget.refresh_seconds ?? props.refreshSeconds
const { data, error, status, reload } = useWidgetData({
  dashboardId: props.dashboardId,
  widget: props.widget,
  refreshSeconds: refresh,
  enabled: hasData,
  preview: ctx.preview,
})

const title = computed(() => props.widget.title ?? registered?.catalog.title ?? props.widget.type)
const headingId = `w-${props.widget.id}-title`
const isHeader = props.widget.type === 'noc_header'

/** Último dato en vivo (WS) recibido por el widget. */
const liveAt = ref<number | null>(null)
function onLive(at: number) {
  liveAt.value = at
  ctx.report(at)
}

const dataAt = computed(() => {
  if (liveAt.value) return liveAt.value
  return data.value ? dataTime(data.value.meta) : null
})
watch(dataAt, (at) => at && ctx.report(at), { immediate: true })

const apiError = computed(() => (error.value instanceof ApiError ? error.value : null))
const forbidden = computed(() => apiError.value?.code === 'WIDGET_TYPE_NOT_ALLOWED')
const degraded = computed(
  () =>
    apiError.value?.status === 503 ||
    apiError.value?.code === 'ANALYTICS_UNAVAILABLE' ||
    apiError.value?.code === 'SERVICE_UNAVAILABLE',
)

type View = 'unavailable' | 'loading' | 'forbidden' | 'degraded' | 'error' | 'empty' | 'ready'
const view = computed<View>(() => {
  if (!registered) return 'unavailable'
  if (!hasData) return 'ready'
  if (data.value) {
    const d = data.value.data
    const empty = registered.isEmpty ? registered.isEmpty(d) : isEmptyData(d)
    return empty ? 'empty' : 'ready'
  }
  if (status.value === 'error') {
    if (forbidden.value) return 'forbidden'
    if (degraded.value) return 'degraded'
    return 'error'
  }
  return 'loading'
})

const freshnessLevelNow = computed(() => {
  if (!dataAt.value) return 'fresh'
  return freshnessLevel((now.value - dataAt.value) / 1000, liveAt.value ? 10 : refresh)
})
const emptyText = computed(() => {
  const key = `widgets.${props.widget.type}.empty`
  return te(key) ? t(key) : t('widgetHost.empty')
})
</script>

<template>
  <section
    v-fit-optional
    class="widget-card relative flex min-h-0 min-w-0 flex-col overflow-hidden"
    :class="isHeader ? 'widget-band' : 'bg-default ring-default rounded-lg ring-1'"
    :aria-labelledby="isHeader ? undefined : headingId"
    :aria-label="isHeader ? title : undefined"
    :data-widget-type="widget.type"
    :data-widget-id="widget.id"
    :data-state="view"
    data-testid="widget"
  >
    <header v-if="!isHeader" class="flex min-w-0 items-start justify-between gap-[0.75em]">
      <h2 :id="headingId" class="w-title text-highlighted min-w-0 font-semibold">
        {{ title }}
      </h2>
      <div class="flex shrink-0 items-center gap-[0.5em]">
        <UBadge
          v-if="data?.meta.partial"
          color="warning"
          variant="subtle"
          icon="i-lucide-circle-dashed"
          :label="t('widgetHost.partial')"
          class="w-meta"
        />
        <UBadge
          v-if="data && error"
          :color="degraded ? 'warning' : 'neutral'"
          variant="subtle"
          icon="i-lucide-triangle-alert"
          :label="degraded ? t('widgetHost.degradedShort') : t('widgetHost.refreshFailed')"
          class="w-meta"
        />
        <FreshnessLabel
          v-if="dataAt && view === 'ready'"
          :at="dataAt"
          :expected-seconds="liveAt ? 10 : refresh"
          :live="!!liveAt"
          :always="scale === 'normal'"
        />
      </div>
    </header>

    <div
      class="relative flex min-h-0 flex-1 flex-col"
      :class="{ 'opacity-55': freshnessLevelNow === 'stale' }"
    >
      <WidgetSkeleton
        v-if="view === 'loading'"
        :shape="registered?.manifest.placeholder ?? 'list'"
      />

      <div v-else-if="view === 'unavailable'" class="widget-state">
        <UIcon name="i-lucide-puzzle" class="text-dimmed size-6" aria-hidden="true" />
        <p class="w-label text-muted">{{ t('widgetHost.unavailable') }}</p>
      </div>

      <div v-else-if="view === 'forbidden'" class="widget-state" data-testid="widget-forbidden">
        <UIcon name="i-lucide-lock" class="text-dimmed size-6" aria-hidden="true" />
        <p class="w-label text-muted">{{ t('widgetHost.forbidden') }}</p>
      </div>

      <div v-else-if="view === 'degraded'" class="widget-state" data-testid="widget-degraded">
        <UIcon name="i-lucide-database-zap" class="text-warning size-6" aria-hidden="true" />
        <p class="w-label text-highlighted font-medium">{{ t('widgetHost.degradedTitle') }}</p>
        <p class="w-meta text-muted max-w-prose">{{ t('widgetHost.degradedHint') }}</p>
      </div>

      <div
        v-else-if="view === 'error'"
        class="widget-state"
        role="alert"
        data-testid="widget-error"
      >
        <UIcon name="i-lucide-circle-alert" class="text-error size-6" aria-hidden="true" />
        <template v-if="scale === 'wall'">
          <p class="w-label text-muted">{{ t('widgetHost.wallError') }}</p>
        </template>
        <template v-else>
          <p class="w-label text-highlighted font-medium">{{ t('widgetHost.error') }}</p>
          <p v-if="apiError?.problem.trace_id" class="text-dimmed font-mono text-xs break-all">
            {{ apiError.code }} · {{ apiError.problem.trace_id }}
          </p>
          <UButton
            size="sm"
            color="neutral"
            variant="outline"
            icon="i-lucide-rotate-ccw"
            :label="t('states.retry')"
            @click="reload()"
          />
        </template>
      </div>

      <div v-else-if="view === 'empty'" class="widget-state" data-testid="widget-empty">
        <UIcon name="i-lucide-inbox" class="text-dimmed size-6" aria-hidden="true" />
        <p class="w-label text-muted max-w-prose">{{ emptyText }}</p>
      </div>

      <component
        :is="registered.component"
        v-else-if="registered"
        :widget="widget"
        :data="data?.data ?? null"
        :meta="data?.meta ?? null"
        :scale="scale"
        :rows="widget.position.h"
        @live="onLive"
      />
    </div>
  </section>
</template>
