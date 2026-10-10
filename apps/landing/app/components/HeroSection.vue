<script setup lang="ts">
const { t } = useI18n()
</script>

<template>
  <section
    aria-labelledby="hero-title"
    class="mx-auto max-w-6xl px-4 pt-12 pb-16 sm:px-6 md:pt-20 lg:pb-24"
  >
    <div class="grid items-center gap-12 lg:grid-cols-[1.25fr_1fr] lg:gap-16">
      <div>
        <p class="eyebrow">{{ t('hero.eyebrow') }}</p>
        <h1
          id="hero-title"
          class="mt-3 text-[2rem] font-bold text-balance text-highlighted sm:text-[2.75rem] lg:text-[3.25rem]"
        >
          {{ t('hero.title') }}
        </h1>
        <p class="mt-6 max-w-2xl text-lg text-toned sm:text-xl sm:leading-relaxed">
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
      <SubnetGrid />
    </div>
  </section>
</template>
