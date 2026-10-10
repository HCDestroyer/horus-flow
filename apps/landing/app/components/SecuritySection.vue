<script setup lang="ts">
const { t } = useI18n()
const items = [
  { key: 'readonly', icon: 'i-lucide-eye' },
  { key: 'isolation', icon: 'i-lucide-layers' },
  { key: 'access', icon: 'i-lucide-key-round' },
  { key: 'secrets', icon: 'i-lucide-lock' },
  { key: 'pii', icon: 'i-lucide-eye-off' },
  { key: 'local', icon: 'i-lucide-server' },
] as const
</script>

<template>
  <section aria-labelledby="security-title" class="mx-auto max-w-6xl px-4 py-16 sm:px-6">
    <SectionHeading id="security-title" :eyebrow="t('security.eyebrow')" :title="t('security.title')" />
    <ul class="mt-10 grid gap-x-10 gap-y-8 sm:grid-cols-2 lg:grid-cols-3">
      <li v-for="i in items" :key="i.key" class="flex gap-4">
        <UIcon :name="i.icon" class="mt-0.5 size-6 shrink-0 text-primary" aria-hidden="true" />
        <div>
          <h3 class="font-semibold text-highlighted">{{ t(`security.items.${i.key}.title`) }}</h3>
          <p class="mt-1 text-toned">{{ t(`security.items.${i.key}.body`) }}</p>
        </div>
      </li>
    </ul>
  </section>
</template>
