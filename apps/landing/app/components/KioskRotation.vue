<script setup lang="ts">
// Televisor con el kiosco rotando dashboards (capturas reales, datos simulados). Sin JS o con
// reduced motion se ve la primera diapositiva; con movimiento, motion-enhance.ts pasa a la
// siguiente cada 6 s (fundido de opacidad), marca su elemento en la lista de reproducción y
// muestra el progreso. Solo a la vista y con botón de pausa (WCAG 2.2.2).
const { t } = useI18n()
const slides = [
  { key: 'noc', shot: 'kioskNoc', alt: 'noc.tv.altNoc' },
  { key: 'security', shot: 'kioskSec', alt: 'noc.tv.altSecurity' },
] as const
const sizes = '(min-width: 1024px) 560px, calc(100vw - 48px)'
</script>

<template>
  <figure data-cycle="slides" data-interval="6000" class="kiosk">
    <figcaption class="sr-only">{{ t('noc.tv.label') }}</figcaption>
    <div class="rounded-[1.1rem] bg-[#05070d] p-1.5 shadow-xl ring-1 ring-black/20 sm:p-2">
      <div class="relative aspect-video overflow-hidden rounded-lg bg-[#0b0f19]">
        <div
          v-for="(s, i) in slides"
          :key="s.key"
          data-slide
          :data-active="i === 0 ? 'true' : 'false'"
          class="slide absolute inset-0"
        >
          <ShotPicture :name="s.shot" :alt="t(s.alt)" :sizes="sizes" :eager="i === 0" />
        </div>
      </div>
    </div>
    <div class="mx-auto h-3 w-24 rounded-b-lg bg-[#05070d]/80" aria-hidden="true" />

    <div class="mt-4 flex items-center gap-2">
      <span class="text-sm text-muted">{{ t('noc.tv.playlist') }}</span>
      <ol class="flex min-w-0 flex-1 flex-wrap gap-2">
        <li
          v-for="(s, i) in slides"
          :key="s.key"
          data-slide-chip
          :data-active="i === 0 ? 'true' : 'false'"
          :aria-current="i === 0 ? 'true' : undefined"
          class="chip relative overflow-hidden rounded-full border border-default px-3 py-1 text-sm"
        >
          <span class="bar absolute inset-0 origin-left" aria-hidden="true" />
          <span class="relative">{{ t(`noc.tv.slides.${s.key}`) }}</span>
        </li>
      </ol>
      <button
        type="button"
        data-cycle-toggle
        hidden
        :data-label-play="t('motion.play')"
        :data-label-pause="t('motion.pause')"
        :aria-label="t('motion.pause')"
        :title="t('motion.pause')"
        class="cycle-toggle flex size-11 shrink-0 items-center justify-center rounded-full border border-default text-toned hover:bg-(--ui-bg-accented)/60 hover:text-highlighted"
      >
        <UIcon name="i-lucide-pause" class="icon-pause size-5" aria-hidden="true" />
        <UIcon name="i-lucide-play" class="icon-play size-5" aria-hidden="true" />
      </button>
    </div>
  </figure>
</template>

<style scoped>
.slide {
  opacity: 0;
  transition: opacity 0.9s var(--ease-out);
}
.slide[data-active='true'] {
  opacity: 1;
}
.chip {
  color: var(--ui-text-toned);
}
.chip[data-active='true'] {
  color: var(--ui-text-highlighted);
  border-color: var(--ui-primary);
  font-weight: 600;
}
.bar {
  background: color-mix(in srgb, var(--ui-primary) 14%, transparent);
  transform: scaleX(0);
}
.chip[data-active='true'] .bar {
  transform: scaleX(1);
}
@media (prefers-reduced-motion: no-preference) {
  .kiosk[data-running='true'] .chip[data-active='true'] .bar {
    animation: kiosk-bar 6s linear both;
  }
}
@keyframes kiosk-bar {
  from {
    transform: scaleX(0);
  }
  to {
    transform: scaleX(1);
  }
}
.cycle-toggle[data-state='playing'] .icon-play,
.cycle-toggle[data-state='paused'] .icon-pause,
.cycle-toggle:not([data-state]) .icon-play {
  display: none;
}
</style>
