<script setup lang="ts">
// Página legal o informativa (editable en /admin › Páginas). El cuerpo llega como HTML ya
// convertido en el servidor por server/lib/markdown.ts (Markdown sin HTML del autor y con los
// enlaces filtrados); el panel usa este mismo componente para la vista previa.
import type { PublicPage } from '#shared/pages'

const props = defineProps<{
  page: Pick<
    PublicPage,
    'title' | 'titleLocale' | 'html' | 'contentLocale' | 'locale' | 'draftNotice' | 'updatedAt'
  >
  /** Vista previa del panel: el título va como <h2> (la página del panel ya tiene su <h1>). */
  preview?: boolean
}>()
const { t } = useI18n()
const onlySpanish = computed(() => props.page.locale === 'en' && props.page.contentLocale === 'es')
const updated = computed(() =>
  new Intl.DateTimeFormat(props.page.locale === 'en' ? 'en-US' : 'es-GT', {
    dateStyle: 'long',
    timeZone: 'America/Guatemala',
  }).format(new Date(props.page.updatedAt)),
)
</script>

<template>
  <article class="mx-auto max-w-3xl px-4 pt-10 sm:px-6 md:pt-16" :lang="page.contentLocale">
    <div
      v-if="page.draftNotice || onlySpanish"
      class="rounded-xl border border-(--ui-border-accented) bg-muted p-4"
      role="note"
    >
      <p
        v-if="page.draftNotice"
        class="flex gap-2 font-medium text-highlighted"
        :lang="page.locale"
      >
        <UIcon name="i-lucide-file-pen-line" class="mt-0.5 size-5 shrink-0" aria-hidden="true" />
        {{ t('legal.draft') }}
      </p>
      <p
        v-if="onlySpanish"
        class="text-muted"
        :class="page.draftNotice ? 'mt-1 pl-7' : 'flex gap-2'"
        :lang="page.locale"
      >
        <UIcon
          v-if="!page.draftNotice"
          name="i-lucide-languages"
          class="mt-0.5 size-5 shrink-0"
          aria-hidden="true"
        />
        {{ t('legal.onlySpanish') }}
      </p>
    </div>
    <component
      :is="preview ? 'h2' : 'h1'"
      class="mt-8 text-4xl font-bold text-pretty text-highlighted"
      :lang="page.titleLocale"
    >
      {{ page.title }}
    </component>
    <p class="mt-2 text-muted" :lang="page.locale">{{ t('legal.updated', { date: updated }) }}</p>
    <!-- HTML seguro generado en el servidor (server/lib/markdown.ts: html: false, enlaces
         http/https/mailto/relativos). Nunca llega HTML escrito por el autor. -->
    <!-- eslint-disable-next-line vue/no-v-html -->
    <div class="legal-prose" v-html="page.html" />
  </article>
</template>

<style scoped>
/* Mismo aspecto que las secciones legales originales: h2 text-xl semibold, párrafos toned. */
.legal-prose :deep(h2) {
  margin-top: 2rem;
  font-size: 1.25rem;
  line-height: 1.75rem;
  font-weight: 600;
  color: var(--ui-text-highlighted);
  text-wrap: pretty;
}
.legal-prose :deep(h3),
.legal-prose :deep(h4),
.legal-prose :deep(h5),
.legal-prose :deep(h6) {
  margin-top: 1.5rem;
  font-size: 1.0625rem;
  font-weight: 600;
  color: var(--ui-text-highlighted);
}
.legal-prose :deep(p),
.legal-prose :deep(ul),
.legal-prose :deep(ol),
.legal-prose :deep(blockquote),
.legal-prose :deep(table),
.legal-prose :deep(pre) {
  margin-top: 0.75rem;
  color: var(--ui-text-toned);
}
.legal-prose :deep(ul) {
  list-style: disc;
  padding-left: 1.5rem;
}
.legal-prose :deep(ol) {
  list-style: decimal;
  padding-left: 1.5rem;
}
.legal-prose :deep(li + li) {
  margin-top: 0.375rem;
}
.legal-prose :deep(li > p) {
  margin-top: 0.375rem;
}
.legal-prose :deep(strong) {
  font-weight: 600;
  color: var(--ui-text-highlighted);
}
.legal-prose :deep(a) {
  font-weight: 500;
  color: var(--ui-primary);
  text-decoration: underline;
  text-underline-offset: 4px;
  overflow-wrap: anywhere;
}
.legal-prose :deep(a:hover) {
  color: var(--ui-text-highlighted);
}
.legal-prose :deep(blockquote) {
  border-left: 3px solid var(--ui-border-accented);
  padding-left: 1rem;
}
.legal-prose :deep(code) {
  border-radius: 0.375rem;
  background: var(--ui-bg-muted);
  padding: 0.1rem 0.35rem;
  font-size: 0.9em;
}
.legal-prose :deep(pre) {
  overflow-x: auto;
  border-radius: 0.75rem;
  background: var(--ui-bg-muted);
  padding: 0.75rem 1rem;
}
.legal-prose :deep(pre code) {
  background: none;
  padding: 0;
}
.legal-prose :deep(hr) {
  margin-top: 2rem;
  border-color: var(--ui-border);
}
.legal-prose :deep(table) {
  display: block;
  max-width: 100%;
  overflow-x: auto;
  border-collapse: collapse;
}
.legal-prose :deep(th),
.legal-prose :deep(td) {
  border: 1px solid var(--ui-border);
  padding: 0.375rem 0.75rem;
  text-align: left;
}
.legal-prose :deep(th) {
  font-weight: 600;
  color: var(--ui-text-highlighted);
}
</style>
