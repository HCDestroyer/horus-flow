<script setup lang="ts">
import type { WidgetPlaceholder } from '~/widgets/define'

/**
 * Esqueleto con la forma final del widget (frontend.md §9.1; HIG widgets › placeholders que
 * se reconocen). La forma la declara el manifiesto (`placeholder`).
 */
defineProps<{ shape: WidgetPlaceholder }>()
const { t } = useI18n()
</script>

<template>
  <div role="status" class="flex size-full min-h-0 flex-col gap-3" data-testid="widget-loading">
    <span class="sr-only">{{ t('states.loading') }}</span>
    <template v-if="shape === 'kpi' || shape === 'sparkline'">
      <USkeleton class="h-[calc(var(--w-kpi)*0.9)] w-1/2" />
      <USkeleton class="h-3 w-1/3" />
      <USkeleton v-if="shape === 'sparkline'" class="mt-auto h-8 w-full" />
    </template>
    <template v-else-if="shape === 'timeseries'">
      <div class="flex flex-1 items-end gap-1.5">
        <USkeleton
          v-for="i in 24"
          :key="i"
          class="flex-1 rounded-sm"
          :style="{ height: `${30 + ((i * 37) % 60)}%` }"
        />
      </div>
    </template>
    <template v-else-if="shape === 'bars'">
      <div v-for="i in 5" :key="i" class="flex items-center gap-3">
        <USkeleton class="h-3 w-1/4" />
        <USkeleton class="h-3" :style="{ width: `${70 - i * 10}%` }" />
      </div>
    </template>
    <template v-else-if="shape === 'header'">
      <div class="flex items-center gap-4">
        <USkeleton class="h-6 w-40" />
        <USkeleton class="ms-auto h-6 w-24" />
      </div>
    </template>
    <template v-else>
      <div v-for="i in 5" :key="i" class="flex items-center gap-3">
        <USkeleton class="size-4 rounded-full" />
        <USkeleton class="h-3 flex-1" :style="{ maxWidth: `${80 - i * 8}%` }" />
        <USkeleton class="h-3 w-12" />
      </div>
    </template>
  </div>
</template>
