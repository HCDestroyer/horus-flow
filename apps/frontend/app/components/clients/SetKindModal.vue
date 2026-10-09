<script setup lang="ts">
import type { CustomerDetail } from '~~/types/api'

/**
 * Cambiar tipo (frontend.md §8.1): formulario corto con motivo obligatorio. El tipo manual
 * queda bloqueado (candado) y el scoring no lo pisa. Ante `412` (el tipo cambió mientras se
 * editaba) se muestra el valor actual y se permite reintentar sobre él.
 */
const props = defineProps<{ customer: CustomerDetail }>()
const emit = defineEmits<{ saved: [customer: CustomerDetail] }>()
const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()

const kind = ref<CustomerDetail['kind']>('commercial')
const reason = ref('')
const touched = ref(false)
const saving = ref(false)
const conflict = ref<CustomerDetail | null>(null)
const error = shallowRef<unknown>(null)
/** Versión sobre la que se guarda (la del 412 si hubo conflicto). */
const base = ref<CustomerDetail>(props.customer)

watch(open, (o) => {
  if (!o) return
  base.value = props.customer
  kind.value = props.customer.kind === 'commercial' ? 'residential' : 'commercial'
  reason.value = ''
  touched.value = false
  conflict.value = null
  error.value = null
})

const reasonError = computed(() =>
  touched.value && !reason.value.trim() ? t('clients.setKind.reasonRequired') : undefined,
)

async function save() {
  touched.value = true
  if (!reason.value.trim()) return
  saving.value = true
  error.value = null
  try {
    const updated = await unwrap(
      $api.POST('/customers/{customer_id}/set-kind', {
        params: {
          path: { customer_id: base.value.id },
          header: { 'If-Match': ifMatch(base.value.version) },
        },
        body: { kind: kind.value, reason: reason.value.trim() },
      }),
    )
    open.value = false
    toast.add({ title: t('clients.setKind.done'), icon: 'i-lucide-check', color: 'success' })
    emit('saved', updated)
  } catch (e) {
    if (e instanceof ApiError && e.status === 412 && e.problem.current) {
      conflict.value = e.problem.current as unknown as CustomerDetail
      base.value = conflict.value
      emit('saved', conflict.value)
    } else {
      error.value = e
    }
  } finally {
    saving.value = false
  }
}

const options = computed(() => [
  { label: t('clients.kind.residential'), value: 'residential', icon: 'i-lucide-house' },
  { label: t('clients.kind.commercial'), value: 'commercial', icon: 'i-lucide-building-2' },
])
</script>

<template>
  <UModal
    v-model:open="open"
    :title="t('clients.setKind.title')"
    :description="t('clients.setKind.description')"
  >
    <template #body>
      <form id="set-kind-form" class="flex flex-col gap-4" @submit.prevent="save">
        <UAlert
          v-if="conflict"
          color="warning"
          variant="subtle"
          icon="i-lucide-git-compare"
          :title="t('clients.setKind.conflictTitle')"
          data-testid="kind-conflict"
        >
          <template #description>
            <p>{{ t('clients.setKind.conflictBody') }}</p>
            <p class="mt-1">
              <ClientKindBadge
                :kind="conflict.kind"
                :source="conflict.kind_source"
                :locked="conflict.kind_locked"
                :confidence="conflict.kind_confidence"
              />
            </p>
          </template>
        </UAlert>
        <UFormField :label="t('clients.setKind.kind')" required>
          <URadioGroup v-model="kind" :items="options" orientation="horizontal" />
        </UFormField>
        <UFormField
          :label="t('clients.setKind.reason')"
          :hint="t('clients.setKind.reasonHint')"
          :error="reasonError"
          required
        >
          <UTextarea
            v-model="reason"
            :rows="3"
            autoresize
            class="w-full"
            :placeholder="t('clients.setKind.reasonPlaceholder')"
            data-testid="kind-reason"
            @blur="touched = true"
          />
        </UFormField>
        <p class="text-muted flex gap-1.5 text-sm">
          <UIcon name="i-lucide-lock" class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
          {{ t('clients.setKind.lockNote') }}
        </p>
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
        <UButton
          type="submit"
          form="set-kind-form"
          :loading="saving"
          :label="conflict ? t('clients.setKind.retry') : t('clients.setKind.submit')"
          data-testid="kind-submit"
        />
      </div>
    </template>
  </UModal>
</template>
