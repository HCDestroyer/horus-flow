<script setup lang="ts">
import type { FindingFeedRow } from '../shapes'
import { maxListRows, type WidgetViewProps } from '../props'

/**
 * `findings_feed` — ¿Qué hallazgos nuevos o activos hay? Severidad (icono + texto), resumen
 * legible del comportamiento observado, cliente, nodo, estado de seguridad con confianza
 * (D18: "Infectado" nunca sin su confianza) y hace cuánto. En mural, una línea por fila.
 */
const props = defineProps<WidgetViewProps>()
const { t } = useI18n()
const now = useNow()

const rows = computed(() =>
  props.data?.kind === 'table'
    ? (props.data.rows as unknown as FindingFeedRow[]).slice(
        0,
        maxListRows(props.scale, props.rows),
      )
    : [],
)
const wide = computed(() => props.widget.position.w >= 12)

function ago(iso: string) {
  const p = agoParts((now.value - new Date(iso).getTime()) / 1000)
  return t(p.key, { n: p.n })
}
</script>

<template>
  <div class="@container flex min-h-0 flex-1 flex-col gap-[calc(var(--w-gap)*0.5)] overflow-hidden">
    <ul
      v-fit-rows
      class="divide-default flex min-h-0 flex-1 flex-col divide-y overflow-hidden"
      :aria-label="t('widgets.findings_feed.listLabel')"
    >
      <li
        v-for="row in rows"
        :key="row.id"
        data-fit-item
        class="w-label min-w-0 py-[0.35em] first:pt-0"
        data-testid="finding-row"
      >
        <div class="flex min-w-0 items-baseline gap-2">
          <SeverityBadge :severity="row.severity" class="w-[5.8em] shrink-0" />
          <span class="text-highlighted min-w-0 flex-1 truncate">{{ row.summary }}</span>
          <!-- Una línea solo si el widget es ancho de verdad (≥ 48 rem); si no, segunda línea. -->
          <span
            v-if="wide"
            class="min-w-0 shrink-0 items-baseline gap-2"
            :class="scale === 'normal' ? 'hidden @3xl:flex' : 'flex'"
          >
            <span class="text-muted truncate" :class="{ 'font-mono': !row.alias }">{{
              row.alias ?? row.customer_ip
            }}</span>
            <SecurityStateBadge
              :state="row.security_state"
              :confidence="row.confidence"
              class="w-meta"
            />
          </span>
          <time class="w-meta text-muted shrink-0 tabular" :datetime="row.last_seen_at">{{
            ago(row.last_seen_at)
          }}</time>
        </div>
        <p
          v-if="scale === 'normal'"
          class="w-meta text-muted mt-0.5 flex min-w-0 flex-wrap items-baseline gap-x-2"
          :class="{ '@3xl:hidden': wide }"
        >
          <span class="font-mono">{{
            row.alias ? `${row.alias} · ${row.customer_ip}` : row.customer_ip
          }}</span>
          <span>{{ row.site }}</span>
          <SecurityStateBadge :state="row.security_state" :confidence="row.confidence" />
        </p>
      </li>
    </ul>
    <p
      v-if="meta?.masked_personal_data && scale === 'normal'"
      class="w-meta text-muted mt-auto flex items-center gap-1"
    >
      <UIcon name="i-lucide-eye-off" class="size-[1.1em]" aria-hidden="true" />
      {{ t('widgets.common.masked') }}
    </p>
  </div>
</template>
