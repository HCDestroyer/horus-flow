<script setup lang="ts">
import type { PasswordResetInspection } from '~~/types/api'

/**
 * Nueva contraseña con un enlace de restablecimiento (pública, sin sesión). Como la
 * invitación, el token viaja en el fragmento (`/reset-password#token=…`): se lee en el cliente
 * y se borra de la barra de direcciones antes de llamar a la API. `inspect` muestra a quién
 * pertenece el enlace; `reset` fija la contraseña (mínimo 12, repetida) y revoca las sesiones.
 * Un enlace inválido, caducado o ya usado se explica (422, `errors[0].code`).
 */
definePageMeta({ layout: 'auth', public: true })

const { t, d } = useI18n()
const { $api } = useNuxtApp()

useHead({ title: () => t('resetPassword.title') })

type Step = 'loading' | 'invalid' | 'ready' | 'done'
type TokenProblem = 'INVALID_TOKEN' | 'EXPIRED' | 'USED'
const step = ref<Step>('loading')
const token = ref('')
const info = ref<PasswordResetInspection | null>(null)
const busy = ref(false)
const formError = ref<string | null>(null)
const tokenProblem = ref<TokenProblem>('INVALID_TOKEN')

const form = reactive({ password: '', repeat: '' })
const pwInvalid = computed(() => {
  if (form.password && form.password.length < 12) return t('account.password.tooShort')
  if (form.repeat && form.password !== form.repeat) return t('account.password.mismatch')
  return null
})
const canSubmit = computed(() => !!form.password && !!form.repeat && !pwInvalid.value)

/** Lee `#token=…` y lo quita de la URL (sin añadir una entrada al historial). */
function takeToken() {
  const params = new URLSearchParams(window.location.hash.replace(/^#/, ''))
  const value = params.get('token') ?? ''
  if (window.location.hash) {
    history.replaceState(history.state, '', window.location.pathname + window.location.search)
  }
  return value
}

/** 422 del token: código del primer error (`INVALID_TOKEN` si no viene). */
function tokenProblemOf(error: unknown): TokenProblem | null {
  if (!(error instanceof ApiError) || error.status !== 422) return null
  const first = error.problem.errors?.[0]
  if (first && first.field !== 'token') return null
  const code = first?.code
  return code === 'EXPIRED' || code === 'USED' ? code : 'INVALID_TOKEN'
}

function messageFor(error: unknown) {
  if (error instanceof NetworkError) return t('invitation.errors.network')
  if (error instanceof ApiError && error.status === 429) {
    return t('login.errors.rateLimited', { seconds: error.retryAfter ?? 60 })
  }
  return t('login.errors.unexpected')
}

function invalidate(problem: TokenProblem) {
  tokenProblem.value = problem
  token.value = ''
  step.value = 'invalid'
}

onMounted(async () => {
  token.value = takeToken()
  if (token.value.length < 32) {
    invalidate('INVALID_TOKEN')
    return
  }
  try {
    info.value = await unwrap(
      $api.POST('/auth/password/reset/inspect', { body: { token: token.value } }),
    )
    step.value = 'ready'
  } catch (error) {
    const problem = tokenProblemOf(error)
    if (problem) invalidate(problem)
    else {
      formError.value = messageFor(error)
      invalidate('INVALID_TOKEN')
    }
  }
})

async function submit() {
  if (!canSubmit.value || !token.value) return
  busy.value = true
  formError.value = null
  try {
    await unwrap(
      $api.POST('/auth/password/reset', {
        body: { token: token.value, new_password: form.password },
      }),
    )
    token.value = ''
    form.password = form.repeat = ''
    step.value = 'done'
  } catch (error) {
    const problem = tokenProblemOf(error)
    if (problem) {
      invalidate(problem)
    } else if (error instanceof ApiError && error.status === 422) {
      formError.value = error.problem.errors?.[0]?.message ?? t('account.errors.invalid')
    } else {
      formError.value = messageFor(error)
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <UCard
    variant="outline"
    class="shadow-sm"
    :ui="{ body: 'p-6 sm:p-8 space-y-6' }"
    data-testid="reset-card"
  >
    <template v-if="step === 'loading'">
      <h1 class="text-highlighted text-xl font-semibold">{{ t('resetPassword.title') }}</h1>
      <LoadingState :rows="2" />
    </template>

    <template v-else-if="step === 'invalid'">
      <header class="space-y-1">
        <h1 class="text-highlighted text-xl font-semibold">
          {{ t('resetPassword.invalid.title') }}
        </h1>
      </header>
      <UAlert
        color="error"
        variant="subtle"
        icon="i-lucide-link-2-off"
        :title="formError ?? t(`resetPassword.invalid.${tokenProblem}`)"
        :description="t('resetPassword.invalid.advice')"
        role="alert"
        data-testid="reset-invalid"
      />
      <div class="flex flex-col gap-2">
        <UButton
          to="/forgot-password"
          block
          icon="i-lucide-key-round"
          :label="t('resetPassword.invalid.again')"
          data-testid="reset-again"
        />
        <UButton
          to="/login"
          color="neutral"
          variant="outline"
          block
          icon="i-lucide-log-in"
          :label="t('resetPassword.goLogin')"
        />
      </div>
    </template>

    <template v-else-if="step === 'done'">
      <header class="space-y-1">
        <h1 class="text-highlighted text-xl font-semibold">{{ t('resetPassword.done.title') }}</h1>
      </header>
      <UAlert
        color="success"
        variant="subtle"
        icon="i-lucide-circle-check"
        :description="t('resetPassword.done.body', { email: info?.email ?? '' })"
        role="status"
        data-testid="reset-done"
      />
      <UButton
        to="/login"
        size="lg"
        block
        icon="i-lucide-log-in"
        :label="t('resetPassword.goLogin')"
        data-testid="reset-go-login"
      />
    </template>

    <template v-else-if="info">
      <header class="space-y-1">
        <h1 class="text-highlighted text-xl font-semibold">{{ t('resetPassword.heading') }}</h1>
        <p class="text-muted text-sm" data-testid="reset-summary">
          {{ t('resetPassword.summary', { name: info.display_name, email: info.email }) }}
        </p>
        <p class="text-dimmed text-xs">
          {{ t('resetPassword.expires', { time: d(new Date(info.expires_at), 'long') }) }}
        </p>
      </header>

      <UAlert
        v-if="formError"
        color="error"
        variant="subtle"
        icon="i-lucide-circle-alert"
        :description="formError"
        role="alert"
        data-testid="reset-error"
      />

      <form class="space-y-4" novalidate data-testid="reset-form" @submit.prevent="submit">
        <UFormField :label="t('invitation.email')" name="email">
          <UInput
            :model-value="info.email"
            readonly
            disabled
            class="w-full"
            autocomplete="username"
          />
        </UFormField>
        <UFormField
          :label="t('resetPassword.password')"
          name="password"
          :help="t('resetPassword.passwordHint')"
          :error="pwInvalid ?? undefined"
          required
        >
          <UInput
            v-model="form.password"
            type="password"
            autocomplete="new-password"
            class="w-full"
            autofocus
            data-testid="reset-password"
          />
        </UFormField>
        <UFormField :label="t('resetPassword.repeat')" name="repeat" required>
          <UInput
            v-model="form.repeat"
            type="password"
            autocomplete="new-password"
            class="w-full"
            data-testid="reset-repeat"
          />
        </UFormField>
        <UButton
          type="submit"
          size="lg"
          block
          :loading="busy"
          :disabled="!canSubmit"
          :label="t('resetPassword.submit')"
          data-testid="reset-submit"
        />
      </form>
    </template>
  </UCard>
</template>
