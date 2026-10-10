<script setup lang="ts">
defineProps<{ title: string; lead?: string }>()
</script>

<template>
  <div class="mb-6 flex flex-wrap items-end justify-between gap-4">
    <div class="min-w-0">
      <h1 class="text-2xl font-bold text-highlighted sm:text-3xl">{{ title }}</h1>
      <p v-if="lead" class="mt-1 max-w-3xl text-toned">{{ lead }}</p>
    </div>
    <div v-if="$slots.actions" class="flex flex-wrap gap-2">
      <slot name="actions" />
    </div>
  </div>
</template>
