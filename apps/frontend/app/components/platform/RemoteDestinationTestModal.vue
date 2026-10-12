<script setup lang="ts">
import type { RemoteDestination, RemoteDestinationTestResult } from '~~/types/api'

/**
 * Probar la conexión de un destino desde su tarjeta. Si la prueba sale bien y el destino está
 * desactivado pero ya puede activarse (credenciales + huella), ofrece activarlo aquí mismo.
 */
const props = defineProps<{ destination: RemoteDestination }>()
const emit = defineEmits<{ changed: []; enable: [destination: RemoteDestination] }>()
const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const latest = ref<RemoteDestination | null>(null)
const ok = ref(false)

watch(
  open,
  (o) => {
    if (!o) return
    latest.value = null
    ok.value = false
  },
  { immediate: true },
)

const current = computed(() => latest.value ?? props.destination)

function result(r: RemoteDestinationTestResult) {
  if (r.destination) latest.value = r.destination
  ok.value = r.ok
  emit('changed')
}
</script>

<template>
  <UModal
    v-model:open="open"
    :title="t('platform.storage.test.title', { name: destination.name })"
    :description="destinationUrl(destination)"
    :ui="{ content: 'sm:max-w-2xl' }"
    data-testid="dst-test-modal"
  >
    <template #body>
      <RemoteDestinationTestPanel :destination="current" @result="result" />
    </template>
    <template #footer>
      <div class="flex w-full flex-wrap justify-end gap-2">
        <UButton
          color="neutral"
          variant="ghost"
          :label="t('common.close')"
          data-testid="dst-test-close"
          @click="open = false"
        />
        <UButton
          v-if="ok && !current.enabled && canEnable(current)"
          icon="i-lucide-power"
          :label="t('platform.storage.enable')"
          data-testid="dst-test-enable"
          @click="emit('enable', current)"
        />
      </div>
    </template>
  </UModal>
</template>
