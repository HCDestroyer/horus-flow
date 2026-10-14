<script setup lang="ts">
// Sin <UApp>: la landing no usa toasts, tooltips ni overlays y así no carga su código.
const { t, locale } = useI18n()
</script>

<template>
  <div>
    <a
      href="#contenido"
      class="sr-only focus:not-sr-only focus:fixed focus:top-3 focus:left-3 focus:z-[60] focus:rounded-md focus:bg-(--surface) focus:px-4 focus:py-3 focus:text-default focus:shadow-lg"
    >
      {{ t('nav.skip') }}
    </a>
    <SiteBanner />
    <SiteHeader />
    <main id="contenido" tabindex="-1" class="outline-none">
      <slot />
    </main>
    <!-- Sin hidratar (solo enlaces); la clave fuerza un render nuevo al cambiar de idioma. -->
    <LazySiteFooter :key="locale" hydrate-never />
  </div>
</template>
