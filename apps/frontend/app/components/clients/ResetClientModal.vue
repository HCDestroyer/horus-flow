<script setup lang="ts">
import type { CustomerDetail } from '~~/types/api'

/**
 * Reiniciar cliente (frontend.md §8.1, §11): destructivo (borra alias y notas), con
 * confirmación, botón nombrado por la acción y Cancelar con foco. Para cuando la IP pasó a
 * otra persona: el tipo vuelve al defecto y el historial registra el reinicio.
 */
const props = defineProps<{ customer: CustomerDetail }>()
const emit = defineEmits<{ done: [customer: CustomerDetail] }>()
const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
const reason = ref('')
const touched = ref(false)
const saving = ref(false)
const error = shallowRef<unknown>(null)

watch(open, (o) => {
  if (o) {
    reason.value = ''
    touched.value = false
    error.value = null
  }
})

async function confirm() {
  touched.value = true
  if (!reason.value.trim()) return
  saving.value = true
  try {
    const updated = await unwrap(
      $api.POST('/customers/{customer_id}/reset', {
        params: {
          path: { customer_id: props.customer.id },
          header: { 'If-Match': ifMatch(props.customer.version) },
        },
        body: { reason: reason.value.trim() },
      }),
    )
    open.value = false
    toast.add({ title: t('clients.reset.done'), icon: 'i-lucide-check', color: 'success' })
    emit('done', updated)
  } catch (e) {
    error.value = e
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <UModal v-model:open="open" :title="t('clients.reset.title')">
    <template #body>
      <form id="reset-form" class="flex flex-col gap-4" @submit.prevent="confirm">
        <p class="text-default">{{ t('clients.reset.body') }}</p>
        <ul class="text-muted list-disc space-y-1 ps-5 text-sm">
          <li>{{ t('clients.reset.effectAlias') }}</li>
          <li>{{ t('clients.reset.effectKind') }}</li>
          <li>{{ t('clients.reset.effectHistory') }}</li>
        </ul>
        <UFormField
          :label="t('clients.reset.reason')"
          :error="touched && !reason.trim() ? t('clients.reset.reasonRequired') : undefined"
          required
        >
          <UInput
            v-model="reason"
            class="w-full"
            :placeholder="t('clients.reset.reasonPlaceholder')"
            data-testid="reset-reason"
            @blur="touched = true"
          />
        </UFormField>
        <ErrorState v-if="error" :error="error" @retry="confirm" />
      </form>
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
          type="submit"
          form="reset-form"
          color="error"
          icon="i-lucide-rotate-ccw"
          :loading="saving"
          :label="t('clients.reset.submit')"
          data-testid="reset-submit"
        />
      </div>
    </template>
  </UModal>
</template>
