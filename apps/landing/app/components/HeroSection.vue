<script setup lang="ts">
const { t } = useI18n()
</script>

<template>
  <section
    aria-labelledby="hero-title"
    class="mx-auto max-w-6xl px-4 pt-12 pb-16 sm:px-6 md:pt-20 lg:pb-24"
  >
    <!--
      Móvil: titular → animación → texto y botones (el póster entra en la primera pantalla y es
      el LCP; el CTA flotante cubre la acción). Escritorio: texto a la izquierda y animación a la
      derecha, como siempre.
    -->
    <div
      class="grid gap-y-8 lg:grid-cols-[1.25fr_1fr] lg:grid-rows-[1fr_auto_auto_1fr] lg:gap-x-16 lg:gap-y-0"
    >
      <div class="lg:col-start-1 lg:row-start-2">
        <p class="eyebrow">{{ t('hero.eyebrow') }}</p>
        <h1
          id="hero-title"
          class="mt-3 text-[2rem] font-bold text-balance text-highlighted sm:text-[2.75rem] lg:text-[3.25rem]"
        >
          {{ t('hero.title') }}
        </h1>
      </div>
      <div class="lg:col-start-2 lg:row-span-4 lg:row-start-1 lg:self-center">
        <SubnetGrid />
      </div>
      <div class="lg:col-start-1 lg:row-start-3">
        <p class="max-w-2xl text-lg text-toned sm:text-xl sm:leading-relaxed lg:mt-6">
          {{ t('hero.lead') }}
        </p>
        <div class="mt-8 flex flex-col gap-3 sm:flex-row">
          <UButton
            to="#demo"
            size="xl"
            color="primary"
            class="min-h-12 rounded-xl px-6 text-base"
            :label="t('hero.ctaPrimary')"
          />
          <UButton
            to="#pricing"
            size="xl"
            color="neutral"
            variant="outline"
            class="min-h-12 rounded-xl px-6 text-base"
            :label="t('hero.ctaSecondary')"
          />
        </div>
        <p class="mt-5 flex items-center gap-2 text-[0.95rem] text-muted">
          <UIcon name="i-lucide-eye" class="size-5 shrink-0 text-primary" aria-hidden="true" />
          {{ t('hero.note') }}
        </p>
      </div>
    </div>
  </section>
</template>
