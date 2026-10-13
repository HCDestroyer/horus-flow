<script setup lang="ts">
/**
 * Layout del kiosco (frontend.md §7.3): siempre el viewport entero, sin barra lateral, sin
 * navbar ni scroll, tema oscuro sin negro puro (§7.8).
 */
</script>

<template>
  <main id="main-content" class="kiosk-root relative h-dvh overflow-hidden" tabindex="-1">
    <div class="app-aura app-aura-wall" aria-hidden="true" />
    <div class="relative h-full">
      <slot />
    </div>
  </main>
</template>

<style>
.kiosk-root {
  background: #0b1014;
  color-scheme: dark;
}
</style>
