<script setup lang="ts">
import type { RemoteDestination } from '~~/types/api'

/** Cambiar las credenciales de un destino (write-only; reemplazan a las guardadas). */
const props = defineProps<{ destination: RemoteDestination }>()
const emit = defineEmits<{ saved: [destination: RemoteDestination] }>()
const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const toast = useToast()

function saved(d: RemoteDestination) {
  toast.add({
    title: t('platform.storage.credentials.saved', { name: props.destination.name }),
    description: t('platform.storage.credentials.savedHint'),
    icon: 'i-lucide-key-round',
    color: 'success',
  })
  open.value = false
  emit('saved', d)
}
</script>

<template>
  <UModal
    v-model:open="open"
    :title="t('platform.storage.credentials.title', { name: destination.name })"
    :description="destinationUrl(destination)"
    data-testid="dst-credentials-modal"
  >
    <template #body>
      <RemoteDestinationCredentialsForm :destination="destination" @saved="saved" />
    </template>
  </UModal>
</template>
