<script setup lang="ts">
import type { RemoteDestination } from '~~/types/api'

/**
 * Almacenamiento (I1-31, frontend.md §3.4, ADR-0019): uso del disco local y destinos remotos
 * SFTP de las copias. Sin destino remoto, aviso permanente "Sin copia remota configurada" con el
 * acceso al alta guiada. Con `platform.storage.manage` (re-autenticación reciente en las
 * escrituras): añadir (datos → credenciales → probar y confirmar la huella → activar), editar,
 * cambiar credenciales, probar, copiar ahora, activar/desactivar y eliminar.
 */
const { t } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
const { withReauth } = useReauth()
useSectionGuard()(findSection('platform', '/platform/storage'))
const platformCan = usePlatformCan()
const canManage = computed(() => platformCan('platform.storage.manage'))

const {
  data: destinations,
  error,
  status,
  refresh,
} = useTenantQuery('platform:destinations', () =>
  unwrap($api.GET('/platform/remote-destinations')).then((r) => r.data),
)
const { data: system } = useTenantQuery('platform:system', () => unwrap($api.GET('/system/status')))
const local = computed(() => system.value?.components.find((c) => c.name === 'local_storage'))
const ratio = computed(() => system.value?.disk_usage_ratio ?? null)
const empty = computed(() => !!destinations.value && !destinations.value.length)

const wizardOpen = ref(false)

/**
 * Copias lanzadas desde aquí ("Copiar ahora"): se recarga la lista cada 1,5 s hasta que cambie
 * la última copia correcta (o pase un minuto) y se avisa al terminar.
 */
const copying = ref<Record<string, { since: string | null; started: number }>>({})
let copyTimer: ReturnType<typeof setInterval> | undefined
function stopCopyTimer() {
  if (copyTimer) clearInterval(copyTimer)
  copyTimer = undefined
}
function followCopy(d: RemoteDestination) {
  copying.value = {
    ...copying.value,
    [d.id]: { since: d.last_success_at ?? null, started: Date.now() },
  }
  void refresh()
  copyTimer ??= setInterval(() => void refresh(), 1500)
}
watch(destinations, (list) => {
  const next = Object.entries(copying.value).filter(([id, w]) => {
    const d = list?.find((x) => x.id === id)
    if (d?.last_success_at && d.last_success_at !== w.since) {
      toast.add({
        title: t('platform.storage.sync.done', { name: d.name }),
        icon: 'i-lucide-cloud-check',
        color: 'success',
      })
      return false
    }
    return !!d && Date.now() - w.started <= 60_000
  })
  copying.value = Object.fromEntries(next)
  if (!next.length) stopCopyTimer()
})
onBeforeUnmount(stopCopyTimer)

// Los modales reciben el destino vigente de la lista (tras cada recarga, su versión nueva).
type Dialog = 'edit' | 'credentials' | 'test' | 'delete'
const dialog = ref<Dialog | null>(null)
const selectedId = ref<string | null>(null)
const selected = computed(() => destinations.value?.find((d) => d.id === selectedId.value) ?? null)
function openDialog(kind: Dialog, d: RemoteDestination) {
  selectedId.value = d.id
  deleteError.value = null
  dialog.value = kind
}
function dialogModel(kind: Dialog) {
  return computed({
    get: () => dialog.value === kind && !!selected.value,
    set: (v: boolean) => {
      if (!v && dialog.value === kind) dialog.value = null
    },
  })
}
const editOpen = dialogModel('edit')
const credentialsOpen = dialogModel('credentials')
const testOpen = dialogModel('test')
const deleteOpen = dialogModel('delete')

function edited(d: RemoteDestination, serverMoved: boolean) {
  void refresh()
  // Otro servidor: hay que volver a probar y confirmar su huella.
  if (serverMoved) openDialog('test', d)
}

async function credentialsSaved(d: RemoteDestination) {
  await refresh()
  // Sin huella confirmada todavía: el siguiente paso es probar la conexión.
  if (!d.host_key_fingerprint) openDialog('test', d)
}

async function enableFromTest(d: RemoteDestination) {
  dialog.value = null
  try {
    await withReauth(
      () =>
        unwrap(
          $api.PATCH('/platform/remote-destinations/{destination_id}', {
            params: {
              path: { destination_id: d.id },
              header: { 'If-Match': ifMatch(d.version) },
            },
            body: { enabled: true },
          }),
        ),
      t('platform.storage.reauth'),
    )
    toast.add({
      title: t('platform.storage.enabledToast', { name: d.name }),
      icon: 'i-lucide-power',
      color: 'success',
    })
  } catch (e) {
    const title = isReauthRequired(e)
      ? t('platform.storage.reauthCancelled')
      : e instanceof ApiError && e.status === 412
        ? t('platform.storage.conflictRetry')
        : e instanceof ApiError && e.code === 'REMOTE_DESTINATION_NOT_READY'
          ? t('platform.storage.notReady')
          : t(describeError(e).messageKey)
    toast.add({ title, color: 'warning' })
  } finally {
    void refresh()
  }
}

const deleting = ref(false)
const deleteError = ref<string | null>(null)
async function remove() {
  const d = selected.value
  if (!d) return
  deleting.value = true
  deleteError.value = null
  try {
    await withReauth(
      () =>
        unwrap(
          $api.DELETE('/platform/remote-destinations/{destination_id}', {
            params: {
              path: { destination_id: d.id },
              header: { 'If-Match': ifMatch(d.version) },
            },
          }),
        ),
      t('platform.storage.reauth'),
    )
    dialog.value = null
    toast.add({
      title: t('platform.storage.delete.done', { name: d.name }),
      description: t('platform.storage.delete.doneHint'),
      icon: 'i-lucide-trash-2',
      color: 'success',
    })
    await refresh()
  } catch (e) {
    if (isReauthRequired(e)) deleteError.value = t('platform.storage.reauthCancelled')
    else if (e instanceof ApiError && e.status === 412) {
      deleteError.value = t('platform.storage.delete.conflict')
      await refresh()
    } else if (e instanceof ApiError && e.status === 404) {
      dialog.value = null
      await refresh()
    } else deleteError.value = t(describeError(e).messageKey)
  } finally {
    deleting.value = false
  }
}
</script>

<template>
  <AppPage :title="t('nav.items.platformStorage')" panel-id="platform-storage">
    <DiskUsageNotice :ratio="ratio" />
    <NoRemoteCopyNotice
      v-if="empty"
      :action="
        canManage
          ? { label: t('platform.storage.add'), onClick: () => (wizardOpen = true) }
          : undefined
      "
    />
    <ErrorState v-if="error" :error="error" @retry="refresh()" />

    <UCard data-testid="local-storage">
      <h2 class="text-highlighted text-base font-semibold">{{ t('platform.storage.local') }}</h2>
      <p v-if="ratio !== null" class="text-highlighted mt-2 text-2xl font-semibold tabular">
        {{ formatPercent(ratio) }}
      </p>
      <UProgress
        v-if="ratio !== null"
        :model-value="ratio * 100"
        :color="ratio >= 0.85 ? 'error' : 'primary'"
        class="mt-2"
        :aria-label="t('platform.storage.usage')"
      />
      <p class="text-muted mt-2 text-sm">{{ local?.detail ?? t('platform.storage.usage') }}</p>
      <p v-if="ratio !== null && ratio >= 0.85" class="text-error mt-1 text-sm">
        {{ t('platform.storage.high') }}
      </p>
      <p class="text-dimmed mt-3 text-xs">{{ t('platform.storage.byTypePending') }}</p>
    </UCard>

    <UCard data-testid="remote-destinations">
      <div class="flex flex-col gap-4">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div class="min-w-0">
            <h2 class="text-highlighted text-base font-semibold">
              {{ t('platform.storage.remote') }}
            </h2>
            <p class="text-muted mt-1 text-sm">{{ t('platform.storage.remoteIntro') }}</p>
          </div>
          <UButton
            v-if="canManage"
            icon="i-lucide-plus"
            :label="t('platform.storage.add')"
            data-testid="dst-add"
            @click="wizardOpen = true"
          />
        </div>

        <RemoteCopyExplainer />

        <LoadingState v-if="status === 'pending' && !destinations" :rows="2" />
        <p v-else-if="empty" class="text-muted text-sm" data-testid="dst-empty">
          {{ t('platform.storage.remoteNone') }}
        </p>
        <div v-else class="flex flex-col gap-3" data-testid="dst-list">
          <RemoteDestinationCard
            v-for="dst in destinations ?? []"
            :key="dst.id"
            :destination="dst"
            :can-manage="canManage"
            :copying="!!copying[dst.id]"
            @synced="followCopy"
            @edit="openDialog('edit', dst)"
            @credentials="openDialog('credentials', dst)"
            @test="openDialog('test', dst)"
            @remove="openDialog('delete', dst)"
            @changed="refresh()"
          />
        </div>
      </div>
    </UCard>

    <RemoteDestinationWizard v-model:open="wizardOpen" @changed="refresh()" @synced="followCopy" />
    <RemoteDestinationEditModal
      v-if="selected"
      v-model:open="editOpen"
      :destination="selected"
      @saved="edited"
      @changed="refresh()"
    />
    <RemoteDestinationCredentialsModal
      v-if="selected"
      v-model:open="credentialsOpen"
      :destination="selected"
      @saved="credentialsSaved"
    />
    <RemoteDestinationTestModal
      v-if="selected"
      v-model:open="testOpen"
      :destination="selected"
      @changed="refresh()"
      @enable="enableFromTest"
    />
    <UModal
      v-model:open="deleteOpen"
      :title="t('platform.storage.delete.title', { name: selected?.name ?? '' })"
      :description="t('platform.storage.delete.description')"
      data-testid="dst-delete-modal"
    >
      <template #body>
        <div class="flex flex-col gap-3">
          <ul class="text-default flex list-disc flex-col gap-1.5 ps-5 text-sm">
            <li>{{ t('platform.storage.delete.remoteKept') }}</li>
            <li>{{ t('platform.storage.delete.localKept') }}</li>
            <li>{{ t('platform.storage.delete.credentialsGone') }}</li>
          </ul>
          <UAlert
            v-if="deleteError"
            color="error"
            variant="subtle"
            icon="i-lucide-circle-alert"
            :title="deleteError"
            data-testid="dst-delete-error"
          />
        </div>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton
            color="neutral"
            variant="outline"
            :label="t('common.cancel')"
            autofocus
            @click="dialog = null"
          />
          <UButton
            color="error"
            icon="i-lucide-trash-2"
            :loading="deleting"
            :label="t('platform.storage.delete.submit')"
            data-testid="dst-delete-confirm"
            @click="remove"
          />
        </div>
      </template>
    </UModal>
  </AppPage>
</template>
