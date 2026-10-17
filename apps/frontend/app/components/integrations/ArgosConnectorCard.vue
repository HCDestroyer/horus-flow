<script setup lang="ts">
import type { ArgosConnectionTest, ArgosWebhookSecret } from '~~/types/api'

/**
 * Conector ArgosISP (ADR-0030, contrato docs/integrations/argosisp.md §1.2 y §2): URL base y
 * token `atk_…` de solo escritura (cifrado), URL y secreto del webhook para copiar en ArgosISP
 * (el secreto se muestra una vez), activo, prueba de conexión, última sincronización e
 * identidades desactualizadas. Guardar exige re-autenticación reciente.
 */
const { t, d } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
const { withReauth } = useReauth()
const can = useCan()
const canManage = computed(() => can('settings.manage'))

const {
  data: conn,
  error,
  status,
  refresh,
} = useTenantQuery('argos-integration', () => unwrap($api.GET('/integrations/argosisp')))

const form = reactive({ baseUrl: '', token: '', enabled: true })
const errors = ref<Record<string, string>>({})
watch(
  conn,
  (c) => {
    if (!c) return
    form.baseUrl = c.base_url ?? ''
    form.enabled = c.configured ? c.enabled : true
    form.token = ''
  },
  { immediate: true },
)

const saving = ref(false)
async function save() {
  errors.value = {}
  saving.value = true
  try {
    conn.value = await withReauth(() =>
      unwrap(
        $api.PUT('/integrations/argosisp', {
          body: {
            base_url: form.baseUrl.trim(),
            enabled: form.enabled,
            ...(form.token.trim() ? { token: form.token.trim() } : {}),
          },
        }),
      ),
    )
    form.token = ''
    toast.add({ title: t('integrations.argos.saved'), icon: 'i-lucide-check', color: 'success' })
  } catch (e) {
    if (e instanceof ApiError && e.problem.errors?.length) {
      errors.value = Object.fromEntries(e.problem.errors.map((x) => [x.field, x.message]))
    } else {
      errors.value = { _: t(describeError(e).messageKey) }
    }
  } finally {
    saving.value = false
  }
}

const secret = ref<ArgosWebhookSecret | null>(null)
const rotating = ref(false)
async function rotate() {
  rotating.value = true
  try {
    secret.value = await withReauth(() =>
      unwrap($api.POST('/integrations/argosisp/webhook-secret')),
    )
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  } finally {
    rotating.value = false
  }
}

const testing = ref(false)
const test = ref<ArgosConnectionTest | null>(null)
async function runTest() {
  testing.value = true
  test.value = null
  try {
    test.value = await unwrap($api.POST('/integrations/argosisp/connection-test'))
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  } finally {
    testing.value = false
  }
}

const SYNC_COLOR = { ok: 'success', degraded: 'warning', failing: 'error' } as const
</script>

<template>
  <section class="flex flex-col gap-3" data-testid="argos-connector">
    <div>
      <h2 class="text-highlighted font-semibold">{{ t('integrations.argos.title') }}</h2>
      <p class="text-muted text-sm">{{ t('integrations.argos.body') }}</p>
    </div>
    <LoadingState v-if="status === 'pending' && !conn" :rows="3" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <UCard v-else-if="conn" :ui="{ body: 'p-4 sm:p-4' }">
      <div class="flex flex-col gap-4">
        <div
          v-if="conn.configured"
          class="flex flex-wrap items-center gap-2 text-sm"
          data-testid="argos-sync"
        >
          <UBadge
            :color="conn.last_sync_status ? SYNC_COLOR[conn.last_sync_status] : 'neutral'"
            variant="subtle"
            :label="t(`integrations.argos.sync.${conn.last_sync_status ?? 'never'}`)"
          />
          <span v-if="conn.last_sync_at" class="text-muted">
            {{ t('integrations.argos.lastSync', { time: d(new Date(conn.last_sync_at), 'long') }) }}
          </span>
          <span v-if="conn.resolver === 'fallback'" class="text-muted">
            · {{ t('integrations.argos.fallback') }}
          </span>
          <span class="text-muted">
            ·
            {{
              t('integrations.argos.identities', {
                n: conn.identities,
                stale: conn.stale_identities,
              })
            }}
          </span>
        </div>
        <p v-if="conn.connection" class="text-muted text-sm" data-testid="argos-connection">
          {{
            t(`integrations.argos.connection.${conn.connection.method}`, {
              time: d(new Date(conn.connection.linked_at), 'long'),
            })
          }}
          <template v-if="!conn.enabled">
            · {{ t('integrations.argos.connection.activate') }}</template
          >
        </p>
        <UAlert
          v-if="conn.last_error"
          color="warning"
          variant="subtle"
          icon="i-lucide-triangle-alert"
          :title="conn.last_error"
          :description="t('integrations.argos.degradedHelp')"
        />
        <form class="grid grid-cols-1 gap-4 lg:grid-cols-2" @submit.prevent="save">
          <UFormField
            class="lg:col-span-2"
            :label="t('integrations.argos.baseUrl')"
            :help="t('integrations.argos.baseUrlHelp')"
            :error="errors.base_url"
            required
          >
            <UInput
              v-model="form.baseUrl"
              type="url"
              class="w-full"
              :disabled="!canManage"
              :ui="{ base: 'font-mono' }"
              placeholder="https://argos.isp.example/api/v1"
              data-testid="argos-base-url"
            />
          </UFormField>
          <UFormField
            class="lg:col-span-2"
            :label="t('integrations.argos.token')"
            :help="
              conn?.has_token
                ? t('integrations.argos.tokenKeep')
                : t('integrations.argos.tokenHelp')
            "
            :error="errors.token"
          >
            <SecretInput
              v-model="form.token"
              :disabled="!canManage"
              :placeholder="conn?.has_token ? t('integrations.argos.tokenSaved') : 'atk_…'"
              data-testid="argos-token"
            />
          </UFormField>
          <USwitch
            v-model="form.enabled"
            :disabled="!canManage"
            :label="t('integrations.argos.enabled')"
            :description="t('integrations.argos.enabledHelp')"
            data-testid="argos-enabled"
          />
          <div class="flex flex-wrap items-end justify-end gap-2">
            <UButton
              v-if="conn?.has_token"
              color="neutral"
              variant="outline"
              icon="i-lucide-plug-zap"
              :loading="testing"
              :disabled="!canManage"
              :label="t('integrations.argos.test')"
              data-testid="argos-test"
              @click="runTest"
            />
            <UButton
              type="submit"
              icon="i-lucide-save"
              :loading="saving"
              :disabled="!canManage || !form.baseUrl.trim()"
              :label="t('common.save')"
              data-testid="argos-save"
            />
          </div>
          <UAlert
            v-if="errors._"
            class="lg:col-span-2"
            color="error"
            variant="subtle"
            :title="errors._"
          />
        </form>
        <UAlert
          v-if="test"
          :color="test.ok ? 'success' : 'error'"
          variant="subtle"
          :icon="test.ok ? 'i-lucide-check' : 'i-lucide-x'"
          :title="
            test.ok
              ? t('integrations.argos.testOk')
              : (test.error ?? t('integrations.argos.testFail'))
          "
          :description="
            test.ok && test.resolver === 'fallback'
              ? t('integrations.argos.fallbackHelp')
              : undefined
          "
          data-testid="argos-test-result"
        />
        <div v-if="conn?.configured" class="flex flex-col gap-2">
          <h3 class="text-highlighted text-sm font-medium">
            {{ t('integrations.argos.webhookTitle') }}
          </h3>
          <p class="text-muted text-sm">{{ t('integrations.argos.webhookBody') }}</p>
          <CodeBlock
            v-if="conn?.webhook_url"
            :code="conn?.webhook_url ?? ''"
            :label="t('integrations.argos.webhookUrl')"
          />
          <template v-if="secret">
            <UAlert
              color="warning"
              variant="subtle"
              icon="i-lucide-key-round"
              :title="t('integrations.argos.secretShown')"
            />
            <CodeBlock :code="secret.webhook_secret" :label="t('integrations.argos.secret')" />
          </template>
          <div>
            <UButton
              color="neutral"
              variant="outline"
              icon="i-lucide-refresh-cw"
              :loading="rotating"
              :disabled="!canManage"
              :label="t('integrations.argos.rotate')"
              data-testid="argos-rotate"
              @click="rotate"
            />
          </div>
        </div>
      </div>
    </UCard>
  </section>
</template>
