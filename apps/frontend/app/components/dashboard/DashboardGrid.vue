<script setup lang="ts">
import type { Dashboard } from '~~/types/api'

/**
 * Grilla de solo lectura del dashboard (I0-16; el editor es de I2): 12 columnas con altura
 * de fila fija (`layout.row_height_px`), adaptada al ancho disponible según §5.2. En
 * escala mural ocupa el viewport entero sin scroll: las filas se reparten la altura y la
 * tipografía escala con ella (frontend.md §7.3–§7.4).
 */
const props = withDefaults(
  defineProps<{
    dashboard: Dashboard
    tenantName: string
    scale?: WidgetScale
    live?: boolean
    /** Mural: llenar el viewport (vista mural) o filas de altura mural fija (galería). */
    fill?: boolean
    /** Datos por vista previa (`POST /widget-data/preview`) en vez del dashboard guardado. */
    preview?: boolean
  }>(),
  { scale: 'normal', live: false, fill: true, preview: false },
)

const root = useTemplateRef<HTMLElement>('root')
const width = ref(1440)
let observer: ResizeObserver | undefined
onMounted(() => {
  if (!root.value) return
  width.value = root.value.clientWidth
  observer = new ResizeObserver(([entry]) => {
    if (entry) width.value = entry.contentRect.width
  })
  observer.observe(root.value)
})
onBeforeUnmount(() => observer?.disconnect())

const mode = computed<GridMode>(() =>
  props.scale === 'wall' ? 'designed' : gridModeFor(width.value),
)
const placed = computed(() => placeWidgets(props.dashboard.widgets, mode.value))
const rows = computed(() => totalRows(props.dashboard.widgets))

const gridStyle = computed(() => {
  if (props.scale === 'wall') {
    return props.fill
      ? { gridTemplateRows: `repeat(${rows.value}, minmax(0, 1fr))` }
      : { gridAutoRows: 'calc(100dvh / 15)' }
  }
  // Una o dos columnas (móvil, tableta): la fila crece con el contenido en vez de dejar que
  // se solape o se corte (los widgets con gráfico se quedan en su altura mínima).
  const row = props.dashboard.layout.row_height_px
  if (mode.value === 'single' || mode.value === 'narrow') {
    return { gridAutoRows: `minmax(${row}px, auto)` }
  }
  return { gridAutoRows: `${row}px` }
})

const lastUpdate = ref<number | null>(null)
provide(DASHBOARD_CONTEXT, {
  dashboard: props.dashboard,
  tenantName: toRef(props, 'tenantName'),
  scale: toRef(props, 'scale'),
  lastUpdate,
  live: toRef(props, 'live'),
  preview: props.preview,
  report: (at: number) => {
    if (!lastUpdate.value || at > lastUpdate.value) lastUpdate.value = at
  },
})
</script>

<template>
  <div
    ref="root"
    class="dash grid"
    :class="[
      mode === 'single' ? 'grid-cols-1' : 'grid-cols-12',
      scale === 'wall' ? 'dash-wall' : '',
      scale === 'wall' && fill ? 'h-dvh' : '',
    ]"
    :style="gridStyle"
    :data-scale="scale"
    :data-grid-mode="mode"
    data-testid="dashboard-grid"
  >
    <WidgetHost
      v-for="item in placed"
      :key="item.widget.id"
      :widget="item.widget"
      :dashboard-id="dashboard.id"
      :refresh-seconds="dashboard.refresh_seconds"
      :style="item.style"
    />
  </div>
</template>
