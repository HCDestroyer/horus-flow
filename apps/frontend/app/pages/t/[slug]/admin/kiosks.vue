<script setup lang="ts">
import type { Kiosk, KioskEnrollmentCode } from '~~/types/api'

/**
 * Pantallas NOC (I1-21, frontend.md §7.2): cada kiosco con su estado (*En línea · última
 * señal hace 12 s*, *Desconectado desde…*, *Pendiente de enrolar*, *Revocado*), última IP y
 * versión; "Generar código" (8 caracteres, un uso, 10 min, con cuenta atrás y enlace QR con
 * el código en el fragmento) y "Revocar" (confirmación: la TV vuelve a la pantalla de código
 * en < 1 min). Alta con red permitida y datos de clientes desactivados por defecto.
 */
const { t, d } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const toast = useToast()
const now = useNow()
const slug = computed(() => String(route.params.slug))
useSectionGuard()(findSection('tenant', 'admin/kiosks'), slug.value)

const {
  data: kiosks,
  error,
  status,
  refresh,
} = useTenantQuery('kiosks', () => unwrap($api.GET('/kiosks')).then((r) => r.data))
const { data: playlists } = useTenantQuery('playlists', () =>
  unwrap($api.GET('/playlists')).then((r) => r.data),
)
let poll: ReturnType<typeof setInterval> | undefined
onMounted(() => (poll = setInterval(() => refresh(), 15_000)))
onBeforeUnmount(() => clearInterval(poll))

/** En línea si la última señal tiene menos de 2 min. */
function presence(k: Kiosk) {
  if (k.status === 'revoked') return 'revoked'
  if (k.status === 'expired') return 'expired'
  if (k.status === 'pending_enrollment') return 'pending'
  const seen = k.last_seen_at ? new Date(k.last_seen_at).getTime() : 0
  return now.value - seen < 120_000 ? 'online' : 'offline'
}
const PRESENCE = {
  online: { color: 'success', icon: 'i-lucide-monitor-check' },
  offline: { color: 'error', icon: 'i-lucide-monitor-off' },
  pending: { color: 'neutral', icon: 'i-lucide-monitor-dot' },
  revoked: { color: 'neutral', icon: 'i-lucide-ban' },
  expired: { color: 'neutral', icon: 'i-lucide-timer-off' },
} as const

// --- Código de enrolamiento -------------------------------------------------------------
const codeFor = ref<Kiosk | null>(null)
const code = ref<KioskEnrollmentCode | null>(null)
const codeOpen = computed({
  get: () => !!codeFor.value,
  set: (v) => {
    if (!v) {
      codeFor.value = null
      code.value = null
      refresh()
    }
  },
})
async function generate(k: Kiosk) {
  codeFor.value = k
  code.value = null
  try {
    code.value = await unwrap(
      $api.POST('/kiosks/{kiosk_id}/enrollment-codes', {
        params: { path: { kiosk_id: k.id }, header: { 'Idempotency-Key': idempotencyKey() } },
      }),
    )
  } catch (e) {
    codeFor.value = null
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  }
}
const remaining = computed(() => {
  if (!code.value) return 0
  return Math.max(0, Math.round((new Date(code.value.expires_at).getTime() - now.value) / 1000))
})
const countdown = computed(
  () => `${Math.floor(remaining.value / 60)}:${String(remaining.value % 60).padStart(2, '0')}`,
)

// --- Revocar ------------------------------------------------------------------------------
const revoking = ref<Kiosk | null>(null)
const revokeOpen = computed({
  get: () => !!revoking.value,
  set: (v) => {
    if (!v) revoking.value = null
  },
})
async function revoke() {
  const k = revoking.value
  if (!k) return
  try {
    await unwrap(
      $api.POST('/kiosks/{kiosk_id}/revoke', { params: { path: { kiosk_id: k.id } }, body: {} }),
    )
    toast.add({
      title: t('kiosks.revoked', { name: k.name }),
      color: 'success',
      icon: 'i-lucide-check',
    })
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  }
  revoking.value = null
  refresh()
}

// --- Alta --------------------------------------------------------------------------------
const formOpen = ref(false)
const form = reactive({
  name: '',
  playlist: '' as string,
  cidrs: '',
  personal: false,
  personalReason: '',
  criticalBanner: true,
})
const formErrors = ref<Record<string, string>>({})
const saving = ref(false)
function openForm() {
  Object.assign(form, {
    name: '',
    playlist: playlists.value?.[0]?.id ?? '',
    cidrs: '',
    personal: false,
    personalReason: '',
    criticalBanner: true,
  })
  formErrors.value = {}
  formOpen.value = true
}
async function create() {
  formErrors.value = {}
  saving.value = true
  try {
    const k = await unwrap(
      $api.POST('/kiosks', {
        body: {
          name: form.name.trim(),
          playlist_id: form.playlist || null,
          allowed_cidrs: form.cidrs.split(/[,\s]+/).filter(Boolean),
          show_personal_data: form.personal,
          show_personal_data_reason: form.personal ? form.personalReason.trim() : null,
          critical_finding_banner: form.criticalBanner,
        },
      }),
    )
    formOpen.value = false
    await refresh()
    generate(k)
  } catch (e) {
    if (e instanceof ApiError && e.problem.errors?.length) {
      formErrors.value = Object.fromEntries(e.problem.errors.map((x) => [x.field, x.message]))
    } else formErrors.value = { _: t(describeError(e).messageKey) }
  } finally {
    saving.value = false
  }
}
const playlistItems = computed(() =>
  (playlists.value ?? []).map((p) => ({ label: p.name, value: p.id })),
)
</script>

<template>
  <AppPage :title="t('nav.items.kiosks')" panel-id="kiosks">
    <template #toolbar-left>
      <p class="text-muted text-sm">{{ t('kiosks.intro') }}</p>
    </template>
    <template #toolbar-right>
      <UButton
        icon="i-lucide-plus"
        :label="t('kiosks.add')"
        data-testid="add-kiosk"
        @click="openForm"
      />
    </template>

    <LoadingState v-if="status === 'pending' && !kiosks" :rows="3" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <ul v-else class="grid gap-3 lg:grid-cols-2" data-testid="kiosks">
      <li v-for="k in kiosks ?? []" :key="k.id" :data-kiosk="k.name">
        <UCard :ui="{ body: 'p-4 sm:p-4 flex flex-col gap-3' }">
          <div class="flex items-start justify-between gap-2">
            <h2 class="text-highlighted font-semibold">{{ k.name }}</h2>
            <UBadge
              :color="PRESENCE[presence(k)].color"
              variant="subtle"
              :icon="PRESENCE[presence(k)].icon"
              :label="t(`kiosks.presence.${presence(k)}`)"
              data-testid="kiosk-presence"
            />
          </div>
          <p class="text-muted text-sm">
            <template v-if="presence(k) === 'online'"
              >{{ t('kiosks.lastSignal') }} <RelativeTime :at="k.last_seen_at"
            /></template>
            <template v-else-if="presence(k) === 'offline' && k.last_seen_at">
              {{ t('kiosks.offlineSince', { time: d(new Date(k.last_seen_at), 'long') }) }}
            </template>
            <template v-else-if="presence(k) === 'pending'">{{ t('kiosks.pendingHint') }}</template>
            <template v-else>{{ t('kiosks.revokedHint') }}</template>
          </p>
          <dl class="grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
            <div>
              <dt class="text-muted text-xs">{{ t('kiosks.lastIp') }}</dt>
              <dd class="font-mono">{{ k.last_ip ?? '—' }}</dd>
            </div>
            <div>
              <dt class="text-muted text-xs">{{ t('kiosks.version') }}</dt>
              <dd>{{ k.frontend_version ?? '—' }}</dd>
            </div>
            <div>
              <dt class="text-muted text-xs">{{ t('kiosks.network') }}</dt>
              <dd class="font-mono break-all">
                {{ k.allowed_cidrs.join(', ') || t('kiosks.anyNetwork') }}
              </dd>
            </div>
            <div>
              <dt class="text-muted text-xs">{{ t('kiosks.personal') }}</dt>
              <dd>{{ k.show_personal_data ? t('kiosks.personalOn') : t('kiosks.personalOff') }}</dd>
            </div>
          </dl>
          <p v-if="k.cidr_risk && k.status !== 'revoked'" class="text-warning flex gap-1.5 text-sm">
            <UIcon
              name="i-lucide-triangle-alert"
              class="mt-0.5 size-4 shrink-0"
              aria-hidden="true"
            />
            {{ t('kiosks.cidrRisk') }}
          </p>
          <div v-if="k.status !== 'revoked'" class="flex flex-wrap gap-2">
            <UButton
              size="sm"
              icon="i-lucide-key-square"
              :label="t('kiosks.generateCode')"
              data-testid="generate-code"
              @click="generate(k)"
            />
            <UButton
              size="sm"
              color="neutral"
              variant="outline"
              icon="i-lucide-ban"
              :label="t('kiosks.revoke')"
              data-testid="revoke-kiosk"
              @click="revoking = k"
            />
          </div>
        </UCard>
      </li>
    </ul>

    <UModal
      v-model:open="codeOpen"
      :title="t('kiosks.codeTitle', { name: codeFor?.name ?? '' })"
      :description="t('kiosks.codeHint')"
    >
      <template #body>
        <div
          v-if="code"
          class="flex flex-col items-center gap-4 text-center"
          data-testid="enrollment-code-box"
        >
          <p
            class="text-highlighted font-mono text-5xl font-semibold tracking-[0.25em]"
            data-testid="enrollment-code"
          >
            {{ code.code }}
          </p>
          <p :class="remaining ? 'text-muted' : 'text-error'" class="tabular" aria-live="polite">
            {{ remaining ? t('kiosks.expiresIn', { time: countdown }) : t('kiosks.codeExpired') }}
          </p>
          <p class="text-muted text-sm">{{ t('kiosks.openKiosk') }}</p>
          <div class="flex flex-wrap justify-center gap-2">
            <CopyButton :value="code.code" />
            <CopyButton :value="code.qr_url" :label="t('kiosks.copyLink')" />
          </div>
          <p class="text-dimmed text-xs">{{ t('kiosks.fragmentNote') }}</p>
        </div>
        <LoadingState v-else :rows="2" />
      </template>
    </UModal>

    <UModal
      v-model:open="revokeOpen"
      :title="t('kiosks.revokeTitle', { name: revoking?.name ?? '' })"
      :description="t('kiosks.revokeBody')"
    >
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton
            color="neutral"
            variant="outline"
            :label="t('common.cancel')"
            autofocus
            @click="revoking = null"
          />
          <UButton
            color="error"
            icon="i-lucide-ban"
            :label="t('kiosks.revokeSubmit')"
            data-testid="revoke-confirm"
            @click="revoke"
          />
        </div>
      </template>
    </UModal>

    <USlideover
      v-model:open="formOpen"
      :title="t('kiosks.form.title')"
      :description="t('kiosks.form.description')"
    >
      <template #body>
        <form id="kiosk-form" class="flex flex-col gap-4" @submit.prevent="create">
          <UFormField :label="t('kiosks.form.name')" :error="formErrors.name" required>
            <UInput
              v-model="form.name"
              class="w-full"
              :placeholder="t('kiosks.form.namePlaceholder')"
              data-testid="kiosk-name"
            />
          </UFormField>
          <UFormField :label="t('kiosks.form.playlist')">
            <USelect v-model="form.playlist" :items="playlistItems" class="w-full" />
          </UFormField>
          <UFormField :label="t('kiosks.form.cidrs')" :help="t('kiosks.form.cidrsHelp')">
            <UInput
              v-model="form.cidrs"
              class="w-full"
              :ui="{ base: 'font-mono' }"
              placeholder="10.20.255.0/24"
            />
          </UFormField>
          <USwitch
            v-model="form.criticalBanner"
            :label="t('kiosks.form.critical')"
            :description="t('kiosks.form.criticalHelp')"
          />
          <USwitch
            v-model="form.personal"
            :label="t('kiosks.form.personal')"
            :description="t('kiosks.form.personalHelp')"
          />
          <UFormField
            v-if="form.personal"
            :label="t('kiosks.form.personalReason')"
            :error="formErrors.show_personal_data_reason"
            required
          >
            <UTextarea v-model="form.personalReason" :rows="2" class="w-full" />
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
            form="kiosk-form"
            :loading="saving"
            :label="t('kiosks.form.submit')"
            data-testid="kiosk-submit-form"
          />
        </div>
      </template>
    </USlideover>
  </AppPage>
</template>
