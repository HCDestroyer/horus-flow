<script setup lang="ts">
import type { SwitchPorts } from '~~/types/api'

/**
 * Ficha de un switch: estado y salud (CPU, memoria, temperatura, encendido; null = "—"; SwOS sin
 * CPU/memoria lo explica), asistente de conexión mientras no está verificado (o con
 * `?onboard=1`), puertos con elección, uplinks, tendencia en vivo, errores, flaps y vecinos LLDP,
 * vecinos y top por puerto a partir del sFlow (fuente secundaria). Sondeo cada 60 s (cada 3 s
 * mientras se verifica la conexión).
 */
const { t } = useI18n()
const route = useRoute()
const { $api } = useNuxtApp()
const slug = computed(() => String(route.params.slug))
const id = computed(() => String(route.params.switchId))

useSectionGuard()(findSection('tenant', 'switches'), slug.value)
const { siteName } = useSites()

const router = useTenantQuery(
  () => `switch:${id.value}`,
  () => unwrap($api.GET('/routers/{router_id}', { params: { path: { router_id: id.value } } })),
  { watch: [id] },
)
const status = useTenantQuery(
  () => `switch:${id.value}:status`,
  () =>
    unwrap($api.GET('/switches/status')).then(
      (r) => r.data.find((s) => s.router_id === id.value) ?? null,
    ),
  { watch: [id] },
)
const verified = computed(
  () => !!router.data.value && router.data.value.onboarding_state !== 'pending_configuration',
)
const ports = useTenantQuery(
  () => `switch:${id.value}:ports`,
  () =>
    verified.value
      ? unwrap(
          $api.GET('/switches/{router_id}/ports', { params: { path: { router_id: id.value } } }),
        )
      : Promise.resolve(null),
  { watch: [id, verified] },
)
useAutoRefresh(() => refreshAll(), 60_000)
function refreshAll() {
  router.refresh()
  status.refresh()
  ports.refresh()
}

// Tendencia en vivo por puerto: un punto por lectura de /ports (última muestra).
const trends = ref<Record<string, { rx: (number | null)[]; tx: (number | null)[] }>>({})
let lastSampleAt = ''
watch(
  () => ports.data.value,
  (p) => {
    if (!p) return
    const at = p.data.map((x) => x.sample?.rx_bps ?? '').join(',') + (p.discovered_at ?? '')
    if (at === lastSampleAt) return
    lastSampleAt = at
    const next = { ...trends.value }
    for (const x of p.data) {
      if (!x.monitored) continue
      const cur = next[x.name] ?? { rx: [], tx: [] }
      next[x.name] = {
        rx: [...cur.rx, x.sample?.rx_bps ?? null].slice(-PORT_TREND_POINTS),
        tx: [...cur.tx, x.sample?.tx_bps ?? null].slice(-PORT_TREND_POINTS),
      }
    }
    trends.value = next
  },
  { immediate: true },
)
function onSaved(p: SwitchPorts) {
  ports.data.value = p
  ports.refresh()
  status.refresh()
}

const showOnboarding = ref(route.query.onboard === '1')
const onboarding = computed(() => showOnboarding.value || !verified.value)
const platform = computed(() => router.data.value?.switch_platform ?? null)
const viaPath = computed(() => (router.data.value ? viaPathOf(router.data.value) : null))
const pct = (v: number | null | undefined) =>
  v === null || v === undefined ? '—' : `${formatNumber(v)} %`
const uptime = (s: number | null | undefined) =>
  s === null || s === undefined
    ? '—'
    : `${Math.floor(s / 86400)} d ${Math.floor((s % 86400) / 3600)} h`

useHead({ title: () => router.data.value?.name ?? t('switches.title') })
</script>

<template>
  <AppPage :title="router.data.value?.name ?? t('switches.title')" panel-id="switch-detail">
    <template #toolbar-left>
      <UButton
        color="neutral"
        variant="ghost"
        icon="i-lucide-arrow-left"
        :label="t('switches.detail.back')"
        :to="`/t/${slug}/switches`"
      />
    </template>
    <template v-if="verified" #toolbar-right>
      <UButton
        color="neutral"
        variant="outline"
        icon="i-lucide-file-code-2"
        :label="t('switches.detail.regenerate')"
        data-testid="switch-regenerate"
        @click="showOnboarding = !showOnboarding"
      />
    </template>

    <LoadingState v-if="router.status.value === 'pending' && !router.data.value" :rows="5" />
    <ErrorState
      v-else-if="router.error.value"
      :error="router.error.value"
      @retry="router.refresh()"
    />
    <div v-else-if="router.data.value" class="flex flex-col gap-4" data-testid="switch-detail">
      <div class="flex flex-wrap items-center gap-2 text-sm">
        <UBadge
          color="neutral"
          variant="subtle"
          icon="i-lucide-network"
          :label="t('switches.badge')"
        />
        <span class="flex items-center gap-1.5">
          <UIcon
            :name="PLATFORM_ICON[(platform ?? 'routeros') as SwitchPlatform]"
            class="size-4"
            aria-hidden="true"
          />
          {{ t(`switches.platform.${platform ?? 'routeros'}`) }}
        </span>
        <span class="text-muted">· {{ siteName(router.data.value.site_id) }}</span>
        <span v-if="router.data.value.management_address" class="text-muted font-mono"
          >· {{ router.data.value.management_address }}</span
        >
        <span
          v-if="router.data.value.connection_mode === 'direct'"
          class="text-muted"
          data-testid="switch-direct-path"
          >· {{ t('switches.path.direct') }}</span
        >
        <RouterStatusBadge
          v-if="status.data.value && verified"
          :status="status.data.value.status"
          :reason="status.data.value.reason"
          :since="status.data.value.since"
          data-testid="switch-status"
        />
      </div>

      <SwitchViaPath
        v-if="viaPath"
        data-testid="switch-detail-path"
        :path="viaPath"
        :private-address="router.data.value.management_address"
        :tunnel-down="status.data.value?.reason === 'via_router_tunnel_down'"
      />

      <SwitchOnboarding
        v-if="onboarding"
        :router="router.data.value"
        :ports-discovered="!!ports.data.value?.data.length"
        @refresh="refreshAll"
      />

      <template v-if="verified">
        <UCard data-testid="switch-health">
          <template #header>
            <h2 class="text-highlighted font-semibold">{{ t('switches.detail.health') }}</h2>
          </template>
          <dl class="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-6">
            <div
              v-for="m in [
                { k: 'cpu', v: pct(status.data.value?.device.cpu_percent) },
                { k: 'memory', v: pct(status.data.value?.device.memory_percent) },
                {
                  k: 'temperature',
                  v:
                    status.data.value?.device.temperature_celsius == null
                      ? '—'
                      : `${formatNumber(status.data.value.device.temperature_celsius, 1)} °C`,
                },
                { k: 'uptime', v: uptime(status.data.value?.device.uptime_seconds) },
                { k: 'model', v: status.data.value?.device.model ?? '—' },
                { k: 'firmware', v: status.data.value?.device.firmware ?? '—' },
              ]"
              :key="m.k"
              class="flex flex-col gap-0.5"
            >
              <dt class="text-muted text-xs">{{ t(`switches.detail.${m.k}`) }}</dt>
              <dd class="text-highlighted font-medium tabular" :data-metric="m.k">{{ m.v }}</dd>
            </div>
          </dl>
          <p
            v-if="isV2cOnly(platform)"
            class="text-muted mt-3 text-sm"
            data-testid="switch-limited-mibs"
          >
            {{ t('switches.detail.limitedMibs') }}
          </p>
        </UCard>

        <LoadingState v-if="ports.status.value === 'pending' && !ports.data.value" :rows="5" />
        <ErrorState
          v-else-if="ports.error.value"
          :error="ports.error.value"
          @retry="ports.refresh()"
        />
        <SwitchPortsTable
          v-else-if="ports.data.value"
          :switch-id="id"
          :ports="ports.data.value"
          :trends="trends"
          @saved="onSaved"
        />

        <UCard data-testid="switch-neighbors">
          <template #header>
            <h2 class="text-highlighted font-semibold">{{ t('switches.neighbors.title') }}</h2>
          </template>
          <p
            v-if="!ports.data.value?.data.some((p) => p.neighbors.length)"
            class="text-muted text-sm"
          >
            {{ t('switches.neighbors.empty') }}
          </p>
          <ul v-else class="flex flex-col divide-y divide-(--ui-border)">
            <template v-for="p in ports.data.value?.data ?? []" :key="p.name">
              <li
                v-for="n in p.neighbors"
                :key="p.name + n.sys_name"
                class="flex flex-wrap items-baseline gap-x-3 gap-y-1 py-2 text-sm"
              >
                <span class="text-highlighted font-mono">{{ p.name }}</span>
                <UIcon
                  name="i-lucide-arrow-right"
                  class="text-dimmed size-3.5"
                  aria-hidden="true"
                />
                <span class="text-default">{{ n.sys_name }}</span>
                <span class="text-muted font-mono">{{ n.port_id }}</span>
                <span v-if="n.mgmt_address" class="text-muted font-mono">{{ n.mgmt_address }}</span>
                <UBadge
                  v-if="n.known_device"
                  color="primary"
                  variant="subtle"
                  size="sm"
                  icon="i-lucide-link"
                  :label="t('switches.ports.knownDevice', { name: n.known_device })"
                />
              </li>
            </template>
          </ul>
        </UCard>

        <SwitchFlowTop
          :switch-id="id"
          :platform="platform"
          :sflow-enabled="router.data.value.sflow_enabled"
          :ports="ports.data.value?.data ?? []"
        />
      </template>
    </div>
  </AppPage>
</template>
