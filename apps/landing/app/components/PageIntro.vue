<script setup lang="ts">
// Cabecera de las páginas interiores: ruta (Inicio › Página), titular, entradilla, acciones y
// una ilustración a la derecha (slot). El titular no se anima: es contenido principal (LCP).
defineProps<{ crumb: string; eyebrow: string; title: string; lead: string }>()
const { t } = useI18n()
const localePath = useLocalePath()
const home = computed(() => localePath('/'))
const hash = (id: string) => `${home.value === '/' ? '/' : home.value}#${id}`
</script>

<template>
  <div class="relative isolate overflow-hidden">
    <HeroAmbient />
    <section
      aria-labelledby="page-title"
      class="mx-auto grid max-w-6xl gap-10 px-4 pt-8 pb-14 sm:px-6 md:pt-12 lg:grid-cols-[1fr_1.1fr] lg:items-center lg:gap-14 lg:pb-20"
    >
      <div>
        <nav :aria-label="t('nav.breadcrumb')" class="mb-6 text-sm">
          <ol class="flex flex-wrap items-center gap-1 text-muted">
            <li>
              <NuxtLink
                :to="home"
                class="inline-flex min-h-11 min-w-11 items-center hover:text-highlighted"
              >
                {{ t('nav.homeShort') }}
              </NuxtLink>
            </li>
            <li aria-hidden="true">
              <UIcon name="i-lucide-chevron-right" class="mt-1 size-4" />
            </li>
            <li>
              <span aria-current="page" class="text-highlighted">{{ crumb }}</span>
            </li>
          </ol>
        </nav>
        <p class="eyebrow">{{ eyebrow }}</p>
        <h1
          id="page-title"
          class="mt-3 text-[2rem] font-bold text-balance text-highlighted sm:text-[2.6rem] lg:text-[3rem]"
        >
          {{ title }}
        </h1>
        <p class="mt-6 max-w-2xl text-lg text-toned sm:text-xl sm:leading-relaxed">{{ lead }}</p>
        <div class="mt-8 flex flex-col gap-3 sm:flex-row">
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
      <div data-reveal="scale" class="min-w-0">
        <slot />
      </div>
    </section>
  </div>
</template>
