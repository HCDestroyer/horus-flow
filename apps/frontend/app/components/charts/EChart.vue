<script setup lang="ts">
import { BarChart, LineChart } from 'echarts/charts'
import {
  AriaComponent,
  GridComponent,
  LegendComponent,
  MarkLineComponent,
  TooltipComponent,
} from 'echarts/components'
import { use } from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'
import type { ChartOption } from '~/utils/chart-palette'
import VChart from 'vue-echarts'

/**
 * ECharts con solo los módulos que usan los widgets (líneas, barras, ejes, leyenda,
 * tooltip, aria) y render en canvas (CSP sin `unsafe-eval`, security.md §3.1). Se carga
 * dentro de los chunks de los widgets, no en el bundle inicial (frontend.md §16).
 */
use([
  LineChart,
  BarChart,
  GridComponent,
  LegendComponent,
  TooltipComponent,
  MarkLineComponent,
  AriaComponent,
  CanvasRenderer,
])

defineProps<{ option: ChartOption; label: string }>()
</script>

<template>
  <!-- El canvas es decorativo para el lector de pantalla: la tabla alternativa da los datos. -->
  <div class="relative size-full min-h-0" role="img" :aria-label="label">
    <VChart :option="option" autoresize class="size-full" />
  </div>
</template>
