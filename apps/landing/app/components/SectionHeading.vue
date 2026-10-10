<script setup lang="ts">
defineProps<{ id: string; eyebrow: string; title: string; lead?: string }>()
</script>

<template>
  <div class="max-w-3xl">
    <p class="eyebrow">{{ eyebrow }}</p>
    <h2 :id="id" class="mt-2 text-3xl font-bold text-highlighted sm:text-4xl">{{ title }}</h2>
    <p v-if="lead" class="mt-4 text-lg text-toned">{{ lead }}</p>
  </div>
</template>
