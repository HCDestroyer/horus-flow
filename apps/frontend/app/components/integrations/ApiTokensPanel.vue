<script setup lang="ts">
import type { ApiToken, ApiTokenIssued } from '~~/types/api'

/**
 * API tokens de integración `hf_pat_` del ISP (docs/security.md §5.4, ADR-0030). El token se
 * muestra una sola vez; solo se guarda su SHA-256. La plantilla "ArgosISP" es el código de
 * conexión que se pega en ArgosISP. Crear exige re-autenticación reciente.
 */
const { t, d } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
const { withReauth } = useReauth()
const can = useCan()

const {
  data: tokens,
  error,
  status,
  refresh,
} = useTenantQuery('api-tokens', () => unwrap($api.GET('/api-tokens')).then((r) => r.data))

const EXPIRIES = [365, 180, 90, 30] as const
const form = reactive({
  name: 'ArgosISP',
  template: 'argosisp' as 'argosisp' | 'custom',
  permissions: [] as string[],
  days: 365 as (typeof EXPIRIES)[number],
})
const formError = ref('')
const creating = ref(false)
const issued = ref<ApiTokenIssued | null>(null)

const templateItems = computed(() => [
  { label: t('integrations.tokens.templateArgos'), value: 'argosisp' },
  { label: t('integrations.tokens.templateCustom'), value: 'custom' },
])
const expiryItems = computed(() =>
  EXPIRIES.map((n) => ({ label: t('integrations.tokens.expiryDays', { n }), value: n })),
)
// Solo los permisos de lectura que la persona tiene: un token nunca lleva más que su creador.
const READ_PERMISSIONS = [
  'customers.read',
  'traffic.customer.read',
  'traffic.read',
  'security.findings.read',
  'snmp.read',
  'devices.read',
  'sites.read',
  'flows.read',
  'alerts.read',
  'reports.read',
]
const permissionItems = computed(() =>
  READ_PERMISSIONS.filter((p) => can(p)).map((p) => ({ label: p, value: p })),
)

async function create() {
  formError.value = ''
  creating.value = true
  const expires = new Date(Date.now() + form.days * 86_400_000).toISOString()
  try {
    issued.value = await withReauth(() =>
      unwrap(
        $api.POST('/api-tokens', {
          body: {
            name: form.name.trim(),
            expires_at: expires,
            ...(form.template === 'argosisp'
              ? { template: 'argosisp' as const }
              : { permissions: form.permissions }),
          },
        }),
      ),
    )
    refresh()
  } catch (e) {
    formError.value =
      e instanceof ApiError && e.problem.errors?.length
        ? e.problem.errors[0]!.message
        : t(describeError(e).messageKey)
  } finally {
    creating.value = false
  }
}

async function revoke(tk: ApiToken) {
  try {
    await unwrap(
      $api.DELETE('/api-tokens/{api_token_id}', { params: { path: { api_token_id: tk.id } } }),
    )
    toast.add({ title: t('integrations.tokens.revoked'), icon: 'i-lucide-check', color: 'success' })
    refresh()
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  }
}

function lastUse(tk: ApiToken) {
  if (!tk.last_used_at) return t('integrations.tokens.neverUsed')
  const when = d(new Date(tk.last_used_at), 'long')
  return tk.last_used_ip
    ? t('integrations.tokens.lastUsedFrom', { time: when, ip: tk.last_used_ip })
    : t('integrations.tokens.lastUsed', { time: when })
}
</script>

<template>
  <section class="flex flex-col gap-3" data-testid="api-tokens">
    <div>
      <h2 class="text-highlighted font-semibold">{{ t('integrations.tokens.title') }}</h2>
      <p class="text-muted text-sm">{{ t('integrations.tokens.body') }}</p>
    </div>
    <UCard :ui="{ body: 'p-4 sm:p-4' }">
      <form class="grid grid-cols-1 gap-3 lg:grid-cols-2" @submit.prevent="create">
        <UFormField :label="t('integrations.tokens.name')" required>
          <UInput v-model="form.name" class="w-full" data-testid="api-token-name" />
        </UFormField>
        <UFormField :label="t('integrations.tokens.template')">
          <USelect
            v-model="form.template"
            :items="templateItems"
            class="w-full"
            data-testid="api-token-template"
          />
        </UFormField>
        <UFormField
          v-if="form.template === 'custom'"
          class="lg:col-span-2"
          :label="t('integrations.tokens.permissions')"
          :help="t('integrations.tokens.permissionsHelp')"
        >
          <USelectMenu
            v-model="form.permissions"
            :items="permissionItems"
            value-key="value"
            multiple
            class="w-full"
          />
        </UFormField>
        <p v-else class="text-muted text-sm lg:col-span-2">
          {{ t('integrations.tokens.argosPermissions') }}
        </p>
        <UFormField :label="t('integrations.tokens.expiry')">
          <USelect v-model="form.days" :items="expiryItems" class="w-full" />
        </UFormField>
        <div class="flex items-end justify-end">
          <UButton
            type="submit"
            icon="i-lucide-key-round"
            :loading="creating"
            :disabled="
              !form.name.trim() || (form.template === 'custom' && !form.permissions.length)
            "
            :label="
              form.template === 'argosisp'
                ? t('integrations.tokens.createArgos')
                : t('integrations.tokens.create')
            "
            data-testid="api-token-create"
          />
        </div>
        <UAlert
          v-if="formError"
          class="lg:col-span-2"
          color="error"
          variant="subtle"
          :title="formError"
        />
      </form>
    </UCard>
    <template v-if="issued">
      <UAlert
        color="warning"
        variant="subtle"
        icon="i-lucide-key-round"
        :title="t('integrations.tokens.created')"
        :description="t('integrations.tokens.createdHelp')"
        data-testid="api-token-issued"
      />
      <CodeBlock :code="issued.token" :label="t('integrations.tokens.value')" />
    </template>

    <LoadingState v-if="status === 'pending' && !tokens" :rows="2" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <p v-else-if="!tokens?.length" class="text-muted text-sm">
      {{ t('integrations.tokens.empty') }}
    </p>
    <ul
      v-else
      class="divide-default ring-default divide-y rounded-md ring-1"
      data-testid="api-token-list"
    >
      <li
        v-for="tk in tokens"
        :key="tk.id"
        class="flex flex-wrap items-center justify-between gap-2 px-3 py-2 text-sm"
      >
        <div class="min-w-0">
          <p class="text-highlighted flex flex-wrap items-center gap-2">
            {{ tk.name }}
            <span class="text-muted font-mono text-xs">{{ tk.prefix }}…</span>
            <UBadge
              v-if="tk.template === 'argosisp'"
              color="primary"
              variant="subtle"
              size="sm"
              label="ArgosISP"
            />
            <UBadge
              :color="tk.active ? 'success' : 'neutral'"
              variant="subtle"
              size="sm"
              :label="
                tk.revoked_at
                  ? t('integrations.tokens.stateRevoked')
                  : tk.active
                    ? t('integrations.tokens.stateActive')
                    : t('integrations.tokens.stateExpired')
              "
            />
          </p>
          <p class="text-muted text-xs">
            {{ t('integrations.tokens.permissionsCount', { n: tk.permissions.length }) }} ·
            {{
              tk.revoked_at
                ? t('integrations.tokens.revokedAt', { time: d(new Date(tk.revoked_at), 'long') })
                : t('integrations.tokens.expires', { time: d(new Date(tk.expires_at), 'long') })
            }}
            · {{ lastUse(tk) }}
          </p>
        </div>
        <UButton
          v-if="tk.active"
          size="sm"
          color="neutral"
          variant="ghost"
          icon="i-lucide-ban"
          :label="t('integrations.tokens.revoke')"
          @click="revoke(tk)"
        />
      </li>
    </ul>
  </section>
</template>
