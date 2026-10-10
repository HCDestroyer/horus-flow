<script setup lang="ts">
import { site } from '~/config/site'

const { t } = useI18n()
const keys = [
  'routers',
  'changes',
  'server',
  'domain',
  'nat',
  'ipv6',
  'offline',
  'screenshots',
] as const
const items = computed(() =>
  keys.map((k) => ({ label: t(`faq.items.${k}.q`), content: t(`faq.items.${k}.a`), value: k })),
)
</script>

<template>
  <section id="faq" aria-labelledby="faq-title" class="mx-auto max-w-6xl px-4 py-16 sm:px-6">
    <div class="grid gap-10 lg:grid-cols-[1fr_1.5fr]">
      <div>
        <SectionHeading id="faq-title" :eyebrow="t('faq.eyebrow')" :title="t('faq.title')" />
        <div class="surface mt-8 rounded-2xl p-6">
          <h3 class="text-lg font-semibold text-highlighted">{{ t('contact.title') }}</h3>
          <p class="mt-1 text-toned">{{ t('contact.body') }}</p>
          <a
            :href="`mailto:${site.seller.email}`"
            class="mt-1 inline-flex min-h-11 items-center text-lg font-semibold text-primary underline underline-offset-4"
          >
            {{ site.seller.email }}
          </a>
          <p class="mt-2 text-[0.95rem] text-muted">{{ t('contact.seller') }}</p>
        </div>
      </div>
      <UAccordion
        :items="items"
        type="multiple"
        :ui="{
          trigger: 'min-h-12 text-base sm:text-lg text-highlighted',
          body: 'text-toned text-base',
        }"
      />
    </div>
  </section>
</template>
