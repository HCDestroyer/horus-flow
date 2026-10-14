<script setup lang="ts">
// Fila del recorrido de producto: texto con puntos clave y una captura (o el slot `visual`).
// En escritorio alterna lados con `reverse`; en móvil el texto va primero.
import type { ShotName } from '~/config/shots'

const props = withDefaults(
  defineProps<{
    id: string
    eyebrow: string
    title: string
    body: string
    points?: string[]
    shot?: ShotName
    alt?: string
    reverse?: boolean
    link?: { to: string; label: string }
  }>(),
  { points: () => [], shot: undefined, alt: '', reverse: false, link: undefined },
)
const { t } = useI18n()
const titleId = computed(() => `${props.id}-title`)
</script>

<template>
  <section
    :id="id"
    :aria-labelledby="titleId"
    class="mx-auto grid max-w-6xl scroll-mt-24 gap-8 px-4 py-14 sm:px-6 lg:grid-cols-12 lg:items-center lg:gap-12"
  >
    <div data-reveal class="lg:col-span-5" :class="reverse ? 'lg:order-2 lg:col-start-8' : ''">
      <p class="eyebrow">{{ eyebrow }}</p>
      <h2 :id="titleId" class="mt-2 text-3xl font-bold text-balance text-highlighted">
        {{ title }}
      </h2>
      <p class="mt-4 text-lg text-toned">{{ body }}</p>
      <ul v-if="points.length" class="mt-6 space-y-3">
        <li v-for="(p, i) in points" :key="i" class="flex gap-3 text-toned">
          <UIcon
            name="i-lucide-check"
            class="mt-1 size-5 shrink-0 text-primary"
            aria-hidden="true"
          />
          <span>{{ p }}</span>
        </li>
      </ul>
      <NuxtLink
        v-if="link"
        :to="link.to"
        class="arrow-link mt-6 inline-flex min-h-11 items-center gap-1.5 font-semibold text-primary"
      >
        {{ link.label }}
        <UIcon name="i-lucide-arrow-right" class="arrow size-4" aria-hidden="true" />
      </NuxtLink>
    </div>
    <div
      data-reveal
      :style="{ '--rv-i': 2 }"
      class="min-w-0 lg:col-span-7"
      :class="reverse ? 'lg:order-1 lg:col-start-1' : ''"
    >
      <slot name="visual">
        <figure v-if="shot" class="surface overflow-hidden rounded-2xl">
          <div class="bg-[#121214]">
            <ShotPicture
              :name="shot"
              :alt="alt"
              sizes="(min-width: 1152px) 640px, (min-width: 1024px) 56vw, calc(100vw - 32px)"
            />
          </div>
          <figcaption class="border-t border-default px-4 py-2 text-sm text-muted">
            {{ t('common.simulated') }}
          </figcaption>
        </figure>
      </slot>
    </div>
  </section>
</template>
