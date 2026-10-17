<script setup lang="ts">
import type { SsoRedeemResult } from '~~/types/api'

/**
 * Entrada por autologin desde ArgosISP (contrato docs/integrations/argosisp.md §1.5, H11).
 * ArgosISP pidió el ticket a Horus de servidor a servidor y abrió `/sso/entrar#<ticket>`: el
 * ticket va en el fragmento, se quita de la barra de direcciones y se canjea en el servidor de
 * Horus (un uso, 60 s). Con la cuenta vinculada abre la sesión del usuario de Horus (con su 2FA)
 * y lo lleva al recurso con SUS permisos; sin vinculación pide iniciar sesión para vincularla.
 * Horus nunca crea usuarios ni vincula por coincidir el correo.
 */
definePageMeta({ layout: 'auth', public: true })

const { t } = useI18n()
const { $api } = useNuxtApp()
const auth = useAuth()
const { targetRoute } = useSso()

useHead({ title: () => t('sso.enter.title') })

type Step = 'loading' | 'invalid' | 'unavailable' | 'mfa' | 'link' | 'error'
const step = ref<Step>('loading')
const result = ref<SsoRedeemResult | null>(null)
const errorText = ref<string | null>(null)
const mfaCode = ref('')
const busy = ref(false)

/** Lee el ticket del fragmento y lo borra de la URL (sin entrada nueva en el historial). */
function takeTicket() {
  const value = ticketFromHash(window.location.hash)
  if (window.location.hash) {
    history.replaceState(history.state, '', window.location.pathname + window.location.search)
  }
  return value
}

async function goToTarget() {
  const target = result.value?.target
  if (!target) return navigateTo('/', { replace: true })
  const { path } = await targetRoute(target)
  await navigateTo(path, { replace: true })
}

function messageFor(error: unknown) {
  if (error instanceof NetworkError) return t('login.errors.network')
  if (error instanceof ApiError && error.status === 429) {
    return t('login.errors.rateLimited', { seconds: error.retryAfter ?? 60 })
  }
  return t('login.errors.unexpected')
}

onMounted(async () => {
  const ticket = takeTicket()
  if (!ticket) {
    step.value = 'invalid'
    return
  }
  try {
    const res = await unwrap(
      $api.POST('/integrations/argosisp/sso/redeem', { body: { ticket }, ...WITH_CSRF }),
    )
    result.value = res
    if (res.status === 'authenticated' && res.access_token) {
      await auth.adoptSession(res.access_token)
      await goToTarget()
      return
    }
    if (res.status === 'mfa_required') {
      step.value = 'mfa'
      return
    }
    if (res.link_token && res.link_expires_at && res.external_user) {
      savePendingLink({
        linkToken: res.link_token,
        linkExpiresAt: res.link_expires_at,
        externalUser: res.external_user,
        target: res.target,
      })
    }
    step.value = 'link'
  } catch (error) {
    if (error instanceof ApiError && error.status === 410) step.value = 'invalid'
    else if (error instanceof ApiError && error.code === 'SSO_ACCOUNT_UNAVAILABLE') {
      step.value = 'unavailable'
    } else {
      errorText.value = messageFor(error)
      step.value = 'error'
    }
  }
})

async function verify() {
  const token = result.value?.mfa_token
  if (!token || !mfaCode.value.trim()) return
  busy.value = true
  errorText.value = null
  try {
    await auth.verifyMfa(token, mfaCode.value.trim())
    await goToTarget()
  } catch (error) {
    mfaCode.value = ''
    errorText.value =
      error instanceof ApiError && error.code === 'INVALID_CREDENTIALS'
        ? t('login.mfa.errors.invalid')
        : error instanceof ApiError && error.code === 'UNAUTHENTICATED'
          ? t('login.mfa.errors.expired')
          : messageFor(error)
  } finally {
    busy.value = false
  }
}

const linkTo = computed(() =>
  auth.isAuthenticated.value ? '/sso/vincular' : '/login?redirect=%2Fsso%2Fvincular',
)
const who = computed(() => {
  const u = result.value?.external_user
  if (!u) return ''
  return [u.name, u.email].filter(Boolean).join(' · ') || u.id
})
</script>

<template>
  <UCard
    variant="outline"
    class="shadow-sm"
    :ui="{ body: 'p-6 sm:p-8 space-y-5' }"
    data-testid="sso-enter"
  >
    <header class="space-y-1">
      <h1 class="text-highlighted text-xl font-semibold">{{ t('sso.enter.title') }}</h1>
      <p class="text-muted text-sm">{{ t('sso.enter.description') }}</p>
    </header>

    <div v-if="step === 'loading'" class="text-muted flex items-center gap-2 text-sm" role="status">
      <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" />
      {{ t('sso.enter.checking') }}
    </div>

    <UAlert
      v-else-if="step === 'invalid'"
      color="warning"
      variant="subtle"
      icon="i-lucide-link-2-off"
      :title="t('sso.enter.invalid.title')"
      :description="t('sso.enter.invalid.body')"
      data-testid="sso-invalid"
    />

    <UAlert
      v-else-if="step === 'unavailable'"
      color="error"
      variant="subtle"
      icon="i-lucide-user-x"
      :title="t('sso.enter.unavailable.title')"
      :description="t('sso.enter.unavailable.body')"
      data-testid="sso-unavailable"
    />

    <form v-else-if="step === 'mfa'" class="space-y-4" novalidate @submit.prevent="verify">
      <UFormField :label="t('login.mfa.code')" :help="t('login.mfa.codeHint')" required>
        <UInput
          v-model="mfaCode"
          autocomplete="one-time-code"
          inputmode="numeric"
          maxlength="12"
          size="xl"
          class="w-full"
          autofocus
          :ui="{ base: 'font-mono tracking-[0.3em] text-center' }"
          data-testid="sso-mfa-code"
        />
      </UFormField>
      <UButton
        type="submit"
        size="lg"
        block
        :loading="busy"
        :label="t('login.mfa.submit')"
        data-testid="sso-mfa-submit"
      />
    </form>

    <div v-else-if="step === 'link'" class="space-y-4" data-testid="sso-link-required">
      <UAlert
        color="info"
        variant="subtle"
        icon="i-lucide-link"
        :title="t('sso.enter.link.title')"
        :description="t('sso.enter.link.body', { who })"
      />
      <p class="text-muted text-xs">{{ t('sso.enter.link.never') }}</p>
      <UButton
        :to="linkTo"
        size="lg"
        block
        icon="i-lucide-log-in"
        :label="
          auth.isAuthenticated.value ? t('sso.enter.link.continue') : t('sso.enter.link.login')
        "
        data-testid="sso-link-login"
      />
    </div>

    <UAlert
      v-if="errorText"
      color="error"
      variant="subtle"
      icon="i-lucide-circle-alert"
      :description="errorText"
      role="alert"
      data-testid="sso-error"
    />

    <UButton
      v-if="step === 'invalid' || step === 'unavailable' || step === 'error'"
      to="/login"
      variant="ghost"
      color="neutral"
      block
      icon="i-lucide-arrow-left"
      :label="t('sso.enter.toLogin')"
    />
  </UCard>
</template>
