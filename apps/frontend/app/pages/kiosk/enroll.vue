<script setup lang="ts">
/**
 * Enlace del código de kiosco del contrato (`<HORUS_PUBLIC_BASE_URL>/kiosk/enroll#code=…`,
 * auth.yaml): lleva a la pantalla del kiosco conservando el fragmento, que nunca viaja al
 * servidor. Pública: la TV aún no tiene sesión.
 */
definePageMeta({ public: true, layout: 'kiosk', colorMode: 'dark' })
await navigateTo({ path: '/kiosk', hash: useRoute().hash }, { replace: true })
</script>

<template>
  <div />
</template>
