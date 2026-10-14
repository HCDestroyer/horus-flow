<script setup lang="ts">
// Animación neón (vídeo renderizado con Remotion en apps/landing-motion). Es el único elemento
// llamativo de la página; todo lo demás se queda sobrio.
//
// - El póster es una imagen normal (AVIF/WebP, dimensiones fijas): sin JS o con
//   prefers-reduced-motion es lo único que se ve; con `priority` es la imagen LCP del hero.
// - El <video autoplay muted loop playsinline preload="none"> no tiene fuentes en el HTML: el
//   script (app/utils/motion-video.ts) las pone al acercarse a la pantalla, elige 960 o 1920
//   px y pausa fuera de pantalla o con la pestaña oculta.
// - El vídeo es decorativo (aria-hidden). La información equivalente va en texto: `label`
//   (nombre accesible del contenedor, role="img") y el HTML que rodea al componente.
// - Botón para pausar y reanudar, siempre disponible; con reduced motion dice "Reproducir
//   animación" y nada se descarga hasta pulsarlo.
// - Slot `fallback`: se muestra si el póster no carga (p. ej. el diagrama SVG).
import { motionClips, motionFile, posterSrcset, type MotionClip } from '~/config/motion'

const props = withDefaults(
  defineProps<{
    clip: MotionClip
    /** Descripción equivalente (nombre accesible). Sin ella, el bloque es decorativo. */
    label?: string
    /** Póster como LCP: carga inmediata y prioridad alta. */
    priority?: boolean
    /** Esquina del botón de pausa: la que la composición deja libre. */
    controls?: 'top-left' | 'bottom-right'
  }>(),
  { label: undefined, priority: false, controls: 'bottom-right' },
)

const { t } = useI18n()
const meta = computed(() => motionClips[props.clip])
const sources = computed(() =>
  (['webm', 'mp4'] as const).map((ext) => ({
    type: ext === 'webm' ? 'video/webm; codecs=vp9' : 'video/mp4; codecs=avc1.640028',
    large: motionFile(props.clip, 'video', 1920, ext),
    small: motionFile(props.clip, 'video', 960, ext),
  })),
)

if (props.priority) {
  useHead({
    link: [
      {
        rel: 'preload',
        as: 'image',
        type: 'image/avif',
        imagesrcset: posterSrcset(props.clip, 'avif'),
        imagesizes: meta.value.sizes,
        fetchpriority: 'high',
      },
    ],
  })
}

const root = ref<HTMLElement>()
onMounted(() => {
  if (root.value) enhanceMotionVideo(root.value)
})
</script>

<template>
  <div
    ref="root"
    data-motion
    :data-large-media="meta.largeMedia"
    class="motion relative overflow-hidden rounded-2xl"
    :style="{ aspectRatio: `${meta.width} / ${meta.height}` }"
  >
    <div
      class="motion-media absolute inset-0"
      :role="label ? 'img' : undefined"
      :aria-label="label"
      :aria-hidden="label ? undefined : 'true'"
    >
      <picture>
        <source type="image/avif" :srcset="posterSrcset(clip, 'avif')" :sizes="meta.sizes" />
        <source type="image/webp" :srcset="posterSrcset(clip, 'webp')" :sizes="meta.sizes" />
        <img
          :src="motionFile(clip, 'poster', 960, 'webp')"
          alt=""
          :width="meta.width"
          :height="meta.height"
          :loading="priority ? 'eager' : 'lazy'"
          :fetchpriority="priority ? 'high' : 'auto'"
          decoding="async"
          class="motion-poster absolute inset-0 block size-full object-cover"
        />
      </picture>
      <!--
        1 px más pequeño que el póster (inset-px): el primer frame del vídeo nunca es "más
        grande" que el póster, así que no sustituye al póster como LCP. El borde es el fondo
        oscuro del lienzo, idéntico en ambos.
      -->
      <video
        class="motion-video absolute inset-px block object-cover"
        autoplay
        muted
        loop
        playsinline
        preload="none"
        disablepictureinpicture
        disableremoteplayback
        aria-hidden="true"
        tabindex="-1"
        :width="meta.width"
        :height="meta.height"
        :style="{ width: 'calc(100% - 2px)', height: 'calc(100% - 2px)' }"
      >
        <source
          v-for="s in sources"
          :key="s.type"
          :type="s.type"
          :data-src-large="s.large"
          :data-src-small="s.small"
        />
      </video>
    </div>
    <div v-if="$slots.fallback" class="motion-fallback absolute inset-0">
      <slot name="fallback" />
    </div>
    <button
      type="button"
      data-motion-toggle
      data-state="paused"
      :data-label-play="t('motion.play')"
      :data-label-pause="t('motion.pause')"
      :aria-label="t('motion.play')"
      :title="t('motion.play')"
      hidden
      :class="controls === 'top-left' ? 'top-3 left-3' : 'right-3 bottom-3'"
      class="motion-toggle absolute inline-flex min-h-11 min-w-11 items-center justify-center gap-2 rounded-full px-3 text-sm font-semibold"
    >
      <svg
        class="motion-icon-play size-5"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
        focusable="false"
      >
        <path d="M6 4.5v15l13-7.5z" />
      </svg>
      <svg
        class="motion-icon-pause size-5"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        aria-hidden="true"
        focusable="false"
      >
        <path d="M8 5v14M16 5v14" />
      </svg>
      <span class="motion-text" data-motion-text aria-hidden="true">{{ t('motion.play') }}</span>
    </button>
  </div>
</template>

<style scoped>
/* Fondo del lienzo de las composiciones (NEON.bg): sin destello claro mientras carga. */
.motion {
  background: #060a14;
  box-shadow:
    0 0 0 1px var(--ui-border),
    0 12px 32px rgb(6 10 20 / 0.18);
}

.motion-video {
  opacity: 0;
  transition: opacity 0.4s ease-out;
}
.motion-playing .motion-video {
  opacity: 1;
}

.motion-fallback {
  display: none;
  background: var(--surface);
}
.motion-failed .motion-fallback {
  display: block;
}

/* Control flotante sólido y legible sobre el fondo oscuro del vídeo (contraste 15:1). */
.motion-toggle {
  color: #e8ecf4;
  background: rgb(13 18 32 / 0.86);
  border: 1px solid rgb(255 255 255 / 0.16);
}
.motion-toggle:hover {
  background: rgb(21 28 46 / 0.95);
}
.motion-toggle:focus-visible {
  outline: 2px solid #93b1ff;
  outline-offset: 2px;
}
.motion-toggle[hidden] {
  display: none;
}
.motion-toggle[data-state='playing'] .motion-icon-play,
.motion-toggle[data-state='paused'] .motion-icon-pause {
  display: none;
}

/* El texto del botón solo se ve con reduced motion ("Reproducir animación") y si el lienzo es
   ancho; en el hero (estrecho) taparía la cuadrícula, así que queda el icono de 44 px con su
   nombre accesible y su title. */
.motion {
  container-type: inline-size;
}
.motion-text {
  display: none;
}
@container (min-width: 36rem) {
  .motion-reduced .motion-toggle .motion-text {
    display: inline;
  }
  .motion-reduced .motion-toggle {
    padding-inline: 1rem;
  }
}
</style>
