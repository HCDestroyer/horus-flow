<script setup lang="ts">
// Banner de anuncio opcional (/admin › Ajustes). Superficie sólida de contenido, encima de la
// barra; no es vidrio ni flota (HIG liquid-glass › The two layers).
const { t, locale } = useI18n()
const site = useSiteState()
const banner = computed(() => {
  const b = site.value?.settings.banner
  const text = b ? (locale.value === 'en' ? b.text.en : b.text.es) || b.text.es : ''
  return b?.enabled && text ? { text, url: b.url } : null
})
</script>

<template>
  <aside
    v-if="banner"
    :aria-label="t('banner.label')"
    class="border-b border-default bg-(--ui-primary)/10 px-4 py-2 text-center text-[0.95rem] text-highlighted"
    data-testid="site-banner"
  >
    <span>{{ banner.text }}</span>
    <a
      v-if="banner.url"
      :href="banner.url"
      class="ml-2 inline-flex min-h-11 items-center font-medium text-primary underline underline-offset-4"
    >
      {{ t('banner.more') }}
    </a>
  </aside>
</template>
