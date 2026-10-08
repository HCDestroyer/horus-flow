<script setup lang="ts">
import type { TimeSeries } from '~/utils/chart-palette'

/**
 * Tabla alternativa de un gráfico (WCAG 1.1.1; HIG charts › accesibilidad; frontend.md §14):
 * los mismos datos en una tabla para lectores de pantalla. Visualmente oculta: en pantalla
 * los valores clave ya están en etiquetas directas o en el tooltip. Hasta 48 filas
 * (submuestreo uniforme) para no inundar al lector.
 */
const props = withDefaults(
  defineProps<{
    series: TimeSeries[]
    label: string
    format: (value: number) => string
    timeFormat?: 'time' | 'day'
  }>(),
  { timeFormat: 'time' },
)

const { t } = useI18n()
const fmt = computed(
  () =>
    new Intl.DateTimeFormat('es-ES', {
      ...(props.timeFormat === 'day'
        ? { day: '2-digit', month: '2-digit' }
        : { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }),
    }),
)

const rows = computed(() => {
  const first = props.series[0]
  if (!first) return []
  const step = Math.max(1, Math.ceil(first.points.length / 48))
  return first.points
    .map((p, i) => ({ i, t: p[0] }))
    .filter(({ i }) => i % step === 0 || i === first.points.length - 1)
    .map(({ i, t: time }) => ({
      time: fmt.value.format(new Date(time)),
      values: props.series.map((s) => {
        const v = s.points[i]?.[1]
        return v === null || v === undefined ? t('charts.gap') : props.format(v)
      }),
    }))
})
</script>

<template>
  <table class="sr-only">
    <caption>
      {{
        label
      }}
    </caption>
    <thead>
      <tr>
        <th scope="col">{{ t('charts.time') }}</th>
        <th v-for="s in series" :key="s.name" scope="col">{{ s.name }}</th>
      </tr>
    </thead>
    <tbody>
      <tr v-for="row in rows" :key="row.time">
        <th scope="row">{{ row.time }}</th>
        <td v-for="(v, i) in row.values" :key="i">{{ v }}</td>
      </tr>
    </tbody>
  </table>
</template>
