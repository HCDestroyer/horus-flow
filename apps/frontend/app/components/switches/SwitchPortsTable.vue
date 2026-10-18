<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'
import type { SwitchPort, SwitchPorts } from '~~/types/api'

/**
 * Puertos de un switch (`GET /switches/{id}/ports`): estado (icono + texto), tráfico rx/tx con
 * utilización, tendencia en vivo (un punto por sondeo), errores y descartes del último
 * intervalo, cambios de estado (1 h / 24 h) y vecino LLDP. Insignias "Subida" y "Subida
 * sugerida". Con `snmp.manage`, modo de edición: elegir qué puertos se monitorean (máximo 64)
 * y cuáles son de subida (`PUT /switches/{id}/ports` con `If-Match`), o volver a la elección
 * por defecto.
 */
const props = defineProps<{
  switchId: string
  ports: SwitchPorts
  trends: Record<string, { rx: (number | null)[]; tx: (number | null)[] }>
}>()
const emit = defineEmits<{ saved: [ports: SwitchPorts] }>()

const { t } = useI18n()
const { $api } = useNuxtApp()
const can = useCan()
const toast = useToast()
const canManage = computed(() => can('snmp.manage'))

const editing = ref(false)
const choice = ref<Record<string, { monitored: boolean; uplink: boolean }>>({})
const saving = ref(false)
const error = shallowRef<unknown>(null)

const rows = computed(() => sortPorts(props.ports.data))
const monitoredCount = computed(() =>
  editing.value
    ? Object.values(choice.value).filter((c) => c.monitored).length
    : props.ports.monitored_count,
)
const tooMany = computed(() => monitoredCount.value > props.ports.max_ports)

function startEdit() {
  choice.value = Object.fromEntries(
    props.ports.data.map((p) => [p.name, { monitored: p.monitored, uplink: p.uplink }]),
  )
  error.value = null
  editing.value = true
}
function setMonitored(p: SwitchPort, v: boolean) {
  const c = choice.value[p.name]!
  choice.value = { ...choice.value, [p.name]: { monitored: v, uplink: v ? c.uplink : false } }
}
function setUplink(p: SwitchPort, v: boolean) {
  const c = choice.value[p.name]!
  choice.value = { ...choice.value, [p.name]: { monitored: v ? true : c.monitored, uplink: v } }
}

async function save() {
  const body = selectionBody(choice.value)
  if (body.tooMany) return
  saving.value = true
  error.value = null
  try {
    const version = props.ports.selection.version
    const out = await unwrap(
      $api.PUT('/switches/{router_id}/ports', {
        params: {
          path: { router_id: props.switchId },
          header: version ? { 'If-Match': `"${version}"` } : {},
        },
        body: { ports: body.ports },
      }),
    )
    editing.value = false
    toast.add({ title: t('switches.ports.saved'), icon: 'i-lucide-check', color: 'success' })
    emit('saved', out)
  } catch (e) {
    if (e instanceof ApiError && e.code === 'PRECONDITION_FAILED') {
      toast.add({
        title: t('switches.ports.conflict'),
        icon: 'i-lucide-git-compare',
        color: 'warning',
      })
      editing.value = false
      emit('saved', props.ports)
      return
    }
    error.value = e
  } finally {
    saving.value = false
  }
}

async function reset() {
  saving.value = true
  error.value = null
  try {
    const out = await unwrap(
      $api.DELETE('/switches/{router_id}/ports', {
        params: { path: { router_id: props.switchId } },
      }),
    )
    editing.value = false
    emit('saved', out)
  } catch (e) {
    error.value = e
  } finally {
    saving.value = false
  }
}

const bps = (v: number | null | undefined) =>
  v === null || v === undefined ? '—' : joinUnit(formatBps(v))
const count = (v: number | null | undefined) =>
  v === null || v === undefined ? '—' : formatNumber(v)
const statusText = (p: SwitchPort) => {
  const v = portStatusVisual(p)
  return v.key === 'up' || v.key === 'down' || v.key === 'unknown'
    ? t(`router.interfaces.operStates.${v.key}`)
    : t(`switches.ports.${v.key}`)
}

const columns = computed<TableColumn<SwitchPort>[]>(() => [
  ...(editing.value
    ? [
        { id: 'monitor', header: () => t('switches.ports.columns.monitor') },
        { id: 'uplinkEdit', header: () => t('switches.ports.columns.uplink') },
      ]
    : []),
  { id: 'port', header: () => t('switches.ports.columns.port') },
  { id: 'status', header: () => t('switches.ports.columns.status') },
  {
    id: 'traffic',
    header: () => t('switches.ports.columns.traffic'),
    meta: { class: { th: 'text-end', td: 'text-end' } },
  },
  {
    id: 'utilization',
    header: () => t('switches.ports.columns.utilization'),
    meta: { class: { th: 'hidden sm:table-cell', td: 'hidden sm:table-cell' } },
  },
  {
    id: 'trend',
    header: () => t('switches.ports.columns.trend'),
    meta: { class: { th: 'hidden lg:table-cell', td: 'hidden lg:table-cell' } },
  },
  {
    id: 'errors',
    header: () => t('switches.ports.columns.errors'),
    meta: { class: { th: 'hidden text-end md:table-cell', td: 'hidden text-end md:table-cell' } },
  },
  {
    id: 'flaps',
    header: () => t('switches.ports.columns.flaps'),
    meta: { class: { th: 'hidden text-end md:table-cell', td: 'hidden text-end md:table-cell' } },
  },
  {
    id: 'neighbor',
    header: () => t('switches.ports.columns.neighbor'),
    meta: { class: { th: 'hidden xl:table-cell', td: 'hidden xl:table-cell' } },
  },
])
</script>

<template>
  <UCard :ui="{ header: 'p-4 sm:px-4', body: 'p-0 sm:p-0' }" data-testid="switch-ports">
    <template #header>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex min-w-0 flex-col gap-0.5">
          <h2 class="text-highlighted font-semibold">{{ t('switches.ports.title') }}</h2>
          <p class="text-muted text-sm tabular" data-testid="switch-ports-summary">
            {{ t('switches.ports.summary', { monitored: monitoredCount, max: ports.max_ports }) }} ·
            {{
              ports.selection.configured
                ? t('switches.ports.customSelection')
                : t('switches.ports.defaultSelection')
            }}
          </p>
        </div>
        <div v-if="canManage && ports.data.length" class="flex flex-wrap gap-2">
          <template v-if="editing">
            <UButton
              v-if="ports.selection.configured"
              color="neutral"
              variant="ghost"
              icon="i-lucide-rotate-ccw"
              :label="t('switches.ports.reset')"
              :loading="saving"
              data-testid="switch-ports-reset"
              @click="reset"
            />
            <UButton
              color="neutral"
              variant="outline"
              :label="t('switches.ports.cancel')"
              @click="editing = false"
            />
            <UButton
              icon="i-lucide-save"
              :label="t('switches.ports.save')"
              :loading="saving"
              :disabled="tooMany"
              data-testid="switch-ports-save"
              @click="save"
            />
          </template>
          <UButton
            v-else
            color="neutral"
            variant="outline"
            icon="i-lucide-list-checks"
            :label="t('switches.ports.edit')"
            data-testid="switch-ports-edit"
            @click="startEdit"
          />
        </div>
      </div>
      <UAlert
        v-if="editing && tooMany"
        class="mt-3"
        color="warning"
        variant="subtle"
        icon="i-lucide-triangle-alert"
        :title="t('switches.ports.tooMany', { max: ports.max_ports })"
        data-testid="switch-ports-too-many"
      />
      <ErrorState v-if="error" class="mt-3" :error="error" @retry="save" />
    </template>
    <EmptyState
      v-if="!ports.data.length"
      class="py-6"
      icon="i-lucide-ethernet-port"
      :title="t('switches.ports.empty')"
    />
    <UTable
      v-else
      :data="rows"
      :columns="columns"
      class="w-full"
      :ui="{ td: 'px-3 py-2 sm:px-4', th: 'px-3 text-xs whitespace-nowrap sm:px-4' }"
      data-testid="switch-ports-table"
      tabindex="0"
      :aria-label="t('switches.ports.title')"
    >
      <template #monitor-cell="{ row }">
        <UCheckbox
          :model-value="choice[row.original.name]?.monitored ?? false"
          :aria-label="t('switches.ports.monitorLabel', { port: row.original.name })"
          :data-port="row.original.name"
          data-testid="port-monitor"
          @update:model-value="(v) => setMonitored(row.original, v === true)"
        />
      </template>
      <template #uplinkEdit-cell="{ row }">
        <USwitch
          :model-value="choice[row.original.name]?.uplink ?? false"
          size="sm"
          :aria-label="t('switches.ports.uplinkLabel', { port: row.original.name })"
          :data-port="row.original.name"
          data-testid="port-uplink"
          @update:model-value="(v) => setUplink(row.original, v === true)"
        />
      </template>
      <template #port-cell="{ row }">
        <span
          class="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5"
          :data-port-row="row.original.name"
        >
          <span class="text-highlighted font-mono font-medium">{{ row.original.name }}</span>
          <span v-if="row.original.alias" class="text-muted text-xs">{{ row.original.alias }}</span>
          <UBadge
            v-if="row.original.uplink"
            color="primary"
            variant="subtle"
            size="sm"
            icon="i-lucide-arrow-up-from-line"
            :label="t('switches.ports.uplink')"
            data-testid="port-uplink-badge"
          />
          <UBadge
            v-else-if="row.original.suggested_uplink"
            color="neutral"
            variant="outline"
            size="sm"
            icon="i-lucide-lightbulb"
            :label="t('switches.ports.suggested')"
          />
          <UBadge
            v-if="!row.original.monitored"
            color="neutral"
            variant="subtle"
            size="sm"
            :label="t('switches.ports.notMonitored')"
          />
        </span>
      </template>
      <template #status-cell="{ row }">
        <span
          class="flex items-center gap-1.5 text-sm whitespace-nowrap"
          :class="portStatusVisual(row.original).cls"
        >
          <UIcon
            :name="portStatusVisual(row.original).icon"
            class="size-4 shrink-0"
            aria-hidden="true"
          />
          {{ statusText(row.original) }}
        </span>
      </template>
      <template #traffic-cell="{ row }">
        <span class="text-default text-sm whitespace-nowrap tabular">
          ↓ {{ bps(row.original.sample?.rx_bps) }} · ↑ {{ bps(row.original.sample?.tx_bps) }}
        </span>
      </template>
      <template #utilization-cell="{ row }">
        <div class="flex min-w-24 items-center gap-2">
          <UProgress
            :model-value="Math.min(100, (portUtilization(row.original) ?? 0) * 100)"
            size="sm"
            :color="
              (portUtilization(row.original) ?? 0) >= 0.85
                ? 'error'
                : (portUtilization(row.original) ?? 0) >= 0.7
                  ? 'warning'
                  : 'primary'
            "
            class="w-16"
          />
          <span class="text-muted text-xs tabular">{{
            formatPercent(portUtilization(row.original))
          }}</span>
        </div>
      </template>
      <template #trend-cell="{ row }">
        <div class="h-8 w-28">
          <Sparkline
            v-if="trends[row.original.name]?.rx.length"
            :series="[
              { name: '↓', values: trends[row.original.name]!.rx },
              { name: '↑', values: trends[row.original.name]!.tx },
            ]"
            :label="t('switches.ports.trendLabel', { port: row.original.name })"
          />
          <span v-else class="text-dimmed text-xs">—</span>
        </div>
      </template>
      <template #errors-cell="{ row }">
        <span
          class="text-sm tabular"
          :class="(row.original.sample?.rx_errors_delta ?? 0) > 0 ? 'text-warning' : 'text-muted'"
        >
          {{ count(row.original.sample?.rx_errors_delta) }} ·
          {{ count(row.original.sample?.rx_drops_delta) }}
        </span>
      </template>
      <template #flaps-cell="{ row }">
        <span
          class="text-sm tabular"
          :class="
            row.original.flaps_1h >= FLAP_THRESHOLD ? 'text-warning font-medium' : 'text-muted'
          "
        >
          {{ row.original.flaps_1h }} / {{ row.original.flaps_24h }}
        </span>
      </template>
      <template #neighbor-cell="{ row }">
        <span
          v-for="n in row.original.neighbors"
          :key="n.sys_name + n.port_id"
          class="flex flex-col text-sm"
        >
          <span class="text-default flex items-center gap-1">
            <UIcon
              v-if="n.known_device"
              name="i-lucide-link"
              class="text-primary size-3.5"
              :aria-label="t('switches.ports.knownDevice', { name: n.known_device })"
            />
            {{ n.sys_name }}
          </span>
          <span class="text-muted font-mono text-xs">{{ n.port_id }}</span>
        </span>
        <span v-if="!row.original.neighbors.length" class="text-dimmed text-xs">—</span>
      </template>
    </UTable>
  </UCard>
</template>
