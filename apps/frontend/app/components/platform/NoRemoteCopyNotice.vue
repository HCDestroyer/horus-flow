<script setup lang="ts">
/**
 * Aviso permanente "Sin copia remota configurada" (frontend.md §3.4, ADR-0019): mientras no
 * haya destino remoto, en Almacenamiento y en Estado del sistema, con su consecuencia.
 */
const { t } = useI18n()
</script>

<template>
  <UAlert
    color="warning"
    variant="subtle"
    icon="i-lucide-hard-drive-download"
    :title="t('platform.noRemote.title')"
    :description="t('platform.noRemote.body')"
    data-testid="no-remote-copy"
  />
</template>
