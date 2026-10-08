<script setup lang="ts">
/** Error en contexto (frontend.md §9.3): qué pasó, qué hacer, "Reintentar" y detalle plegado. */
const props = withDefaults(
  defineProps<{
    error: unknown
    /** Título propio; si no, se deriva del error. */
    title?: string
    hint?: string
  }>(),
  { title: undefined, hint: undefined },
)

const emit = defineEmits<{ retry: [] }>()

const { t } = useI18n()
const described = computed(() => describeError(props.error))
const message = computed(() => t(described.value.messageKey, described.value.params ?? {}))
const hasDetail = computed(() => !!(described.value.code || described.value.traceId))
</script>

<template>
  <UAlert
    color="error"
    variant="subtle"
    icon="i-lucide-circle-alert"
    :title="title ?? message"
    role="alert"
    :ui="{ description: 'space-y-2' }"
  >
    <template #description>
      <p v-if="title">{{ message }}</p>
      <p v-if="hint">{{ hint }}</p>
      <details v-if="hasDetail" class="text-xs">
        <summary class="cursor-pointer select-none">{{ t('states.technicalDetail') }}</summary>
        <dl class="mt-1 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
          <template v-if="described.code">
            <dt>{{ t('states.code') }}</dt>
            <dd class="font-mono break-all">{{ described.code }}</dd>
          </template>
          <template v-if="described.traceId">
            <dt>{{ t('states.traceId') }}</dt>
            <dd class="font-mono break-all">{{ described.traceId }}</dd>
          </template>
        </dl>
      </details>
    </template>
    <template v-if="described.retryable" #actions>
      <UButton
        color="error"
        variant="outline"
        size="sm"
        icon="i-lucide-rotate-ccw"
        :label="t('states.retry')"
        @click="emit('retry')"
      />
    </template>
  </UAlert>
</template>
