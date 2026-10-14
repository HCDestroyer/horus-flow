<script setup lang="ts">
// Páginas legales (/legal/<slug>, /en/legal/<slug>): aviso legal, privacidad, términos,
// cookies… Se editan en /admin › Páginas.
const page = await useContentPage()
</script>

<template>
  <LegalPage :page="page" />
</template>
