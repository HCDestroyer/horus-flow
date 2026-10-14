<script setup lang="ts">
// Páginas informativas (/info/<slug>, /en/info/<slug>), creadas en /admin › Páginas.
const page = await useContentPage()
</script>

<template>
  <LegalPage :page="page" />
</template>
