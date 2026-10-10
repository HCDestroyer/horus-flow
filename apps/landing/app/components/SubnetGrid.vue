<script setup lang="ts">
// ELEMENTO DISTINTIVO de la landing: una red /24 dibujada como 256 puntos (uno por IP, que en
// Horus es un cliente) con un solo punto señalado como Infectado. Resume el producto: entre
// miles de clientes, Horus señala cuál y por qué. La red (.0) y el broadcast (.255) van huecos,
// un detalle que un ingeniero de red reconoce. Se anima una sola vez (barrido y anillo) y nada
// con prefers-reduced-motion.
const { t } = useI18n()

const COLS = 16
const STEP = 20
const PAD = 10
const FLAGGED = 47

// Actividad pseudoaleatoria determinista (misma en SSR y cliente).
function active(i: number) {
  return ((i * 2654435761) >>> 0) % 7 < 2
}

const dots = Array.from({ length: 256 }, (_, i) => ({
  i,
  x: PAD + (i % COLS) * STEP,
  y: PAD + Math.floor(i / COLS) * STEP,
  kind: i === FLAGGED ? 'flagged' : i === 0 || i === 255 ? 'reserved' : active(i) ? 'active' : 'idle',
}))
const flagged = dots[FLAGGED]!
const gridBottom = PAD + 15 * STEP
</script>

<template>
  <figure class="relative mx-auto w-full max-w-[22rem]">
    <figcaption class="mb-2 text-sm text-muted">
      <span class="font-mono" aria-hidden="true">{{ t('hero.gridCaption') }}</span>
    </figcaption>
    <svg
      viewBox="0 0 344 344"
      role="img"
      aria-labelledby="grid-title grid-desc"
      class="subnet block h-auto w-full"
    >
      <title id="grid-title">{{ t('hero.gridTitle') }}</title>
      <desc id="grid-desc">{{ t('hero.gridDesc') }}</desc>
      <g aria-hidden="true">
        <rect class="scan" x="0" :y="PAD - 8" width="330" height="16" rx="8" />
        <template v-for="d in dots" :key="d.i">
          <circle
            v-if="d.kind === 'reserved'"
            :cx="d.x"
            :cy="d.y"
            r="3.2"
            fill="none"
            stroke="var(--dot-strong)"
            stroke-width="1.2"
          />
          <circle
            v-else-if="d.kind !== 'flagged'"
            :cx="d.x"
            :cy="d.y"
            r="3.4"
            :fill="d.kind === 'active' ? 'var(--dot-strong)' : 'var(--dot)'"
          />
        </template>
        <!-- Punto señalado: anillo, punto y guía hasta la ficha (debajo de la cuadrícula). -->
        <circle
          class="ring"
          :cx="flagged.x"
          :cy="flagged.y"
          r="9"
          fill="none"
          stroke="var(--infected)"
          stroke-width="2"
        />
        <circle :cx="flagged.x" :cy="flagged.y" r="4.6" fill="var(--infected)" />
        <path
          class="leader"
          :d="`M${flagged.x + 9} ${flagged.y} H${flagged.x + 24} V${gridBottom + 34}`"
          fill="none"
          stroke="var(--infected)"
          stroke-width="1.5"
          stroke-dasharray="3 3"
        />
      </g>
    </svg>
    <div class="surface relative rounded-xl p-4 shadow-sm" aria-hidden="true">
      <div class="flex items-center gap-2">
        <span class="inline-block size-2.5 rounded-full bg-(--infected)" />
        <span class="font-mono font-semibold text-highlighted">10.20.1.47</span>
      </div>
      <p class="mt-1 font-semibold text-(--infected)">
        {{ t('hero.flagged') }} <span class="font-normal text-muted">· {{ t('hero.confidence') }}</span>
      </p>
      <p class="mt-1 text-[0.95rem] text-toned">{{ t('hero.reason') }}</p>
    </div>
  </figure>
</template>

<style scoped>
.scan {
  fill: var(--ui-primary);
  opacity: 0;
}

@media (prefers-reduced-motion: no-preference) {
  .scan {
    animation: scan 1.4s cubic-bezier(0.4, 0, 0.2, 1) 0.2s 1 both;
  }
  .ring {
    transform-box: fill-box;
    transform-origin: center;
    animation: ring 0.5s cubic-bezier(0.2, 0.9, 0.3, 1) 1.5s 1 both;
  }
  .leader {
    animation: fade 0.3s ease-out 1.8s 1 both;
  }
}

@keyframes scan {
  0% {
    opacity: 0.12;
    transform: translateY(0);
  }
  85% {
    opacity: 0.12;
  }
  100% {
    opacity: 0;
    transform: translateY(300px);
  }
}

@keyframes ring {
  from {
    opacity: 0;
    transform: scale(2);
  }
  to {
    opacity: 1;
    transform: scale(1);
  }
}

@keyframes fade {
  from {
    opacity: 0;
  }
  to {
    opacity: 1;
  }
}
</style>
