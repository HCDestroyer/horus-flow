<script setup lang="ts">
/**
 * Layout del kiosco (frontend.md §7.3): siempre el viewport entero, sin barra lateral, sin
 * navbar ni scroll, tema oscuro sin negro puro (§7.8).
 */
</script>

<template>
  <main id="main-content" class="kiosk-root h-dvh overflow-hidden" tabindex="-1">
    <slot />
  </main>
</template>

<style>
.kiosk-root {
  background: #0d0d10;
  color-scheme: dark;
}
</style>
