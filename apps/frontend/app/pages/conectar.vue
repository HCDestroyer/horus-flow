<script setup lang="ts">
/**
 * Destino del enlace de conexión de Horus (`https://<horus>/conectar#<código>`, contrato
 * ArgosISP §1.4). El enlace no se abre aquí: se pega en ArgosISP ("Conectar con enlace"), que lo
 * canjea de servidor a servidor. Si alguien lo abre en el navegador, se explica qué hacer y se
 * quita el código de la barra de direcciones (nunca se envía a ningún servidor desde aquí).
 */
definePageMeta({ layout: 'auth', public: true })

const { t } = useI18n()
useHead({ title: () => t('connect.title') })

onMounted(() => {
  if (window.location.hash) {
    history.replaceState(history.state, '', window.location.pathname + window.location.search)
  }
})
</script>

<template>
  <UCard
    variant="outline"
    class="shadow-sm"
    :ui="{ body: 'p-6 sm:p-8 space-y-4' }"
    data-testid="connect-page"
  >
    <h1 class="text-highlighted text-xl font-semibold">{{ t('connect.title') }}</h1>
    <p class="text-muted text-sm">{{ t('connect.body') }}</p>
    <UAlert
      color="warning"
      variant="subtle"
      icon="i-lucide-shield-alert"
      :title="t('connect.secretTitle')"
      :description="t('connect.secretBody')"
    />
    <UButton
      to="/"
      variant="ghost"
      color="neutral"
      block
      icon="i-lucide-arrow-left"
      :label="t('connect.home')"
    />
  </UCard>
</template>
