<script setup lang="ts">
// Marca: el ojo de Horus reducido a tres trazos (contorno, pupila y la marca bajo el ojo).
// Decorativa: el nombre "Horus Flow" siempre la acompaña en texto.
withDefaults(defineProps<{ size?: number }>(), { size: 28 })
</script>

<template>
  <svg
    :width="size"
    :height="size"
    viewBox="0 0 32 32"
    fill="none"
    aria-hidden="true"
    focusable="false"
    class="shrink-0"
  >
    <path
      d="M3 15.5C6.5 10 11 7.5 16 7.5s9.5 2.5 13 8c-3.5 5.5-8 8-13 8s-9.5-2.5-13-8Z"
      stroke="currentColor"
      stroke-width="2.2"
      stroke-linejoin="round"
    />
    <circle cx="16" cy="15.5" r="3.6" fill="currentColor" />
    <path
      d="M13 23.2c-.6 2.6-2.2 4.4-4.6 5.3"
      stroke="currentColor"
      stroke-width="2.2"
      stroke-linecap="round"
    />
  </svg>
</template>
