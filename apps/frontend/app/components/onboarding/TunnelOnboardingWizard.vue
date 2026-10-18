<script setup lang="ts">
import type { FlowExporter, Peer, Router, TunnelConfig } from '~~/types/api'

/**
 * Conexión de un router por IPsec IKEv2 o GRE en tres pasos (docs/tunnels.md §5):
 * 1. Generar la configuración (`POST /routers/{id}/tunnel-config`, `x-reauth`): PSK nueva (IPsec)
 *    y credenciales SNMP nuevas que se muestran UNA vez; configuración del fabricante y ejemplo
 *    genérico Linux/strongSwan.
 * 2. Pegar en el router y esperar el túnel: IKE SA establecida (IPsec) o primer paquete (GRE).
 * 3. Verificación: túnel arriba → primer flujo → primeros clientes (sondeo cada 3 s).
 * Horus nunca escribe en el router.
 */
const props = defineProps<{
  router: Router
  peer: Peer | null
  exporter: FlowExporter | null
  activeCustomers: number
  discovery: boolean
}>()
const emit = defineEmits<{ refresh: [] }>()

const { t } = useI18n()
const { $api } = useNuxtApp()
const can = useCan()
const { withReauth } = useReauth()
const canWrite = computed(() => can('wireguard.write'))

const result = ref<TunnelConfig | null>(null)
const generating = ref(false)
const genError = shallowRef<unknown>(null)
const platform = ref('0')

const transport = computed(() => (props.router.connection_mode === 'gre' ? 'gre' : 'ipsec'))
const ORDER = ['pending_configuration', 'tunnel_up', 'exporting'] as const
const stage = computed(() =>
  Math.max(0, ORDER.indexOf(props.router.onboarding_state as (typeof ORDER)[number])),
)
const tunnel = computed(() =>
  props.peer?.transport === transport.value ? props.peer.tunnel : null,
)

async function generate() {
  generating.value = true
  genError.value = null
  try {
    result.value = await withReauth(
      () =>
        unwrap(
          $api.POST('/routers/{router_id}/tunnel-config', {
            params: { path: { router_id: props.router.id } },
            body: {},
          }),
        ),
      t('tunnelOnboarding.generate.reauth'),
    )
    platform.value = '0'
    emit('refresh')
  } catch (e) {
    genError.value = e
  } finally {
    generating.value = false
  }
}

const genErrorText = computed(() => {
  const e = genError.value
  if (isReauthRequired(e)) return t('tunnelOnboarding.generate.reauthNeeded')
  if (e instanceof ApiError && e.code === 'TUNNEL_TRANSPORT_DISABLED')
    return t('tunnelOnboarding.generate.disabled', { mode: t(`transport.mode.${transport.value}`) })
  return null
})
const tabs = computed(() =>
  (result.value?.configs ?? []).map((c, i) => ({ label: c.title, value: String(i) })),
)
const current = computed(() => result.value?.configs[Number(platform.value)] ?? null)
const ext = (lang: string) => (lang === 'routeros' ? 'rsc' : lang === 'shell' ? 'sh' : 'txt')

type StepState = 'done' | 'current' | 'waiting'
const steps = computed<{ key: string; state: StepState }[]>(() => {
  const generated = !!result.value || props.peer?.status === 'pending_handshake' || stage.value > 0
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
  { key: 'tunnel', done: stage.value >= 1, at: props.peer?.last_handshake_at ?? null },
  { key: 'flow', done: stage.value >= 2, at: props.exporter?.last_flow_at ?? null },
  { key: 'customers', done: props.activeCustomers > 0, at: null },
])
const STEP_ICON: Record<StepState, string> = {
  done: 'i-lucide-circle-check',
  current: 'i-lucide-circle-dot',
  waiting: 'i-lucide-circle',
}

let timer: ReturnType<typeof setInterval> | undefined
watchEffect(() => {
  clearInterval(timer)
  const finished = stage.value >= 2 && (props.activeCustomers > 0 || props.discovery)
  const started = !!result.value || stage.value > 0 || props.peer?.status === 'pending_handshake'
  if (started && !finished) timer = setInterval(() => emit('refresh'), 3000)
})
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="tunnel-onboarding" :data-transport="transport">
    <UAlert
      v-if="transport === 'gre'"
      :color="router.protection_level === 'unencrypted_internet' ? 'error' : 'warning'"
      variant="subtle"
      icon="i-lucide-lock-open"
      :title="t('tunnelOnboarding.greWarningTitle')"
      :description="t('tunnelOnboarding.greWarningBody')"
      data-testid="tunnel-gre-warning"
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
                {{ t(`tunnelOnboarding.${step.key}.title.${transport}`) }}
                <span class="sr-only">({{ t(`onboarding.state.${step.state}`) }})</span>
              </h3>

              <template v-if="step.key === 'generate'">
                <p class="text-muted text-sm">
                  {{ t(`tunnelOnboarding.generate.body.${transport}`) }}
                </p>
                <template v-if="result">
                  <UAlert
                    color="warning"
                    variant="subtle"
                    icon="i-lucide-eye-off"
                    :title="t('tunnelOnboarding.generate.onceTitle')"
                    :description="t(`tunnelOnboarding.generate.onceBody.${transport}`)"
                    data-testid="tunnel-once"
                  />
                  <dl
                    class="grid gap-x-4 gap-y-1 text-sm sm:grid-cols-[auto_1fr]"
                    data-testid="tunnel-summary"
                  >
                    <dt class="text-muted">{{ t('tunnelOnboarding.tunnelAddress') }}</dt>
                    <dd class="font-mono">{{ result.tunnel_address }}</dd>
                    <dt class="text-muted">{{ t('tunnelOnboarding.hub') }}</dt>
                    <dd class="font-mono">{{ result.hub_address ?? '—' }}</dd>
                    <template v-if="result.hub_id">
                      <dt class="text-muted">{{ t('tunnelOnboarding.hubId') }}</dt>
                      <dd class="font-mono">{{ result.hub_id }}</dd>
                    </template>
                    <template v-if="result.remote_id">
                      <dt class="text-muted">{{ t('tunnelOnboarding.routerId') }}</dt>
                      <dd class="font-mono">{{ result.remote_id }}</dd>
                    </template>
                    <template v-if="result.gre_key">
                      <dt class="text-muted">{{ t('tunnelOnboarding.greKey') }}</dt>
                      <dd class="font-mono">{{ result.gre_key }}</dd>
                    </template>
                  </dl>
                  <CodeBlock
                    v-if="result.psk"
                    :code="result.psk"
                    :label="t('tunnelOnboarding.psk')"
                    data-testid="tunnel-psk"
                  />
                  <UAlert
                    v-for="w in result.warnings"
                    :key="w"
                    color="error"
                    variant="subtle"
                    icon="i-lucide-shield-alert"
                    :title="w"
                  />
                  <UTabs v-model="platform" :items="tabs" :content="false" variant="link" />
                  <template v-if="current">
                    <UAlert
                      color="info"
                      variant="subtle"
                      icon="i-lucide-list-checks"
                      :title="t('tunnelOnboarding.notes')"
                    >
                      <template #description>
                        <ul class="list-disc space-y-1 ps-4" data-testid="tunnel-notes">
                          <li v-for="n in current.notes" :key="n">{{ n }}</li>
                        </ul>
                      </template>
                    </UAlert>
                    <UAlert
                      v-if="current.placeholders.length"
                      color="neutral"
                      variant="subtle"
                      icon="i-lucide-pencil-line"
                      :title="t('tunnelOnboarding.placeholders')"
                    >
                      <template #description>
                        <ul class="list-disc space-y-1 ps-4">
                          <li v-for="p in current.placeholders" :key="p.key">
                            <code class="font-mono">{{ p.key }}</code> — {{ p.description }}
                          </li>
                        </ul>
                      </template>
                    </UAlert>
                    <CodeBlock
                      :code="current.config"
                      :label="current.title"
                      :filename="`horus-${transport}-${router.name}.${ext(current.language)}`"
                      max-height="20rem"
                      :data-platform="current.platform"
                      data-testid="tunnel-config"
                    />
                  </template>
                </template>
                <UAlert
                  v-if="genErrorText"
                  color="warning"
                  variant="subtle"
                  icon="i-lucide-triangle-alert"
                  :title="genErrorText"
                  data-testid="tunnel-blocked"
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
                        ? t(`tunnelOnboarding.generate.regenerate.${transport}`)
                        : t('tunnelOnboarding.generate.action')
                    "
                    data-testid="tunnel-generate"
                    @click="generate"
                  />
                </div>
                <p v-else class="text-muted text-sm">
                  {{ t('tunnelOnboarding.generate.noPermission') }}
                </p>
              </template>

              <template v-else-if="step.key === 'paste'">
                <p class="text-default text-sm">
                  {{ t(`tunnelOnboarding.paste.body.${transport}`) }}
                </p>
                <dl
                  v-if="tunnel"
                  class="text-muted grid gap-x-4 gap-y-1 text-sm sm:grid-cols-[auto_1fr]"
                  data-testid="tunnel-status"
                >
                  <dt>{{ t('tunnelOnboarding.status.state') }}</dt>
                  <dd class="text-highlighted" :data-state="tunnel.state ?? 'none'">
                    {{
                      tunnel.state
                        ? t(`tunnelOnboarding.states.${tunnel.state}`)
                        : t('tunnelOnboarding.states.none')
                    }}
                  </dd>
                  <template v-if="tunnel.last_rekey_at">
                    <dt>{{ t('tunnelOnboarding.status.lastRekey') }}</dt>
                    <dd><RelativeTime :at="tunnel.last_rekey_at" /></dd>
                  </template>
                  <template v-if="tunnel.last_packet_at">
                    <dt>{{ t('tunnelOnboarding.status.lastPacket') }}</dt>
                    <dd><RelativeTime :at="tunnel.last_packet_at" /></dd>
                  </template>
                  <template v-if="tunnel.error">
                    <dt>{{ t('tunnelOnboarding.status.error') }}</dt>
                    <dd class="text-error font-mono">{{ tunnel.error }}</dd>
                  </template>
                </dl>
                <p
                  v-if="stage > 0"
                  class="text-success flex items-center gap-1.5 text-sm font-medium"
                  data-testid="tunnel-up"
                >
                  <UIcon name="i-lucide-shield-check" class="size-4" aria-hidden="true" />
                  {{ t(`tunnelOnboarding.paste.up.${transport}`) }}
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
                  {{ t(`tunnelOnboarding.paste.waiting.${transport}`) }}
                </p>
              </template>

              <template v-else>
                <ul class="flex flex-col gap-2" data-testid="tunnel-checks">
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
                      {{ t(`tunnelOnboarding.verify.${c.key}`) }}
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
                <UCollapsible v-if="stage < 1" class="flex flex-col gap-2">
                  <UButton
                    color="neutral"
                    variant="link"
                    size="sm"
                    icon="i-lucide-life-buoy"
                    :label="t('tunnelOnboarding.verify.stuck')"
                    class="self-start px-0"
                  />
                  <template #content>
                    <ul class="text-muted list-disc space-y-1 ps-5 text-sm">
                      <li v-for="k in ['ports', 'identity', 'proposals', 'mtu']" :key="k">
                        {{ t(`tunnelOnboarding.verify.check.${transport}.${k}`) }}
                      </li>
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
