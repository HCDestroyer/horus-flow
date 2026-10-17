<script setup lang="ts">
/**
 * Vinculación de la cuenta de ArgosISP con el usuario de Horus (contrato §1.5, primer uso del
 * autologin). Exige sesión y que la persona confirme quién es en Horus DESPUÉS de canjear el
 * ticket (login o re-autenticación con su 2FA): si la sesión ya estaba abierta, el servidor
 * responde `REAUTH_REQUIRED` y se pide la contraseña. Muestra qué cuenta externa se vincula
 * para que un enlace ajeno no pase inadvertido. Nunca se vincula por correo.
 */
definePageMeta({ layout: 'auth' })

const { t, d } = useI18n()
const { $api } = useNuxtApp()
const auth = useAuth()
const { withReauth } = useReauth()
const { targetRoute } = useSso()

useHead({ title: () => t('sso.link.title') })

const pending = ref(readPendingLink())
const busy = ref(false)
const errorText = ref<string | null>(null)
const expired = ref(false)

const who = computed(() => {
  const u = pending.value?.externalUser
  if (!u) return ''
  return [u.name, u.email].filter(Boolean).join(' · ') || u.id
})

async function link() {
  if (!pending.value) return
  busy.value = true
  errorText.value = null
  try {
    const token = pending.value.linkToken
    const res = await withReauth(
      () => unwrap($api.POST('/me/sso-links', { body: { link_token: token } })),
      t('sso.link.reauth'),
    )
    clearPendingLink()
    const { path } = await targetRoute(res.target)
    await navigateTo(path, { replace: true })
  } catch (error) {
    if (error instanceof ApiError && (error.status === 410 || error.code === 'SSO_LINK_INVALID')) {
      clearPendingLink()
      expired.value = true
    } else if (error instanceof ApiError && error.code === 'SSO_NOT_A_MEMBER') {
      errorText.value = t('sso.link.notMember')
    } else if (error instanceof ApiError && error.code === 'SSO_LINK_EXISTS') {
      errorText.value = t('sso.link.exists')
    } else if (isReauthRequired(error)) {
      errorText.value = t('sso.link.reauthCancelled')
    } else {
      errorText.value = t(describeError(error).messageKey)
    }
  } finally {
    busy.value = false
  }
}

function cancel() {
  clearPendingLink()
  pending.value = null
  return navigateTo('/', { replace: true })
}
</script>

<template>
  <UCard
    variant="outline"
    class="shadow-sm"
    :ui="{ body: 'p-6 sm:p-8 space-y-5' }"
    data-testid="sso-link"
  >
    <header class="space-y-1">
      <h1 class="text-highlighted text-xl font-semibold">{{ t('sso.link.title') }}</h1>
      <p class="text-muted text-sm">{{ t('sso.link.description') }}</p>
    </header>

    <UAlert
      v-if="!pending || expired"
      color="warning"
      variant="subtle"
      icon="i-lucide-link-2-off"
      :title="t('sso.link.missing.title')"
      :description="t('sso.link.missing.body')"
      data-testid="sso-link-missing"
    />

    <template v-else>
      <dl class="divide-default divide-y text-sm">
        <div class="flex flex-wrap justify-between gap-2 py-2.5">
          <dt class="text-muted">{{ t('sso.link.external') }}</dt>
          <dd class="text-highlighted min-w-0 break-words" data-testid="sso-link-external">
            {{ who }}
          </dd>
        </div>
        <div class="flex flex-wrap justify-between gap-2 py-2.5">
          <dt class="text-muted">{{ t('sso.link.local') }}</dt>
          <dd class="text-highlighted min-w-0 break-words">{{ auth.me.value?.email }}</dd>
        </div>
        <div class="flex flex-wrap justify-between gap-2 py-2.5">
          <dt class="text-muted">{{ t('sso.link.expires') }}</dt>
          <dd>{{ d(new Date(pending.linkExpiresAt), 'long') }}</dd>
        </div>
      </dl>
      <p class="text-muted text-xs">{{ t('sso.link.hint') }}</p>
      <div class="flex flex-col gap-2 sm:flex-row-reverse">
        <UButton
          icon="i-lucide-link"
          :loading="busy"
          :label="t('sso.link.confirm')"
          class="justify-center"
          data-testid="sso-link-confirm"
          @click="link"
        />
        <UButton
          color="neutral"
          variant="ghost"
          :label="t('common.cancel')"
          class="justify-center"
          data-testid="sso-link-cancel"
          @click="cancel"
        />
      </div>
    </template>

    <UAlert
      v-if="errorText"
      color="error"
      variant="subtle"
      icon="i-lucide-circle-alert"
      :description="errorText"
      role="alert"
      data-testid="sso-link-error"
    />
  </UCard>
</template>
