<script setup lang="ts">
import type { WireguardHub, WireguardHubUpdate } from '~~/types/api'

/**
 * Editar el hub WireGuard (`PATCH /platform/wireguard/hubs/{id}` con `If-Match`, re-autenticación
 * reciente): nombre, endpoint público, MTU y keepalive por defecto. Solo se envía lo que cambió.
 * Los valores van a los scripts de conexión que se generen desde ahora; los routers ya
 * conectados conservan los suyos hasta que se les genere uno nuevo. El puerto UDP no se cambia
 * aquí: lo publica Docker y se cambia con el instalador (`install.sh --wg-port`).
 * Ante `412` se carga la versión vigente en el formulario y se avisa para revisar.
 */
const props = defineProps<{ hub: WireguardHub }>()
const emit = defineEmits<{ saved: [hub: WireguardHub]; changed: [] }>()
const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
const { withReauth } = useReauth()

const MTU_RANGE = { min: 1280, max: 1500 } as const
const KEEPALIVE_RANGE = { min: 0, max: 120 } as const

const form = reactive({
  name: '',
  endpoint: '',
  mtu: 1420 as number | null,
  keepalive: 25 as number | null,
})
const errors = ref<Record<string, string>>({})
const conflict = ref(false)
const saving = ref(false)
/** Versión sobre la que se guarda (la del 412 si hubo conflicto). */
const base = ref<WireguardHub | null>(null)

function fill(hub: WireguardHub) {
  base.value = hub
  form.name = hub.name
  form.endpoint = hub.endpoint
  form.mtu = hub.mtu ?? 1420
  form.keepalive = hub.persistent_keepalive_seconds ?? 25
}

watch(
  open,
  (o) => {
    if (!o) return
    fill(props.hub)
    errors.value = {}
    conflict.value = false
  },
  { immediate: true },
)

const endpointChanged = computed(
  () => !!base.value && form.endpoint.trim() !== '' && form.endpoint.trim() !== base.value.endpoint,
)

const ENDPOINT_FORBIDDEN = /[\s/]|^[a-z]+:\/\/|:\d+$/i

function values(): WireguardHubUpdate | null {
  const e: Record<string, string> = {}
  const name = form.name.trim()
  if (!name || name.length > 64) e.name = t('platform.wireguard.edit.nameInvalid')
  const endpoint = form.endpoint.trim()
  // Host o IP sin esquema ni puerto (el puerto es el de Docker).
  const v6 = endpoint.includes('::') || endpoint.split(':').length > 2
  if (!endpoint || endpoint.length > 253 || (!v6 && ENDPOINT_FORBIDDEN.test(endpoint))) {
    e.endpoint = t('platform.wireguard.edit.endpointInvalid')
  }
  const mtu = form.mtu
  if (mtu === null || !Number.isInteger(mtu) || mtu < MTU_RANGE.min || mtu > MTU_RANGE.max) {
    e.mtu = t('platform.wireguard.edit.mtuInvalid', MTU_RANGE)
  }
  const keepalive = form.keepalive
  if (
    keepalive === null ||
    !Number.isInteger(keepalive) ||
    keepalive < KEEPALIVE_RANGE.min ||
    keepalive > KEEPALIVE_RANGE.max
  ) {
    e.persistent_keepalive_seconds = t('platform.wireguard.edit.keepaliveInvalid', KEEPALIVE_RANGE)
  }
  errors.value = e
  if (Object.keys(e).length) return null
  return { name, endpoint, mtu: mtu!, persistent_keepalive_seconds: keepalive! }
}

async function save() {
  const next = values()
  const hub = base.value
  if (!next || !hub) return
  const patch = mergePatch<WireguardHubUpdate>(
    {
      name: hub.name,
      endpoint: hub.endpoint,
      mtu: hub.mtu,
      persistent_keepalive_seconds: hub.persistent_keepalive_seconds,
    },
    next,
  )
  if (!Object.keys(patch).length) {
    open.value = false
    return
  }
  saving.value = true
  try {
    const saved = await withReauth(
      () =>
        unwrap(
          $api.PATCH('/platform/wireguard/hubs/{hub_id}', {
            params: {
              path: { hub_id: hub.id },
              header: { 'If-Match': ifMatch(hub.version ?? 1) },
            },
            body: patch,
          }),
        ),
      t('platform.wireguard.edit.reauth'),
    )
    toast.add({
      title: t('platform.wireguard.edit.saved', { name: saved.name }),
      description: t('platform.wireguard.edit.savedHint'),
      icon: 'i-lucide-check',
      color: 'success',
    })
    open.value = false
    emit('saved', saved)
  } catch (e) {
    if (e instanceof ApiError && e.status === 412 && e.problem.current) {
      fill(e.problem.current as unknown as WireguardHub)
      conflict.value = true
      errors.value = {}
      emit('changed')
    } else if (isReauthRequired(e)) {
      errors.value = { _: t('platform.wireguard.reauthCancelled') }
    } else if (e instanceof ApiError && e.status === 422 && e.problem.errors?.length) {
      errors.value = fieldErrors(e.problem)
    } else if (e instanceof ApiError && e.status === 404) {
      errors.value = { _: t('platform.wireguard.hubGone') }
      emit('changed')
    } else {
      errors.value = { _: t(describeError(e).messageKey) }
    }
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <UModal
    v-model:open="open"
    :title="t('platform.wireguard.edit.title', { name: hub.name })"
    :description="t('platform.wireguard.edit.description')"
    data-testid="wg-hub-edit-modal"
  >
    <template #body>
      <form id="wg-hub-form" class="flex flex-col gap-4" novalidate @submit.prevent="save">
        <UAlert
          v-if="conflict"
          color="warning"
          variant="subtle"
          icon="i-lucide-git-compare"
          :title="t('platform.wireguard.edit.conflictTitle')"
          :description="t('platform.wireguard.edit.conflictBody')"
          data-testid="wg-hub-conflict"
        />
        <UAlert
          color="info"
          variant="subtle"
          icon="i-lucide-info"
          :title="t('platform.wireguard.edit.scopeTitle')"
          :description="t('platform.wireguard.edit.scopeBody')"
        />
        <UFormField :label="t('platform.wireguard.edit.name')" :error="errors.name" required>
          <UInput
            v-model="form.name"
            class="w-full"
            maxlength="64"
            autocomplete="off"
            :ui="{ base: 'font-mono' }"
            data-testid="wg-hub-name"
          />
        </UFormField>
        <UFormField
          :label="t('platform.wireguard.endpoint')"
          :help="t('platform.wireguard.edit.endpointHelp')"
          :error="errors.endpoint"
          required
        >
          <UInput
            v-model="form.endpoint"
            class="w-full"
            maxlength="253"
            autocomplete="off"
            spellcheck="false"
            :ui="{ base: 'font-mono' }"
            data-testid="wg-hub-endpoint"
          />
        </UFormField>
        <UAlert
          v-if="endpointChanged"
          color="warning"
          variant="soft"
          icon="i-lucide-triangle-alert"
          :title="t('platform.wireguard.edit.endpointWarnTitle')"
          :description="t('platform.wireguard.edit.endpointWarnBody', { old: base?.endpoint })"
          data-testid="wg-hub-endpoint-warning"
        />
        <div class="grid gap-4 sm:grid-cols-2">
          <UFormField
            :label="t('platform.wireguard.mtu')"
            :help="t('platform.wireguard.edit.mtuHelp')"
            :error="errors.mtu"
            required
          >
            <UInputNumber
              v-model="form.mtu"
              :min="MTU_RANGE.min"
              :max="MTU_RANGE.max"
              class="w-full"
              data-testid="wg-hub-mtu"
            />
          </UFormField>
          <UFormField
            :label="t('platform.wireguard.edit.keepalive')"
            :help="t('platform.wireguard.edit.keepaliveHelp')"
            :error="errors.persistent_keepalive_seconds"
            required
          >
            <UInputNumber
              v-model="form.keepalive"
              :min="KEEPALIVE_RANGE.min"
              :max="KEEPALIVE_RANGE.max"
              class="w-full"
              data-testid="wg-hub-keepalive"
            />
          </UFormField>
        </div>
        <div class="border-default rounded-md border p-3" data-testid="wg-hub-port">
          <p class="text-highlighted text-sm font-medium">
            {{ t('platform.wireguard.edit.port') }}
            <span class="font-mono tabular">{{ hub.listen_port }}/udp</span>
          </p>
          <p class="text-muted mt-1 text-sm">
            {{ t('platform.wireguard.edit.portHelp') }}
          </p>
          <code class="text-highlighted mt-2 block font-mono text-xs break-all">
            sudo ./install.sh --wg-port {{ t('platform.wireguard.edit.portPlaceholder') }}
          </code>
        </div>
        <UAlert
          v-if="errors._"
          color="error"
          variant="subtle"
          icon="i-lucide-circle-alert"
          :title="errors._"
          data-testid="wg-hub-error"
        />
      </form>
    </template>
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton
          color="neutral"
          variant="ghost"
          :label="t('common.cancel')"
          @click="open = false"
        />
        <UButton
          type="submit"
          form="wg-hub-form"
          icon="i-lucide-save"
          :loading="saving"
          :label="t('common.save')"
          data-testid="wg-hub-submit"
        />
      </div>
    </template>
  </UModal>
</template>
