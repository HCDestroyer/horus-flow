<script setup lang="ts">
// Monitoreo de routers (/monitoreo, /en/monitoring): métricas SNMPv3 + ICMP, los ocho estados
// con sus razones, histéresis, ficha del router, vistas del NOC y umbrales.
definePageMeta({ viewTransition: true })
usePageSeo('monitoring', 'meta.monitoringTitle', 'meta.monitoringDescription')
usePageLd('monitoring', 'nav.monitoring', 'meta.monitoringDescription')

const { t } = useI18n()
const localePath = useLocalePath()

const metrics = [
  { key: 'cpu', icon: 'i-lucide-cpu' },
  { key: 'memory', icon: 'i-lucide-memory-stick' },
  { key: 'temperature', icon: 'i-lucide-thermometer' },
  { key: 'uptime', icon: 'i-lucide-timer' },
  { key: 'interfaces', icon: 'i-lucide-cable' },
  { key: 'latency', icon: 'i-lucide-activity' },
]
const states = [
  'online',
  'warning',
  'critical',
  'degraded',
  'offline',
  'stale',
  'unknown',
  'maintenance',
]
const views = [
  { key: 'health', icon: 'i-lucide-heart-pulse' },
  { key: 'wall', icon: 'i-lucide-maximize' },
  { key: 'widget', icon: 'i-lucide-layout-dashboard' },
  { key: 'template', icon: 'i-lucide-monitor' },
]
</script>

<template>
  <div>
    <PageIntro
      :crumb="t('nav.monitoring')"
      :eyebrow="t('monitoring.eyebrow')"
      :title="t('monitoring.title')"
      :lead="t('monitoring.lead')"
    >
      <RouterHealthGrid />
    </PageIntro>

    <section aria-labelledby="mon-metrics" class="mx-auto max-w-6xl px-4 py-14 sm:px-6">
      <SectionHeading
        id="mon-metrics"
        :eyebrow="t('monitoring.eyebrow')"
        :title="t('monitoring.metrics.title')"
        :lead="t('monitoring.metrics.lead')"
      />
      <ul class="mt-10 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <li
          v-for="(m, i) in metrics"
          :key="m.key"
          data-reveal
          :style="{ '--rv-i': i % 3 }"
          class="surface flex gap-4 rounded-2xl p-5"
        >
          <UIcon :name="m.icon" class="mt-0.5 size-6 shrink-0 text-primary" aria-hidden="true" />
          <div>
            <h3 class="font-semibold text-highlighted">
              {{ t(`monitoring.metrics.items.${m.key}.title`) }}
            </h3>
            <p class="mt-1 text-toned">{{ t(`monitoring.metrics.items.${m.key}.body`) }}</p>
          </div>
        </li>
      </ul>
    </section>

    <section aria-labelledby="mon-states" class="mx-auto max-w-6xl px-4 py-14 sm:px-6">
      <SectionHeading
        id="mon-states"
        :eyebrow="t('monitoring.eyebrow')"
        :title="t('monitoring.states.title')"
        :lead="t('monitoring.states.lead')"
      />
      <ul class="mt-10 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <li
          v-for="(s, i) in states"
          :key="s"
          :data-state="s"
          data-reveal
          :style="{ '--rv-i': i % 4 }"
          class="surface state-card rounded-2xl p-5"
        >
          <p class="flex items-center gap-2 font-semibold text-highlighted">
            <span class="st-dot" aria-hidden="true" />
            {{ t(`monitoring.states.items.${s}.label`) }}
          </p>
          <p class="mt-2 text-[0.95rem] text-toned">{{ t(`monitoring.states.items.${s}.body`) }}</p>
        </li>
      </ul>
    </section>

    <section aria-labelledby="mon-hyst" class="mx-auto max-w-6xl px-4 py-14 sm:px-6">
      <div class="grid gap-10 lg:grid-cols-[1fr_1.4fr] lg:items-center">
        <SectionHeading
          id="mon-hyst"
          :eyebrow="t('monitoring.eyebrow')"
          :title="t('monitoring.hysteresis.title')"
          :lead="t('monitoring.hysteresis.body')"
        />
        <HysteresisChart />
      </div>
    </section>

    <FeatureRow
      id="mon-detail"
      reverse
      :eyebrow="t('monitoring.eyebrow')"
      :title="t('monitoring.detail.title')"
      :body="t('monitoring.detail.body')"
    >
      <template #visual>
        <RouterDetailMock />
      </template>
    </FeatureRow>

    <section aria-labelledby="mon-views" class="mx-auto max-w-6xl px-4 py-14 sm:px-6">
      <SectionHeading
        id="mon-views"
        :eyebrow="t('monitoring.eyebrow')"
        :title="t('monitoring.views.title')"
      />
      <ul class="mt-10 grid gap-x-10 gap-y-8 sm:grid-cols-2 lg:grid-cols-4">
        <li v-for="(v, i) in views" :key="v.key" data-reveal :style="{ '--rv-i': i }">
          <UIcon :name="v.icon" class="size-6 text-primary" aria-hidden="true" />
          <h3 class="mt-3 font-semibold text-highlighted">
            {{ t(`monitoring.views.items.${v.key}.title`) }}
          </h3>
          <p class="mt-1 text-toned">{{ t(`monitoring.views.items.${v.key}.body`) }}</p>
        </li>
      </ul>
      <div
        data-reveal
        class="surface mt-10 flex flex-col gap-4 rounded-2xl p-6 sm:flex-row sm:items-start"
      >
        <UIcon
          name="i-lucide-sliders-horizontal"
          class="size-7 shrink-0 text-primary"
          aria-hidden="true"
        />
        <div>
          <h3 class="text-lg font-semibold text-highlighted">
            {{ t('monitoring.thresholds.title') }}
          </h3>
          <p class="mt-1 text-toned">{{ t('monitoring.thresholds.body') }}</p>
          <div class="mt-3 flex flex-wrap gap-x-6">
            <NuxtLink
              :to="localePath('/inteligencia-artificial')"
              class="arrow-link inline-flex min-h-11 items-center gap-1.5 font-semibold text-primary"
            >
              {{ t('product.sections.ai.cta') }}
              <UIcon name="i-lucide-arrow-right" class="arrow size-4" aria-hidden="true" />
            </NuxtLink>
            <NuxtLink
              :to="localePath('/pantallas-noc')"
              class="arrow-link inline-flex min-h-11 items-center gap-1.5 font-semibold text-primary"
            >
              {{ t('product.sections.noc.cta') }}
              <UIcon name="i-lucide-arrow-right" class="arrow size-4" aria-hidden="true" />
            </NuxtLink>
          </div>
        </div>
      </div>
    </section>

    <CtaBand />
    <LazyFloatingCta hydrate-on-idle />
  </div>
</template>

<style scoped>
/* Cada estado con un filo de su color (además del punto y el nombre). */
.state-card {
  box-shadow: inset 3px 0 0 var(--st);
}
</style>
