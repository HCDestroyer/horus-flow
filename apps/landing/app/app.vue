<script setup lang="ts">
import { site } from '~/config/site'

useHead({
  titleTemplate: (title) => title ?? site.product,
})
</script>

<template>
  <!-- Landing: layouts/default.vue (sin <UApp>, para no cargar su código). Panel: layouts/admin.vue. -->
  <NuxtLayout>
    <NuxtPage />
  </NuxtLayout>
</template>
