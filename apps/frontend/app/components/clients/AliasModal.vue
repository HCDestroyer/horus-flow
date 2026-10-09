<script setup lang="ts">
import type { CustomerDetail } from '~~/types/api'

/** Alias y notas del cliente (dato personal que pone el ISP; reversible, sin confirmación). */
const props = defineProps<{ customer: CustomerDetail }>()
const emit = defineEmits<{ saved: [customer: CustomerDetail] }>()
const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
const alias = ref('')
const notes = ref('')
const saving = ref(false)
const error = shallowRef<unknown>(null)

watch(open, (o) => {
  if (!o) return
  alias.value = props.customer.alias ?? ''
  notes.value = props.customer.notes ?? ''
  error.value = null
})

async function save() {
  saving.value = true
  try {
    const updated = await unwrap(
      $api.PATCH('/customers/{customer_id}', {
        params: {
          path: { customer_id: props.customer.id },
          header: { 'If-Match': ifMatch(props.customer.version) },
        },
        body: { alias: alias.value.trim() || null, notes: notes.value.trim() || null },
      }),
    )
    open.value = false
    toast.add({ title: t('clients.alias.done'), icon: 'i-lucide-check', color: 'success' })
    emit('saved', updated)
  } catch (e) {
    error.value = e
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <UModal
    v-model:open="open"
    :title="t('clients.alias.title')"
    :description="t('clients.alias.description')"
  >
    <template #body>
      <form id="alias-form" class="flex flex-col gap-4" @submit.prevent="save">
        <UFormField :label="t('clients.alias.alias')">
          <UInput
            v-model="alias"
            class="w-full"
            maxlength="80"
            :placeholder="t('clients.alias.aliasPlaceholder')"
          />
        </UFormField>
        <UFormField :label="t('clients.alias.notes')">
          <UTextarea v-model="notes" :rows="3" autoresize class="w-full" maxlength="500" />
        </UFormField>
        <ErrorState v-if="error" :error="error" @retry="save" />
      </form>
    </template>
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton
          color="neutral"
          variant="ghost"
          :label="t('common.cancel')"
          @click="open = false"
        />
        <UButton type="submit" form="alias-form" :loading="saving" :label="t('common.save')" />
      </div>
    </template>
  </UModal>
</template>
