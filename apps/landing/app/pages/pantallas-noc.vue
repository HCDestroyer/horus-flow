<script setup lang="ts">
// Pantallas NOC (/pantallas-noc, /en/noc-screens): kiosco para TV, emparejamiento con código,
// listas de reproducción, resistencia sin conexión, dashboards incluidos y versión ligera.
definePageMeta({ viewTransition: true })
usePageSeo('noc', 'meta.nocTitle', 'meta.nocDescription')
usePageLd('noc', 'nav.noc', 'meta.nocDescription')

const { t } = useI18n()
const localePath = useLocalePath()
const code = 'K7QM2XRD'.split('')
const steps = [
  { key: 'enroll', icon: 'i-lucide-qr-code' },
  { key: 'playlist', icon: 'i-lucide-list-checks' },
  { key: 'forget', icon: 'i-lucide-refresh-ccw-dot' },
]
const shotSizes = '(min-width: 1152px) 360px, (min-width: 768px) 30vw, calc(100vw - 32px)'
</script>

<template>
  <div>
    <PageIntro
      :crumb="t('nav.noc')"
      :eyebrow="t('noc.eyebrow')"
      :title="t('noc.title')"
      :lead="t('noc.lead')"
    >
      <KioskRotation />
    </PageIntro>

    <section aria-labelledby="noc-steps" class="mx-auto max-w-6xl px-4 py-14 sm:px-6">
      <SectionHeading id="noc-steps" :eyebrow="t('noc.eyebrow')" :title="t('noc.steps.title')" />
      <ol class="mt-10 grid gap-4 md:grid-cols-3">
        <li
          v-for="(s, i) in steps"
          :key="s.key"
          data-reveal
          :style="{ '--rv-i': i }"
          class="surface flex flex-col rounded-2xl p-6"
        >
          <div class="flex items-center gap-3">
            <span
              class="flex size-9 items-center justify-center rounded-full bg-primary/10 text-sm font-bold text-primary"
              aria-hidden="true"
              >{{ i + 1 }}</span
            >
            <UIcon :name="s.icon" class="size-5 text-muted" aria-hidden="true" />
          </div>
          <h3 class="mt-4 text-lg font-semibold text-highlighted">
            {{ t(`noc.steps.items.${s.key}.title`) }}
          </h3>
          <p class="mt-2 text-toned">{{ t(`noc.steps.items.${s.key}.body`) }}</p>
          <!-- Ilustraciones pequeñas: código de emparejamiento y aviso sin conexión. -->
          <div v-if="s.key === 'enroll'" class="mt-auto pt-5">
            <p class="text-xs text-muted">{{ t('noc.steps.code') }}</p>
            <p
              class="code mt-2 flex gap-0.5"
              role="img"
              :aria-label="`${t('noc.steps.codeLabel')}: ${code.join(' ')}`"
            >
              <span
                v-for="(c, n) in code"
                :key="n"
                class="flex h-9 w-[1.6rem] items-center justify-center rounded-md border border-default bg-(--ui-bg-muted) font-mono text-base font-semibold text-highlighted"
                :class="n === 4 ? 'ml-1.5' : ''"
                :style="{ '--c': n }"
                aria-hidden="true"
                >{{ c }}</span
              >
            </p>
          </div>
          <div v-else-if="s.key === 'forget'" class="mt-auto pt-5">
            <p
              class="inline-flex items-center gap-2 rounded-full border border-(--st-warning)/50 px-3 py-1.5 text-sm font-medium text-highlighted"
            >
              <UIcon
                name="i-lucide-wifi-off"
                class="size-4 text-(--st-warning)"
                aria-hidden="true"
              />
              {{ t('noc.steps.offline') }}
            </p>
          </div>
        </li>
      </ol>
    </section>

    <section aria-labelledby="noc-dash" class="mx-auto max-w-6xl px-4 py-14 sm:px-6">
      <SectionHeading
        id="noc-dash"
        :eyebrow="t('noc.eyebrow')"
        :title="t('noc.dashboards.title')"
        :lead="t('noc.dashboards.lead')"
      />
      <div class="mt-10 grid gap-6 md:grid-cols-3">
        <figure data-reveal class="surface flex flex-col overflow-hidden rounded-2xl">
          <div class="border-b border-default bg-[#121214]">
            <ShotPicture
              name="noc720"
              :alt="t('noc.dashboards.items.noc.alt')"
              :sizes="shotSizes"
            />
          </div>
          <figcaption class="p-5">
            <h3 class="font-semibold text-highlighted">
              {{ t('noc.dashboards.items.noc.title') }}
            </h3>
            <p class="mt-1 text-toned">{{ t('noc.dashboards.items.noc.body') }}</p>
          </figcaption>
        </figure>
        <figure
          data-reveal
          :style="{ '--rv-i': 1 }"
          class="surface flex flex-col overflow-hidden rounded-2xl"
        >
          <div class="border-b border-default bg-[#121214]">
            <ShotPicture
              name="security"
              :alt="t('features.items.security.alt')"
              :sizes="shotSizes"
            />
          </div>
          <figcaption class="p-5">
            <h3 class="font-semibold text-highlighted">
              {{ t('noc.dashboards.items.security.title') }}
            </h3>
            <p class="mt-1 text-toned">{{ t('noc.dashboards.items.security.body') }}</p>
          </figcaption>
        </figure>
        <figure
          data-reveal
          :style="{ '--rv-i': 2 }"
          class="surface flex flex-col overflow-hidden rounded-2xl"
        >
          <div class="border-b border-default bg-(--ui-bg-muted) p-3">
            <RouterHealthGrid compact />
          </div>
          <figcaption class="p-5">
            <h3 class="font-semibold text-highlighted">
              {{ t('noc.dashboards.items.routers.title') }}
            </h3>
            <p class="mt-1 text-toned">{{ t('noc.dashboards.items.routers.body') }}</p>
            <NuxtLink
              :to="localePath('/monitoreo')"
              class="arrow-link mt-2 inline-flex min-h-11 items-center gap-1.5 font-semibold text-primary"
            >
              {{ t('product.sections.monitoring.cta') }}
              <UIcon name="i-lucide-arrow-right" class="arrow size-4" aria-hidden="true" />
            </NuxtLink>
          </figcaption>
        </figure>
      </div>
      <p class="mt-3 text-sm text-muted">{{ t('common.simulated') }}</p>
    </section>

    <div class="mx-auto grid max-w-6xl gap-4 px-4 py-6 sm:px-6 md:grid-cols-3">
      <div
        v-for="(k, i) in [
          { key: 'resolution', icon: 'i-lucide-tv' },
          { key: 'lite', icon: 'i-lucide-monitor-smartphone' },
          { key: 'privacy', icon: 'i-lucide-eye-off' },
        ]"
        :key="k.key"
        data-reveal
        :style="{ '--rv-i': i }"
        class="surface rounded-2xl p-6"
      >
        <UIcon :name="k.icon" class="size-6 text-primary" aria-hidden="true" />
        <h2 class="mt-4 text-lg font-semibold text-highlighted">{{ t(`noc.${k.key}.title`) }}</h2>
        <p class="mt-2 text-toned">{{ t(`noc.${k.key}.body`) }}</p>
      </div>
    </div>

    <CtaBand />
    <LazyFloatingCta hydrate-on-idle />
  </div>
</template>

<style scoped>
/* El código "se escribe" carácter a carácter al entrar (opacity + scale, una vez). */
@media (prefers-reduced-motion: no-preference) {
  .rv-in .code span {
    animation: rv-scale 0.35s var(--ease-out) backwards;
    animation-delay: calc(0.4s + var(--c) * 70ms);
  }
}
</style>
