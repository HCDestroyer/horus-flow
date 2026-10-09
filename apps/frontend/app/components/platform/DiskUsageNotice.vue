<script setup lang="ts">
/**
 * Aviso de disco local (I1-23): a partir del 85 % de uso (`SystemStatus.disk_usage_ratio`,
 * que el servidor calcula de las métricas `horus_store_disk_*`) se avisa en la consola de
 * plataforma; desde el 95 %, como crítico (la ingesta empieza a descartar datos).
 */
const props = defineProps<{ ratio: number | null | undefined }>()
const { t } = useI18n()
const level = computed(() =>
  props.ratio === null || props.ratio === undefined
    ? null
    : props.ratio >= 0.95
      ? 'critical'
      : props.ratio >= 0.85
        ? 'warning'
        : null,
)
</script>

<template>
  <UAlert
    v-if="level"
    :color="level === 'critical' ? 'error' : 'warning'"
    variant="subtle"
    icon="i-lucide-hard-drive"
    :title="t(`platform.disk.${level}`, { pct: formatPercent(ratio) })"
    :description="t('platform.disk.body')"
    data-testid="disk-usage-notice"
  />
</template>
