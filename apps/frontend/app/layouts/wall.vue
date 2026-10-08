<script setup lang="ts">
/**
 * Layout mural (frontend.md §7.3): ocupa el viewport sin barra lateral, navbar ni scroll.
 * Lo usa la vista mural de un dashboard dentro de la app; el kiosco (`/kiosk`, I1) tendrá el
 * suyo con credencial de dispositivo.
 */
</script>

<template>
  <main id="main-content" class="bg-muted h-dvh overflow-hidden" tabindex="-1">
    <slot />
  </main>
</template>
