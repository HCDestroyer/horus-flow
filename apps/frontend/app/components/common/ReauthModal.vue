<script setup lang="ts">
/**
 * Confirmar identidad (re-autenticación reciente, acciones `x-reauth`): contraseña y, si la
 * cuenta tiene 2FA, el código TOTP. Al confirmar emite `close(true)`; al cancelar,
 * `close(false)`. Se abre con `useReauth()` (overlay de Nuxt UI), que reintenta la acción.
 */
const props = withDefaults(defineProps<{ description?: string }>(), { description: undefined })
const emit = defineEmits<{ close: [confirmed: boolean] }>()

const { t } = useI18n()
const auth = useAuth()
const mfa = computed(() => !!auth.me.value?.mfa_enabled)

const password = ref('')
const code = ref('')
const errors = ref<{ password?: string; code?: string; _?: string }>({})
const busy = ref(false)

async function submit() {
  errors.value = {}
  if (!password.value) {
    errors.value = { password: t('reauth.passwordRequired') }
    return
  }
  if (mfa.value && !/^\d{6}$/.test(code.value.replace(/\s/g, ''))) {
    errors.value = { code: t('reauth.codeInvalid') }
    return
  }
  busy.value = true
  try {
    await auth.reauthenticate(password.value, mfa.value ? code.value.replace(/\s/g, '') : null)
    emit('close', true)
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) {
      if (e.code === 'INVALID_MFA_CODE' || e.code === 'MFA_REQUIRED') {
        errors.value = { code: t('reauth.codeWrong') }
      } else {
        errors.value = mfa.value
          ? { _: t('reauth.wrongWithCode') }
          : { password: t('reauth.wrong') }
      }
    } else {
      const info = describeError(e)
      errors.value = { _: t(info.messageKey, info.params ?? {}) }
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <UModal
    :title="t('reauth.title')"
    :description="props.description ?? t('reauth.description')"
    :close="{ onClick: () => emit('close', false) }"
    data-testid="reauth-modal"
  >
    <template #body>
      <form id="reauth-form" class="flex flex-col gap-4" novalidate @submit.prevent="submit">
        <UFormField :label="t('reauth.password')" :error="errors.password" required>
          <UInput
            v-model="password"
            type="password"
            autocomplete="current-password"
            autofocus
            class="w-full"
            data-testid="reauth-password"
          />
        </UFormField>
        <UFormField
          v-if="mfa"
          :label="t('reauth.code')"
          :hint="t('reauth.codeHint')"
          :error="errors.code"
          required
        >
          <UInput
            v-model="code"
            inputmode="numeric"
            autocomplete="one-time-code"
            maxlength="7"
            class="w-full"
            :ui="{ base: 'tabular tracking-widest' }"
            data-testid="reauth-code"
          />
        </UFormField>
        <UAlert
          v-if="errors._"
          color="error"
          variant="subtle"
          icon="i-lucide-circle-alert"
          :title="errors._"
          data-testid="reauth-error"
        />
      </form>
    </template>
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton
          color="neutral"
          variant="ghost"
          :label="t('common.cancel')"
          data-testid="reauth-cancel"
          @click="emit('close', false)"
        />
        <UButton
          type="submit"
          form="reauth-form"
          icon="i-lucide-lock-keyhole-open"
          :loading="busy"
          :label="t('reauth.submit')"
          data-testid="reauth-submit"
        />
      </div>
    </template>
  </UModal>
</template>
