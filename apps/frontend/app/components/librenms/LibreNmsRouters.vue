<script setup lang="ts">
import type {
  LibreNmsMonitoringMode,
  LibreNmsObject,
  LibreNmsRouter,
  LibreNmsSnmpCredentials,
} from '~~/types/api'

/**
 * IP de gestión y modo de monitoreo de cada router en LibreNMS (D17). La IP del túnel
 * WireGuard hacia Horus no es alcanzable desde LibreNMS: cada router necesita una IP de gestión
 * propia. Modos: solo ping (por defecto), SNMP v2c/v3 propio para LibreNMS (credenciales
 * write-only, 🔒) o las credenciales SNMPv3 de Horus con consentimiento explícito (auditado).
 * Se aplica en la siguiente sincronización; "Borrar de LibreNMS" solo existe para los
 * dispositivos que creó Horus.
 */
const props = defineProps<{
  channelId: string | null
  devices: Map<string, LibreNmsObject>
  manage: boolean
}>()
const emit = defineEmits<{ deviceDeleted: [] }>()

const { t } = useI18n()
const { $api } = useNuxtApp()
const { withReauth } = useReauth()
const toast = useToast()

const {
  data: routers,
  error,
  status,
  refresh,
} = useTenantQuery('librenms-routers', () =>
  unwrap($api.GET('/librenms/routers')).then((r) => r.data),
)

interface Draft {
  ip: string
  mode: LibreNmsMonitoringMode
  consent: boolean
}
const drafts = reactive<Record<string, Draft>>({})
watch(
  routers,
  (list) => {
    for (const r of list ?? []) {
      drafts[r.router_id] = {
        ip: r.mgmt_ip ?? '',
        mode: r.mode,
        consent: r.horus_snmp_reuse_consent,
      }
    }
  },
  { immediate: true },
)
const MODES: LibreNmsMonitoringMode[] = ['ping', 'snmp_v2c', 'snmp_v3', 'horus_snmp_v3']
const modeItems = computed(() => MODES.map((m) => ({ label: t(`librenms.mode.${m}`), value: m })))
const errors = ref<Record<string, string>>({})
const saving = ref<string | null>(null)

async function save(r: LibreNmsRouter) {
  const draft = drafts[r.router_id]
  if (!draft) return
  saving.value = r.router_id
  errors.value = {}
  try {
    await unwrap(
      $api.PUT('/librenms/routers/{router_id}', {
        params: { path: { router_id: r.router_id } },
        body: {
          mgmt_ip: draft.ip.trim(),
          mode: draft.mode,
          reuse_horus_snmp_consent: draft.mode === 'horus_snmp_v3' ? draft.consent : false,
        },
      }),
    )
    toast.add({
      title: t('librenms.routerSaved', { name: r.router_name }),
      color: 'success',
      icon: 'i-lucide-check',
    })
    await refresh()
  } catch (e) {
    if (e instanceof ApiError && e.problem.errors?.length) {
      errors.value = { [r.router_id]: e.problem.errors.map((x) => x.message).join(' ') }
    } else toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  } finally {
    saving.value = null
  }
}

async function clear(r: LibreNmsRouter) {
  try {
    await unwrap(
      $api.DELETE('/librenms/routers/{router_id}', {
        params: { path: { router_id: r.router_id } },
      }),
    )
    toast.add({ title: t('librenms.routerCleared', { name: r.router_name }), color: 'success' })
    await refresh()
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  }
}

// --- Borrar el dispositivo de LibreNMS (solo creado por Horus) ------------------------------
const deleting = ref<LibreNmsRouter | null>(null)
const deleteOpen = computed({
  get: () => !!deleting.value,
  set: (v) => {
    if (!v) deleting.value = null
  },
})
async function deleteDevice() {
  const r = deleting.value
  if (!r || !props.channelId) return
  try {
    await unwrap(
      $api.DELETE('/notification-channels/{channel_id}/librenms/devices/{router_id}', {
        params: { path: { channel_id: props.channelId, router_id: r.router_id } },
      }),
    )
    toast.add({ title: t('librenms.deviceDeleted', { name: r.router_name }), color: 'success' })
    emit('deviceDeleted')
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  }
  deleting.value = null
}

// --- Credenciales SNMP propias (write-only) -----------------------------------------------
const credsFor = ref<LibreNmsRouter | null>(null)
const credsOpen = computed({
  get: () => !!credsFor.value,
  set: (v) => {
    if (!v) credsFor.value = null
  },
})
const creds = reactive({
  community: '',
  level: 'authPriv' as NonNullable<LibreNmsSnmpCredentials['v3_auth_level']>,
  username: '',
  authProtocol: 'SHA-256' as NonNullable<LibreNmsSnmpCredentials['v3_auth_protocol']>,
  authPassword: '',
  privProtocol: 'AES' as NonNullable<LibreNmsSnmpCredentials['v3_priv_protocol']>,
  privPassword: '',
})
const credsErrors = ref<Record<string, string>>({})
function openCreds(r: LibreNmsRouter) {
  Object.assign(creds, {
    community: '',
    username: '',
    authPassword: '',
    privPassword: '',
  })
  credsErrors.value = {}
  credsFor.value = r
}
async function saveCreds() {
  const r = credsFor.value
  if (!r) return
  const body: LibreNmsSnmpCredentials =
    r.mode === 'snmp_v2c'
      ? { community: creds.community }
      : {
          v3_auth_level: creds.level,
          v3_username: creds.username,
          ...(creds.level !== 'noAuthNoPriv'
            ? { v3_auth_protocol: creds.authProtocol, v3_auth_password: creds.authPassword }
            : {}),
          ...(creds.level === 'authPriv'
            ? { v3_priv_protocol: creds.privProtocol, v3_priv_password: creds.privPassword }
            : {}),
        }
  try {
    await withReauth(
      () =>
        unwrap(
          $api.PUT('/librenms/routers/{router_id}/snmp-credentials', {
            params: { path: { router_id: r.router_id } },
            body,
          }),
        ),
      t('librenms.reauth'),
    )
    credsFor.value = null
    toast.add({ title: t('librenms.credentialsSaved'), color: 'success', icon: 'i-lucide-check' })
    await refresh()
  } catch (e) {
    if (e instanceof ApiError && e.problem.errors?.length) {
      credsErrors.value = Object.fromEntries(e.problem.errors.map((x) => [x.field, x.message]))
    } else credsErrors.value = { _: t(describeError(e).messageKey) }
  }
}
const levelItems = ['noAuthNoPriv', 'authNoPriv', 'authPriv'].map((v) => ({ label: v, value: v }))
const authItems = ['MD5', 'SHA', 'SHA-224', 'SHA-256', 'SHA-384', 'SHA-512'].map((v) => ({
  label: v,
  value: v,
}))
const privItems = ['AES', 'DES'].map((v) => ({ label: v, value: v }))
</script>

<template>
  <UCard :ui="{ body: 'p-4 sm:p-5 flex flex-col gap-3' }" data-testid="librenms-routers">
    <h2 class="text-highlighted font-semibold">{{ t('librenms.routers') }}</h2>
    <p class="text-muted text-sm">{{ t('librenms.routersHint') }}</p>
    <LoadingState v-if="status === 'pending' && !routers" :rows="2" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <EmptyState
      v-else-if="!routers?.length"
      icon="i-lucide-router"
      :title="t('librenms.noRouters')"
    />
    <ul v-else class="flex flex-col gap-3">
      <li
        v-for="r in routers"
        :key="r.router_id"
        class="border-default flex flex-col gap-2 rounded-md border p-3"
        :data-router="r.router_name"
      >
        <div class="flex flex-wrap items-center gap-2">
          <span class="text-highlighted font-medium">{{ r.router_name }}</span>
          <UBadge
            v-if="r.mgmt_ip"
            color="success"
            variant="subtle"
            :label="t('librenms.monitored')"
          />
          <UBadge v-else color="neutral" variant="outline" :label="t('librenms.notMonitored')" />
          <UBadge
            v-if="devices.get(r.router_id)"
            color="neutral"
            variant="outline"
            :label="`LibreNMS #${devices.get(r.router_id)!.remote_id}`"
          />
        </div>
        <div
          v-if="drafts[r.router_id]"
          class="grid gap-2 md:grid-cols-[minmax(0,14rem)_minmax(0,16rem)_1fr] md:items-end"
        >
          <UFormField :label="t('librenms.mgmtIp')">
            <UInput
              v-model="drafts[r.router_id]!.ip"
              class="w-full font-mono"
              placeholder="192.0.2.10"
              :disabled="!manage"
              data-testid="router-mgmt-ip"
            />
          </UFormField>
          <UFormField :label="t('librenms.modeLabel')">
            <USelect
              v-model="drafts[r.router_id]!.mode"
              :items="modeItems"
              class="w-full"
              :disabled="!manage"
              data-testid="router-mode"
            />
          </UFormField>
          <div v-if="manage" class="flex flex-wrap gap-2">
            <UButton
              size="sm"
              icon="i-lucide-save"
              :label="t('librenms.saveRouter')"
              :loading="saving === r.router_id"
              data-testid="router-save"
              @click="save(r)"
            />
            <UButton
              v-if="r.mgmt_ip && (r.mode === 'snmp_v2c' || r.mode === 'snmp_v3')"
              size="sm"
              color="neutral"
              variant="outline"
              icon="i-lucide-key-round"
              :label="
                r.has_snmp_credentials
                  ? t('librenms.replaceCredentials')
                  : t('librenms.setCredentials')
              "
              data-testid="router-credentials"
              @click="openCreds(r)"
            />
            <UButton
              v-if="r.mgmt_ip"
              size="sm"
              color="neutral"
              variant="ghost"
              icon="i-lucide-unplug"
              :label="t('librenms.clearRouter')"
              data-testid="router-clear"
              @click="clear(r)"
            />
            <UButton
              v-if="devices.get(r.router_id)?.created_by_horus"
              size="sm"
              color="error"
              variant="ghost"
              icon="i-lucide-trash-2"
              :label="t('librenms.deleteDevice')"
              data-testid="router-delete-device"
              @click="deleting = r"
            />
          </div>
        </div>
        <UCheckbox
          v-if="manage && drafts[r.router_id]?.mode === 'horus_snmp_v3'"
          v-model="drafts[r.router_id]!.consent"
          :label="t('librenms.consent')"
          data-testid="router-consent"
        />
        <p v-if="r.mode === 'snmp_v2c' || r.mode === 'snmp_v3'" class="text-muted text-xs">
          {{
            r.has_snmp_credentials ? t('librenms.credentialsSet') : t('librenms.credentialsMissing')
          }}
        </p>
        <p v-if="errors[r.router_id]" class="text-error text-sm">{{ errors[r.router_id] }}</p>
      </li>
    </ul>

    <UModal
      v-model:open="credsOpen"
      :title="t('librenms.credentialsTitle', { name: credsFor?.router_name ?? '' })"
      :description="t('librenms.credentialsBody')"
    >
      <template #body>
        <form class="flex flex-col gap-3" @submit.prevent="saveCreds">
          <UFormField
            v-if="credsFor?.mode === 'snmp_v2c'"
            :label="t('librenms.community')"
            :error="credsErrors.community"
          >
            <UInput
              v-model="creds.community"
              type="password"
              autocomplete="off"
              class="w-full"
              data-testid="snmp-community"
            />
          </UFormField>
          <template v-else>
            <UFormField :label="t('librenms.v3Level')" :error="credsErrors.v3_auth_level">
              <USelect v-model="creds.level" :items="levelItems" class="w-full" />
            </UFormField>
            <UFormField :label="t('librenms.v3User')" :error="credsErrors.v3_username">
              <UInput
                v-model="creds.username"
                class="w-full"
                autocomplete="off"
                data-testid="snmp-v3-user"
              />
            </UFormField>
            <template v-if="creds.level !== 'noAuthNoPriv'">
              <UFormField :label="t('librenms.v3AuthProtocol')">
                <USelect v-model="creds.authProtocol" :items="authItems" class="w-full" />
              </UFormField>
              <UFormField
                :label="t('librenms.v3AuthPassword')"
                :error="credsErrors.v3_auth_password"
              >
                <UInput
                  v-model="creds.authPassword"
                  type="password"
                  autocomplete="off"
                  class="w-full"
                />
              </UFormField>
            </template>
            <template v-if="creds.level === 'authPriv'">
              <UFormField :label="t('librenms.v3PrivProtocol')">
                <USelect v-model="creds.privProtocol" :items="privItems" class="w-full" />
              </UFormField>
              <UFormField
                :label="t('librenms.v3PrivPassword')"
                :error="credsErrors.v3_priv_password"
              >
                <UInput
                  v-model="creds.privPassword"
                  type="password"
                  autocomplete="off"
                  class="w-full"
                />
              </UFormField>
            </template>
          </template>
          <p v-if="credsErrors._ || credsErrors.mode" class="text-error text-sm">
            {{ credsErrors._ ?? credsErrors.mode }}
          </p>
          <div class="flex justify-end gap-2">
            <UButton
              color="neutral"
              variant="ghost"
              :label="t('common.cancel')"
              @click="credsFor = null"
            />
            <UButton type="submit" :label="t('librenms.saveCredentials')" data-testid="snmp-save" />
          </div>
        </form>
      </template>
    </UModal>

    <UModal
      v-model:open="deleteOpen"
      :title="t('librenms.deleteTitle', { name: deleting?.router_name ?? '' })"
      :description="t('librenms.deleteBody')"
    >
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton
            color="neutral"
            variant="ghost"
            :label="t('common.cancel')"
            @click="deleting = null"
          />
          <UButton
            color="error"
            :label="t('librenms.deleteConfirm')"
            data-testid="router-delete-confirm"
            @click="deleteDevice"
          />
        </div>
      </template>
    </UModal>
  </UCard>
</template>
