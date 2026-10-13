<script setup lang="ts">
import type {
  AssistAuditEntry,
  AssistEffort,
  AssistProvider,
  AssistSettingsUpdate,
  AssistTestResult,
  McpToken,
  McpTokenIssued,
} from '~~/types/api'

/**
 * Administración › Inteligencia artificial (docs/assist.md): proveedor del asistente del ISP
 * (Claude por la API de Anthropic, o un servidor compatible con OpenAI para un modelo local),
 * modelo, esfuerzo, límite diario y clave de API write-only; prueba de conexión; auditoría de
 * las consultas (sin datos de clientes) y servidor MCP (tokens que se muestran una sola vez).
 */
const { t, d } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const toast = useToast()
const can = useCan()
const slug = computed(() => String(route.params.slug))
useSectionGuard()(findSection('tenant', 'admin/ai'), slug.value)
const canManage = computed(() => can('settings.manage'))
const canTokens = computed(() => can('api_tokens.manage'))

const DEFAULT_MODELS: Record<AssistProvider, string> = {
  anthropic: 'claude-opus-5-5',
  openai_compatible: 'llama3.1',
}

const {
  data: settings,
  error,
  status,
  refresh,
} = useTenantQuery('assist-settings', () => unwrap($api.GET('/assist/settings')))

const form = reactive({
  enabled: false,
  provider: 'anthropic' as AssistProvider,
  model: '',
  base_url: '',
  effort: 'medium' as AssistEffort,
  daily_request_limit: 100,
  api_key: '',
  clear_api_key: false,
})
const errors = ref<Record<string, string>>({})
watch(
  settings,
  (s) => {
    if (!s) return
    Object.assign(form, {
      enabled: s.enabled,
      provider: s.provider,
      model: s.model,
      base_url: s.base_url ?? '',
      effort: s.effort,
      daily_request_limit: s.daily_request_limit,
      api_key: '',
      clear_api_key: false,
    })
  },
  { immediate: true },
)
watch(
  () => form.provider,
  (p, old) => {
    // Al cambiar de proveedor se propone su modelo por defecto si seguía el del anterior.
    if (old && (!form.model || form.model === DEFAULT_MODELS[old])) form.model = DEFAULT_MODELS[p]
  },
)

const providerItems = computed(() => [
  { label: t('assist.providers.anthropic'), value: 'anthropic' },
  { label: t('assist.providers.openai_compatible'), value: 'openai_compatible' },
])
const effortItems = computed(() =>
  (['low', 'medium', 'high'] as const).map((e) => ({
    label: t(`assist.admin.efforts.${e}`),
    value: e,
  })),
)
const keyHint = computed(() => {
  const s = settings.value
  if (!s) return ''
  if (s.api_key_set && s.provider === form.provider) return t('assist.admin.keySet')
  if (s.platform_default.api_key_set && s.platform_default.provider === form.provider)
    return t('assist.admin.keyPlatform')
  return form.provider === 'anthropic'
    ? t('assist.admin.keyRequired')
    : t('assist.admin.keyOptional')
})

const saving = ref(false)
async function save() {
  errors.value = {}
  saving.value = true
  const body: AssistSettingsUpdate = {
    enabled: form.enabled,
    provider: form.provider,
    model: form.model.trim(),
    effort: form.effort,
    daily_request_limit: Number(form.daily_request_limit),
    base_url: form.provider === 'openai_compatible' ? form.base_url.trim() : null,
  }
  if (form.api_key.trim()) body.api_key = form.api_key.trim()
  else if (form.clear_api_key) body.clear_api_key = true
  try {
    settings.value = await unwrap($api.PUT('/assist/settings', { body }))
    toast.add({ title: t('assist.admin.saved'), icon: 'i-lucide-check', color: 'success' })
    clearNuxtData((key) => key.endsWith(':assist-status'))
    refreshAudit()
  } catch (e) {
    if (e instanceof ApiError && e.problem.errors?.length) {
      errors.value = Object.fromEntries(e.problem.errors.map((x) => [x.field, x.message]))
    } else {
      errors.value = {
        _:
          e instanceof ApiError && e.problem.detail
            ? e.problem.detail
            : t(describeError(e).messageKey),
      }
    }
  } finally {
    saving.value = false
  }
}

const testing = ref(false)
const testResult = ref<AssistTestResult | null>(null)
async function testConnection() {
  testing.value = true
  testResult.value = null
  try {
    testResult.value = await unwrap($api.POST('/assist/settings/test'))
  } catch (e) {
    testResult.value = {
      ok: false,
      provider: form.provider,
      model: form.model,
      latency_ms: 0,
      message:
        e instanceof ApiError && e.problem.detail
          ? e.problem.detail
          : t(describeError(e).messageKey),
    }
  } finally {
    testing.value = false
    refreshAudit()
  }
}

// --- Auditoría ----------------------------------------------------------------------------
const auditRows = ref<AssistAuditEntry[]>([])
const auditCursor = ref<string | null>(null)
const auditLoading = ref(false)
async function loadAudit(more = false) {
  auditLoading.value = true
  try {
    const res = await unwrap(
      $api.GET('/assist/audit', {
        params: {
          query: { limit: 20, ...(more && auditCursor.value ? { cursor: auditCursor.value } : {}) },
        },
      }),
    )
    auditRows.value = more ? [...auditRows.value, ...res.data] : res.data
    auditCursor.value = res.page.next_cursor ?? null
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  } finally {
    auditLoading.value = false
  }
}
const refreshAudit = () => loadAudit(false)
onMounted(() => loadAudit(false))
const OUTCOME_COLOR: Record<string, 'success' | 'warning' | 'error' | 'neutral'> = {
  success: 'success',
  refused: 'warning',
  limited: 'warning',
  denied: 'neutral',
  error: 'error',
}
const tools = (row: AssistAuditEntry) =>
  row.tool_calls.map((c) => `${t(`assist.toolNames.${c.name}`, c.name)} ×${c.count}`).join(', ')

// --- MCP ---------------------------------------------------------------------------------
const mcpUrl = computed(() =>
  typeof window === 'undefined'
    ? '/api/v1/assist/mcp'
    : `${window.location.origin}/api/v1/assist/mcp`,
)
const { data: tokens, refresh: refreshTokens } = useTenantQuery(
  'assist-mcp-tokens',
  () => unwrap($api.GET('/assist/mcp-tokens')).then((r) => r.data),
  { immediate: canTokens.value },
)
const tokenName = ref('')
const tokenError = ref('')
const creating = ref(false)
const issued = ref<McpTokenIssued | null>(null)
async function createToken() {
  tokenError.value = ''
  creating.value = true
  try {
    issued.value = await unwrap(
      $api.POST('/assist/mcp-tokens', { body: { name: tokenName.value.trim() } }),
    )
    tokenName.value = ''
    refreshTokens()
  } catch (e) {
    tokenError.value =
      e instanceof ApiError && e.problem.errors?.length
        ? e.problem.errors[0]!.message
        : t(describeError(e).messageKey)
  } finally {
    creating.value = false
  }
}
async function revoke(tk: McpToken) {
  try {
    await unwrap(
      $api.DELETE('/assist/mcp-tokens/{token_id}', { params: { path: { token_id: tk.id } } }),
    )
    toast.add({ title: t('assist.mcp.revoked'), icon: 'i-lucide-check', color: 'success' })
    refreshTokens()
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  }
}
const desktopConfig = computed(() =>
  JSON.stringify(
    {
      mcpServers: {
        horus: {
          command: 'npx',
          args: [
            'mcp-remote',
            mcpUrl.value,
            '--header',
            `Authorization: Bearer ${issued.value?.token ?? 'hmcp_…'}`,
          ],
        },
      },
    },
    null,
    2,
  ),
)
</script>

<template>
  <AppPage :title="t('nav.items.ai')" panel-id="assist-admin">
    <template #toolbar-left>
      <p class="text-muted text-sm">{{ t('assist.admin.intro') }}</p>
    </template>

    <LoadingState v-if="status === 'pending' && !settings" :rows="4" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <template v-else-if="settings">
      <UAlert
        color="info"
        variant="subtle"
        icon="i-lucide-shield-check"
        :title="t('assist.admin.privacyTitle')"
        :description="t('assist.admin.privacyBody')"
      />

      <!-- Proveedor -->
      <section
        class="flex flex-col gap-3"
        data-testid="assist-settings"
        :data-version="settings.version"
      >
        <div class="flex flex-wrap items-end justify-between gap-2">
          <div>
            <h2 class="text-highlighted font-semibold">{{ t('assist.admin.providerTitle') }}</h2>
            <p class="text-muted text-sm">
              {{
                settings.source === 'tenant'
                  ? t('assist.admin.sourceTenant')
                  : t('assist.admin.sourcePlatform')
              }}
              ·
              {{
                t('assist.admin.usage', {
                  used: settings.usage_today,
                  limit: settings.daily_request_limit,
                })
              }}
            </p>
          </div>
          <UBadge
            :color="settings.enabled ? 'success' : 'neutral'"
            variant="subtle"
            :label="settings.enabled ? t('assist.admin.on') : t('assist.admin.off')"
            data-testid="assist-state"
          />
        </div>
        <UCard :ui="{ body: 'p-4 sm:p-4' }">
          <form id="assist-form" class="grid gap-4 lg:grid-cols-2" @submit.prevent="save">
            <UFormField
              class="lg:col-span-2"
              :label="t('assist.admin.enabled')"
              :description="t('assist.admin.enabledHelp')"
            >
              <USwitch
                v-model="form.enabled"
                :disabled="!canManage"
                :aria-label="t('assist.admin.enabled')"
                data-testid="assist-enabled"
              />
            </UFormField>
            <UFormField :label="t('assist.admin.provider')" :error="errors.provider">
              <USelect
                v-model="form.provider"
                :items="providerItems"
                :disabled="!canManage"
                class="w-full"
                data-testid="assist-provider"
              />
            </UFormField>
            <UFormField
              :label="t('assist.admin.model')"
              :description="t(`assist.admin.modelHelp.${form.provider}`)"
              :error="errors.model"
            >
              <UInput
                v-model="form.model"
                :disabled="!canManage"
                class="w-full"
                :ui="{ base: 'font-mono' }"
                :placeholder="DEFAULT_MODELS[form.provider]"
                data-testid="assist-model"
              />
            </UFormField>
            <UFormField
              v-if="form.provider === 'openai_compatible'"
              class="lg:col-span-2"
              :label="t('assist.admin.baseUrl')"
              :description="t('assist.admin.baseUrlHelp')"
              :error="errors.base_url"
              required
            >
              <UInput
                v-model="form.base_url"
                :disabled="!canManage"
                class="w-full"
                :ui="{ base: 'font-mono' }"
                placeholder="http://10.0.0.5:11434"
                data-testid="assist-base-url"
              />
            </UFormField>
            <UFormField
              class="lg:col-span-2"
              :label="t('assist.admin.apiKey')"
              :description="keyHint"
              :error="errors.api_key || errors.clear_api_key"
            >
              <SecretInput
                v-model="form.api_key"
                :disabled="!canManage || !settings?.key_store_available"
                :placeholder="
                  settings?.api_key_set
                    ? t('assist.admin.keyKeep')
                    : t('assist.admin.keyPlaceholder')
                "
                data-testid="assist-api-key"
              />
              <p v-if="!settings?.key_store_available" class="text-warning mt-1 text-xs">
                {{ t('assist.admin.keyStoreMissing') }}
              </p>
              <UCheckbox
                v-if="settings?.api_key_set && canManage"
                v-model="form.clear_api_key"
                class="mt-2"
                :label="t('assist.admin.clearKey')"
                :disabled="!!form.api_key"
              />
            </UFormField>
            <UFormField
              v-if="form.provider === 'anthropic'"
              :label="t('assist.admin.effort')"
              :description="t('assist.admin.effortHelp')"
              :error="errors.effort"
            >
              <USelect
                v-model="form.effort"
                :items="effortItems"
                :disabled="!canManage"
                class="w-full"
                data-testid="assist-effort"
              />
            </UFormField>
            <UFormField
              :label="t('assist.admin.limit')"
              :description="t('assist.admin.limitHelp')"
              :error="errors.daily_request_limit"
            >
              <UInput
                v-model.number="form.daily_request_limit"
                type="number"
                min="1"
                max="10000"
                :disabled="!canManage"
                class="w-full"
                data-testid="assist-limit"
              />
            </UFormField>
            <UAlert
              v-if="errors._"
              class="lg:col-span-2"
              color="error"
              variant="subtle"
              :title="errors._"
            />
          </form>
          <template v-if="canManage" #footer>
            <div class="flex flex-wrap items-center justify-end gap-2">
              <p
                v-if="testResult"
                class="me-auto text-sm"
                :class="testResult.ok ? 'text-success' : 'text-error'"
                data-testid="assist-test-result"
              >
                {{
                  testResult.ok
                    ? t('assist.admin.testOk', { ms: testResult.latency_ms })
                    : testResult.message
                }}
              </p>
              <UButton
                color="neutral"
                variant="outline"
                icon="i-lucide-plug-zap"
                :loading="testing"
                :label="t('assist.admin.test')"
                data-testid="assist-test"
                @click="testConnection"
              />
              <UButton
                type="submit"
                form="assist-form"
                :loading="saving"
                :label="t('common.save')"
                data-testid="assist-save"
              />
            </div>
          </template>
        </UCard>
      </section>

      <!-- Auditoría -->
      <section class="flex flex-col gap-3" data-testid="assist-audit">
        <div>
          <h2 class="text-highlighted font-semibold">{{ t('assist.audit.title') }}</h2>
          <p class="text-muted text-sm">{{ t('assist.audit.body') }}</p>
        </div>
        <p v-if="!auditRows.length && !auditLoading" class="text-muted text-sm">
          {{ t('assist.audit.empty') }}
        </p>
        <ul v-else class="divide-default ring-default divide-y rounded-md ring-1">
          <li
            v-for="row in auditRows"
            :key="row.id"
            class="flex flex-wrap items-start justify-between gap-2 px-3 py-2 text-sm"
            data-testid="assist-audit-row"
          >
            <div class="flex min-w-0 flex-col gap-0.5">
              <p class="text-highlighted">
                {{ t(`assist.audit.feature.${row.feature}`, row.feature) }}
                <span v-if="row.model" class="text-muted font-mono text-xs">· {{ row.model }}</span>
              </p>
              <p class="text-muted text-xs">
                {{ d(new Date(row.created_at), 'long') }} ·
                {{ t('assist.audit.duration', { s: (row.duration_ms / 1000).toFixed(1) }) }} ·
                {{
                  t('assist.audit.tokens', {
                    input: row.input_tokens.toLocaleString('es'),
                    output: row.output_tokens.toLocaleString('es'),
                  })
                }}
              </p>
              <p v-if="row.tool_calls.length" class="text-dimmed text-xs">{{ tools(row) }}</p>
            </div>
            <UBadge
              :color="OUTCOME_COLOR[row.outcome] ?? 'neutral'"
              variant="subtle"
              :label="t(`assist.audit.outcome.${row.outcome}`)"
            />
          </li>
        </ul>
        <div v-if="auditCursor">
          <UButton
            color="neutral"
            variant="ghost"
            :loading="auditLoading"
            :label="t('assist.audit.more')"
            @click="loadAudit(true)"
          />
        </div>
      </section>

      <!-- MCP -->
      <section class="flex flex-col gap-3" data-testid="assist-mcp">
        <div>
          <h2 class="text-highlighted font-semibold">{{ t('assist.mcp.title') }}</h2>
          <p class="text-muted text-sm">{{ t('assist.mcp.body') }}</p>
        </div>
        <UAlert
          v-if="!settings?.mcp_enabled"
          color="neutral"
          variant="subtle"
          :title="t('assist.mcp.unavailable')"
        />
        <template v-else>
          <CodeBlock :code="mcpUrl" :label="t('assist.mcp.url')" />
          <p v-if="!canTokens" class="text-muted text-sm">{{ t('assist.mcp.noPermission') }}</p>
          <template v-else>
            <form class="flex flex-wrap items-end gap-2" @submit.prevent="createToken">
              <UFormField :label="t('assist.mcp.name')" :error="tokenError" class="min-w-60 flex-1">
                <UInput
                  v-model="tokenName"
                  class="w-full"
                  :placeholder="t('assist.mcp.namePlaceholder')"
                  data-testid="assist-mcp-name"
                />
              </UFormField>
              <UButton
                type="submit"
                icon="i-lucide-key-round"
                :loading="creating"
                :disabled="!tokenName.trim()"
                :label="t('assist.mcp.create')"
                data-testid="assist-mcp-create"
              />
            </form>
            <UAlert
              v-if="issued"
              color="warning"
              variant="subtle"
              icon="i-lucide-key-round"
              :title="t('assist.mcp.created')"
              data-testid="assist-mcp-issued"
            />
            <CodeBlock
              v-if="issued"
              :code="issued.token"
              :label="t('assist.mcp.token')"
              data-testid="assist-mcp-token"
            />
            <CodeBlock :code="desktopConfig" :label="t('assist.mcp.desktop')" />
            <p v-if="!tokens?.length" class="text-muted text-sm">{{ t('assist.mcp.empty') }}</p>
            <ul v-else class="divide-default ring-default divide-y rounded-md ring-1">
              <li
                v-for="tk in tokens"
                :key="tk.id"
                class="flex flex-wrap items-center justify-between gap-2 px-3 py-2 text-sm"
              >
                <div class="min-w-0">
                  <p class="text-highlighted">
                    {{ tk.name }}
                    <span class="text-muted font-mono text-xs">{{ tk.prefix }}…</span>
                  </p>
                  <p class="text-muted text-xs">
                    {{
                      tk.revoked_at
                        ? t('assist.mcp.revokedAt', { time: d(new Date(tk.revoked_at), 'long') })
                        : t('assist.mcp.expires', { time: d(new Date(tk.expires_at), 'long') })
                    }}
                    ·
                    {{
                      tk.last_used_at
                        ? t('assist.mcp.lastUsed', { time: d(new Date(tk.last_used_at), 'long') })
                        : t('assist.mcp.neverUsed')
                    }}
                  </p>
                </div>
                <UButton
                  v-if="tk.active"
                  size="sm"
                  color="neutral"
                  variant="ghost"
                  icon="i-lucide-ban"
                  :label="t('assist.mcp.revoke')"
                  @click="revoke(tk)"
                />
              </li>
            </ul>
          </template>
        </template>
      </section>
    </template>
  </AppPage>
</template>
