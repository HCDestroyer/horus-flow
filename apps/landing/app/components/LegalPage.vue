<script setup lang="ts">
import { legalDocs } from '~/content/legal'

const props = defineProps<{ doc: keyof typeof legalDocs }>()
const { t, locale } = useI18n()
const doc = computed(() => legalDocs[props.doc])
const updated = computed(() =>
  new Intl.DateTimeFormat(locale.value === 'en' ? 'en-US' : 'es-GT', { dateStyle: 'long' }).format(
    new Date(`${doc.value.updated}T12:00:00Z`),
  ),
)
</script>

<template>
  <article class="mx-auto max-w-3xl px-4 pt-10 sm:px-6 md:pt-16" lang="es">
    <div class="rounded-xl border border-(--ui-border-accented) bg-muted p-4" role="note">
      <p class="flex gap-2 font-medium text-highlighted" :lang="locale">
        <UIcon name="i-lucide-file-pen-line" class="mt-0.5 size-5 shrink-0" aria-hidden="true" />
        {{ t('legal.draft') }}
      </p>
      <p v-if="locale === 'en'" class="mt-1 pl-7 text-muted" :lang="locale">
        {{ t('legal.onlySpanish') }}
      </p>
    </div>
    <h1 class="mt-8 text-4xl font-bold text-highlighted">{{ doc.title }}</h1>
    <p class="mt-2 text-muted" :lang="locale">{{ t('legal.updated', { date: updated }) }}</p>
    <section v-for="s in doc.sections" :key="s.title" class="mt-8">
      <h2 class="text-xl font-semibold text-highlighted">{{ s.title }}</h2>
      <p v-for="(p, i) in s.paragraphs" :key="i" class="mt-3 text-toned">{{ p }}</p>
    </section>
  </article>
</template>
