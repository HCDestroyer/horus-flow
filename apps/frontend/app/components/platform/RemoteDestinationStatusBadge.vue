<script setup lang="ts">
import type { RemoteDestination } from '~~/types/api'

/** Estado de un destino remoto = icono + texto + color (nunca solo color, WCAG 1.4.1). */
const props = defineProps<{ status: RemoteDestination['status'] }>()
const { t } = useI18n()
const visual = computed(
  () => DESTINATION_STATUS_VISUAL[props.status] ?? DESTINATION_STATUS_VISUAL.failing,
)
</script>

<template>
  <UBadge
    :color="visual.color"
    variant="subtle"
    :icon="visual.icon"
    :label="t(`platform.storage.status.${status}`)"
    class="shrink-0"
    data-testid="dst-status"
    :data-status="status"
  />
</template>
