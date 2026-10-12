<script setup lang="ts">
/**
 * Confirmación de una baja (nodo o router): qué pasará, el botón destructivo y, si el backend
 * la rechaza, por qué y qué hacer (en el cuerpo, sin cerrar el diálogo). "Cancelar" lleva el
 * foco inicial para que Enter no borre por accidente.
 */
withDefaults(
  defineProps<{
    title: string
    description?: string
    confirmLabel: string
    loading?: boolean
    /** Motivo del rechazo (p. ej. `SITE_NOT_EMPTY`), ya traducido. */
    blocked?: string | null
  }>(),
  { description: undefined, loading: false, blocked: null },
)
const emit = defineEmits<{ confirm: [] }>()
const open = defineModel<boolean>('open', { default: false })
const { t } = useI18n()
</script>

<template>
  <UModal v-model:open="open" :title="title" :description="description" data-testid="delete-modal">
    <template #body>
      <div class="flex flex-col gap-4">
        <slot />
        <UAlert
          v-if="blocked"
          color="error"
          variant="subtle"
          icon="i-lucide-circle-alert"
          :title="blocked"
          data-testid="delete-blocked"
        >
          <template v-if="$slots.blocked" #description>
            <slot name="blocked" />
          </template>
        </UAlert>
      </div>
    </template>
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton
          color="neutral"
          variant="outline"
          :label="t('common.cancel')"
          autofocus
          @click="open = false"
        />
        <UButton
          color="error"
          icon="i-lucide-trash-2"
          :loading="loading"
          :label="confirmLabel"
          data-testid="delete-confirm"
          @click="emit('confirm')"
        />
      </div>
    </template>
  </UModal>
</template>
