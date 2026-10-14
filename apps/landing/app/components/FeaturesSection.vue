<script setup lang="ts">
const { t } = useI18n()
const pairs = [
  ['infected', 'finding'],
  ['router', 'kiosk'],
] as const
const more = ['alerts', 'reputation', 'ipv6', 'multi'] as const
const halfSizes =
  '(min-width: 1152px) 540px, (min-width: 768px) calc(50vw - 48px), calc(100vw - 32px)'
</script>

<template>
  <section
    id="features"
    aria-labelledby="features-title"
    class="mx-auto max-w-6xl px-4 py-16 sm:px-6"
  >
    <SectionHeading
      id="features-title"
      :eyebrow="t('features.eyebrow')"
      :title="t('features.title')"
      :lead="t('features.lead')"
    />

    <figure class="surface mt-10 overflow-hidden rounded-2xl">
      <div class="border-b border-default bg-[#121214]">
        <ShotPicture
          name="mural"
          :alt="t('features.items.mural.alt')"
          sizes="(min-width: 1152px) 1104px, calc(100vw - 32px)"
        />
      </div>
      <figcaption class="grid gap-2 p-6 md:grid-cols-[1fr_2fr] md:gap-8">
        <h3 class="text-xl font-semibold text-highlighted">
          {{ t('features.items.mural.title') }}
        </h3>
        <p class="text-toned">
          {{ t('features.items.mural.body') }}
          <span class="ml-1 text-sm whitespace-nowrap text-muted"
            >· {{ t('features.simulated') }}</span
          >
        </p>
      </figcaption>
    </figure>

    <div v-for="(pair, row) in pairs" :key="row" class="mt-6 grid gap-6 md:grid-cols-2">
      <figure
        v-for="key in pair"
        :key="key"
        class="surface flex flex-col overflow-hidden rounded-2xl"
      >
        <div class="border-b border-default bg-[#121214]">
          <ShotPicture :name="key" :alt="t(`features.items.${key}.alt`)" :sizes="halfSizes" />
        </div>
        <figcaption class="p-6">
          <h3 class="text-xl font-semibold text-highlighted">
            {{ t(`features.items.${key}.title`) }}
          </h3>
          <p class="mt-2 text-toned">{{ t(`features.items.${key}.body`) }}</p>
        </figcaption>
      </figure>
    </div>

    <div class="mt-6 grid gap-6 md:grid-cols-[minmax(0,5fr)_minmax(0,7fr)]">
      <figure
        class="surface flex flex-col gap-6 overflow-hidden rounded-2xl p-6 sm:flex-row sm:items-center"
      >
        <div class="w-36 shrink-0 overflow-hidden rounded-[1.25rem] border border-default sm:w-44">
          <ShotPicture name="mobile" :alt="t('features.items.mobile.alt')" sizes="176px" />
        </div>
        <figcaption>
          <h3 class="text-xl font-semibold text-highlighted">
            {{ t('features.items.mobile.title') }}
          </h3>
          <p class="mt-2 text-toned">{{ t('features.items.mobile.body') }}</p>
        </figcaption>
      </figure>
      <div class="surface rounded-2xl p-6">
        <h3 class="text-xl font-semibold text-highlighted">{{ t('features.moreTitle') }}</h3>
        <ul class="mt-4 space-y-3">
          <li v-for="m in more" :key="m" class="flex gap-3 text-toned">
            <UIcon
              name="i-lucide-check"
              class="mt-1 size-5 shrink-0 text-primary"
              aria-hidden="true"
            />
            <span>{{ t(`features.more.${m}`) }}</span>
          </li>
        </ul>
      </div>
    </div>
  </section>
</template>
