<script setup lang="ts">
import type { RemoteDestination } from '~~/types/api'

/**
 * Tarjeta de un destino remoto: `sftp://usuario@host:puerto/ruta`, estado con su explicación,
 * última copia correcta, retraso, pendiente, último error, retención, huella del servidor y
 * el interruptor de copias. Con `platform.storage.manage`: copiar ahora (`POST .../sync` con
 * `Idempotency-Key`; `REMOTE_DESTINATION_NOT_READY` y `REMOTE_DESTINATION_SYNC_RUNNING`
 * explicados), activar/desactivar (`PATCH enabled` con `If-Match`), y abrir editar,
 * credenciales, probar y eliminar (los modales y el seguimiento de la copia viven en la página).
 */
const props = defineProps<{
  destination: RemoteDestination
  canManage: boolean
  /** Hay una copia lanzada desde la página que aún no ha llegado. */
  copying?: boolean
}>()
const emit = defineEmits<{
  synced: [destination: RemoteDestination]
  edit: []
  credentials: []
  test: []
  remove: []
  changed: []
}>()

const { t } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
const { withReauth } = useReauth()

const d = computed(() => props.destination)
const lag = computed(() => lagParts(d.value.lag_seconds))
const ready = computed(() => canEnable(d.value))
const toggling = ref(false)
const syncing = ref(false)

const missing = computed(() => {
  const out: string[] = []
  if (!d.value.has_credentials) out.push(t('platform.storage.missing.credentials'))
  if (!d.value.host_key_fingerprint) out.push(t('platform.storage.missing.fingerprint'))
  return out
})

const pending = computed(() => {
  const files = d.value.files_pending
  const bytes = d.value.bytes_pending
  if (!files && !bytes) return t('platform.storage.nothingPending')
  return t('platform.storage.pendingValue', {
    files: formatNumber(files ?? 0),
    bytes: joinUnit(formatBytes(bytes ?? 0)),
  })
})

async function syncNow() {
  syncing.value = true
  try {
    const queued = await unwrap(
      $api.POST('/platform/remote-destinations/{destination_id}/sync', {
        params: {
          path: { destination_id: d.value.id },
          header: { 'Idempotency-Key': idempotencyKey() },
        },
      }),
    )
    toast.add({
      title: t('platform.storage.sync.queued', { name: d.value.name }),
      description: t('platform.storage.sync.queuedHint'),
      icon: 'i-lucide-upload-cloud',
      color: 'success',
    })
    emit('synced', queued)
  } catch (e) {
    if (e instanceof ApiError && e.code === 'REMOTE_DESTINATION_SYNC_RUNNING') {
      toast.add({
        title: t('platform.storage.sync.running'),
        icon: 'i-lucide-loader',
        color: 'info',
      })
      emit('changed')
    } else if (e instanceof ApiError && e.code === 'REMOTE_DESTINATION_NOT_READY') {
      toast.add({
        title: t('platform.storage.sync.notReadyTitle'),
        description: t('platform.storage.sync.notReadyBody'),
        icon: 'i-lucide-circle-pause',
        color: 'warning',
      })
      emit('changed')
    } else if (e instanceof ApiError && e.status === 404) {
      emit('changed')
    } else {
      toast.add({ title: t(describeError(e).messageKey), color: 'error' })
    }
  } finally {
    syncing.value = false
  }
}

async function setEnabled(enabled: boolean) {
  toggling.value = true
  try {
    await withReauth(
      () =>
        unwrap(
          $api.PATCH('/platform/remote-destinations/{destination_id}', {
            params: {
              path: { destination_id: d.value.id },
              header: { 'If-Match': ifMatch(d.value.version) },
            },
            body: { enabled },
          }),
        ),
      t('platform.storage.reauth'),
    )
    toast.add({
      title: enabled
        ? t('platform.storage.enabledToast', { name: d.value.name })
        : t('platform.storage.disabledToast', { name: d.value.name }),
      icon: enabled ? 'i-lucide-power' : 'i-lucide-power-off',
      color: enabled ? 'success' : 'neutral',
    })
  } catch (e) {
    if (isReauthRequired(e)) {
      toast.add({ title: t('platform.storage.reauthCancelled'), color: 'warning' })
    } else if (e instanceof ApiError && e.status === 412) {
      toast.add({
        title: t('platform.storage.conflictTitle'),
        description: t('platform.storage.conflictRetry'),
        icon: 'i-lucide-git-compare',
        color: 'warning',
      })
    } else if (e instanceof ApiError && e.code === 'REMOTE_DESTINATION_NOT_READY') {
      toast.add({ title: t('platform.storage.notReady'), color: 'warning' })
    } else {
      toast.add({ title: t(describeError(e).messageKey), color: 'error' })
    }
  } finally {
    toggling.value = false
    emit('changed')
  }
}
</script>

<template>
  <article
    class="border-default flex flex-col gap-4 rounded-md border p-3 sm:p-4"
    :aria-labelledby="`dst-${d.id}-name`"
    data-testid="dst-card"
    :data-name="d.name"
  >
    <header class="flex flex-wrap items-start justify-between gap-2">
      <div class="flex min-w-0 flex-col gap-1">
        <div class="flex min-w-0 flex-wrap items-center gap-2">
          <h3 :id="`dst-${d.id}-name`" class="text-highlighted text-base font-semibold break-words">
            {{ d.name }}
          </h3>
          <RemoteDestinationStatusBadge :status="d.status" />
          <UBadge
            v-if="copying"
            color="info"
            variant="outline"
            icon="i-lucide-loader"
            :label="t('platform.storage.sync.inProgress')"
            data-testid="dst-syncing"
          />
        </div>
        <p class="flex min-w-0 flex-wrap items-center gap-1">
          <span class="text-default font-mono text-sm break-all" data-testid="dst-url">{{
            destinationUrl(d)
          }}</span>
          <CopyButton :value="destinationUrl(d)" size="xs" variant="ghost" />
        </p>
      </div>
      <UButton
        v-if="canManage"
        size="sm"
        icon="i-lucide-upload-cloud"
        :color="d.enabled ? 'primary' : 'neutral'"
        :variant="d.enabled ? 'solid' : 'outline'"
        :loading="syncing"
        :label="t('platform.storage.sync.now')"
        data-testid="dst-sync"
        @click="syncNow"
      />
    </header>

    <p class="text-muted text-sm" data-testid="dst-status-explain">
      {{ t(`platform.storage.statusExplain.${d.status}`) }}
    </p>

    <UAlert
      v-if="d.last_error"
      color="error"
      variant="subtle"
      icon="i-lucide-circle-alert"
      :title="t('platform.storage.lastError')"
      :description="d.last_error"
      data-testid="dst-last-error"
    />

    <UAlert
      v-if="missing.length"
      color="warning"
      variant="subtle"
      icon="i-lucide-list-checks"
      :title="t('platform.storage.missing.title')"
      :description="t('platform.storage.missing.body', { items: missing.join(' · ') })"
      :actions="
        canManage
          ? [
              d.has_credentials
                ? {
                    label: t('platform.storage.actions.test'),
                    icon: 'i-lucide-plug-zap',
                    color: 'warning',
                    variant: 'solid',
                    onClick: () => emit('test'),
                  }
                : {
                    label: t('platform.storage.actions.credentials'),
                    icon: 'i-lucide-key-round',
                    color: 'warning',
                    variant: 'solid',
                    onClick: () => emit('credentials'),
                  },
            ]
          : []
      "
      data-testid="dst-missing"
    />

    <dl class="grid grid-cols-2 gap-x-4 gap-y-3 lg:grid-cols-4">
      <div class="min-w-0">
        <dt class="text-muted text-xs">{{ t('platform.storage.lastSuccess') }}</dt>
        <dd class="text-highlighted text-sm" data-testid="dst-last-success">
          <RelativeTime v-if="d.last_success_at" :at="d.last_success_at" />
          <span v-else class="text-muted">{{ t('platform.storage.neverCopied') }}</span>
        </dd>
      </div>
      <div class="min-w-0">
        <dt class="text-muted text-xs">{{ t('platform.storage.lag.label') }}</dt>
        <dd class="text-highlighted text-sm tabular" data-testid="dst-lag">
          {{ lag ? t(lag.key, { n: lag.n }) : '—' }}
        </dd>
      </div>
      <div class="min-w-0">
        <dt class="text-muted text-xs">{{ t('platform.storage.pending') }}</dt>
        <dd class="text-highlighted text-sm tabular" data-testid="dst-pending">{{ pending }}</dd>
      </div>
      <div class="min-w-0">
        <dt class="text-muted text-xs">{{ t('platform.storage.retention') }}</dt>
        <dd class="text-highlighted text-sm tabular" data-testid="dst-retention">
          {{ t('platform.storage.retentionValue', { n: d.retention_days ?? 30 }) }}
        </dd>
      </div>
      <div class="col-span-2 min-w-0 lg:col-span-4">
        <dt class="text-muted text-xs">{{ t('platform.storage.fingerprint') }}</dt>
        <dd class="flex flex-wrap items-center gap-1" data-testid="dst-fingerprint-value">
          <template v-if="d.host_key_fingerprint">
            <span
              class="text-highlighted font-mono text-sm break-all"
              data-testid="dst-fingerprint-text"
              >{{ d.host_key_fingerprint }}</span
            >
            <CopyButton :value="d.host_key_fingerprint" size="xs" variant="ghost" />
          </template>
          <span v-else class="text-muted text-sm">{{ t('platform.storage.noFingerprint') }}</span>
        </dd>
      </div>
    </dl>

    <footer
      v-if="canManage"
      class="border-default flex flex-col gap-3 border-t pt-3 md:flex-row md:items-center md:justify-between"
    >
      <USwitch
        :model-value="d.enabled"
        :disabled="toggling || (!d.enabled && !ready)"
        :loading="toggling"
        :label="t('platform.storage.enabledLabel')"
        :description="
          !d.enabled && !ready
            ? t('platform.storage.enableBlocked')
            : d.enabled
              ? t('platform.storage.enabledHelp')
              : t('platform.storage.disabledHelp')
        "
        data-testid="dst-enabled"
        @update:model-value="(v: boolean) => setEnabled(v)"
      />
      <div class="flex flex-wrap gap-1">
        <UButton
          size="sm"
          color="neutral"
          variant="ghost"
          icon="i-lucide-plug-zap"
          :label="t('platform.storage.actions.test')"
          data-testid="dst-test-open"
          @click="emit('test')"
        />
        <UButton
          size="sm"
          color="neutral"
          variant="ghost"
          icon="i-lucide-pencil"
          :label="t('platform.storage.actions.edit')"
          data-testid="dst-edit-open"
          @click="emit('edit')"
        />
        <UButton
          size="sm"
          color="neutral"
          variant="ghost"
          icon="i-lucide-key-round"
          :label="t('platform.storage.actions.credentials')"
          data-testid="dst-credentials-open"
          @click="emit('credentials')"
        />
        <UButton
          size="sm"
          color="error"
          variant="ghost"
          icon="i-lucide-trash-2"
          :label="t('platform.storage.actions.delete')"
          data-testid="dst-delete-open"
          @click="emit('remove')"
        />
      </div>
    </footer>
  </article>
</template>
