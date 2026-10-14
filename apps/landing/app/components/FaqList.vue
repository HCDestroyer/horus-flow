<script setup lang="ts">
// Preguntas frecuentes con <details> nativo: accesibles con teclado y lector de pantalla, sin
// JavaScript. Los datos estructurados FAQPage los añade la página (usePageLd).
defineProps<{ id: string; title: string; items: { q: string; a: string }[] }>()
</script>

<template>
  <section :aria-labelledby="id" class="mx-auto max-w-3xl px-4 py-14 sm:px-6">
    <h2 :id="id" data-reveal class="text-3xl font-bold text-highlighted">{{ title }}</h2>
    <div class="mt-8 divide-y divide-(--ui-border) border-y border-default">
      <details v-for="(it, i) in items" :key="i" class="faq group">
        <summary
          class="flex min-h-14 cursor-pointer list-none items-center gap-4 py-3 text-lg font-medium text-highlighted"
        >
          <span class="flex-1">{{ it.q }}</span>
          <UIcon
            name="i-lucide-chevron-down"
            class="chev size-5 shrink-0 text-muted"
            aria-hidden="true"
          />
        </summary>
        <p class="pb-5 text-toned">{{ it.a }}</p>
      </details>
    </div>
  </section>
</template>

<style scoped>
.faq summary::-webkit-details-marker {
  display: none;
}
.chev {
  transition: rotate 0.25s var(--ease-out);
}
.faq[open] .chev {
  rotate: 180deg;
}
</style>
