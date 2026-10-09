/** Paleta y tinta de gráficos del tema activo; reactiva al cambio claro/oscuro. */
export function useChartTheme() {
  const colorMode = useColorMode()
  const mode = computed<ChartMode>(() => (colorMode.value === 'dark' ? 'dark' : 'light'))
  return computed(() => ({
    mode: mode.value,
    series: CATEGORICAL[mode.value],
    ink: CHART_INK[mode.value],
  }))
}
