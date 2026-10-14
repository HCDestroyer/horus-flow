<script setup lang="ts">
// Captura responsive: AVIF con WebP de respaldo, tamaños por ancho, carga diferida y
// dimensiones explícitas (sin saltos de diseño).
import { shots, srcset, type Shot } from '~/config/shots'

const props = withDefaults(
  defineProps<{ name: Shot['name']; alt: string; sizes: string; eager?: boolean }>(),
  { eager: false },
)
const shot = computed(() => shots[props.name])
const fallback = computed(() => {
  const w = shot.value.widths[Math.min(1, shot.value.widths.length - 1)]
  return `/img/shots/${shot.value.name}-${w}.webp`
})
</script>

<template>
  <picture>
    <source type="image/avif" :srcset="srcset(shot, 'avif')" :sizes="sizes" />
    <source type="image/webp" :srcset="srcset(shot, 'webp')" :sizes="sizes" />
    <img
      :src="fallback"
      :alt="alt"
      :width="shot.width"
      :height="shot.height"
      :loading="eager ? 'eager' : 'lazy'"
      :fetchpriority="eager ? 'high' : 'auto'"
      decoding="async"
      class="block h-auto w-full"
    />
  </picture>
</template>
