<script setup lang="ts">
import type { ConnectionTestResult, NotificationChannel, NotificationDelivery } from '~~/types/api'

/**
 * Canales de notificación del ISP (D13, D17; I1 canal mínimo de alertas): email, Telegram y
 * LibreNMS por su API, cada uno con su estado, a qué eventos se suscribe y sus credenciales
 * write-only. "Probar conexión" comprueba el destino sin enviar nada; "Enviar prueba" manda
 * un mensaje de prueba. Todo aislado por ISP aunque compartan servidor LibreNMS. Debajo, el
 * registro de envíos (GET /notification-deliveries): qué se avisó, por qué canal y si llegó.
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
  webhook: 'i-lucide-webhook',
} as const

function subscriptionSummary(c: NotificationChannel) {
  const cats = (c.subscription.categories ?? []).map((k) => t(`channels.categories.${k}.label`))
  const loose = c.subscription.event_types
    .filter((e) => !(c.subscription.categories ?? []).includes(eventCategory(e)!))
    .map((e) => t(`channels.events.${e}`))
  return [...cats, ...loose].join(' · ')
}

const deliveryStatus = ref<'all' | 'sent' | 'failed' | 'throttled'>('all')
const {
  data: deliveries,
  error: deliveriesError,
  status: deliveriesStatus,
  refresh: refreshDeliveries,
} = useTenantQuery(
  () => `deliveries:${deliveryStatus.value}`,
  () =>
    unwrap(
      $api.GET('/notification-deliveries', {
        params: {
          query: {
            limit: 50,
            ...(deliveryStatus.value === 'all' ? {} : { status: deliveryStatus.value }),
          },
        },
      }),
    ).then((r) => r.data),
  { watch: [deliveryStatus] },
)
const channelName = computed(
  () => new Map((channels.value ?? []).map((c) => [c.id, c.name] as const)),
)
const DELIVERY = {
  sent: { color: 'success', icon: 'i-lucide-check' },
  queued: { color: 'info', icon: 'i-lucide-clock' },
  failed: { color: 'error', icon: 'i-lucide-circle-x' },
  throttled: { color: 'neutral', icon: 'i-lucide-layers' },
} as const
const deliveryFilterItems = computed(() =>
  (['all', 'sent', 'failed', 'throttled'] as const).map((s) => ({
    label: t(`channels.deliveries.filter.${s}`),
    value: s,
  })),
)
function deliveryTitle(d: NotificationDelivery) {
  if (d.is_test) return t('channels.deliveries.test')
  return d.summary ?? (d.event_type ? t(`channels.events.${d.event_type}`) : '—')
}

function destination(c: NotificationChannel) {
  const cfg = c.config as Record<string, unknown>
  if (c.kind === 'email') return ((cfg.recipients as string[]) ?? []).join(', ')
  if (c.kind === 'telegram') return t('channels.chat', { id: String(cfg.chat_id ?? '') })
  if (c.kind === 'webhook') return String(cfg.url ?? '')
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
    setTimeout(() => refreshDeliveries(), 1500)
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
                <span data-testid="channel-subscription">{{ subscriptionSummary(c) }}</span>
                <span v-if="c.subscription.min_severity">{{
                  t('channels.form.fromSeverity', {
                    s: t(`severity.${c.subscription.min_severity}`),
                  })
                }}</span>
                <span v-if="c.subscription.notify_recovery !== false">{{
                  t('channels.recoveryOn')
                }}</span>
                <span v-if="c.subscription.site_ids?.length">{{
                  t('channels.sitesCount', { n: c.subscription.site_ids.length })
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
                  : results[c.id]!.bot_username
                    ? t('channels.telegramOk', {
                        bot: results[c.id]!.bot_username,
                        chat: results[c.id]!.chat_title ?? '—',
                      })
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

    <section v-if="channels?.length" class="mt-6 flex flex-col gap-3" data-testid="deliveries">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <h2 class="text-highlighted text-base font-semibold">
          {{ t('channels.deliveries.title') }}
        </h2>
        <div class="flex items-center gap-2">
          <USelect
            v-model="deliveryStatus"
            :items="deliveryFilterItems"
            class="w-40"
            :aria-label="t('channels.deliveries.filterLabel')"
            data-testid="deliveries-filter"
          />
          <UButton
            color="neutral"
            variant="ghost"
            icon="i-lucide-refresh-cw"
            :aria-label="t('channels.deliveries.refresh')"
            @click="refreshDeliveries()"
          />
        </div>
      </div>
      <LoadingState v-if="deliveriesStatus === 'pending' && !deliveries" :rows="3" />
      <ErrorState
        v-else-if="deliveriesError"
        :error="deliveriesError"
        @retry="refreshDeliveries()"
      />
      <UCard v-else-if="!deliveries?.length" class="py-4">
        <EmptyState icon="i-lucide-inbox" :title="t('channels.deliveries.empty')" />
      </UCard>
      <UCard v-else :ui="{ body: 'p-0 sm:p-0' }">
        <ul class="divide-default divide-y">
          <li
            v-for="d in deliveries"
            :key="d.id"
            class="flex flex-col gap-1 px-4 py-3 sm:flex-row sm:items-center sm:gap-3"
            :data-delivery="d.status"
          >
            <UBadge
              class="self-start sm:self-center"
              :color="DELIVERY[d.status].color"
              variant="subtle"
              :icon="DELIVERY[d.status].icon"
              :label="t(`channels.deliveries.status.${d.status}`)"
            />
            <div class="flex min-w-0 flex-1 flex-col">
              <p class="text-highlighted truncate text-sm">
                <UBadge
                  v-if="d.severity"
                  class="me-1.5"
                  size="sm"
                  color="neutral"
                  variant="outline"
                  :label="t(`severity.${d.severity}`)"
                />{{ deliveryTitle(d) }}
              </p>
              <p class="text-muted truncate text-xs">
                {{ channelName.get(d.channel_id) ?? t(`channels.kind.${d.channel_kind}`) }}
                <template v-if="d.error">
                  · <span class="text-error">{{ d.error }}</span></template
                >
              </p>
            </div>
            <RelativeTime class="text-dimmed shrink-0 text-xs" :at="d.sent_at ?? d.created_at" />
          </li>
        </ul>
      </UCard>
    </section>

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
