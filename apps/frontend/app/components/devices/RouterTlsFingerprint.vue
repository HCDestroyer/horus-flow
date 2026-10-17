<script setup lang="ts">
import type { Router, RouterTlsFingerprint } from '~~/types/api'

/**
 * Huella TLS del servicio REST del router (`GET/POST /routers/{id}/tls-fingerprint…`). En el
 * alta Horus lee el certificado (solo el saludo TLS, sin enviar la contraseña) y la huella queda
 * **pendiente** hasta que un administrador compara el SHA-256 con el del router
 * (`/certificate print detail`) y pulsa «Confirmar huella» (`devices.credentials.write`,
 * re-autenticación). Si el router presenta otra huella, las lecturas se detienen y se avisa:
 * nunca se acepta sola. `compact` muestra solo el aviso (cabecera de la ficha).
 */
const props = defineProps<{ router: Router; compact?: boolean }>()
const emit = defineEmits<{ changed: [RouterTlsFingerprint]; review: [] }>()

const { t } = useI18n()
const { $api } = useNuxtApp()
const can = useCan()
const toast = useToast()
const { withReauth } = useReauth()

const {
  data: pin,
  error,
  refresh,
} = useTenantQuery(
  () => `router:${props.router.id}:tls-fingerprint`,
  () =>
    unwrap(
      $api.GET('/routers/{router_id}/tls-fingerprint', {
        params: { path: { router_id: props.router.id } },
      }),
    ),
)

const hasTunnel = computed(() => props.router.onboarding_state !== 'pending_configuration')
const canCapture = computed(() => can('devices.update') && hasTunnel.value)
const canConfirm = computed(() => can('devices.credentials.write'))
const needsAction = computed(
  () => pin.value?.status === 'pending_confirmation' || pin.value?.status === 'changed',
)

const capturing = ref(false)
const confirming = ref(false)
const failure = ref<string | null>(null)
const confirmOpen = ref(false)

function set(v: RouterTlsFingerprint) {
  pin.value = v
  emit('changed', v)
}

async function capture() {
  capturing.value = true
  failure.value = null
  try {
    set(
      await unwrap(
        $api.POST('/routers/{router_id}/tls-fingerprint/capture', {
          params: { path: { router_id: props.router.id } },
        }),
      ),
    )
  } catch (e) {
    failure.value =
      e instanceof ApiError && e.code === 'ROUTER_UNREACHABLE'
        ? t('tlsPin.unreachable')
        : t(describeError(e).messageKey)
  } finally {
    capturing.value = false
  }
}

async function confirm() {
  const fp = pin.value?.observed_sha256
  if (!fp) return
  confirming.value = true
  failure.value = null
  try {
    const v = await withReauth(
      () =>
        unwrap(
          $api.POST('/routers/{router_id}/tls-fingerprint/confirm', {
            params: { path: { router_id: props.router.id } },
            body: { fingerprint_sha256: fp },
          }),
        ),
      t('tlsPin.reauth'),
    )
    set(v)
    confirmOpen.value = false
    toast.add({ title: t('tlsPin.confirmed'), icon: 'i-lucide-shield-check', color: 'success' })
  } catch (e) {
    if (isReauthRequired(e)) {
      failure.value = t('tlsPin.reauthCancelled')
    } else if (e instanceof ApiError && e.code === 'ROUTER_TLS_FINGERPRINT_MISMATCH') {
      failure.value = t('tlsPin.mismatch')
      confirmOpen.value = false
      refresh()
    } else {
      failure.value = t(describeError(e).messageKey)
    }
  } finally {
    confirming.value = false
  }
}

const statusColor = computed(
  () =>
    (
      ({
        confirmed: 'success',
        pending_confirmation: 'warning',
        changed: 'error',
        not_captured: 'neutral',
      }) as const
    )[pin.value?.status ?? 'not_captured'],
)
</script>

<template>
  <div v-if="compact">
    <UAlert
      v-if="pin && needsAction"
      :color="pin.status === 'changed' ? 'error' : 'warning'"
      variant="subtle"
      :icon="pin.status === 'changed' ? 'i-lucide-shield-alert' : 'i-lucide-shield-question'"
      :title="t(`tlsPin.banner.${pin.status}.title`)"
      :description="t(`tlsPin.banner.${pin.status}.body`)"
      data-testid="tls-pin-banner"
    >
      <template #actions>
        <UButton
          size="sm"
          color="neutral"
          variant="outline"
          icon="i-lucide-fingerprint"
          :label="t('tlsPin.review')"
          data-testid="tls-pin-review"
          @click="emit('review')"
        />
      </template>
    </UAlert>
  </div>
  <UCard v-else data-testid="tls-pin-card">
    <div class="flex flex-col gap-3">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <h2 class="text-highlighted font-semibold">{{ t('tlsPin.title') }}</h2>
        <UBadge
          v-if="pin"
          :color="statusColor"
          variant="subtle"
          :label="t(`tlsPin.status.${pin.status}`)"
          data-testid="tls-pin-status"
        />
      </div>
      <p class="text-muted text-sm">{{ t('tlsPin.body') }}</p>
      <ErrorState v-if="error" :error="error" @retry="refresh" />
      <template v-else-if="pin">
        <UAlert
          v-if="pin.status === 'changed'"
          color="error"
          variant="subtle"
          icon="i-lucide-shield-alert"
          :title="t('tlsPin.banner.changed.title')"
          :description="t('tlsPin.changedBody')"
          data-testid="tls-pin-changed"
        />
        <dl class="grid gap-2 text-sm">
          <div v-if="pin.pinned_sha256">
            <dt class="text-muted">{{ t('tlsPin.pinned') }}</dt>
            <dd class="font-mono text-xs break-all" data-testid="tls-pin-pinned">
              {{ pin.pinned_sha256 }}
            </dd>
            <dd v-if="pin.confirmed_at" class="text-muted text-xs">
              <RelativeTime :at="pin.confirmed_at" :prefix="t('tlsPin.confirmedAt')" />
            </dd>
          </div>
          <div v-if="pin.observed_sha256">
            <dt class="text-muted">
              {{ pin.status === 'changed' ? t('tlsPin.observedNew') : t('tlsPin.observed') }}
            </dt>
            <dd class="font-mono text-xs break-all" data-testid="tls-pin-observed">
              {{ pin.observed_sha256 }}
            </dd>
            <dd v-if="pin.observed_at" class="text-muted text-xs">
              <RelativeTime :at="pin.observed_at" :prefix="t('tlsPin.observedAt')" />
            </dd>
          </div>
        </dl>
        <p v-if="pin.status === 'not_captured'" class="text-muted text-sm">
          {{ hasTunnel ? t('tlsPin.notCaptured') : t('tlsPin.needsTunnel') }}
        </p>
        <p v-if="needsAction" class="text-default text-sm">{{ t('tlsPin.howToCompare') }}</p>
        <p v-if="failure" class="text-error text-sm" role="alert" data-testid="tls-pin-error">
          {{ failure }}
        </p>
        <div class="flex flex-wrap gap-2">
          <UButton
            v-if="canCapture"
            color="neutral"
            variant="outline"
            icon="i-lucide-scan-line"
            :loading="capturing"
            :label="pin.status === 'not_captured' ? t('tlsPin.capture') : t('tlsPin.recapture')"
            data-testid="tls-pin-capture"
            @click="capture"
          />
          <UButton
            v-if="needsAction && canConfirm"
            :color="pin.status === 'changed' ? 'error' : 'primary'"
            icon="i-lucide-shield-check"
            :label="pin.status === 'changed' ? t('tlsPin.repin') : t('tlsPin.confirm')"
            data-testid="tls-pin-confirm"
            @click="confirmOpen = true"
          />
        </div>
        <p v-if="needsAction && !canConfirm" class="text-muted text-sm">
          {{ t('tlsPin.noPermission') }}
        </p>
      </template>
      <LoadingState v-else :rows="2" />
    </div>
    <UModal
      v-model:open="confirmOpen"
      :title="pin?.status === 'changed' ? t('tlsPin.repin') : t('tlsPin.confirm')"
      :description="t('tlsPin.confirmDescription')"
    >
      <template #body>
        <div class="flex flex-col gap-3">
          <p class="font-mono text-sm break-all" data-testid="tls-pin-confirm-value">
            {{ pin?.observed_sha256 }}
          </p>
          <UAlert
            v-if="pin?.status === 'changed'"
            color="error"
            variant="subtle"
            icon="i-lucide-triangle-alert"
            :description="t('tlsPin.repinWarning', { previous: pin?.pinned_sha256 ?? '' })"
          />
        </div>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton
            color="neutral"
            variant="ghost"
            :label="t('common.cancel')"
            @click="confirmOpen = false"
          />
          <UButton
            :color="pin?.status === 'changed' ? 'error' : 'primary'"
            icon="i-lucide-shield-check"
            :loading="confirming"
            :label="t('tlsPin.confirmSubmit')"
            data-testid="tls-pin-confirm-submit"
            @click="confirm"
          />
        </div>
      </template>
    </UModal>
  </UCard>
</template>
