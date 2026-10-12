<script setup lang="ts">
import type { RemoteDestination, RemoteDestinationTestResult } from '~~/types/api'
import type { DestinationForm } from '~/utils/remote-destinations'
import { PORT_RANGE, RETENTION_RANGE } from '~/utils/remote-destinations'

/**
 * "Añadir destino SFTP", guiado en cuatro pasos (todas las escrituras con re-autenticación):
 * 1. Datos → `POST /platform/remote-destinations` (queda deshabilitado).
 * 2. Credenciales → `PUT .../credentials` (write-only).
 * 3. Probar conexión → `POST .../test`, confirmando la huella del servidor la primera vez.
 * 4. Activar → `PATCH enabled=true` (`If-Match`) y, si se quiere, "Copiar ahora" (`POST .../sync`).
 * Si se cierra a medias, el destino queda en la lista (desactivado) y se completa desde su tarjeta.
 */
const open = defineModel<boolean>('open', { default: false })
const emit = defineEmits<{ changed: []; synced: [destination: RemoteDestination] }>()

const { t } = useI18n()
const { $api } = useNuxtApp()
const toast = useToast()
const { withReauth } = useReauth()

type Step = 'details' | 'credentials' | 'test' | 'activate'
const STEPS: Step[] = ['details', 'credentials', 'test', 'activate']

const step = ref<Step>('details')
const destination = ref<RemoteDestination | null>(null)
const form = reactive<DestinationForm>({
  name: '',
  host: '',
  port: SFTP_DEFAULTS.port,
  username: '',
  path: '',
  retention_days: SFTP_DEFAULTS.retention_days,
})
const errors = ref<Record<string, string | undefined>>({})
const busy = ref(false)
const tested = ref(false)
const activated = ref(false)
const syncQueued = ref(false)

watch(open, (o) => {
  if (!o) return
  step.value = 'details'
  destination.value = null
  Object.assign(form, {
    name: '',
    host: '',
    port: SFTP_DEFAULTS.port,
    username: '',
    path: '',
    retention_days: SFTP_DEFAULTS.retention_days,
  })
  errors.value = {}
  tested.value = false
  activated.value = false
  syncQueued.value = false
})

const stepIndex = computed(() => STEPS.indexOf(step.value))

function translateErrors(e: Record<string, string>) {
  return Object.fromEntries(
    Object.entries(e).map(([k, v]) => [
      k,
      t(`platform.storage.form.errors.${v}`, { ...RETENTION_RANGE, ...PORT_RANGE }),
    ]),
  )
}

async function create() {
  const local = validateDestination(form)
  errors.value = translateErrors(local as Record<string, string>)
  if (Object.keys(local).length) return
  busy.value = true
  try {
    destination.value = await withReauth(
      () =>
        unwrap(
          $api.POST('/platform/remote-destinations', {
            body: { kind: 'sftp', ...destinationValues(form) },
          }),
        ),
      t('platform.storage.reauth'),
    )
    emit('changed')
    step.value = 'credentials'
  } catch (e) {
    errors.value = { _: errorText(e) }
    if (e instanceof ApiError && (e.status === 422 || e.status === 409) && e.problem.errors) {
      errors.value = fieldErrors(e.problem)
    }
  } finally {
    busy.value = false
  }
}

function errorText(e: unknown) {
  if (isReauthRequired(e)) return t('platform.storage.reauthCancelled')
  if (e instanceof ApiError && e.status === 404) return t('platform.storage.gone')
  if (e instanceof ApiError && e.code === 'REMOTE_DESTINATION_NOT_READY') {
    return t('platform.storage.notReady')
  }
  const info = describeError(e)
  return t(info.messageKey, info.params ?? {})
}

function credentialsSaved(d: RemoteDestination) {
  destination.value = d
  emit('changed')
  step.value = 'test'
}

function testResult(r: RemoteDestinationTestResult) {
  if (r.destination) destination.value = r.destination
  tested.value = r.ok
  emit('changed')
}

async function activate() {
  const d = destination.value
  if (!d) return
  busy.value = true
  errors.value = {}
  try {
    destination.value = await withReauth(
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
    activated.value = true
    emit('changed')
  } catch (e) {
    if (e instanceof ApiError && e.status === 412 && e.problem.current) {
      destination.value = e.problem.current as unknown as RemoteDestination
      errors.value = { _: t('platform.storage.conflictRetry') }
    } else errors.value = { _: errorText(e) }
  } finally {
    busy.value = false
  }
}

async function syncNow() {
  const d = destination.value
  if (!d) return
  busy.value = true
  errors.value = {}
  try {
    destination.value = await unwrap(
      $api.POST('/platform/remote-destinations/{destination_id}/sync', {
        params: { path: { destination_id: d.id }, header: { 'Idempotency-Key': idempotencyKey() } },
      }),
    )
    syncQueued.value = true
    emit('synced', destination.value)
    toast.add({
      title: t('platform.storage.sync.queued', { name: d.name }),
      description: t('platform.storage.sync.queuedHint'),
      icon: 'i-lucide-upload-cloud',
      color: 'success',
    })
    emit('changed')
  } catch (e) {
    if (e instanceof ApiError && e.code === 'REMOTE_DESTINATION_SYNC_RUNNING') {
      errors.value = { _: t('platform.storage.sync.running') }
    } else errors.value = { _: errorText(e) }
  } finally {
    busy.value = false
  }
}

function finish() {
  if (destination.value && !activated.value) {
    toast.add({
      title: t('platform.storage.wizard.leftDisabled', { name: destination.value.name }),
      icon: 'i-lucide-info',
      color: 'info',
    })
  }
  open.value = false
}
</script>

<template>
  <UModal
    v-model:open="open"
    :title="t('platform.storage.wizard.title')"
    :description="t('platform.storage.wizard.description')"
    :dismissible="false"
    :ui="{ content: 'sm:max-w-2xl' }"
    data-testid="dst-wizard"
  >
    <template #body>
      <div class="flex flex-col gap-5">
        <ol
          class="grid grid-cols-4 gap-1 sm:gap-2"
          :aria-label="t('platform.storage.wizard.stepsLabel')"
          data-testid="dst-wizard-steps"
        >
          <li
            v-for="(s, i) in STEPS"
            :key="s"
            :aria-current="s === step ? 'step' : undefined"
            class="flex min-w-0 flex-col gap-1"
          >
            <span
              :class="[
                'h-1 rounded-full',
                i < stepIndex ? 'bg-primary' : i === stepIndex ? 'bg-primary/70' : 'bg-accented',
              ]"
              aria-hidden="true"
            />
            <span
              :class="[
                'truncate text-xs',
                s === step ? 'text-highlighted font-semibold' : 'text-muted',
              ]"
            >
              <span class="tabular">{{ i + 1 }}.</span>
              {{ t(`platform.storage.wizard.steps.${s}`) }}
            </span>
          </li>
        </ol>

        <!-- 1. Datos -->
        <form
          v-if="step === 'details'"
          id="dst-create-form"
          class="flex flex-col gap-4"
          novalidate
          @submit.prevent="create"
        >
          <RemoteCopyExplainer :retention-days="form.retention_days" />
          <RemoteDestinationFields v-model="form" :errors="errors" id-prefix="dst-new" />
          <UAlert
            v-if="errors._"
            color="error"
            variant="subtle"
            icon="i-lucide-circle-alert"
            :title="errors._"
            data-testid="dst-wizard-error"
          />
        </form>

        <!-- 2. Credenciales -->
        <template v-else-if="step === 'credentials' && destination">
          <p class="text-muted text-sm">
            {{
              t('platform.storage.wizard.credentialsIntro', { url: destinationUrl(destination) })
            }}
          </p>
          <RemoteDestinationCredentialsForm
            :destination="destination"
            :submit-label="t('platform.storage.wizard.credentialsSubmit')"
            @saved="credentialsSaved"
          />
        </template>

        <!-- 3. Probar -->
        <template v-else-if="step === 'test' && destination">
          <RemoteDestinationTestPanel :destination="destination" @result="testResult" />
        </template>

        <!-- 4. Activar -->
        <template v-else-if="step === 'activate' && destination">
          <div class="flex flex-col gap-4" data-testid="dst-wizard-activate">
            <UAlert
              v-if="!activated"
              color="info"
              variant="subtle"
              icon="i-lucide-power"
              :title="t('platform.storage.wizard.activateTitle')"
              :description="
                t('platform.storage.wizard.activateBody', {
                  days: destination.retention_days ?? 30,
                })
              "
            />
            <UAlert
              v-else
              color="success"
              variant="subtle"
              icon="i-lucide-circle-check"
              :title="t('platform.storage.wizard.activatedTitle', { name: destination.name })"
              :description="
                syncQueued
                  ? t('platform.storage.wizard.syncQueuedBody')
                  : t('platform.storage.wizard.activatedBody')
              "
              data-testid="dst-wizard-activated"
            />
            <RemoteCopyExplainer :retention-days="destination.retention_days" compact />
          </div>
          <UAlert
            v-if="errors._"
            color="error"
            variant="subtle"
            icon="i-lucide-circle-alert"
            :title="errors._"
            data-testid="dst-wizard-error"
          />
        </template>
      </div>
    </template>

    <template #footer>
      <div class="flex w-full flex-wrap justify-end gap-2">
        <template v-if="step === 'details'">
          <UButton
            color="neutral"
            variant="ghost"
            :label="t('common.cancel')"
            @click="open = false"
          />
          <UButton
            type="submit"
            form="dst-create-form"
            icon="i-lucide-arrow-right"
            trailing
            :loading="busy"
            :label="t('platform.storage.wizard.create')"
            data-testid="dst-wizard-create"
          />
        </template>
        <template v-else-if="step === 'credentials' || step === 'test'">
          <UButton
            color="neutral"
            variant="ghost"
            :label="t('platform.storage.wizard.later')"
            data-testid="dst-wizard-later"
            @click="finish"
          />
          <UButton
            v-if="step === 'test'"
            icon="i-lucide-arrow-right"
            trailing
            :disabled="!tested"
            :label="t('platform.storage.wizard.next')"
            data-testid="dst-wizard-next"
            @click="step = 'activate'"
          />
        </template>
        <template v-else>
          <UButton
            v-if="!activated"
            color="neutral"
            variant="ghost"
            :label="t('platform.storage.wizard.later')"
            data-testid="dst-wizard-later"
            @click="finish"
          />
          <UButton
            v-if="!activated"
            icon="i-lucide-power"
            :loading="busy"
            :label="t('platform.storage.wizard.activate')"
            data-testid="dst-wizard-activate-submit"
            @click="activate"
          />
          <template v-else>
            <UButton
              v-if="!syncQueued"
              color="neutral"
              variant="outline"
              icon="i-lucide-upload-cloud"
              :loading="busy"
              :label="t('platform.storage.sync.now')"
              data-testid="dst-wizard-sync"
              @click="syncNow"
            />
            <UButton
              icon="i-lucide-check"
              :label="t('platform.storage.wizard.finish')"
              data-testid="dst-wizard-finish"
              @click="finish"
            />
          </template>
        </template>
      </div>
    </template>
  </UModal>
</template>
