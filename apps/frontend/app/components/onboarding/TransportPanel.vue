<script setup lang="ts">
import type { Router } from '~~/types/api'
import type { ConnectionMode, TransportPath } from '~/utils/transport'

/**
 * Transporte del router con Horus (docs/tunnels.md §3): WireGuard, IPsec IKEv2, GRE o directo,
 * según el fabricante, con el recomendado marcado. Sin cifrado (GRE/directo) hay que declarar el
 * camino; por Internet, además, confirmar el riesgo de forma explícita (re-autenticación y
 * auditoría en el backend). `PUT /routers/{id}/transport` (`devices.credentials.write`).
 */
const props = defineProps<{ router: Router }>()
const emit = defineEmits<{ changed: [Router] }>()

const { t } = useI18n()
const { $api } = useNuxtApp()
const can = useCan()
const toast = useToast()
const { withReauth } = useReauth()
const canWrite = computed(() => can('devices.credentials.write'))

const editing = ref(false)
const mode = ref<ConnectionMode>(props.router.connection_mode)
const remoteAddress = ref(props.router.tunnel_remote_address ?? '')
const remoteId = ref(props.router.tunnel_remote_id ?? '')
const path = ref<TransportPath>(
  props.router.protection_level === 'unencrypted_internet' ? 'internet' : 'private',
)
const confirmInternet = ref(false)
const saving = ref(false)
const saveError = shallowRef<unknown>(null)
const fieldErrors = ref<Record<string, string>>({})

const modes = computed(() => modesFor(props.router.vendor))
const suggested = computed(() => suggestedMode(props.router.vendor))
const items = computed(() =>
  modes.value.map((m) => ({
    value: m,
    label:
      t(`transport.mode.${m}`) + (m === suggested.value ? ` · ${t('transport.recommended')}` : ''),
    description: t(`transport.modeHelp.${m}`),
  })),
)
const pathItems = computed(() => [
  {
    value: 'private',
    label: t('transport.path.private'),
    description: t('transport.path.privateHelp'),
  },
  {
    value: 'internet',
    label: t('transport.path.internet'),
    description: t('transport.path.internetHelp'),
  },
])
const encrypted = computed(() => isEncrypted(mode.value))
const level = computed(() => protectionFor(mode.value, encrypted.value ? null : path.value))
const needsConfirm = computed(() => level.value === 'unencrypted_internet')

function open() {
  mode.value = props.router.connection_mode
  remoteAddress.value = props.router.tunnel_remote_address ?? ''
  remoteId.value = props.router.tunnel_remote_id ?? ''
  path.value = props.router.protection_level === 'unencrypted_internet' ? 'internet' : 'private'
  confirmInternet.value = false
  fieldErrors.value = {}
  saveError.value = null
  editing.value = true
}

function validate(): boolean {
  const fe: Record<string, string> = {}
  const addr = remoteAddress.value.trim()
  const id = remoteId.value.trim()
  if (mode.value === 'gre' && !isIPv4(addr))
    fe.tunnel_remote_address = t('transport.errors.addressRequired')
  if (mode.value === 'ipsec') {
    if (addr && !isIPv4(addr)) fe.tunnel_remote_address = t('transport.errors.addressInvalid')
    if (id && !isIkeId(id)) fe.tunnel_remote_id = t('transport.errors.idInvalid')
    if (!addr && !id) fe.tunnel_remote_id = t('transport.errors.idRequired')
  }
  if (needsConfirm.value && !confirmInternet.value)
    fe.confirm = t('transport.errors.confirmRequired')
  fieldErrors.value = fe
  return Object.keys(fe).length === 0
}

async function save() {
  if (!validate()) return
  saving.value = true
  saveError.value = null
  const tunnel = mode.value === 'ipsec' || mode.value === 'gre'
  try {
    const next = await withReauth(
      () =>
        unwrap(
          $api.PUT('/routers/{router_id}/transport', {
            params: { path: { router_id: props.router.id } },
            body: {
              connection_mode: mode.value,
              tunnel_remote_address: tunnel ? remoteAddress.value.trim() || null : null,
              tunnel_remote_id: mode.value === 'ipsec' ? remoteId.value.trim() || null : null,
              path: encrypted.value ? null : path.value,
              confirm_unencrypted_internet: needsConfirm.value && confirmInternet.value,
            },
          }),
        ),
      t('transport.reauth'),
    )
    editing.value = false
    toast.add({ title: t('transport.saved'), icon: 'i-lucide-check', color: 'success' })
    emit('changed', next)
  } catch (e) {
    if (e instanceof ApiError) {
      if (e.code === 'TUNNEL_REMOTE_IN_USE') {
        fieldErrors.value = {
          [mode.value === 'gre' ? 'tunnel_remote_address' : 'tunnel_remote_id']:
            t('transport.errors.inUse'),
        }
        return
      }
      if (e.code === 'UNENCRYPTED_TRANSPORT_CONFIRMATION_REQUIRED') {
        fieldErrors.value = { confirm: t('transport.errors.confirmRequired') }
        return
      }
    }
    saveError.value = e
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <UCard data-testid="transport-panel">
    <div class="flex flex-col gap-3">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <div class="flex min-w-0 flex-col gap-1">
          <h2 class="text-highlighted font-semibold">{{ t('transport.title') }}</h2>
          <div class="flex flex-wrap items-center gap-2">
            <ProtectionBadge
              :level="router.protection_level"
              :mode="router.connection_mode"
              data-testid="transport-current"
            />
            <span
              v-if="router.tunnel_remote_id || router.tunnel_remote_address"
              class="text-muted font-mono text-sm"
            >
              {{ router.tunnel_remote_id ?? router.tunnel_remote_address }}
            </span>
          </div>
        </div>
        <UButton
          v-if="canWrite && !editing"
          color="neutral"
          variant="outline"
          icon="i-lucide-arrow-left-right"
          :label="t('transport.change')"
          data-testid="transport-change"
          @click="open"
        />
      </div>
      <UAlert
        v-if="router.protection_level === 'unencrypted_internet'"
        color="error"
        variant="subtle"
        icon="i-lucide-shield-alert"
        :title="t('transport.internetWarningTitle')"
        :description="t('transport.internetWarningBody')"
        data-testid="transport-internet-warning"
      />
      <p v-else-if="router.connection_mode !== suggested && !editing" class="text-muted text-sm">
        {{ t('transport.suggestion', { mode: t(`transport.mode.${suggested}`) }) }}
      </p>

      <form
        v-if="editing"
        class="flex flex-col gap-4"
        data-testid="transport-form"
        @submit.prevent="save"
      >
        <URadioGroup
          v-model="mode"
          :items="items"
          :legend="t('transport.choose')"
          data-testid="transport-modes"
        />
        <div v-if="mode === 'ipsec' || mode === 'gre'" class="grid gap-3 sm:grid-cols-2">
          <UFormField
            :label="t('transport.remoteAddress')"
            :help="
              t(
                mode === 'gre'
                  ? 'transport.remoteAddressHelpGre'
                  : 'transport.remoteAddressHelpIpsec',
              )
            "
            :error="fieldErrors.tunnel_remote_address"
            :required="mode === 'gre'"
          >
            <UInput
              v-model="remoteAddress"
              class="w-full"
              :ui="{ base: 'font-mono' }"
              autocomplete="off"
              spellcheck="false"
              placeholder="198.51.100.7"
              data-testid="transport-remote-address"
            />
          </UFormField>
          <UFormField
            v-if="mode === 'ipsec'"
            :label="t('transport.remoteId')"
            :help="t('transport.remoteIdHelp')"
            :error="fieldErrors.tunnel_remote_id"
          >
            <UInput
              v-model="remoteId"
              class="w-full"
              :ui="{ base: 'font-mono' }"
              autocomplete="off"
              spellcheck="false"
              placeholder="rt-centro.isp.example"
              data-testid="transport-remote-id"
            />
          </UFormField>
        </div>
        <URadioGroup
          v-if="!encrypted"
          v-model="path"
          :items="pathItems"
          :legend="t('transport.path.legend')"
          data-testid="transport-path"
        />
        <div class="flex flex-wrap items-center gap-2">
          <span class="text-muted text-sm">{{ t('transport.resulting') }}</span>
          <ProtectionBadge :level="level" data-testid="transport-resulting" />
        </div>
        <UAlert
          v-if="needsConfirm"
          color="error"
          variant="subtle"
          icon="i-lucide-shield-alert"
          :title="t('transport.confirmTitle')"
          :description="t('transport.confirmBody')"
          data-testid="transport-confirm-alert"
        >
          <template #actions>
            <UCheckbox
              v-model="confirmInternet"
              :label="t('transport.confirmLabel')"
              data-testid="transport-confirm"
            />
          </template>
        </UAlert>
        <p v-if="fieldErrors.confirm" class="text-error text-sm" role="alert">
          {{ fieldErrors.confirm }}
        </p>
        <p class="text-muted text-sm">{{ t('transport.afterChange') }}</p>
        <ErrorState v-if="saveError" :error="saveError" @retry="save" />
        <div class="flex flex-wrap gap-2">
          <UButton
            type="submit"
            :color="needsConfirm ? 'error' : 'primary'"
            :loading="saving"
            icon="i-lucide-check"
            :label="t('transport.save')"
            data-testid="transport-save"
          />
          <UButton
            color="neutral"
            variant="ghost"
            :label="t('common.cancel')"
            @click="editing = false"
          />
        </div>
      </form>
      <p v-else-if="!canWrite" class="text-muted text-sm">{{ t('transport.noPermission') }}</p>
    </div>
  </UCard>
</template>
