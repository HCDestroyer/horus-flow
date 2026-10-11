<script setup lang="ts">
import type { InvitationInspection } from '~~/types/api'

/**
 * Aceptar una invitación a un ISP (pública, sin sesión). El enlace trae el token en el
 * fragmento (`/invitation#token=…`): no llega a ningún servidor ni a los logs. Se lee en el
 * cliente y se borra de la barra de direcciones (y del historial) antes de llamar a la API.
 * - Cuenta nueva: nombre + contraseña (mínimo 12, repetida), igual que "Mi cuenta".
 * - Cuenta existente: "Aceptar invitación" y a iniciar sesión con esa cuenta.
 */
definePageMeta({ layout: 'auth', public: true })

const { t, d } = useI18n()
const { $api } = useNuxtApp()
const auth = useAuth()
const loginNotice = useLoginNotice()

useHead({ title: () => t('invitation.title') })

type Step = 'loading' | 'invalid' | 'ready'
const step = ref<Step>('loading')
const token = ref('')
const info = ref<InvitationInspection | null>(null)
const busy = ref(false)
const formError = ref<string | null>(null)
const errorMessage = ref<string | null>(null)

const form = reactive({ name: '', password: '', repeat: '' })
const pwInvalid = computed(() => {
  if (form.password && form.password.length < 12) return t('account.password.tooShort')
  if (form.repeat && form.password !== form.repeat) return t('account.password.mismatch')
  return null
})
const canSubmit = computed(
  () => !!form.name.trim() && !!form.password && !!form.repeat && !pwInvalid.value,
)

/** Lee `#token=…` y lo quita de la URL (sin añadir una entrada al historial). */
function takeToken() {
  const params = new URLSearchParams(window.location.hash.replace(/^#/, ''))
  const value = params.get('token') ?? ''
  if (window.location.hash) {
    history.replaceState(history.state, '', window.location.pathname + window.location.search)
  }
  return value
}

function messageFor(error: unknown) {
  if (error instanceof NetworkError) return t('invitation.errors.network')
  if (error instanceof ApiError && error.status === 429) {
    return t('login.errors.rateLimited', { seconds: error.retryAfter ?? 60 })
  }
  return null
}

onMounted(async () => {
  token.value = takeToken()
  if (token.value.length < 32) {
    step.value = 'invalid'
    return
  }
  try {
    info.value = await unwrap(
      $api.POST('/auth/invitations/inspect', { body: { token: token.value } }),
    )
    step.value = 'ready'
  } catch (error) {
    errorMessage.value = messageFor(error)
    step.value = 'invalid'
  }
})

async function accept() {
  if (!info.value) return
  if (info.value.needs_password && !canSubmit.value) return
  busy.value = true
  formError.value = null
  try {
    await unwrap(
      $api.POST('/auth/invitations/accept', {
        body: info.value.needs_password
          ? { token: token.value, display_name: form.name.trim(), password: form.password }
          : { token: token.value },
      }),
    )
    const email = info.value.email
    const isNew = info.value.needs_password
    token.value = ''
    form.password = form.repeat = ''
    if (!isNew && auth.isAuthenticated.value) {
      // Ya tenía la sesión abierta con esa cuenta: el ISP nuevo aparece al recargar `/me`.
      await auth.reloadMe().catch(() => undefined)
      await navigateTo('/', { replace: true })
      return
    }
    loginNotice.value = {
      kind: isNew ? 'invitation-new' : 'invitation-existing',
      email,
      tenantName: info.value.tenant_name,
    }
    await navigateTo('/login', { replace: true })
  } catch (error) {
    if (error instanceof ApiError && error.status === 422) {
      const tokenError = error.problem.errors?.some((e) => e.field === 'token')
      if (tokenError || !error.problem.errors?.length) {
        step.value = 'invalid'
        return
      }
      formError.value = error.problem.errors[0]?.message ?? t('account.errors.invalid')
    } else {
      formError.value = messageFor(error) ?? t('login.errors.unexpected')
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
    data-testid="invitation-card"
  >
    <template v-if="step === 'loading'">
      <h1 class="text-highlighted text-xl font-semibold">{{ t('invitation.title') }}</h1>
      <LoadingState :rows="2" />
    </template>

    <template v-else-if="step === 'invalid'">
      <header class="space-y-1">
        <h1 class="text-highlighted text-xl font-semibold">{{ t('invitation.invalid.title') }}</h1>
      </header>
      <UAlert
        color="error"
        variant="subtle"
        icon="i-lucide-link-2-off"
        :title="errorMessage ?? t('invitation.invalid.body')"
        :description="t('invitation.invalid.advice')"
        role="alert"
        data-testid="invitation-invalid"
      />
      <UButton
        to="/login"
        color="neutral"
        variant="outline"
        block
        icon="i-lucide-log-in"
        :label="t('invitation.goLogin')"
      />
    </template>

    <template v-else-if="info">
      <header class="space-y-1">
        <h1 class="text-highlighted text-xl font-semibold">
          {{ t('invitation.heading', { tenant: info.tenant_name }) }}
        </h1>
        <p class="text-muted text-sm" data-testid="invitation-summary">
          {{ t('invitation.summary', { email: info.email, tenant: info.tenant_name }) }}
        </p>
        <p class="text-dimmed text-xs">
          {{ t('invitation.expires', { time: d(new Date(info.expires_at), 'long') }) }}
        </p>
      </header>

      <UAlert
        v-if="formError"
        color="error"
        variant="subtle"
        icon="i-lucide-circle-alert"
        :description="formError"
        role="alert"
        data-testid="invitation-error"
      />

      <form
        v-if="info.needs_password"
        class="space-y-4"
        novalidate
        data-testid="invitation-form"
        @submit.prevent="accept"
      >
        <UFormField :label="t('invitation.email')" name="email">
          <UInput
            :model-value="info.email"
            readonly
            disabled
            class="w-full"
            autocomplete="username"
          />
        </UFormField>
        <UFormField :label="t('invitation.name')" name="name" required>
          <UInput
            v-model="form.name"
            autocomplete="name"
            maxlength="120"
            class="w-full"
            data-testid="invitation-name"
          />
        </UFormField>
        <UFormField
          :label="t('invitation.password')"
          name="password"
          :help="t('invitation.passwordHint')"
          :error="pwInvalid ?? undefined"
          required
        >
          <UInput
            v-model="form.password"
            type="password"
            autocomplete="new-password"
            class="w-full"
            data-testid="invitation-password"
          />
        </UFormField>
        <UFormField :label="t('invitation.repeat')" name="repeat" required>
          <UInput
            v-model="form.repeat"
            type="password"
            autocomplete="new-password"
            class="w-full"
            data-testid="invitation-repeat"
          />
        </UFormField>
        <UButton
          type="submit"
          size="lg"
          block
          :loading="busy"
          :disabled="!canSubmit"
          :label="t('invitation.create')"
          data-testid="invitation-submit"
        />
      </form>

      <div v-else class="space-y-4">
        <p class="text-sm">{{ t('invitation.existing') }}</p>
        <UButton
          size="lg"
          block
          icon="i-lucide-check"
          :loading="busy"
          :label="t('invitation.accept')"
          data-testid="invitation-accept"
          @click="accept"
        />
      </div>
    </template>
  </UCard>
</template>
