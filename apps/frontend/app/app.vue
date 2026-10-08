<script setup lang="ts">
import { es } from '@nuxt/ui/locale'
</script>

<template>
  <UApp :locale="es" :toaster="{ position: 'bottom-right' }">
    <NuxtLayout>
      <NuxtPage />
    </NuxtLayout>
  </UApp>
</template>
