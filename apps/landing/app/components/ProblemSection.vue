<script setup lang="ts">
const { t } = useI18n()
const problems = [
  { key: 'blacklist', icon: 'i-lucide-ban' },
  { key: 'abuse', icon: 'i-lucide-mail-warning' },
  { key: 'link', icon: 'i-lucide-gauge' },
]
const solution = [
  { key: 'who', icon: 'i-lucide-scan-search' },
  { key: 'why', icon: 'i-lucide-list-checks' },
  { key: 'what', icon: 'i-lucide-terminal' },
]
</script>

<template>
  <section aria-labelledby="problem-title" class="mx-auto max-w-6xl px-4 py-16 sm:px-6">
    <SectionHeading
      id="problem-title"
      :eyebrow="t('problem.eyebrow')"
      :title="t('problem.title')"
    />
    <ul class="mt-10 grid gap-4 md:grid-cols-3">
      <li v-for="p in problems" :key="p.key" class="surface rounded-2xl p-6">
        <UIcon :name="p.icon" class="size-6 text-muted" aria-hidden="true" />
        <h3 class="mt-4 text-lg font-semibold text-highlighted">
          {{ t(`problem.items.${p.key}.title`) }}
        </h3>
        <p class="mt-2 text-toned">{{ t(`problem.items.${p.key}.body`) }}</p>
      </li>
    </ul>

    <div class="mt-16">
      <SectionHeading
        id="solution-title"
        :eyebrow="t('problem.solutionEyebrow')"
        :title="t('problem.solutionTitle')"
      />
      <ol class="mt-10 grid gap-8 md:grid-cols-3">
        <li v-for="s in solution" :key="s.key" class="border-l-2 border-primary pl-5">
          <h3 class="flex items-center gap-2 text-lg font-semibold text-highlighted">
            <UIcon :name="s.icon" class="size-5 text-primary" aria-hidden="true" />
            {{ t(`problem.solution.${s.key}.title`) }}
          </h3>
          <p class="mt-2 text-toned">{{ t(`problem.solution.${s.key}.body`) }}</p>
        </li>
      </ol>
    </div>
  </section>
</template>
