<script setup lang="ts">
import type { Evidence } from '~~/types/api'

/**
 * Evidencia agregada del hallazgo (frontend.md §8.2): solo metadatos según el tipo, nunca
 * payloads ni la IP del cliente. Cada dato con su etiqueta; la serie de destinos por minuto
 * como barras con el valor escrito (no depende de hover).
 */
const props = defineProps<{ evidence: Evidence }>()
const { t, d } = useI18n()
const e = computed(() => props.evidence)

const perMinute = computed(() => e.value.destinations_per_minute ?? [])
const maxPerMinute = computed(() => Math.max(1, ...perMinute.value))

const facts = computed(() => {
  const list: { label: string; value: string; mono?: boolean }[] = []
  const v = e.value
  if (v.distinct_destinations !== undefined)
    list.push({
      label: t('findings.evidence.destinations'),
      value: formatNumber(v.distinct_destinations),
    })
  if (v.distinct_nets24 !== undefined)
    list.push({ label: t('findings.evidence.nets24'), value: formatNumber(v.distinct_nets24) })
  if (v.syn_ratio !== undefined)
    list.push({ label: t('findings.evidence.synRatio'), value: formatPercent(v.syn_ratio) })
  if (v.flows !== undefined)
    list.push({ label: t('findings.evidence.flows'), value: formatNumber(v.flows) })
  if (v.bytes_est !== undefined)
    list.push({
      label: t('findings.evidence.bytes'),
      value: joinUnit(formatBytes(Number(v.bytes_est))),
    })
  if (v.smtp_servers_per_hour !== undefined)
    list.push({
      label: t('findings.evidence.smtpPerHour'),
      value: formatNumber(v.smtp_servers_per_hour),
    })
  if (v.interval_seconds !== undefined)
    list.push({
      label: t('findings.evidence.interval'),
      value: `${formatNumber(v.interval_seconds)} s`,
    })
  if (v.interval_cv !== undefined)
    list.push({ label: t('findings.evidence.intervalCv'), value: formatNumber(v.interval_cv, 2) })
  if (v.duration_seconds !== undefined)
    list.push({ label: t('findings.evidence.duration'), value: duration(v.duration_seconds) })
  if (v.pps_peak !== undefined)
    list.push({ label: t('findings.evidence.ppsPeak'), value: `${formatNumber(v.pps_peak)} pps` })
  if (v.bps_peak !== undefined)
    list.push({ label: t('findings.evidence.bpsPeak'), value: joinUnit(formatBps(v.bps_peak)) })
  if (v.protocol)
    list.push({ label: t('findings.evidence.protocol'), value: v.protocol.toUpperCase() })
  if (v.target_prefix)
    list.push({ label: t('findings.evidence.target'), value: v.target_prefix, mono: true })
  if (v.destination_asns?.length)
    list.push({
      label: t('findings.evidence.asns'),
      value: v.destination_asns.map((a) => `AS${a}`).join(', '),
    })
  return list
})

function duration(s: number) {
  if (s < 60) return `${s} s`
  if (s < 3600) return `${Math.round(s / 60)} min`
  return `${formatNumber(s / 3600, 1)} h`
}
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="finding-evidence">
    <dl v-if="facts.length" class="grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-3 lg:grid-cols-4">
      <div v-for="f in facts" :key="f.label" class="min-w-0">
        <dt class="text-muted text-xs">{{ f.label }}</dt>
        <dd
          class="text-highlighted font-medium tabular break-words"
          :class="{ 'font-mono text-sm': f.mono }"
        >
          {{ f.value }}
        </dd>
      </div>
    </dl>

    <div v-if="e.destination_ports?.length" class="flex flex-wrap items-center gap-1.5">
      <span class="text-muted text-xs">{{ t('findings.evidence.ports') }}</span>
      <UBadge
        v-for="p in e.destination_ports"
        :key="p"
        color="neutral"
        variant="outline"
        class="font-mono"
        :label="String(p)"
      />
    </div>

    <figure v-if="perMinute.length" class="flex flex-col gap-1.5">
      <figcaption class="text-muted text-xs">{{ t('findings.evidence.perMinute') }}</figcaption>
      <ol class="flex h-20 items-end gap-1.5" :aria-label="t('findings.evidence.perMinute')">
        <li
          v-for="(v, i) in perMinute"
          :key="i"
          class="flex h-full min-w-0 flex-1 flex-col items-center justify-end gap-1"
        >
          <span class="text-muted text-[0.6875rem] tabular">{{ v }}</span>
          <span
            class="w-full rounded-t-sm bg-(--viz-1)"
            :style="{ height: `${Math.max(4, (v / maxPerMinute) * 100)}%` }"
            aria-hidden="true"
          />
        </li>
      </ol>
    </figure>

    <div v-if="e.destination_sample?.length" class="flex flex-col gap-1">
      <span class="text-muted text-xs">{{ t('findings.evidence.sample') }}</span>
      <p class="flex flex-wrap gap-x-3 gap-y-1 font-mono text-sm">
        <span v-for="ip in e.destination_sample" :key="ip">{{ ip }}</span>
      </p>
    </div>

    <ul v-if="e.reputation_sources?.length" class="flex flex-col gap-2">
      <li
        v-for="r in e.reputation_sources"
        :key="r.source + r.indicator"
        class="bg-muted rounded-md p-3 text-sm"
      >
        <p class="text-highlighted font-mono">{{ r.indicator }}</p>
        <p class="text-muted">
          {{
            t('findings.evidence.listedIn', {
              source: r.source,
              date: d(new Date(r.listed_at), 'short'),
            })
          }}
          <template v-if="r.category"> · {{ r.category }}</template>
          · {{ r.responded ? t('findings.evidence.responded') : t('findings.evidence.synOnly') }}
        </p>
      </li>
    </ul>
  </div>
</template>
