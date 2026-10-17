<script setup lang="ts">
import type { CustomerDetail, CustomerScoring } from '~~/types/api'

/**
 * Tipo sugerido por el scoring residencial/comercial (I2, docs/api.md §2.9, frontend.md §8.1):
 * nivel, puntuación y confianza, razones con los números reales y la tabla de características
 * (`GET /customers/{id}/scoring`). Con el tipo fijado a mano se muestra igualmente (el scoring
 * sugiere pero no cambia). Confirmar/descartar reutiliza `set-kind` (permiso
 * `customers.kind.write`) y deja el tipo bloqueado.
 */
const props = defineProps<{ customer: CustomerDetail }>()
const emit = defineEmits<{ saved: [customer: CustomerDetail]; stale: [] }>()

const { t, d } = useI18n()
const { $api } = useNuxtApp()
const can = useCan()
const { decide, busy } = useKindDecision()

const {
  data: scoring,
  error,
  refresh,
} = useTenantQuery(
  () => `customer:${props.customer.id}:scoring`,
  () =>
    unwrap(
      $api.GET('/customers/{customer_id}/scoring', {
        params: { path: { customer_id: props.customer.id }, query: { range: '90d' } },
      }),
    ),
)
defineExpose({ refresh })

const latest = computed<CustomerScoring | null>(() => scoring.value?.latest ?? null)
const series = computed(() => scoring.value?.series ?? [])

const LEVEL = {
  commercial: { color: 'info', icon: 'i-lucide-building-2' },
  suspected: { color: 'warning', icon: 'i-lucide-building' },
  residential: { color: 'neutral', icon: 'i-lucide-house' },
} as const
const level = computed(() => LEVEL[latest.value?.level ?? 'residential'])
const reasons = computed(() => topReasons(latest.value?.reasons, 5))
const featureRows = computed(() =>
  [...(latest.value?.features ?? [])].sort(
    (a, b) =>
      SCORING_FEATURES.indexOf(a.code as (typeof SCORING_FEATURES)[number]) -
      SCORING_FEATURES.indexOf(b.code as (typeof SCORING_FEATURES)[number]),
  ),
)
const pendingDays = computed(() =>
  latest.value?.pending_since
    ? Math.max(
        0,
        Math.floor((Date.now() - new Date(latest.value.pending_since).getTime()) / 86_400_000),
      )
    : 0,
)
const seriesMax = computed(() =>
  series.value.length ? Math.max(...series.value.map((p) => p.commercial_score)) : null,
)
const canDecide = computed(
  () =>
    can('customers.kind.write') &&
    !props.customer.kind_locked &&
    !!latest.value &&
    latest.value.level !== 'residential',
)
const featureName = (code: string) => t(`clients.scoring.featureNames.${code}`)

async function onDecide(decision: 'confirm' | 'discard') {
  const updated = await decide(props.customer, decision)
  if (updated) emit('saved', updated)
  else emit('stale')
}
</script>

<template>
  <UCard data-testid="kind-suggestion">
    <template #header>
      <div class="flex flex-wrap items-center justify-between gap-2">
        <h3 class="text-highlighted font-semibold">{{ t('clients.scoring.title') }}</h3>
        <div v-if="canDecide" class="flex flex-wrap gap-2">
          <UButton
            icon="i-lucide-building-2"
            :label="t('clients.scoring.confirm')"
            :loading="busy === customer.id"
            data-testid="suggestion-confirm"
            @click="onDecide('confirm')"
          />
          <UButton
            color="neutral"
            variant="outline"
            icon="i-lucide-house"
            :label="t('clients.scoring.discard')"
            :disabled="busy === customer.id"
            data-testid="suggestion-discard"
            @click="onDecide('discard')"
          />
        </div>
      </div>
    </template>

    <DegradedNotice v-if="error" :error="error" @retry="refresh()" />
    <p v-else-if="scoring && !latest" class="text-muted text-sm" data-testid="suggestion-none">
      {{ t('clients.scoring.none') }}
    </p>
    <div v-else-if="latest" class="flex flex-col gap-4">
      <div class="flex flex-wrap items-center gap-x-4 gap-y-1">
        <UBadge
          :color="level.color"
          variant="subtle"
          :icon="level.icon"
          :label="t(`clients.scoring.level.${latest.level}`)"
          data-testid="suggestion-level"
        />
        <span class="text-highlighted text-sm font-medium tabular" data-testid="suggestion-score">
          {{ t('clients.scoring.score', { n: latest.commercial_score }) }}
        </span>
        <span class="text-muted text-sm tabular">
          · {{ t('clients.scoring.confidence', { pct: formatPercent(latest.confidence) }) }}
        </span>
        <span class="text-muted text-xs">
          {{
            t('clients.scoring.thresholds', {
              commercial: latest.thresholds.commercial,
              suspected: latest.thresholds.suspected,
            })
          }}
        </span>
      </div>

      <ul class="flex flex-col gap-1.5 text-sm">
        <li v-if="customer.kind_locked" class="text-muted flex gap-1.5">
          <UIcon name="i-lucide-lock" class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
          {{ t('clients.scoring.locked') }}
        </li>
        <li v-if="latest.pending_level && latest.pending_since" class="text-muted flex gap-1.5">
          <UIcon name="i-lucide-hourglass" class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
          {{
            t('clients.scoring.pending', {
              level: t(`clients.scoring.level.${latest.pending_level}`),
              date: d(new Date(latest.pending_since), 'short'),
              days: pendingDays,
            })
          }}
        </li>
        <li v-if="latest.dynamic_pool" class="text-muted flex gap-1.5">
          <UIcon name="i-lucide-shuffle" class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
          {{ t('clients.scoring.dynamicPool') }}
        </li>
      </ul>

      <div v-if="reasons.length">
        <h4 class="text-highlighted text-sm font-semibold">{{ t('clients.scoring.why') }}</h4>
        <ul class="mt-1.5 space-y-1.5" data-testid="suggestion-reasons">
          <li v-for="r in reasons" :key="r.code" class="flex gap-2 text-sm">
            <UIcon
              name="i-lucide-dot"
              class="text-muted mt-0.5 size-4 shrink-0"
              aria-hidden="true"
            />
            <span>{{ r.detail }}</span>
          </li>
        </ul>
      </div>

      <!-- Móvil: lista; desde sm: tabla (sin desbordamiento a 320 px). -->
      <div class="sm:hidden" data-testid="suggestion-features-list">
        <h4 class="text-highlighted text-sm font-semibold">{{ t('clients.scoring.features') }}</h4>
        <dl class="mt-1.5 flex flex-col gap-2 text-sm">
          <div v-for="f in featureRows" :key="f.code" class="border-default border-t pt-2">
            <dt class="text-default flex justify-between gap-2 font-medium">
              <span>{{ featureName(f.code) }}</span>
              <span class="text-muted shrink-0 tabular"
                >{{ Math.round(f.contribution * 100) }} · {{ formatPercent(f.weight) }}</span
              >
            </dt>
            <dd class="text-muted">
              {{ f.available ? f.detail : t('clients.scoring.unavailable') }}
            </dd>
          </div>
        </dl>
      </div>
      <div class="hidden sm:block">
        <table class="w-full text-sm" data-testid="suggestion-features">
          <caption class="text-highlighted mb-1.5 text-start font-semibold">
            {{
              t('clients.scoring.features')
            }}
          </caption>
          <thead class="text-muted text-xs">
            <tr>
              <th scope="col" class="py-1 pe-3 text-start font-medium">
                {{ t('clients.scoring.feature') }}
              </th>
              <th scope="col" class="py-1 pe-3 text-start font-medium">
                {{ t('clients.scoring.value') }}
              </th>
              <th scope="col" class="py-1 pe-3 text-end font-medium">
                {{ t('clients.scoring.weight') }}
              </th>
              <th scope="col" class="py-1 text-end font-medium">
                {{ t('clients.scoring.contribution') }}
              </th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="f in featureRows" :key="f.code" class="border-default border-t">
              <th scope="row" class="text-default py-1.5 pe-3 text-start font-medium">
                {{ featureName(f.code) }}
              </th>
              <td class="text-muted py-1.5 pe-3">
                {{ f.available ? f.detail : t('clients.scoring.unavailable') }}
              </td>
              <td class="py-1.5 pe-3 text-end tabular">{{ formatPercent(f.weight) }}</td>
              <td class="py-1.5 text-end tabular">
                <span class="inline-flex items-center justify-end gap-2">
                  <span
                    class="bg-elevated hidden h-1.5 w-16 overflow-hidden rounded-full sm:inline-block"
                    aria-hidden="true"
                  >
                    <span
                      class="bg-primary block h-full rounded-full"
                      :style="{ width: `${Math.round(Math.min(1, f.contribution / 0.3) * 100)}%` }"
                    />
                  </span>
                  {{ Math.round(f.contribution * 100) }}
                </span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <p v-if="seriesMax !== null" class="text-muted text-xs" data-testid="suggestion-series">
        {{ t('clients.scoring.series') }}:
        {{ t('clients.scoring.seriesSummary', { now: latest.commercial_score, max: seriesMax }) }}
      </p>
      <p class="text-dimmed text-xs">
        {{
          t('clients.scoring.window', {
            from: d(new Date(latest.window_from), 'short'),
            to: d(new Date(latest.window_to), 'short'),
            model: latest.model_version,
          })
        }}
      </p>
    </div>
    <LoadingState v-else :rows="3" />
  </UCard>
</template>
