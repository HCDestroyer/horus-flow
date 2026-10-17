<script setup lang="ts">
/**
 * Nivel del pronóstico de capacidad = icono + texto + color: Urgente (acción ya),
 * Planificar (conviene mirarlo), Sin acción, Datos insuficientes, Sin capacidad.
 */
const props = defineProps<{ level: string }>()
const { t, te } = useI18n()
const meta = computed(() => forecastMeta(props.level))
const label = computed(() =>
  te(`capacity.forecast.level.${props.level}`)
    ? t(`capacity.forecast.level.${props.level}`)
    : props.level,
)
</script>

<template>
  <UBadge
    :color="meta.color"
    :icon="meta.icon"
    variant="subtle"
    size="sm"
    :label="label"
    :data-forecast="level"
  />
</template>
