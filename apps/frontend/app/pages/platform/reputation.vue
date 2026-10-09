<script setup lang="ts">
import type { ReputationCategory, ReputationSource, ReputationSourceFormat } from '~~/types/api'

/**
 * Fuentes de reputación (D20): el catálogo base aprobado (abuse.ch, Spamhaus DROP, Tor…) y
 * las listas que añade el superadministrador, con su estado de carga. La carga es
 * automática y queda en el snapshot; el alta exige confirmar que se puede usar la lista.
 * La cabecera de autenticación es write-only (nunca se devuelve).
 */
const { t, d } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
useSectionGuard()(findSection('platform', '/platform/reputation'))

const {
  data: sources,
  error,
  status,
  refresh,
} = useTenantQuery('platform:reputation', () =>
  unwrap($api.GET('/platform/reputation/sources')).then((r) => r.data),
)

const STATUS: Record<
  ReputationSource['status'],
  { color: 'success' | 'warning' | 'error' | 'neutral'; icon: string }
> = {
  ok: { color: 'success', icon: 'i-lucide-circle-check' },
  pending: { color: 'neutral', icon: 'i-lucide-loader-circle' },
  failing: { color: 'error', icon: 'i-lucide-circle-alert' },
  disabled: { color: 'neutral', icon: 'i-lucide-circle-pause' },
}

async function toggle(s: ReputationSource, enabled: boolean) {
  try {
    await unwrap(
      $api.PATCH('/platform/reputation/sources/{source_id}', {
        params: { path: { source_id: s.id }, header: { 'If-Match': ifMatch(s.version) } },
        body: { enabled },
      }),
    )
    toast.add({
      title: enabled ? t('reputation.enabled') : t('reputation.disabled'),
      color: 'success',
      icon: 'i-lucide-check',
    })
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  }
  refresh()
}

async function reload(s: ReputationSource) {
  try {
    await unwrap(
      $api.POST('/platform/reputation/sources/{source_id}/refresh', {
        params: { path: { source_id: s.id }, header: { 'Idempotency-Key': idempotencyKey() } },
      }),
    )
    toast.add({
      title: t('reputation.reloadQueued', { name: s.name }),
      color: 'success',
      icon: 'i-lucide-check',
    })
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  }
  refresh()
}

const deleting = ref<ReputationSource | null>(null)
const deleteOpen = computed({
  get: () => !!deleting.value,
  set: (v) => {
    if (!v) deleting.value = null
  },
})
async function remove() {
  const s = deleting.value
  if (!s) return
  try {
    await unwrap(
      $api.DELETE('/platform/reputation/sources/{source_id}', {
        params: { path: { source_id: s.id }, header: { 'If-Match': ifMatch(s.version) } },
      }),
    )
    toast.add({ title: t('reputation.deleted'), color: 'success', icon: 'i-lucide-check' })
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  }
  deleting.value = null
  refresh()
}

// --- Alta de una fuente personalizada ------------------------------------------------------
const formOpen = ref(false)
const empty = () => ({
  name: '',
  key: '',
  url: 'https://',
  format: 'netset' as ReputationSourceFormat,
  category: 'blocklist' as ReputationCategory,
  confidence: 70,
  frequency: '6h',
  license: '',
  notes: '',
  auth_header_name: '',
  auth_header_value: '',
  terms_acknowledged: true,
  terms: false,
})
const form = reactive(empty())
const fieldErrors = ref<Record<string, string>>({})
const saving = ref(false)

function openForm() {
  Object.assign(form, empty())
  fieldErrors.value = {}
  formOpen.value = true
}

async function create() {
  fieldErrors.value = {}
  if (!form.terms) {
    fieldErrors.value = { terms_acknowledged: t('reputation.form.termsRequired') }
    return
  }
  saving.value = true
  try {
    await unwrap(
      $api.POST('/platform/reputation/sources', {
        body: {
          name: form.name.trim(),
          key: form.key.trim(),
          url: form.url.trim(),
          format: form.format,
          category: form.category,
          confidence: Number(form.confidence),
          frequency: form.frequency,
          license: form.license?.trim() || null,
          notes: form.notes?.trim() || null,
          auth_header_name: form.auth_header_name?.trim() || null,
          auth_header_value: form.auth_header_value || null,
          terms_acknowledged: true,
        },
      }),
    )
    formOpen.value = false
    toast.add({ title: t('reputation.form.done'), color: 'success', icon: 'i-lucide-check' })
    refresh()
  } catch (e) {
    if (e instanceof ApiError && e.problem.errors?.length) {
      fieldErrors.value = Object.fromEntries(e.problem.errors.map((x) => [x.field, x.message]))
    } else {
      fieldErrors.value = { _: t(describeError(e).messageKey) }
    }
  } finally {
    saving.value = false
  }
}

const FORMATS: ReputationSourceFormat[] = [
  'netset',
  'abusech-feodo-csv',
  'abusech-threatfox-csv',
  'spamhaus-drop-json',
]
const CATEGORIES: ReputationCategory[] = [
  'botnet_cc',
  'scanner',
  'malware_dist',
  'mining_pool',
  'proxy_vpn',
  'tor_exit',
  'blocklist',
]
const formatItems = computed(() =>
  FORMATS.map((f) => ({ label: t(`reputation.format.${f}`), value: f })),
)
const categoryItems = computed(() =>
  CATEGORIES.map((c) => ({ label: t(`reputation.category.${c}`), value: c })),
)
const frequencyItems = computed(() =>
  ['15m', '1h', '6h', '24h', '7d'].map((f) => ({
    label: t(`reputation.frequency.${f}`),
    value: f,
  })),
)
</script>

<template>
  <AppPage :title="t('nav.items.platformReputation')" panel-id="platform-reputation">
    <template #toolbar-left>
      <p class="text-muted text-sm">{{ t('reputation.intro') }}</p>
    </template>
    <template #toolbar-right>
      <UButton
        icon="i-lucide-plus"
        :label="t('reputation.add')"
        data-testid="add-source"
        @click="openForm"
      />
    </template>

    <LoadingState v-if="status === 'pending' && !sources" :rows="5" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <ul v-else class="flex flex-col gap-3" data-testid="reputation-sources">
      <li v-for="s in sources ?? []" :key="s.id" :data-source="s.key">
        <UCard :ui="{ body: 'p-4 sm:p-4 flex flex-col gap-3' }">
          <div class="flex flex-col gap-2 md:flex-row md:items-start md:justify-between">
            <div class="flex min-w-0 flex-col gap-1">
              <p class="flex flex-wrap items-center gap-2">
                <span class="text-highlighted font-semibold">{{ s.name }}</span>
                <UBadge
                  :color="s.origin === 'catalog' ? 'neutral' : 'info'"
                  variant="outline"
                  :label="t(`reputation.origin.${s.origin}`)"
                />
                <UBadge
                  :color="STATUS[s.status].color"
                  variant="subtle"
                  :icon="STATUS[s.status].icon"
                  :label="t(`reputation.status.${s.status}`)"
                  data-testid="source-status"
                />
              </p>
              <p class="text-muted font-mono text-xs break-all">{{ s.key }} · {{ s.url }}</p>
            </div>
            <div class="flex shrink-0 flex-wrap items-center gap-2">
              <USwitch
                :model-value="s.enabled"
                :label="t('reputation.enabledLabel')"
                data-testid="source-enabled"
                @update:model-value="(v) => toggle(s, v)"
              />
              <UButton
                color="neutral"
                variant="outline"
                size="sm"
                icon="i-lucide-refresh-cw"
                :disabled="!s.enabled"
                :label="t('reputation.reload')"
                @click="reload(s)"
              />
              <UButton
                v-if="s.origin === 'custom'"
                color="neutral"
                variant="ghost"
                size="sm"
                icon="i-lucide-trash-2"
                :aria-label="t('reputation.delete', { name: s.name })"
                @click="deleting = s"
              />
            </div>
          </div>
          <dl class="grid grid-cols-2 gap-x-4 gap-y-2 text-sm md:grid-cols-6">
            <div>
              <dt class="text-muted text-xs">{{ t('reputation.col.category') }}</dt>
              <dd>{{ t(`reputation.category.${s.category}`) }}</dd>
            </div>
            <div>
              <dt class="text-muted text-xs">{{ t('reputation.col.format') }}</dt>
              <dd>{{ t(`reputation.format.${s.format}`) }}</dd>
            </div>
            <div>
              <dt class="text-muted text-xs">{{ t('reputation.col.frequency') }}</dt>
              <dd>{{ t('reputation.every', { f: s.frequency }) }}</dd>
            </div>
            <div>
              <dt class="text-muted text-xs">{{ t('reputation.col.confidence') }}</dt>
              <dd class="tabular">{{ s.confidence }}</dd>
            </div>
            <div>
              <dt class="text-muted text-xs">{{ t('reputation.col.entries') }}</dt>
              <dd class="tabular">{{ formatNumber(s.entries) }}</dd>
            </div>
            <div>
              <dt class="text-muted text-xs">{{ t('reputation.col.lastSuccess') }}</dt>
              <dd><RelativeTime :at="s.last_success_at" /></dd>
            </div>
          </dl>
          <p v-if="s.last_error" class="text-error flex gap-1.5 text-sm">
            <UIcon name="i-lucide-circle-alert" class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
            {{ s.last_error }}
          </p>
          <p class="text-dimmed text-xs">
            {{ s.license ? t('reputation.license', { license: s.license }) : '' }}
            <template v-if="s.has_auth"> · {{ t('reputation.hasAuth') }}</template>
            <template v-if="s.created_at">
              · {{ t('reputation.since', { date: d(new Date(s.created_at), 'short') }) }}</template
            >
          </p>
        </UCard>
      </li>
    </ul>

    <USlideover
      v-model:open="formOpen"
      :title="t('reputation.form.title')"
      :description="t('reputation.form.description')"
    >
      <template #body>
        <form id="source-form" class="flex flex-col gap-4" @submit.prevent="create">
          <UFormField :label="t('reputation.form.name')" :error="fieldErrors.name" required>
            <UInput v-model="form.name" class="w-full" data-testid="source-name" />
          </UFormField>
          <UFormField
            :label="t('reputation.form.key')"
            :help="t('reputation.form.keyHelp')"
            :error="fieldErrors.key"
            required
          >
            <UInput
              v-model="form.key"
              class="w-full"
              :ui="{ base: 'font-mono' }"
              data-testid="source-key"
            />
          </UFormField>
          <UFormField
            :label="t('reputation.form.url')"
            :help="t('reputation.form.urlHelp')"
            :error="fieldErrors.url"
            required
          >
            <UInput
              v-model="form.url"
              type="url"
              class="w-full"
              :ui="{ base: 'font-mono' }"
              data-testid="source-url"
            />
          </UFormField>
          <div class="grid gap-4 sm:grid-cols-2">
            <UFormField :label="t('reputation.col.format')" :error="fieldErrors.format">
              <USelect v-model="form.format" :items="formatItems" class="w-full" />
            </UFormField>
            <UFormField :label="t('reputation.col.category')" :error="fieldErrors.category">
              <USelect v-model="form.category" :items="categoryItems" class="w-full" />
            </UFormField>
            <UFormField :label="t('reputation.col.frequency')" :error="fieldErrors.frequency">
              <USelect v-model="form.frequency" :items="frequencyItems" class="w-full" />
            </UFormField>
            <UFormField
              :label="t('reputation.col.confidence')"
              :help="t('reputation.form.confidenceHelp')"
              :error="fieldErrors.confidence"
            >
              <UInputNumber v-model="form.confidence" :min="0" :max="100" class="w-full" />
            </UFormField>
          </div>
          <UFormField :label="t('reputation.form.license')">
            <UInput v-model="form.license" class="w-full" />
          </UFormField>
          <fieldset class="flex flex-col gap-3">
            <legend class="text-highlighted mb-1 text-sm font-medium">
              {{ t('reputation.form.auth') }}
            </legend>
            <div class="grid gap-3 sm:grid-cols-2">
              <UFormField :label="t('reputation.form.authName')">
                <UInput v-model="form.auth_header_name" class="w-full" placeholder="Auth-Key" />
              </UFormField>
              <UFormField
                :label="t('reputation.form.authValue')"
                :help="t('reputation.form.writeOnly')"
              >
                <UInput
                  v-model="form.auth_header_value"
                  type="password"
                  autocomplete="new-password"
                  class="w-full"
                />
              </UFormField>
            </div>
          </fieldset>
          <UFormField :error="fieldErrors.terms_acknowledged">
            <UCheckbox
              v-model="form.terms"
              :label="t('reputation.form.terms')"
              data-testid="source-terms"
            />
          </UFormField>
          <UAlert v-if="fieldErrors._" color="error" variant="subtle" :title="fieldErrors._" />
        </form>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton
            color="neutral"
            variant="ghost"
            :label="t('common.cancel')"
            @click="formOpen = false"
          />
          <UButton
            type="submit"
            form="source-form"
            :loading="saving"
            :label="t('reputation.form.submit')"
            data-testid="source-submit"
          />
        </div>
      </template>
    </USlideover>

    <UModal
      v-model:open="deleteOpen"
      :title="t('reputation.deleteTitle', { name: deleting?.name ?? '' })"
      :description="t('reputation.deleteBody')"
    >
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton
            color="neutral"
            variant="outline"
            :label="t('common.cancel')"
            autofocus
            @click="deleting = null"
          />
          <UButton
            color="error"
            icon="i-lucide-trash-2"
            :label="t('reputation.deleteSubmit')"
            @click="remove"
          />
        </div>
      </template>
    </UModal>
  </AppPage>
</template>
