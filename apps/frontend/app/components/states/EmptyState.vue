<script setup lang="ts">
import type { ButtonProps } from '@nuxt/ui'

/**
 * Vacío con título que describe la situación y siguiente acción (frontend.md §9.2,
 * HIG writing: "Provide clear next steps on any blank screens").
 */
withDefaults(
  defineProps<{
    title: string
    description?: string
    icon?: string
    actions?: ButtonProps[]
  }>(),
  { description: undefined, icon: 'i-lucide-inbox', actions: () => [] },
)
</script>

<template>
  <UEmpty
    :icon="icon"
    :title="title"
    :description="description"
    :actions="actions"
    variant="naked"
    :ui="{
      title: 'text-base font-semibold text-highlighted',
      description: 'text-muted max-w-prose',
    }"
  />
</template>
