<script setup lang="ts">
// Gráfica ilustrativa de la histéresis: la CPU cruza el umbral un instante (sin cambio), lo
// supera de forma sostenida (Aviso) y se normaliza (En línea). La línea se descubre de
// izquierda a derecha con una cortina que se desplaza (transform), una sola vez al entrar en
// pantalla. Las anotaciones son HTML para que se lean a cualquier ancho.
const { t } = useI18n()

// 600×200: y crece hacia abajo; umbral en y=70.
const points = [
  [0, 140],
  [40, 136],
  [80, 142],
  [110, 128],
  [125, 52],
  [140, 126],
  [180, 138],
  [220, 132],
  [260, 120],
  [290, 90],
  [315, 58],
  [350, 52],
  [390, 60],
  [420, 50],
  [450, 62],
  [470, 96],
  [500, 128],
  [540, 136],
  [600, 132],
]
  .map(([x, y]) => `${x},${y}`)
  .join(' ')

const marks = [
  { key: 'spike', n: 1, left: '20.8%', top: '8%' },
  { key: 'sustained', n: 2, left: '61%', top: '6%' },
  { key: 'recover', n: 3, left: '86%', top: '50%' },
] as const
</script>

<template>
  <figure class="hyst surface rounded-2xl p-4 sm:p-6" data-reveal>
    <figcaption class="sr-only">{{ t('monitoring.hysteresis.chartLabel') }}</figcaption>
    <div aria-hidden="true">
      <div class="flex flex-wrap gap-x-5 gap-y-1 text-sm text-muted">
        <span class="flex items-center gap-2"
          ><span class="inline-block h-0.5 w-5 rounded bg-primary" />{{
            t('monitoring.hysteresis.metric')
          }}</span
        >
        <span class="flex items-center gap-2"
          ><span class="inline-block w-5 border-t-2 border-dashed border-(--st-warning)" />{{
            t('monitoring.hysteresis.threshold')
          }}</span
        >
      </div>
      <div class="relative mt-3 overflow-hidden">
        <svg viewBox="0 0 600 200" class="block h-auto w-full" focusable="false">
          <line
            x1="0"
            y1="70"
            x2="600"
            y2="70"
            stroke="var(--st-warning)"
            stroke-width="2"
            stroke-dasharray="6 6"
            vector-effect="non-scaling-stroke"
          />
          <polyline
            :points="points"
            fill="none"
            stroke="var(--ui-primary)"
            stroke-width="2.5"
            stroke-linejoin="round"
            stroke-linecap="round"
            vector-effect="non-scaling-stroke"
          />
        </svg>
        <span
          v-for="m in marks"
          :key="m.key"
          class="absolute flex size-6 -translate-x-1/2 items-center justify-center rounded-full bg-(--ui-text-highlighted) text-xs font-bold text-(--surface)"
          :style="{ left: m.left, top: m.top }"
          >{{ m.n }}</span
        >
        <span class="curtain absolute inset-0 bg-(--surface)" />
      </div>
      <!-- Cinta de estado: cambia solo con la condición sostenida (no con el pico). -->
      <div class="mt-2 flex h-8 overflow-hidden rounded-lg text-xs font-semibold">
        <span data-state="online" class="ribbon flex basis-[58%] items-center px-2">{{
          t('monitoring.states.items.online.label')
        }}</span>
        <span data-state="warning" class="ribbon flex basis-[26%] items-center px-2">{{
          t('monitoring.states.items.warning.label')
        }}</span>
        <span data-state="online" class="ribbon flex basis-[16%] items-center truncate px-2">{{
          t('monitoring.states.items.online.label')
        }}</span>
      </div>
    </div>
    <ol class="mt-4 grid gap-2 text-sm text-toned sm:grid-cols-3">
      <li v-for="m in marks" :key="m.key" class="flex items-start gap-2">
        <span
          class="flex size-6 shrink-0 items-center justify-center rounded-full bg-(--ui-text-highlighted) text-xs font-bold text-(--surface)"
          aria-hidden="true"
          >{{ m.n }}</span
        >
        {{ t(`monitoring.hysteresis.marks.${m.key}`) }}
      </li>
    </ol>
  </figure>
</template>

<style scoped>
.ribbon {
  color: var(--ui-text-highlighted);
  background: color-mix(in srgb, var(--st) 22%, var(--surface));
  border-top: 3px solid var(--st);
}
.curtain {
  display: none;
  transform-origin: right;
}
@media (prefers-reduced-motion: no-preference) {
  .hyst.rv-in .curtain {
    display: block;
    animation: curtain 2.2s var(--ease-out) 0.3s both;
  }
}
@keyframes curtain {
  from {
    transform: translateX(0);
  }
  to {
    transform: translateX(100%);
  }
}
</style>
