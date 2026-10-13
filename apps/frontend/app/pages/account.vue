<script setup lang="ts">
/**
 * Mi cuenta (fuera de la barra, frontend.md §4): datos de la cuenta (nombre, zona horaria),
 * correo (exige re-autenticación reciente, `useReauth`), cambio de contraseña y alta del TOTP.
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
const toast = useToast()
const { withReauth } = useReauth()

const required = computed(() => {
  const r = route.query.required
  return r === 'password' || r === 'mfa' || r === 'mfa-login' ? r : null
})

// --- Datos de la cuenta -----------------------------------------------------------------------
const profile = reactive({ name: '', timezone: '' })
const profileBusy = ref(false)
const profileError = ref<string | null>(null)
const profileDone = ref(false)

function allZones() {
  try {
    const intl = Intl as unknown as { supportedValuesOf?: (key: string) => string[] }
    return intl.supportedValuesOf?.('timeZone') ?? []
  } catch {
    return []
  }
}
const zoneItems = ref(allZones())
/** Navegador sin `Intl.supportedValuesOf`: la zona se escribe a mano. */
const zonesAvailable = computed(() => zoneItems.value.length > 0)
function addZone(zone: string) {
  const value = zone.trim()
  if (!value) return
  if (!zoneItems.value.includes(value)) zoneItems.value = [value, ...zoneItems.value]
  profile.timezone = value
}

function fillProfile() {
  profile.name = me.value?.display_name ?? ''
  profile.timezone = me.value?.timezone ?? ''
  if (profile.timezone && !zoneItems.value.includes(profile.timezone)) {
    zoneItems.value = [profile.timezone, ...zoneItems.value]
  }
}
watch(me, fillProfile, { immediate: true })

const profileChanged = computed(
  () =>
    profile.name.trim() !== (me.value?.display_name ?? '') ||
    (profile.timezone.trim() || null) !== (me.value?.timezone ?? null),
)

async function saveProfile() {
  const name = profile.name.trim()
  if (!name) {
    profileError.value = t('account.profile.nameRequired')
    return
  }
  if (!profileChanged.value) return
  profileBusy.value = true
  profileError.value = null
  profileDone.value = false
  try {
    const body: { display_name?: string; timezone?: string | null } = {}
    if (name !== me.value?.display_name) body.display_name = name
    const zone = profile.timezone.trim() || null
    if (zone !== (me.value?.timezone ?? null)) body.timezone = zone
    await unwrap($api.PATCH('/me', { body }))
    await auth.reloadMe()
    profileDone.value = true
    toast.add({ title: t('account.profile.saved'), color: 'success', icon: 'i-lucide-check' })
  } catch (error) {
    profileError.value = errorMessage(error)
  } finally {
    profileBusy.value = false
  }
}

// --- Correo (re-autenticación) ------------------------------------------------------------------
const emailForm = reactive({ email: '' })
const emailBusy = ref(false)
const emailError = ref<string | null>(null)
const emailDone = ref<string | null>(null)
const emailInvalid = computed(() => {
  const value = emailForm.email.trim()
  if (!value) return null
  if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(value)) return t('account.emailChange.invalid')
  if (value.toLowerCase() === me.value?.email.toLowerCase()) return t('account.emailChange.same')
  return null
})

async function changeEmail() {
  const email = emailForm.email.trim()
  if (!email || emailInvalid.value) return
  emailBusy.value = true
  emailError.value = null
  emailDone.value = null
  try {
    await withReauth(
      () => unwrap($api.PATCH('/me', { body: { email } })),
      t('account.emailChange.reauthBody'),
    )
    await auth.reloadMe()
    emailDone.value = t('account.emailChange.saved', { email: me.value?.email ?? email })
    emailForm.email = ''
  } catch (error) {
    if (isReauthRequired(error)) emailError.value = t('account.emailChange.reauthCancelled')
    else if (error instanceof ApiError && error.code === 'ALREADY_EXISTS') {
      emailError.value = t('account.emailChange.exists')
    } else emailError.value = errorMessage(error)
  } finally {
    emailBusy.value = false
  }
}

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
    if (error.status === 422) {
      return error.problem.errors?.[0]?.message ?? t('account.errors.invalid')
    }
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

      <!-- Datos de la cuenta -->
      <UCard data-testid="profile-card">
        <h2 class="text-highlighted text-base font-semibold">{{ t('account.profile.title') }}</h2>
        <p class="text-muted mt-1 text-sm">{{ t('account.profile.hint') }}</p>
        <form class="mt-4 flex flex-col gap-3" novalidate @submit.prevent="saveProfile">
          <UFormField :label="t('account.profile.name')" name="display_name" required>
            <UInput
              v-model="profile.name"
              autocomplete="name"
              maxlength="120"
              class="w-full"
              data-testid="profile-name"
            />
          </UFormField>
          <UFormField
            :label="t('account.profile.timezone')"
            name="timezone"
            :help="t('account.profile.timezoneHint')"
          >
            <USelectMenu
              v-if="zonesAvailable"
              v-model="profile.timezone"
              :items="zoneItems"
              create-item
              virtualize
              class="w-full"
              :search-input="{ placeholder: t('account.profile.timezoneSearch') }"
              data-testid="profile-timezone"
              @create="addZone"
            />
            <UInput
              v-else
              v-model="profile.timezone"
              autocomplete="off"
              spellcheck="false"
              class="w-full font-mono"
              data-testid="profile-timezone"
            />
          </UFormField>
          <p v-if="profileError" class="text-error text-sm" role="alert">{{ profileError }}</p>
          <p
            v-if="profileDone && !profileChanged"
            class="text-success text-sm"
            role="status"
            data-testid="profile-done"
          >
            {{ t('account.profile.saved') }}
          </p>
          <div>
            <UButton
              type="submit"
              :loading="profileBusy"
              :disabled="!profileChanged || !profile.name.trim()"
              data-testid="profile-submit"
            >
              {{ t('account.profile.submit') }}
            </UButton>
          </div>
        </form>
      </UCard>

      <!-- Correo -->
      <UCard data-testid="email-card">
        <h2 class="text-highlighted text-base font-semibold">
          {{ t('account.emailChange.title') }}
        </h2>
        <p class="text-muted mt-1 text-sm">{{ t('account.emailChange.hint') }}</p>
        <form class="mt-4 flex flex-col gap-3" novalidate @submit.prevent="changeEmail">
          <UFormField :label="t('account.email')" name="current_email">
            <UInput
              :model-value="me.email"
              readonly
              disabled
              class="w-full"
              data-testid="email-current"
            />
          </UFormField>
          <UFormField
            :label="t('account.emailChange.label')"
            name="email"
            :error="emailInvalid ?? undefined"
          >
            <UInput
              v-model="emailForm.email"
              type="email"
              autocomplete="off"
              inputmode="email"
              spellcheck="false"
              autocapitalize="none"
              class="w-full"
              data-testid="email-new"
            />
          </UFormField>
          <p v-if="emailError" class="text-error text-sm" role="alert" data-testid="email-error">
            {{ emailError }}
          </p>
          <p v-if="emailDone" class="text-success text-sm" role="status" data-testid="email-done">
            {{ emailDone }}
          </p>
          <div>
            <UButton
              type="submit"
              icon="i-lucide-lock-keyhole"
              :loading="emailBusy"
              :disabled="!emailForm.email.trim() || !!emailInvalid"
              data-testid="email-submit"
            >
              {{ t('account.emailChange.submit') }}
            </UButton>
          </div>
        </form>
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
