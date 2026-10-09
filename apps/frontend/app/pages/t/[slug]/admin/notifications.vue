<script setup lang="ts">
import type { ConnectionTestResult, NotificationChannel } from '~~/types/api'

/**
 * Canales de notificación del ISP (D13, D17; I1 canal mínimo de alertas): email, Telegram y
 * LibreNMS por su API, cada uno con su estado, a qué eventos se suscribe y sus credenciales
 * write-only. "Probar conexión" comprueba el destino sin enviar nada; "Enviar prueba" manda
 * un mensaje de prueba. Todo aislado por ISP aunque compartan servidor LibreNMS.
 */
const { t } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const can = useCan()
const toast = useToast()
const slug = computed(() => String(route.params.slug))
useSectionGuard()(findSection('tenant', 'admin/notifications'), slug.value)
const manage = computed(() => can('alerts.manage'))

const {
  data: channels,
  error,
  status,
  refresh,
} = useTenantQuery('channels', () => unwrap($api.GET('/notification-channels')).then((r) => r.data))

const STATUS = {
  ok: { color: 'success', icon: 'i-lucide-circle-check' },
  unverified: { color: 'neutral', icon: 'i-lucide-circle-help' },
  failing: { color: 'error', icon: 'i-lucide-circle-alert' },
  disabled: { color: 'neutral', icon: 'i-lucide-circle-pause' },
} as const
const ICON = {
  email: 'i-lucide-mail',
  telegram: 'i-lucide-send',
  librenms: 'i-lucide-server-cog',
} as const

function destination(c: NotificationChannel) {
  const cfg = c.config as Record<string, unknown>
  if (c.kind === 'email') return ((cfg.recipients as string[]) ?? []).join(', ')
  if (c.kind === 'telegram') return t('channels.chat', { id: String(cfg.chat_id ?? '') })
  return `${cfg.base_url} · ${cfg.username}`
}

const results = ref<Record<string, ConnectionTestResult | undefined>>({})
const testing = ref<string | null>(null)
async function testConnection(c: NotificationChannel) {
  testing.value = c.id
  try {
    results.value[c.id] = await unwrap(
      $api.POST('/notification-channels/{channel_id}/connection-test', {
        params: { path: { channel_id: c.id } },
      }),
    )
    refresh()
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  } finally {
    testing.value = null
  }
}
async function sendTest(c: NotificationChannel) {
  try {
    await unwrap(
      $api.POST('/notification-channels/{channel_id}/test', {
        params: { path: { channel_id: c.id }, header: { 'Idempotency-Key': idempotencyKey() } },
      }),
    )
    toast.add({ title: t('channels.testQueued'), color: 'success', icon: 'i-lucide-send' })
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  }
}

const formOpen = ref(false)
const editing = ref<NotificationChannel | null>(null)
function create() {
  editing.value = null
  formOpen.value = true
}
function edit(c: NotificationChannel) {
  editing.value = c
  formOpen.value = true
}

const deleting = ref<NotificationChannel | null>(null)
const deleteOpen = computed({
  get: () => !!deleting.value,
  set: (v) => {
    if (!v) deleting.value = null
  },
})
async function remove() {
  const c = deleting.value
  if (!c) return
  try {
    await unwrap(
      $api.DELETE('/notification-channels/{channel_id}', {
        params: { path: { channel_id: c.id }, header: { 'If-Match': ifMatch(c.version) } },
      }),
    )
    toast.add({ title: t('channels.deleted'), color: 'success', icon: 'i-lucide-check' })
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  }
  deleting.value = null
  refresh()
}
</script>

<template>
  <AppPage :title="t('nav.items.notifications')" panel-id="notifications">
    <template #toolbar-left>
      <p class="text-muted text-sm">{{ t('channels.intro') }}</p>
    </template>
    <template v-if="manage" #toolbar-right>
      <UButton
        icon="i-lucide-plus"
        :label="t('channels.add')"
        data-testid="add-channel"
        @click="create"
      />
    </template>

    <LoadingState v-if="status === 'pending' && !channels" :rows="3" />
    <ErrorState v-else-if="error" :error="error" @retry="refresh()" />
    <UCard v-else-if="!channels?.length" class="py-6">
      <EmptyState
        icon="i-lucide-bell-off"
        :title="t('channels.empty')"
        :description="t('channels.emptyHint')"
      />
    </UCard>
    <ul v-else class="flex flex-col gap-3" data-testid="channels">
      <li v-for="c in channels" :key="c.id" :data-channel="c.name">
        <UCard :ui="{ body: 'p-4 sm:p-4 flex flex-col gap-3' }">
          <div class="flex flex-col gap-2 md:flex-row md:items-start md:justify-between">
            <div class="flex min-w-0 flex-col gap-1">
              <p class="flex flex-wrap items-center gap-2">
                <UIcon :name="ICON[c.kind]" class="text-muted size-5 shrink-0" aria-hidden="true" />
                <span class="text-highlighted font-semibold">{{ c.name }}</span>
                <UBadge color="neutral" variant="outline" :label="t(`channels.kind.${c.kind}`)" />
                <UBadge
                  :color="STATUS[c.status].color"
                  variant="subtle"
                  :icon="STATUS[c.status].icon"
                  :label="t(`channels.status.${c.status}`)"
                  data-testid="channel-status"
                />
              </p>
              <p class="text-muted text-sm break-all">{{ destination(c) }}</p>
              <p class="text-muted flex flex-wrap gap-x-3 text-xs">
                <span>{{
                  c.subscription.event_types.map((e) => t(`channels.events.${e}`)).join(' · ')
                }}</span>
                <span v-if="c.subscription.min_severity">{{
                  t('channels.form.fromSeverity', {
                    s: t(`severity.${c.subscription.min_severity}`),
                  })
                }}</span>
              </p>
              <p
                class="flex items-center gap-1.5 text-xs"
                :class="c.has_credentials ? 'text-muted' : 'text-warning'"
                data-testid="channel-credentials"
              >
                <UIcon
                  :name="c.has_credentials ? 'i-lucide-key-round' : 'i-lucide-key'"
                  class="size-3.5"
                  aria-hidden="true"
                />
                {{
                  c.has_credentials
                    ? t('channels.credentialsSet')
                    : t('channels.credentialsMissing')
                }}
              </p>
            </div>
            <div v-if="manage" class="flex shrink-0 flex-wrap gap-2">
              <UButton
                size="sm"
                color="neutral"
                variant="outline"
                icon="i-lucide-plug-zap"
                :loading="testing === c.id"
                :label="t('channels.testConnection')"
                data-testid="test-connection"
                @click="testConnection(c)"
              />
              <UButton
                size="sm"
                color="neutral"
                variant="outline"
                icon="i-lucide-send"
                :label="t('channels.sendTest')"
                @click="sendTest(c)"
              />
              <UButton
                size="sm"
                color="neutral"
                variant="ghost"
                icon="i-lucide-pencil"
                :aria-label="t('channels.edit', { name: c.name })"
                @click="edit(c)"
              />
              <UButton
                size="sm"
                color="neutral"
                variant="ghost"
                icon="i-lucide-trash-2"
                :aria-label="t('channels.delete', { name: c.name })"
                @click="deleting = c"
              />
            </div>
          </div>
          <UAlert
            v-if="results[c.id]"
            :color="results[c.id]!.ok ? 'success' : 'error'"
            variant="subtle"
            :icon="results[c.id]!.ok ? 'i-lucide-circle-check' : 'i-lucide-circle-x'"
            :title="
              results[c.id]!.ok
                ? t('channels.testOk', { ms: results[c.id]!.latency_ms ?? 0 })
                : t(`channels.testError.${results[c.id]!.error_code ?? 'unexpected_response'}`)
            "
            :description="
              results[c.id]!.ok
                ? results[c.id]!.remote_version
                  ? t('channels.remoteVersion', { v: results[c.id]!.remote_version })
                  : undefined
                : (results[c.id]!.error ?? undefined)
            "
            data-testid="test-result"
          />
          <p v-else-if="c.last_error" class="text-error text-sm">{{ c.last_error }}</p>
          <p class="text-dimmed text-xs">
            {{ t('channels.lastDelivery') }} <RelativeTime :at="c.last_delivery_at" />
            <template v-if="c.include_personal_data"> · {{ t('channels.personalOn') }}</template>
          </p>
        </UCard>
      </li>
    </ul>

    <ChannelForm v-model:open="formOpen" :channel="editing" @saved="refresh()" />

    <UModal
      v-model:open="deleteOpen"
      :title="t('channels.deleteTitle', { name: deleting?.name ?? '' })"
      :description="t('channels.deleteBody')"
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
            :label="t('channels.deleteSubmit')"
            @click="remove"
          />
        </div>
      </template>
    </UModal>
  </AppPage>
</template>
