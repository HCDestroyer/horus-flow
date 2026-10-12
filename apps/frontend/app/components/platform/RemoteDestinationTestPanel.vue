<script setup lang="ts">
import type { RemoteDestination, RemoteDestinationTestResult } from '~~/types/api'
import type { StageState, TestStage } from '~/utils/remote-destinations'

/**
 * Prueba de conexión (`POST /platform/remote-destinations/{id}/test`, re-autenticación reciente)
 * paso a paso: conexión, clave del servidor, autenticación y escritura de prueba.
 *
 * - Primera vez (TOFU): para en `host_key` con la huella sin confirmar. Se muestra en grande y
 *   se pide comprobarla en el servidor (`ssh-keygen -lf …`) antes de "Confirmar huella", que
 *   repite la prueba con `confirm_host_key`.
 * - Huella distinta de la confirmada: aviso de seguridad "La clave del servidor cambió"; solo se
 *   acepta la nueva tras marcar que se verificó en el servidor.
 */
const props = defineProps<{ destination: RemoteDestination }>()
const emit = defineEmits<{ result: [result: RemoteDestinationTestResult] }>()

const { t } = useI18n()
const { $api } = useNuxtApp()
const { withReauth } = useReauth()

const result = ref<RemoteDestinationTestResult | null>(null)
/** Huella confirmada antes de la prueba (para detectar el cambio de clave). */
const pinned = ref<string | null>(null)
const busy = ref(false)
const error = ref<string | null>(null)
const verifiedChange = ref(false)

const outcome = computed(() => (result.value ? testOutcome(result.value, pinned.value) : null))
const states = computed(() => (result.value ? stageStates(result.value) : null))
const STEPS = TEST_STAGES.filter((s) => s !== 'done')

const STATE_ICON: Record<StageState, { icon: string; class: string }> = {
  done: { icon: 'i-lucide-circle-check', class: 'text-success' },
  failed: { icon: 'i-lucide-circle-x', class: 'text-error' },
  waiting: { icon: 'i-lucide-circle-pause', class: 'text-warning' },
  skipped: { icon: 'i-lucide-circle-dashed', class: 'text-dimmed' },
}

async function run(confirm?: string) {
  busy.value = true
  error.value = null
  pinned.value =
    result.value?.destination?.host_key_fingerprint ??
    props.destination.host_key_fingerprint ??
    null
  try {
    const res = await withReauth(
      () =>
        unwrap(
          $api.POST('/platform/remote-destinations/{destination_id}/test', {
            params: { path: { destination_id: props.destination.id } },
            body: confirm ? { confirm_host_key: confirm } : {},
          }),
        ),
      t('platform.storage.reauth'),
    )
    result.value = res
    verifiedChange.value = false
    emit('result', res)
  } catch (e) {
    if (isReauthRequired(e)) error.value = t('platform.storage.reauthCancelled')
    else if (e instanceof ApiError && e.status === 404) error.value = t('platform.storage.gone')
    else {
      const info = describeError(e)
      error.value = t(info.messageKey, info.params ?? {})
    }
  } finally {
    busy.value = false
  }
}

function stageLabel(stage: TestStage) {
  return t(`platform.storage.test.stages.${stage}`)
}
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="dst-test">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <p class="text-muted min-w-0 text-sm">
        {{ t('platform.storage.test.intro') }}
      </p>
      <UButton
        :color="result ? 'neutral' : 'primary'"
        :variant="result ? 'outline' : 'solid'"
        icon="i-lucide-plug-zap"
        :loading="busy && !result"
        :disabled="busy"
        :label="result ? t('platform.storage.test.again') : t('platform.storage.test.run')"
        data-testid="dst-test-run"
        @click="run()"
      />
    </div>

    <ol
      v-if="states"
      class="flex flex-col gap-2"
      :aria-label="t('platform.storage.test.stagesLabel')"
      data-testid="dst-test-stages"
    >
      <li
        v-for="stage in STEPS"
        :key="stage"
        class="flex items-start gap-2 text-sm"
        data-testid="dst-test-stage"
        :data-stage="stage"
        :data-state="states[stage]"
      >
        <UIcon
          :name="STATE_ICON[states[stage]].icon"
          :class="['mt-0.5 size-4 shrink-0', STATE_ICON[states[stage]].class]"
          aria-hidden="true"
        />
        <span :class="states[stage] === 'skipped' ? 'text-muted' : 'text-highlighted'">
          {{ stageLabel(stage) }}
        </span>
        <span class="text-muted">· {{ t(`platform.storage.test.state.${states[stage]}`) }}</span>
      </li>
    </ol>

    <!-- Correcto -->
    <UAlert
      v-if="outcome === 'ok'"
      color="success"
      variant="subtle"
      icon="i-lucide-circle-check"
      :title="t('platform.storage.test.okTitle')"
      :description="t('platform.storage.test.okBody', { path: destination.path })"
      data-testid="dst-test-ok"
    />

    <!-- TOFU: confirmar la huella -->
    <section
      v-else-if="outcome === 'confirm' && result?.host_key_fingerprint"
      class="border-warning bg-warning/5 flex flex-col gap-3 rounded-md border p-3 sm:p-4"
      aria-labelledby="dst-fp-title"
      data-testid="dst-fingerprint-confirm"
    >
      <h4 id="dst-fp-title" class="text-highlighted flex items-center gap-2 text-sm font-semibold">
        <UIcon
          name="i-lucide-fingerprint"
          class="text-warning size-5 shrink-0"
          aria-hidden="true"
        />
        {{ t('platform.storage.test.confirmTitle') }}
      </h4>
      <p class="text-default text-sm">
        {{ t('platform.storage.test.confirmBody', { host: destination.host }) }}
      </p>
      <div class="flex flex-wrap items-center gap-2">
        <span
          class="text-highlighted bg-default border-default rounded border px-2 py-1.5 font-mono text-sm font-semibold break-all"
          data-testid="dst-fingerprint"
          >{{ result.host_key_fingerprint }}</span
        >
        <CopyButton :value="result.host_key_fingerprint" size="xs" variant="ghost" />
      </div>
      <div>
        <p class="text-muted text-sm">{{ t('platform.storage.test.confirmHow') }}</p>
        <code
          class="text-highlighted bg-default border-default mt-1 block rounded border px-2 py-1.5 font-mono text-xs break-all"
          >ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub</code
        >
        <p class="text-muted mt-1 text-xs">{{ t('platform.storage.test.confirmHowOther') }}</p>
      </div>
      <p class="text-warning text-sm font-medium">{{ t('platform.storage.test.confirmWarn') }}</p>
      <div class="flex justify-end">
        <UButton
          color="warning"
          icon="i-lucide-fingerprint"
          :loading="busy"
          :label="t('platform.storage.test.confirm')"
          data-testid="dst-confirm-fingerprint"
          @click="run(result.host_key_fingerprint)"
        />
      </div>
    </section>

    <!-- La clave del servidor cambió -->
    <section
      v-else-if="outcome === 'mismatch' && result?.host_key_fingerprint"
      role="alert"
      class="border-error bg-error/5 flex flex-col gap-3 rounded-md border p-3 sm:p-4"
      aria-labelledby="dst-mismatch-title"
      data-testid="dst-fingerprint-mismatch"
    >
      <h4 id="dst-mismatch-title" class="text-error flex items-center gap-2 text-sm font-semibold">
        <UIcon name="i-lucide-shield-alert" class="size-5 shrink-0" aria-hidden="true" />
        {{ t('platform.storage.test.mismatchTitle') }}
      </h4>
      <p class="text-default text-sm">
        {{ t('platform.storage.test.mismatchBody', { host: destination.host }) }}
      </p>
      <dl class="grid gap-2 text-sm">
        <div class="min-w-0">
          <dt class="text-muted text-xs">{{ t('platform.storage.test.mismatchPinned') }}</dt>
          <dd class="text-highlighted font-mono break-all" data-testid="dst-fingerprint-pinned">
            {{ pinned ?? result.destination?.host_key_fingerprint ?? '—' }}
          </dd>
        </div>
        <div class="min-w-0">
          <dt class="text-muted text-xs">{{ t('platform.storage.test.mismatchPresented') }}</dt>
          <dd class="text-error font-mono font-semibold break-all" data-testid="dst-fingerprint">
            {{ result.host_key_fingerprint }}
          </dd>
        </div>
      </dl>
      <p class="text-default text-sm">{{ t('platform.storage.test.mismatchWhat') }}</p>
      <code
        class="text-highlighted bg-default border-default block rounded border px-2 py-1.5 font-mono text-xs break-all"
        >ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub</code
      >
      <UCheckbox
        v-model="verifiedChange"
        :label="t('platform.storage.test.mismatchVerified')"
        data-testid="dst-mismatch-verified"
      />
      <div class="flex justify-end">
        <UButton
          color="error"
          variant="outline"
          icon="i-lucide-fingerprint"
          :disabled="!verifiedChange"
          :loading="busy"
          :label="t('platform.storage.test.mismatchAccept')"
          data-testid="dst-accept-new-fingerprint"
          @click="run(result.host_key_fingerprint)"
        />
      </div>
    </section>

    <!-- Otro paso falló -->
    <UAlert
      v-else-if="outcome === 'failed'"
      color="error"
      variant="subtle"
      icon="i-lucide-circle-x"
      :title="
        t('platform.storage.test.failedTitle', { stage: stageLabel(result!.stage).toLowerCase() })
      "
      :description="
        [result?.error, t(`platform.storage.test.hint.${result!.stage}`)].filter(Boolean).join(' ')
      "
      data-testid="dst-test-failed"
    />

    <UAlert
      v-if="error"
      color="error"
      variant="subtle"
      icon="i-lucide-circle-alert"
      :title="error"
      data-testid="dst-test-error"
    />
  </div>
</template>
