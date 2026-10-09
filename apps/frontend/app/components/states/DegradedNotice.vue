<script setup lang="ts">
/**
 * Degradado (frontend.md §9.4): si el error es una dependencia caída (503, analítica no
 * disponible) se dice qué está afectado y qué sigue funcionando; cualquier otro error va a
 * `ErrorState` con "Reintentar".
 */
const props = defineProps<{ error: unknown }>()
const emit = defineEmits<{ retry: [] }>()
const { t } = useI18n()

const degraded = computed(
  () =>
    props.error instanceof ApiError &&
    (props.error.status === 503 || props.error.code === 'ANALYTICS_UNAVAILABLE'),
)
</script>

<template>
  <UAlert
    v-if="degraded"
    color="warning"
    variant="subtle"
    icon="i-lucide-database-zap"
    :title="t('degraded.analyticsTitle')"
    :description="t('degraded.analyticsBody')"
    data-testid="degraded-notice"
  />
  <ErrorState v-else :error="error" @retry="emit('retry')" />
</template>
