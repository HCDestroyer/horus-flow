<script setup lang="ts">
import type { Dashboard } from '~~/types/api'
import { registeredTypes, widgetFor } from '~/widgets/registry'

/**
 * Galería de widgets (I0-16, "página /dev/widgets"): cada tipo registrado con datos del
 * ISP actual, en escala normal o mural (`?scale=wall`). Fuera de la navegación; sirve para
 * revisar y capturar cada widget aislado.
 */
const { t } = useI18n()
const route = useRoute()
const { membership } = useTenant()
const config = useRuntimeConfig()
const scale = computed<WidgetScale>(() => (route.query.scale === 'wall' ? 'wall' : 'normal'))

const { data: list } = useDashboardList()
const templates = ref<Dashboard[]>([])
const { $api } = useNuxtApp()
watch(
  list,
  async (l) => {
    if (!l) return
    templates.value = await Promise.all(
      l.map((d) =>
        unwrap(
          $api.GET('/dashboards/{dashboard_id}', { params: { path: { dashboard_id: d.id } } }),
        ),
      ),
    )
  },
  { immediate: true },
)

/** Un mini-dashboard por tipo: el widget de la plantilla que lo usa, en su tamaño. */
const items = computed(() =>
  registeredTypes().flatMap((type) => {
    const template = templates.value.find((d) => d.widgets.some((w) => w.type === type))
    const widget = template?.widgets.find((w) => w.type === type)
    if (!template || !widget) return []
    return [
      {
        type,
        title: widgetFor(type)!.catalog.title,
        description: t(widgetFor(type)!.manifest.description),
        size: `${widget.position.w}×${widget.position.h}`,
        dashboard: {
          ...template,
          widgets: [{ ...widget, position: { ...widget.position, x: 0, y: 0 } }],
        } satisfies Dashboard,
      },
    ]
  }),
)

useHead({ title: () => t('devWidgets.title') })
</script>

<template>
  <AppPage :title="t('devWidgets.title')" panel-id="dev-widgets">
    <template #navbar-right>
      <UButton
        color="neutral"
        variant="outline"
        :icon="scale === 'wall' ? 'i-lucide-minimize-2' : 'i-lucide-maximize-2'"
        :label="scale === 'wall' ? t('devWidgets.normal') : t('devWidgets.wall')"
        :to="{ path: route.path, query: scale === 'wall' ? {} : { scale: 'wall' } }"
      />
    </template>
    <p class="text-muted max-w-prose">{{ t('devWidgets.intro') }}</p>
    <section
      v-for="item in items"
      :key="item.type"
      :aria-labelledby="`dev-${item.type}`"
      class="space-y-2"
      :data-testid="`dev-widget-${item.type}`"
    >
      <h2 :id="`dev-${item.type}`" class="text-highlighted text-sm font-semibold">
        {{ item.title }}
        <span class="text-muted font-mono font-normal">· {{ item.type }} · {{ item.size }}</span>
      </h2>
      <p class="text-muted text-sm">{{ item.description }}</p>
      <div :class="scale === 'wall' ? 'dark bg-muted rounded-lg' : ''">
        <DashboardGrid
          :dashboard="item.dashboard"
          :tenant-name="membership?.tenant_name ?? ''"
          :scale="scale"
          :live="config.public.apiMock"
          :fill="false"
        />
      </div>
    </section>
  </AppPage>
</template>
