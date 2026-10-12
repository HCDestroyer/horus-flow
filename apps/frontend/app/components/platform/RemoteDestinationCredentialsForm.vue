<script setup lang="ts">
import type { RemoteDestination } from '~~/types/api'
import type { CredentialForm, CredentialMode } from '~/utils/remote-destinations'

/**
 * Credenciales SFTP write-only (`PUT /platform/remote-destinations/{id}/credentials`,
 * re-autenticación reciente): contraseña, o clave privada OpenSSH con frase de paso opcional.
 * Se guardan cifradas y nunca se devuelven: el formulario se vacía al guardar y no hay forma de
 * volver a verlas. La clave se pega oculta (Mostrar/Ocultar).
 */
const props = defineProps<{ destination: RemoteDestination; submitLabel?: string }>()
const emit = defineEmits<{ saved: [destination: RemoteDestination] }>()

const { t } = useI18n()
const { $api } = useNuxtApp()
const { withReauth } = useReauth()

const form = reactive<CredentialForm>({
  mode: 'private_key',
  password: '',
  privateKey: '',
  passphrase: '',
})
const showKey = ref(false)
const error = ref<string | null>(null)
const busy = ref(false)

const modes = computed(() => [
  {
    value: 'private_key' as CredentialMode,
    label: t('platform.storage.credentials.modeKey'),
    description: t('platform.storage.credentials.modeKeyHelp'),
  },
  {
    value: 'password' as CredentialMode,
    label: t('platform.storage.credentials.modePassword'),
    description: t('platform.storage.credentials.modePasswordHelp'),
  },
])

watch(
  () => form.mode,
  () => {
    error.value = null
  },
)

function clear() {
  form.password = ''
  form.privateKey = ''
  form.passphrase = ''
  showKey.value = false
}

async function save() {
  const problem = validateCredentials(form)
  if (problem) {
    error.value = t(`platform.storage.credentials.errors.${problem}`)
    return
  }
  error.value = null
  busy.value = true
  try {
    const body = credentialsBody(form)
    const saved = await withReauth(
      () =>
        unwrap(
          $api.PUT('/platform/remote-destinations/{destination_id}/credentials', {
            params: { path: { destination_id: props.destination.id } },
            body,
          }),
        ),
      t('platform.storage.reauth'),
    )
    clear()
    emit('saved', saved)
  } catch (e) {
    if (isReauthRequired(e)) error.value = t('platform.storage.reauthCancelled')
    else if (e instanceof ApiError && e.status === 422) {
      const fields = fieldErrors(e.problem)
      error.value = Object.values(fields)[0] ?? t('platform.storage.credentials.errors.keyFormat')
    } else if (e instanceof ApiError && e.status === 404) error.value = t('platform.storage.gone')
    else error.value = t(describeError(e).messageKey)
  } finally {
    busy.value = false
  }
}

onBeforeUnmount(clear)
</script>

<template>
  <form
    class="flex flex-col gap-4"
    novalidate
    data-testid="dst-credentials-form"
    @submit.prevent="save"
  >
    <UAlert
      color="info"
      variant="subtle"
      icon="i-lucide-eye-off"
      :title="t('platform.storage.credentials.writeOnlyTitle')"
      :description="
        destination.has_credentials
          ? t('platform.storage.credentials.replaceBody')
          : t('platform.storage.credentials.writeOnlyBody')
      "
    />
    <URadioGroup
      v-model="form.mode"
      :legend="t('platform.storage.credentials.mode')"
      :items="modes"
      variant="card"
      orientation="vertical"
      data-testid="dst-credentials-mode"
    />

    <UFormField
      v-if="form.mode === 'password'"
      :label="t('platform.storage.credentials.password')"
      :help="t('platform.storage.credentials.passwordHelp', { user: destination.username })"
      required
    >
      <SecretInput v-model="form.password" maxlength="1024" data-testid="dst-password" />
    </UFormField>

    <template v-else>
      <UFormField
        :label="t('platform.storage.credentials.privateKey')"
        :help="t('platform.storage.credentials.privateKeyHelp')"
        required
      >
        <template #hint>
          <UButton
            color="neutral"
            variant="link"
            size="xs"
            :icon="showKey ? 'i-lucide-eye-off' : 'i-lucide-eye'"
            :label="
              showKey
                ? t('platform.storage.credentials.hideKey')
                : t('platform.storage.credentials.showKey')
            "
            :aria-pressed="showKey"
            data-testid="dst-key-toggle"
            @click="showKey = !showKey"
          />
        </template>
        <UTextarea
          v-model="form.privateKey"
          :rows="6"
          autoresize
          :maxrows="12"
          spellcheck="false"
          autocomplete="off"
          class="w-full"
          placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
          :ui="{ base: ['font-mono text-xs', showKey ? '' : 'rd-secret-mask'].join(' ') }"
          data-testid="dst-private-key"
        />
      </UFormField>
      <UFormField
        :label="t('platform.storage.credentials.passphrase')"
        :help="t('platform.storage.credentials.passphraseHelp')"
        :hint="t('common.optional')"
      >
        <SecretInput v-model="form.passphrase" maxlength="1024" data-testid="dst-passphrase" />
      </UFormField>
    </template>

    <UAlert
      v-if="error"
      color="error"
      variant="subtle"
      icon="i-lucide-circle-alert"
      :title="error"
      data-testid="dst-credentials-error"
    />
    <div class="flex justify-end">
      <UButton
        type="submit"
        icon="i-lucide-key-round"
        :loading="busy"
        :label="submitLabel ?? t('platform.storage.credentials.submit')"
        data-testid="dst-credentials-submit"
      />
    </div>
  </form>
</template>

<style>
/* Clave pegada oculta: discos en Chromium/Safari; difuminada donde no hay -webkit-text-security. */
.rd-secret-mask {
  -webkit-text-security: disc;
}
@supports not (-webkit-text-security: disc) {
  .rd-secret-mask {
    filter: blur(4px);
  }
}
</style>
