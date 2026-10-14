<script setup lang="ts">
// Maqueta animada de un análisis con IA (no es un chat real; datos simulados). El texto
// completo está en el HTML: sin JavaScript o con reduced motion se ve el análisis terminado.
// Con movimiento, al entrar en pantalla (clase rv-in, app/utils/reveal-script.ts):
//   1. "Analizando con datos enmascarados…" (tres puntos),
//   2. las palabras aparecen en orden (solo opacity: el texto ya ocupa su sitio, sin saltos),
//   3. entra el comando sugerido y por último la insignia "Verificada".
// Es finito (~4 s) y "Ver de nuevo" lo repite (botón activado por motion-enhance.ts).
const props = withDefaults(defineProps<{ compact?: boolean }>(), { compact: false })
const { t } = useI18n()

const START = 0.9 // s hasta la primera palabra
const STEP = 0.022 // s entre palabras

const paragraphs = computed(() => {
  const keys = props.compact ? ['p1'] : ['p1', 'p2', 'p3']
  let n = 0
  return keys.map((k) => ({
    key: k,
    title: t(`ai.mock.${k}Title`),
    words: t(`ai.mock.${k}`)
      .split(' ')
      .map((w) => ({ w, i: n++ })),
  }))
})
const total = computed(() => paragraphs.value.reduce((a, p) => a + p.words.length, 0))
const end = computed(() => `${(START + total.value * STEP + 0.2).toFixed(2)}s`)
</script>

<template>
  <figure
    class="ai-mock surface overflow-hidden rounded-2xl text-left shadow-sm"
    data-reveal="fade"
    :style="{ '--t0': `${START}s`, '--step': `${STEP}s`, '--t-end': end }"
  >
    <figcaption class="sr-only">{{ t('ai.mock.label') }}</figcaption>
    <div class="flex flex-wrap items-center gap-2 border-b border-default px-4 py-3 sm:px-5">
      <UIcon name="i-lucide-sparkles" class="size-5 shrink-0 text-primary" aria-hidden="true" />
      <span class="font-semibold text-highlighted">{{ t('ai.mock.title') }}</span>
      <span
        class="ml-auto rounded-full border border-default px-2.5 py-0.5 text-xs whitespace-nowrap text-muted"
        >{{ t('ai.mock.model') }}</span
      >
    </div>

    <div class="space-y-3 px-4 py-4 sm:px-5">
      <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
        <span class="text-toned">{{ t('ai.mock.subject') }}</span>
        <span
          class="rounded-md bg-(--ui-bg-accented) px-2 py-0.5 font-mono text-[0.8rem] text-highlighted"
          >{{ t('ai.mock.client') }}</span
        >
      </div>

      <!-- Estado: "Analizando…" se superpone a "Análisis listo" hasta que termina. -->
      <p class="relative text-sm font-medium">
        <span class="ai-done inline-flex items-center gap-1.5 text-(--st-online)">
          <UIcon name="i-lucide-circle-check" class="size-4" aria-hidden="true" />
          {{ t('ai.mock.done') }}
        </span>
        <span
          class="ai-pending absolute inset-0 flex items-center gap-1.5 bg-(--surface) text-muted"
          aria-hidden="true"
        >
          {{ t('ai.mock.pending') }}
          <span class="ai-dots inline-flex gap-0.5" aria-hidden="true"><i /><i /><i /></span>
        </span>
      </p>

      <p v-for="p in paragraphs" :key="p.key" class="text-[0.95rem] leading-relaxed text-toned">
        <strong class="w font-semibold text-highlighted" :style="{ '--w': p.words[0]?.i ?? 0 }"
          >{{ p.title + ' ' }}
        </strong>
        <span v-for="w in p.words" :key="w.i" class="w" :style="{ '--w': w.i }">{{
          w.w + ' '
        }}</span>
      </p>

      <template v-if="!compact">
        <div class="ai-late">
          <pre
            class="rounded-lg border border-default bg-(--ui-bg-muted) p-3 font-mono text-[0.78rem] leading-relaxed whitespace-pre-wrap [overflow-wrap:anywhere] text-highlighted"
          ><code>{{ t('ai.mock.cmd') }}</code></pre>
          <p class="mt-1.5 flex items-start gap-1.5 text-xs text-muted">
            <UIcon name="i-lucide-hand" class="mt-px size-3.5 shrink-0" aria-hidden="true" />
            {{ t('ai.mock.cmdNote') }}
          </p>
        </div>
      </template>
    </div>

    <div
      class="flex flex-wrap items-center gap-x-3 gap-y-2 border-t border-default px-4 py-3 sm:px-5"
    >
      <span
        class="ai-badge inline-flex items-center gap-1.5 rounded-full border border-(--st-online)/40 px-2.5 py-1 text-sm font-semibold text-(--st-online)"
      >
        <UIcon name="i-lucide-badge-check" class="size-4" aria-hidden="true" />
        {{ t('ai.mock.verified') }}
      </span>
      <span class="ai-late min-w-0 flex-1 text-xs text-muted">
        {{ compact ? t('ai.mock.tools') : t('ai.mock.verifiedBody') }}
      </span>
      <button
        v-if="!compact"
        type="button"
        data-replay
        hidden
        class="inline-flex min-h-11 items-center gap-1.5 rounded-lg px-3 text-sm font-medium text-primary hover:bg-(--ui-bg-accented)/60"
      >
        <UIcon name="i-lucide-rotate-ccw" class="size-4" aria-hidden="true" />
        {{ t('common.replay') }}
      </button>
    </div>
  </figure>
</template>

<style scoped>
.ai-pending {
  opacity: 0;
}
.ai-dots i {
  display: block;
  width: 4px;
  height: 4px;
  border-radius: 9999px;
  background: currentColor;
}
button[hidden] {
  display: none;
}

@media (prefers-reduced-motion: no-preference) {
  .ai-mock.rv-in .w {
    animation: ai-word 0.32s ease-out backwards;
    animation-delay: calc(var(--t0) + var(--w) * var(--step));
  }
  .ai-mock.rv-in .ai-pending {
    animation: ai-pending var(--t-end) linear both;
  }
  .ai-mock.rv-in .ai-dots i {
    animation: ai-dot 0.9s ease-in-out 4 both;
  }
  .ai-mock.rv-in .ai-dots i:nth-child(2) {
    animation-delay: 0.15s;
  }
  .ai-mock.rv-in .ai-dots i:nth-child(3) {
    animation-delay: 0.3s;
  }
  .ai-mock.rv-in .ai-done {
    animation: ai-word 0.3s ease-out backwards;
    animation-delay: var(--t-end);
  }
  .ai-mock.rv-in .ai-late {
    animation: rv-up 0.5s var(--ease-out) backwards;
    animation-delay: var(--t-end);
  }
  .ai-mock.rv-in .ai-badge {
    animation: ai-badge 0.5s var(--ease-out) backwards;
    animation-delay: calc(var(--t-end) + 0.25s);
  }
}

@keyframes ai-word {
  from {
    opacity: 0;
  }
}
@keyframes ai-pending {
  0%,
  96% {
    opacity: 1;
  }
  100% {
    opacity: 0;
  }
}
@keyframes ai-dot {
  0%,
  100% {
    opacity: 0.25;
    translate: 0 0;
  }
  50% {
    opacity: 1;
    translate: 0 -2px;
  }
}
@keyframes ai-badge {
  from {
    opacity: 0;
    scale: 0.85;
  }
}
</style>
