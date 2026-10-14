<script setup lang="ts">
import type {
  ConnectionTestResult,
  LibreNmsObject,
  LibreNmsSyncResult,
  LibreNmsSyncStep,
  LibreNmsTestResult,
  NotificationChannel,
} from '~~/types/api'

/**
 * Integración con LibreNMS (D17): Horus aprovisiona LibreNMS y le delega el alertado.
 * Asistente en cuatro pasos (canal con token de API, preparar LibreNMS —syslog y transportes
 * por defecto—, sincronizar y probar de extremo a extremo), la lista de lo que Horus creó en
 * LibreNMS con sus ids y la IP de gestión + modo de monitoreo de cada router
 * (`LibreNmsRouters`). Independiente del formulario de canales.
 */
const { t, d } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const { withReauth } = useReauth()
const can = useCan()
const toast = useToast()
const slug = computed(() => String(route.params.slug))
useSectionGuard()(findSection('tenant', 'admin/librenms'), slug.value)
const manage = computed(() => can('alerts.manage'))

const SYSLOG_COMMAND = 'lnms config:set enable_syslog true'

const {
  data: channels,
  error,
  status,
  refresh: refreshChannels,
} = useTenantQuery('channels', () => unwrap($api.GET('/notification-channels')).then((r) => r.data))
const libreChannels = computed(() => (channels.value ?? []).filter((c) => c.kind === 'librenms'))
const selectedId = ref('')
watch(
  libreChannels,
  (list) => {
    if (!list.some((c) => c.id === selectedId.value)) selectedId.value = list[0]?.id ?? ''
  },
  { immediate: true },
)
const selected = computed<NotificationChannel | null>(
  () => libreChannels.value.find((c) => c.id === selectedId.value) ?? null,
)
const selectedUrl = computed(() =>
  String((selected.value?.config as { base_url?: string } | undefined)?.base_url ?? ''),
)
const channelItems = computed(() =>
  libreChannels.value.map((c) => ({ label: c.name, value: c.id })),
)

const { data: integration, refresh: refreshIntegration } = useTenantQuery(
  () => `librenms:${selectedId.value}`,
  () =>
    selectedId.value
      ? unwrap(
          $api.GET('/notification-channels/{channel_id}/librenms', {
            params: { path: { channel_id: selectedId.value } },
          }),
        )
      : Promise.resolve(null),
  { watch: [selectedId] },
)

// --- Paso 1: canal ------------------------------------------------------------------------
const creating = ref(false)
const newChannel = reactive({
  name: 'LibreNMS',
  baseUrl: '',
  username: 'horus',
  token: '',
  tlsVerify: true,
})
const newErrors = ref<Record<string, string>>({})
const savingChannel = ref(false)
async function createChannel() {
  newErrors.value = {}
  savingChannel.value = true
  try {
    const c = await unwrap(
      $api.POST('/notification-channels', {
        body: {
          name: newChannel.name.trim(),
          kind: 'librenms',
          config: {
            base_url: newChannel.baseUrl.trim(),
            username: newChannel.username.trim(),
            tls_verify: newChannel.tlsVerify,
          },
          subscription: {
            event_types: ['finding_opened', 'finding_reopened', 'exporter_silent', 'tunnel_down'],
            min_severity: 'medium',
          },
        },
      }),
    )
    if (newChannel.token) {
      await withReauth(
        () =>
          unwrap(
            $api.PUT('/notification-channels/{channel_id}/credentials', {
              params: { path: { channel_id: c.id } },
              body: { api_token: newChannel.token },
            }),
          ),
        t('librenms.reauth'),
      )
    }
    creating.value = false
    newChannel.token = ''
    await refreshChannels()
    selectedId.value = c.id
    toast.add({ title: t('librenms.channelCreated'), color: 'success', icon: 'i-lucide-check' })
  } catch (e) {
    if (e instanceof ApiError && e.problem.errors?.length) {
      newErrors.value = Object.fromEntries(
        e.problem.errors.map((x) => [x.field.replace(/^config\./, ''), x.message]),
      )
    } else newErrors.value = { _: t(describeError(e).messageKey) }
  } finally {
    savingChannel.value = false
  }
}

const connection = ref<ConnectionTestResult | null>(null)
const testingConnection = ref(false)
async function testConnection() {
  if (!selected.value) return
  testingConnection.value = true
  try {
    connection.value = await unwrap(
      $api.POST('/notification-channels/{channel_id}/connection-test', {
        params: { path: { channel_id: selected.value.id } },
      }),
    )
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  } finally {
    testingConnection.value = false
  }
}
watch(selectedId, () => {
  connection.value = null
  syncResult.value = null
})

// --- Paso 3: sincronizar ------------------------------------------------------------------
const syncForm = reactive({ host: '', cpu: 85, memory: 90 })
watch(
  integration,
  (v) => {
    if (!v) return
    syncForm.host = v.horus_hostname ?? ''
    syncForm.cpu = v.cpu_threshold
    syncForm.memory = v.memory_threshold
  },
  { immediate: true },
)
const syncing = ref(false)
const syncResult = ref<LibreNmsSyncResult | null>(null)
const syncErrors = ref<Record<string, string>>({})
async function sync() {
  if (!selected.value) return
  syncing.value = true
  syncErrors.value = {}
  try {
    const body: { horus_hostname?: string; cpu_threshold: number; memory_threshold: number } = {
      cpu_threshold: Number(syncForm.cpu),
      memory_threshold: Number(syncForm.memory),
    }
    if (syncForm.host.trim()) body.horus_hostname = syncForm.host.trim()
    syncResult.value = await unwrap(
      $api.POST('/notification-channels/{channel_id}/librenms/sync', {
        params: { path: { channel_id: selected.value.id } },
        body,
      }),
    )
    integration.value = syncResult.value.integration
    toast.add(
      syncResult.value.ok
        ? { title: t('librenms.synced'), color: 'success', icon: 'i-lucide-check' }
        : { title: t('librenms.syncFailed'), color: 'error', icon: 'i-lucide-circle-alert' },
    )
  } catch (e) {
    if (e instanceof ApiError && e.problem.errors?.length) {
      syncErrors.value = Object.fromEntries(e.problem.errors.map((x) => [x.field, x.message]))
    } else toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  } finally {
    syncing.value = false
  }
}
const provisioned = computed(
  () => !!integration.value && integration.value.status !== 'not_provisioned',
)
const STATUS = {
  not_provisioned: { color: 'neutral', icon: 'i-lucide-circle-dashed' },
  ok: { color: 'success', icon: 'i-lucide-circle-check' },
  partial: { color: 'warning', icon: 'i-lucide-triangle-alert' },
  failed: { color: 'error', icon: 'i-lucide-circle-alert' },
} as const
const ACTION_COLOR: Record<
  LibreNmsSyncStep['action'],
  'success' | 'neutral' | 'warning' | 'error'
> = {
  created: 'success',
  recreated: 'warning',
  adopted: 'neutral',
  updated: 'neutral',
  unchanged: 'neutral',
  removed: 'warning',
  deleted: 'warning',
  failed: 'error',
  skipped: 'neutral',
}
const lastSteps = computed(
  () => syncResult.value?.steps ?? integration.value?.last_sync_steps ?? [],
)

// --- Paso 4: probar -----------------------------------------------------------------------
const testing = ref(false)
const testResult = ref<LibreNmsTestResult | null>(null)
const shownTest = computed(() => testResult.value ?? integration.value?.last_test ?? null)
async function runTest() {
  if (!selected.value) return
  testing.value = true
  try {
    testResult.value = await unwrap(
      $api.POST('/notification-channels/{channel_id}/librenms/test', {
        params: { path: { channel_id: selected.value.id } },
      }),
    )
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  } finally {
    testing.value = false
  }
}

// --- Objetos ------------------------------------------------------------------------------
const KIND_ORDER = ['device', 'group', 'router_device', 'infra_device', 'rule', 'template'] as const
const objects = computed(() =>
  [...(integration.value?.objects ?? [])].sort(
    (a, b) =>
      KIND_ORDER.indexOf(a.kind) - KIND_ORDER.indexOf(b.kind) || a.name.localeCompare(b.name),
  ),
)
const routerDevices = computed(() => {
  const m = new Map<string, LibreNmsObject>()
  for (const o of integration.value?.objects ?? []) if (o.kind === 'router_device') m.set(o.key, o)
  return m
})
</script>

<template>
  <AppPage :title="t('nav.items.librenms')" panel-id="librenms">
    <template #toolbar-left>
      <p class="text-muted text-sm">{{ t('librenms.intro') }}</p>
    </template>

    <LoadingState v-if="status === 'pending' && !channels" :rows="4" />
    <ErrorState v-else-if="error" :error="error" @retry="refreshChannels()" />
    <div v-else class="flex flex-col gap-4" data-testid="librenms">
      <!-- Paso 1 -->
      <UCard :ui="{ body: 'p-4 sm:p-5 flex flex-col gap-3' }" data-step="channel">
        <h2 class="text-highlighted font-semibold">{{ t('librenms.step1') }}</h2>
        <p class="text-muted text-sm">{{ t('librenms.step1Hint') }}</p>
        <div v-if="libreChannels.length" class="flex flex-col gap-3 md:flex-row md:items-end">
          <UFormField :label="t('librenms.channel')" class="md:w-80">
            <USelect
              v-model="selectedId"
              :items="channelItems"
              class="w-full"
              data-testid="librenms-channel"
            />
          </UFormField>
          <UButton
            v-if="manage"
            color="neutral"
            variant="outline"
            icon="i-lucide-plug-zap"
            :label="t('librenms.testConnection')"
            :loading="testingConnection"
            data-testid="librenms-connection-test"
            @click="testConnection"
          />
        </div>
        <p v-if="selected" class="text-muted text-sm break-all">
          {{ selectedUrl }} ·
          {{
            selected.has_credentials ? t('librenms.hasCredentials') : t('librenms.noCredentials')
          }}
        </p>
        <p
          v-if="connection"
          class="flex items-start gap-1.5 text-sm"
          :class="connection.ok ? 'text-success' : 'text-error'"
          data-testid="librenms-connection-result"
        >
          <UIcon
            :name="connection.ok ? 'i-lucide-circle-check' : 'i-lucide-circle-alert'"
            class="mt-0.5 size-4 shrink-0"
            aria-hidden="true"
          />
          {{
            connection.ok
              ? t('librenms.connectionOk', { version: connection.remote_version ?? '?' })
              : (connection.error ?? connection.error_code)
          }}
        </p>
        <template v-if="manage">
          <UButton
            v-if="!creating"
            class="self-start"
            color="neutral"
            variant="ghost"
            icon="i-lucide-plus"
            :label="libreChannels.length ? t('librenms.newChannel') : t('librenms.createChannel')"
            data-testid="librenms-new-channel"
            @click="creating = true"
          />
          <form
            v-else
            class="grid gap-3 md:grid-cols-2"
            data-testid="librenms-channel-form"
            @submit.prevent="createChannel"
          >
            <UFormField :label="t('librenms.channelName')" :error="newErrors.name">
              <UInput v-model="newChannel.name" class="w-full" data-testid="librenms-name" />
            </UFormField>
            <UFormField
              :label="t('librenms.baseUrl')"
              :hint="t('librenms.baseUrlHint')"
              :error="newErrors.base_url"
            >
              <UInput
                v-model="newChannel.baseUrl"
                class="w-full"
                placeholder="https://librenms.tu-isp.example"
                data-testid="librenms-base-url"
              />
            </UFormField>
            <UFormField :label="t('librenms.username')" :error="newErrors.username">
              <UInput v-model="newChannel.username" class="w-full" />
            </UFormField>
            <UFormField
              :label="t('librenms.apiToken')"
              :hint="t('librenms.apiTokenHint')"
              :error="newErrors.api_token"
            >
              <UInput
                v-model="newChannel.token"
                type="password"
                autocomplete="off"
                class="w-full"
                data-testid="librenms-token"
              />
            </UFormField>
            <UCheckbox
              v-model="newChannel.tlsVerify"
              class="md:col-span-2"
              :label="t('librenms.tlsVerify')"
            />
            <p v-if="newErrors._" class="text-error text-sm md:col-span-2">{{ newErrors._ }}</p>
            <div class="flex gap-2 md:col-span-2">
              <UButton
                type="submit"
                :loading="savingChannel"
                :label="t('librenms.saveChannel')"
                data-testid="librenms-save-channel"
              />
              <UButton
                color="neutral"
                variant="ghost"
                :label="t('common.cancel')"
                @click="creating = false"
              />
            </div>
          </form>
        </template>
        <EmptyState
          v-if="!libreChannels.length && !creating"
          icon="i-lucide-server-cog"
          :title="t('librenms.noChannel')"
          :description="t('librenms.noChannelHint')"
        />
      </UCard>

      <!-- Paso 2 -->
      <UCard :ui="{ body: 'p-4 sm:p-5 flex flex-col gap-3' }" data-step="prepare">
        <h2 class="text-highlighted font-semibold">{{ t('librenms.step2') }}</h2>
        <ol class="flex list-decimal flex-col gap-2 ps-5 text-sm">
          <li>{{ t('librenms.prepareToken') }}</li>
          <li class="flex flex-col gap-2">
            <span>{{ t('librenms.prepareSyslog') }}</span>
            <CodeBlock :code="SYSLOG_COMMAND" :label="t('librenms.syslogCommand')" />
          </li>
          <li>{{ t('librenms.prepareTransports') }}</li>
          <li>{{ t('librenms.prepareReach') }}</li>
        </ol>
      </UCard>

      <!-- Paso 3 -->
      <UCard v-if="selected" :ui="{ body: 'p-4 sm:p-5 flex flex-col gap-3' }" data-step="sync">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h2 class="text-highlighted font-semibold">{{ t('librenms.step3') }}</h2>
          <UBadge
            v-if="integration"
            :color="STATUS[integration.status].color"
            variant="subtle"
            :icon="STATUS[integration.status].icon"
            :label="t(`librenms.status.${integration.status}`)"
            data-testid="librenms-status"
          />
        </div>
        <p class="text-muted text-sm">{{ t('librenms.step3Hint') }}</p>
        <form class="grid gap-3 md:grid-cols-3" @submit.prevent="sync">
          <UFormField
            :label="t('librenms.horusHost')"
            :hint="
              integration?.horus_hostname_default
                ? t('librenms.horusHostDefault', { host: integration.horus_hostname_default })
                : undefined
            "
            :error="syncErrors.horus_hostname"
          >
            <UInput
              v-model="syncForm.host"
              class="w-full"
              :placeholder="integration?.horus_hostname_default ?? 'horus.tu-isp.example'"
              :disabled="!manage"
              data-testid="librenms-horus-host"
            />
          </UFormField>
          <UFormField :label="t('librenms.cpu')" :error="syncErrors.cpu_threshold">
            <UInputNumber
              v-model="syncForm.cpu"
              :min="50"
              :max="99"
              class="w-full"
              :disabled="!manage"
            />
          </UFormField>
          <UFormField :label="t('librenms.memory')" :error="syncErrors.memory_threshold">
            <UInputNumber
              v-model="syncForm.memory"
              :min="50"
              :max="99"
              class="w-full"
              :disabled="!manage"
            />
          </UFormField>
          <div v-if="manage" class="md:col-span-3">
            <UButton
              type="submit"
              icon="i-lucide-refresh-cw"
              :loading="syncing"
              :label="provisioned ? t('librenms.sync') : t('librenms.connect')"
              data-testid="librenms-sync"
            />
          </div>
        </form>
        <p v-if="integration?.last_sync_at" class="text-muted text-xs">
          {{ t('librenms.lastSync', { time: d(new Date(integration.last_sync_at), 'long') }) }}
          <template v-if="integration.remote_version">
            · LibreNMS {{ integration.remote_version }}</template
          >
        </p>
        <p
          v-if="integration?.last_error"
          class="text-error flex items-start gap-1.5 text-sm"
          data-testid="librenms-sync-error"
        >
          <UIcon name="i-lucide-circle-alert" class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
          {{ integration.last_error }}
          <span v-if="integration.last_error_code" class="font-mono text-xs"
            >({{ integration.last_error_code }})</span
          >
        </p>
        <ul v-if="integration?.warnings.length" class="flex flex-col gap-1 text-sm">
          <li
            v-for="w in integration.warnings"
            :key="w"
            class="text-warning flex items-start gap-1.5"
          >
            <UIcon
              name="i-lucide-triangle-alert"
              class="mt-0.5 size-4 shrink-0"
              aria-hidden="true"
            />
            {{ w }}
          </li>
        </ul>
        <details v-if="lastSteps.length" class="text-sm" data-testid="librenms-steps">
          <summary class="cursor-pointer">
            {{ t('librenms.stepsSummary', { n: lastSteps.length }) }}
          </summary>
          <ul class="mt-2 flex flex-col gap-1">
            <li
              v-for="s in lastSteps"
              :key="`${s.kind}/${s.key}`"
              class="flex flex-wrap items-center gap-2"
            >
              <UBadge
                :color="ACTION_COLOR[s.action]"
                variant="subtle"
                size="sm"
                :label="t(`librenms.action.${s.action}`)"
              />
              <span>{{ s.name }}</span>
              <span v-if="s.remote_id !== null" class="text-muted font-mono text-xs"
                >#{{ s.remote_id }}</span
              >
              <span v-if="s.error" class="text-error text-xs">{{ s.error }}</span>
            </li>
          </ul>
        </details>
      </UCard>

      <!-- Paso 4 -->
      <UCard v-if="selected" :ui="{ body: 'p-4 sm:p-5 flex flex-col gap-3' }" data-step="test">
        <h2 class="text-highlighted font-semibold">{{ t('librenms.step4') }}</h2>
        <p class="text-muted text-sm">{{ t('librenms.step4Hint') }}</p>
        <UButton
          v-if="manage"
          class="self-start"
          icon="i-lucide-send"
          :disabled="!provisioned"
          :loading="testing"
          :label="t('librenms.test')"
          data-testid="librenms-test"
          @click="runTest"
        />
        <div v-if="shownTest" class="flex flex-col gap-2" data-testid="librenms-test-result">
          <p
            class="flex items-start gap-1.5 text-sm font-medium"
            :class="shownTest.ok ? 'text-success' : 'text-error'"
          >
            <UIcon
              :name="shownTest.ok ? 'i-lucide-circle-check' : 'i-lucide-circle-alert'"
              class="mt-0.5 size-4 shrink-0"
              aria-hidden="true"
            />
            {{
              shownTest.ok
                ? t('librenms.testOk')
                : t('librenms.testFailed', {
                    step: t(`librenms.testStep.${shownTest.failed_step ?? 'auth'}`),
                  })
            }}
          </p>
          <ol class="flex flex-col gap-1 text-sm">
            <li v-for="s in shownTest.steps" :key="s.step" class="flex items-start gap-1.5">
              <UIcon
                :name="s.ok ? 'i-lucide-check' : 'i-lucide-x'"
                :class="s.ok ? 'text-success' : 'text-error'"
                class="mt-0.5 size-4 shrink-0"
                aria-hidden="true"
              />
              <span>
                {{ t(`librenms.testStep.${s.step}`) }}
                <span v-if="!s.ok && s.detail" class="text-error block">{{ s.detail }}</span>
              </span>
            </li>
          </ol>
          <p v-if="shownTest.line" class="text-muted font-mono text-xs break-all">
            {{ shownTest.line }}
          </p>
        </div>
      </UCard>

      <!-- Objetos en LibreNMS -->
      <UCard v-if="objects.length" :ui="{ body: 'p-4 sm:p-5 flex flex-col gap-3' }">
        <h2 class="text-highlighted font-semibold">{{ t('librenms.objects') }}</h2>
        <p class="text-muted text-sm">{{ t('librenms.objectsHint') }}</p>
        <div class="overflow-x-auto">
          <table class="w-full text-left text-sm" data-testid="librenms-objects">
            <thead class="text-muted text-xs">
              <tr>
                <th scope="col" class="py-1 pe-3 font-medium">{{ t('librenms.objectKind') }}</th>
                <th scope="col" class="py-1 pe-3 font-medium">{{ t('librenms.objectName') }}</th>
                <th scope="col" class="py-1 pe-3 font-medium">{{ t('librenms.objectId') }}</th>
                <th scope="col" class="py-1 font-medium">{{ t('librenms.objectOrigin') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="o in objects"
                :key="`${o.kind}/${o.key}`"
                class="border-default border-t"
                :data-object="o.name"
              >
                <td class="py-1.5 pe-3">{{ t(`librenms.kind.${o.kind}`) }}</td>
                <td class="py-1.5 pe-3">
                  {{ o.name }}
                  <span v-if="o.hostname" class="text-muted font-mono text-xs">
                    · {{ o.hostname }}</span
                  >
                </td>
                <td class="py-1.5 pe-3 font-mono">#{{ o.remote_id }}</td>
                <td class="py-1.5">
                  {{ o.created_by_horus ? t('librenms.createdByHorus') : t('librenms.adopted') }}
                  <template v-if="!o.active"> · {{ t('librenms.inactive') }}</template>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </UCard>

      <LibreNmsRouters
        :channel-id="selected?.id ?? null"
        :devices="routerDevices"
        :manage="manage"
        @device-deleted="refreshIntegration()"
      />
    </div>
  </AppPage>
</template>
