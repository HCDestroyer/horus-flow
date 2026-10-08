<script setup lang="ts">
import * as z from 'zod'
import type { FormSubmitEvent } from '@nuxt/ui'

/**
 * Login (I0-14, criterio 1): usuario + contraseña y paso de TOTP (docs/api.md §2.1).
 * Errores en contexto y sin revelar si el usuario existe (security.md §4.5).
 */
definePageMeta({ layout: 'auth', public: true })

const { t } = useI18n()
const route = useRoute()
const auth = useAuth()
const config = useRuntimeConfig()

useHead({ title: () => t('login.title') })

type Step = 'credentials' | 'mfa'
const step = ref<Step>('credentials')
const mfaToken = ref<string | null>(null)
const submitting = ref(false)
const formError = ref<string | null>(
  route.query.reason === 'expired' ? t('login.errors.sessionExpired') : null,
)
const showPassword = ref(false)

const credentialsSchema = z.object({
  email: z
    .string()
    .trim()
    .min(1, t('login.errors.emailRequired'))
    .pipe(z.email(t('login.errors.emailInvalid'))),
  password: z.string().min(1, t('login.errors.passwordRequired')),
})
type Credentials = z.output<typeof credentialsSchema>
const credentials = reactive({ email: '', password: '' })

const mfaSchema = z.object({
  code: z.string().trim().min(1, t('login.mfa.errors.required')),
})
type MfaForm = z.output<typeof mfaSchema>
const mfa = reactive({ code: '' })

const codeInput = useTemplateRef<{ inputRef?: HTMLInputElement }>('codeInput')

function messageFor(error: unknown, context: Step) {
  if (error instanceof NetworkError) return t('login.errors.network')
  if (error instanceof ApiError) {
    if (error.status === 429) {
      return t('login.errors.rateLimited', { seconds: error.retryAfter ?? 60 })
    }
    if (context === 'mfa') {
      if (error.code === 'UNAUTHENTICATED') return t('login.mfa.errors.expired')
      if (error.code === 'INVALID_CREDENTIALS') return t('login.mfa.errors.invalid')
    }
    if (error.code === 'INVALID_CREDENTIALS') return t('login.errors.invalidCredentials')
  }
  return t('login.errors.unexpected')
}

async function finish() {
  await navigateTo(safeRedirect(route.query.redirect), { replace: true })
}

async function onCredentials(event: FormSubmitEvent<Credentials>) {
  submitting.value = true
  formError.value = null
  try {
    const { mfaToken: token } = await auth.login(event.data.email, event.data.password)
    if (token) {
      mfaToken.value = token
      step.value = 'mfa'
      await nextTick()
      codeInput.value?.inputRef?.focus()
      return
    }
    await finish()
  } catch (error) {
    formError.value = messageFor(error, 'credentials')
    credentials.password = ''
  } finally {
    submitting.value = false
  }
}

async function onMfa(event: FormSubmitEvent<MfaForm>) {
  if (!mfaToken.value) return
  submitting.value = true
  formError.value = null
  try {
    await auth.verifyMfa(mfaToken.value, event.data.code)
    await finish()
  } catch (error) {
    formError.value = messageFor(error, 'mfa')
    mfa.code = ''
    if (error instanceof ApiError && error.code === 'UNAUTHENTICATED') backToCredentials(false)
  } finally {
    submitting.value = false
  }
}

function backToCredentials(clearError = true) {
  step.value = 'credentials'
  mfaToken.value = null
  mfa.code = ''
  credentials.password = ''
  if (clearError) formError.value = null
}
</script>

<template>
  <UCard
    variant="outline"
    class="shadow-sm"
    :ui="{ body: 'p-6 sm:p-8 space-y-6' }"
    data-testid="login-card"
  >
    <header class="space-y-1">
      <h1 class="text-highlighted text-xl font-semibold">
        {{ step === 'credentials' ? t('login.title') : t('login.mfa.title') }}
      </h1>
      <p class="text-muted text-sm">
        {{ step === 'credentials' ? t('login.description') : t('login.mfa.description') }}
      </p>
    </header>

    <UAlert
      v-if="formError"
      color="error"
      variant="subtle"
      icon="i-lucide-circle-alert"
      :description="formError"
      role="alert"
      data-testid="login-error"
    />

    <UForm
      v-if="step === 'credentials'"
      :schema="credentialsSchema"
      :state="credentials"
      :validate-on="['blur']"
      class="space-y-4"
      novalidate
      @submit="onCredentials"
    >
      <UFormField :label="t('login.email')" name="email" required>
        <UInput
          v-model="credentials.email"
          type="email"
          autocomplete="username"
          inputmode="email"
          spellcheck="false"
          autocapitalize="none"
          :placeholder="t('login.emailPlaceholder')"
          size="lg"
          class="w-full"
          autofocus
        />
      </UFormField>

      <UFormField :label="t('login.password')" name="password" required>
        <template #hint>
          <UPopover :content="{ side: 'top' }">
            <UButton
              variant="link"
              color="neutral"
              size="xs"
              class="px-0"
              :label="t('login.forgot')"
            />
            <template #content>
              <p class="max-w-64 p-3 text-sm">{{ t('login.forgotHint') }}</p>
            </template>
          </UPopover>
        </template>
        <UInput
          v-model="credentials.password"
          :type="showPassword ? 'text' : 'password'"
          autocomplete="current-password"
          size="lg"
          class="w-full"
          :ui="{ trailing: 'pe-1' }"
        >
          <template #trailing>
            <UButton
              color="neutral"
              variant="link"
              size="sm"
              :icon="showPassword ? 'i-lucide-eye-off' : 'i-lucide-eye'"
              :aria-label="showPassword ? t('login.hidePassword') : t('login.showPassword')"
              :aria-pressed="showPassword"
              @click="showPassword = !showPassword"
            />
          </template>
        </UInput>
      </UFormField>

      <UButton
        type="submit"
        size="lg"
        block
        :loading="submitting"
        :label="t('login.submit')"
        data-testid="login-submit"
      />
    </UForm>

    <UForm v-else :schema="mfaSchema" :state="mfa" class="space-y-4" novalidate @submit="onMfa">
      <UFormField :label="t('login.mfa.code')" name="code" :help="t('login.mfa.codeHint')" required>
        <UInput
          ref="codeInput"
          v-model="mfa.code"
          autocomplete="one-time-code"
          inputmode="numeric"
          spellcheck="false"
          maxlength="12"
          size="xl"
          class="w-full"
          :ui="{ base: 'font-mono tracking-[0.3em] text-center' }"
        />
      </UFormField>

      <UButton
        type="submit"
        size="lg"
        block
        :loading="submitting"
        :label="t('login.mfa.submit')"
        data-testid="mfa-submit"
      />
      <UButton
        variant="ghost"
        color="neutral"
        block
        icon="i-lucide-arrow-left"
        :label="t('login.mfa.back')"
        @click="backToCredentials()"
      />
    </UForm>

    <UAlert
      v-if="config.public.apiMock"
      color="info"
      variant="soft"
      icon="i-lucide-flask-conical"
      :title="t('login.demo.title')"
      :ui="{ description: 'text-xs' }"
      data-testid="mock-hint"
    >
      <template #description>
        <i18n-t keypath="login.demo.description" tag="span" scope="global">
          <template #email><code class="font-mono">ana.ruiz@fibranorte.example</code></template>
          <template #password><code class="font-mono">horus-demo-2026</code></template>
          <template #code><code class="font-mono">123456</code></template>
        </i18n-t>
      </template>
    </UAlert>
  </UCard>
</template>
