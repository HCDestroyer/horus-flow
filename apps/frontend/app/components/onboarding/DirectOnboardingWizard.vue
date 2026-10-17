<script setup lang="ts">
import type { DirectOnboarding, FlowExporter, Router } from '~~/types/api'

/**
 * Conexión de un router Huawei VRP en modo directo, en tres pasos (docs/vendors/huawei.md §5):
 * 1. Generar la configuración VRP (`POST /routers/{id}/direct-onboarding`, `x-reauth`): aviso
 *    claro de que no hay túnel WireGuard y de que el camino lo asegura el operador; dirección de
 *    gestión y muestreo; la credencial SNMPv3 se muestra una sola vez.
 * 2. Pegar en el router: se espera a que el sondeador verifique la credencial SNMPv3 en la
 *    dirección de gestión (`onboarding_state = snmp_verified`).
 * 3. Verificación: SNMP verificado → primer flujo → primeros clientes (sondeo cada 3 s).
 * Horus nunca escribe en el router: la configuración la pega el técnico.
 */
const props = defineProps<{
  router: Router
  exporter: FlowExporter | null
  activeCustomers: number
  discovery: boolean
}>()
const emit = defineEmits<{ refresh: [] }>()

const { t } = useI18n()
const { $api } = useNuxtApp()
const can = useCan()
const { withReauth } = useReauth()
const canWrite = computed(() => can('devices.credentials.write'))

const address = ref(props.router.management_address ?? '')
const sampling = ref('1000')
const result = ref<DirectOnboarding | null>(null)
const generating = ref(false)
const genError = shallowRef<unknown>(null)
const addressError = ref<string | null>(null)

const ORDER = ['pending_configuration', 'snmp_verified', 'exporting'] as const
const stage = computed(() =>
  Math.max(0, ORDER.indexOf(props.router.onboarding_state as (typeof ORDER)[number])),
)

async function request() {
  const rate = Number(sampling.value)
  return unwrap(
    $api.POST('/routers/{router_id}/direct-onboarding', {
      params: { path: { router_id: props.router.id } },
      body: {
        management_address: address.value.trim() || null,
        sampling_rate: Number.isInteger(rate) && rate > 0 ? rate : null,
      },
    }),
  )
}

async function generate() {
  addressError.value = null
  if (!address.value.trim() && !props.router.management_address) {
    addressError.value = t('onboarding.direct.generate.addressRequired')
    return
  }
  generating.value = true
  genError.value = null
  try {
    result.value = await withReauth(request, t('onboarding.direct.generate.reauth'))
    emit('refresh')
  } catch (e) {
    if (e instanceof ApiError && e.code === 'VALIDATION_FAILED') {
      const fe = fieldErrors(e.problem)
      if (fe.management_address) {
        addressError.value = t('devices.router.managementAddressInvalid')
        return
      }
    }
    if (e instanceof ApiError && e.code === 'MANAGEMENT_ADDRESS_IN_USE') {
      addressError.value = t('onboarding.direct.generate.addressTaken')
      return
    }
    genError.value = e
  } finally {
    generating.value = false
  }
}

const genErrorText = computed(() =>
  isReauthRequired(genError.value) ? t('onboarding.direct.generate.reauthNeeded') : null,
)
const filename = computed(() => `horus-alta-${props.router.name}.txt`)
const shownAddress = computed(
  () => result.value?.management_address ?? props.router.management_address ?? '',
)

type StepState = 'done' | 'current' | 'waiting'
const steps = computed<{ key: string; state: StepState }[]>(() => {
  const generated = !!result.value || stage.value > 0
  return [
    { key: 'generate', state: generated ? 'done' : 'current' },
    { key: 'paste', state: stage.value > 0 ? 'done' : generated ? 'current' : 'waiting' },
    {
      key: 'verify',
      state:
        stage.value >= 2 && props.activeCustomers > 0
          ? 'done'
          : stage.value > 0
            ? 'current'
            : 'waiting',
    },
  ]
})

const checks = computed(() => [
  { key: 'snmp', done: stage.value >= 1, at: null },
  { key: 'flow', done: stage.value >= 2, at: props.exporter?.last_flow_at ?? null },
  { key: 'customers', done: props.activeCustomers > 0, at: null },
])

const STEP_ICON: Record<StepState, string> = {
  done: 'i-lucide-circle-check',
  current: 'i-lucide-circle-dot',
  waiting: 'i-lucide-circle',
}

// Sondeo en vivo mientras el asistente no ha terminado (solo tras generar o con dirección).
let timer: ReturnType<typeof setInterval> | undefined
watchEffect(() => {
  clearInterval(timer)
  const finished = stage.value >= 2 && (props.activeCustomers > 0 || props.discovery)
  const started = !!result.value || stage.value > 0
  if (started && !finished) timer = setInterval(() => emit('refresh'), 3000)
})
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="direct-onboarding">
    <UAlert
      color="warning"
      variant="subtle"
      icon="i-lucide-shield-alert"
      :title="t('onboarding.direct.warningTitle')"
      :description="t('onboarding.direct.warningBody')"
      data-testid="direct-warning"
    />
    <ol class="flex flex-col gap-4">
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
              }"
              aria-hidden="true"
            />
            <div class="flex min-w-0 flex-1 flex-col gap-3">
              <h3 class="text-highlighted font-semibold">
                {{ t('onboarding.stepN', { n: i + 1 }) }} ·
                {{ t(`onboarding.direct.${step.key}.title`) }}
                <span class="sr-only">({{ t(`onboarding.state.${step.state}`) }})</span>
              </h3>

              <!-- Paso 1: generar la configuración VRP -->
              <template v-if="step.key === 'generate'">
                <p class="text-muted text-sm">{{ t('onboarding.direct.generate.pending') }}</p>
                <div v-if="canWrite" class="grid gap-3 sm:grid-cols-2">
                  <UFormField
                    :label="t('onboarding.direct.generate.address')"
                    :help="t('onboarding.direct.generate.addressHelp')"
                    :error="addressError ?? undefined"
                    required
                  >
                    <UInput
                      v-model="address"
                      class="w-full"
                      :ui="{ base: 'font-mono' }"
                      autocomplete="off"
                      spellcheck="false"
                      placeholder="198.51.100.20"
                      data-testid="direct-address"
                    />
                  </UFormField>
                  <UFormField
                    :label="t('onboarding.direct.generate.sampling')"
                    :help="t('onboarding.direct.generate.samplingHelp')"
                  >
                    <UInput
                      v-model="sampling"
                      class="w-full"
                      inputmode="numeric"
                      :ui="{ base: 'font-mono' }"
                      data-testid="direct-sampling"
                    />
                  </UFormField>
                </div>
                <template v-if="result">
                  <UAlert
                    color="warning"
                    variant="subtle"
                    icon="i-lucide-eye-off"
                    :title="t('onboarding.direct.generate.onceTitle')"
                    :description="t('onboarding.direct.generate.onceBody')"
                    data-testid="direct-once"
                  />
                  <UAlert
                    color="info"
                    variant="subtle"
                    icon="i-lucide-list-checks"
                    :title="t('onboarding.direct.generate.notes')"
                  >
                    <template #description>
                      <ul class="list-disc space-y-1 ps-4" data-testid="direct-notes">
                        <li v-for="n in result.notes" :key="n">{{ n }}</li>
                      </ul>
                    </template>
                  </UAlert>
                  <UAlert
                    v-if="result.placeholders.length"
                    color="neutral"
                    variant="subtle"
                    icon="i-lucide-pencil-line"
                    :title="t('onboarding.direct.generate.placeholders')"
                  >
                    <template #description>
                      <ul class="list-disc space-y-1 ps-4" data-testid="direct-placeholders">
                        <li v-for="p in result.placeholders" :key="p.key">
                          <code class="font-mono">{{ p.key }}</code> — {{ p.description }}
                        </li>
                      </ul>
                    </template>
                  </UAlert>
                  <CodeBlock
                    :code="result.config"
                    :label="t('onboarding.direct.generate.configLabel')"
                    :filename="filename"
                    max-height="20rem"
                    data-testid="direct-config"
                  />
                  <UAlert
                    v-for="w in result.warnings"
                    :key="w"
                    color="warning"
                    variant="subtle"
                    icon="i-lucide-triangle-alert"
                    :title="w"
                  />
                </template>
                <UAlert
                  v-if="genErrorText"
                  color="warning"
                  variant="subtle"
                  icon="i-lucide-triangle-alert"
                  :title="genErrorText"
                  data-testid="direct-blocked"
                />
                <ErrorState v-else-if="genError" :error="genError" @retry="generate" />
                <div v-if="canWrite" class="flex flex-wrap gap-2">
                  <UButton
                    :icon="result ? 'i-lucide-refresh-cw' : 'i-lucide-file-code-2'"
                    :color="result || stage > 0 ? 'neutral' : 'primary'"
                    :variant="result || stage > 0 ? 'outline' : 'solid'"
                    :loading="generating"
                    :label="
                      result || stage > 0
                        ? t('onboarding.direct.generate.regenerate')
                        : t('onboarding.direct.generate.action')
                    "
                    data-testid="direct-generate"
                    @click="generate"
                  />
                </div>
                <p v-else class="text-muted text-sm">
                  {{ t('onboarding.direct.generate.noPermission') }}
                </p>
              </template>

              <!-- Paso 2: pegar en el router -->
              <template v-else-if="step.key === 'paste'">
                <p class="text-default text-sm">{{ t('onboarding.direct.paste.body') }}</p>
                <p v-if="shownAddress" class="text-muted text-sm">
                  {{ t('onboarding.direct.allowList', { ip: shownAddress }) }}
                </p>
                <p
                  v-if="stage > 0"
                  class="text-success flex items-center gap-1.5 text-sm font-medium"
                  data-testid="direct-verified"
                >
                  <UIcon name="i-lucide-shield-check" class="size-4" aria-hidden="true" />
                  {{ t('onboarding.direct.paste.verified', { ip: shownAddress }) }}
                </p>
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
                  {{ t('onboarding.direct.paste.waiting') }}
                </p>
              </template>

              <!-- Paso 3: verificación -->
              <template v-else>
                <ul class="flex flex-col gap-2" data-testid="direct-checks">
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
                      {{ t(`onboarding.direct.verify.${c.key}`) }}
                      <RelativeTime v-if="c.done && c.at" :at="c.at" class="text-muted" />
                      <span class="sr-only">{{
                        c.done ? t('onboarding.state.done') : t('onboarding.state.waiting')
                      }}</span>
                    </span>
                  </li>
                </ul>
                <p v-if="stage >= 2 && !activeCustomers && discovery" class="text-muted text-sm">
                  {{ t('onboarding.verify.needPrefixes') }}
                </p>
                <UCollapsible v-if="stage < 2" class="flex flex-col gap-2">
                  <UButton
                    color="neutral"
                    variant="link"
                    size="sm"
                    icon="i-lucide-life-buoy"
                    :label="t('onboarding.direct.verify.stuck')"
                    class="self-start px-0"
                  />
                  <template #content>
                    <ul class="text-muted list-disc space-y-1 ps-5 text-sm">
                      <li>{{ t('onboarding.direct.verify.checkPath') }}</li>
                      <li>{{ t('onboarding.direct.verify.checkAcl') }}</li>
                      <li>{{ t('onboarding.direct.verify.checkNetstream') }}</li>
                      <li>{{ t('onboarding.direct.verify.checkNat') }}</li>
                    </ul>
                  </template>
                </UCollapsible>
              </template>
            </div>
          </div>
        </UCard>
      </li>
    </ol>
  </div>
</template>
