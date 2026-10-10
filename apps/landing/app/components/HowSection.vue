<script setup lang="ts">
const { t } = useI18n()
</script>

<template>
  <section id="how" aria-labelledby="how-title" class="mx-auto max-w-6xl px-4 py-16 sm:px-6">
    <SectionHeading
      id="how-title"
      :eyebrow="t('how.eyebrow')"
      :title="t('how.title')"
      :lead="t('how.lead')"
    />
    <div class="mt-10">
      <FlowDiagram />
    </div>
    <div class="surface mt-8 flex flex-col gap-4 rounded-2xl p-6 sm:flex-row sm:items-start">
      <UIcon
        name="i-lucide-terminal-square"
        class="size-7 shrink-0 text-primary"
        aria-hidden="true"
      />
      <div>
        <h3 class="text-lg font-semibold text-highlighted">{{ t('how.install.title') }}</h3>
        <p class="mt-1 text-toned">{{ t('how.install.body') }}</p>
      </div>
    </div>
  </section>
</template>
