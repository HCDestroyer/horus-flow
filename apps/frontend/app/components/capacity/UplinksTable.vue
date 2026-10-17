<script setup lang="ts">
import type { CapacitySettingsValues, CapacityUplink } from '~~/types/api'

/**
 * Enlaces de subida de un nodo: utilización ahora y p95 de la ventana sobre la capacidad
 * (contratada o, si no, del puerto), horas sobre los umbrales de saturación, pérdida /
 * latencia / jitter de la sonda de su puerta de enlace, diagnóstico (lleno o averiado) y el
 * pronóstico con su recomendación. Peor p95 primero. En pantallas estrechas se ocultan las
 * columnas secundarias (el detalle del enlace lo tiene todo).
 */
const props = defineProps<{
  uplinks: CapacityUplink[]
  settings: CapacitySettingsValues
  manage: boolean
  slug: string
}>()
const emit = defineEmits<{ configure: [CapacityUplink] }>()
const { t, te } = useI18n()

const rows = computed(() => sortUplinks(props.uplinks))
const levelClass = (u: number | null | undefined) =>
  ({ ok: '', warning: 'text-warning font-semibold', critical: 'text-error font-semibold' })[
    utilLevel(u, props.settings.saturation_warning, props.settings.saturation_critical)
  ]
const bps = (v: number | null | undefined) => joinUnit(formatBps(v))
const hours = (v: number) => `${formatNumber(v, v < 10 ? 1 : 0)} h`
const diagnosis = (k: string) => (te(`capacity.diagnosis.${k}`) ? t(`capacity.diagnosis.${k}`) : k)
const crossing = (u: CapacityUplink) => {
  const f = u.forecast
  if (!f || (f.level !== 'planning' && f.level !== 'urgent')) return null
  const c = f.level === 'urgent' ? f.urgent : f.planning
  if (c.already) return t('capacity.forecast.already', { pct: formatPercent(c.threshold) })
  if (!c.date) return null
  return t('capacity.forecast.crossesOn', {
    pct: formatPercent(c.threshold),
    date: formatMonth(c.date),
  })
}
</script>

<template>
  <div class="ring-default overflow-x-auto rounded-md ring-1">
    <table class="w-full text-sm" data-testid="uplinks-table">
      <caption class="sr-only">
        {{
          t('capacity.table.caption')
        }}
      </caption>
      <thead class="bg-elevated/50 text-muted text-left text-xs">
        <tr>
          <th scope="col" class="px-3 py-2 font-medium">{{ t('capacity.table.uplink') }}</th>
          <th scope="col" class="px-3 py-2 text-right font-medium">
            {{ t('capacity.table.now') }}
          </th>
          <th scope="col" class="px-3 py-2 text-right font-medium">
            {{ t('capacity.table.p95') }}
          </th>
          <th scope="col" class="hidden px-3 py-2 text-right font-medium lg:table-cell">
            {{
              t('capacity.table.hours', {
                warn: formatPercent(settings.saturation_warning),
                crit: formatPercent(settings.saturation_critical),
              })
            }}
          </th>
          <th scope="col" class="hidden px-3 py-2 text-right font-medium md:table-cell">
            {{ t('capacity.table.quality') }}
          </th>
          <th scope="col" class="hidden px-3 py-2 text-right font-medium sm:table-cell">
            {{ t('capacity.table.capacity') }}
          </th>
          <th scope="col" class="hidden px-3 py-2 font-medium xl:table-cell">
            {{ t('capacity.table.forecast') }}
          </th>
          <th scope="col" class="px-3 py-2">
            <span class="sr-only">{{ t('capacity.table.actions') }}</span>
          </th>
        </tr>
      </thead>
      <tbody class="divide-default divide-y">
        <tr
          v-for="u in rows"
          :key="u.interface_id"
          data-testid="uplink-row"
          :data-interface="u.interface_name"
        >
          <td class="px-3 py-2">
            <NuxtLink
              :to="`/t/${slug}/capacity/uplinks/${u.interface_id}`"
              class="text-highlighted font-medium hover:underline"
              data-testid="uplink-link"
            >
              {{ u.label }}
            </NuxtLink>
            <p class="text-muted flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
              <span class="font-mono">{{ u.interface_name }}</span>
              <span>{{ u.router_name }}</span>
              <UBadge
                v-if="u.kind"
                color="neutral"
                variant="soft"
                size="sm"
                :label="t(`capacity.kind.${u.kind}`)"
              />
              <UBadge
                v-if="u.international_egress"
                color="info"
                variant="outline"
                size="sm"
                icon="i-lucide-globe"
                :label="t('capacity.egressBadge')"
              />
              <UBadge
                v-if="!u.configured"
                color="neutral"
                variant="outline"
                size="sm"
                :label="t('capacity.detectedOnly')"
              />
              <UBadge
                v-if="u.oper_status && u.oper_status !== 'up'"
                color="error"
                variant="subtle"
                size="sm"
                icon="i-lucide-circle-x"
                :label="t('capacity.down')"
              />
              <UBadge
                v-if="u.diagnosis.kind === 'faulty' || u.diagnosis.kind === 'full_and_faulty'"
                color="warning"
                variant="subtle"
                size="sm"
                icon="i-lucide-triangle-alert"
                :label="diagnosis(u.diagnosis.kind)"
                data-testid="uplink-faulty"
              />
            </p>
          </td>
          <td
            class="tabular px-3 py-2 text-right"
            :class="levelClass(u.utilization)"
            data-testid="uplink-now"
          >
            {{ formatPercent(u.utilization) }}
            <p class="text-muted text-xs font-normal">↓ {{ bps(u.rx_bps) }}</p>
          </td>
          <td
            class="tabular px-3 py-2 text-right"
            :class="levelClass(u.window?.util_p95)"
            data-testid="uplink-p95"
          >
            {{ formatPercent(u.window?.util_p95) }}
            <p class="text-muted text-xs font-normal">{{ diagnosis(u.diagnosis.kind) }}</p>
          </td>
          <td class="tabular hidden px-3 py-2 text-right lg:table-cell">
            <template v-if="u.window">
              {{ hours(u.window.hours_above_warning) }} · {{ hours(u.window.hours_above_critical) }}
            </template>
            <template v-else>—</template>
          </td>
          <td
            class="tabular hidden px-3 py-2 text-right md:table-cell"
            data-testid="uplink-quality"
          >
            <template v-if="u.probe">
              <span
                :class="
                  u.probe.loss_pct != null && u.probe.loss_pct >= settings.loss_warning_pct
                    ? 'text-warning font-semibold'
                    : ''
                "
              >
                {{ u.probe.loss_pct != null ? `${formatNumber(u.probe.loss_pct, 1)} %` : '—' }}
              </span>
              <p class="text-muted text-xs">
                {{ formatMs(u.probe.rtt_avg_ms) }} · ± {{ formatMs(u.probe.jitter_ms) }}
              </p>
            </template>
            <span v-else class="text-muted text-xs">{{ t('capacity.noProbe') }}</span>
          </td>
          <td class="tabular hidden px-3 py-2 text-right sm:table-cell">
            {{ bps(u.capacity_bps) }}
            <p class="text-muted text-xs">
              {{ t(`capacity.capacitySource.${u.capacity_source}`) }}
            </p>
          </td>
          <td class="hidden max-w-80 px-3 py-2 xl:table-cell">
            <template v-if="u.forecast">
              <ForecastLevelBadge :level="u.forecast.level" />
              <p class="text-muted mt-0.5 text-xs">
                <span v-if="u.forecast.monthly_growth != null">{{
                  t('capacity.forecast.growth', { g: formatSigned(u.forecast.monthly_growth) })
                }}</span>
                <span v-if="crossing(u)"> · {{ crossing(u) }}</span>
              </p>
            </template>
            <span v-else class="text-muted text-xs">{{ t('capacity.forecast.none') }}</span>
          </td>
          <td class="px-3 py-2">
            <span class="flex justify-end gap-1">
              <UButton
                v-if="manage"
                size="sm"
                color="neutral"
                variant="ghost"
                icon="i-lucide-settings-2"
                :aria-label="t('capacity.actions.configure', { name: u.label })"
                data-testid="uplink-configure"
                @click="emit('configure', u)"
              />
            </span>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
