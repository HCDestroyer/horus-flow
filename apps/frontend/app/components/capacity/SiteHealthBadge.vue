<script setup lang="ts">
/**
 * Salud de un nodo (capacidad de enlaces) = icono + texto + color, con el vocabulario de
 * estado de equipos (frontend.md §13.2, estados.md): Sano, Aviso, Degradado, Crítico, Fuera
 * de servicio, Sin datos. Nunca solo color.
 */
const props = withDefaults(defineProps<{ status: string; compact?: boolean }>(), {
  compact: false,
})
const { t, te } = useI18n()
const meta = computed(() => healthMeta(props.status))
const label = computed(() =>
  te(`capacity.health.${props.status}`) ? t(`capacity.health.${props.status}`) : props.status,
)
</script>

<template>
  <UBadge
    :color="meta.color"
    :icon="meta.icon"
    variant="subtle"
    :size="compact ? 'sm' : 'md'"
    :label="label"
    :data-health="status"
  />
</template>
