<script setup lang="ts">
import type { RemoteDestination } from '~~/types/api'
import type { DestinationForm } from '~/utils/remote-destinations'
import { PORT_RANGE, RETENTION_RANGE } from '~/utils/remote-destinations'

/**
 * Editar un destino (`PATCH /platform/remote-destinations/{id}` con `If-Match`, re-autenticación
 * reciente). Solo se envía lo que cambió. Cambiar host o puerto borra la huella confirmada y
 * deshabilita el destino hasta volver a probarlo (se avisa antes de guardar y, al guardar, se
 * emite `retest`). Ante `412` se cargan los valores vigentes y se avisa para revisar.
 */
const props = defineProps<{ destination: RemoteDestination }>()
const emit = defineEmits<{
  saved: [destination: RemoteDestination, serverChanged: boolean]
  changed: []
}>()
const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
const { withReauth } = useReauth()

const form = reactive<DestinationForm>({
  name: '',
  host: '',
  port: SFTP_DEFAULTS.port,
  username: '',
  path: '',
  retention_days: SFTP_DEFAULTS.retention_days,
})
const errors = ref<Record<string, string | undefined>>({})
const conflict = ref(false)
const saving = ref(false)
const base = ref<RemoteDestination | null>(null)

function fill(d: RemoteDestination) {
  base.value = d
  Object.assign(form, {
    name: d.name,
    host: d.host,
    port: d.port,
    username: d.username,
    path: d.path,
    retention_days: d.retention_days ?? SFTP_DEFAULTS.retention_days,
  })
}

watch(
  open,
  (o) => {
    if (!o) return
    fill(props.destination)
    errors.value = {}
    conflict.value = false
  },
  { immediate: true },
)

const moving = computed(() => !!base.value && serverChanged(base.value, form))

async function save() {
  const d = base.value
  if (!d) return
  const local = validateDestination(form)
  errors.value = Object.fromEntries(
    Object.entries(local).map(([k, v]) => [
      k,
      t(`platform.storage.form.errors.${v}`, { ...RETENTION_RANGE, ...PORT_RANGE }),
    ]),
  )
  if (Object.keys(local).length) return
  const patch = mergePatch(
    {
      name: d.name,
      host: d.host,
      port: d.port,
      username: d.username,
      path: d.path,
      retention_days: d.retention_days,
    },
    destinationValues(form),
  )
  if (!Object.keys(patch).length) {
    open.value = false
    return
  }
  const moved = moving.value
  saving.value = true
  try {
    const saved = await withReauth(
      () =>
        unwrap(
          $api.PATCH('/platform/remote-destinations/{destination_id}', {
            params: {
              path: { destination_id: d.id },
              header: { 'If-Match': ifMatch(d.version) },
            },
            body: patch,
          }),
        ),
      t('platform.storage.reauth'),
    )
    toast.add({
      title: t('platform.storage.edit.saved', { name: saved.name }),
      description: moved ? t('platform.storage.edit.savedRetest') : undefined,
      icon: moved ? 'i-lucide-triangle-alert' : 'i-lucide-check',
      color: moved ? 'warning' : 'success',
    })
    open.value = false
    emit('saved', saved, moved)
  } catch (e) {
    if (e instanceof ApiError && e.status === 412 && e.problem.current) {
      fill(e.problem.current as unknown as RemoteDestination)
      conflict.value = true
      errors.value = {}
      emit('changed')
    } else if (isReauthRequired(e)) {
      errors.value = { _: t('platform.storage.reauthCancelled') }
    } else if (
      e instanceof ApiError &&
      (e.status === 422 || e.status === 409) &&
      e.problem.errors
    ) {
      errors.value = fieldErrors(e.problem)
    } else if (e instanceof ApiError && e.status === 404) {
      errors.value = { _: t('platform.storage.gone') }
      emit('changed')
    } else {
      errors.value = { _: t(describeError(e).messageKey) }
    }
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <UModal
    v-model:open="open"
    :title="t('platform.storage.edit.title', { name: destination.name })"
    :description="t('platform.storage.edit.description')"
    data-testid="dst-edit-modal"
  >
    <template #body>
      <form id="dst-edit-form" class="flex flex-col gap-4" novalidate @submit.prevent="save">
        <UAlert
          v-if="conflict"
          color="warning"
          variant="subtle"
          icon="i-lucide-git-compare"
          :title="t('platform.storage.edit.conflictTitle')"
          :description="t('platform.storage.edit.conflictBody')"
          data-testid="dst-edit-conflict"
        />
        <RemoteDestinationFields v-model="form" :errors="errors" id-prefix="dst-edit" />
        <UAlert
          v-if="moving"
          color="warning"
          variant="soft"
          icon="i-lucide-triangle-alert"
          :title="t('platform.storage.edit.serverWarnTitle')"
          :description="t('platform.storage.edit.serverWarnBody')"
          data-testid="dst-edit-server-warning"
        />
        <UAlert
          v-if="errors._"
          color="error"
          variant="subtle"
          icon="i-lucide-circle-alert"
          :title="errors._"
          data-testid="dst-edit-error"
        />
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
          form="dst-edit-form"
          icon="i-lucide-save"
          :loading="saving"
          :label="t('common.save')"
          data-testid="dst-edit-submit"
        />
      </div>
    </template>
  </UModal>
</template>
