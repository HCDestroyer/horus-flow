<script setup lang="ts">
// Cierre de las páginas interiores: una sola acción prominente (demo) y precios en contorno.
const { t } = useI18n()
const localePath = useLocalePath()
const home = computed(() => localePath('/'))
const hash = (id: string) => `${home.value === '/' ? '/' : home.value}#${id}`
</script>

<template>
  <section aria-labelledby="cta-title" class="mx-auto max-w-6xl px-4 py-16 sm:px-6">
    <div
      data-reveal
      class="surface flex flex-col gap-6 rounded-3xl p-8 sm:p-10 lg:flex-row lg:items-center lg:justify-between"
    >
      <div class="max-w-2xl">
        <h2 id="cta-title" class="text-3xl font-bold text-highlighted">
          {{ t('common.ctaTitle') }}
        </h2>
        <p class="mt-3 text-lg text-toned">{{ t('common.ctaBody') }}</p>
      </div>
      <div class="flex shrink-0 flex-col gap-3 sm:flex-row">
        <UButton
          :to="hash('demo')"
          size="xl"
          color="primary"
          class="min-h-12 rounded-xl px-6 text-base"
          :label="t('common.ctaDemo')"
        />
        <UButton
          :to="hash('pricing')"
          size="xl"
          color="neutral"
          variant="outline"
          class="min-h-12 rounded-xl px-6 text-base"
          :label="t('common.ctaPricing')"
        />
      </div>
    </div>
  </section>
</template>
