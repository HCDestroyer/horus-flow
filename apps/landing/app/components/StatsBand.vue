<script setup lang="ts">
// Cifras clave con contador animado (app/utils/motion-enhance.ts). El HTML ya trae el valor
// final: sin JavaScript, con reduced motion o si la banda está a la vista al cargar, no cuenta.
// Todas salen de tests/load/REPORT.md y del README del producto.
const { t, locale } = useI18n()
const sep = computed(() => (locale.value === 'en' ? ',' : ' '))
const fmt = (n: number) => String(n).replace(/\B(?=(\d{3})+(?!\d))/g, sep.value)

const items = [
  { key: 'flows', value: 10000, count: true },
  { key: 'lost', value: 0, count: false },
  { key: 'states', value: 8, count: true },
  { key: 'history', value: 90, count: true },
] as const
</script>

<template>
  <section aria-labelledby="stats-title" class="mx-auto max-w-6xl px-4 sm:px-6">
    <h2 id="stats-title" class="sr-only">{{ t('stats.title') }}</h2>
    <dl class="grid grid-cols-2 gap-x-6 gap-y-8 border-y border-default py-10 lg:grid-cols-4">
      <div
        v-for="(it, i) in items"
        :key="it.key"
        data-reveal
        :style="{ '--rv-i': i }"
        class="flex flex-col"
      >
        <dt class="order-2 mt-2 text-[0.95rem] text-muted">{{ t(`stats.${it.key}.label`) }}</dt>
        <dd class="order-1 flex items-baseline gap-2 text-highlighted">
          <span
            class="text-4xl font-bold tracking-tight sm:text-5xl"
            :data-count="it.count ? it.value : undefined"
            :data-count-sep="it.count ? sep : undefined"
            >{{ fmt(it.value) }}</span
          >
          <span class="text-lg font-semibold text-toned">{{ t(`stats.${it.key}.unit`) }}</span>
        </dd>
      </div>
    </dl>
    <p class="mt-3 text-sm text-muted">{{ t('stats.note') }}</p>
  </section>
</template>
