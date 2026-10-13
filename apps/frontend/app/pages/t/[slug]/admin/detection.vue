<script setup lang="ts">
import type { AllowlistEntry } from '~~/types/api'

/**
 * Administración › Detección (frontend.md §4, I1 lectura): con qué umbrales se detectan las
 * señales compatibles con botnet (valores por defecto + ajustes del ISP, `GET /detection/config`),
 * qué prefijos o ASN no generan hallazgos (allowlist) y el estado de los feeds de reputación.
 * Los umbrales no se editan desde la consola en I1.
 */
const { t, d, te } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const toast = useToast()
const can = useCan()
const slug = computed(() => String(route.params.slug))
useSectionGuard()(findSection('tenant', 'admin/detection'), slug.value)
const canManage = computed(() => can('security.findings.manage'))

const {
  data: config,
  error: configError,
  status: configStatus,
  refresh: refreshConfig,
} = useTenantQuery('detection-config', () => unwrap($api.GET('/detection/config')))
const { data: sources } = useTenantQuery('reputation-sources', () =>
  unwrap($api.GET('/reputation/sources')).then((r) => r.data),
)
const { data: allowlist, refresh: refreshAllow } = useTenantQuery('reputation-allowlist', () =>
  unwrap($api.GET('/reputation/allowlist')).then((r) => r.data),
)

const ORDER = [
  'c2_contact',
  'scanning',
  'watched_ports',
  'fanout',
  'smtp',
  'ddos',
  'beaconing',
  'sustained_upload',
]
type Params = Record<string, unknown>
const detectors = computed(() => {
  const all = (config.value?.detectors ?? {}) as Record<string, Params>
  return ORDER.filter((k) => all[k]).map((k) => ({
    key: k,
    enabled: all[k]!.enabled !== false,
    custom: config.value?.customized.includes(k) ?? false,
    fields: Object.entries(all[k]!).filter(([f, v]) => f !== 'enabled' && v !== null),
  }))
})
const autoExpire = computed(() => (config.value?.detectors as Params | undefined)?.auto_expire)

/** "1h30m0s" → "1 h 30 min". */
function duration(v: string) {
  const m = v.match(/^(?:(\d+)h)?(?:(\d+)m)?(?:([\d.]+)s)?$/)
  if (!m) return v
  const [h, min, s] = [Number(m[1] ?? 0), Number(m[2] ?? 0), Number(m[3] ?? 0)]
  if (h >= 24 && h % 24 === 0 && !min && !s) return t('detection.units.days', { n: h / 24 }, h / 24)
  return [h && `${h} h`, min && `${min} min`, s && `${s} s`].filter(Boolean).join(' ') || '0 s'
}
const RATIO = /ratio|share|_cv$/
function format(field: string, v: unknown) {
  if (typeof v === 'boolean') return v ? t('common.yes') : t('common.no')
  if (Array.isArray(v)) return v.length ? v.join(', ') : '—'
  if (typeof v === 'string') return duration(v)
  if (typeof v === 'number' && RATIO.test(field)) return `${Math.round(v * 100)} %`
  return typeof v === 'number' ? v.toLocaleString('es') : String(v)
}
const label = (detector: string, field: string) => {
  const key = `detection.fields.${field}`
  return te(`detection.fields.${detector}.${field}`)
    ? t(`detection.fields.${detector}.${field}`)
    : te(key)
      ? t(key)
      : field
}

// --- Allowlist ---------------------------------------------------------------------------
const formOpen = ref(false)
const form = reactive({ kind: 'prefix' as 'prefix' | 'asn', prefix: '', asn: '', reason: '' })
const formErrors = ref<Record<string, string>>({})
const saving = ref(false)
function openForm() {
  Object.assign(form, { kind: 'prefix', prefix: '', asn: '', reason: '' })
  formErrors.value = {}
  formOpen.value = true
}
async function addEntry() {
  formErrors.value = {}
  saving.value = true
  try {
    await unwrap(
      $api.POST('/reputation/allowlist', {
        body: {
          ...(form.kind === 'prefix'
            ? { prefix: form.prefix.trim() }
            : { asn: Number(form.asn.replace(/^AS/i, '')) }),
          reason: form.reason.trim(),
        },
      }),
    )
    formOpen.value = false
    toast.add({ title: t('detection.allow.added'), icon: 'i-lucide-check', color: 'success' })
    refreshAllow()
  } catch (e) {
    if (e instanceof ApiError && e.problem.errors?.length) {
      formErrors.value = Object.fromEntries(e.problem.errors.map((x) => [x.field, x.message]))
    } else formErrors.value = { _: t(describeError(e).messageKey) }
  } finally {
    saving.value = false
  }
}
const removing = ref<AllowlistEntry | null>(null)
const removeOpen = computed({
  get: () => !!removing.value,
  set: (v) => {
    if (!v) removing.value = null
  },
})
async function removeEntry() {
  const e = removing.value
  if (!e) return
  try {
    await unwrap(
      $api.DELETE('/reputation/allowlist/{entry_id}', { params: { path: { entry_id: e.id } } }),
    )
    toast.add({ title: t('detection.allow.removed'), icon: 'i-lucide-check', color: 'success' })
  } catch (err) {
    toast.add({ title: t(describeError(err).messageKey), color: 'error' })
  }
  removing.value = null
  refreshAllow()
}
const target = (e: AllowlistEntry) => e.prefix ?? (e.asn ? `AS${e.asn}` : '—')
const ago = (seconds?: number | null) =>
  seconds == null
    ? '—'
    : seconds < 3600
      ? `${Math.round(seconds / 60)} min`
      : duration(`${Math.round(seconds / 3600)}h`)
</script>

<template>
  <AppPage :title="t('nav.items.detection')" panel-id="detection">
    <template #toolbar-left>
      <p class="text-muted text-sm">{{ t('detection.intro') }}</p>
    </template>

    <!-- Reglas y umbrales -->
    <section class="flex flex-col gap-3" data-testid="detection-rules">
      <div>
        <h2 class="text-highlighted font-semibold">{{ t('detection.rules.title') }}</h2>
        <p class="text-muted text-sm">{{ t('detection.rules.body') }}</p>
      </div>
      <LoadingState v-if="configStatus === 'pending' && !config" :rows="3" />
      <ErrorState v-else-if="configError" :error="configError" @retry="refreshConfig()" />
      <ul v-else class="grid gap-3 lg:grid-cols-2">
        <li v-for="det in detectors" :key="det.key" :data-detector="det.key">
          <UCard :ui="{ body: 'p-4 sm:p-4 flex flex-col gap-2' }">
            <div class="flex flex-wrap items-start justify-between gap-2">
              <div class="min-w-0">
                <h3 class="text-highlighted font-medium">
                  {{ t(`detection.detectors.${det.key}.name`) }}
                </h3>
                <p class="text-muted text-sm">{{ t(`detection.detectors.${det.key}.body`) }}</p>
              </div>
              <span class="flex shrink-0 gap-1.5">
                <UBadge
                  v-if="det.custom"
                  color="info"
                  variant="subtle"
                  :label="t('detection.rules.custom')"
                />
                <UBadge
                  :color="det.enabled ? 'success' : 'neutral'"
                  variant="subtle"
                  :icon="det.enabled ? 'i-lucide-circle-check' : 'i-lucide-circle-pause'"
                  :label="det.enabled ? t('detection.rules.on') : t('detection.rules.off')"
                  data-testid="detector-state"
                />
              </span>
            </div>
            <dl class="grid grid-cols-1 gap-x-4 gap-y-1.5 text-sm sm:grid-cols-2">
              <div v-for="[field, value] in det.fields" :key="field" class="min-w-0">
                <dt class="text-muted text-xs">{{ label(det.key, field) }}</dt>
                <dd class="tabular break-words">{{ format(field, value) }}</dd>
              </div>
            </dl>
          </UCard>
        </li>
      </ul>
      <p v-if="typeof autoExpire === 'string'" class="text-muted text-sm">
        {{ t('detection.rules.autoExpire', { time: duration(autoExpire) }) }}
      </p>
    </section>

    <!-- Allowlist -->
    <section class="mt-8 flex flex-col gap-3" data-testid="detection-allowlist">
      <div class="flex flex-wrap items-end justify-between gap-2">
        <div>
          <h2 class="text-highlighted font-semibold">{{ t('detection.allow.title') }}</h2>
          <p class="text-muted text-sm">{{ t('detection.allow.body') }}</p>
        </div>
        <UButton
          v-if="canManage"
          icon="i-lucide-plus"
          color="neutral"
          variant="outline"
          :label="t('detection.allow.add')"
          data-testid="allow-add"
          @click="openForm"
        />
      </div>
      <p v-if="!allowlist?.length" class="text-muted text-sm">{{ t('detection.allow.empty') }}</p>
      <ul v-else class="divide-default ring-default divide-y rounded-md ring-1">
        <li
          v-for="e in allowlist"
          :key="e.id"
          class="flex flex-wrap items-center justify-between gap-2 px-3 py-2"
          :data-allow="target(e)"
        >
          <div class="min-w-0">
            <p class="text-highlighted font-mono text-sm">{{ target(e) }}</p>
            <p class="text-muted text-sm break-words">{{ e.reason }}</p>
            <p class="text-dimmed text-xs">{{ d(new Date(e.created_at), 'long') }}</p>
          </div>
          <UButton
            v-if="canManage"
            size="sm"
            color="neutral"
            variant="ghost"
            icon="i-lucide-trash-2"
            :label="t('detection.allow.remove')"
            data-testid="allow-remove"
            @click="removing = e"
          />
        </li>
      </ul>
    </section>

    <!-- Feeds de reputación -->
    <section class="mt-8 flex flex-col gap-3" data-testid="detection-sources">
      <div>
        <h2 class="text-highlighted font-semibold">{{ t('detection.sources.title') }}</h2>
        <p class="text-muted text-sm">{{ t('detection.sources.body') }}</p>
      </div>
      <p v-if="!sources?.length" class="text-muted text-sm">{{ t('detection.sources.empty') }}</p>
      <ul v-else class="grid gap-3 lg:grid-cols-3">
        <li v-for="s in sources" :key="s.key">
          <UCard :ui="{ body: 'p-4 sm:p-4 flex flex-col gap-1' }">
            <h3 class="text-highlighted font-medium">{{ s.name }}</h3>
            <p class="text-muted text-sm">
              {{ t(`detection.sources.category.${s.category}`) }} ·
              {{ t('detection.sources.entries', { n: s.entries.toLocaleString('es') }) }}
            </p>
            <p class="text-dimmed text-xs">
              {{ t('detection.sources.updated', { time: ago(s.age_seconds) }) }}
            </p>
          </UCard>
        </li>
      </ul>
    </section>

    <USlideover
      v-model:open="formOpen"
      :title="t('detection.allow.formTitle')"
      :description="t('detection.allow.formBody')"
    >
      <template #body>
        <form id="allow-form" class="flex flex-col gap-4" @submit.prevent="addEntry">
          <URadioGroup
            v-model="form.kind"
            orientation="horizontal"
            :items="[
              { label: t('detection.allow.prefix'), value: 'prefix' },
              { label: t('detection.allow.asn'), value: 'asn' },
            ]"
          />
          <UFormField
            v-if="form.kind === 'prefix'"
            :label="t('detection.allow.prefix')"
            :error="formErrors.prefix"
            required
          >
            <UInput
              v-model="form.prefix"
              class="w-full"
              :ui="{ base: 'font-mono' }"
              placeholder="200.0.113.0/24"
              data-testid="allow-prefix"
            />
          </UFormField>
          <UFormField v-else :label="t('detection.allow.asn')" :error="formErrors.asn" required>
            <UInput
              v-model="form.asn"
              class="w-full"
              :ui="{ base: 'font-mono' }"
              placeholder="AS15169"
              data-testid="allow-asn"
            />
          </UFormField>
          <UFormField :label="t('detection.allow.reason')" :error="formErrors.reason" required>
            <UTextarea v-model="form.reason" :rows="2" class="w-full" data-testid="allow-reason" />
          </UFormField>
          <UAlert v-if="formErrors._" color="error" variant="subtle" :title="formErrors._" />
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
            form="allow-form"
            :loading="saving"
            :label="t('detection.allow.add')"
            data-testid="allow-submit"
          />
        </div>
      </template>
    </USlideover>

    <UModal
      v-model:open="removeOpen"
      :title="t('detection.allow.removeTitle', { target: removing ? target(removing) : '' })"
      :description="t('detection.allow.removeBody')"
    >
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton
            color="neutral"
            variant="outline"
            :label="t('common.cancel')"
            autofocus
            @click="removing = null"
          />
          <UButton
            color="error"
            icon="i-lucide-trash-2"
            :label="t('detection.allow.remove')"
            data-testid="allow-remove-confirm"
            @click="removeEntry"
          />
        </div>
      </template>
    </UModal>
  </AppPage>
</template>
