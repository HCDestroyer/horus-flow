<script setup lang="ts">
// Diagrama router → WireGuard → Horus → NOC. En md+ es una animación neón (MotionVideo, con
// nombre accesible y el SVG de siempre como respaldo); la misma información va además como
// lista ordenada (visible), para lectores de pantalla y para móviles: en pantallas estrechas
// la animación no se carga y queda la lista, que ya es el diagrama en vertical.
const { t } = useI18n()
const steps = [
  { key: 'router', icon: 'i-lucide-router' },
  { key: 'tunnel', icon: 'i-lucide-lock-keyhole' },
  { key: 'horus', icon: 'i-lucide-server' },
  { key: 'noc', icon: 'i-lucide-monitor' },
] as const
const X = [20, 300, 580, 860]
</script>

<template>
  <div>
    <!--
      Escritorio y tableta: animación neón (HowItWorks, apps/landing-motion), con las cuatro
      columnas alineadas con la lista de debajo. El diagrama SVG queda como respaldo si el
      póster no carga. En móvil no se carga nada: la lista vertical ya es el diagrama.
    -->
    <div class="hidden md:block">
      <MotionVideo clip="how-it-works" :label="`${t('how.diagramTitle')}. ${t('how.diagramDesc')}`">
        <template #fallback>
          <svg
            viewBox="0 0 1100 170"
            aria-hidden="true"
            focusable="false"
            class="block h-full w-full"
          >
            <defs>
              <marker
                id="arrow"
                viewBox="0 0 10 10"
                refX="9"
                refY="5"
                markerWidth="7"
                markerHeight="7"
                orient="auto-start-reverse"
              >
                <path d="M0 0L10 5L0 10z" fill="var(--ui-text-muted)" />
              </marker>
            </defs>
            <g aria-hidden="true">
              <!-- Túnel: la conexión router → Horus va "dentro" de WireGuard. -->
              <rect
                x="200"
                y="60"
                width="380"
                height="50"
                rx="25"
                fill="none"
                stroke="var(--ui-primary)"
                stroke-width="2"
                stroke-dasharray="6 5"
              />
              <line
                x1="240"
                y1="85"
                x2="296"
                y2="85"
                stroke="var(--ui-text-muted)"
                stroke-width="2"
                marker-end="url(#arrow)"
              />
              <line
                x1="520"
                y1="85"
                x2="576"
                y2="85"
                stroke="var(--ui-text-muted)"
                stroke-width="2"
                marker-end="url(#arrow)"
              />
              <line
                x1="800"
                y1="85"
                x2="856"
                y2="85"
                stroke="var(--ui-text-muted)"
                stroke-width="2"
                marker-end="url(#arrow)"
              />
              <g v-for="(s, i) in steps" :key="s.key" :transform="`translate(${X[i]} 20)`">
                <rect
                  width="220"
                  height="130"
                  rx="16"
                  :fill="s.key === 'tunnel' ? 'var(--ui-bg)' : 'var(--surface)'"
                  :stroke="s.key === 'horus' ? 'var(--ui-primary)' : 'var(--ui-border-accented)'"
                  :stroke-width="s.key === 'horus' ? 2 : 1"
                />
                <text
                  x="110"
                  y="58"
                  text-anchor="middle"
                  font-size="22"
                  font-weight="700"
                  fill="var(--ui-text-highlighted)"
                >
                  {{ t(`how.steps.${s.key}.title`) }}
                </text>
                <text
                  x="110"
                  y="88"
                  text-anchor="middle"
                  font-size="16"
                  fill="var(--ui-text-muted)"
                >
                  {{ t(`how.steps.${s.key}.sub`) }}
                </text>
              </g>
            </g>
          </svg>
        </template>
      </MotionVideo>
    </div>

    <ol class="mt-8 grid gap-4 md:grid-cols-4" :aria-label="t('how.diagramTitle')">
      <li v-for="(s, i) in steps" :key="s.key" class="relative flex gap-4 md:block">
        <div class="flex flex-col items-center md:hidden">
          <span
            class="flex size-11 items-center justify-center rounded-full border border-default bg-(--surface)"
          >
            <UIcon :name="s.icon" class="size-5 text-primary" aria-hidden="true" />
          </span>
          <span
            v-if="i < steps.length - 1"
            class="mt-1 w-px flex-1 bg-(--ui-border-accented)"
            aria-hidden="true"
          />
        </div>
        <div class="pb-2">
          <h3 class="font-semibold text-highlighted">
            {{ t(`how.steps.${s.key}.title`) }}
            <span class="font-normal text-muted"> · {{ t(`how.steps.${s.key}.sub`) }}</span>
          </h3>
          <p class="mt-1 text-toned">{{ t(`how.steps.${s.key}.body`) }}</p>
        </div>
      </li>
    </ol>
  </div>
</template>
