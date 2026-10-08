<script setup lang="ts">
/**
 * Carga con la forma final (frontend.md §9.1, HIG loading: "Show something as soon as
 * possible"). Nunca un spinner de página completa.
 */
withDefaults(defineProps<{ rows?: number; label?: string }>(), { rows: 3, label: undefined })

const { t } = useI18n()
</script>

<template>
  <div
    role="status"
    aria-live="polite"
    :aria-label="label ?? t('states.loading')"
    class="space-y-3"
  >
    <span class="sr-only">{{ label ?? t('states.loading') }}</span>
    <div v-for="i in rows" :key="i" class="flex items-center gap-3">
      <USkeleton class="size-5 rounded-full" />
      <USkeleton class="h-4 flex-1" :style="{ maxWidth: `${70 - i * 8}%` }" />
      <USkeleton class="h-5 w-20 rounded-full" />
    </div>
  </div>
</template>
