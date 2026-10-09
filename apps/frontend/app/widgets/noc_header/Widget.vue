<script setup lang="ts">
import type { WidgetViewProps } from '../props'

/**
 * `noc_header` — ¿Qué hora es, qué ISP es y cuán frescos son estos datos? Siempre muestra
 * "Actualizado hace N s" con el dato más reciente del dashboard (§7.6). Baja luminancia.
 * En pantallas estrechas pasa a dos líneas (móvil); si la banda tiene altura fija (mural) y
 * no cabe, se ocultan por orden la fecha, el nombre del dashboard y "En vivo"; la hora y la
 * frescura nunca.
 */
const props = defineProps<WidgetViewProps>()
const { t } = useI18n()
const ctx = inject(DASHBOARD_CONTEXT)!
const now = useNow()

const config = computed(() => props.widget.config as Record<string, boolean | undefined>)
const clock = computed(() =>
  new Intl.DateTimeFormat('es-ES', { hour: '2-digit', minute: '2-digit', hour12: false }).format(
    now.value,
  ),
)
const date = computed(() =>
  new Intl.DateTimeFormat('es-ES', { weekday: 'long', day: 'numeric', month: 'long' }).format(
    now.value,
  ),
)
</script>

<template>
  <div
    class="flex h-full min-w-0 flex-wrap content-center items-center gap-x-[calc(var(--w-gap)*2)] gap-y-1"
    data-testid="noc-header"
  >
    <div class="flex min-w-0 flex-[1_1_auto] items-baseline gap-x-[0.6em]">
      <span
        v-if="config.show_tenant !== false"
        class="w-title text-highlighted truncate font-semibold"
        data-testid="noc-header-tenant"
        >{{ ctx.tenantName.value }}</span
      >
      <span class="w-label text-muted truncate" data-fit-optional="2">{{
        ctx.dashboard.name
      }}</span>
    </div>

    <div
      class="ms-auto flex min-w-0 flex-wrap items-center justify-end gap-x-[calc(var(--w-gap)*2)] gap-y-1"
    >
      <span
        v-if="ctx.live.value"
        class="w-meta text-muted inline-flex items-center gap-[0.35em] whitespace-nowrap"
        data-fit-optional="3"
      >
        <UIcon name="i-lucide-radio" class="text-success size-[1.1em]" aria-hidden="true" />
        {{ t('widgets.noc_header.live') }}
      </span>
      <span class="w-meta text-muted whitespace-nowrap" data-testid="noc-header-updated">
        <template v-if="ctx.lastUpdate.value">
          {{ t('widgets.noc_header.updated') }}
          <FreshnessLabel
            :at="ctx.lastUpdate.value"
            :expected-seconds="ctx.dashboard.refresh_seconds"
            class="ms-0.5 align-baseline"
          />
        </template>
        <template v-else>{{ t('widgets.noc_header.waiting') }}</template>
      </span>
      <time
        v-if="config.show_clock !== false"
        class="text-default flex items-baseline gap-[0.5em] whitespace-nowrap tabular"
        :datetime="new Date(now).toISOString()"
      >
        <span class="w-meta text-muted first-letter:uppercase" data-fit-optional="1">{{
          date
        }}</span>
        <span class="w-title font-semibold">{{ clock }}</span>
      </time>
    </div>
  </div>
</template>
