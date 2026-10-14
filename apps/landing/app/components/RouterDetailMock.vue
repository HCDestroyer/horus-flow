<script setup lang="ts">
// Ilustración de la ficha de un router (datos simulados): métricas con su mini gráfica,
// interfaces y "Sondear ahora" (aquí no es un botón: es una ilustración).
const { t } = useI18n()

function spark(values: number[]) {
  const max = 100
  return values
    .map(
      (v, i) =>
        `${((i / (values.length - 1)) * 120).toFixed(1)},${(36 - (v / max) * 32).toFixed(1)}`,
    )
    .join(' ')
}

const metrics = [
  {
    key: 'cpu',
    label: 'CPU',
    value: '23 %',
    valueEn: '23%',
    series: [30, 28, 35, 22, 26, 40, 31, 24, 20, 25, 23],
  },
  {
    key: 'memory',
    label: null,
    value: '41 %',
    valueEn: '41%',
    series: [38, 39, 39, 40, 41, 40, 41, 42, 41, 41, 41],
  },
  {
    key: 'temperature',
    label: null,
    value: '47 °C',
    valueEn: '47 °C',
    series: [44, 45, 45, 46, 48, 49, 48, 47, 47, 46, 47],
  },
  {
    key: 'latency',
    label: null,
    value: '4 ms',
    valueEn: '4 ms',
    series: [8, 6, 5, 9, 4, 5, 6, 4, 5, 4, 4],
  },
]
const { locale } = useI18n()
const interfaces = [
  { name: 'sfp-sfpplus1', up: true, rate: '2,1 Gbit/s', rateEn: '2.1 Gbit/s' },
  { name: 'ether1', up: true, rate: '812 Mbit/s', rateEn: '812 Mbit/s' },
  { name: 'ether5', up: false, rate: '—', rateEn: '—' },
]
</script>

<template>
  <figure class="surface overflow-hidden rounded-2xl" data-reveal>
    <figcaption class="sr-only">{{ t('monitoring.detail.label') }}</figcaption>
    <div
      class="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-default px-4 py-3 sm:px-5"
    >
      <span class="font-mono font-semibold text-highlighted">{{
        t('monitoring.detail.router')
      }}</span>
      <span class="text-sm text-muted">{{ t('monitoring.detail.node') }}</span>
      <span
        data-state="online"
        class="flex items-center gap-1.5 text-sm font-semibold text-highlighted"
      >
        <span class="st-dot" aria-hidden="true" />{{ t('monitoring.states.items.online.label') }}
      </span>
      <span
        class="ml-auto inline-flex items-center gap-1.5 rounded-lg border border-default px-2.5 py-1 text-sm text-toned"
        aria-hidden="true"
      >
        <UIcon name="i-lucide-refresh-cw" class="size-3.5" />{{ t('monitoring.detail.pollNow') }}
      </span>
    </div>
    <div class="grid grid-cols-2 gap-px bg-(--ui-border) sm:grid-cols-4">
      <div v-for="m in metrics" :key="m.key" class="bg-(--surface) px-4 py-3">
        <p class="text-xs text-muted">
          {{ m.label ?? t(`monitoring.metrics.items.${m.key}.title`) }}
        </p>
        <p class="text-lg font-semibold text-highlighted tabular">
          {{ locale === 'en' ? m.valueEn : m.value }}
        </p>
        <svg
          viewBox="0 0 120 40"
          class="mt-1 block h-8 w-full"
          aria-hidden="true"
          focusable="false"
          preserveAspectRatio="none"
        >
          <polyline
            :points="spark(m.series)"
            fill="none"
            stroke="var(--ui-primary)"
            stroke-width="1.6"
            vector-effect="non-scaling-stroke"
            stroke-linejoin="round"
          />
        </svg>
      </div>
    </div>
    <div class="px-4 py-3 sm:px-5">
      <p class="flex items-center justify-between text-xs text-muted">
        <span>{{ t('monitoring.detail.interfaces') }}</span
        ><span>{{ t('monitoring.detail.uptime') }}</span>
      </p>
      <ul class="mt-2 divide-y divide-(--ui-border) text-sm">
        <li v-for="i in interfaces" :key="i.name" class="flex items-center gap-2 py-1.5">
          <span class="min-w-0 flex-1 truncate font-mono text-toned">{{ i.name }}</span>
          <span
            :data-state="i.up ? 'online' : 'offline'"
            class="flex items-center gap-1.5 text-muted"
          >
            <span class="st-dot" aria-hidden="true" />{{
              i.up ? t('monitoring.detail.up') : t('monitoring.detail.down')
            }}
          </span>
          <span class="w-24 text-right text-highlighted tabular">{{
            locale === 'en' ? i.rateEn : i.rate
          }}</span>
        </li>
      </ul>
    </div>
  </figure>
</template>
