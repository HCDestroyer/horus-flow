<script setup lang="ts">
// Fondo del hero: líneas de flujo que convergen hacia la cuadrícula (los flujos de los clientes
// llegando a Horus). Decorativo (aria-hidden) y sin JavaScript: SVG + CSS.
//
// - Las líneas son muy tenues y estáticas; solo los "paquetes" se mueven, con transform.
// - Movimiento finito (dos pasadas, < 5 s en total, WCAG 2.2.2) y con propósito: entran, llegan
//   al punto de la cuadrícula y se apagan. Nada de bucles lentos de fondo (apple-design §14).
// - Con prefers-reduced-motion no hay paquetes: solo las líneas.
const W = 1200
const H = 640
const TARGET = { x: 1010, y: 300 }
const starts = [40, 130, 230, 340, 450, 560, 620]

const lanes = starts.map((y0, i) => {
  const dx = TARGET.x
  const dy = TARGET.y - y0
  return {
    y0,
    angle: (Math.atan2(dy, dx) * 180) / Math.PI,
    length: Math.round(Math.hypot(dx, dy)),
    delay: `${(i % 4) * 0.18 + (i > 3 ? 0.1 : 0)}s`,
    duration: `${1.8 + (i % 3) * 0.15}s`,
  }
})
</script>

<template>
  <svg
    class="hero-ambient pointer-events-none absolute inset-0 -z-10 h-full w-full"
    :viewBox="`0 0 ${W} ${H}`"
    preserveAspectRatio="xMidYMid slice"
    aria-hidden="true"
    focusable="false"
  >
    <g
      v-for="(l, i) in lanes"
      :key="i"
      :transform="`translate(-20 ${l.y0}) rotate(${l.angle})`"
      :style="{ '--d': l.delay, '--len': `${l.length}px`, '--dur': l.duration }"
    >
      <line class="lane" x1="0" y1="0" :x2="l.length + 20" y2="0" />
      <rect class="pk" x="0" y="-1.5" width="44" height="3" rx="1.5" />
    </g>
  </svg>
</template>

<style scoped>
.hero-ambient {
  /* Se desvanece hacia los bordes y bajo el texto, para no competir con él. */
  -webkit-mask-image: radial-gradient(120% 90% at 85% 45%, #000 30%, transparent 75%);
  mask-image: radial-gradient(120% 90% at 85% 45%, #000 30%, transparent 75%);
}
.lane {
  stroke: var(--ui-primary);
  stroke-opacity: 0.13;
  stroke-width: 1.2;
  vector-effect: non-scaling-stroke;
}
.pk {
  fill: var(--ui-primary);
  opacity: 0;
}

@media (prefers-reduced-motion: no-preference) {
  .pk {
    animation: hf-packet var(--dur) cubic-bezier(0.45, 0.05, 0.4, 1) var(--d) 2 both;
  }
}

@keyframes hf-packet {
  0% {
    transform: translateX(0);
    opacity: 0;
  }
  15% {
    opacity: 0.6;
  }
  85% {
    opacity: 0.6;
  }
  100% {
    transform: translateX(var(--len));
    opacity: 0;
  }
}
</style>
