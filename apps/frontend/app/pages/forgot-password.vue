<script setup lang="ts">
import { z } from '~/utils/zod'
import type { FormSubmitEvent } from '@nuxt/ui'

/**
 * Recuperar contraseña (pública, sin sesión): correo → `POST /auth/password/forgot`. La
 * respuesta es la misma exista o no la cuenta (security.md §4.5); solo dice si la instalación
 * envía correo. Sin SMTP, el enlace lo emite un administrador del ISP desde Usuarios y roles.
 */
definePageMeta({ layout: 'auth', public: true })

const { t } = useI18n()
const { $api } = useNuxtApp()

useHead({ title: () => t('forgotPassword.title') })

const schema = z.object({
  email: z
    .string()
    .trim()
    .min(1, t('forgotPassword.errors.emailRequired'))
    .pipe(z.email(t('forgotPassword.errors.emailInvalid'))),
})
type Form = z.output<typeof schema>
const form = reactive({ email: '' })
const submitting = ref(false)
const formError = ref<string | null>(null)
/** Respuesta del backend: `true` si envía correo; `false` si no hay SMTP. */
const delivery = ref<boolean | null>(null)

function messageFor(error: unknown) {
  if (error instanceof NetworkError) return t('login.errors.network')
  if (error instanceof ApiError && error.status === 429) {
    return t('login.errors.rateLimited', { seconds: error.retryAfter ?? 60 })
  }
  return t('login.errors.unexpected')
}

async function onSubmit(event: FormSubmitEvent<Form>) {
  submitting.value = true
  formError.value = null
  try {
    const res = await unwrap(
      $api.POST('/auth/password/forgot', { body: { email: event.data.email } }),
    )
    delivery.value = res.email_delivery
  } catch (error) {
    formError.value = messageFor(error)
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <UCard
    variant="outline"
    class="shadow-sm"
    :ui="{ body: 'p-6 sm:p-8 space-y-6' }"
    data-testid="forgot-card"
  >
    <header class="space-y-1">
      <h1 class="text-highlighted text-xl font-semibold">{{ t('forgotPassword.title') }}</h1>
      <p v-if="delivery === null" class="text-muted text-sm">
        {{ t('forgotPassword.description') }}
      </p>
    </header>

    <template v-if="delivery !== null">
      <UAlert
        v-if="delivery"
        color="success"
        variant="subtle"
        icon="i-lucide-mail-check"
        :title="t('forgotPassword.sent.title')"
        :description="t('forgotPassword.sent.body')"
        role="status"
        data-testid="forgot-sent"
      />
      <UAlert
        v-else
        color="warning"
        variant="subtle"
        icon="i-lucide-mail-x"
        :title="t('forgotPassword.noEmail.title')"
        :description="t('forgotPassword.noEmail.body')"
        role="status"
        data-testid="forgot-no-email"
      />
      <UButton
        to="/login"
        color="neutral"
        variant="outline"
        block
        icon="i-lucide-log-in"
        :label="t('forgotPassword.back')"
        data-testid="forgot-back"
      />
    </template>

    <template v-else>
      <UAlert
        v-if="formError"
        color="error"
        variant="subtle"
        icon="i-lucide-circle-alert"
        :description="formError"
        role="alert"
        data-testid="forgot-error"
      />

      <!-- Validación por defecto: el error aparece al salir del campo y se limpia al corregirlo,
           así no hay saltos de layout al pulsar "Enviar". -->
      <UForm :schema="schema" :state="form" class="space-y-4" novalidate @submit="onSubmit">
        <UFormField :label="t('forgotPassword.email')" name="email" required>
          <UInput
            v-model="form.email"
            type="email"
            autocomplete="username"
            inputmode="email"
            spellcheck="false"
            autocapitalize="none"
            :placeholder="t('login.emailPlaceholder')"
            size="lg"
            class="w-full"
            autofocus
            data-testid="forgot-email"
          />
        </UFormField>
        <UButton
          type="submit"
          size="lg"
          block
          icon="i-lucide-send"
          :loading="submitting"
          :label="t('forgotPassword.submit')"
          data-testid="forgot-submit"
        />
      </UForm>

      <UButton
        to="/login"
        variant="ghost"
        color="neutral"
        block
        icon="i-lucide-arrow-left"
        :label="t('forgotPassword.back')"
        data-testid="forgot-back"
      />
    </template>
  </UCard>
</template>
