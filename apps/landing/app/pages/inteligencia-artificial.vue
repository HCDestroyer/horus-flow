<script setup lang="ts">
// Asistente de IA (/inteligencia-artificial, /en/ai): cómo funciona, privacidad y
// enmascarado, proveedores (Claude o modelo local), verificación, control del ISP, MCP y
// preguntas frecuentes. La maqueta animada del hero no es un chat real (datos simulados).
definePageMeta({ viewTransition: true })
usePageSeo('ai', 'meta.aiTitle', 'meta.aiDescription')

const { t } = useI18n()
const faqKeys = ['routers', 'data', 'cost', 'default', 'history']
const faq = computed(() => faqKeys.map((k) => ({ q: t(`ai.faq.${k}.q`), a: t(`ai.faq.${k}.a`) })))
usePageLd('ai', 'nav.aiLong', 'meta.aiDescription', faq.value)

const steps = [
  { key: 'ask', icon: 'i-lucide-sparkles' },
  { key: 'mask', icon: 'i-lucide-eye-off' },
  { key: 'tools', icon: 'i-lucide-file-search' },
  { key: 'verify', icon: 'i-lucide-badge-check' },
]
const sees = ['behavior', 'asn', 'routers', 'pseudo']
const never = ['ip', 'names', 'contacts']
const verifyItems = ['numbers', 'catalog', 'flagged', 'never']
const control = [
  { key: 'off', icon: 'i-lucide-power' },
  { key: 'audit', icon: 'i-lucide-list-checks' },
  { key: 'limit', icon: 'i-lucide-gauge' },
  { key: 'history', icon: 'i-lucide-history' },
]
</script>

<template>
  <div>
    <PageIntro
      :crumb="t('nav.aiLong')"
      :eyebrow="t('ai.eyebrow')"
      :title="t('ai.title')"
      :lead="t('ai.lead')"
    >
      <AiAnalysisMock />
    </PageIntro>

    <!-- Cómo funciona: cuatro pasos numerados. -->
    <section aria-labelledby="ai-steps" class="mx-auto max-w-6xl px-4 py-14 sm:px-6">
      <SectionHeading
        id="ai-steps"
        :eyebrow="t('ai.eyebrow')"
        :title="t('ai.steps.title')"
        :lead="t('ai.steps.lead')"
      />
      <ol class="mt-10 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <li
          v-for="(s, i) in steps"
          :key="s.key"
          data-reveal
          :style="{ '--rv-i': i }"
          class="surface relative rounded-2xl p-6"
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
            {{ t(`ai.steps.items.${s.key}.title`) }}
          </h3>
          <p class="mt-2 text-toned">{{ t(`ai.steps.items.${s.key}.body`) }}</p>
        </li>
      </ol>
    </section>

    <!-- Privacidad: ejemplo de enmascarado y qué ve / qué no ve. -->
    <section aria-labelledby="ai-privacy" class="mx-auto max-w-6xl px-4 py-14 sm:px-6">
      <SectionHeading
        id="ai-privacy"
        :eyebrow="t('security.eyebrow')"
        :title="t('ai.privacy.title')"
        :lead="t('ai.privacy.lead')"
      />
      <figure data-reveal class="mask-demo surface mt-10 rounded-2xl p-6">
        <figcaption class="sr-only">{{ t('ai.privacy.maskLabel') }}</figcaption>
        <div class="grid items-center gap-4 md:grid-cols-[1fr_auto_1fr]" aria-hidden="true">
          <div class="rounded-xl border border-default bg-(--ui-bg-muted) p-4">
            <p class="text-xs font-semibold text-muted uppercase">{{ t('ai.privacy.before') }}</p>
            <p class="mask-before mt-2 font-mono text-highlighted">
              {{ t('ai.privacy.beforeIp') }}
              <span class="text-toned">· {{ t('ai.privacy.beforeAlias') }}</span>
            </p>
          </div>
          <UIcon
            name="i-lucide-arrow-right"
            class="mask-arrow mx-auto size-6 rotate-90 text-primary md:rotate-0"
          />
          <div class="rounded-xl border border-primary/40 bg-primary/5 p-4">
            <p class="text-xs font-semibold text-primary uppercase">{{ t('ai.privacy.after') }}</p>
            <p class="mask-after mt-2 font-mono font-semibold text-highlighted">
              {{ t('ai.privacy.afterValue') }}
            </p>
          </div>
        </div>
        <p class="mt-3 text-sm text-muted">{{ t('common.simulated') }}</p>
      </figure>
      <div class="mt-6 grid gap-6 md:grid-cols-2">
        <div data-reveal class="surface rounded-2xl p-6">
          <h3 class="flex items-center gap-2 text-lg font-semibold text-highlighted">
            <UIcon name="i-lucide-eye" class="size-5 text-primary" aria-hidden="true" />
            {{ t('ai.privacy.seesTitle') }}
          </h3>
          <ul class="mt-4 space-y-3">
            <li v-for="k in sees" :key="k" class="flex gap-3 text-toned">
              <UIcon
                name="i-lucide-check"
                class="mt-1 size-5 shrink-0 text-primary"
                aria-hidden="true"
              />
              {{ t(`ai.privacy.sees.${k}`) }}
            </li>
          </ul>
        </div>
        <div data-reveal :style="{ '--rv-i': 1 }" class="surface rounded-2xl p-6">
          <h3 class="flex items-center gap-2 text-lg font-semibold text-highlighted">
            <UIcon name="i-lucide-eye-off" class="size-5 text-muted" aria-hidden="true" />
            {{ t('ai.privacy.neverTitle') }}
          </h3>
          <ul class="mt-4 space-y-3">
            <li v-for="k in never" :key="k" class="flex gap-3 text-toned">
              <UIcon name="i-lucide-x" class="mt-1 size-5 shrink-0 text-muted" aria-hidden="true" />
              {{ t(`ai.privacy.never.${k}`) }}
            </li>
          </ul>
        </div>
      </div>
    </section>

    <!-- Proveedores. -->
    <section aria-labelledby="ai-providers" class="mx-auto max-w-6xl px-4 py-14 sm:px-6">
      <SectionHeading
        id="ai-providers"
        :eyebrow="t('ai.eyebrow')"
        :title="t('ai.providers.title')"
        :lead="t('ai.providers.lead')"
      />
      <div class="mt-10 grid gap-6 md:grid-cols-2">
        <article
          v-for="(p, i) in ['claude', 'local']"
          :key="p"
          data-reveal
          :style="{ '--rv-i': i }"
          class="surface rounded-2xl p-6"
        >
          <div class="flex items-center gap-3">
            <UIcon
              :name="p === 'claude' ? 'i-lucide-cloud' : 'i-lucide-server-cog'"
              class="size-6 text-primary"
              aria-hidden="true"
            />
            <span class="rounded-full border border-default px-2.5 py-0.5 text-xs text-muted">{{
              t(`ai.providers.${p}.tag`)
            }}</span>
          </div>
          <h3 class="mt-4 text-xl font-semibold text-highlighted">
            {{ t(`ai.providers.${p}.title`) }}
          </h3>
          <p class="mt-2 text-toned">{{ t(`ai.providers.${p}.body`) }}</p>
        </article>
      </div>
    </section>

    <!-- Verificación. -->
    <section aria-labelledby="ai-verify" class="mx-auto max-w-6xl px-4 py-14 sm:px-6">
      <div class="grid gap-10 lg:grid-cols-2 lg:items-center">
        <div>
          <SectionHeading
            id="ai-verify"
            :eyebrow="t('ai.eyebrow')"
            :title="t('ai.verify.title')"
            :lead="t('ai.verify.lead')"
          />
          <ul class="mt-8 space-y-3">
            <li
              v-for="(k, i) in verifyItems"
              :key="k"
              data-reveal
              :style="{ '--rv-i': i }"
              class="flex gap-3 text-toned"
            >
              <UIcon
                name="i-lucide-check"
                class="mt-1 size-5 shrink-0 text-primary"
                aria-hidden="true"
              />
              {{ t(`ai.verify.items.${k}`) }}
            </li>
          </ul>
        </div>
        <div data-reveal="scale" class="surface flex flex-col gap-3 rounded-2xl p-6">
          <span
            class="inline-flex w-fit items-center gap-1.5 rounded-full border border-(--st-online)/40 px-3 py-1.5 font-semibold text-(--st-online)"
          >
            <UIcon name="i-lucide-badge-check" class="size-5" aria-hidden="true" />{{
              t('ai.verify.badges.verified')
            }}
          </span>
          <span
            class="inline-flex w-fit items-center gap-1.5 rounded-full border border-(--st-warning)/40 px-3 py-1.5 font-semibold text-(--st-warning)"
          >
            <UIcon name="i-lucide-badge-alert" class="size-5" aria-hidden="true" />{{
              t('ai.verify.badges.partial')
            }}
          </span>
          <span
            class="inline-flex w-fit items-center gap-1.5 rounded-full border border-default px-3 py-1.5 font-semibold text-toned"
          >
            <UIcon name="i-lucide-flag" class="size-5" aria-hidden="true" />{{
              t('ai.verify.badges.flagged')
            }}
          </span>
        </div>
      </div>
    </section>

    <!-- Control del ISP. -->
    <section aria-labelledby="ai-control" class="mx-auto max-w-6xl px-4 py-14 sm:px-6">
      <SectionHeading id="ai-control" :eyebrow="t('ai.eyebrow')" :title="t('ai.control.title')" />
      <ul class="mt-10 grid gap-x-10 gap-y-8 sm:grid-cols-2 lg:grid-cols-4">
        <li v-for="(c, i) in control" :key="c.key" data-reveal :style="{ '--rv-i': i }">
          <UIcon :name="c.icon" class="size-6 text-primary" aria-hidden="true" />
          <h3 class="mt-3 font-semibold text-highlighted">
            {{ t(`ai.control.items.${c.key}.title`) }}
          </h3>
          <p class="mt-1 text-toned">{{ t(`ai.control.items.${c.key}.body`) }}</p>
        </li>
      </ul>
    </section>

    <!-- Pantalla de ajustes y MCP. -->
    <FeatureRow
      id="ai-settings"
      :eyebrow="t('ai.eyebrow')"
      :title="t('ai.shot.title')"
      :body="t('ai.shot.body')"
      shot="ai"
      :alt="t('product.sections.ai.alt')"
    />
    <section aria-labelledby="ai-mcp" class="mx-auto max-w-6xl px-4 py-6 sm:px-6">
      <div
        data-reveal
        class="surface flex flex-col gap-4 rounded-2xl p-6 sm:flex-row sm:items-start"
      >
        <UIcon name="i-lucide-plug-zap" class="size-7 shrink-0 text-primary" aria-hidden="true" />
        <div>
          <h2 id="ai-mcp" class="text-lg font-semibold text-highlighted">
            {{ t('ai.mcp.title') }}
          </h2>
          <p class="mt-1 text-toned">{{ t('ai.mcp.body') }}</p>
        </div>
      </div>
    </section>

    <FaqList id="ai-faq" :title="t('common.faqTitle')" :items="faq" />
    <CtaBand />
    <LazyFloatingCta hydrate-on-idle />
  </div>
</template>

<style scoped>
/* Enmascarado: el dato real se atenúa y el seudónimo aparece (una vez, al entrar). */
@media (prefers-reduced-motion: no-preference) {
  .mask-demo.rv-in .mask-before {
    animation: mask-dim 0.6s var(--ease-out) 0.5s both;
  }
  .mask-demo.rv-in .mask-arrow {
    animation: mask-arrow 0.5s var(--ease-out) 0.8s backwards;
  }
  .mask-demo.rv-in .mask-after {
    animation: rv-up 0.6s var(--ease-out) 1.1s backwards;
  }
}
@keyframes mask-dim {
  to {
    opacity: 0.55;
  }
}
@keyframes mask-arrow {
  from {
    opacity: 0;
    translate: -8px 0;
  }
}
</style>
