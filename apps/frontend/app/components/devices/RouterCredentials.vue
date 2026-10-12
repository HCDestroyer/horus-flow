<script setup lang="ts">
import type { CredentialStatus, Router } from '~~/types/api'

/**
 * Credenciales de lectura del router (`PUT /routers/{id}/credentials/{kind}`, write-only y
 * `x-reauth`): SNMPv3 authPriv y el usuario de solo lectura de la API de RouterOS. Nunca se
 * muestra un secreto guardado; solo el estado (configurada, cuándo, último uso y resultado).
 * Horus solo lee del router: se recomienda un grupo de RouterOS sin escritura (comandos con
 * "Copiar"). Si el backend pide confirmar identidad, se abre `ReauthModal` y se reintenta.
 */
const props = defineProps<{ router: Router }>()
const emit = defineEmits<{ refresh: [] }>()

const { t } = useI18n()
const { $api } = useNuxtApp()
const can = useCan()
const toast = useToast()
const { withReauth } = useReauth()
const canWrite = computed(() => can('devices.credentials.write'))

type Kind = CredentialStatus['kind']
const saved = reactive<Record<Kind, CredentialStatus | null>>({ snmp: null, routeros_api: null })
const status = (kind: Kind) => credentialStatus(props.router, kind, saved[kind])

const snmp = reactive({
  username: '',
  authProtocol: 'sha1' as 'sha1' | 'sha256',
  authPassword: '',
  privPassword: '',
})
const api = reactive({ username: '', password: '', fingerprint: '', forgetFingerprint: false })

const fieldErrs = reactive<{ snmp: Record<string, string>; routeros_api: Record<string, string> }>({
  snmp: {},
  routeros_api: {},
})
const failure = reactive<Record<Kind, unknown>>({ snmp: null, routeros_api: null })
const needsReauth = reactive<Record<Kind, boolean>>({ snmp: false, routeros_api: false })
const saving = ref<Kind | null>(null)

function validate(kind: Kind) {
  const e: Record<string, string> = {}
  if (kind === 'snmp') {
    if (!snmp.username.trim()) e.username = t('credentials.usernameRequired')
    if (snmp.authPassword.length < SNMP_SECRET_MIN)
      e.auth_password = t('credentials.minLength', { n: SNMP_SECRET_MIN })
    if (snmp.privPassword.length < SNMP_SECRET_MIN)
      e.priv_password = t('credentials.minLength', { n: SNMP_SECRET_MIN })
  } else {
    if (!api.username.trim()) e.username = t('credentials.usernameRequired')
    if (api.password.length < ROUTEROS_API_PASSWORD_MIN)
      e.password = t('credentials.minLength', { n: ROUTEROS_API_PASSWORD_MIN })
    if (!api.forgetFingerprint && api.fingerprint.trim() && !normalizeFingerprint(api.fingerprint))
      e.tls_fingerprint_sha256 = t('credentials.api.fingerprintInvalid')
  }
  fieldErrs[kind] = e
  return Object.keys(e).length === 0
}

function body(kind: Kind) {
  if (kind === 'snmp') {
    return {
      username: snmp.username.trim(),
      auth_protocol: snmp.authProtocol,
      auth_password: snmp.authPassword,
      priv_protocol: 'aes128' as const,
      priv_password: snmp.privPassword,
    }
  }
  const out: { username: string; password: string; tls_fingerprint_sha256?: string | null } = {
    username: api.username.trim(),
    password: api.password,
  }
  // Sin el campo se conserva la huella fijada; `null` la olvida (se fija en el próximo contacto).
  if (api.forgetFingerprint) out.tls_fingerprint_sha256 = null
  else if (api.fingerprint.trim())
    out.tls_fingerprint_sha256 = normalizeFingerprint(api.fingerprint)
  return out
}

async function save(kind: Kind) {
  if (!validate(kind)) return
  saving.value = kind
  failure[kind] = null
  needsReauth[kind] = false
  try {
    const result = await withReauth(
      () =>
        unwrap(
          $api.PUT('/routers/{router_id}/credentials/{kind}', {
            params: { path: { router_id: props.router.id, kind } },
            body: body(kind),
          }),
        ),
      t('credentials.reauthDescription'),
    )
    saved[kind] = result
    if (kind === 'snmp') Object.assign(snmp, { authPassword: '', privPassword: '' })
    else Object.assign(api, { password: '', fingerprint: '', forgetFingerprint: false })
    toast.add({
      title: t('credentials.saved', { kind: t(`credentials.${kind}.title`) }),
      icon: 'i-lucide-check',
      color: 'success',
    })
    emit('refresh')
  } catch (e) {
    if (isReauthRequired(e)) needsReauth[kind] = true
    else if (e instanceof ApiError && e.code === 'VALIDATION_FAILED') {
      fieldErrs[kind] = fieldErrors(e.problem)
      if (fieldErrs[kind]._) failure[kind] = e
    } else failure[kind] = e
  } finally {
    saving.value = null
  }
}

const snippet = computed(() =>
  routerOsReadOnlySnippet({
    apiUser: api.username.trim() || 'horus-ro',
    snmpUser: snmp.username.trim() || 'horus-snmp',
  }),
)
const authItems = [
  { label: 'SHA-1', value: 'sha1' },
  { label: 'SHA-256', value: 'sha256' },
]
const KINDS: Kind[] = ['snmp', 'routeros_api']
</script>

<template>
  <section class="flex flex-col gap-4" data-testid="router-credentials">
    <UCard>
      <div class="flex gap-3">
        <UIcon name="i-lucide-eye" class="text-primary mt-0.5 size-6 shrink-0" aria-hidden="true" />
        <div class="flex min-w-0 flex-col gap-2">
          <h2 class="text-highlighted font-semibold">{{ t('credentials.readOnlyTitle') }}</h2>
          <p class="text-default text-sm">{{ t('credentials.readOnlyBody') }}</p>
          <p class="text-muted text-sm">{{ t('credentials.scriptNote') }}</p>
          <p class="text-muted flex items-center gap-1.5 text-sm">
            <UIcon name="i-lucide-eye-off" class="size-4 shrink-0" aria-hidden="true" />
            {{ t('credentials.neverShown') }}
          </p>
        </div>
      </div>
    </UCard>

    <UCard>
      <div class="flex flex-col gap-3">
        <h2 class="text-highlighted font-semibold">{{ t('credentials.snippetTitle') }}</h2>
        <p class="text-muted text-sm">{{ t('credentials.snippetBody') }}</p>
        <CodeBlock
          :code="snippet"
          :label="t('credentials.snippetLabel')"
          max-height="16rem"
          data-testid="credentials-snippet"
        />
      </div>
    </UCard>

    <div class="grid gap-4 xl:grid-cols-2">
      <UCard v-for="kind in KINDS" :key="kind" :data-testid="`credential-${kind}`">
        <div class="flex flex-col gap-4">
          <div class="flex flex-wrap items-start justify-between gap-2">
            <div class="flex min-w-0 flex-col gap-0.5">
              <h3 class="text-highlighted font-semibold">{{ t(`credentials.${kind}.title`) }}</h3>
              <p class="text-muted text-sm">{{ t(`credentials.${kind}.body`) }}</p>
            </div>
            <UBadge
              :color="status(kind).configured ? 'success' : 'neutral'"
              variant="subtle"
              :icon="status(kind).configured ? 'i-lucide-circle-check' : 'i-lucide-circle-dashed'"
              :label="
                status(kind).configured
                  ? t('credentials.configured')
                  : t('credentials.notConfigured')
              "
              data-testid="credential-status"
            />
          </div>
          <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-sm">
            <dt class="text-muted">{{ t('credentials.updatedAt') }}</dt>
            <dd class="text-default">
              <RelativeTime v-if="status(kind).updated_at" :at="status(kind).updated_at!" />
              <span v-else>—</span>
            </dd>
            <dt class="text-muted">{{ t('credentials.lastUsed') }}</dt>
            <dd class="text-default">
              <RelativeTime v-if="status(kind).last_used_at" :at="status(kind).last_used_at!" />
              <span v-else>{{ t('common.never') }}</span>
            </dd>
            <dt class="text-muted">{{ t('credentials.lastResult') }}</dt>
            <dd
              :class="
                status(kind).last_result && status(kind).last_result !== 'ok'
                  ? 'text-error'
                  : 'text-default'
              "
              data-testid="credential-result"
            >
              {{
                status(kind).last_result ? t(`credentials.result.${status(kind).last_result}`) : '—'
              }}
            </dd>
          </dl>

          <form
            v-if="canWrite"
            class="flex flex-col gap-4"
            novalidate
            :data-testid="`credential-form-${kind}`"
            @submit.prevent="save(kind)"
          >
            <template v-if="kind === 'snmp'">
              <UFormField
                :label="t('credentials.username')"
                :error="fieldErrs.snmp.username"
                required
              >
                <UInput
                  v-model="snmp.username"
                  class="w-full"
                  autocomplete="off"
                  spellcheck="false"
                  placeholder="horus-snmp"
                  :ui="{ base: 'font-mono' }"
                  data-testid="snmp-username"
                />
              </UFormField>
              <div class="grid gap-4 sm:grid-cols-2">
                <UFormField :label="t('credentials.snmp.authProtocol')">
                  <USelect v-model="snmp.authProtocol" :items="authItems" class="w-full" />
                </UFormField>
                <UFormField :label="t('credentials.snmp.privProtocol')">
                  <UInput model-value="AES-128" class="w-full" disabled />
                </UFormField>
              </div>
              <UFormField
                :label="t('credentials.snmp.authPassword')"
                :help="t('credentials.minLength', { n: SNMP_SECRET_MIN })"
                :error="fieldErrs.snmp.auth_password"
                required
              >
                <SecretInput v-model="snmp.authPassword" data-testid="snmp-auth-password" />
              </UFormField>
              <UFormField
                :label="t('credentials.snmp.privPassword')"
                :help="t('credentials.minLength', { n: SNMP_SECRET_MIN })"
                :error="fieldErrs.snmp.priv_password"
                required
              >
                <SecretInput v-model="snmp.privPassword" data-testid="snmp-priv-password" />
              </UFormField>
            </template>
            <template v-else>
              <UFormField
                :label="t('credentials.username')"
                :help="t('credentials.api.usernameHelp')"
                :error="fieldErrs.routeros_api.username"
                required
              >
                <UInput
                  v-model="api.username"
                  class="w-full"
                  autocomplete="off"
                  spellcheck="false"
                  placeholder="horus-ro"
                  :ui="{ base: 'font-mono' }"
                  data-testid="api-username"
                />
              </UFormField>
              <UFormField
                :label="t('credentials.api.password')"
                :help="t('credentials.minLength', { n: ROUTEROS_API_PASSWORD_MIN })"
                :error="fieldErrs.routeros_api.password"
                required
              >
                <SecretInput v-model="api.password" data-testid="api-password" />
              </UFormField>
              <UFormField
                :label="t('credentials.api.fingerprint')"
                :hint="t('common.optional')"
                :help="t('credentials.api.fingerprintHelp')"
                :error="fieldErrs.routeros_api.tls_fingerprint_sha256"
              >
                <UInput
                  v-model="api.fingerprint"
                  class="w-full"
                  autocomplete="off"
                  spellcheck="false"
                  :disabled="api.forgetFingerprint"
                  :ui="{ base: 'font-mono text-xs' }"
                  placeholder="AA:BB:CC:…"
                  data-testid="api-fingerprint"
                />
              </UFormField>
              <UCheckbox
                v-model="api.forgetFingerprint"
                :label="t('credentials.api.forgetFingerprint')"
              />
            </template>

            <UAlert
              v-if="needsReauth[kind]"
              color="warning"
              variant="subtle"
              icon="i-lucide-lock-keyhole"
              :title="t('credentials.reauthNeeded')"
              :data-testid="`credential-reauth-${kind}`"
            />
            <ErrorState v-if="failure[kind]" :error="failure[kind]" @retry="save(kind)" />
            <div class="flex justify-end">
              <UButton
                type="submit"
                icon="i-lucide-key-round"
                :loading="saving === kind"
                :label="
                  status(kind).configured ? t('credentials.replace') : t('credentials.saveAction')
                "
                :data-testid="`credential-save-${kind}`"
              />
            </div>
          </form>
          <p v-else class="text-muted text-sm">{{ t('credentials.noPermission') }}</p>
        </div>
      </UCard>
    </div>
  </section>
</template>
