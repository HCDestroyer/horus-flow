<script setup lang="ts">
import type { Router, SwitchOnboarding } from '~~/types/api'

/**
 * Conexión de un switch en tres pasos (docs/vendors/mikrotik.md §13, huawei.md §11,
 * docs/tunnels.md §10):
 * 1. Generar (`POST /routers/{id}/switch-onboarding`, `x-reauth`): camino — «¿Horus está en la
 *    misma red de gestión que el switch?» → directo; si no (por defecto), vía un router del sitio
 *    con túnel —, dirección de gestión, sFlow opcional (solo serie S) y la credencial SNMP nueva,
 *    que se muestra una sola vez (v3; en SwOS una comunidad v2c con aviso).
 * 2. Pegar en el switch (o seguir los pasos de la web de SwOS) y, vía router, en el router del
 *    sitio (NAT 1:1 y cortafuegos mínimo).
 * 3. Verificación en vivo: `onboarding_state = snmp_verified` (sondeo cada 3 s).
 * Horus nunca escribe en el switch ni en el router.
 */
const props = defineProps<{ router: Router; portsDiscovered: boolean }>()
const emit = defineEmits<{ refresh: [] }>()

const { t } = useI18n()
const { $api } = useNuxtApp()
const can = useCan()
const { withReauth } = useReauth()
const canWrite = computed(() => can('devices.credentials.write'))
const platform = computed(() => props.router.switch_platform ?? 'routeros')

const address = ref(props.router.management_address ?? '')
type SwitchMode = 'direct' | 'via_router'
// Por defecto vía router: lo habitual es Horus en un servidor público y el switch en una red
// privada. Un switch ya dado de alta en modo directo conserva su modo.
const mode = ref<SwitchMode>(
  props.router.connection_mode === 'via_router' ||
    props.router.onboarding_state === 'pending_configuration'
    ? 'via_router'
    : 'direct',
)
const viaRouterId = ref<string | undefined>(props.router.via_router_id ?? undefined)
const routerError = ref<string | null>(null)
const tunnelRouters = useTenantQuery(
  () => `switch:${props.router.id}:via-routers`,
  () =>
    unwrap($api.GET('/routers', { params: { query: { device_type: 'router', limit: 200 } } })).then(
      (r) => r.data.filter((x) => hasTunnel(x.connection_mode)),
    ),
)
const routerItems = computed(() =>
  [...(tunnelRouters.data.value ?? [])]
    .sort(
      (a, b) =>
        Number(b.site_id === props.router.site_id) - Number(a.site_id === props.router.site_id) ||
        a.name.localeCompare(b.name),
    )
    .map((x) => ({
      label: x.name,
      value: x.id,
      description: `${t(`transport.mode.${x.connection_mode}`)} · ${t(`transport.protection.short.${x.protection_level}`)}${
        x.site_id === props.router.site_id ? ` · ${t('switches.onboarding.mode.sameSite')}` : ''
      }`,
      icon: protectionVisual(x.protection_level).icon,
    })),
)
const modeItems = computed(() => [
  {
    value: 'via_router',
    label: t('switches.onboarding.mode.via'),
    description: t('switches.onboarding.mode.viaHelp'),
  },
  {
    value: 'direct',
    label: t('switches.onboarding.mode.direct'),
    description: t('switches.onboarding.mode.directHelp'),
  },
])
const sflow = ref(props.router.sflow_enabled)
const sampling = ref('4096')
const result = ref<SwitchOnboarding | null>(null)
const generating = ref(false)
const genError = shallowRef<unknown>(null)
const addressError = ref<string | null>(null)
const verified = computed(() => props.router.onboarding_state !== 'pending_configuration')

async function request() {
  const rate = Number(sampling.value)
  return unwrap(
    $api.POST('/routers/{router_id}/switch-onboarding', {
      params: { path: { router_id: props.router.id } },
      body: {
        connection_mode: mode.value,
        via_router_id: mode.value === 'via_router' ? (viaRouterId.value ?? null) : null,
        management_address: address.value.trim() || null,
        sflow: sflowSupported(platform.value) ? sflow.value : null,
        sampling_rate: sflow.value && Number.isInteger(rate) ? rate : null,
      },
    }),
  )
}

async function generate() {
  addressError.value = null
  routerError.value = null
  if (mode.value === 'via_router' && !viaRouterId.value) {
    routerError.value = t('switches.onboarding.mode.routerRequired')
    return
  }
  if (!address.value.trim() && !props.router.management_address) {
    addressError.value = t('switches.onboarding.generate.addressRequired')
    return
  }
  generating.value = true
  genError.value = null
  try {
    result.value = await withReauth(request, t('switches.onboarding.generate.reauth'))
    emit('refresh')
  } catch (e) {
    if (e instanceof ApiError && e.code === 'VALIDATION_FAILED') {
      const fe = fieldErrors(e.problem)
      if (fe.via_router_id) {
        routerError.value = t('switches.onboarding.mode.routerInvalid')
        return
      }
      if (fe.management_address) {
        addressError.value = t('switches.onboarding.generate.addressInvalid')
        return
      }
    }
    if (e instanceof ApiError && e.code === 'MANAGEMENT_ADDRESS_IN_USE') {
      addressError.value =
        mode.value === 'via_router'
          ? t('switches.onboarding.mode.addressTakenVia')
          : t('switches.onboarding.generate.addressTaken')
      return
    }
    genError.value = e
  } finally {
    generating.value = false
  }
}

const genErrorText = computed(() =>
  isReauthRequired(genError.value) ? t('switches.onboarding.generate.reauthNeeded') : null,
)
const shownAddress = computed(
  () => result.value?.management_address ?? props.router.management_address ?? '',
)

type StepState = 'done' | 'current' | 'waiting'
const steps = computed<{ key: string; state: StepState }[]>(() => {
  const generated = !!result.value || verified.value
  return [
    { key: 'generate', state: generated ? 'done' : 'current' },
    { key: 'paste', state: verified.value ? 'done' : generated ? 'current' : 'waiting' },
    {
      key: 'verify',
      state:
        verified.value && props.portsDiscovered ? 'done' : verified.value ? 'current' : 'waiting',
    },
  ]
})
const STEP_ICON: Record<StepState, string> = {
  done: 'i-lucide-circle-check',
  current: 'i-lucide-circle-dot',
  waiting: 'i-lucide-circle',
}

let timer: ReturnType<typeof setInterval> | undefined
watchEffect(() => {
  clearInterval(timer)
  const started = !!result.value || !!props.router.management_address
  if (started && !(verified.value && props.portsDiscovered))
    timer = setInterval(() => emit('refresh'), 3000)
})
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="switch-onboarding">
    <UAlert
      v-if="mode === 'direct'"
      color="warning"
      variant="subtle"
      icon="i-lucide-shield-alert"
      :title="t('switches.onboarding.directTitle')"
      :description="t('switches.onboarding.directBody')"
      data-testid="switch-mode-direct-note"
    />
    <UAlert
      v-else
      color="info"
      variant="subtle"
      icon="i-lucide-route"
      :title="t('switches.onboarding.viaTitle')"
      :description="t('switches.onboarding.viaBody')"
      data-testid="switch-mode-via-note"
    />
    <UAlert
      v-if="isV2cOnly(platform)"
      color="warning"
      variant="subtle"
      icon="i-lucide-lock-open"
      :title="t('switches.form.swosWarningTitle')"
      :description="t('switches.form.swosWarningBody')"
      data-testid="switch-onboarding-v2c"
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
                {{ t(`switches.onboarding.${step.key}.title`) }}
                <span class="sr-only">({{ t(`onboarding.state.${step.state}`) }})</span>
              </h3>

              <template v-if="step.key === 'generate'">
                <p class="text-muted text-sm">{{ t('switches.onboarding.generate.pending') }}</p>
                <UFormField
                  v-if="canWrite"
                  :label="t('switches.onboarding.mode.question')"
                  :help="t('switches.onboarding.mode.help')"
                >
                  <URadioGroup
                    v-model="mode"
                    :items="modeItems"
                    variant="card"
                    orientation="horizontal"
                    :ui="{ fieldset: 'flex flex-col gap-2 sm:flex-row' }"
                    data-testid="switch-mode"
                  />
                </UFormField>
                <template v-if="canWrite && mode === 'via_router'">
                  <UAlert
                    v-if="tunnelRouters.data.value && !tunnelRouters.data.value.length"
                    color="warning"
                    variant="subtle"
                    icon="i-lucide-triangle-alert"
                    :title="t('switches.onboarding.mode.noRouters')"
                    data-testid="switch-no-routers"
                  />
                  <ErrorState
                    v-else-if="tunnelRouters.error.value"
                    :error="tunnelRouters.error.value"
                    @retry="tunnelRouters.refresh()"
                  />
                  <UFormField
                    v-else
                    :label="t('switches.onboarding.mode.router')"
                    :help="t('switches.onboarding.mode.routerHelp')"
                    :error="routerError ?? undefined"
                    required
                  >
                    <USelectMenu
                      v-model="viaRouterId"
                      :items="routerItems"
                      value-key="value"
                      class="w-full sm:max-w-md"
                      :loading="tunnelRouters.status.value === 'pending'"
                      data-testid="switch-via-router"
                    />
                  </UFormField>
                </template>
                <div v-if="canWrite" class="grid gap-3 sm:grid-cols-2">
                  <UFormField
                    :label="t('switches.onboarding.generate.address')"
                    :help="t('switches.onboarding.generate.addressHelp')"
                    :error="addressError ?? undefined"
                    required
                  >
                    <UInput
                      v-model="address"
                      class="w-full"
                      :ui="{ base: 'font-mono' }"
                      autocomplete="off"
                      spellcheck="false"
                      placeholder="10.20.0.5"
                      data-testid="switch-address"
                    />
                  </UFormField>
                  <div v-if="sflowSupported(platform)" class="flex flex-col gap-3">
                    <USwitch
                      v-model="sflow"
                      :label="t('switches.onboarding.generate.sflow')"
                      :description="t('switches.onboarding.generate.sflowHelp')"
                      data-testid="switch-sflow"
                    />
                    <UFormField v-if="sflow" :label="t('switches.onboarding.generate.sampling')">
                      <UInput
                        v-model="sampling"
                        class="w-full"
                        inputmode="numeric"
                        :ui="{ base: 'font-mono' }"
                        data-testid="switch-sampling"
                      />
                    </UFormField>
                  </div>
                </div>
                <template v-if="result">
                  <UAlert
                    color="warning"
                    variant="subtle"
                    icon="i-lucide-eye-off"
                    :title="t('switches.onboarding.generate.onceTitle')"
                    :description="t('switches.onboarding.generate.onceBody')"
                    data-testid="switch-once"
                  />
                  <UAlert
                    v-for="w in result.warnings"
                    :key="w"
                    color="warning"
                    variant="subtle"
                    icon="i-lucide-triangle-alert"
                    :title="w"
                  />
                  <UAlert
                    color="info"
                    variant="subtle"
                    icon="i-lucide-list-checks"
                    :title="t('switches.onboarding.generate.notes')"
                  >
                    <template #description>
                      <ul class="list-disc space-y-1 ps-4" data-testid="switch-notes">
                        <li v-for="n in result.notes" :key="n">{{ n }}</li>
                      </ul>
                    </template>
                  </UAlert>
                  <UAlert
                    v-if="result.placeholders.length"
                    color="neutral"
                    variant="subtle"
                    icon="i-lucide-pencil-line"
                    :title="t('switches.onboarding.generate.placeholders')"
                  >
                    <template #description>
                      <ul class="list-disc space-y-1 ps-4">
                        <li v-for="p in result.placeholders" :key="p.key">
                          <code class="font-mono">{{ p.key }}</code> — {{ p.description }}
                        </li>
                      </ul>
                    </template>
                  </UAlert>
                  <CodeBlock
                    :code="result.config"
                    :label="t('switches.onboarding.generate.configLabel')"
                    :filename="`horus-switch-${router.name}.txt`"
                    max-height="20rem"
                    data-testid="switch-config"
                  />
                  <SwitchViaPath
                    v-if="result.path"
                    :path="result.path"
                    :private-address="result.management_address"
                  />
                  <template v-if="result.router_config">
                    <h4 class="text-highlighted text-sm font-semibold">
                      {{ t('switches.onboarding.router.title') }}
                    </h4>
                    <p class="text-muted text-sm">{{ t('switches.onboarding.router.body') }}</p>
                    <UAlert
                      v-for="w in result.router_config.warnings"
                      :key="w"
                      color="warning"
                      variant="subtle"
                      icon="i-lucide-lock-open"
                      :title="w"
                    />
                    <CodeBlock
                      :code="result.router_config.config"
                      :label="
                        t('switches.onboarding.router.label', {
                          name: result.path?.router_name ?? '',
                        })
                      "
                      :filename="`horus-via-${router.name}.${result.router_config.language === 'vrp' ? 'txt' : 'rsc'}`"
                      max-height="20rem"
                      data-testid="switch-router-config"
                    />
                    <UAlert
                      color="info"
                      variant="subtle"
                      icon="i-lucide-list-checks"
                      :title="t('switches.onboarding.router.notes')"
                    >
                      <template #description>
                        <ul class="list-disc space-y-1 ps-4" data-testid="switch-router-notes">
                          <li v-for="n in result.router_config.notes" :key="n">{{ n }}</li>
                        </ul>
                      </template>
                    </UAlert>
                  </template>
                  <CodeBlock
                    v-if="result.sflow_config"
                    :code="result.sflow_config"
                    :label="t('switches.onboarding.generate.sflowLabel')"
                    :filename="`horus-sflow-${router.name}.txt`"
                    max-height="14rem"
                    data-testid="switch-sflow-config"
                  />
                </template>
                <UAlert
                  v-if="genErrorText"
                  color="warning"
                  variant="subtle"
                  icon="i-lucide-triangle-alert"
                  :title="genErrorText"
                />
                <ErrorState v-else-if="genError" :error="genError" @retry="generate" />
                <div v-if="canWrite" class="flex flex-wrap gap-2">
                  <UButton
                    :icon="result || verified ? 'i-lucide-refresh-cw' : 'i-lucide-file-code-2'"
                    :color="result || verified ? 'neutral' : 'primary'"
                    :variant="result || verified ? 'outline' : 'solid'"
                    :loading="generating"
                    :label="
                      result || verified
                        ? t('switches.onboarding.generate.regenerate')
                        : t('switches.onboarding.generate.action')
                    "
                    data-testid="switch-generate"
                    @click="generate"
                  />
                </div>
                <p v-else class="text-muted text-sm">
                  {{ t('switches.onboarding.generate.noPermission') }}
                </p>
              </template>

              <template v-else-if="step.key === 'paste'">
                <p class="text-default text-sm">{{ t('switches.onboarding.paste.body') }}</p>
                <p
                  v-if="verified"
                  class="text-success flex items-center gap-1.5 text-sm font-medium"
                  data-testid="switch-verified"
                >
                  <UIcon name="i-lucide-shield-check" class="size-4" aria-hidden="true" />
                  {{ t('switches.onboarding.paste.verified', { ip: shownAddress }) }}
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
                  {{ t('switches.onboarding.paste.waiting') }}
                </p>
              </template>

              <template v-else>
                <ul class="flex flex-col gap-2" data-testid="switch-checks">
                  <li
                    v-for="c in [
                      { key: 'snmp', done: verified },
                      { key: 'ports', done: verified && portsDiscovered },
                    ]"
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
                      {{ t(`switches.onboarding.verify.${c.key}`) }}
                      <span class="sr-only">{{
                        c.done ? t('onboarding.state.done') : t('onboarding.state.waiting')
                      }}</span>
                    </span>
                  </li>
                </ul>
                <UCollapsible v-if="!verified" class="flex flex-col gap-2">
                  <UButton
                    color="neutral"
                    variant="link"
                    size="sm"
                    icon="i-lucide-life-buoy"
                    :label="t('switches.onboarding.verify.stuck')"
                    class="self-start px-0"
                  />
                  <template #content>
                    <ul class="text-muted list-disc space-y-1 ps-5 text-sm">
                      <li v-if="mode === 'via_router'">
                        {{ t('switches.onboarding.verify.checkRoute') }}
                      </li>
                      <li v-else>{{ t('switches.onboarding.verify.checkPath') }}</li>
                      <li>{{ t('switches.onboarding.verify.checkAcl') }}</li>
                      <li>{{ t('switches.onboarding.verify.checkCredential') }}</li>
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
