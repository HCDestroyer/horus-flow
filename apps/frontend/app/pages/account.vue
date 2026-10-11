<script setup lang="ts">
/**
 * Mi cuenta (fuera de la barra, frontend.md §4): cambio de contraseña y alta del TOTP.
 * El superadministrador recién instalado llega aquí obligado (`?required=password|mfa`): el
 * backend no emite tokens de plataforma ni de ISP hasta que cambie la contraseña inicial
 * (PASSWORD_CHANGE_REQUIRED) y, para la plataforma, active el TOTP (MFA_ENROLLMENT_REQUIRED).
 */
import { renderSVG } from 'uqr'

const { t } = useI18n()
const { $api } = useNuxtApp()
const auth = useAuth()
const { me } = auth
const route = useRoute()

const required = computed(() => {
  const r = route.query.required
  return r === 'password' || r === 'mfa' || r === 'mfa-login' ? r : null
})

// --- Contraseña ---------------------------------------------------------------------------------
const pw = reactive({ current: '', next: '', repeat: '' })
const pwBusy = ref(false)
const pwError = ref<string | null>(null)
const pwDone = ref(false)
const pwInvalid = computed(() => {
  if (pw.next && pw.next.length < 12) return t('account.password.tooShort')
  if (pw.repeat && pw.next !== pw.repeat) return t('account.password.mismatch')
  return null
})

async function changePassword() {
  if (pwInvalid.value || !pw.current || !pw.next) return
  pwBusy.value = true
  pwError.value = null
  try {
    await unwrap(
      $api.POST('/me/password', { body: { current_password: pw.current, new_password: pw.next } }),
    )
    pw.current = pw.next = pw.repeat = ''
    pwDone.value = true
    await auth.reloadMe()
    await continueIfReady()
  } catch (error) {
    pwError.value = errorMessage(error)
  } finally {
    pwBusy.value = false
  }
}

// --- TOTP --------------------------------------------------------------------------------------
const enrollment = ref<{ secret: string; uri: string; qr: string } | null>(null)
const code = ref('')
const totpBusy = ref(false)
const totpError = ref<string | null>(null)
const recoveryCodes = ref<string[] | null>(null)

async function startTotp() {
  totpBusy.value = true
  totpError.value = null
  try {
    const res = await unwrap($api.POST('/me/totp/enroll'))
    enrollment.value = {
      secret: res.secret,
      uri: res.otpauth_uri,
      qr: `data:image/svg+xml;utf8,${encodeURIComponent(renderSVG(res.otpauth_uri, { border: 2 }))}`,
    }
  } catch (error) {
    totpError.value = errorMessage(error)
  } finally {
    totpBusy.value = false
  }
}

async function confirmTotp() {
  if (!/^\d{6,8}$/.test(code.value.trim())) {
    totpError.value = t('account.mfaSetup.codeInvalid')
    return
  }
  totpBusy.value = true
  totpError.value = null
  try {
    const res = await unwrap($api.POST('/me/totp/confirm', { body: { code: code.value.trim() } }))
    recoveryCodes.value = res.recovery_codes
    enrollment.value = null
    code.value = ''
    await auth.reloadMe()
  } catch (error) {
    totpError.value = errorMessage(error)
  } finally {
    totpBusy.value = false
  }
}

function downloadRecoveryCodes() {
  if (!recoveryCodes.value) return
  const text = `${t('account.mfaSetup.recoveryFileHeader', { email: me.value?.email ?? '' })}\n\n${recoveryCodes.value.join('\n')}\n`
  const url = URL.createObjectURL(new Blob([text], { type: 'text/plain' }))
  const a = document.createElement('a')
  a.href = url
  a.download = 'horus-codigos-recuperacion.txt'
  a.click()
  URL.revokeObjectURL(url)
}

/** Tras completar lo obligatorio, vuelve a la consola de plataforma o al inicio. */
async function continueIfReady() {
  if (!me.value || me.value.must_change_password) return
  if (isPlatformUser(me.value) && !me.value.mfa_enabled) return
  if (required.value) await navigateTo(isPlatformUser(me.value) ? '/platform/system' : '/')
}

function errorMessage(error: unknown) {
  if (error instanceof ApiError) {
    if (error.status === 401 || error.status === 403) return t('account.errors.wrongCredentials')
    if (error.status === 422) return t('account.errors.invalid')
    return error.problem.detail ?? error.problem.title ?? error.code
  }
  return t('account.errors.network')
}
</script>

<template>
  <AppPage :title="t('account.title')" panel-id="account">
    <div v-if="me" class="flex max-w-xl flex-col gap-4">
      <UAlert
        v-if="required"
        color="warning"
        variant="subtle"
        icon="i-lucide-shield-alert"
        :title="t(`account.required.${required}.title`)"
        :description="t(`account.required.${required}.body`)"
        data-testid="account-required"
      />

      <UCard>
        <div class="mb-4 flex items-center gap-3">
          <UAvatar :alt="me.display_name" size="lg" />
          <div class="min-w-0">
            <p class="text-highlighted truncate font-semibold">{{ me.display_name }}</p>
            <p class="text-muted truncate text-sm">{{ me.email }}</p>
          </div>
        </div>
        <dl class="divide-default divide-y text-sm">
          <div class="flex justify-between gap-4 py-2.5">
            <dt class="text-muted">{{ t('account.mfa') }}</dt>
            <dd>
              <UBadge
                :color="me.mfa_enabled ? 'success' : 'neutral'"
                variant="subtle"
                :icon="me.mfa_enabled ? 'i-lucide-shield-check' : 'i-lucide-shield-off'"
                :label="me.mfa_enabled ? t('account.mfaOn') : t('account.mfaOff')"
                data-testid="mfa-status"
              />
            </dd>
          </div>
          <div class="flex justify-between gap-4 py-2.5">
            <dt class="text-muted">{{ t('account.timeZone') }}</dt>
            <dd class="font-mono">{{ me.timezone ?? '—' }}</dd>
          </div>
        </dl>
      </UCard>

      <!-- Contraseña -->
      <UCard data-testid="password-card">
        <h2 class="text-highlighted text-base font-semibold">{{ t('account.password.title') }}</h2>
        <p class="text-muted mt-1 text-sm">{{ t('account.password.hint') }}</p>
        <form class="mt-4 flex flex-col gap-3" @submit.prevent="changePassword">
          <UFormField :label="t('account.password.current')" name="current">
            <UInput
              v-model="pw.current"
              type="password"
              autocomplete="current-password"
              class="w-full"
              data-testid="pw-current"
            />
          </UFormField>
          <UFormField :label="t('account.password.new')" name="new" :error="pwInvalid ?? undefined">
            <UInput
              v-model="pw.next"
              type="password"
              autocomplete="new-password"
              class="w-full"
              data-testid="pw-new"
            />
          </UFormField>
          <UFormField :label="t('account.password.repeat')" name="repeat">
            <UInput
              v-model="pw.repeat"
              type="password"
              autocomplete="new-password"
              class="w-full"
              data-testid="pw-repeat"
            />
          </UFormField>
          <p v-if="pwError" class="text-error text-sm" role="alert">{{ pwError }}</p>
          <p v-if="pwDone" class="text-success text-sm" role="status" data-testid="pw-done">
            {{ t('account.password.done') }}
          </p>
          <div>
            <UButton
              type="submit"
              :loading="pwBusy"
              :disabled="!pw.current || !pw.next || !pw.repeat || !!pwInvalid"
              data-testid="pw-submit"
            >
              {{ t('account.password.submit') }}
            </UButton>
          </div>
        </form>
      </UCard>

      <!-- Verificación en dos pasos -->
      <UCard data-testid="mfa-card">
        <h2 class="text-highlighted text-base font-semibold">{{ t('account.mfaSetup.title') }}</h2>

        <template v-if="recoveryCodes">
          <UAlert
            class="mt-3"
            color="success"
            variant="subtle"
            icon="i-lucide-shield-check"
            :title="t('account.mfaSetup.enabled')"
            :description="t('account.mfaSetup.recoveryHint')"
          />
          <ul
            class="bg-elevated mt-3 grid grid-cols-2 gap-x-6 gap-y-1 rounded-md p-3 font-mono text-sm"
            data-testid="recovery-codes"
          >
            <li v-for="c in recoveryCodes" :key="c">{{ c }}</li>
          </ul>
          <div class="mt-3 flex flex-wrap gap-2">
            <UButton
              variant="outline"
              icon="i-lucide-download"
              data-testid="recovery-download"
              @click="downloadRecoveryCodes"
            >
              {{ t('account.mfaSetup.download') }}
            </UButton>
            <UButton
              v-if="required"
              icon="i-lucide-arrow-right"
              data-testid="account-continue"
              @click="continueIfReady"
            >
              {{ t('account.mfaSetup.continue') }}
            </UButton>
          </div>
        </template>

        <p v-else-if="me.mfa_enabled" class="text-muted mt-1 text-sm">
          {{ t('account.mfaSetup.alreadyOn') }}
        </p>

        <template v-else-if="enrollment">
          <p class="text-muted mt-1 text-sm">{{ t('account.mfaSetup.scan') }}</p>
          <div class="mt-3 flex flex-wrap items-start gap-4">
            <img
              :src="enrollment.qr"
              :alt="t('account.mfaSetup.qrLabel')"
              class="size-44 shrink-0 rounded-md bg-white p-1"
              data-testid="totp-qr"
            />
            <div class="min-w-0 flex-1 text-sm">
              <p class="text-muted">{{ t('account.mfaSetup.manual') }}</p>
              <code
                class="bg-elevated mt-1 block break-all rounded px-2 py-1"
                data-testid="totp-secret"
                >{{ enrollment.secret }}</code
              >
            </div>
          </div>
          <form class="mt-4 flex flex-wrap items-end gap-3" @submit.prevent="confirmTotp">
            <UFormField :label="t('account.mfaSetup.code')" name="code">
              <UInput
                v-model="code"
                inputmode="numeric"
                autocomplete="one-time-code"
                maxlength="8"
                class="w-40 font-mono"
                data-testid="totp-code"
              />
            </UFormField>
            <UButton type="submit" :loading="totpBusy" data-testid="totp-confirm">
              {{ t('account.mfaSetup.confirm') }}
            </UButton>
          </form>
        </template>

        <template v-else>
          <p class="text-muted mt-1 text-sm">{{ t('account.mfaSetup.intro') }}</p>
          <UButton
            class="mt-3"
            icon="i-lucide-shield-plus"
            :loading="totpBusy"
            :disabled="me.must_change_password"
            data-testid="totp-start"
            @click="startTotp"
          >
            {{ t('account.mfaSetup.start') }}
          </UButton>
          <p v-if="me.must_change_password" class="text-muted mt-2 text-xs">
            {{ t('account.mfaSetup.passwordFirst') }}
          </p>
        </template>

        <p v-if="totpError" class="text-error mt-3 text-sm" role="alert">{{ totpError }}</p>
      </UCard>
    </div>
  </AppPage>
</template>
