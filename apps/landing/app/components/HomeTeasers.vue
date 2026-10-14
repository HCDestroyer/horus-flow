<script setup lang="ts">
// Novedades en la portada: tarjetas que llevan a las páginas de IA, monitoreo y pantallas NOC.
// Toda la tarjeta es pulsable (enlace del título extendido) y responde al pulsar (.lift).
// Las ilustraciones son finitas (sin bucles ni botones dentro de un enlace).
const { t } = useI18n()
const localePath = useLocalePath()
</script>

<template>
  <section
    id="novedades"
    aria-labelledby="teasers-title"
    class="mx-auto max-w-6xl px-4 py-16 sm:px-6"
  >
    <SectionHeading
      id="teasers-title"
      :eyebrow="t('teasers.eyebrow')"
      :title="t('teasers.title')"
      :lead="t('teasers.lead')"
    />
    <div class="mt-10 grid grid-cols-1 gap-6 lg:grid-cols-12">
      <article
        data-reveal
        class="lift surface min-w-0 flex flex-col gap-6 rounded-2xl p-6 lg:col-span-7"
      >
        <div>
          <h3 class="text-xl font-semibold text-highlighted">
            <NuxtLink
              :to="localePath('/inteligencia-artificial')"
              class="card-link inline-flex min-h-11 items-center"
            >
              {{ t('teasers.ai.title') }}
            </NuxtLink>
          </h3>
          <p class="mt-2 text-toned">{{ t('teasers.ai.body') }}</p>
        </div>
        <AiAnalysisMock compact />
        <span
          class="mt-auto inline-flex items-center gap-1.5 font-semibold text-primary"
          aria-hidden="true"
        >
          {{ t('teasers.ai.cta') }}
          <UIcon name="i-lucide-arrow-right" class="arrow size-4" />
        </span>
      </article>

      <article
        data-reveal
        :style="{ '--rv-i': 1 }"
        class="lift surface min-w-0 flex flex-col gap-6 rounded-2xl p-6 lg:col-span-5"
      >
        <div>
          <h3 class="text-xl font-semibold text-highlighted">
            <NuxtLink
              :to="localePath('/monitoreo')"
              class="card-link inline-flex min-h-11 items-center"
            >
              {{ t('teasers.monitoring.title') }}
            </NuxtLink>
          </h3>
          <p class="mt-2 text-toned">{{ t('teasers.monitoring.body') }}</p>
        </div>
        <RouterHealthGrid compact />
        <span
          class="mt-auto inline-flex items-center gap-1.5 font-semibold text-primary"
          aria-hidden="true"
        >
          {{ t('teasers.monitoring.cta') }}
          <UIcon name="i-lucide-arrow-right" class="arrow size-4" />
        </span>
      </article>

      <article
        data-reveal
        class="lift surface min-w-0 grid gap-6 overflow-hidden rounded-2xl p-6 md:grid-cols-[1fr_1.2fr] md:items-center lg:col-span-12"
      >
        <div>
          <h3 class="text-xl font-semibold text-highlighted">
            <NuxtLink
              :to="localePath('/pantallas-noc')"
              class="card-link inline-flex min-h-11 items-center"
            >
              {{ t('teasers.noc.title') }}
            </NuxtLink>
          </h3>
          <p class="mt-2 text-toned">{{ t('teasers.noc.body') }}</p>
          <span
            class="mt-4 inline-flex items-center gap-1.5 font-semibold text-primary"
            aria-hidden="true"
          >
            {{ t('teasers.noc.cta') }}
            <UIcon name="i-lucide-arrow-right" class="arrow size-4" />
          </span>
        </div>
        <div class="overflow-hidden rounded-xl border border-default bg-[#121214]">
          <ShotPicture
            name="noc720"
            :alt="t('noc.dashboards.items.noc.alt')"
            sizes="(min-width: 1152px) 560px, (min-width: 768px) 52vw, calc(100vw - 80px)"
          />
        </div>
      </article>
    </div>
  </section>
</template>
