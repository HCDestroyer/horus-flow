<script setup lang="ts">
import type { FlowExporter, Peer, Router } from '~~/types/api'

/**
 * Conexión del router en tres pasos (I1-19, frontend.md §8.4; vendors/mikrotik.md §5.2, §7):
 * 1. Generar script: requisitos visibles; "Copiar" y "Descargar .rsc"; contraseñas y token
 *    de enrolamiento (un uso, 24 h) se muestran una sola vez; "Regenerar" y "Revocar token".
 * 2. Pegar en el router: el script envía la clave sola; la UI pasa a "Clave recibida".
 * 3. Verificación: clave → handshake → primer flujo → primeros clientes, marcados en vivo
 *    (sondeo cada 3 s mientras no termina; el WebSocket `wireguard.peers` lo sustituirá).
 * Horus no escribe en el router: el script lo pega el técnico.
 */
const props = defineProps<{
  router: Router
  peer: Peer | null
  exporter: FlowExporter | null
  activeCustomers: number
  discovery: boolean
}>()
const emit = defineEmits<{ refresh: [] }>()

const { t, d } = useI18n()
const { $api } = useNuxtApp()
const can = useCan()
const toast = useToast()
const canWrite = computed(() => can('wireguard.write'))

const script = ref<string | null>(null)
const tokenId = ref<string | null>(null)
const tokenExpires = ref<string | null>(null)
const generating = ref(false)
const genError = shallowRef<unknown>(null)

const ORDER = ['pending_configuration', 'key_received', 'tunnel_up', 'exporting'] as const
const stage = computed(() => Math.max(0, ORDER.indexOf(props.router.onboarding_state)))
const tokenState = computed(() => props.peer?.enrollment.token_state ?? 'none')
const tokenDead = computed(
  () => stage.value === 0 && (tokenState.value === 'expired' || tokenState.value === 'revoked'),
)

async function generate() {
  generating.value = true
  genError.value = null
  try {
    const res = await $api.POST('/routers/{router_id}/provisioning-script', {
      params: {
        path: { router_id: props.router.id },
        header: { 'Idempotency-Key': idempotencyKey() },
      },
      body: {},
      parseAs: 'text',
    })
    if (!res.response.ok) {
      throw new ApiError(toProblem(res.response.status, res.error))
    }
    script.value = res.data as unknown as string
    tokenId.value = res.response.headers.get('X-Horus-Enrollment-Token-Id')
    tokenExpires.value = res.response.headers.get('X-Horus-Enrollment-Token-Expires-At')
    emit('refresh')
  } catch (e) {
    genError.value = e
  } finally {
    generating.value = false
  }
}

async function revoke() {
  const id = tokenId.value ?? props.peer?.enrollment.token_id
  if (!id) return
  try {
    await unwrap(
      $api.POST('/wireguard/enrollment-tokens/{token_id}/revoke', {
        params: { path: { token_id: id } },
      }),
    )
    script.value = null
    toast.add({ title: t('onboarding.revoked'), icon: 'i-lucide-ban', color: 'success' })
    emit('refresh')
  } catch (e) {
    toast.add({ title: t(describeError(e).messageKey), color: 'error' })
  }
}

const filename = computed(() => `horus-alta-${props.router.name}.rsc`)

type StepState = 'done' | 'current' | 'waiting' | 'error'
const steps = computed<{ key: string; state: StepState }[]>(() => {
  const generated =
    !!script.value || ['pending', 'used'].includes(tokenState.value) || stage.value > 0
  return [
    { key: 'generate', state: generated ? 'done' : 'current' },
    {
      key: 'paste',
      state:
        stage.value > 0 ? 'done' : tokenDead.value ? 'error' : generated ? 'current' : 'waiting',
    },
    {
      key: 'verify',
      state:
        stage.value >= 3 && props.activeCustomers > 0
          ? 'done'
          : stage.value > 0
            ? 'current'
            : 'waiting',
    },
  ]
})

const checks = computed(() => [
  { key: 'key', done: stage.value >= 1, at: props.peer?.enrollment.enrolled_at },
  { key: 'tunnel', done: stage.value >= 2, at: props.peer?.last_handshake_at },
  { key: 'flow', done: stage.value >= 3, at: props.exporter?.last_flow_at },
  { key: 'customers', done: props.activeCustomers > 0, at: null },
])

const STEP_ICON: Record<StepState, string> = {
  done: 'i-lucide-circle-check',
  current: 'i-lucide-circle-dot',
  waiting: 'i-lucide-circle',
  error: 'i-lucide-circle-alert',
}

// Sondeo en vivo mientras el asistente no ha terminado.
let timer: ReturnType<typeof setInterval> | undefined
watchEffect(() => {
  clearInterval(timer)
  const finished = stage.value >= 3 && (props.activeCustomers > 0 || props.discovery)
  if (!finished) timer = setInterval(() => emit('refresh'), 3000)
})
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <ol class="flex flex-col gap-4" data-testid="onboarding">
    <li v-for="(step, i) in steps" :key="step.key" :data-step="step.key" :data-state="step.state">
      <UCard>
        <div class="flex gap-3">
          <UIcon
            :name="STEP_ICON[step.state]"
            class="mt-0.5 size-6 shrink-0"
            :class="{
              'text-success': step.state === 'done',
              'text-primary': step.state === 'current',
              'text-dimmed': step.state === 'waiting',
              'text-error': step.state === 'error',
            }"
            aria-hidden="true"
          />
          <div class="flex min-w-0 flex-1 flex-col gap-3">
            <h3 class="text-highlighted font-semibold">
              {{ t('onboarding.stepN', { n: i + 1 }) }} · {{ t(`onboarding.${step.key}.title`) }}
              <span class="sr-only">({{ t(`onboarding.state.${step.state}`) }})</span>
            </h3>

            <!-- Paso 1: generar script -->
            <template v-if="step.key === 'generate'">
              <ul class="text-muted list-disc space-y-1 ps-5 text-sm">
                <li>{{ t('onboarding.generate.reqVersion') }}</li>
                <li>{{ t('onboarding.generate.reqNoWrite') }}</li>
                <li>{{ t('onboarding.generate.reqOutbound') }}</li>
              </ul>
              <template v-if="script">
                <UAlert
                  color="warning"
                  variant="subtle"
                  icon="i-lucide-eye-off"
                  :title="t('onboarding.generate.onceTitle')"
                  :description="
                    t('onboarding.generate.onceBody', {
                      date: tokenExpires ? d(new Date(tokenExpires), 'long') : '',
                    })
                  "
                  data-testid="script-once"
                />
                <CodeBlock
                  :code="script"
                  :label="t('onboarding.generate.scriptLabel')"
                  :filename="filename"
                  max-height="18rem"
                  data-testid="provisioning-script"
                />
              </template>
              <ErrorState v-if="genError" :error="genError" @retry="generate" />
              <div v-if="canWrite && stage === 0" class="flex flex-wrap gap-2">
                <UButton
                  :icon="script ? 'i-lucide-refresh-cw' : 'i-lucide-file-code-2'"
                  :color="script ? 'neutral' : 'primary'"
                  :variant="script ? 'outline' : 'solid'"
                  :loading="generating"
                  :label="
                    script || tokenState === 'pending'
                      ? t('onboarding.generate.regenerate')
                      : t('onboarding.generate.action')
                  "
                  data-testid="generate-script"
                  @click="generate"
                />
                <UButton
                  v-if="script || tokenState === 'pending'"
                  color="neutral"
                  variant="ghost"
                  icon="i-lucide-ban"
                  :label="t('onboarding.generate.revoke')"
                  data-testid="revoke-token"
                  @click="revoke"
                />
              </div>
              <p v-else-if="!canWrite && stage === 0" class="text-muted text-sm">
                {{ t('onboarding.generate.noPermission') }}
              </p>
            </template>

            <!-- Paso 2: pegar en el router -->
            <template v-else-if="step.key === 'paste'">
              <p class="text-default text-sm">{{ t('onboarding.paste.body') }}</p>
              <p
                v-if="stage > 0"
                class="text-success flex items-center gap-1.5 text-sm font-medium"
                data-testid="key-received"
              >
                <UIcon name="i-lucide-key-round" class="size-4" aria-hidden="true" />
                {{ t('onboarding.paste.received') }}
              </p>
              <UAlert
                v-else-if="tokenDead"
                color="error"
                variant="subtle"
                icon="i-lucide-timer-off"
                :title="
                  tokenState === 'revoked'
                    ? t('onboarding.paste.revoked')
                    : t('onboarding.paste.expired')
                "
                data-testid="token-dead"
              >
                <template v-if="canWrite" #actions>
                  <UButton
                    size="sm"
                    icon="i-lucide-file-code-2"
                    :label="t('onboarding.paste.newToken')"
                    @click="generate"
                  />
                </template>
              </UAlert>
              <p
                v-else-if="step.state === 'current'"
                class="text-muted flex items-center gap-1.5 text-sm"
                role="status"
              >
                <UIcon
                  name="i-lucide-loader-circle"
                  class="size-4 animate-spin motion-reduce:animate-none"
                  aria-hidden="true"
                />
                {{ t('onboarding.paste.waiting') }}
              </p>
            </template>

            <!-- Paso 3: verificación -->
            <template v-else>
              <ul class="flex flex-col gap-2" data-testid="onboarding-checks">
                <li
                  v-for="c in checks"
                  :key="c.key"
                  class="flex items-start gap-2 text-sm"
                  :data-check="c.key"
                  :data-done="c.done"
                >
                  <UIcon
                    :name="c.done ? 'i-lucide-check' : 'i-lucide-minus'"
                    class="mt-0.5 size-4 shrink-0"
                    :class="c.done ? 'text-success' : 'text-dimmed'"
                    aria-hidden="true"
                  />
                  <span :class="c.done ? 'text-default' : 'text-muted'">
                    {{ t(`onboarding.verify.${c.key}`) }}
                    <RelativeTime v-if="c.done && c.at" :at="c.at" class="text-muted" />
                    <span class="sr-only">{{
                      c.done ? t('onboarding.state.done') : t('onboarding.state.waiting')
                    }}</span>
                  </span>
                </li>
              </ul>
              <p v-if="stage >= 3 && !activeCustomers && discovery" class="text-muted text-sm">
                {{ t('onboarding.verify.needPrefixes') }}
              </p>
              <UCollapsible v-if="stage > 0 && stage < 3" class="flex flex-col gap-2">
                <UButton
                  color="neutral"
                  variant="link"
                  size="sm"
                  icon="i-lucide-life-buoy"
                  :label="t('onboarding.verify.stuck')"
                  class="self-start px-0"
                />
                <template #content>
                  <ul class="text-muted list-disc space-y-1 ps-5 text-sm">
                    <li>{{ t('onboarding.verify.checkFirewall') }}</li>
                    <li>{{ t('onboarding.verify.checkOutbound') }}</li>
                    <li>{{ t('onboarding.verify.checkTrafficFlow') }}</li>
                    <li>{{ t('onboarding.verify.checkOffload') }}</li>
                  </ul>
                </template>
              </UCollapsible>
            </template>
          </div>
        </div>
      </UCard>
    </li>
  </ol>
</template>
