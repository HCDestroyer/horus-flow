<script setup lang="ts">
usePageSeo('home', 'meta.homeTitle', 'meta.homeDescription')
useStructuredData()
</script>

<template>
  <div>
    <LazyHeroSection hydrate-never />
    <!--
      Hidratación diferida (rendimiento en móvil): las secciones sin interacción se sirven ya
      renderizadas y no se hidratan; precios, formulario y preguntas se hidratan al acercarse a la
      pantalla (los campos del formulario llevan id fijo para que etiqueta e id casen).
    -->
    <LazyProblemSection hydrate-never />
    <LazyFeaturesSection hydrate-never />
    <LazyHowSection hydrate-never />
    <LazyReliabilitySection hydrate-never />
    <LazySecuritySection hydrate-never />
    <LazyPricingSection :hydrate-on-visible="{ rootMargin: '600px' }" />
    <LazyDemoSection :hydrate-on-visible="{ rootMargin: '600px' }" />
    <LazyFaqSection :hydrate-on-visible="{ rootMargin: '600px' }" />
    <LazyFloatingCta hydrate-on-idle />
  </div>
</template>
